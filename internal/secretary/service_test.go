package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

func TestSharedChatStreamingAndAuthentication(t *testing.T) {
	const stream = "data: {\"choices\":[{\"delta\":{\"content\":\"Hola\"}}]}\n\ndata: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Error("upstream auth lost")
		}
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		if b["stream"] != true {
			t.Error("stream flag changed")
		}
		messages := b["messages"].([]any)
		if messages[0].(map[string]any)["content"] != "shared instructions" {
			t.Error("shared skill missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, stream)
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "shared instructions", t.TempDir(), map[int64]bool{7: true})
	for _, auth := range []string{"", "Bearer key"} {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"main","stream":true,"messages":[{"role":"user","content":"Hola"}]}`))
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if auth == "" && w.Code != 401 {
			t.Fatal("missing auth accepted")
		}
		if auth != "" && (w.Code != 200 || w.Body.String() != stream) {
			t.Fatalf("stream changed: %d %q", w.Code, w.Body.String())
		}
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
	messages := []Turn{{Role: "user", Content: strings.Repeat("a", 5000)}, {Role: "assistant", Content: "old"}, {Role: "user", Content: "new"}, {Role: "assistant", Content: "answer"}}
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
	if len(s.history[7]) != 0 {
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
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != 200 || !strings.Contains(result.Body.String(), summary) || strings.Contains(result.Body.String(), "lanzamiento el viernes.") {
		t.Fatalf("WebUI returned raw Granola data instead of a summary: %d %s", result.Code, result.Body.String())
	}
}
