package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestStartAutonomousInitiativeRequiresRunner(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/initiatives/autonomous", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	server.startAutonomousInitiative(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestStartAutonomousInitiativeUsesDefaultSurfaceAndAutoApproval(t *testing.T) {
	starter := &fakeAutonomousHTTPStarter{
		result: &domain.AutonomousRunResult{InitiativeID: "initiative-1", Summary: "queued"},
	}
	server := &Server{AutonomousRunner: starter}
	req := httptest.NewRequest(http.MethodPost, "/initiatives/autonomous", bytes.NewBufferString(`{
		"workspace_alias":"remote",
		"workspace_root":"/srv/stratecode",
		"goal":"Arregla el gateway"
	}`))
	rec := httptest.NewRecorder()

	server.startAutonomousInitiative(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d: %s", http.StatusAccepted, rec.Code, rec.Body.String())
	}
	if starter.lastReq.Surface != "openclaw.http" {
		t.Fatalf("unexpected surface: %q", starter.lastReq.Surface)
	}
	if !starter.lastReq.AutoApprovePhases {
		t.Fatal("expected auto approve phases to default to true")
	}
	var result domain.AutonomousRunResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result.InitiativeID != "initiative-1" {
		t.Fatalf("unexpected initiative id: %q", result.InitiativeID)
	}
}

type fakeAutonomousHTTPStarter struct {
	lastReq domain.AutonomousInitiativeRequest
	result  *domain.AutonomousRunResult
	err     error
}

func (f *fakeAutonomousHTTPStarter) StartFromChannel(_ context.Context, req domain.AutonomousInitiativeRequest) (*domain.AutonomousRunResult, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}
