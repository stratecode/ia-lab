package cognitivegateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionsRoutesAndTransforms(t *testing.T) {
	var seen ChatCompletionRequest
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer backend-key" {
			t.Fatalf("unexpected backend auth %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": `{"ok":true}`}}},
		})
	}))
	defer backend.Close()

	cfg := testConfig()
	cfg.AcceptedAPIKeys = []string{"client-key"}
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", APIKey: "backend-key", Model: "coder"}
	app := NewServer(cfg).Router()
	body := []byte(`{"model":"ignored","messages":[{"role":"user","content":"[CODE_MODE]\nhello"}],"response_format":{"type":"json_object"}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer client-key")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rr.Code, rr.Body.String())
	}
	if seen.Model != "coder" {
		t.Fatalf("expected routed model coder, got %q", seen.Model)
	}
	if len(seen.Messages) == 0 || seen.Messages[0].Role != "system" {
		t.Fatalf("expected transformed system message, got %+v", seen.Messages)
	}
	if strings.Contains(seen.Messages[len(seen.Messages)-1].Content, "[CODE_MODE]") {
		t.Fatal("mode tag leaked to backend")
	}
}

func TestPlanModeInjectsOperationalIRAndForcesJSON(t *testing.T) {
	var contextReq map[string]any
	contextBuilder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer context-key" {
			t.Fatalf("unexpected context auth %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&contextReq); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"operational_ir": map[string]any{
				"version":     "semantic-runtime-ir/v1",
				"mode":        "PLAN_MODE",
				"trusted":     []map[string]any{{"source_ref": "artifact:a#0", "outcome": "trusted"}},
				"invalid":     []map[string]any{{"source_ref": "task:b#0", "outcome": "failed", "failure_reason": "bad split"}},
				"constraints": []string{"preserve approvals"},
				"jsonata":     map[string]any{"conditions": map[string]string{"has_invalid_prior": "$count($.invalid)>0"}},
				"source_refs": []string{"artifact:a#0", "task:b#0"},
			},
		})
	}))
	defer contextBuilder.Close()

	var seen ChatCompletionRequest
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&seen); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": `{"version":"planner-jsonata-plan/v1","tasks":[]}`}}},
		})
	}))
	defer backend.Close()

	cfg := testConfig()
	cfg.DefaultMode = ModePlan
	cfg.Backends["planner"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "planner"}
	cfg.Modes[ModePlan] = ModeConfig{
		RouteTo:      "planner",
		SystemPrompt: "Plan prompt",
		Policies:     []string{"Respect approvals"},
		Middlewares:  []string{"sanitize", "mode_prompt", "policy", "retrieval_hook", "budget", "compress"},
		TokenBudget:  TokenBudget{MaxPromptTokens: 1000},
	}
	cfg.ContextBuilderURL = contextBuilder.URL
	cfg.ContextBuilderAPIKey = "context-key"
	cfg.OperationalIR = OperationalIRConfig{Enabled: true, MaxChars: 6000, PlanJSONataRequired: true}

	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"[PLAN_MODE] plan this"}]}`))
	req.Header.Set("X-Lab-Initiative-ID", "11111111-1111-1111-1111-111111111111")
	req.Header.Set("X-Lab-Workspace-Root", "/tmp/work")
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rr.Code, rr.Body.String())
	}
	if seen.ResponseFormat["type"] != "json_object" {
		t.Fatalf("PLAN_MODE did not force json_object: %+v", seen.ResponseFormat)
	}
	joined := seen.Messages[0].Content
	if !strings.Contains(joined, "semantic-runtime-ir/v1") || !strings.Contains(joined, "planner-jsonata-plan/v1") {
		t.Fatalf("operational IR or planner contract missing: %q", joined)
	}
	if contextReq["output_format"] != "operational_ir" {
		t.Fatalf("unexpected context output format: %+v", contextReq)
	}
}

func TestPlanModeRejectsConversationalResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "Here is a plan in prose"}}},
		})
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.DefaultMode = ModePlan
	cfg.Backends["planner"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "planner"}
	cfg.Modes[ModePlan] = ModeConfig{
		RouteTo:      "planner",
		SystemPrompt: "Plan prompt",
		Middlewares:  []string{"mode_prompt", "policy", "budget"},
		TokenBudget:  TokenBudget{MaxPromptTokens: 1000},
	}
	cfg.OperationalIR = OperationalIRConfig{Enabled: true, MaxChars: 6000, PlanJSONataRequired: true}
	cfg.JSONValidation = JSONValidationConfig{WarnOnly: true}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"[PLAN_MODE] plan this"}]}`))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected JSON rejection, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletionsPreservesUnknownOpenAIFields(t *testing.T) {
	var raw map[string]any
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}}})
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "coder"}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}],"top_p":0.9}`))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rr.Code)
	}
	if _, ok := raw["tools"]; !ok {
		t.Fatal("tools field was not preserved")
	}
	if raw["top_p"] == nil {
		t.Fatal("top_p field was not preserved")
	}
}

func TestChatCompletionsAppendsGatewayFooterForTextResponses(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": "plain response"}}},
		})
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "coder"}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"[CODE_MODE] hi"}]}`))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "[Mode: CODE_MODE]") || !strings.Contains(body, "cognitive_gateway") {
		t.Fatalf("gateway footer missing: %s", body)
	}
	if rr.Header().Get("X-Cognitive-Gateway-Metrics") == "" {
		t.Fatal("gateway metrics header missing")
	}
}

func TestJSONModeDoesNotAppendFooterToMessageContent(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": `{"ok":true}`}}},
		})
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "coder"}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"[CODE_MODE] hi"}],"response_format":{"type":"json_object"}}`))
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rr.Code)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Choices[0].Message.Content != `{"ok":true}` {
		t.Fatalf("JSON content was modified: %q", parsed.Choices[0].Message.Content)
	}
	if !strings.Contains(rr.Body.String(), "cognitive_gateway") {
		t.Fatalf("gateway metadata missing: %s", rr.Body.String())
	}
}

func TestStreamingPassthrough(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\ndata: [DONE]\n\n"))
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", APIKey: "backend-key", Model: "coder"}
	gateway := httptest.NewServer(NewServer(cfg).Router())
	defer gateway.Close()
	resp, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"[CODE_MODE] hi"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "data:") {
		t.Fatalf("expected SSE data, got %q", string(body))
	}
	if !strings.Contains(string(body), "[Mode: CODE_MODE]") {
		t.Fatalf("expected gateway footer in stream, got %q", string(body))
	}
}

func TestReadyDegradesWhenBackendFails(t *testing.T) {
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: "http://127.0.0.1:1/v1"}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected degraded ready, got %d", rr.Code)
	}
}

func TestModelsAggregatesBackends(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []map[string]any{{"id": "coder"}}})
	}))
	defer backend.Close()
	cfg := testConfig()
	cfg.Backends["coder"] = BackendConfig{BaseURL: backend.URL + "/v1", Model: "coder"}
	app := NewServer(cfg).Router()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	app.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "coder") {
		t.Fatalf("expected coder model in response: %s", rr.Body.String())
	}
}
