package donnajira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type staticAuthenticator struct{ principal ActionPrincipal }

func (a staticAuthenticator) Authenticate(*http.Request) (ActionPrincipal, error) {
	return a.principal, nil
}

func TestHTTPDraftIsPreviewOnlyAndUsesAuthenticatedPrincipal(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{}
	principal := ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"}
	handler := NewHandler(store, writer, staticAuthenticator{principal: principal}, func() time.Time { return now })
	body := `{"kind":"create","project_key":"PMI","issue_type":"Task","summary":"Preparar el informe","note_id":"note-123","note_url":"https://notes.example.test/note-123","source_span":"Preparar el informe","dedupe_key":"note-123:preparar-el-informe"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/actions/draft", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || writer.createCalls != 0 || writer.commentCalls != 0 {
		t.Fatalf("status=%d createCalls=%d commentCalls=%d body=%s", response.Code, writer.createCalls, writer.commentCalls, response.Body.String())
	}
	var preview ActionPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ActionID == "" || preview.PayloadDigest == "" || preview.Principal != principal || preview.Summary != "Preparar el informe" {
		t.Fatalf("preview=%+v", preview)
	}
}

func TestHTTPDraftRejectsPrincipalSuppliedByRequestBody(t *testing.T) {
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, &fakeJiraWriter{}, staticAuthenticator{principal: ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"}}, time.Now)
	body := `{"kind":"create","project_key":"PMI","issue_type":"Task","summary":"Preparar el informe","note_id":"note-123","note_url":"https://notes.example.test/note-123","source_span":"Preparar el informe","dedupe_key":"note-123:preparar-el-informe","principal":{"operator_id":999,"channel":"openwebui","conversation_id":"other"}}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/actions/draft", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, principal override must be rejected", response.Code, response.Body.String())
	}
}

func TestHTTPApplyUsesAuthenticatedPrincipalAndExactDigest(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	principal := ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{created: MatchCandidate{IssueKey: "PMI-43", Summary: draft.Summary}, issue: MatchCandidate{IssueKey: "PMI-43", Summary: draft.Summary}}
	handler := NewHandler(store, writer, staticAuthenticator{principal: principal}, func() time.Time { return now.Add(time.Minute) })
	body, _ := json.Marshal(map[string]string{"payload_digest": draft.PayloadDigest})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/actions/"+draft.ID+"/apply", strings.NewReader(string(body))))
	if response.Code != http.StatusOK || writer.createCalls != 1 {
		t.Fatalf("status=%d createCalls=%d body=%s", response.Code, writer.createCalls, response.Body.String())
	}
	var receipt ActionReceipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || !receipt.Verified || receipt.IssueKey != "PMI-43" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestHTTPRejectsUnauthenticatedRequestBeforeJiraCall(t *testing.T) {
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, &fakeJiraWriter{}, HeaderAuthenticator{}, time.Now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/tasks/matches", strings.NewReader(`{"project_key":"PMI","query":"informe"}`)))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPOptionsExposesOnlyConfiguredJiraAllowlist(t *testing.T) {
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	principal := ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"}
	handler := NewHandler(store, &fakeJiraWriter{}, staticAuthenticator{principal: principal}, time.Now)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/config/options", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"project_keys":["PMI"]`) || !strings.Contains(response.Body.String(), `"issue_types":["Task"]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPCancelDoesNotCallJiraAndConsumesExactPendingAction(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	principal := ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{}
	handler := NewHandler(store, writer, staticAuthenticator{principal: principal}, func() time.Time { return now.Add(time.Minute) })
	body, _ := json.Marshal(map[string]string{"payload_digest": draft.PayloadDigest})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/actions/"+draft.ID+"/cancel", strings.NewReader(string(body))))
	if response.Code != http.StatusOK || writer.createCalls != 0 || writer.commentCalls != 0 {
		t.Fatalf("status=%d writes=%d/%d body=%s", response.Code, writer.createCalls, writer.commentCalls, response.Body.String())
	}
	if _, err := store.Consume(context.Background(), draft.ID, principal, draft.PayloadDigest, now.Add(2*time.Minute)); !errors.Is(err, ErrActionAlreadyConsumed) {
		t.Fatalf("canceled action should not remain consumable: %v", err)
	}
}
