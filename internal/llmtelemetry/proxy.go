// Package llmtelemetry transparently forwards llama.cpp traffic and exports
// bounded aggregate metrics. Request/response content is never logged or stored.
package llmtelemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Proxy struct {
	target          *url.URL
	model           string
	requests        *prometheus.CounterVec
	durations, ttft *prometheus.HistogramVec
	active          prometheus.Gauge
	metrics         http.Handler
}

func New(target *url.URL, model string, registry *prometheus.Registry) *Proxy {
	buckets := []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 900}
	p := &Proxy{target: target, model: model,
		requests:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "lab_llm_requests_total", Help: "Completed inference calls, including errors and cancellations."}, []string{"model", "endpoint", "status", "outcome"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "lab_llm_request_duration_seconds", Help: "Inference duration through end of response body, including queue and failed calls.", Buckets: buckets}, []string{"model", "endpoint", "outcome"}),
		ttft:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "lab_llm_ttft_seconds", Help: "Streaming time to first nonempty content, reasoning or tool output; excludes role-only events.", Buckets: buckets}, []string{"model", "endpoint"}),
		active:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "lab_llm_requests_in_flight", Help: "Inference calls currently forwarding."}),
	}
	registry.MustRegister(p.requests, p.durations, p.ttft, p.active)
	p.metrics = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/metrics" {
		p.metrics.ServeHTTP(w, r)
		return
	}
	endpoint := ""
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/v1/chat/completions":
			endpoint = "chat"
		case "/v1/completions":
			endpoint = "completion"
		}
	}
	proxy := httputil.NewSingleHostReverseProxy(p.target)
	proxy.FlushInterval = -1 // Forward each upstream read immediately, including SSE role events.
	// Upstream errors may contain credentials in URLs: return a fixed public error.
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "model upstream unavailable", http.StatusBadGateway)
	}
	if endpoint == "" {
		proxy.ServeHTTP(w, r)
		return
	}
	start := time.Now()
	status := http.StatusBadGateway
	received := false
	outcome := "ok"
	observer := &streamObserver{start: start}
	p.active.Inc()
	defer func() {
		p.active.Dec()
		if received && status >= 400 {
			outcome = "http_error"
		} else if r.Context().Err() != nil && !observer.complete && !observer.done {
			status = 499
			outcome = "cancelled"
		} else if status >= 400 {
			outcome = "http_error"
		} else if observer.failed {
			outcome = "stream_error"
		}
		p.requests.WithLabelValues(p.model, endpoint, strconv.Itoa(status), outcome).Inc()
		p.durations.WithLabelValues(p.model, endpoint, outcome).Observe(time.Since(start).Seconds())
		if observer.first > 0 {
			p.ttft.WithLabelValues(p.model, endpoint).Observe(observer.first)
		}
	}()
	proxy.ModifyResponse = func(res *http.Response) error {
		received = true
		status = res.StatusCode
		res.Body = &observedBody{ReadCloser: res.Body, observer: observer, sse: strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream")}
		return nil
	}
	proxy.ServeHTTP(w, r)
}

type observedBody struct {
	io.ReadCloser
	observer *streamObserver
	sse      bool
}

func (b *observedBody) Read(buf []byte) (int, error) {
	n, err := b.ReadCloser.Read(buf)
	if err == io.EOF {
		b.observer.complete = true
	}
	if b.sse && n > 0 {
		b.observer.consume(buf[:n])
	}
	if (err == io.EOF && b.sse && !b.observer.done) || (err != nil && err != io.EOF) {
		b.observer.failed = true
	}
	return n, err
}

type streamObserver struct {
	start    time.Time
	first    float64
	failed   bool
	done     bool
	complete bool
	pending  []byte
	skipping bool
}

// Parse only complete SSE data lines, tolerating arbitrary transport boundaries.
// Bound transient memory even if an upstream emits an oversized data event.
func (o *streamObserver) consume(chunk []byte) {
	for len(chunk) > 0 {
		pos := bytes.IndexByte(chunk, '\n')
		line := chunk
		if pos >= 0 {
			line = chunk[:pos]
		}
		if !o.skipping {
			if len(o.pending)+len(line) > 65536 {
				o.pending = nil
				o.skipping = true
			} else {
				o.pending = append(o.pending, line...)
			}
		}
		if pos < 0 {
			return
		}
		if !o.skipping {
			o.event(o.pending)
		}
		o.pending = nil
		o.skipping = false
		chunk = chunk[pos+1:]
	}
}
func (o *streamObserver) event(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(line[5:])
	if bytes.Equal(payload, []byte("[DONE]")) {
		o.done = true
		return
	}
	var d struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Text  string `json:"text"`
			Delta struct {
				Content   string            `json:"content"`
				Reasoning string            `json:"reasoning_content"`
				Tools     []json.RawMessage `json:"tool_calls"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &d) != nil {
		return
	}
	if len(d.Error) > 0 && string(d.Error) != "null" {
		o.failed = true
	}
	if o.first > 0 {
		return
	}
	for _, c := range d.Choices {
		if c.Text != "" || c.Delta.Content != "" || c.Delta.Reasoning != "" || meaningfulTool(c.Delta.Tools) {
			o.first = time.Since(o.start).Seconds()
			return
		}
	}
}

func meaningfulTool(tools []json.RawMessage) bool {
	for _, raw := range tools {
		var tool struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if json.Unmarshal(raw, &tool) == nil && (tool.Function.Name != "" || tool.Function.Arguments != "") {
			return true
		}
	}
	return false
}
