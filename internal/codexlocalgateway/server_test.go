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

	cfg := testConfig(upstreamServer.URL + "/v1")
	srv := httptest.NewServer(NewServer(cfg, zerolog.Nop()))
	t.Cleanup(srv.Close)
	return srv, upstreamServer
}

func testConfig(upstreamBaseURL string) Config {
	return Config{
		Addr:            "127.0.0.1:0",
		APIKey:          "test-key",
		UpstreamBaseURL: upstreamBaseURL,
		UpstreamAPIKey:  "upstream-key",
		Model:           "qwen-local-code",
		UpstreamModel:   "coder",
		RequestTimeout:  2 * time.Second,
		MaxBodyBytes:    1 << 20,
		RateLimitRPM:    60,
		RateLimitBurst:  10,
	}
}

func newTestServerWithConfig(t *testing.T, cfg Config, upstream http.Handler) (*httptest.Server, *httptest.Server) {
	t.Helper()
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)
	cfg.UpstreamBaseURL = upstreamServer.URL + "/v1"
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
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"model":"coder","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
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

	if upstreamBody["model"] != "coder" {
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

func TestResponsesAppliesDefaultMaxOutputTokens(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}
	if upstreamBody["max_tokens"] != float64(defaultMaxOutputTokens) {
		t.Fatalf("expected default max_tokens, got %#v", upstreamBody["max_tokens"])
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

func TestResponsesCompactsToolsForUpstreamAndMapsNativeToolCall(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_tool",
			"choices":[{
				"message":{
					"role":"assistant",
					"tool_calls":[{
						"id":"call_read_1",
						"type":"function",
						"function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}
		}`))
	}))

	payload := `{
		"model":"qwen-local-code",
		"input":"Inspect README",
		"tools":[{
			"type":"function",
			"function":{
				"name":"read_file",
				"description":"Read a local file",
				"parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}
			}
		}],
		"tool_choice":"auto",
		"parallel_tool_calls":false
	}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}
	if _, ok := upstreamBody["tools"]; ok {
		t.Fatalf("expected gateway to strip verbose tools before llama.cpp upstream, got %#v", upstreamBody["tools"])
	}
	messages := upstreamBody["messages"].([]any)
	toolInstruction := messages[len(messages)-2].(map[string]any)["content"].(string)
	if !strings.Contains(toolInstruction, "read_file") {
		t.Fatalf("expected compact tool instruction to mention read_file, got %q", toolInstruction)
	}

	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 {
		t.Fatalf("expected one output item, got %#v", out.Output)
	}
	item := out.Output[0]
	if item.Type != "function_call" || item.CallID != "call_read_1" || item.Name != "read_file" || item.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected function_call output: %#v", item)
	}
	if out.OutputText != "" {
		t.Fatalf("tool call response should not synthesize output_text, got %q", out.OutputText)
	}
}

func TestResponsesForcesExecCommandForImmediateExecutionRequests(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"CREATED"}}]}`))
	}))

	payload := `{
		"model":"qwen-local-code",
		"input":"Approval policy is never. Execute this command immediately: touch /tmp/proof. Reply with CREATED.",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected successful synthesized tool call, got %d: %s", res.StatusCode, body)
	}
	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "exec_command" {
		t.Fatalf("expected synthesized exec_command tool call, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Arguments, `touch /tmp/proof`) {
		t.Fatalf("expected synthesized command arguments, got %q", out.Output[0].Arguments)
	}
}

func TestEffectiveToolChoiceLeavesNonExecRequestsOnAuto(t *testing.T) {
	req := responsesRequest{
		Input: json.RawMessage(`"Explain whether this command is safe: rm -rf /tmp/demo"`),
		Tools: json.RawMessage(`[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]`),
	}
	if got := effectiveToolChoice(req); got != nil {
		t.Fatalf("expected no forced tool choice, got %#v", got)
	}
}

func TestEffectiveToolChoiceRequiresAnyToolForAgenticExecutionRequests(t *testing.T) {
	req := responsesRequest{
		Input: json.RawMessage(`"Update Laravel to the latest version, validate the app, and modify the code if needed."`),
		Tools: json.RawMessage(`[{"type":"function","name":"read_file","parameters":{"type":"object"}}]`),
	}
	if got := effectiveToolChoice(req); got != "required" {
		t.Fatalf("expected required tool choice for agentic execution request, got %#v", got)
	}
}

func TestEffectiveToolChoiceClearsRequiredChoiceAfterFunctionCallOutput(t *testing.T) {
	req := responsesRequest{
		Input: json.RawMessage(`[{"type":"function_call_output","call_id":"call_exec_1","output":{"exit_code":0,"status":"completed"}}]`),
		Tools: json.RawMessage(`[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]`),
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "exec_command",
			},
		},
	}
	if got := effectiveToolChoice(req); got != nil {
		t.Fatalf("expected tool choice to be cleared after tool output, got %#v", got)
	}
}

func TestResponsesStreamAllowsTextAfterToolOutputEvenIfToolChoiceWasRequired(t *testing.T) {
	var upstreamBodies []map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		upstreamBodies = append(upstreamBodies, body)
		if len(upstreamBodies) == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"type\":\"function_call\",\"call_id\":\"call_exec_1\",\"name\":\"exec_command\",\"arguments\":{\"cmd\":\"echo ok\"}}"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"PASSED\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	first, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Execute this command immediately: echo ok",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var firstOut responsesEnvelope
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatal(err)
	}

	second, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"previous_response_id":"`+firstOut.ID+`",
		"input":[{"type":"function_call_output","call_id":"call_exec_1","output":{"exit_code":0,"status":"completed"}}],
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","function":{"name":"exec_command"}}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	body, _ := io.ReadAll(second.Body)
	if bytes.Contains(body, []byte("response.failed")) {
		t.Fatalf("expected successful text completion after tool output, got: %s", body)
	}
	if !bytes.Contains(body, []byte("PASSED")) {
		t.Fatalf("expected PASSED text in stream, got: %s", body)
	}
	if len(upstreamBodies) < 2 {
		t.Fatalf("expected two upstream calls, got %d", len(upstreamBodies))
	}
	if _, ok := upstreamBodies[1]["tool_choice"]; ok {
		t.Fatalf("expected cleared tool_choice on post-tool turn, got %#v", upstreamBodies[1]["tool_choice"])
	}
}

func TestResponsesRejectsProseOnlyReplyInStrictExecutionMode(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"I would first inspect the repository and then decide what to change."}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Update the project dependencies and validate the result.",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected execution_not_started conflict, got %d: %s", res.StatusCode, body)
	}
	var out errorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Type != "execution_not_started" {
		t.Fatalf("expected execution_not_started error, got %#v", out.Error)
	}
}

func TestResponsesStreamRejectsProseOnlyReplyInStrictExecutionMode(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I would inspect the repo and outline the changes.\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Fix the app and validate it.",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte(`"error_type":"execution_not_started"`)) {
		t.Fatalf("expected execution_not_started stream failure, got: %s", body)
	}
}

func TestResponsesStreamSynthesizesExecCommandForImmediateExecutionRequests(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"CREATED\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Approval policy is never. Execute this command immediately: touch /tmp/proof. Reply with CREATED.",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if bytes.Contains(body, []byte("response.failed")) {
		t.Fatalf("expected synthesized stream tool call, got failure: %s", body)
	}
	for _, want := range []string{`"type":"function_call"`, `"name":"exec_command"`, `touch /tmp/proof`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in synthesized stream body: %s", want, body)
		}
	}
}

func TestResponsesNormalizesNativeJSONBlobExecCommandToFileWrite(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{
				"message":{
					"role":"assistant",
					"tool_calls":[{
						"id":"call_exec_1",
						"type":"function",
						"function":{"name":"exec_command","arguments":"{\"cmd\":\"{\\\"file_path\\\":\\\"hello.txt\\\",\\\"patch_content\\\":\\\"after\\n\\\"}\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}]
		}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Name != "exec_command" {
		t.Fatalf("expected exec_command output, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Arguments, "write_text") || strings.Contains(out.Output[0].Arguments, "file_path") {
		t.Fatalf("expected normalized file write command, got %q", out.Output[0].Arguments)
	}
}

func TestResponsesSuppressesDuplicateNativeExecCommandsInSameResponse(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{
				"message":{
					"role":"assistant",
					"tool_calls":[{
						"id":"call_exec_1",
						"type":"function",
						"function":{"name":"exec_command","arguments":"{\"cmd\":\"echo \\\"after\\\" > hello.txt\"}"}
					},{
						"id":"call_exec_2",
						"type":"function",
						"function":{"name":"exec_command","arguments":"{\"cmd\":\"touch /tmp/codex-lab-native-e2e/hello.txt\necho 'after' > /tmp/codex-lab-native-e2e/hello.txt\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}]
		}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].CallID != "call_exec_1" {
		t.Fatalf("expected only first duplicate exec_command, got %#v", out.Output)
	}
}

func TestResponsesPreviousResponseAddsFunctionCallOutput(t *testing.T) {
	var upstreamRequests []map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstreamBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		upstreamRequests = append(upstreamRequests, upstreamBody)
		w.Header().Set("Content-Type", "application/json")
		if len(upstreamRequests) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_read_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"README inspected"}}]}`))
	}))

	first, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Inspect README",
		"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var firstOut responsesEnvelope
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatal(err)
	}

	secondPayload := `{
		"model":"qwen-local-code",
		"previous_response_id":"` + firstOut.ID + `",
		"input":[{"type":"function_call_output","call_id":"call_read_1","output":"# Lab\n"}],
		"tools":[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}]
	}`
	second, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(secondPayload)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("unexpected status %d: %s", second.StatusCode, body)
	}
	if len(upstreamRequests) != 2 {
		t.Fatalf("expected two upstream requests, got %d", len(upstreamRequests))
	}
	messages := upstreamRequests[1]["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "call_read_1" || last["content"] != "# Lab\n" {
		t.Fatalf("expected function_call_output as tool message, got %#v", last)
	}
}

func TestResponsesFallbackJSONToolCall(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"```json\\n{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_patch_1\\\",\\\"name\\\":\\\"apply_patch\\\",\\\"arguments\\\":{\\\"patch\\\":\\\"*** Begin Patch\\\\n*** End Patch\\\"}}\\n```\"}}]}"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Patch file",
		"tools":[{"type":"function","function":{"name":"apply_patch","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"apply_patch"}}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "apply_patch" {
		t.Fatalf("expected fallback function_call, got %#v", out.Output)
	}
	if out.Output[0].Arguments != `{"patch":"*** Begin Patch\n*** End Patch"}` {
		t.Fatalf("unexpected fallback arguments: %q", out.Output[0].Arguments)
	}
}

func TestResponsesParsesEmbeddedMarkdownFunctionCall(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"I will check the latest Laravel release first.\\n\\n**Action:** exec_command\\n\\n```json\\n{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_exec_1\\\",\\\"name\\\":\\\"exec_command\\\",\\\"arguments\\\":{\\\"cmd\\\":\\\"curl -s https://api.laravel.com/docs/v1/releases/stable\\\"}}\\n```\"}}]}"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Check latest Laravel release",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "exec_command" {
		t.Fatalf("expected parsed embedded exec_command function_call, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Arguments, "api.laravel.com/docs/v1/releases/stable") {
		t.Fatalf("unexpected embedded function_call arguments: %q", out.Output[0].Arguments)
	}
}

func TestResponsesStreamBuffersFallbackToolCall(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```json\\n\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_write_1\\\",\\\"name\\\":\\\"write_file\\\",\\\"arguments\\\":{\\\"path\\\":\\\"hello.txt\\\",\\\"content\\\":\\\"after\\\\n\\\"}}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"\\n```\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Change hello.txt",
		"stream":true,
		"tools":[{"type":"function","function":{"name":"write_file","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"write_file"}}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		"event: response.output_item.added",
		`"type":"function_call"`,
		`"call_id":"call_write_1"`,
		`"name":"write_file"`,
		"event: response.completed",
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte("response.output_text.delta")) {
		t.Fatalf("tool streams must not leak fallback JSON as text deltas: %s", body)
	}
}

func TestResponsesStreamMapsCustomApplyPatchToolCall(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_patch_1\\\",\\\"name\\\":\\\"apply_patch\\\",\\\"arguments\\\":{\\\"patch\\\":\\\"*** Begin Patch\\\\n*** End Patch\\\"}}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Patch file",
		"stream":true,
		"tools":[{"type":"custom","name":"apply_patch","format":{"type":"grammar"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"custom_tool_call"`,
		`"name":"apply_patch"`,
		`"input":"*** Begin Patch\n*** End Patch"`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}

func TestResponsesStreamDowngradesInvalidApplyPatchToExecCommand(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_patch_1\\\",\\\"name\\\":\\\"apply_patch\\\",\\\"arguments\\\":{\\\"cmd\\\":\\\"echo after > hello.txt\\\"}}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Patch file",
		"stream":true,
		"tools":[
			{"type":"custom","name":"apply_patch","format":{"type":"grammar"}},
			{"type":"function","name":"exec_command","parameters":{"type":"object"}}
		]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`echo after \\u003e hello.txt`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}

func TestResponsesStreamAliasesWriteFileToExecCommand(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_write_1\\\",\\\"name\\\":\\\"write_file\\\",\\\"arguments\\\":{\\\"path\\\":\\\"hello.txt\\\",\\\"content\\\":\\\"after\\\\n\\\"}}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`write_text`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}

func TestResponsesStreamNormalizesJSONBlobExecCommandToFileWrite(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_exec_1\\\",\\\"name\\\":\\\"exec_command\\\",\\\"arguments\\\":{\\\"cmd\\\":\\\"{\\\\\\\"file_path\\\\\\\":\\\\\\\"hello.txt\\\\\\\",\\\\\\\"patch_content\\\\\\\":\\\\\\\"after\\\\\\\\n\\\\\\\"}\\\"}}\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`write_text`,
		`hello.txt`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte(`file_path`)) || bytes.Contains(body, []byte(`patch_content`)) {
		t.Fatalf("json blob command leaked through as shell input: %s", body)
	}
}

func TestResponsesStreamMapsShellFenceToExecCommand(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```sh\\necho \\\"after\\\" > hello.txt\\n```\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`echo \\\"after\\\" \\u003e hello.txt`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte("response.output_text.delta")) {
		t.Fatalf("shell fence must become a tool call, not text: %s", body)
	}
}

func TestResponsesStreamMapsShellFenceToToolSearchCodeWhenExecUnavailable(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```bash\\necho after > hello.txt\\n```\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	payload := `{
		"model":"qwen-local-code",
		"stream":true,
		"input":"Create hello.txt and write after into it.",
		"tools":[{"type":"function","name":"tool_search_code","parameters":{"type":"object"}}]
	}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"tool_search_code"`,
		`openclaw.tools.call`,
		`hello.txt`,
		`"type":"response.function_call_arguments.delta"`,
		`"type":"response.function_call_arguments.done"`,
		`"arguments":"{\"code\":`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("expected stream body to contain %q, got: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte(`"type":"message"`)) {
		t.Fatalf("shell fence must become a tool_search_code call, not text: %s", body)
	}
}

func TestResponsesStreamMapsNarratedShellFenceToExecCommand(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Use this command:\\n\\n```sh\\necho \\\"after\\\" > hello.txt\\n```\\n\\nThen stop.\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"exec_command"`,
		`echo \\\"after\\\" \\u003e hello.txt`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte("response.output_text.delta")) {
		t.Fatalf("narrated shell fence must become a tool call, not text: %s", body)
	}
}

func TestResponsesStreamMapsNarratedShellFenceToToolSearchCodeWhenExecUnavailable(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```bash\\necho after > hello.txt\\ncat hello.txt\\n```\\nContenido de hello.txt:\\nafter\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	payload := `{
		"model":"qwen-local-code",
		"stream":true,
		"input":"Create hello.txt, verify it, and summarize the result.",
		"tools":[{"type":"function","name":"tool_search_code","parameters":{"type":"object"}}]
	}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{
		`"type":"function_call"`,
		`"name":"tool_search_code"`,
		`cat hello.txt`,
		`"type":"response.function_call_arguments.delta"`,
		`"type":"response.function_call_arguments.done"`,
		`"arguments":"{\"code\":`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("expected stream body to contain %q, got: %s", want, body)
		}
	}
	if bytes.Contains(body, []byte(`"type":"message"`)) {
		t.Fatalf("narrated shell fence must become a tool_search_code call, not text: %s", body)
	}
}

func TestResponsesNormalizesNativeToolSearchCodeCmdArguments(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_tool_search_code",
			"choices":[{
				"message":{
					"role":"assistant",
					"tool_calls":[{
						"id":"call_12345",
						"type":"function",
						"function":{"name":"tool_search_code","arguments":"{\"cmd\":\"echo after > hello.txt && cat hello.txt\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}
		}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Create hello.txt with exactly after, read it back, and reply with one short validation sentence.",
		"tools":[{"type":"function","name":"tool_search_code","parameters":{"type":"object"}}],
		"tool_choice":"required",
		"parallel_tool_calls":false
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "tool_search_code" {
		t.Fatalf("expected normalized tool_search_code function call, got %#v", out.Output)
	}
	for _, want := range []string{`"code":"`, `openclaw.tools.call`, `hello.txt`} {
		if !strings.Contains(out.Output[0].Arguments, want) {
			t.Fatalf("expected normalized arguments to contain %q, got %q", want, out.Output[0].Arguments)
		}
	}
	if strings.Contains(out.Output[0].Arguments, `"cmd":`) {
		t.Fatalf("expected cmd arguments to be rewritten into code bridge, got %q", out.Output[0].Arguments)
	}
}

func TestResponsesStreamSuppressesRepeatedSuccessfulExecCommand(t *testing.T) {
	var upstreamCalls int
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if upstreamCalls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"type\":\"function_call\",\"call_id\":\"call_exec_1\",\"name\":\"exec_command\",\"arguments\":{\"cmd\":\"echo after > hello.txt\"}}"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```sh\\necho after > hello.txt\\n```\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	first, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var firstOut responsesEnvelope
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatal(err)
	}

	second, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"previous_response_id":"`+firstOut.ID+`",
		"input":[{"type":"function_call_output","call_id":"call_exec_1","output":{"exit_code":0,"status":"completed"}}],
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	body, _ := io.ReadAll(second.Body)
	if bytes.Contains(body, []byte(`"type":"function_call"`)) {
		t.Fatalf("repeated successful exec_command should be text, not another tool call: %s", body)
	}
	if !bytes.Contains(body, []byte("already completed successfully")) {
		t.Fatalf("expected duplicate command completion message, got: %s", body)
	}
}

func TestResponsesParsesPrefixedExecCommandFallback(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"exec_command {\"cmd\":\"echo after > hello.txt\"}"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "exec_command" {
		t.Fatalf("expected parsed exec_command function_call, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Arguments, `echo after > hello.txt`) {
		t.Fatalf("expected command to survive prefixed fallback parse, got %q", out.Output[0].Arguments)
	}
}

func TestResponsesUnwrapsNestedPrefixedExecCommandInNativeToolCall(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_exec_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"exec_command {\\\"cmd\\\":\\\"docker --version\\\"}\"}"}}]}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Start docker",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "function_call" || out.Output[0].Name != "exec_command" {
		t.Fatalf("expected normalized exec_command function_call, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Arguments, `docker --version`) {
		t.Fatalf("expected nested prefixed command to unwrap, got %q", out.Output[0].Arguments)
	}
	if strings.Contains(out.Output[0].Arguments, `exec_command {`) {
		t.Fatalf("expected wrapper exec_command prefix to be removed, got %q", out.Output[0].Arguments)
	}
}

func TestResponsesParsesPrefixedCustomToolFallback(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"apply_patch {\"patch\":\"*** Begin Patch\\n*** End Patch\"}"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Patch file",
		"tools":[{"type":"custom","name":"apply_patch","format":{"type":"grammar"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}

	var out responsesEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Output) != 1 || out.Output[0].Type != "custom_tool_call" || out.Output[0].Name != "apply_patch" {
		t.Fatalf("expected parsed apply_patch custom_tool_call, got %#v", out.Output)
	}
	if !strings.Contains(out.Output[0].Input, "*** Begin Patch") {
		t.Fatalf("expected patch payload in custom tool input, got %q", out.Output[0].Input)
	}
}

func TestResponsesStreamSuppressesEquivalentRepeatedFileWrite(t *testing.T) {
	var upstreamCalls int
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if upstreamCalls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"type\":\"function_call\",\"call_id\":\"call_exec_1\",\"name\":\"exec_command\",\"arguments\":{\"cmd\":\"echo \\\"after\\\" > hello.txt\"}}"}}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"```sh\\ntouch /tmp/codex-lab-native-e2e/hello.txt\\necho 'after' > /tmp/codex-lab-native-e2e/hello.txt\\n```\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	first, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Write file",
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var firstOut responsesEnvelope
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatal(err)
	}

	second, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"previous_response_id":"`+firstOut.ID+`",
		"input":[{"type":"function_call_output","call_id":"call_exec_1","output":{"exit_code":0,"status":"completed"}}],
		"stream":true,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	body, _ := io.ReadAll(second.Body)
	if bytes.Contains(body, []byte(`"type":"function_call"`)) {
		t.Fatalf("equivalent successful file write should be text, not another tool call: %s", body)
	}
	if !bytes.Contains(body, []byte("already completed successfully")) {
		t.Fatalf("expected duplicate command completion message, got: %s", body)
	}
}

func TestRepeatedSuccessfulExecCommandSuppressesEquivalentFileWrite(t *testing.T) {
	messages := []chatMessage{
		{
			Role: "assistant",
			ToolCalls: []chatToolCall{{
				ID:   "call_exec_1",
				Type: "function",
				Function: chatToolFunction{
					Name:      "exec_command",
					Arguments: mustJSON(map[string]string{"cmd": `echo "after" > hello.txt`}),
				},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call_exec_1",
			Content:    `{"exit_code":0,"status":"completed"}`,
		},
	}
	item := responseItem{
		Type:      "function_call",
		Name:      "exec_command",
		Arguments: mustJSON(map[string]string{"cmd": "touch /tmp/codex-lab-native-e2e/hello.txt\necho 'after' > /tmp/codex-lab-native-e2e/hello.txt"}),
	}

	if !repeatedSuccessfulExecCommand(messages, item) {
		t.Fatal("expected equivalent successful file write to be suppressed")
	}
}

func TestFileWriteEffectNormalizesEscapedEchoCommands(t *testing.T) {
	first, ok := fileWriteEffect(`echo \"after\" > hello.txt`)
	if !ok {
		t.Fatal("expected first echo write effect")
	}
	second, ok := fileWriteEffect("touch /tmp/codex-lab-native-e2e/hello.txt\necho 'after' > /tmp/codex-lab-native-e2e/hello.txt")
	if !ok {
		t.Fatal("expected second echo write effect")
	}
	if !first.equivalent(second) {
		t.Fatalf("expected equivalent writes, first=%#v second=%#v", first, second)
	}
}

func TestFallbackToolInstructionStopsAfterSuccessfulExecCommand(t *testing.T) {
	msg := fallbackToolInstruction(json.RawMessage(`[{"type":"function","name":"exec_command","parameters":{"type":"object"}}]`), nil)

	for _, want := range []string{"exact same cmd", "continue calling exec_command as needed", "tool result confirms it"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("fallback instruction missing %q: %s", want, msg.Content)
		}
	}
}

func TestFallbackToolInstructionGuidesToolSearchCodeExecution(t *testing.T) {
	msg := fallbackToolInstruction(json.RawMessage(`[{"type":"function","name":"tool_search_code","parameters":{"type":"object"}}]`), nil)

	for _, want := range []string{
		`"code":"..."`,
		"searches the hidden tool catalog",
		"Do not answer with NO_REPLY or prose before that tool-backed execution starts",
	} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("fallback instruction missing %q: %s", want, msg.Content)
		}
	}
}

func TestResponsesRejectsProseOnlyCreateReplyInStrictExecutionMode(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"NO_REPLY"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Create hello.txt with exactly hello, read it back, and reply with one short validation sentence.",
		"tools":[{"type":"function","name":"tool_search_code","parameters":{"type":"object"}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected execution_not_started conflict, got %d: %s", res.StatusCode, body)
	}
	var out errorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Type != "execution_not_started" {
		t.Fatalf("expected execution_not_started error, got %#v", out.Error)
	}
}

func TestResponsesRequiredToolChoiceRejectsInvalidFallback(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I edited the file."}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{
		"model":"qwen-local-code",
		"input":"Patch file",
		"tools":[{"type":"function","function":{"name":"apply_patch","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"apply_patch"}}
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected execution_not_started conflict, got %d: %s", res.StatusCode, body)
	}
	var out errorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Type != "execution_not_started" {
		t.Fatalf("unexpected error: %#v", out)
	}
}

func TestResponsesCompactsPreviousTranscript(t *testing.T) {
	cfg := testConfig("")
	cfg.MaxPromptChars = 245
	var upstreamRequests []map[string]any
	gateway, _ := newTestServerWithConfig(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstreamBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		upstreamRequests = append(upstreamRequests, upstreamBody)
		w.Header().Set("Content-Type", "application/json")
		if len(upstreamRequests) == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"first"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"second"}}]}`))
	}))

	longPrompt := strings.Repeat("context ", 30)
	first, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"`+longPrompt+`"} `)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	var firstOut responsesEnvelope
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatal(err)
	}

	second, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","previous_response_id":"`+firstOut.ID+`","input":"next"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("unexpected status %d: %s", second.StatusCode, body)
	}
	messages := upstreamRequests[1]["messages"].([]any)
	if len(messages) == 0 || !strings.Contains(messages[0].(map[string]any)["content"].(string), "Compacted previous context") {
		t.Fatalf("expected compacted transcript marker, got %#v", messages)
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
			ID                       string `json:"id"`
			Slug                     string `json:"slug"`
			DisplayName              string `json:"display_name"`
			DefaultReasoningLevel    string `json:"default_reasoning_level"`
			ContextWindow            int    `json:"context_window"`
			AutoCompactTokenLimit    int    `json:"auto_compact_token_limit"`
			SupportedReasoningLevels []struct {
				Effort      string `json:"effort"`
				Description string `json:"description"`
			} `json:"supported_reasoning_levels"`
		} `json:"data"`
		Models []struct {
			ID                       string `json:"id"`
			Slug                     string `json:"slug"`
			DisplayName              string `json:"display_name"`
			DefaultReasoningLevel    string `json:"default_reasoning_level"`
			ContextWindow            int    `json:"context_window"`
			AutoCompactTokenLimit    int    `json:"auto_compact_token_limit"`
			SupportedReasoningLevels []struct {
				Effort      string `json:"effort"`
				Description string `json:"description"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 1 || out.Data[0].ID != "qwen-local-code" {
		t.Fatalf("unexpected models response: %#v", out)
	}
	if out.Data[0].Slug != "qwen-local-code" {
		t.Fatalf("expected OpenAI-compatible data slug, got %#v", out.Data[0])
	}
	if out.Data[0].DisplayName != "qwen-local-code" {
		t.Fatalf("expected OpenAI-compatible display_name, got %#v", out.Data[0])
	}
	if out.Data[0].DefaultReasoningLevel != "low" || len(out.Data[0].SupportedReasoningLevels) == 0 || out.Data[0].SupportedReasoningLevels[0].Effort == "" {
		t.Fatalf("expected OpenAI-compatible reasoning metadata, got %#v", out.Data[0])
	}
	if out.Data[0].ContextWindow != defaultContextWindow || out.Data[0].AutoCompactTokenLimit != defaultAutoCompact {
		t.Fatalf("expected OpenAI-compatible context metadata, got %#v", out.Data[0])
	}
	if len(out.Models) != 1 || out.Models[0].ID != "qwen-local-code" {
		t.Fatalf("unexpected Codex models response: %#v", out)
	}
	if out.Models[0].Slug != "qwen-local-code" {
		t.Fatalf("expected Codex models slug, got %#v", out.Models[0])
	}
	if out.Models[0].DisplayName != "qwen-local-code" {
		t.Fatalf("expected Codex models display_name, got %#v", out.Models[0])
	}
	if out.Models[0].DefaultReasoningLevel != "low" || len(out.Models[0].SupportedReasoningLevels) == 0 || out.Models[0].SupportedReasoningLevels[0].Effort == "" {
		t.Fatalf("expected Codex models reasoning metadata, got %#v", out.Models[0])
	}
	if out.Models[0].ContextWindow != defaultContextWindow || out.Models[0].AutoCompactTokenLimit != defaultAutoCompact {
		t.Fatalf("expected Codex context metadata, got %#v", out.Models[0])
	}
}

func TestModelsUsesConfiguredContextMetadata(t *testing.T) {
	cfg := testConfig("")
	cfg.ContextWindow = 9000
	cfg.AutoCompactTokenLimit = 6000
	gateway, _ := newTestServerWithConfig(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("models should not hit upstream")
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodGet, gateway.URL+"/v1/models", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct {
		Models []struct {
			ContextWindow         int `json:"context_window"`
			AutoCompactTokenLimit int `json:"auto_compact_token_limit"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Models) != 1 || out.Models[0].ContextWindow != 9000 || out.Models[0].AutoCompactTokenLimit != 6000 {
		t.Fatalf("unexpected context metadata: %#v", out)
	}
}

func TestResponsesRejectsOversizedPromptBeforeUpstream(t *testing.T) {
	cfg := testConfig("")
	cfg.MaxPromptChars = 10
	gateway, _ := newTestServerWithConfig(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("oversized prompt should not hit upstream")
	}))

	payload := `{"model":"qwen-local-code","input":"this prompt is too long"}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected bad request, got %d: %s", res.StatusCode, body)
	}
	var out errorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Error.Type != "context_window_exceeded" {
		t.Fatalf("unexpected error envelope: %#v", out)
	}
	if !strings.Contains(out.Error.Message, "split the task") || !strings.Contains(out.Error.Message, "limit 10") {
		t.Fatalf("expected actionable size error, got %#v", out)
	}
}

func TestChatCompletionsRejectsOversizedPromptBeforeUpstream(t *testing.T) {
	cfg := testConfig("")
	cfg.MaxPromptChars = 10
	gateway, _ := newTestServerWithConfig(t, cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("oversized prompt should not hit upstream")
	}))

	payload := `{"model":"qwen-local-code","messages":[{"role":"user","content":"this prompt is too long"}]}`
	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected bad request, got %d: %s", res.StatusCode, body)
	}
}

func TestCompactMessagesPreservesObjectiveWorkspaceAndErrors(t *testing.T) {
	messages := []chatMessage{
		{Role: "system", Content: "Initial system instruction."},
		{Role: "user", Content: "User task:\nUpgrade Laravel without touching .env.local.\nProject path: /Users/fran.lopez/Development/smartfm/api"},
		{Role: "assistant", Content: "Modified composer.json and composer.lock"},
		{Role: "tool", Content: "PHP Fatal error: test failed with exit code 1"},
		{Role: "user", Content: "Keep docker compose as the validation path."},
		{Role: "assistant", Content: "latest"},
		{Role: "user", Content: "continue"},
	}

	compacted := compactMessages(messages, 700)
	if len(compacted) < 3 {
		t.Fatalf("expected compacted transcript, got %#v", compacted)
	}
	summary := compacted[0].Content
	if !strings.Contains(summary, "Objective:") || !strings.Contains(summary, "Upgrade Laravel") {
		t.Fatalf("expected summary to preserve objective, got %q", summary)
	}
	if !strings.Contains(summary, "/Users/fran.lopez/Development/smartfm/api") {
		t.Fatalf("expected summary to preserve workspace, got %q", summary)
	}
	if !strings.Contains(summary, "without touching .env.local") || !strings.Contains(summary, "docker compose") {
		t.Fatalf("expected summary to preserve constraints, got %q", summary)
	}
	if !strings.Contains(summary, "Fatal error") {
		t.Fatalf("expected summary to preserve recent validation error, got %q", summary)
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
	if upstreamBody["model"] != "coder" {
		t.Fatalf("expected model remap, got %#v", upstreamBody["model"])
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte(`"chat"`)) {
		t.Fatalf("unexpected passthrough body: %s", body)
	}
}

func TestChatCompletionsNormalizesEmbeddedMarkdownFunctionCall(t *testing.T) {
	var upstreamBody map[string]any
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"id\":\"chatcmpl_1\",\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"To determine the latest stable Laravel version available today, I will first search for the latest version on the official Laravel website.\\n\\n**Action:** exec_command\\n\\n```json\\n{\\\"type\\\":\\\"function_call\\\",\\\"call_id\\\":\\\"call_12345\\\",\\\"name\\\":\\\"exec_command\\\",\\\"arguments\\\":{\\\"cmd\\\":\\\"curl -s https://api.laravel.com/docs/v1/releases/stable\\\"}}\\n```\"}}]}"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"qwen-local-code",
		"messages":[{"role":"user","content":"Check latest Laravel release"}],
		"tools":[{"type":"function","function":{"name":"exec_command","parameters":{"type":"object"}}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}
	if upstreamBody["model"] != "coder" {
		t.Fatalf("expected model remap, got %#v", upstreamBody["model"])
	}
	var out chatCompletionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected normalized native tool_calls, got %#v", out.Choices)
	}
	call := out.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "exec_command" || !strings.Contains(call.Function.Arguments, "api.laravel.com/docs/v1/releases/stable") {
		t.Fatalf("unexpected normalized tool call: %#v", call)
	}
	if out.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %#v", out.Choices[0].FinishReason)
	}
}

func TestChatCompletionsNormalizesColonPrefixedExecCommand(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"Let me determine the latest stable Laravel version available today.\n\nexec_command: {\"cmd\":\"curl -s https://api.github.com/repos/laravel/framework/releases/latest | grep tag_name | cut -d '\\\"' -f 4\"}"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"qwen-local-code",
		"messages":[{"role":"user","content":"Check latest Laravel release"}],
		"tools":[{"type":"function","function":{"name":"exec_command","parameters":{"type":"object"}}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("unexpected status %d: %s", res.StatusCode, body)
	}
	var out chatCompletionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected normalized tool_calls, got %#v", out.Choices)
	}
	call := out.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "exec_command" || !strings.Contains(call.Function.Arguments, "api.github.com/repos/laravel/framework/releases/latest") {
		t.Fatalf("unexpected colon-prefixed normalized tool call: %#v", call)
	}
}

func TestChatCompletionsSynthesizesExecCommandFromBrowserProseURL(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"To determine the latest stable Laravel version available today, I will first check the official Laravel website.\n\n**Action:** Open a web browser and navigate to https://laravel.com/docs and find the upgrading section."}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"qwen-local-code",
		"messages":[{"role":"user","content":"Determine the latest stable Laravel version and migrate the project."}],
		"tools":[{"type":"function","function":{"name":"exec_command","parameters":{"type":"object"}}}]
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out chatCompletionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected synthesized tool_calls, got %#v", out.Choices)
	}
	call := out.Choices[0].Message.ToolCalls[0]
	if call.Function.Name != "exec_command" || !strings.Contains(call.Function.Arguments, "curl -fsSL https://laravel.com/docs") {
		t.Fatalf("unexpected synthesized browser-prose tool call: %#v", call)
	}
}

func TestChatCompletionsRepairsStrictExecutionNarrationToToolCall(t *testing.T) {
	var upstreamCalls int
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		if upstreamCalls == 1 {
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"I will inspect the Laravel release source first."}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl_2","choices":[{"message":{"role":"assistant","content":"exec_command: {\"cmd\":\"curl -fsSL https://api.github.com/repos/laravel/framework/releases/latest\"}"}}]}`))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{
		"model":"qwen-local-code",
		"messages":[{"role":"user","content":"Determine the latest stable Laravel version and migrate the project."}],
		"tools":[{"type":"function","function":{"name":"exec_command","parameters":{"type":"object"}}}],
		"tool_choice":"required"
	}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out chatCompletionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls != 2 {
		t.Fatalf("expected repair second upstream call, got %d", upstreamCalls)
	}
	if len(out.Choices) != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected repaired tool_calls, got %#v", out.Choices)
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
		UpstreamModel:   "coder",
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
		var upstreamBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatal(err)
		}
		if upstreamBody["stream"] != true {
			t.Fatalf("expected upstream stream=true, got %#v", upstreamBody["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"stream\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ed\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
	for _, want := range []string{"event: response.created", "event: response.output_text.delta", "event: response.completed", "streamed", `"output_text":"streamed"`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}

func TestResponsesStreamAlwaysEmitsUsageFields(t *testing.T) {
	gateway, _ := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))

	res, err := http.DefaultClient.Do(authedRequest(t, http.MethodPost, gateway.URL+"/v1/responses", strings.NewReader(`{"model":"qwen-local-code","input":"hi","stream":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	for _, want := range []string{`"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}`, `"output_text":"ok"`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}
