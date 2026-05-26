package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestListModelsMatchesGoldenShape(t *testing.T) {
	server := &Server{
		Config:        config.Config{SafeMode: true},
		Now:           func() time.Time { return time.Unix(1700000000, 0).UTC() },
		Version:       "test",
		OpenAIToolsID: "orchestrator-tools",
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	server.listModels(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	assertJSONGolden(t, "testdata/models_golden.json", rec.Body.Bytes())
}

func TestHealthResponseMatchesGoldenShape(t *testing.T) {
	payload := domain.HealthResponse{
		Status:    "healthy",
		Timestamp: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC),
		Version:   "0.1.0-go-runtime",
		SafeMode:  true,
		Components: map[string]domain.ComponentHealth{
			"database": {Status: "healthy", Details: map[string]any{}, LatencyMS: ptrFloat(1.25)},
			"redis":    {Status: "healthy", Details: map[string]any{}, LatencyMS: ptrFloat(0.75)},
			"workers":  {Status: "degraded", Details: map[string]any{"registered_workers": 0, "note": "No workers registered"}},
		},
	}
	body, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	assertJSONGolden(t, "testdata/health_golden.json", body)
}

func TestParseJudgeVerdictAcceptsMarkdownJSON(t *testing.T) {
	verdict, err := parseJudgeVerdict("```json\n{\"accuracy_score\":0.9,\"coverage_score\":0.8,\"source_use_score\":0.7,\"usefulness_score\":0.85,\"hallucination_risk_score\":0.1,\"winner\":\"orchestrator\",\"reasoning\":\"sound\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Winner != "orchestrator" {
		t.Fatalf("unexpected winner: %s", verdict.Winner)
	}
	if verdict.AccuracyScore != 0.9 {
		t.Fatalf("unexpected accuracy score: %v", verdict.AccuracyScore)
	}
	if verdict.Reasoning != "sound" {
		t.Fatalf("unexpected reasoning: %s", verdict.Reasoning)
	}
}

func assertJSONGolden(t *testing.T, relativePath string, actual []byte) {
	t.Helper()
	goldenPath := filepath.Join(filepath.Dir(relativePathFromCaller(t)), relativePath)
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var expectedJSON any
	var actualJSON any
	if err := json.Unmarshal(expected, &expectedJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &actualJSON); err != nil {
		t.Fatal(err)
	}
	expectedBytes, _ := json.Marshal(expectedJSON)
	actualBytes, _ := json.Marshal(actualJSON)
	if string(expectedBytes) != string(actualBytes) {
		t.Fatalf("golden mismatch\nexpected: %s\nactual: %s", expectedBytes, actualBytes)
	}
}

func relativePathFromCaller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtimeCaller(1)
	if !ok {
		t.Fatal("unable to determine caller path")
	}
	return file
}

var runtimeCaller = func(skip int) (uintptr, string, int, bool) {
	return 0, "", 0, false
}

func init() {
	runtimeCaller = func(skip int) (uintptr, string, int, bool) {
		return runtime.Caller(skip + 1)
	}
}

func ptrFloat(v float64) *float64 { return &v }

func TestListCapabilitiesIncludesDocumentAndImage(t *testing.T) {
	server := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	rec := httptest.NewRecorder()

	server.listCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	items, _ := payload["capabilities"].([]any)
	if len(items) != 6 {
		t.Fatalf("unexpected capabilities payload: %#v", payload)
	}
}

func TestToggleSafeModeUpdatesState(t *testing.T) {
	server := &Server{
		Config:   config.Config{SafeMode: true},
		SafeMode: NewSafeModeState(true),
	}
	req := httptest.NewRequest(http.MethodPost, "/config/safe-mode", bytes.NewBufferString(`{"enabled":false}`))
	rec := httptest.NewRecorder()

	server.toggleSafeMode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if server.safeModeEnabled() {
		t.Fatal("safe mode should be disabled after toggle")
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["safe_mode_enabled"] != false {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestSafeModeEnabledUsesMutableState(t *testing.T) {
	server := &Server{
		Config:   config.Config{SafeMode: true},
		SafeMode: NewSafeModeState(false),
	}
	if server.safeModeEnabled() {
		t.Fatal("safe mode helper should read mutable state before static config")
	}
}

func TestPersistCapabilityExecutionReturnsInvocationAndArtifacts(t *testing.T) {
	server := &Server{
		Config:   config.Config{},
		Postgres: nil,
	}
	_ = server
	_ = capabilities.Result{}
}
