package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestPublicPathBypassesAuth(t *testing.T) {
	auth := &Authenticator{limiter: newBruteForceTracker(10, 60, 300)}
	handler := auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected public path to bypass auth, got %d", rec.Code)
	}
}

func TestCheckScopeBlocksBotOnAdminPath(t *testing.T) {
	errText := checkScope(domain.ScopeBot, http.MethodPost, "/config/safe-mode")
	if errText == "" {
		t.Fatal("expected scope error for bot on admin path")
	}
}
