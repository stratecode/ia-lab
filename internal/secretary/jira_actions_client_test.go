package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJiraActionsClientDraftCarriesAuthenticatedOperatorAndNeverWritesJira(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/actions/draft" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer service-secret-long-enough-to-be-at-least-32-characters" || r.Header.Get("X-Donna-Operator-ID") != "17" || r.Header.Get("X-Donna-Channel") != "telegram" || r.Header.Get("X-Donna-Conversation-ID") != "17" {
			t.Fatalf("operator context not forwarded: %#v", r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if _, found := payload["principal"]; found {
			t.Fatal("request body must not be able to override the authenticated principal")
		}
		if payload["kind"] != "create" || payload["summary"] != "Revisar integración" {
			t.Fatalf("unexpected draft payload: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"action_id":"action-1","kind":"create","payload_digest":"digest","summary":"Revisar integración"}`))
	}))
	defer server.Close()
	client := &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}
	preview, err := client.Draft(context.Background(), JiraActionPrincipal{OperatorID: 17, Channel: "telegram", ConversationID: "17"}, JiraTaskDraft{Kind: "create", Summary: "Revisar integración"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ActionID != "action-1" || preview.PayloadDigest != "digest" || requests != 1 {
		t.Fatalf("unexpected preview %#v requests=%d", preview, requests)
	}
}

func TestJiraActionsClientRejectsServiceErrorsWithoutLeakingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private atlassian token leaked", http.StatusBadGateway)
	}))
	defer server.Close()
	client := &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}
	_, err := client.Draft(context.Background(), JiraActionPrincipal{OperatorID: 17, Channel: "openwebui", ConversationID: "chat-1"}, JiraTaskDraft{Kind: "create", Summary: "x"})
	if err == nil || strings.Contains(err.Error(), "private atlassian token") {
		t.Fatalf("error must be generic and non-nil: %v", err)
	}
}

func TestJiraActionsClientRequiresLoopbackServiceEndpoint(t *testing.T) {
	client := &JiraActionsClient{BaseURL: "https://example.com", Token: "service-secret-long-enough-to-be-at-least-32-characters"}
	_, err := client.Draft(context.Background(), JiraActionPrincipal{OperatorID: 17, Channel: "openwebui", ConversationID: "chat-1"}, JiraTaskDraft{Kind: "create", Summary: "x"})
	if err == nil {
		t.Fatal("refuse to send the private service token to a non-loopback host")
	}
}
