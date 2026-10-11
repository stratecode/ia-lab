package llmtelemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestStreamPreservesBytesAndMeasuresRealFirstToken(t *testing.T) {
	role := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n"
	token := "data: {\"choices\":[{\"delta\":{\"content\":\"Hola\"}}]}\n\n"
	done := "data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private" {
			t.Error("authorization lost")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "{\"stream\":true}" {
			t.Error("body changed")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, role)
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
		// Split a JSON event across transport reads.
		io.WriteString(w, token[:15])
		w.(http.Flusher).Flush()
		io.WriteString(w, token[15:])
		w.(http.Flusher).Flush()
		time.Sleep(60 * time.Millisecond)
		io.WriteString(w, done)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	reg := prometheus.NewRegistry()
	proxy := New(target, "test-model", reg)
	server := httptest.NewServer(proxy)
	defer server.Close()
	req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader("{\"stream\":true}"))
	req.Header.Set("Authorization", "Bearer private")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	b := make([]byte, len(role))
	if _, err := io.ReadFull(res.Body, b); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("role buffered until content")
	}
	rest, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b)+string(rest) != role+token+done {
		t.Fatal("stream changed")
	}
	families, _ := reg.Gather()
	var ttft, total float64
	for _, f := range families {
		switch f.GetName() {
		case "lab_llm_ttft_seconds":
			ttft = f.Metric[0].Histogram.GetSampleSum()
		case "lab_llm_request_duration_seconds":
			total = f.Metric[0].Histogram.GetSampleSum()
		}
	}
	if ttft < 0.05 || total < ttft+0.05 {
		t.Fatalf("wrong timing ttft=%f total=%f", ttft, total)
	}
	if v := value(proxy.requests.WithLabelValues("test-model", "chat", "200", "ok")); v != 1 {
		t.Fatalf("count=%f", v)
	}
	if value(proxy.active) != 0 {
		t.Fatal("active leaked")
	}
}

func TestHTTPErrorAndNonStreamingHaveNoFakeTTFT(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"private"}`)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	reg := prometheus.NewRegistry()
	proxy := New(target, "test-model", reg)
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}")))
	if recorder.Code != 401 || recorder.Body.String() != `{"error":"private"}` {
		t.Fatal("error changed")
	}
	if value(proxy.requests.WithLabelValues("test-model", "chat", "401", "http_error")) != 1 {
		t.Fatal("error not counted")
	}
	families, _ := reg.Gather()
	for _, f := range families {
		if f.GetName() == "lab_llm_ttft_seconds" {
			t.Fatal("invented TTFT")
		}
	}
}

func TestObserverToolsAndStreamErrors(t *testing.T) {
	for _, content := range []string{`{"choices":[{"delta":{"tool_calls":[{"function":{"name":"status"}}]}}]}`, `{"choices":[{"delta":{"reasoning_content":"check"}}]}`} {
		o := &streamObserver{start: time.Now()}
		o.consume([]byte("data: " + content + "\n\n"))
		if o.first == 0 {
			t.Fatal("tool/reasoning first output ignored")
		}
	}
	o := &streamObserver{start: time.Now()}
	o.consume([]byte("data: {\"error\":{\"message\":\"failed\"}}\n\n"))
	if !o.failed {
		t.Fatal("stream error ignored")
	}
}

func value(m prometheus.Metric) float64 {
	var d dto.Metric
	_ = m.Write(&d)
	if d.Counter != nil {
		return d.Counter.GetValue()
	}
	return d.Gauge.GetValue()
}

func TestTruncatedSSEIsCountedAsError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hola\"}}]}\n\n")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	reg := prometheus.NewRegistry()
	proxy := New(target, "test-model", reg)
	proxy.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}")))
	if value(proxy.requests.WithLabelValues("test-model", "chat", "200", "stream_error")) != 1 {
		t.Fatal("truncated stream counted as success")
	}
}

func TestCancellationClearsActiveAndCountsCancelled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	reg := prometheus.NewRegistry()
	proxy := New(target, "test-model", reg)
	server := httptest.NewServer(proxy)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader("{}"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	res.Body.Close()
	deadline := time.Now().Add(time.Second)
	for value(proxy.requests.WithLabelValues("test-model", "chat", "499", "cancelled")) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if value(proxy.active) != 0 || value(proxy.requests.WithLabelValues("test-model", "chat", "499", "cancelled")) != 1 {
		t.Fatal("cancelled request leaked or was not counted")
	}
}

func TestEmptyToolMetadataIsNotFirstToken(t *testing.T) {
	o := &streamObserver{start: time.Now()}
	o.consume([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"\",\"arguments\":\"\"}}]}}]}\n\n"))
	if o.first != 0 {
		t.Fatal("empty tool metadata counted as first token")
	}
}

func TestKnownHTTPErrorWinsOverClientClosingErrorBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	reg := prometheus.NewRegistry()
	proxy := New(target, "test-model", reg)
	server := httptest.NewServer(proxy)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader("{}"))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 401 {
		t.Fatal("status lost")
	}
	cancel()
	res.Body.Close()
	deadline := time.Now().Add(time.Second)
	for value(proxy.requests.WithLabelValues("test-model", "chat", "401", "http_error")) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if value(proxy.requests.WithLabelValues("test-model", "chat", "401", "http_error")) != 1 {
		t.Fatal("HTTP error mislabeled as cancellation")
	}
}
