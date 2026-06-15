package codexlocalgateway

import (
	"bufio"
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
	toolCalls   *prometheus.CounterVec
	toolOutputs prometheus.Counter
	parseErrors prometheus.Counter
	storeHits   *prometheus.CounterVec
	compactions prometheus.Counter
	sseFailures prometheus.Counter
	store       *responseStore
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
		store:    newResponseStore(cfg.ResponseStoreTTL, cfg.ResponseStoreMaxEntries),
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
	s.toolCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_tool_calls_total",
		Help: "Tool calls emitted by the Responses adapter.",
	}, []string{"source", "name"})
	s.toolOutputs = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "codex_gateway_tool_outputs_total",
		Help: "Function call outputs received from Codex.",
	})
	s.parseErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "codex_gateway_tool_parse_errors_total",
		Help: "Tool call fallback parse errors.",
	})
	s.storeHits = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "codex_gateway_response_store_total",
		Help: "Response store lookups.",
	}, []string{"result"})
	s.compactions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "codex_gateway_compactions_total",
		Help: "Transcript compactions performed before upstream calls.",
	})
	s.sseFailures = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "codex_gateway_sse_failures_total",
		Help: "SSE response failures.",
	})

	s.registry.MustRegister(s.requests, s.durations, s.upDurations, s.upErrors, s.tokens, s.active, s.streams, s.toolCalls, s.toolOutputs, s.parseErrors, s.storeHits, s.compactions, s.sseFailures)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	model := map[string]any{
		"id":                      s.cfg.Model,
		"slug":                    s.cfg.Model,
		"name":                    s.cfg.Model,
		"display_name":            s.cfg.Model,
		"object":                  "model",
		"created":                 0,
		"owned_by":                "local",
		"default_reasoning_level": "low",
		"supported_reasoning_levels": []map[string]string{
			{"effort": "minimal", "description": "Small local-model prompt budget"},
			{"effort": "low", "description": "Light local-model reasoning"},
		},
		"supports_reasoning_summaries":     false,
		"supports_parallel_tool_calls":     false,
		"description":                      "StrateCode lab local coding model served through llama.cpp.",
		"shell_type":                       "shell_command",
		"visibility":                       "list",
		"supported_in_api":                 true,
		"priority":                         1,
		"additional_speed_tiers":           []string{},
		"service_tiers":                    []map[string]any{},
		"availability_nux":                 nil,
		"upgrade":                          nil,
		"base_instructions":                "You are Codex running against a small local coding model through the StrateCode lab gateway. Keep responses concise and use this route for low-stakes tasks.",
		"model_messages":                   nil,
		"default_reasoning_summary":        "none",
		"support_verbosity":                false,
		"default_verbosity":                "low",
		"apply_patch_tool_type":            "freeform",
		"web_search_tool_type":             "text_and_image",
		"truncation_policy":                map[string]any{"mode": "tokens", "limit": s.cfg.AutoCompactTokenLimit},
		"supports_image_detail_original":   false,
		"context_window":                   s.cfg.ContextWindow,
		"max_context_window":               s.cfg.ContextWindow,
		"auto_compact_token_limit":         s.cfg.AutoCompactTokenLimit,
		"effective_context_window_percent": 80,
		"experimental_supported_tools":     []string{"function"},
		"supports_search_tool":             false,
		"use_responses_lite":               false,
		"auto_review_model_override":       nil,
		"tool_mode":                        "function_calling",
		"multi_agent_version":              nil,
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   []map[string]any{model},
		"models": []map[string]any{model},
	})
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := readLimitedBody(w, r, s.cfg.MaxBodyBytes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.enforcePromptSize(body); err != nil {
		writeError(w, http.StatusBadRequest, "context_window_exceeded", err.Error())
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
		if strings.Contains(err.Error(), "prompt text is too large") {
			writeError(w, http.StatusBadRequest, "context_window_exceeded", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	chatReq.Stream = req.Stream
	chatBody, err := json.Marshal(chatReq)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to build upstream request")
		return
	}
	if err := s.enforceChatPromptSize(chatReq); err != nil {
		writeError(w, http.StatusBadRequest, "context_window_exceeded", err.Error())
		return
	}

	if req.Stream {
		if err := s.writeResponsesStreamFromUpstream(w, r.Context(), chatBody, req, chatReq); err != nil {
			s.logger.Warn().Err(err).Msg("streaming response failed")
		}
		return
	}

	status, _, upstreamBody, err := s.callUpstream(r.Context(), "responses", chatBody)
	if err != nil {
		writeError(w, status, "upstream_error", err.Error())
		return
	}

	envelope, err := s.chatToResponses(upstreamBody, req, chatReq)
	if err != nil {
		var toolErr *toolCallParseError
		if errors.As(err, &toolErr) {
			s.parseErrors.Inc()
			writeError(w, http.StatusBadGateway, "tool_call_parse_error", toolErr.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "upstream_malformed_json", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, envelope)
}

func (s *Server) responsesToChat(req responsesRequest) (chatCompletionRequest, error) {
	inputMessages, err := inputToChatMessages(req.Input)
	if err != nil {
		return chatCompletionRequest{}, err
	}

	messages := make([]chatMessage, 0, len(inputMessages)+2)
	if req.PreviousResponse != "" {
		prior, ok := s.store.get(req.PreviousResponse)
		if !ok {
			s.storeHits.WithLabelValues("miss").Inc()
			return chatCompletionRequest{}, fmt.Errorf("previous_response_id %q was not found or expired", req.PreviousResponse)
		}
		s.storeHits.WithLabelValues("hit").Inc()
		messages = append(messages, prior...)
	} else if strings.TrimSpace(req.Instructions) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.Instructions})
	}
	if hasTools(req.Tools) {
		messages = append(messages, fallbackToolInstruction(req.Tools, req.ToolChoice))
	}
	for _, msg := range inputMessages {
		if msg.Role == "tool" {
			s.toolOutputs.Inc()
		}
		messages = append(messages, msg)
	}

	maxTokens := req.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = s.cfg.DefaultMaxOutputTokens
	}

	chatReq := chatCompletionRequest{
		Model:       s.cfg.UpstreamModel,
		Messages:    messages,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
	if err := s.compactChatPrompt(&chatReq); err != nil {
		return chatCompletionRequest{}, err
	}
	return chatReq, nil
}

func hasTools(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != "[]"
}

func fallbackToolInstruction(tools json.RawMessage, choice any) chatMessage {
	names := preferredFallbackToolNames(tools)
	chosen := requiredToolName(choice)
	if chosen != "" {
		names = []string{chosen}
	}
	nameLine := "Use only one of these tool names: " + strings.Join(names, ", ") + "."
	if len(names) == 0 {
		nameLine = "Use only a tool name from the provided tools list."
	}
	return chatMessage{
		Role: "system",
		Content: nameLine + " If you need to call a tool and native tool_calls are unavailable, return exactly one JSON object: " +
			`{"type":"function_call","call_id":"call_<short_id>","name":"tool_name","arguments":{...}}. ` +
			"For exec_command, arguments must be {\"cmd\":\"real shell command\"}; never put another JSON object inside cmd. " +
			"If the latest exec_command tool result has exit_code 0, do not call exec_command again for the same task; summarize completion briefly. " +
			"Do not use write_stdin unless a prior exec_command returned a session_id. Do not claim that you edited files or ran commands unless a tool result confirms it.",
	}
}

func (s *Server) enforcePromptSize(body []byte) error {
	if s.cfg.MaxPromptChars <= 0 {
		return nil
	}
	var req chatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	return s.enforceChatPromptSize(req)
}

func (s *Server) enforceChatPromptSize(req chatCompletionRequest) error {
	if s.cfg.MaxPromptChars <= 0 {
		return nil
	}
	total := chatPromptChars(req.Messages)
	if total <= s.cfg.MaxPromptChars {
		return nil
	}
	return fmt.Errorf("prompt text is too large for the local lab model: %d chars exceeds limit %d; compact or use native GPT for this task", total, s.cfg.MaxPromptChars)
}

func (s *Server) compactChatPrompt(req *chatCompletionRequest) error {
	if s.cfg.MaxPromptChars <= 0 {
		return nil
	}
	total := chatPromptChars(req.Messages)
	if total <= s.cfg.MaxPromptChars {
		return nil
	}
	if len(req.Messages) < 3 {
		return fmt.Errorf("prompt text is too large for the local lab model: %d chars exceeds limit %d; compact or use native GPT for this task", total, s.cfg.MaxPromptChars)
	}
	req.Messages = compactMessages(req.Messages, s.cfg.MaxPromptChars)
	if chatPromptChars(req.Messages) > s.cfg.MaxPromptChars {
		return fmt.Errorf("prompt text is too large for the local lab model after compaction; compact or use native GPT for this task")
	}
	s.compactions.Inc()
	return nil
}

func chatPromptChars(messages []chatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
		for _, call := range msg.ToolCalls {
			total += len(call.Function.Name) + len(call.Function.Arguments)
		}
	}
	return total
}

func compactMessages(messages []chatMessage, limit int) []chatMessage {
	if len(messages) <= 2 {
		return messages
	}
	first := messages[0]
	tail := append([]chatMessage(nil), messages[len(messages)-2:]...)
	summary := chatMessage{
		Role: "system",
		Content: "Compacted previous context for local lab model. Preserved only the latest actionable messages, " +
			"recent tool outputs, test failures, and the current objective. Older transcript was discarded to stay within context.",
	}
	compacted := []chatMessage{summary}
	if first.Role == "system" && !strings.Contains(first.Content, "Compacted previous context") {
		compacted = append(compacted, first)
	}
	compacted = append(compacted, tail...)
	for chatPromptChars(compacted) > limit && len(compacted) > 1 {
		compacted = append(compacted[:1], compacted[2:]...)
	}
	return compacted
}

type toolCallParseError struct {
	message string
}

func (e *toolCallParseError) Error() string {
	return e.message
}

func (s *Server) chatToResponses(body []byte, req responsesRequest, chatReq chatCompletionRequest) (responsesEnvelope, error) {
	var chat chatCompletionResponse
	if err := json.Unmarshal(body, &chat); err != nil {
		return responsesEnvelope{}, fmt.Errorf("upstream did not return valid chat completion JSON: %w", err)
	}
	text := ""
	var toolCalls []chatToolCall
	if len(chat.Choices) > 0 {
		text = chat.Choices[0].Message.Content
		toolCalls = chat.Choices[0].Message.ToolCalls
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
	responseID := "resp_" + uuid.NewString()
	output := []responseItem{}
	var assistant chatMessage
	if len(toolCalls) > 0 {
		assistant = chatMessage{Role: "assistant", ToolCalls: toolCalls}
		for _, call := range toolCalls {
			item := responseItem{
				ID:        "fc_" + call.ID,
				Type:      "function_call",
				Status:    "completed",
				CallID:    call.ID,
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			}
			item = normalizeResponseToolItem(item)
			if repeatedSuccessfulExecCommand(chatReq.Messages, item) {
				text = repeatedExecCommandMessage
				assistant = chatMessage{Role: "assistant", Content: text}
				output = append(output, messageResponseItem(text))
				break
			}
			output = append(output, item)
			s.toolCalls.WithLabelValues("native", call.Function.Name).Inc()
		}
		if text != repeatedExecCommandMessage {
			text = ""
		}
	} else if item, ok, err := parseFallbackToolCall(text, req.Tools); err != nil {
		return responsesEnvelope{}, &toolCallParseError{message: err.Error()}
	} else if ok {
		if repeatedSuccessfulExecCommand(chatReq.Messages, item) {
			text = repeatedExecCommandMessage
			assistant = chatMessage{Role: "assistant", Content: text}
			output = append(output, messageResponseItem(text))
			goto responseEnvelope
		}
		output = append(output, item)
		assistant = chatMessage{
			Role: "assistant",
			ToolCalls: []chatToolCall{{
				ID:   item.CallID,
				Type: "function",
				Function: chatToolFunction{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			}},
		}
		s.toolCalls.WithLabelValues("fallback_json", item.Name).Inc()
		text = ""
	} else if requiresToolCall(req.ToolChoice) {
		return responsesEnvelope{}, &toolCallParseError{message: "required tool_choice did not produce a valid native tool_call or fallback JSON function_call"}
	} else {
		assistant = chatMessage{Role: "assistant", Content: text}
		output = append(output, messageResponseItem(text))
	}
responseEnvelope:
	envelope := responsesEnvelope{
		ID:         responseID,
		Object:     "response",
		CreatedAt:  now,
		Model:      s.cfg.Model,
		Status:     "completed",
		Output:     output,
		OutputText: text,
		Usage:      usage,
	}
	s.store.set(responseID, append(cloneMessages(chatReq.Messages), assistant))
	return envelope, nil
}

const repeatedExecCommandMessage = "Command already completed successfully; no further tool call is needed."

func messageResponseItem(text string) responseItem {
	return responseItem{
		ID:   "msg_" + uuid.NewString(),
		Type: "message",
		Role: "assistant",
		Content: []responseContentPart{
			{Type: "output_text", Text: text},
		},
	}
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

func (s *Server) writeResponsesStreamFromUpstream(w http.ResponseWriter, ctx context.Context, body []byte, req responsesRequest, chatReq chatCompletionRequest) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	responseID := "resp_" + uuid.NewString()
	messageID := "msg_" + uuid.NewString()
	createdAt := time.Now().Unix()
	startedEnvelope := responsesEnvelope{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Model:     s.cfg.Model,
		Status:    "in_progress",
		Output:    []responseItem{},
	}
	writeSSE(w, "response.created", map[string]any{
		"type":     "response.created",
		"response": startedEnvelope,
	})
	streamTools := hasTools(req.Tools)
	if !streamTools {
		writeSSETextStart(w, messageID)
	}

	res, err := s.openStreamingUpstream(ctx, body, w)
	if err != nil {
		s.streams.WithLabelValues("error").Inc()
		writeSSE(w, "response.failed", map[string]any{
			"type":    "response.failed",
			"message": err.Error(),
		})
		return err
	}
	defer res.Body.Close()

	var output strings.Builder
	var usage responsesUsage
	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), int(s.cfg.MaxBodyBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk chatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			s.upErrors.WithLabelValues("responses_stream", "malformed_chunk").Inc()
			continue
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = responsesUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
				TotalTokens:  chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			delta = chunk.Choices[0].Message.Content
		}
		if delta == "" {
			continue
		}
		output.WriteString(delta)
		if !streamTools {
			writeSSE(w, "response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       messageID,
				"output_index":  0,
				"content_index": 0,
				"delta":         delta,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		s.streams.WithLabelValues("error").Inc()
		s.upErrors.WithLabelValues("responses_stream", "read").Inc()
		return err
	}

	if streamTools {
		if item, ok, err := parseFallbackToolCall(output.String(), req.Tools); err != nil {
			s.streams.WithLabelValues("error").Inc()
			s.sseFailures.Inc()
			s.parseErrors.Inc()
			s.logger.Warn().
				Err(err).
				Strs("tool_names", toolNames(req.Tools)).
				Str("output_excerpt", excerpt(output.String(), 500)).
				Msg("stream fallback tool call parse failed")
			writeSSE(w, "response.failed", map[string]any{
				"type":    "response.failed",
				"message": err.Error(),
			})
			return &toolCallParseError{message: err.Error()}
		} else if ok {
			if repeatedSuccessfulExecCommand(chatReq.Messages, item) {
				text := repeatedExecCommandMessage
				envelope := responsesEnvelope{
					ID:         responseID,
					Object:     "response",
					CreatedAt:  createdAt,
					Model:      s.cfg.Model,
					Status:     "completed",
					Output:     []responseItem{messageResponseItem(text)},
					OutputText: text,
					Usage:      usage,
				}
				s.observeUsage(usage)
				s.streams.WithLabelValues("completed").Inc()
				s.store.set(responseID, append(cloneMessages(chatReq.Messages), chatMessage{Role: "assistant", Content: text}))
				writeSSETextStart(w, messageID)
				writeSSE(w, "response.output_text.delta", map[string]any{
					"type":          "response.output_text.delta",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"delta":         text,
				})
				writeSSE(w, "response.output_text.done", map[string]any{
					"type":          "response.output_text.done",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"text":          text,
				})
				writeSSE(w, "response.content_part.done", map[string]any{
					"type":          "response.content_part.done",
					"item_id":       messageID,
					"output_index":  0,
					"content_index": 0,
					"part":          responseContentPart{Type: "output_text", Text: text},
				})
				writeSSE(w, "response.output_item.done", map[string]any{
					"type":         "response.output_item.done",
					"output_index": 0,
					"item":         envelope.Output[0],
				})
				writeSSE(w, "response.completed", map[string]any{
					"type":     "response.completed",
					"response": envelope,
				})
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				return nil
			}
			s.toolCalls.WithLabelValues("fallback_json_stream", item.Name).Inc()
			envelope := responsesEnvelope{
				ID:         responseID,
				Object:     "response",
				CreatedAt:  createdAt,
				Model:      s.cfg.Model,
				Status:     "completed",
				Output:     []responseItem{item},
				OutputText: "",
				Usage:      usage,
			}
			s.observeUsage(usage)
			s.streams.WithLabelValues("completed").Inc()
			s.store.set(responseID, append(cloneMessages(chatReq.Messages), chatMessage{
				Role: "assistant",
				ToolCalls: []chatToolCall{{
					ID:   item.CallID,
					Type: "function",
					Function: chatToolFunction{
						Name:      item.Name,
						Arguments: item.Arguments,
					},
				}},
			}))
			writeSSE(w, "response.output_item.added", map[string]any{
				"type":         "response.output_item.added",
				"output_index": 0,
				"item":         item,
			})
			writeSSE(w, "response.output_item.done", map[string]any{
				"type":         "response.output_item.done",
				"output_index": 0,
				"item":         item,
			})
			writeSSE(w, "response.completed", map[string]any{
				"type":     "response.completed",
				"response": envelope,
			})
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return nil
		} else if requiresToolCall(req.ToolChoice) {
			s.streams.WithLabelValues("error").Inc()
			s.sseFailures.Inc()
			s.parseErrors.Inc()
			msg := "required tool_choice did not produce a valid native tool_call or fallback JSON function_call"
			s.logger.Warn().
				Strs("tool_names", toolNames(req.Tools)).
				Str("output_excerpt", excerpt(output.String(), 500)).
				Msg(msg)
			writeSSE(w, "response.failed", map[string]any{
				"type":    "response.failed",
				"message": msg,
			})
			return &toolCallParseError{message: msg}
		}
		writeSSETextStart(w, messageID)
		if output.String() != "" {
			writeSSE(w, "response.output_text.delta", map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       messageID,
				"output_index":  0,
				"content_index": 0,
				"delta":         output.String(),
			})
		}
	}

	envelope := responsesEnvelope{
		ID:        responseID,
		Object:    "response",
		CreatedAt: createdAt,
		Model:     s.cfg.Model,
		Status:    "completed",
		Output: []responseItem{
			{
				ID:   messageID,
				Type: "message",
				Role: "assistant",
				Content: []responseContentPart{
					{Type: "output_text", Text: output.String()},
				},
			},
		},
		OutputText: output.String(),
		Usage:      usage,
	}
	s.observeUsage(usage)
	s.streams.WithLabelValues("completed").Inc()
	s.store.set(responseID, append(cloneMessages(chatReq.Messages), chatMessage{Role: "assistant", Content: output.String()}))
	writeSSE(w, "response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"text":          output.String(),
	})
	writeSSE(w, "response.content_part.done", map[string]any{
		"type":          "response.content_part.done",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": output.String(),
		},
	})
	writeSSE(w, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": 0,
		"item":         envelope.Output[0],
	})
	writeSSE(w, "response.completed", map[string]any{
		"type":     "response.completed",
		"response": envelope,
	})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (s *Server) openStreamingUpstream(ctx context.Context, body []byte, w http.ResponseWriter) (*http.Response, error) {
	start := time.Now()
	url := s.cfg.UpstreamBaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.UpstreamAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.UpstreamAPIKey)
	}

	type upstreamResult struct {
		res *http.Response
		err error
	}
	resultCh := make(chan upstreamResult, 1)
	go func() {
		res, err := s.httpClient.Do(req)
		resultCh <- upstreamResult{res: res, err: err}
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case result := <-resultCh:
			s.upDurations.WithLabelValues("responses_stream").Observe(time.Since(start).Seconds())
			if result.err != nil {
				s.upErrors.WithLabelValues("responses_stream", "transport").Inc()
				return nil, result.err
			}
			if result.res.StatusCode < 200 || result.res.StatusCode >= 300 {
				upstreamBody, _ := io.ReadAll(io.LimitReader(result.res.Body, s.cfg.MaxBodyBytes))
				_ = result.res.Body.Close()
				s.upErrors.WithLabelValues("responses_stream", fmt.Sprintf("%d", result.res.StatusCode)).Inc()
				return nil, fmt.Errorf("upstream returned %d: %s", result.res.StatusCode, strings.TrimSpace(string(upstreamBody)))
			}
			return result.res, nil
		case <-ticker.C:
			writeSSEComment(w, "keepalive")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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

func writeSSETextStart(w http.ResponseWriter, messageID string) {
	writeSSE(w, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": 0,
		"item": map[string]any{
			"id":      messageID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	})
	writeSSE(w, "response.content_part.added", map[string]any{
		"type":          "response.content_part.added",
		"item_id":       messageID,
		"output_index":  0,
		"content_index": 0,
		"part": map[string]any{
			"type": "output_text",
			"text": "",
		},
	})
}

func excerpt(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...<truncated>"
}

func writeSSEComment(w http.ResponseWriter, comment string) {
	_, _ = fmt.Fprintf(w, ": %s\n\n", comment)
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
