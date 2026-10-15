package secretary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrivateAllowlistAndNoLegacyCommands(t *testing.T) {
	s := &Service{Allowed: map[int64]bool{7: true}}
	for _, m := range []Message{{From: User{ID: 8}, Chat: Chat{ID: 8, Type: "private"}}, {From: User{ID: 7}, Chat: Chat{ID: 99, Type: "group"}}} {
		if s.Permitted(m) {
			t.Fatal("unauthorised identity accepted")
		}
	}
	if !s.Permitted(Message{From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}}) {
		t.Fatal("operator rejected")
	}
	reply, err := s.Reply(context.Background(), 7, "/launch 123")
	if err != nil || !strings.Contains(reply, "/help") {
		t.Fatalf("legacy command must not execute: %q %v", reply, err)
	}
}

func TestReplyWithInteractionEmitsIntentAndLookupLifecycle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Hay un correo pendiente."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	var events []InteractionEvent
	s.Capabilities = []ReadCapability{{
		Name: "Correo", Description: "mensajes recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "correo") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			if len(events) < 2 || events[0].Kind != EventIntentRecognized || events[1].Kind != EventLookupStarted || events[1].Source != "Correo" {
				t.Errorf("lookup started before intent/source events: %+v", events)
			}
			return CapabilityResult{Text: "Hay un correo pendiente.", ResultCount: 1}, nil
		},
	}}

	answer, err := s.ReplyWithInteraction(context.Background(), 7, "telegram", "chat-7", "¿Tengo algún correo pendiente?", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil || !strings.Contains(answer, "Hay un correo pendiente") {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if len(events) < 3 || events[0].Kind != EventIntentRecognized || events[0].Meaning != InteractionLookup || events[1] != (InteractionEvent{Kind: EventLookupStarted, Meaning: InteractionLookup, Source: "Correo"}) || events[len(events)-1].Kind != EventCompleted {
		t.Fatalf("unexpected interaction lifecycle: %+v", events)
	}
}

func TestReadOnlyActionNeverEmitsActionStarted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"La tarea aparece abierta."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Jira", Description: "tareas del equipo",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "La tarea STRAT-42 aparece abierta.", ResultCount: 1}, nil
		},
	}}
	var events []InteractionEvent
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "telegram", "chat-7", "Cierra la tarea STRAT-42 en Jira", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Meaning != InteractionAction {
		t.Fatalf("meaning = %q, want action; events=%+v", events[0].Meaning, events)
	}
	for _, event := range events {
		if event.Kind == EventActionStarted {
			t.Fatalf("read-only Jira emitted action_started: %+v", events)
		}
	}
	if !strings.Contains(strings.ToLower(answer), "solo he consultado") || !strings.Contains(strings.ToLower(answer), "no he cambiado") {
		t.Fatalf("read-only action answer must state no change was made: %q", answer)
	}
}

func TestUnsupportedActionDoesNotAnnounceLookup(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"clarify\"}"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	var events []InteractionEvent
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "telegram", "chat-7", "Cierra la tarea STRAT-42", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == EventLookupStarted || event.Kind == EventActionStarted || event.Kind == EventCompleted {
			t.Fatalf("unsupported action announced work/completion: %+v", events)
		}
	}
	if len(events) < 2 || events[0].Meaning != InteractionAction || events[len(events)-1] != (InteractionEvent{Kind: EventBlocked, Meaning: InteractionAction, Reason: BlockUnavailable}) {
		t.Fatalf("unsupported action lifecycle = %+v", events)
	}
	if !strings.Contains(strings.ToLower(answer), "no tengo") {
		t.Fatalf("answer should explain the unavailable action: %q", answer)
	}
}

func TestCapabilityFailureEmitsBlockedNotCompleted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("synthesizer should not run after the connector failed")
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Correo", Description: "mensajes recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "correo") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{}, errors.New("private connector detail")
		},
	}}
	var events []InteractionEvent
	answer, err := s.ReplyWithInteraction(context.Background(), 7, "telegram", "chat-7", "¿Tengo algún correo pendiente?", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil || !strings.Contains(strings.ToLower(answer), "no he podido verificar") {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if events[len(events)-1] != (InteractionEvent{Kind: EventBlocked, Meaning: InteractionLookup, Source: "Correo", Reason: BlockConnectorFailure}) {
		t.Fatalf("connector failure must end blocked, events=%+v", events)
	}
	for _, event := range events {
		if event.Kind == EventCompleted {
			t.Fatalf("connector failure emitted completed: %+v", events)
		}
	}
}

func TestCapabilityFailureExplainsMissingRovoScopes(t *testing.T) {
	s := &Service{}
	capability := ReadCapability{
		Name: "Rovo",
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{}, errors.New("Rovo missing required scopes: read:jira:agent-interface, search:jira:agent-interface")
		},
	}
	answer, outcomes, reason, err := s.answerFromCapabilities(context.Background(), "Dame las tareas de SW-76", "Dame las tareas de SW-76", []ReadCapability{capability}, nil, nil, nil, nil)
	if err != nil || reason != BlockConnectorFailure || len(outcomes) != 1 {
		t.Fatalf("unexpected connector failure result: %q %#v %q %v", answer, outcomes, reason, err)
	}
	for _, scope := range []string{"read:jira:agent-interface", "search:jira:agent-interface"} {
		if !strings.Contains(answer, scope) {
			t.Errorf("user-facing failure did not identify missing scope %q: %q", scope, answer)
		}
	}
}

func TestNeutralMessageEmitsNoLookupOrAction(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Buenos días."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	var events []InteractionEvent
	_, err := s.ReplyWithInteraction(context.Background(), 7, "webui", "chat-web-7", "Buenos días", func(event InteractionEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != (InteractionEvent{Kind: EventIntentRecognized, Meaning: InteractionNeutral}) || events[1].Kind != EventCompleted {
		t.Fatalf("neutral message emitted unexpected interaction: %+v", events)
	}
}

func TestSharedChatStreamingAndAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Error("upstream auth lost")
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["stream"] != true {
			t.Error("secretary should preserve model token streaming")
		}
		messages := b["messages"].([]any)
		if messages[0].(map[string]any)["content"] != "shared instructions" {
			t.Error("shared instructions missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Ho\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"la\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "shared instructions", t.TempDir(), map[int64]bool{7: true})
	for _, auth := range []string{"", "Bearer key"} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":true,"messages":[{"role":"user","content":"Hola"}]}`))
		r.Header.Set("Authorization", auth)
		if auth != "" {
			r.Header.Set("X-OpenWebUI-User-Id", "owner")
			r.Header.Set("X-OpenWebUI-User-Role", "admin")
		}
		r.Header.Set("X-OpenWebUI-Chat-Id", "stream-test")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if auth == "" && w.Code != 401 {
			t.Fatal("missing auth accepted")
		}
		if auth != "" && (w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"Ho"`) || !strings.Contains(w.Body.String(), `"content":"la"`) || !strings.Contains(w.Body.String(), "[DONE]")) {
			t.Fatalf("stream response invalid: %d %q", w.Code, w.Body.String())
		}
	}
}

func TestSharedChatStreamingRendersInteractionBeforeAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Resumen útil.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Granola", Description: "reuniones recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "reuniones") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Notas de las reuniones de hoy.", ResultCount: 1}, nil
		},
	}}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":true,"messages":[{"role":"user","content":"Resume las reuniones de hoy."}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "interaction-stream")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	body := result.Body.String()
	reaction := strings.Index(body, `"content":"🔍"`)
	progress := strings.Index(body, `"content":"Déjame revisar Granola."`)
	answer := strings.Index(body, `"content":"Resumen útil."`)
	if result.Code != http.StatusOK || reaction < 0 || progress <= reaction || answer <= progress || !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream interaction order/status invalid: status=%d reaction=%d progress=%d answer=%d body=%q", result.Code, reaction, progress, answer, body)
	}
}

func TestSharedChatNonStreamingProgressOnlyWhenLookupStarts(t *testing.T) {
	t.Run("lookup includes semantic and source feedback", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"choices":[{"message":{"content":"Hay dos tareas importantes."}}]}`)
		}))
		defer upstream.Close()
		s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
		s.Capabilities = []ReadCapability{{
			Name: "Jira", Description: "tareas actuales",
			Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
			Read: func(context.Context, string) (CapabilityResult, error) {
				return CapabilityResult{Text: "Tarea uno y tarea dos.", ResultCount: 1}, nil
			},
		}}
		result := performOpenWebUIChat(t, s, "¿Qué tareas tengo en Jira?", false)
		var response struct {
			Choices []struct {
				Message Turn `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Choices) != 1 || !strings.Contains(response.Choices[0].Message.Content, "🔍") || !strings.Contains(response.Choices[0].Message.Content, "Déjame revisar Jira.") || !strings.Contains(response.Choices[0].Message.Content, "Hay dos tareas importantes.") {
			t.Fatalf("non-streaming lookup response missing source feedback: %q", result.Body.String())
		}
	})

	t.Run("neutral response has no source feedback", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"choices":[{"message":{"content":"Buenos días."}}]}`)
		}))
		defer upstream.Close()
		s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
		result := performOpenWebUIChat(t, s, "Buenos días", false)
		if strings.Contains(result.Body.String(), "Déjame revisar") || strings.Contains(result.Body.String(), "🔍") {
			t.Fatalf("neutral response must not invent source feedback: %q", result.Body.String())
		}
	})

	t.Run("read-only action is acknowledged without claiming a change", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"choices":[{"message":{"content":"He encontrado la tarea STRAT-42."}}]}`)
		}))
		defer upstream.Close()
		s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
		s.Capabilities = []ReadCapability{{
			Name: "Jira", Description: "tareas actuales",
			Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "jira") },
			Read: func(context.Context, string) (CapabilityResult, error) {
				return CapabilityResult{Text: "La tarea STRAT-42 sigue abierta.", ResultCount: 1}, nil
			},
		}}
		result := performOpenWebUIChat(t, s, "Cierra la tarea STRAT-42 en Jira", false)
		body := result.Body.String()
		if !strings.Contains(body, "👍") || !strings.Contains(body, "Buscando en Jira; esta conexión solo permite consultar.") || !strings.Contains(body, "No he cambiado nada; solo he consultado Jira") {
			t.Fatalf("read-only action feedback is missing or claims a write: %q", body)
		}
	})

	t.Run("unsupported action has no source feedback", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "Clasifica la intención") {
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"clarify\"}"}}]}`)
				return
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"No hay herramienta disponible."}}]}`)
		}))
		defer upstream.Close()
		s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
		result := performOpenWebUIChat(t, s, "Cierra la tarea STRAT-42", false)
		if strings.Contains(result.Body.String(), "Déjame revisar") || strings.Contains(result.Body.String(), "🔍") {
			t.Fatalf("unsupported action must not claim a lookup started: %q", result.Body.String())
		}
	})
}

func performOpenWebUIChat(t *testing.T, service *Service, prompt string, streaming bool) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"model": "main", "stream": streaming,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(payload)))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "interaction-nonstream")
	result := httptest.NewRecorder()
	service.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("Open WebUI returned status %d: %s", result.Code, result.Body.String())
	}
	return result
}

func TestOpenWebUINaturalLanguageUsesSharedRouterAndMemory(t *testing.T) {
	const source = "Notas de Granola: lanzamiento el viernes."
	const summary = "Granola indica que el lanzamiento queda para el viernes."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, source) {
			t.Errorf("shared router did not pass Granola context to the model: %#v", body.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s := New(upstream.URL, "key", "shared secretary instructions", dir, map[int64]bool{7: true})
	reader := &intentSource{granola: source}
	s.Sources = reader
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":false,"messages":[{"role":"user","content":"Resume las reuniones que he tenido."}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "natural-language-test")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), summary) || reader.granolaCalls != 1 || modelCalls != 1 {
		t.Fatalf("Open WebUI answer=%q status=%d Granola=%d model=%d", result.Body.String(), result.Code, reader.granolaCalls, modelCalls)
	}
	webHistory := s.history[s.historyKey(7, "openwebui", "natural-language-test")]
	if len(webHistory) != 2 || webHistory[0].Content != "Resume las reuniones que he tenido." {
		t.Fatalf("Open WebUI exchange did not enter its conversation history: %#v", webHistory)
	}
	if _, err := os.Stat(s.dailyMemoryPath(7, time.Now())); err != nil {
		t.Fatalf("Open WebUI exchange did not enter daily durable memory: %v", err)
	}
}

func TestOpenWebUITaskQuestionUsesRovoThroughSharedRouter(t *testing.T) {
	const question = "¿Qué tareas importantes debería priorizar hoy?"
	const source = "Rovo: STRAT-18 · Preparar el plan de despliegue antes del mediodía."
	const summary = "Prioriza preparar el plan de despliegue antes del mediodía."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, question) {
			t.Errorf("shared router did not pass Rovo results to the model: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "shared secretary instructions", t.TempDir(), map[int64]bool{7: true})
	reader := &rovoIntentSource{intentSource: intentSource{granola: "Granola not queried for this task request."}, rovo: source}
	s.Sources = reader
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":false,"messages":[{"role":"user","content":"`+question+`"}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "rovo-task-test")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), summary) || reader.rovoCalls != 1 || reader.lastQuery != question || reader.granolaCalls != 0 || modelCalls != 1 {
		t.Fatalf("Open WebUI answer=%q status=%d Rovo=%d Granola=%d model=%d", result.Body.String(), result.Code, reader.rovoCalls, reader.granolaCalls, modelCalls)
	}
}

func TestOpenWebUIRejectsUntrustedOrAmbiguousIdentity(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		io.WriteString(w, `{"choices":[{"message":{"content":"should not run"}}]}`)
	}))
	defer upstream.Close()
	request := func(role string, allowed map[int64]bool) *httptest.ResponseRecorder {
		s := New(upstream.URL, "key", "instructions", t.TempDir(), allowed)
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"Hola"}]}`))
		r.Header.Set("Authorization", "Bearer key")
		r.Header.Set("X-OpenWebUI-User-Id", "owner")
		r.Header.Set("X-OpenWebUI-User-Role", role)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	for _, w := range []*httptest.ResponseRecorder{
		request("user", map[int64]bool{7: true}),
		request("admin", map[int64]bool{7: true, 8: true}),
	} {
		if w.Code != http.StatusForbidden {
			t.Fatalf("untrusted or ambiguous identity accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("unauthorized Open WebUI request reached model: %d calls", calls)
	}
}

func TestSecretaryMetricsEndpointExposesVoiceMetrics(t *testing.T) {
	service := New("http://127.0.0.1:1", "secret-key", "instructions", t.TempDir(), map[int64]bool{7: true})
	service.VoiceMetrics().ObservePhase(VoicePhaseSTT, VoiceResultOK, 250*time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `lab_secretary_voice_phase_calls_total{phase="stt",result="ok"}`) {
		t.Fatalf("unauthenticated metrics endpoint unavailable: %d %s", response.Code, response.Body.String())
	}
}

func TestTelegramHistoryPersistsAndReset(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		got := len(b["messages"].([]any))
		want := 2
		if calls == 2 {
			want = 4
		}
		if got != want {
			t.Errorf("history length %d want %d", got, want)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"Respuesta"}}]}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		s := New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
		if reply, err := s.Reply(context.Background(), 7, "Hola"); err != nil || reply != "Respuesta" {
			t.Fatalf("reply %q %v", reply, err)
		}
	}
	s := New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reply(context.Background(), 7, "Nuevo"); err != nil {
		t.Fatal(err)
	}
}

func TestMissingSourcesAreExplicitAndHistoryIsBounded(t *testing.T) {
	s := New("http://127.0.0.1:8081", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	reply, err := s.Reply(context.Background(), 7, "/resumen")
	if err != nil || !strings.Contains(reply, "Jira: no conectado") || !strings.Contains(reply, "Granola: no conectado") {
		t.Fatalf("missing sources hidden: %q %v", reply, err)
	}
	messages := []Turn{{Role: "user", Content: strings.Repeat("a", 13000)}, {Role: "assistant", Content: "old"}, {Role: "user", Content: "new"}, {Role: "assistant", Content: "answer"}}
	got := bounded(messages)
	if len(got) != 2 || got[0].Content != "new" {
		t.Fatal("unbounded history or split exchange")
	}
}

func TestUpstreamFailureDoesNotPersistFalseAnswer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "Hola"); err == nil {
		t.Fatal("upstream error ignored")
	}
	if len(s.history[s.historyKey(7, "telegram", "7")]) != 0 {
		t.Fatal("failed exchange persisted")
	}
}

func TestBlankMessageAndWebUIReadCommand(t *testing.T) {
	s := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, " "); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":true,"messages":[{"role":"user","content":"/resumen"}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "read-command-test")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Body.String(), "Jira: no conectado") || !strings.Contains(result.Body.String(), "[DONE]") {
		t.Fatalf("read command did not use common service: %d %s", result.Code, result.Body.String())
	}
}

func TestSourceCommandSurvivesRestartInConversationContext(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) != 4 || body.Messages[1].Content != "/resumen" || !strings.Contains(body.Messages[2].Content, "Jira: no conectado") {
			t.Errorf("source context lost: %#v", body.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"No hay fuentes conectadas."}}]}`)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s := New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "/resumen"); err != nil {
		t.Fatal(err)
	}
	s = New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "¿Qué falta conectar?"); err != nil {
		t.Fatal(err)
	}
}

type executiveSummarySource struct{ granola string }

func (s executiveSummarySource) Jira(context.Context) (string, error) {
	return "Jira: no conectado", nil
}
func (s executiveSummarySource) Granola(context.Context) (string, error) { return s.granola, nil }
func (s executiveSummarySource) Status() map[string]bool                 { return map[string]bool{"Granola": true} }

func TestTelegramMeetingsCommandReturnsExecutiveSummaryAndPersistsNoRawNotes(t *testing.T) {
	const source = "Notas de Granola (últimos 7 días; muestra de hasta 5 reuniones):\nPlanificación de lanzamiento · 2026-10-12\nAcordamos publicar el viernes. Ana preparará el anuncio."
	const summary = "Resumen ejecutivo\n- El lanzamiento queda para el viernes.\n\nAcciones\n- Ana: preparar el anuncio."
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "No inventes") || !strings.Contains(body.Messages[0].Content, "transcripciones") || !strings.Contains(body.Messages[1].Content, source) {
			t.Errorf("missing source-grounded summarization prompt: %#v", body.Messages)
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": summary}}}})
		w.Write(response)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s := New(upstream.URL, "key", "instrucciones compartidas", dir, map[int64]bool{7: true})
	s.Sources = executiveSummarySource{granola: source}
	reply, err := s.Reply(context.Background(), 7, "/reuniones")
	if err != nil || reply != summary || modelCalls != 1 {
		t.Fatalf("summary %q, calls %d, err %v", reply, modelCalls, err)
	}
	stored, err := os.ReadFile(s.historyPath(7))
	if err != nil || strings.Contains(string(stored), "Acordamos publicar") || !strings.Contains(string(stored), "El lanzamiento queda para el viernes") {
		t.Fatalf("history stored raw notes or lost summary: %s, %v", stored, err)
	}
}

func TestWebUIReunionesUsesExecutiveSummaryModel(t *testing.T) {
	const summary = "Resumen ejecutivo: la decisión principal es lanzar el viernes."
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "Notas de Granola") {
			t.Errorf("source context missing from model request: %#v", body.Messages)
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": summary}}}})
		w.Write(response)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instrucciones compartidas", t.TempDir(), map[int64]bool{7: true})
	s.Sources = executiveSummarySource{granola: "Notas de Granola (últimos 7 días; muestra de hasta 5 reuniones):\nLanzamiento · 2026-10-12\nAcordamos publicar el viernes."}
	request := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":false,"messages":[{"role":"user","content":"/reuniones"}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "meeting-summary-test")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Body.String(), summary) || strings.Contains(result.Body.String(), "lanzamiento el viernes.") {
		t.Fatalf("WebUI returned raw Granola data instead of a summary: %d %s", result.Code, result.Body.String())
	}
}

type intentSource struct {
	granola      string
	granolaCalls int
	jiraCalls    int
}

func (s *intentSource) Jira(context.Context) (string, error) {
	s.jiraCalls++
	return "Jira data must not be queried", nil
}
func (s *intentSource) Granola(context.Context) (string, error) {
	s.granolaCalls++
	return s.granola, nil
}
func (s *intentSource) Status() map[string]bool { return map[string]bool{"Granola": true} }

type rovoIntentSource struct {
	intentSource
	rovo      string
	rovoCalls int
	lastQuery string
}

func (s *rovoIntentSource) Rovo(_ context.Context, query string) (string, error) {
	s.rovoCalls++
	s.lastQuery = query
	return s.rovo, nil
}

func TestMeetingTaskQuestionRoutesToGranolaAndRovo(t *testing.T) {
	const question = "¿Qué tareas importantes tengo pendientes hoy según las reuniones de esta mañana?"
	const granolaSource = "Granola: Plan de lanzamiento · Revisar propuesta antes de las 16:00."
	const source = "Rovo: Plan de lanzamiento · Revisar propuesta antes de las 16:00."
	const summary = "Prioriza revisar la propuesta antes de las 16:00."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, granolaSource) || !strings.Contains(joined, question) {
			t.Errorf("model did not receive the question and both source results: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &rovoIntentSource{intentSource: intentSource{granola: granolaSource}, rovo: source}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader

	answer, err := s.Reply(context.Background(), 7, question)
	if err != nil || answer != summary || modelCalls != 1 || sourceReader.rovoCalls != 1 || sourceReader.lastQuery != question || sourceReader.granolaCalls != 1 {
		t.Fatalf("answer=%q model=%d Rovo=%d Granola=%d query=%q err=%v", answer, modelCalls, sourceReader.rovoCalls, sourceReader.granolaCalls, sourceReader.lastQuery, err)
	}
}

func TestMeetingSummaryRoutesToGranolaWhenNoTaskIntentIsPresent(t *testing.T) {
	const question = "Resúmeme las reuniones de esta mañana y las decisiones que tomamos."
	const source = "Notas de Granola: planificación · Acordamos mantener el calendario actual."
	const summary = "Acordasteis mantener el calendario actual."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, question) {
			t.Errorf("model did not receive Granola notes and question: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &rovoIntentSource{intentSource: intentSource{granola: source}, rovo: "Rovo must not be consulted for a meeting summary."}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader

	answer, err := s.Reply(context.Background(), 7, question)
	if err != nil || answer != summary || modelCalls != 1 || sourceReader.granolaCalls != 1 || sourceReader.rovoCalls != 0 {
		t.Fatalf("answer=%q model=%d Granola=%d Rovo=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, sourceReader.rovoCalls, err)
	}
}

func TestNaturalLanguageMeetingTaskQueriesGranolaAndRovoAndSummarizes(t *testing.T) {
	const question = "Hola, dona. Oye, ¿qué tareas tengo pendientes de hacer según las reuniones que tenéis?"
	const source = "Notas de Granola (últimos 7 días):\nPlanificación · 2026-10-12\nAcordamos publicar el viernes."
	const rovoSource = "Rovo: buscar tareas asociadas a las decisiones."
	const summary = "Pendiente: publicar el viernes, según Planificación del 12 de octubre."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, rovoSource) || !strings.Contains(joined, question) {
			t.Errorf("model did not receive the user question and both source results: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &rovoIntentSource{intentSource: intentSource{granola: source}, rovo: rovoSource}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader
	answer, err := s.Reply(context.Background(), 7, question)
	if err != nil || answer != summary || modelCalls != 1 || sourceReader.granolaCalls != 1 || sourceReader.rovoCalls != 1 || sourceReader.jiraCalls != 0 {
		t.Fatalf("answer=%q model=%d granola=%d rovo=%d jira=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, sourceReader.rovoCalls, sourceReader.jiraCalls, err)
	}
}

type queryAwareGranolaSource struct {
	intentSource
	query        string
	queryCalls   int
	queryContent string
}

func (s *queryAwareGranolaSource) GranolaQueryResult(_ context.Context, query string) (CapabilityResult, error) {
	s.queryCalls++
	s.query = query
	return CapabilityResult{Text: s.queryContent, ResultCount: 1}, nil
}

func TestGranolaCapabilityPassesQuestionToQueryAwareConnector(t *testing.T) {
	const question = "¿Quién revisa los textos en español e inglés de Stonecare según la reunión del 14 de octubre?"
	source := &queryAwareGranolaSource{queryContent: "Iris revisa textos; Fran implementa."}
	s := &Service{Sources: source}
	capabilities := s.capabilities()
	if len(capabilities) == 0 {
		t.Fatal("Granola capability was not registered")
	}
	result, err := capabilities[0].Read(context.Background(), question)
	if err != nil || result.Text != source.queryContent || source.queryCalls != 1 || source.query != question || source.granolaCalls != 0 {
		t.Fatalf("result=%+v query=%q queryCalls=%d legacyCalls=%d err=%v", result, source.query, source.queryCalls, source.granolaCalls, err)
	}
}

func TestMeetingFollowUpUsesAnchorWithoutRepeatingPreviousTaskDetails(t *testing.T) {
	s := &Service{Capabilities: []ReadCapability{
		{Name: "Granola", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
		{Name: "Rovo", Match: func(string) bool { return true }, Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
	}}
	previous := "En la reunión de Stonecare del 14 de octubre, dime quién debía enviar las imágenes y qué tareas tenía Fran para dejar la web lista."
	followUp := "¿Y qué tareas relacionadas con los textos en castellano e inglés le quedan a Fran?"
	capabilities, matched, readQuery := s.capabilitiesFor(followUp, []Turn{{Role: "user", Content: previous}})
	if !matched || len(capabilities) != 2 {
		t.Fatalf("capabilities=%v matched=%v query=%q", capabilities, matched, readQuery)
	}
	if !strings.Contains(readQuery, "Stonecare del 14 de octubre") || !strings.Contains(readQuery, followUp) {
		t.Fatalf("read query lost meeting anchor or follow-up: %q", readQuery)
	}
	if strings.Contains(readQuery, "debía enviar las imágenes") || strings.Contains(readQuery, "qué tareas tenía Fran") {
		t.Fatalf("read query repeated unrelated details from the previous task, which can bias attribution: %q", readQuery)
	}
}

func TestDirectSourceAnswerSurvivesSecondaryConnectorFailure(t *testing.T) {
	const sourceAnswer = "Fran: aplicar los cambios de la web. Paula y Alba: enviar las imágenes. [[1]](https://notes.granola.ai/d/meeting-1)"
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"invented answer"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), nil)
	capabilities := []ReadCapability{
		{Name: "Granola", Description: "notas", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: sourceAnswer, ResultCount: 1, DirectAnswer: true}, nil
		}},
		{Name: "Rovo", Description: "Jira", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{}, errors.New("Rovo missing required scopes: read:jira:agent-interface")
		}},
	}
	answer, outcomes, reason, err := s.answerFromCapabilities(context.Background(), "¿Qué tareas quedaron asignadas?", "¿Qué tareas quedaron asignadas?", capabilities, nil, nil, nil, nil)
	if err != nil || reason != "" || len(outcomes) != 2 || modelCalls != 0 {
		t.Fatalf("answer=%q outcomes=%+v reason=%q calls=%d err=%v", answer, outcomes, reason, modelCalls, err)
	}
	if !strings.Contains(answer, sourceAnswer) || !strings.Contains(answer, "Rovo (faltan permisos: read:jira:agent-interface)") || strings.Contains(answer, "invented answer") {
		t.Fatalf("direct source answer or connector limitation was lost: %q", answer)
	}
}

func TestEmptySecondarySourceIsNotPresentedAsEvidence(t *testing.T) {
	const question = "¿Quién envía las imágenes y qué tiene que hacer Fran?"
	const meetingNotes = "Próximos pasos:\n- Enviar imágenes adaptadas a Fran (Paula, Alba).\n- Aplicar cambios y dejar la web lista para el viernes (Fran)."
	var modelInput string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, message := range body.Messages {
			modelInput += message.Content + "\n"
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"Paula y Alba envían las imágenes. Fran aplica los cambios y deja la web lista para el viernes."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), nil)
	capabilities := []ReadCapability{
		{Name: "Granola", Description: "notas", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: meetingNotes, ResultCount: 1}, nil
		}},
		{Name: "Rovo", Description: "Jira", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{}, nil
		}},
	}
	answer, _, _, err := s.answerFromCapabilities(context.Background(), question, question, capabilities, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(modelInput, meetingNotes) || strings.Contains(modelInput, "Rovo — Jira") || strings.Contains(modelInput, "no devolvió resultados") {
		t.Fatalf("model received a source without evidence: %q", modelInput)
	}
	if !strings.Contains(answer, "Rovo") || !strings.Contains(answer, "no devolvió resultados") {
		t.Fatalf("empty secondary-source coverage was not reported separately: %q", answer)
	}
}

func TestSourcedAnswerDoesNotMixConversationMemory(t *testing.T) {
	const unrelated = "Cambia Tu Euro KYC operación bancaria"
	const source = "Granola: Stonecare, Iris revisa textos y Fran implementa el viernes."
	var gotPrompt string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, message := range body.Messages {
			gotPrompt += message.Content + "\n"
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"Iris revisa los textos; Fran implementa los cambios para el viernes."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), nil)
	capability := ReadCapability{Name: "Granola", Description: "notas", Read: func(context.Context, string) (CapabilityResult, error) {
		return CapabilityResult{Text: source, ResultCount: 1}, nil
	}}
	_, _, _, err := s.answerFromCapabilities(context.Background(), "¿Quién revisa Stonecare?", "¿Quién revisa Stonecare?", []ReadCapability{capability}, []conversationMemory{{User: "qué pasó", Assistant: unrelated}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPrompt, source) || strings.Contains(gotPrompt, unrelated) {
		t.Fatalf("sourced synthesis included unrelated memory or omitted source: %q", gotPrompt)
	}
}

func TestTaskSynthesisPromptPreservesEveryExplicitAssignment(t *testing.T) {
	var systemPrompt string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) > 0 {
			systemPrompt = body.Messages[0].Content
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"resumen"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), nil)
	_, err := s.answerWithContext(context.Background(), "¿Qué tareas quedaron asignadas a cada persona?", nil, nil, false, nil, "Granola: Paula y Alba enviarán imágenes; Iris revisará los textos.", "Granola")
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"cada persona mencionada", "acciones compartidas", "tareas que corresponden al usuario"} {
		if !strings.Contains(strings.ToLower(systemPrompt), phrase) {
			t.Errorf("task synthesis prompt omitted %q: %s", phrase, systemPrompt)
		}
	}
}

func TestGranolaCapabilityCarriesToNaturalTaskFollowUp(t *testing.T) {
	const firstQuestion = "Hola, dona. Oye, ¿qué tareas tengo pendientes de hacer según las reuniones que tenéis?"
	const followUp = "Hola, dona. Oye, ¿qué cosas tenemos que hacer para hoy?"
	const source = "Notas de Granola (últimos 7 días):\nStandup semanal · 2026-10-13\nEnviar correos de configuración de Active Directory a Minicor."
	const summary = "Hoy: enviar los correos de configuración de Active Directory a Minicor."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) {
			t.Errorf("expected Granola notes in synthesis context: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &rovoIntentSource{intentSource: intentSource{granola: source}, rovo: "Rovo: acción asociada a la reunión."}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader
	if _, err := s.Reply(context.Background(), 7, firstQuestion); err != nil {
		t.Fatal(err)
	}
	answer, err := s.Reply(context.Background(), 7, followUp)
	if err != nil || !strings.HasPrefix(answer, summary) || modelCalls != 2 || sourceReader.granolaCalls != 2 || sourceReader.rovoCalls != 2 || sourceReader.jiraCalls != 0 {
		t.Fatalf("answer=%q model=%d granola=%d rovo=%d jira=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, sourceReader.rovoCalls, sourceReader.jiraCalls, err)
	}
}

func TestMeetingFollowUpKeepsMeetingContextInConnectorQuery(t *testing.T) {
	const firstQuestion = "Resume la reunión de Stonecare del 14 de octubre."
	const followUp = "¿Qué tareas concretas quedaron asignadas a cada persona en esa misma reunión y cuáles me corresponden a mí?"
	const source = "Granola: Iris revisa los textos y Fran implementa los cambios."
	const summary = "Iris revisa los textos; Fran implementa los cambios."
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Granola", Description: "notas de reuniones",
		Match: func(query string) bool {
			return localIntent(query) == intentCapability && containsAny(normalizedIntentText(query), "reunion", "reuniones")
		},
		Read: func(_ context.Context, query string) (CapabilityResult, error) {
			gotQuery = query
			return CapabilityResult{Text: source, ResultCount: 1}, nil
		},
	}, {
		Name: "Rovo", Description: "tareas",
		Match: rovoTaskIntent,
		Read: func(_ context.Context, query string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Rovo sin coincidencias", ResultCount: 0}, nil
		},
	}}
	if _, err := s.Reply(context.Background(), 7, firstQuestion); err != nil {
		t.Fatal(err)
	}
	answer, err := s.Reply(context.Background(), 7, followUp)
	if err != nil || answer != summary+"\n\nRovo no devolvió resultados en esta consulta." {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if !strings.Contains(gotQuery, "Stonecare del 14 de octubre") || !strings.Contains(gotQuery, followUp) {
		t.Fatalf("connector query lost meeting reference: %q", gotQuery)
	}
}

func TestGranolaEmptyResultDoesNotLetModelInventGenericAdvice(t *testing.T) {
	modelCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"respuesta inventada"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &intentSource{}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader
	answer, err := s.Reply(context.Background(), 7, "Resume las reuniones de esta mañana.")
	if err != nil || !strings.Contains(answer, "No he encontrado notas o resultados") || modelCalls != 0 || sourceReader.granolaCalls != 1 {
		t.Fatalf("answer=%q model=%d granola=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, err)
	}
}

func TestTaskQuestionWithoutMeetingNameUsesLLMClassifier(t *testing.T) {
	const question = "¿Qué tareas me quedan para hoy?"
	const source = "Notas de Granola: hoy quedó pendiente enviar el informe a Marta."
	const summary = "Te queda enviar el informe a Marta."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		prompt := body.Messages[0].Content
		if strings.Contains(prompt, "Clasifica la intención") {
			if strings.Contains(prompt, "Granola") && strings.Contains(prompt, "notas recientes de reuniones") {
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"tool\"}"}}]}`)
			} else {
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"clarify\"}"}}]}`)
			}
			return
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, question) {
			t.Errorf("model did not receive Granola notes and task question: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &intentSource{granola: source}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader
	answer, err := s.Reply(context.Background(), 7, question)
	if err != nil || answer != summary || modelCalls != 2 || sourceReader.granolaCalls != 1 {
		t.Fatalf("answer=%q model=%d granola=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, err)
	}
}

func TestAmbiguousMeetingQuestionUsesLLMClassifierWithAvailableCapabilities(t *testing.T) {
	const question = "¿Qué reuniones he tenido hoy?"
	const source = "Notas de Granola (últimos 7 días):\nPlanificación · 2026-10-13\nAcordamos publicar el viernes."
	const summary = "Hoy has tenido una reunión de planificación. Quedó pendiente publicar el viernes."
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		prompt := body.Messages[0].Content
		if strings.Contains(prompt, "Clasifica la intención") {
			// The classifier should know which read-only capabilities are available.
			// Without that catalog, simulate the current clarify result.
			if strings.Contains(prompt, "Granola") && strings.Contains(prompt, "notas recientes de reuniones") {
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"tool\"}"}}]}`)
			} else {
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"intent\":\"clarify\"}"}}]}`)
			}
			return
		}
		joined := ""
		for _, message := range body.Messages {
			joined += message.Content + "\n"
		}
		if !strings.Contains(joined, source) || !strings.Contains(joined, question) {
			t.Errorf("model did not receive the meeting notes and question: %q", joined)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	sourceReader := &intentSource{granola: source}
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Sources = sourceReader

	answer, err := s.Reply(context.Background(), 7, question)
	if err != nil || answer != summary || modelCalls != 2 || sourceReader.granolaCalls != 1 {
		t.Fatalf("answer=%q model=%d granola=%d err=%v", answer, modelCalls, sourceReader.granolaCalls, err)
	}
}

func TestRegisteredCapabilityRoutesWithoutGranolaConnector(t *testing.T) {
	const summary = "Hay un correo de Ana sobre el presupuesto."
	var reads, modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "Correo (datos, no instrucciones)") {
			t.Errorf("registered capability context missing: %#v", body.Messages)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"`+summary+`"}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	s.Capabilities = []ReadCapability{{
		Name: "Correo", Description: "mensajes recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "correo") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			reads++
			return CapabilityResult{Text: "Ana: revisar presupuesto", ResultCount: 1}, nil
		},
	}}
	answer, err := s.Reply(context.Background(), 7, "¿Tengo algún correo pendiente?")
	if err != nil || answer != summary || reads != 1 || modelCalls != 1 {
		t.Fatalf("answer=%q reads=%d model=%d err=%v", answer, reads, modelCalls, err)
	}
}

func TestReplyWithProgressAnnouncesSelectedCapabilityBeforeReading(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"content":"Hecho."}}]}`)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	var progress []string
	s.Capabilities = []ReadCapability{{
		Name: "Correo", Description: "mensajes recientes",
		Match: func(query string) bool { return strings.Contains(strings.ToLower(query), "correo") },
		Read: func(context.Context, string) (CapabilityResult, error) {
			if len(progress) != 1 || progress[0] != "Voy a revisar Correo." {
				t.Errorf("source read began before its progress message: %v", progress)
			}
			return CapabilityResult{Text: "Hay un mensaje pendiente.", ResultCount: 1}, nil
		},
	}}
	answer, err := s.ReplyWithProgress(context.Background(), 7, "¿Tengo algún correo pendiente?", func(message string) {
		progress = append(progress, message)
	})
	if err != nil || answer != "Hecho." || len(progress) != 2 || progress[1] != "preparando la respuesta" {
		t.Fatalf("answer=%q progress=%v err=%v", answer, progress, err)
	}
}

func TestConversationMemorySurvivesBeyondRecentHistoryAndCanBeForgotten(t *testing.T) {
	var requests int
	var lastMessages []Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		lastMessages = body.Messages
		answer := "Respuesta de relleno"
		if strings.Contains(body.Messages[len(body.Messages)-1].Content, "servicio Alfa") && requests == 1 {
			answer = "El acuerdo Alfa fue desplegarlo el lunes."
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
		w.Write(response)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	s := New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "Resume el acuerdo del servicio Alfa."); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.Reply(context.Background(), 7, fmt.Sprintf("Dime un dato general número %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	s = New(upstream.URL, "key", "instructions", dir, map[int64]bool{7: true})
	if _, err := s.Reply(context.Background(), 7, "¿Qué acordamos del servicio Alfa?"); err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, message := range lastMessages {
		joined += message.Content + "\n"
	}
	if !strings.Contains(joined, "El acuerdo Alfa fue desplegarlo el lunes.") || !strings.Contains(joined, "servicio Alfa") {
		t.Fatalf("older conversation was not retrieved into context: %q", joined)
	}
	memoryPath := s.dailyMemoryPath(7, time.Now())
	if _, err := os.Stat(memoryPath); err != nil {
		t.Fatalf("conversation archive was not persisted: %v", err)
	}
	if _, err := s.Reply(context.Background(), 7, "/forget"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(memoryPath); !os.IsNotExist(err) {
		t.Fatalf("/forget left persistent memory in place: stat err=%v", err)
	}
}
