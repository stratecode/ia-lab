package donnaidentity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type staticAuthenticator struct {
	principal Principal
	err       error
}

func (a staticAuthenticator) Authenticate(*http.Request) (Principal, error) {
	return a.principal, a.err
}

func TestHTTPCreateDerivesPrincipalFromAuthenticator(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, staticAuthenticator{principal: Principal{
		OperatorID: 42, Channel: "telegram", ConversationID: "private-chat-42",
	}})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/mappings", strings.NewReader(`{"kind":"person","canonical_id":"jira-user-42","canonical_label":"Valentina Ruiz","provider":"jira","external_id":"jira-user-42","aliases":["Valentina"]}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}

	var created MappingCandidate
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OperatorID != 42 || created.Channel != "telegram" || created.ConversationID != "private-chat-42" {
		t.Fatalf("created mapping principal = (%d, %s, %s)", created.OperatorID, created.Channel, created.ConversationID)
	}
	if created.Status != MappingPending || created.PayloadDigest == "" {
		t.Fatalf("created mapping state = %q, digest %q", created.Status, created.PayloadDigest)
	}
}

func TestHTTPDoesNotTrustIdentityOrStatusFromJSON(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, staticAuthenticator{principal: Principal{
		OperatorID: 42, Channel: "telegram", ConversationID: "private-chat-42",
	}})
	request := httptest.NewRequest(http.MethodPost, "/v1/mappings", strings.NewReader(`{"operator_id":99,"channel":"openwebui","kind":"person","canonical_id":"jira-user-42","canonical_label":"Valentina Ruiz","provider":"jira","external_id":"jira-user-42","status":"approved"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("create status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
}

func TestHTTPRejectsMissingAuthenticatedIdentity(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, staticAuthenticator{})
	request := httptest.NewRequest(http.MethodGet, "/v1/mappings", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("list status = %d, want 401", response.Code)
	}
}

func TestHTTPScopesMappingsByAuthenticatedOperator(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	auth := &mutableAuthenticator{principal: Principal{OperatorID: 42, Channel: "telegram", ConversationID: "chat-42"}}
	handler := NewHandler(store, auth)
	create := httptest.NewRequest(http.MethodPost, "/v1/mappings", strings.NewReader(`{"kind":"project","canonical_id":"PMI","canonical_label":"PMI","provider":"jira","external_id":"PMI"}`))
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}

	auth.principal = Principal{OperatorID: 99, Channel: "telegram", ConversationID: "chat-99"}
	list := httptest.NewRequest(http.MethodGet, "/v1/mappings", nil)
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}
	var items []MappingCandidate
	if err := json.Unmarshal(listed.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("operator 99 received %d mappings, want none", len(items))
	}
}

func TestHTTPMalformedAndMissingIDs(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, staticAuthenticator{principal: Principal{OperatorID: 42, Channel: "telegram", ConversationID: "chat-42"}})

	badCreate := httptest.NewRequest(http.MethodPost, "/v1/mappings", strings.NewReader(`{"kind":"unknown","canonical_id":"x","canonical_label":"x","provider":"jira","external_id":"x"}`))
	badCreateResponse := httptest.NewRecorder()
	handler.ServeHTTP(badCreateResponse, badCreate)
	if badCreateResponse.Code != http.StatusBadRequest {
		t.Fatalf("malformed create status = %d, want 400", badCreateResponse.Code)
	}

	missing := httptest.NewRequest(http.MethodGet, "/v1/mappings/missing", nil)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing mapping status = %d, want 404", missingResponse.Code)
	}
}

func TestHTTPConfirmsOnlyMatchingPendingPayload(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{OperatorID: 42, Channel: "telegram", ConversationID: "chat-42"}
	handler := NewHandler(store, staticAuthenticator{principal: principal})
	create := httptest.NewRequest(http.MethodPost, "/v1/mappings", strings.NewReader(`{"kind":"person","canonical_id":"jira-user-42","canonical_label":"Valentina Ruiz","provider":"jira","external_id":"jira-user-42"}`))
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	var candidate MappingCandidate
	if err := json.Unmarshal(created.Body.Bytes(), &candidate); err != nil {
		t.Fatal(err)
	}

	confirm := httptest.NewRequest(http.MethodPost, "/v1/mappings/"+candidate.ID+"/confirm", strings.NewReader(`{"payload_digest":"stale"}`))
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, confirm)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale confirmation status = %d, want 409", stale.Code)
	}

	confirm = httptest.NewRequest(http.MethodPost, "/v1/mappings/"+candidate.ID+"/confirm", strings.NewReader(`{"payload_digest":"`+candidate.PayloadDigest+`"}`))
	approved := httptest.NewRecorder()
	handler.ServeHTTP(approved, confirm)
	if approved.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d, body = %s", approved.Code, approved.Body.String())
	}
	var result MappingCandidate
	if err := json.Unmarshal(approved.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != MappingApproved || result.ConfirmedBy != principal.OperatorID {
		t.Fatalf("confirmation result = %#v", result)
	}
}

type mutableAuthenticator struct {
	principal Principal
}

func (a *mutableAuthenticator) Authenticate(*http.Request) (Principal, error) {
	return a.principal, nil
}
