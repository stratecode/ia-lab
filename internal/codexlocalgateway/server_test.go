package codexlocalgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func newTestServer(t *testing.T, upstream http.Handler) (*httptest.Server, *httptest.Server) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	cfg := Config{
		Addr:            "127.0.0.1:0",
		APIKey:          "test-key",
		UpstreamBaseURL: upstreamServer.URL + "/v1",
		UpstreamAPIKey:  "upstream-key",
		Model:           "qwen-local-code",
		UpstreamModel:   "codex-local",
		RequestTimeout:  2 * time.Second,
		MaxBodyBytes:    1 << 20,
		RateLimitRPM:    60,
		RateLimitBurst:  10,
	}
	srv := httptest.NewServer(NewServer(cfg, zerolog.Nop()))
	t.Cleanup(srv.Close)
	return srv, upstreamServer
}

func authedRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestResponsesMapsStringInputAndInstructionsToChatCompletion(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Fatalf("unexpected upstream auth: %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"codex-local","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	}))

	payload := `{"model":"qwen-local-code","instructions":"Be brief.","input":"Write a Go test.","max_output_tokens":32,"temperature":0.2,"top_p":0.8}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	if upstreamBody["model"] != "codex-local" {
		t.Fatalf("expected upstream model remap, got %#v", upstreamBody["model"])
	}
	if upstreamBody["max_tokens"] != float64(32) {
		t.Fatalf("expected max_tokens propagation, got %#v", upstreamBody["max_tokens"])
	}
	messages := upstreamBody["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("expected system + user messages, got %#v", messages)
	}
	if messages[0].(map[string]any)["role"] != "system" || messages[0].(map[string]any)["content"] != "Be brief." {
		t.Fatalf("unexpected system message: %#v", messages[0])
	}
	if messages[1].(map[string]any)["role"] != "user" || messages[1].(map[string]any)["content"] != "Write a Go test." {
		t.Fatalf("unexpected user message: %#v", messages[1])
	}

	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out["object"] != "response" || out["status"] != "completed" || out["model"] != "qwen-local-code" {
		t.Fatalf("unexpected response envelope: %#v", out)
	}
	if out["output_text"] != "done" {
		t.Fatalf("expected output_text, got %#v", out["output_text"])
	}
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"] != float64(5) || usage["output_tokens"] != float64(2) || usage["total_tokens"] != float64(7) {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestResponsesFlattensArrayInput(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))

	payload := `{"model":"qwen-local-code","input":["first","second",{"role":"user","content":[{"type":"input_text","text":"third"}]}]}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	messages := upstreamBody["messages"].([]any)
	content := messages[len(messages)-1].(map[string]any)["content"]
	if content != "first\nsecond\nthird" {
		t.Fatalf("unexpected flattened content: %#v", content)
	}
}

func TestModelsAuthAndHealth(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("models should not hit upstream")
	}))

	health, err := http.Get(gateway.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}

	unauth, err := http.Get(gateway.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected auth requirement, got %d", unauth.StatusCode)
	}

	req := authedRequest(t, http.MethodGet, gateway.URL+"/v1/models", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d", res.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "qwen-local-code" {
		t.Fatalf("unexpected models response: %#v", out)
	}
}

func TestChatCompletionsPassthroughRemapsModel(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"content":"chat"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"qwen-local-code","messages":[{"role":"user","content":"hi"}]}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", res.StatusCode)
	}
	if upstreamBody["model"] != "codex-local" {
		t.Fatalf("expected model remap, got %#v", upstreamBody["model"])
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte(`"chat"`)) {
		t.Fatalf("unexpected passthrough body: %s", body)
	}
}

func TestUpstreamErrorMapping(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusBadGateway)
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte("upstream_error")) {
		t.Fatalf("expected upstream_error JSON, got %s", body)
	}
}

func TestMalformedUpstreamJSON(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not-json`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte("upstream_malformed_json")) {
		t.Fatalf("expected upstream_malformed_json JSON, got %s", body)
	}
}

func TestMetricsIncrement(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("request status = %d", res.StatusCode)
	}

	metrics, err := http.Get(gateway.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer metrics.Body.Close()
	body, _ := io.ReadAll(metrics.Body)
	for _, want := range []string{
		"codex_gateway_requests_total",
		"codex_gateway_request_duration_seconds",
		"codex_gateway_upstream_duration_seconds",
		`codex_gateway_tokens_total{type="prompt"} 3`,
		`codex_gateway_tokens_total{type="completion"} 4`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing metric %q in:\n%s", want, body)
		}
	}
}

func TestRateLimit(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	})
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	cfg := Config{
		APIKey:          "test-key",
		UpstreamBaseURL: upstreamServer.URL + "/v1",
		Model:           "qwen-local-code",
		UpstreamModel:   "codex-local",
		RequestTimeout:  time.Second,
		MaxBodyBytes:    1 << 20,
		RateLimitRPM:    1,
		RateLimitBurst:  1,
	}
	gateway := httptest.NewServer(NewServer(cfg, zerolog.Nop()))
	defer gateway.Close()

	req1 := authedRequest(t, http.MethodGet, gateway.URL+"/v1/models", nil)
	res1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	_ = res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("first request status = %d", res1.StatusCode)
	}

	req2 := authedRequest(t, http.MethodGet, gateway.URL+"/v1/models", nil)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()
	if res2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d", res2.StatusCode)
	}
}

func TestResponsesStreamEmitsSSE(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"streamed"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi","stream":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected SSE content type, got %q", got)
	}
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "event: response.completed", "streamed"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}
