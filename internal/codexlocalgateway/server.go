package codexlocalgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

type Server struct {
	cfg         Config
	logger      zerolog.Logger
	httpClient  *http.Client
	limiter     *rateLimiter
	registry    *prometheus.Registry
	requests    *prometheus.CounterVec
	durations   *prometheus.HistogramVec
	upDurations *prometheus.HistogramVec
	upErrors    *prometheus.CounterVec
	tokens      *prometheus.CounterVec
	active      prometheus.Gauge
	streams     *prometheus.CounterVec
}

func NewServer(cfg Config, logger zerolog.Logger) http.Handler {
	cfg = cfg.normalized()
	s := &Server{
		cfg:    cfg,
		logger: logger,
		httpClient: &http.Client{
			Timeout: cfg.RequestTimeout,
		},
		limiter:  newRateLimiter(cfg.RateLimitRPM, cfg.RateLimitBurst),
		registry: prometheus.NewRegistry(),
	}
	s.initMetrics()

	r := chi.NewRouter()
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)
	r.Handle("/metrics", promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{}))

	r.Group(func(r chi.Router) {
		r.Use(s.instrument)
		r.Use(s.authenticate)
		r.Use(s.limit)
		r.Get("/v1/models", s.models)
		r.Post("/v1/chat/completions", s.chatCompletions)
		r.Post("/v1/responses", s.responses)
	})

	return r
}

func (s *Server) initMetrics() {
	s.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_requests_total",
		Help: "Total gateway requests.",
	}, []string{"endpoint", "method", "status"})
	s.durations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "codex_gateway_request_duration_seconds",
		Help:    "Gateway request duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint", "method"})
	s.upDurations = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "codex_gateway_upstream_duration_seconds",
		Help:    "Upstream llama.cpp request duration.",
		Buckets: prometheus.DefBuckets,
	}, []string{"endpoint"})
	s.upErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_upstream_errors_total",
		Help: "Total upstream errors.",
	}, []string{"endpoint", "status"})
	s.tokens = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_tokens_total",
		Help: "Tokens reported by upstream usage.",
	}, []string{"type"})
	s.active = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "codex_gateway_active_requests",
		Help: "Active gateway requests.",
	})
	s.streams = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_streams_total",
		Help: "Total response streams.",
	}, []string{"status"})

	s.registry.MustRegister(s.requests, s.durations, s.upDurations, s.upErrors, s.tokens, s.active, s.streams)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{
				"id":       s.cfg.Model,
				"object":   "model",
				"created":  0,
				"owned_by": "local",
			},
		},
	})
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := readLimitedBody(w, r, s.cfg.MaxBodyBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	body = remapModel(body, s.cfg.Model, s.cfg.UpstreamModel)
	status, contentType, upstreamBody, err := s.callUpstream(r.Context(), "chat_completions", body)
	if err != nil {
		writeError(w, status, "upstream_error", err.Error())
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(upstreamBody)
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	body, err := readLimitedBody(w, r, s.cfg.MaxBodyBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	chatReq, err := s.responsesToChat(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	chatReq.Stream = false
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to build upstream request")
		return
	}

	status, _, upstreamBody, err := s.callUpstream(r.Context(), "responses", chatBody)
	if err != nil {
		writeError(w, status, "upstream_error", err.Error())
		return
	}

	envelope, err := s.chatToResponses(upstreamBody)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_malformed_json", err.Error())
		return
	}

	if req.Stream {
		s.writeResponsesStream(w, envelope)
		return
	}
	writeJSON(w, http.StatusOK, envelope)
}

func (s *Server) responsesToChat(req responsesRequest) (chatCompletionRequest, error) {
	content, err := flattenInput(req.Input)
	if err != nil {
		return chatCompletionRequest{}, err
	}
	if strings.TrimSpace(content) == "" {
		return chatCompletionRequest{}, errors.New("input must contain text")
	}

	messages := make([]chatMessage, 0, 2)
	if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.Instructions})
	}
	messages = append(messages, chatMessage{Role: "user", Content: content})

	return chatCompletionRequest{
		Model:       s.cfg.UpstreamModel,
		Messages:    messages,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}, nil
}

func (s *Server) chatToResponses(body []byte) (responsesEnvelope, error) {
	var chat chatCompletionResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return responsesEnvelope{}, fmt.Errorf("upstream did not return valid chat completion JSON: %w", err)
	}
	text := ""
	if len(chat.Choices) > 0 {
		text = chat.Choices[0].Message.Content
		if text == "" {
			text = chat.Choices[0].Delta.Content
		}
	}

	usage := responsesUsage{
		InputTokens:  chat.Usage.PromptTokens,
		OutputTokens: chat.Usage.CompletionTokens,
		TotalTokens:  chat.Usage.TotalTokens,
	}
	s.observeUsage(usage)

	now := time.Now().Unix()
	return responsesEnvelope{
		ID:        "resp_" + uuid.NewString(),
		Object:    "response",
		CreatedAt: now,
		Model:     s.cfg.Model,
		Status:    "completed",
		Output: []responseItem{
			{
				ID:   "msg_" + uuid.NewString(),
				Type: "message",
				Role: "assistant",
				Content: []responseContentPart{
					{Type: "output_text", Text: text},
				},
			},
		},
		OutputText: text,
		Usage:      usage,
	}, nil
}

func (s *Server) callUpstream(ctx context.Context, endpoint string, body []byte) (int, string, []byte, error) {
	start := time.Now()
	url := s.cfg.UpstreamBaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.UpstreamAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.UpstreamAPIKey)
	}

	res, err := s.httpClient.Do(req)
	s.upDurations.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
	if err != nil {
		s.upErrors.WithLabelValues(endpoint, "transport").Inc()
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout") {
			return http.StatusGatewayTimeout, "", nil, err
		}
		return http.StatusBadGateway, "", nil, err
	}
	defer res.Body.Close()

	upstreamBody, readErr := io.ReadAll(io.LimitReader(res.Body, s.cfg.MaxBodyBytes))
	if readErr != nil {
		s.upErrors.WithLabelValues(endpoint, "read").Inc()
		return http.StatusBadGateway, "", nil, readErr
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		s.upErrors.WithLabelValues(endpoint, fmt.Sprintf("%d", res.StatusCode)).Inc()
		return http.StatusBadGateway, "", nil, fmt.Errorf("upstream returned %d: %s", res.StatusCode, strings.TrimSpace(string(upstreamBody)))
	}

	contentType := res.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	return res.StatusCode, contentType, upstreamBody, nil
}

func (s *Server) writeResponsesStream(w http.ResponseWriter, envelope responsesEnvelope) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	s.streams.WithLabelValues("completed").Inc()
	writeSSE(w, "response.created", map[string]any{
		"type":        "response.created",
		"response_id": envelope.ID,
	})
	writeSSE(w, "response.output_text.delta", map[string]any{
		"type":  "response.output_text.delta",
		"delta": envelope.OutputText,
	})
	writeSSE(w, "response.completed", map[string]any{
		"type":     "response.completed",
		"response": envelope,
	})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeSSE(w http.ResponseWriter, event string, data any) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) observeUsage(usage responsesUsage) {
	if usage.InputTokens > 0 {
		s.tokens.WithLabelValues("prompt").Add(float64(usage.InputTokens))
	}
	if usage.OutputTokens > 0 {
		s.tokens.WithLabelValues("completion").Add(float64(usage.OutputTokens))
	}
	if usage.TotalTokens > 0 {
		s.tokens.WithLabelValues("total").Add(float64(usage.TotalTokens))
	}
}

func readLimitedBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, typ, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Type: typ, Message: message}})
}
