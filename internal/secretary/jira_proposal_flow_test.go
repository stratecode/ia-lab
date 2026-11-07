package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrepareJiraTaskProposalAsksBelowCalibrationWithoutCreatingDraft(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		t.Fatal("uncalibrated candidate must not call Jira actions service")
	}))
	defer server.Close()
	candidate := readyTaskCandidate()
	policy := readyTaskPolicy()
	delete(policy.Calibration, TaskDimensionSummary)
	result, err := PrepareJiraTaskProposal(context.Background(), &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters"}, JiraActionPrincipal{OperatorID: 17, Channel: "telegram", ConversationID: "17"}, candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Status != CandidateClarify || result.Decision.Dimension != TaskDimensionSummary || result.Preview != nil || requests != 0 {
		t.Fatalf("unexpected fail-closed decision: %#v requests=%d", result, requests)
	}
}

func TestPrepareJiraTaskProposalCreatesOnlyExactGroundedPreviewWhenNoMatch(t *testing.T) {
	var draft map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tasks/matches":
			w.Write([]byte(`{"candidates":[]}`))
		case "/v1/actions/draft":
			if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"action_id":"action-1","kind":"create","project_key":"LAB","issue_type":"Task","summary":"Revisar integración","payload_digest":"` + string(make([]byte, 0)) + `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	result, err := PrepareJiraTaskProposal(context.Background(), &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}, JiraActionPrincipal{OperatorID: 17, Channel: "telegram", ConversationID: "17"}, readyTaskCandidate(), readyTaskPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.Kind != "create" || draft["source_span"] != "Revisar la integración con el cliente" || draft["dedupe_key"] == "" {
		t.Fatalf("proposal lost evidence or dedupe key: result=%#v draft=%#v", result, draft)
	}
	if result.Message == "" || result.Preview.PayloadDigest == "" {
		t.Fatalf("preview not rendered with a confirmation marker: %#v", result)
	}
}

func TestPrepareJiraTaskProposalCommentsOnlyAfterCalibratedExactMatch(t *testing.T) {
	var draft map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tasks/matches":
			w.Write([]byte(`{"candidates":[{"issue_key":"LAB-9","summary":"Revisar integración","confidence":0.94,"similarity":0.96}]}`))
		case "/v1/actions/draft":
			_ = json.NewDecoder(r.Body).Decode(&draft)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"action_id":"action-2","kind":"comment","issue_key":"LAB-9","issue_summary":"Revisar integración","comment":"Acción de reunión","note_url":"https://granola.example/notes/note-1","source_span":"Revisar la integración con el cliente","payload_digest":"` + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + `"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	policy := readyTaskPolicy()
	policy.Calibration[TaskDimensionMatch] = CalibrationResult{Precision: 0.94, SampleCount: 40}
	result, err := PrepareJiraTaskProposal(context.Background(), &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}, JiraActionPrincipal{OperatorID: 17, Channel: "openwebui", ConversationID: "chat-1"}, readyTaskCandidate(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Preview == nil || result.Preview.Kind != "comment" || draft["kind"] != "comment" || draft["issue_key"] != "LAB-9" || draft["comment"] == "" {
		t.Fatalf("matching issue must get a comment-only proposal: result=%#v draft=%#v", result, draft)
	}
	if result.Preview.IssueKey != "LAB-9" || result.Preview.Comment == "" {
		t.Fatalf("preview does not bind exact target/comment: %#v", result.Preview)
	}
}

func TestPrepareJiraTaskProposalAsksWhenMatchGateIsBelowNinetyPercent(t *testing.T) {
	drafts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/tasks/matches" {
			w.Write([]byte(`{"candidates":[{"issue_key":"LAB-9","summary":"Revisar integración","confidence":0.94,"similarity":0.89}]}`))
			return
		}
		drafts++
	}))
	defer server.Close()
	policy := readyTaskPolicy()
	policy.Calibration[TaskDimensionMatch] = CalibrationResult{Precision: 0.95, SampleCount: 40}
	result, err := PrepareJiraTaskProposal(context.Background(), &JiraActionsClient{BaseURL: server.URL, Token: "service-secret-long-enough-to-be-at-least-32-characters", HTTPClient: server.Client()}, JiraActionPrincipal{OperatorID: 17, Channel: "telegram", ConversationID: "17"}, readyTaskCandidate(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Status != CandidateClarify || result.Decision.IssueKey != "LAB-9" || result.Preview != nil || drafts != 0 {
		t.Fatalf("below-threshold match must ask and create no draft: %#v drafts=%d", result, drafts)
	}
}

func readyTaskCandidate() TaskCandidate {
	return TaskCandidate{
		GranolaNoteID: "note-1", NoteURL: "https://granola.example/notes/note-1", MeetingTitle: "Reunión de integración",
		SourceSummary: "Acciones:\n- Revisar la integración con el cliente", SourceSpan: "Revisar la integración con el cliente",
		ProjectKey: "LAB", IssueType: "Task", Summary: "Revisar integración", Description: "Validar la integración",
		Confidence: map[TaskDimension]float64{TaskDimensionAction: .95, TaskDimensionProject: .95, TaskDimensionIssueType: .95, TaskDimensionSummary: .95},
	}
}

func readyTaskPolicy() ConfidencePolicy {
	calibration := map[TaskDimension]CalibrationResult{}
	for _, dimension := range []TaskDimension{TaskDimensionAction, TaskDimensionProject, TaskDimensionIssueType, TaskDimensionSummary} {
		calibration[dimension] = CalibrationResult{Precision: .95, SampleCount: 40}
	}
	return ConfidencePolicy{Threshold: .90, MinSamples: 30, Calibration: calibration}
}
