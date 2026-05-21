package cognitivegateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type Middleware func(context.Context, *contextState) error

var secretLinePattern = regexp.MustCompile(`(?i)(api[_-]?key|authorization|bearer|credential|password|private[_-]?key|secret|token)\s*[:=]`)

func DetectMode(req *ChatCompletionRequest, fallback Mode) Mode {
	mode, _ := detectAndStripMode(req, fallback)
	return mode
}

func detectAndStripMode(req *ChatCompletionRequest, fallback Mode) (Mode, bool) {
	for i := range req.Messages {
		content := strings.TrimLeft(req.Messages[i].Content, " \t\r\n")
		for _, mode := range []Mode{ModePlan, ModeCode, ModeReview, ModeResearch} {
			tag := "[" + string(mode) + "]"
			if strings.HasPrefix(content, tag) {
				req.Messages[i].Content = strings.TrimLeft(strings.TrimPrefix(content, tag), " \t\r\n")
				return mode, true
			}
		}
		if strings.TrimSpace(content) != "" {
			break
		}
	}
	return fallback, false
}

func BuildPipeline(names []string) []Middleware {
	out := make([]Middleware, 0, len(names))
	for _, name := range names {
		switch strings.TrimSpace(name) {
		case "sanitize":
			out = append(out, sanitizeMiddleware)
		case "mode_prompt":
			out = append(out, modePromptMiddleware)
		case "policy":
			out = append(out, policyMiddleware)
		case "budget":
			out = append(out, budgetMiddleware)
		case "compress":
			out = append(out, compressMiddleware)
		case "retrieval_hook":
			out = append(out, retrievalHookMiddleware)
		}
	}
	return out
}

func sanitizeMiddleware(_ context.Context, state *contextState) error {
	for i := range state.Request.Messages {
		state.Request.Messages[i].Content = sanitizeText(state.Request.Messages[i].Content)
	}
	return nil
}

func modePromptMiddleware(_ context.Context, state *contextState) error {
	modeCfg := stateModeConfig(state)
	prompt := strings.TrimSpace(modeCfg.SystemPrompt)
	if prompt == "" {
		return nil
	}
	if len(state.Request.Messages) > 0 && state.Request.Messages[0].Role == "system" {
		state.Request.Messages[0].Content = prompt + "\n\n" + state.Request.Messages[0].Content
		return nil
	}
	state.Request.Messages = append([]ChatMessage{{Role: "system", Content: prompt}}, state.Request.Messages...)
	return nil
}

func policyMiddleware(_ context.Context, state *contextState) error {
	modeCfg := stateModeConfig(state)
	if len(modeCfg.Policies) == 0 {
		if state.Mode == ModePlan && state.Config.OperationalIR.PlanJSONataRequired {
			injectSystemMessage(state, plannerJSONataInstruction())
		}
		return nil
	}
	policy := "Operational policies:\n- " + strings.Join(modeCfg.Policies, "\n- ")
	if len(state.Request.Messages) > 0 && state.Request.Messages[0].Role == "system" {
		state.Request.Messages[0].Content += "\n\n" + policy
	} else {
		state.Request.Messages = append([]ChatMessage{{Role: "system", Content: policy}}, state.Request.Messages...)
	}
	if state.Mode == ModePlan && state.Config.OperationalIR.PlanJSONataRequired {
		injectSystemMessage(state, plannerJSONataInstruction())
	}
	return nil
}

func budgetMiddleware(_ context.Context, state *contextState) error {
	state.Estimated = estimateTokens(state.Request.Messages)
	return nil
}

func compressMiddleware(_ context.Context, state *contextState) error {
	modeCfg := stateModeConfig(state)
	limit := modeCfg.TokenBudget.MaxPromptTokens
	if limit <= 0 {
		return nil
	}
	for estimateTokens(state.Request.Messages) > limit && len(state.Request.Messages) > 3 {
		remove := 1
		if state.Request.Messages[0].Role == "system" {
			remove = 1
		}
		state.Request.Messages = append(state.Request.Messages[:remove], state.Request.Messages[remove+1:]...)
	}
	for i := range state.Request.Messages {
		if estimateTokens(state.Request.Messages) <= limit {
			break
		}
		if state.Request.Messages[i].Role == "system" || i == len(state.Request.Messages)-1 {
			continue
		}
		state.Request.Messages[i].Content = truncateChars(state.Request.Messages[i].Content, len(state.Request.Messages[i].Content)/2)
	}
	state.Estimated = estimateTokens(state.Request.Messages)
	return nil
}

func retrievalHookMiddleware(ctx context.Context, state *contextState) error {
	if state == nil || !state.Config.OperationalIR.Enabled || strings.TrimSpace(state.Config.ContextBuilderURL) == "" {
		return nil
	}
	req := contextBuildRequest{
		AgentType:       agentTypeForMode(state.Mode),
		TaskID:          optionalString(state.TaskID),
		InitiativeID:    optionalString(state.InitiativeID),
		WorkspaceRoot:   optionalString(state.WorkspaceRoot),
		TaskDescription: latestUserContent(state.Request.Messages),
		Metadata: map[string]any{
			"mode":       string(state.Mode),
			"request_id": state.RequestID,
		},
		OutputFormat:    "operational_ir",
		IncludeFailed:   state.Mode == ModePlan || state.Mode == ModeReview,
		IncludeRejected: state.Mode == ModePlan || state.Mode == ModeReview,
		MaxChunks:       maxChunksForMode(state.Mode),
		MaxChars:        state.Config.OperationalIR.MaxChars,
	}
	if state.Mode == ModePlan || state.Mode == ModeReview {
		req.Outcomes = []string{"trusted", "failed", "rejected", "invalid"}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, state.Config.ContextBuilderURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Request-ID", state.RequestID)
	if strings.TrimSpace(state.Config.ContextBuilderAPIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(state.Config.ContextBuilderAPIKey))
	}
	client := &http.Client{Timeout: 10 * time.Second}
	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		state.ContextError = err.Error()
		contextBuilderTotal.WithLabelValues(string(state.Mode), "error").Inc()
		if state.Config.ContextBuilderRequired {
			return err
		}
		log.Warn().Err(err).Str("request_id", state.RequestID).Str("mode", string(state.Mode)).Msg("context builder request failed")
		return nil
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		state.ContextError = err.Error()
		contextBuilderTotal.WithLabelValues(string(state.Mode), "error").Inc()
		if state.Config.ContextBuilderRequired {
			return err
		}
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		state.ContextError = fmt.Sprintf("context builder status %d: %s", resp.StatusCode, string(payload))
		contextBuilderTotal.WithLabelValues(string(state.Mode), "upstream_error").Inc()
		if state.Config.ContextBuilderRequired {
			return fmt.Errorf("%s", state.ContextError)
		}
		return nil
	}
	var parsed contextPackage
	if err := json.Unmarshal(payload, &parsed); err != nil {
		state.ContextError = err.Error()
		contextBuilderTotal.WithLabelValues(string(state.Mode), "error").Inc()
		if state.Config.ContextBuilderRequired {
			return err
		}
		return nil
	}
	if parsed.OperationalIR == nil {
		contextBuilderTotal.WithLabelValues(string(state.Mode), "empty").Inc()
		return nil
	}
	irRaw, err := json.Marshal(parsed.OperationalIR)
	if err != nil {
		return err
	}
	if state.Config.OperationalIR.MaxChars > 0 && len(irRaw) > state.Config.OperationalIR.MaxChars {
		irRaw = []byte(truncateChars(string(irRaw), state.Config.OperationalIR.MaxChars))
	}
	state.IRChars = len(irRaw)
	irCompileTotal.WithLabelValues(string(state.Mode), "success").Inc()
	irChars.WithLabelValues(string(state.Mode)).Observe(float64(state.IRChars))
	contextBuilderTotal.WithLabelValues(string(state.Mode), "success").Inc()
	log.Debug().Str("request_id", state.RequestID).Str("mode", string(state.Mode)).Dur("duration", time.Since(start)).Int("ir_chars", state.IRChars).Msg("context builder operational IR attached")
	injectSystemMessage(state, "Operational IR (JSONata-compatible, semantic-runtime-ir/v1):\n"+string(irRaw))
	return nil
}

func sanitizeText(input string) string {
	lines := strings.Split(input, "\n")
	for i, line := range lines {
		if secretLinePattern.MatchString(line) || strings.Contains(line, "-----BEGIN") && strings.Contains(line, "PRIVATE KEY-----") {
			lines[i] = "[REDACTED_SECRET]"
		}
	}
	return strings.Join(lines, "\n")
}

func estimateTokens(messages []ChatMessage) int {
	chars := 0
	for _, msg := range messages {
		chars += len([]rune(msg.Role)) + len([]rune(msg.Content))
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4
}

func truncateChars(input string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(input)
	if len(runes) <= maxChars {
		return input
	}
	return string(runes[:maxChars]) + "\n[TRUNCATED_BY_COGNITIVE_GATEWAY]"
}

type contextBuildRequest struct {
	AgentType       string         `json:"agent_type"`
	TaskID          *string        `json:"task_id,omitempty"`
	InitiativeID    *string        `json:"initiative_id,omitempty"`
	WorkspaceRoot   *string        `json:"workspace_root,omitempty"`
	TaskDescription string         `json:"task_description,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	OutputFormat    string         `json:"output_format,omitempty"`
	Outcomes        []string       `json:"outcomes,omitempty"`
	IncludeFailed   bool           `json:"include_failed,omitempty"`
	IncludeRejected bool           `json:"include_rejected,omitempty"`
	MaxChunks       int            `json:"max_chunks,omitempty"`
	MaxChars        int            `json:"max_chars,omitempty"`
}

type contextPackage struct {
	OperationalIR map[string]any `json:"operational_ir"`
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func latestUserContent(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	if len(messages) == 0 {
		return ""
	}
	return messages[len(messages)-1].Content
}

func agentTypeForMode(mode Mode) string {
	switch mode {
	case ModePlan:
		return "planner"
	case ModeCode:
		return "coder"
	case ModeReview:
		return "reviewer"
	case ModeResearch:
		return "researcher"
	default:
		return strings.ToLower(string(mode))
	}
}

func maxChunksForMode(mode Mode) int {
	switch mode {
	case ModePlan:
		return 8
	case ModeReview:
		return 10
	case ModeCode:
		return 6
	default:
		return 4
	}
}

func injectSystemMessage(state *contextState, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if len(state.Request.Messages) > 0 && state.Request.Messages[0].Role == "system" {
		state.Request.Messages[0].Content += "\n\n" + content
		return
	}
	state.Request.Messages = append([]ChatMessage{{Role: "system", Content: content}}, state.Request.Messages...)
}

func plannerJSONataInstruction() string {
	return `PLAN_MODE output contract:
Return only a JSON object, with no prose outside JSON.
Use this shape:
{"version":"planner-jsonata-plan/v1","summary":"","tasks":[],"dependencies":[],"approvals":[],"policies":[],"retry_policies":[],"jsonata":{"execution_order":"","approval_required":"","blocked_tasks":""},"source_refs":[]}`
}

func stateModeConfig(state *contextState) ModeConfig {
	if state == nil {
		return ModeConfig{}
	}
	return state.ModeConfig
}

func prepareRequest(ctx context.Context, cfg Config, req ChatCompletionRequest, requestID string, headers http.Header) (*contextState, error) {
	mode, _ := detectAndStripMode(&req, cfg.DefaultMode)
	modeCfg, ok := cfg.Modes[mode]
	if !ok {
		return nil, fmt.Errorf("mode %s is not configured", mode)
	}
	backend, ok := cfg.Backends[modeCfg.RouteTo]
	if !ok {
		return nil, fmt.Errorf("backend %s is not configured", modeCfg.RouteTo)
	}
	if backend.Model != "" {
		req.Model = backend.Model
	}
	if mode == ModePlan && cfg.OperationalIR.PlanJSONataRequired {
		req.ResponseFormat = map[string]any{"type": "json_object"}
	}
	state := &contextState{
		Config:        cfg,
		Request:       req,
		Mode:          mode,
		ModeConfig:    modeCfg,
		BackendName:   modeCfg.RouteTo,
		Backend:       backend,
		Streaming:     req.Stream,
		RequestID:     requestID,
		TaskID:        strings.TrimSpace(headers.Get("X-Lab-Task-ID")),
		InitiativeID:  strings.TrimSpace(headers.Get("X-Lab-Initiative-ID")),
		WorkspaceRoot: strings.TrimSpace(headers.Get("X-Lab-Workspace-Root")),
	}
	for _, mw := range BuildPipeline(modeCfg.Middlewares) {
		if err := mw(ctx, state); err != nil {
			return nil, err
		}
	}
	return state, nil
}
