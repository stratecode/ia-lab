package cognitivegateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDetectModeStripsLeadingTag(t *testing.T) {
	req := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: " \n[PLAN_MODE]\nBuild a plan"}}}
	mode := DetectMode(&req, ModeCode)
	if mode != ModePlan {
		t.Fatalf("expected PLAN_MODE, got %s", mode)
	}
	if strings.Contains(req.Messages[0].Content, "[PLAN_MODE]") {
		t.Fatalf("tag was not stripped: %q", req.Messages[0].Content)
	}
}

func TestDetectModeFallsBackWithoutTag(t *testing.T) {
	req := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
	if got := DetectMode(&req, ModeCode); got != ModeCode {
		t.Fatalf("expected fallback CODE_MODE, got %s", got)
	}
}

func TestPipelineInjectsPromptPoliciesAndSanitizes(t *testing.T) {
	cfg := testConfig()
	req := ChatCompletionRequest{Messages: []ChatMessage{{Role: "user", Content: "[CODE_MODE]\nAPI_KEY=secret\nwrite code"}}}
	state, err := prepareRequest(context.Background(), cfg, req, "test", http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Mode != ModeCode || state.BackendName != "coder" {
		t.Fatalf("unexpected route: %s/%s", state.Mode, state.BackendName)
	}
	if state.Request.Messages[0].Role != "system" {
		t.Fatalf("expected system prompt first, got %+v", state.Request.Messages[0])
	}
	joined := state.Request.Messages[0].Content + "\n" + state.Request.Messages[1].Content
	if !strings.Contains(joined, "Code prompt") || !strings.Contains(joined, "Operational policies") {
		t.Fatalf("prompt/policy not injected: %q", joined)
	}
	if strings.Contains(joined, "API_KEY=secret") {
		t.Fatalf("secret was not sanitized: %q", joined)
	}
}

func TestCompressionPreservesSystemAndLastMessage(t *testing.T) {
	cfg := testConfig()
	modeCfg := cfg.Modes[ModeCode]
	modeCfg.TokenBudget.MaxPromptTokens = 40
	cfg.Modes[ModeCode] = modeCfg
	req := ChatCompletionRequest{Messages: []ChatMessage{
		{Role: "system", Content: "base system"},
		{Role: "user", Content: strings.Repeat("old ", 200)},
		{Role: "assistant", Content: strings.Repeat("middle ", 200)},
		{Role: "user", Content: "final request"},
	}}
	state, err := prepareRequest(context.Background(), cfg, req, "test", http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if state.Request.Messages[0].Role != "system" {
		t.Fatal("system message was not preserved")
	}
	if state.Request.Messages[len(state.Request.Messages)-1].Content != "final request" {
		t.Fatal("last user message was not preserved")
	}
}

func testConfig() Config {
	return Config{
		ListenAddr:  "127.0.0.1:0",
		DefaultMode: ModeCode,
		Backends: map[string]BackendConfig{
			"coder": {BaseURL: "http://127.0.0.1:1/v1", APIKey: "backend", Model: "coder"},
		},
		Modes: map[Mode]ModeConfig{
			ModeCode: {
				RouteTo:      "coder",
				SystemPrompt: "Code prompt",
				Policies:     []string{"Do safe work"},
				Middlewares:  []string{"sanitize", "mode_prompt", "policy", "budget", "compress"},
				TokenBudget:  TokenBudget{MaxPromptTokens: 1000},
			},
		},
		JSONValidation: JSONValidationConfig{WarnOnly: true},
		ResponseFooter: ResponseFooterConfig{Enabled: true},
	}
}
