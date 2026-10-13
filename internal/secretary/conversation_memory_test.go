package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedHistoryKeepsTenExchangesAndExpiresAfterTwoHours(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	turns := make([]Turn, 0, 24)
	for i := 0; i < 12; i++ {
		at := now.Add(-time.Duration(12-i) * time.Minute)
		turns = append(turns, Turn{Role: "user", Content: strings.Repeat("p", 500), At: at}, Turn{Role: "assistant", Content: strings.Repeat("r", 500), At: at})
	}
	got := boundedAt(turns, now)
	if len(got) != 20 || got[0].At != turns[4].At {
		t.Fatalf("history kept %d turns, first timestamp %v; want latest 10 exchanges", len(got), got[0].At)
	}
	for i := range got {
		got[i].At = now.Add(-3 * time.Hour)
	}
	if expired := boundedAt(got, now); len(expired) != 0 {
		t.Fatalf("history retained %d expired turns", len(expired))
	}
}

func TestConversationHistoryIsScopedByChannelAndConversation(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	for _, turn := range []struct{ channel, conversation, text string }{
		{"telegram", "chat-a", "secreto del chat A"},
		{"telegram", "chat-b", "mensaje del chat B"},
		{"telegram", "chat-a", "¿Qué te he dicho?"},
		{"openwebui", "chat-c", "contenido del chat C"},
	} {
		if _, err := s.ReplyInConversation(context.Background(), 7, turn.channel, turn.conversation, turn.text); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(joinTurnContent(modelMessages[2]), "secreto del chat A") {
		t.Fatal("same conversation did not retain its context")
	}
	for _, index := range []int{1, 3} {
		if strings.Contains(joinNonSystemTurnContent(modelMessages[index]), "secreto del chat A") {
			t.Fatalf("conversation %d received another chat's short-term history: %s", index, joinNonSystemTurnContent(modelMessages[index]))
		}
	}
}

func TestOpenWebUIUsesChatIDHeaderForHistoryScope(t *testing.T) {
	var modelMessages [][]Turn
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []Turn `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		modelMessages = append(modelMessages, request.Messages)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer upstream.Close()
	s := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	for _, item := range []struct{ chat, text string }{{"one", "solo chat uno"}, {"two", "solo chat dos"}, {"one", "seguimiento"}} {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"`+item.text+`"}]}`))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("X-OpenWebUI-User-Id", "owner")
		request.Header.Set("X-OpenWebUI-User-Role", "admin")
		request.Header.Set("X-OpenWebUI-Chat-Id", item.chat)
		result := httptest.NewRecorder()
		s.Handler().ServeHTTP(result, request)
		if result.Code != http.StatusOK {
			t.Fatalf("chat %q returned %d: %s", item.chat, result.Code, result.Body.String())
		}
	}
	if !strings.Contains(joinTurnContent(modelMessages[2]), "solo chat uno") || strings.Contains(joinTurnContent(modelMessages[2]), "solo chat dos") {
		t.Fatalf("Open WebUI chat history was not isolated: %s", joinTurnContent(modelMessages[2]))
	}
}

func TestOpenWebUIRejectsMissingConversationID(t *testing.T) {
	s := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"Hola"}]}`))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	result := httptest.NewRecorder()
	s.Handler().ServeHTTP(result, request)
	if result.Code != http.StatusBadRequest || !strings.Contains(result.Body.String(), "chat ID required") {
		t.Fatalf("request without stable conversation ID returned %d: %s", result.Code, result.Body.String())
	}
}

func TestDailyMemoryStoresMetadataAndRecallsPreviousWeek(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s := New("", "", "", t.TempDir(), map[int64]bool{7: true})
	if err := s.appendMemoryAt(7, "telegram", "chat-a", "Acordamos revisar presupuesto Atlas", "Fran enviará la propuesta Atlas", now.AddDate(0, 0, -8)); err != nil {
		t.Fatal(err)
	}
	entries, err := s.relevantMemoriesAt(7, "", "¿Qué acordamos la semana pasada sobre Atlas?", false, now)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Assistant, "propuesta Atlas") {
		t.Fatalf("last week memory=%#v err=%v", entries, err)
	}
	if entries[0].Channel != "telegram" || entries[0].Conversation == "" {
		t.Fatalf("memory metadata missing: %#v", entries[0])
	}
	path := s.dailyMemoryPath(7, now.AddDate(0, 0, -8))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("daily partition missing at %s: %v", path, err)
	}
}

func TestNaturalLanguageConversationRecallRoutesToMemory(t *testing.T) {
	for _, query := range []string{
		"¿Qué hablamos la semana pasada?",
		"¿Qué hablamos?",
		"¿Qué acordamos ayer sobre Atlas?",
		"¿Qué comentamos esta semana del lanzamiento?",
	} {
		if got := localIntent(query); got != intentMemory {
			t.Errorf("localIntent(%q) = %q, want %q", query, got, intentMemory)
		}
	}
}

func TestForgetDeletesDailyAndLegacyMemoryAndAllConversationHistory(t *testing.T) {
	s := New("", "", "", t.TempDir(), map[int64]bool{7: true})
	if err := os.MkdirAll(filepath.Dir(s.dailyMemoryPath(7, time.Now())), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.dailyMemoryPath(7, time.Now()), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.legacyMemoryPath(7), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(s.StateDir, "history-7-some-chat.json")
	if err := os.WriteFile(history, []byte("[]"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.forget(7); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.dailyMemoryDir(7), s.legacyMemoryPath(7), history, s.historyPath(7)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("forget left data at %s (err=%v)", path, err)
		}
	}
}

func joinTurnContent(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		parts = append(parts, turn.Content)
	}
	return strings.Join(parts, "\n")
}

func joinNonSystemTurnContent(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != "system" {
			parts = append(parts, turn.Content)
		}
	}
	return strings.Join(parts, "\n")
}
