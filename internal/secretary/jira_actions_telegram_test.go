package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramJiraCallbackAppliesOnlyAuthenticatedActionAndReportsVerifiedKey(t *testing.T) {
	var applied, sent int
	var botServer *httptest.Server
	botServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/actions/action-1" && r.Method == http.MethodGet:
			if r.Header.Get("X-Donna-Operator-ID") != "17" || r.Header.Get("X-Donna-Channel") != "telegram" || r.Header.Get("X-Donna-Conversation-ID") != "17" {
				t.Errorf("untrusted operator context: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"action_id":"action-1","kind":"create","payload_digest":"digest"}`))
		case r.URL.Path == "/v1/actions/action-1/apply" && r.Method == http.MethodPost:
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["payload_digest"] != "digest" {
				t.Errorf("wrong digest: %#v", payload)
			}
			applied++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"action_id":"action-1","kind":"create","issue_key":"LAB-42","verified":true,"status":"committed"}`))
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			w.Write([]byte(`{"ok":true,"result":true}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sent++
			w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer botServer.Close()
	service := &Service{Allowed: map[int64]bool{17: true}, JiraActions: &JiraActionsClient{BaseURL: botServer.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: botServer.Client()}}
	bot := &Bot{Base: botServer.URL, Token: "telegram-token", Client: botServer.Client(), Service: service}
	callback := &CallbackQuery{ID: "callback-1", From: User{ID: 17}, Message: &Message{Chat: Chat{ID: 17, Type: "private"}}, Data: "dja:action-1"}
	if err := bot.processUpdate(context.Background(), Update{ID: 3, CallbackQuery: callback}); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || sent != 1 {
		t.Fatalf("expected one verified apply and one reply, applied=%d sent=%d", applied, sent)
	}
}

func TestTelegramJiraCallbackRejectsAnotherOperatorBeforeServiceCall(t *testing.T) {
	serviceCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/v1/actions/") {
			serviceCalls++
		}
		if strings.HasSuffix(r.URL.Path, "/answerCallbackQuery") {
			w.Write([]byte(`{"ok":true,"result":true}`))
			return
		}
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	service := &Service{Allowed: map[int64]bool{17: true}, JiraActions: &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}}
	bot := &Bot{Base: server.URL, Token: "telegram-token", Client: server.Client(), Service: service}
	callback := &CallbackQuery{ID: "callback-2", From: User{ID: 99}, Message: &Message{Chat: Chat{ID: 99, Type: "private"}}, Data: "dja:action-1"}
	if err := bot.processUpdate(context.Background(), Update{ID: 4, CallbackQuery: callback}); err != nil {
		t.Fatal(err)
	}
	if serviceCalls != 0 {
		t.Fatalf("unapproved operator reached Jira action service %d times", serviceCalls)
	}
}

func TestTelegramJiraNotSameCallbackCancelsCommentWithoutApplyingIt(t *testing.T) {
	canceled, applied, sent := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/actions/action-2":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"action_id":"action-2","kind":"comment","payload_digest":"digest"}`))
		case "/v1/actions/action-2/cancel":
			canceled++
			w.Write([]byte(`{"action_id":"action-2","status":"canceled"}`))
		case "/v1/actions/action-2/apply":
			applied++
		case "/bottelegram-token/answerCallbackQuery":
			w.Write([]byte(`{"ok":true,"result":true}`))
		case "/bottelegram-token/sendMessage":
			sent++
			w.Write([]byte(`{"ok":true,"result":{}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := &Service{Allowed: map[int64]bool{17: true}, JiraActions: &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}}
	bot := &Bot{Base: server.URL, Token: "telegram-token", Client: server.Client(), Service: service}
	callback := &CallbackQuery{ID: "callback-3", From: User{ID: 17}, Message: &Message{Chat: Chat{ID: 17, Type: "private"}}, Data: "djn:action-2"}
	if err := bot.processUpdate(context.Background(), Update{ID: 5, CallbackQuery: callback}); err != nil {
		t.Fatal(err)
	}
	if canceled != 1 || applied != 0 || sent != 1 {
		t.Fatalf("cancelled=%d applied=%d sent=%d", canceled, applied, sent)
	}
}

func TestTelegramCommentPreviewShowsExactCommentAndBothChoices(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer server.Close()
	bot := &Bot{Base: server.URL, Token: "telegram-token", Client: server.Client()}
	preview := JiraActionPreview{ActionID: "action-3", Kind: "comment", IssueKey: "LAB-9", IssueSummary: "Revisar integración", Comment: "Acción exacta de Granola", SourceSpan: "Revisar integración con el cliente", NoteURL: "https://granola.example/notes/note-1", PayloadDigest: "digest"}
	if err := bot.sendJiraActionPreview(context.Background(), 17, preview); err != nil {
		t.Fatal(err)
	}
	message, _ := payload["text"].(string)
	markup, _ := payload["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	if !strings.Contains(message, preview.Comment) || !strings.Contains(message, preview.NoteURL) || len(rows) != 1 {
		t.Fatalf("preview text/markup incomplete: %#v", payload)
	}
	buttons, _ := rows[0].([]any)
	if len(buttons) != 3 {
		t.Fatalf("comment preview must offer update, reject-match, and discard: %#v", rows)
	}
	if buttons[2].(map[string]any)["callback_data"] != "djc:action-3" {
		t.Fatalf("comment preview does not offer explicit discard: %#v", buttons[2])
	}
}
