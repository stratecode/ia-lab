package cognitivegateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

var (
	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cognitive_gateway_requests_total",
		Help: "Total cognitive gateway requests.",
	}, []string{"mode", "backend", "stream", "status"})
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cognitive_gateway_request_duration_seconds",
		Help:    "Cognitive gateway request latency.",
		Buckets: prometheus.DefBuckets,
	}, []string{"mode", "backend"})
	jsonValidationTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cognitive_gateway_json_validation_total",
		Help: "JSON response validation results.",
	}, []string{"mode", "backend", "status"})
	contextBuilderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cognitive_gateway_context_builder_total",
		Help: "Context builder integration results.",
	}, []string{"mode", "status"})
	irCompileTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cognitive_gateway_ir_compile_total",
		Help: "Operational IR compile and injection results.",
	}, []string{"mode", "status"})
	irChars = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cognitive_gateway_ir_chars",
		Help:    "Operational IR character size injected by the gateway.",
		Buckets: []float64{256, 512, 1024, 2048, 4096, 6000, 8000, 12000},
	}, []string{"mode"})
)

type Server struct {
	cfg    Config
	client *http.Client
}

func NewServer(cfg Config) *Server {
	return &Server{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout()},
	}
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)
	r.Handle("/metrics", promhttp.Handler())
	r.Route("/v1", func(r chi.Router) {
		r.Get("/models", s.models)
		r.Post("/chat/completions", s.chatCompletions)
	})
	return r
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "cognitive-gateway"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	status := http.StatusOK
	checks := map[string]string{}
	for name, backend := range s.cfg.Backends {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(backend.BaseURL, "/")+"/models", nil)
		if backend.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+backend.APIKey)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			checks[name] = err.Error()
			status = http.StatusServiceUnavailable
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			checks[name] = fmt.Sprintf("status %d", resp.StatusCode)
			status = http.StatusServiceUnavailable
			continue
		}
		checks[name] = "ok"
	}
	writeJSON(w, status, map[string]any{"ready": status == http.StatusOK, "backends": checks})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	type modelResponse struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	out := modelResponse{Object: "list", Data: []map[string]any{}}
	seen := map[string]bool{}
	for name, backend := range s.cfg.Backends {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(backend.BaseURL, "/")+"/models", nil)
		if err != nil {
			continue
		}
		if backend.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+backend.APIKey)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			continue
		}
		var parsed modelResponse
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&parsed)
		}
		_ = resp.Body.Close()
		if len(parsed.Data) == 0 && backend.Model != "" {
			parsed.Data = []map[string]any{{"id": backend.Model, "object": "model", "owned_by": "cognitive-gateway"}}
		}
		for _, model := range parsed.Data {
			id, _ := model["id"].(string)
			if id == "" {
				id = name
				model["id"] = id
			}
			if !seen[id] {
				out.Data = append(out.Data, model)
				seen[id] = true
			}
		}
	}
	if len(out.Data) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "no backend models available"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.IsAcceptedAPIKey(r.Header.Get("Authorization")) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "invalid API key"})
		return
	}
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	var reqBody ChatCompletionRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 32<<20))
	if err := decoder.Decode(&reqBody); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "invalid JSON body"})
		return
	}
	start := time.Now()
	initialTokens := estimateTokens(reqBody.Messages)
	state, err := prepareRequest(r.Context(), s.cfg, reqBody, requestID, r.Header)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
		return
	}
	state.InitialTokens = initialTokens
	status := "success"
	defer func() {
		requestsTotal.WithLabelValues(string(state.Mode), state.BackendName, fmt.Sprintf("%v", state.Streaming), status).Inc()
		requestDuration.WithLabelValues(string(state.Mode), state.BackendName).Observe(time.Since(start).Seconds())
	}()
	raw, err := json.Marshal(state.Request)
	if err != nil {
		status = "error"
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	backendURL := strings.TrimRight(state.Backend.BaseURL, "/") + "/chat/completions"
	upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost, backendURL, bytes.NewReader(raw))
	if err != nil {
		status = "error"
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Request-ID", requestID)
	if state.Backend.APIKey != "" {
		upstream.Header.Set("Authorization", "Bearer "+state.Backend.APIKey)
	}
	if state.Streaming {
		statusCode := s.proxyStreaming(w, upstream, state, start)
		if statusCode < 200 || statusCode >= 300 {
			status = "upstream_error"
		}
		log.Info().Str("request_id", requestID).Str("mode", string(state.Mode)).Str("backend", state.BackendName).Bool("stream", true).Int("estimated_tokens", state.Estimated).Msg("gateway request proxied")
		return
	}
	modelStart := time.Now()
	resp, err := s.client.Do(upstream)
	modelDuration := time.Since(modelStart)
	if err != nil {
		status = "error"
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		status = "error"
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
		return
	}
	if shouldValidateJSON(state.Request) {
		if err := validateJSONContent(payload); err != nil {
			jsonValidationTotal.WithLabelValues(string(state.Mode), state.BackendName, "invalid").Inc()
			log.Warn().Err(err).Str("request_id", requestID).Str("mode", string(state.Mode)).Msg("gateway JSON validation failed")
			if !s.cfg.JSONValidation.WarnOnly || (state.Mode == ModePlan && s.cfg.OperationalIR.PlanJSONataRequired) {
				status = "json_invalid"
				writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
				return
			}
		} else {
			jsonValidationTotal.WithLabelValues(string(state.Mode), state.BackendName, "valid").Inc()
		}
	}
	footer := buildFooter(state, modelDuration, responseTokenEstimate(payload), time.Since(start))
	if s.cfg.ResponseFooter.Enabled {
		var err error
		if shouldValidateJSON(state.Request) {
			payload, err = addGatewayMetadata(payload, footer)
		} else {
			payload, err = appendFooterToContent(payload, footer)
		}
		if err != nil {
			log.Warn().Err(err).Str("request_id", requestID).Msg("failed to attach gateway footer")
		}
	}
	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("X-Cognitive-Gateway-Metrics", footer)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(payload)
	log.Info().Str("request_id", requestID).Str("mode", string(state.Mode)).Str("backend", state.BackendName).Bool("stream", false).Int("estimated_tokens", state.Estimated).Int("status", resp.StatusCode).Msg("gateway request proxied")
}

func (s *Server) proxyRaw(w http.ResponseWriter, req *http.Request, contentType string) {
	resp, err := s.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *Server) proxyStreaming(w http.ResponseWriter, req *http.Request, state *contextState, start time.Time) int {
	modelStart := time.Now()
	resp, err := s.client.Do(req)
	modelDuration := time.Since(modelStart)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"detail": err.Error()})
		return http.StatusBadGateway
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Add("Trailer", "X-Cognitive-Gateway-Metrics")
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(w, resp.Body)
		return resp.StatusCode
	}
	flusher, _ := w.(http.Flusher)
	responseBytes := 0
	reader := bufio.NewReaderSize(io.LimitReader(resp.Body, 64<<20), 32*1024)
	for {
		rawLine, err := reader.ReadString('\n')
		if rawLine != "" {
			line := strings.TrimSuffix(rawLine, "\n")
			if strings.TrimSpace(line) == "data: [DONE]" && s.cfg.ResponseFooter.Enabled && !shouldValidateJSON(state.Request) {
				footer := buildFooter(state, modelDuration, (responseBytes+3)/4, time.Since(start))
				w.Header().Set("X-Cognitive-Gateway-Metrics", footer)
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", "\n\n"+footer)
			}
			responseBytes += len(line)
			_, _ = w.Write([]byte(rawLine))
			if line == "" && flusher != nil {
				flusher.Flush()
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		if s.cfg.ResponseFooter.Enabled && !shouldValidateJSON(state.Request) {
			footer := buildFooter(state, modelDuration, (responseBytes+3)/4, time.Since(start))
			w.Header().Set("X-Cognitive-Gateway-Metrics", footer)
			if flusher != nil {
				flusher.Flush()
			}
		}
		return http.StatusBadGateway
	}
	return resp.StatusCode
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func shouldValidateJSON(req ChatCompletionRequest) bool {
	if req.Stream || req.ResponseFormat == nil {
		return false
	}
	value, _ := req.ResponseFormat["type"].(string)
	return value == "json_object"
}

func validateJSONContent(payload []byte) error {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return err
	}
	if len(parsed.Choices) == 0 {
		return fmt.Errorf("response has no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return fmt.Errorf("response content is empty")
	}
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return fmt.Errorf("response content is not JSON: %w", err)
	}
	return nil
}

func Run(ctx context.Context, cfg Config) error {
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           NewServer(cfg).Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Info().Str("addr", cfg.ListenAddr).Str("default_mode", string(cfg.DefaultMode)).Msg("starting cognitive gateway")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}
