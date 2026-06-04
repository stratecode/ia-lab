package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type fakeClaimExecutor struct {
	mu      sync.Mutex
	claims  []domain.LocalBridgeTaskClaimResponse
	result  domain.LocalBridgeResultRequest
	execErr error
}

func (f *fakeClaimExecutor) Execute(_ context.Context, claim domain.LocalBridgeTaskClaimResponse) (domain.LocalBridgeResultRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = append(f.claims, claim)
	return f.result, f.execErr
}

func TestObjectiveRunnerProcessesClaimUntilInitiativeCompletes(t *testing.T) {
	workspaceRoot := t.TempDir()
	executor := &fakeClaimExecutor{
		result: domain.LocalBridgeResultRequest{
			Status:       "success",
			Summary:      stringPtr("task completed"),
			ChangedFiles: []string{"README.md"},
		},
	}
	var (
		mu          sync.Mutex
		registerHit bool
		submitHit   bool
		claimCalls  int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/register":
			registerHit = true
			writeJSONTest(t, w, domain.LocalBridgeResponse{ID: "bridge-1", WorkspaceRoot: workspaceRoot, Status: "active"})
		case r.Method == http.MethodPost && r.URL.Path == "/objectives/":
			writeJSONTest(t, w, domain.ObjectiveResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusExecuting},
				Contract:   domain.ExecutionContract{Title: "Ship objective loop"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/initiatives/initiative-1":
			status := domain.InitiativeStatusExecuting
			if submitHit {
				status = domain.InitiativeStatusCompleted
			}
			writeJSONTest(t, w, domain.InitiativeDetailResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: status},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks"):
			writeJSONTest(t, w, domain.TaskListResponse{Items: []domain.TaskResponse{}, Total: 0})
		case r.Method == http.MethodGet && r.URL.Path == "/approvals":
			writeJSONTest(t, w, domain.ApprovalListResponse{Items: []domain.ApprovalResponse{}, Total: 0})
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/bridge-1/claim-next":
			claimCalls++
			if claimCalls == 1 {
				writeJSONTest(t, w, domain.LocalBridgeTaskClaimResponse{
					TaskID:        "task-1",
					Description:   "Run the first task",
					WorkspaceRoot: workspaceRoot,
					Metadata: map[string]any{
						"tool_request": map[string]any{
							"tool": "run_tests",
							"argv": []string{"python3", "-c", "print('ok')"},
						},
					},
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/bridge-1/tasks/task-1/result":
			submitHit = true
			writeJSONTest(t, w, map[string]any{"status": "accepted"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", time.Second)
	runner := &ObjectiveRunner{
		client:         client,
		executor:       executor,
		bridgeID:       "bridge-1",
		workspaceRoot:  workspaceRoot,
		approvalMode:   ApprovalModeManual,
		pollInterval:   5 * time.Millisecond,
		heartbeatEvery: 50 * time.Millisecond,
		name:           "test-runner",
		hostname:       "localhost",
	}
	result, err := runner.Run(context.Background(), ObjectiveRunRequest{
		Title:         "Ship objective loop",
		Objective:     "Make the system do useful work.",
		WorkspaceRoot: workspaceRoot,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}
	if !registerHit {
		t.Fatal("expected local bridge registration")
	}
	if !submitHit {
		t.Fatal("expected task result submission")
	}
	if result.Initiative == nil || result.Initiative.Status != domain.InitiativeStatusCompleted {
		t.Fatalf("expected completed initiative, got %#v", result)
	}
	if result.ProcessedTasks != 1 {
		t.Fatalf("expected one processed task, got %#v", result)
	}
	if len(executor.claims) != 1 || executor.claims[0].TaskID != "task-1" {
		t.Fatalf("unexpected executor claims: %#v", executor.claims)
	}
}

func TestObjectiveRunnerAutoApprovesObjectiveScopedLocalBridgeApprovals(t *testing.T) {
	workspaceRoot := t.TempDir()
	executor := &fakeClaimExecutor{}
	var (
		mu             sync.Mutex
		approveCalls   int
		initiativePoll int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/register":
			writeJSONTest(t, w, domain.LocalBridgeResponse{ID: "bridge-1", WorkspaceRoot: workspaceRoot, Status: "active"})
		case r.Method == http.MethodPost && r.URL.Path == "/objectives/":
			writeJSONTest(t, w, domain.ObjectiveResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusExecuting},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/initiatives/initiative-1":
			initiativePoll++
			status := domain.InitiativeStatusExecuting
			if approveCalls > 0 {
				status = domain.InitiativeStatusCompleted
			}
			writeJSONTest(t, w, domain.InitiativeDetailResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: status},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/bridge-1/claim-next":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks"):
			if strings.Contains(r.URL.RawQuery, "initiative_id=initiative-1") && strings.Contains(r.URL.RawQuery, "state=waiting_approval") {
				writeJSONTest(t, w, domain.TaskListResponse{
					Items: []domain.TaskResponse{
						{ID: "task-approve", InitiativeID: stringPtr("initiative-1"), State: domain.TaskStateWaitingApproval},
					},
					Total: 1,
				})
				return
			}
			writeJSONTest(t, w, domain.TaskListResponse{Items: []domain.TaskResponse{}, Total: 0})
		case r.Method == http.MethodGet && r.URL.Path == "/approvals":
			writeJSONTest(t, w, domain.ApprovalListResponse{
				Items: []domain.ApprovalResponse{
					{ID: "approval-1", TaskID: "task-approve", ActionType: "local_bridge_tool", Status: domain.ApprovalPending},
					{ID: "approval-2", TaskID: "other-task", ActionType: "local_bridge_tool", Status: domain.ApprovalPending},
				},
				Total: 2,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/approvals/approval-1/approve":
			approveCalls++
			writeJSONTest(t, w, domain.ApprovalResponse{ID: "approval-1", TaskID: "task-approve", Status: domain.ApprovalApproved})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	runner := &ObjectiveRunner{
		client:         NewClient(server.URL, "test-key", time.Second),
		executor:       executor,
		bridgeID:       "bridge-1",
		workspaceRoot:  workspaceRoot,
		approvalMode:   ApprovalModeLocalOnly,
		pollInterval:   5 * time.Millisecond,
		heartbeatEvery: 50 * time.Millisecond,
		name:           "test-runner",
		hostname:       "localhost",
	}
	result, err := runner.Run(context.Background(), ObjectiveRunRequest{
		Title:         "Ship objective loop",
		Objective:     "Make the system do useful work.",
		WorkspaceRoot: workspaceRoot,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatalf("runner failed: %v", err)
	}
	if approveCalls != 1 {
		t.Fatalf("expected one approval resolution, got %d", approveCalls)
	}
	if result.ResolvedApprovals != 1 {
		t.Fatalf("expected result to report one resolved approval, got %#v", result)
	}
}

func TestObjectiveRunnerReturnsErrorWhenInitiativeBlocks(t *testing.T) {
	workspaceRoot := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/register":
			writeJSONTest(t, w, domain.LocalBridgeResponse{ID: "bridge-1", WorkspaceRoot: workspaceRoot, Status: "active"})
		case r.Method == http.MethodPost && r.URL.Path == "/objectives/":
			writeJSONTest(t, w, domain.ObjectiveResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusExecuting},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/initiatives/initiative-1":
			writeJSONTest(t, w, domain.InitiativeDetailResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusBlocked},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks"):
			writeJSONTest(t, w, domain.TaskListResponse{Items: []domain.TaskResponse{}, Total: 0})
		case r.Method == http.MethodGet && r.URL.Path == "/approvals":
			writeJSONTest(t, w, domain.ApprovalListResponse{Items: []domain.ApprovalResponse{}, Total: 0})
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/bridge-1/claim-next":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	runner := &ObjectiveRunner{
		client:         NewClient(server.URL, "test-key", time.Second),
		executor:       &fakeClaimExecutor{},
		bridgeID:       "bridge-1",
		workspaceRoot:  workspaceRoot,
		approvalMode:   ApprovalModeManual,
		pollInterval:   5 * time.Millisecond,
		heartbeatEvery: 50 * time.Millisecond,
		name:           "test-runner",
		hostname:       "localhost",
	}
	_, err := runner.Run(context.Background(), ObjectiveRunRequest{
		Title:         "Ship objective loop",
		Objective:     "Make the system do useful work.",
		WorkspaceRoot: workspaceRoot,
		CreatedBy:     "test",
	})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked initiative error, got %v", err)
	}
}

func TestRunObjectivePrintsFinalResult(t *testing.T) {
	workspaceRoot := t.TempDir()
	executor := &fakeClaimExecutor{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/register":
			writeJSONTest(t, w, domain.LocalBridgeResponse{ID: "bridge-1", WorkspaceRoot: workspaceRoot, Status: "active"})
		case r.Method == http.MethodPost && r.URL.Path == "/objectives/":
			writeJSONTest(t, w, domain.ObjectiveResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusCompleted},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/initiatives/initiative-1":
			writeJSONTest(t, w, domain.InitiativeDetailResponse{
				Initiative: &domain.InitiativeResponse{ID: "initiative-1", Status: domain.InitiativeStatusCompleted},
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks"):
			writeJSONTest(t, w, domain.TaskListResponse{Items: []domain.TaskResponse{}, Total: 0})
		case r.Method == http.MethodGet && r.URL.Path == "/approvals":
			writeJSONTest(t, w, domain.ApprovalListResponse{Items: []domain.ApprovalResponse{}, Total: 0})
		case r.Method == http.MethodPost && r.URL.Path == "/bridges/bridge-1/claim-next":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("null"))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	tempFile := filepath.Join(t.TempDir(), "stdout.txt")
	oldStdout := os.Stdout
	fh, err := os.Create(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = fh
	t.Cleanup(func() { os.Stdout = oldStdout })
	t.Cleanup(func() { _ = fh.Close() })

	err = RunObjective(context.Background(), CLIOptions{
		BaseURL:        server.URL,
		APIKey:         "test-key",
		BridgeID:       "bridge-1",
		WorkspaceRoot:  workspaceRoot,
		Objective:      "Make the system do useful work.",
		ObjectiveTitle: "Useful autonomy",
		CreatedBy:      "test",
		ApprovalMode:   string(ApprovalModeManual),
		PollInterval:   5 * time.Millisecond,
	}, executor)
	if err != nil {
		t.Fatalf("RunObjective failed: %v", err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	var out ObjectiveRunResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("failed to decode output: %v\n%s", err, string(raw))
	}
	if out.Initiative == nil || out.Initiative.Status != domain.InitiativeStatusCompleted {
		t.Fatalf("unexpected printed result: %#v", out)
	}
}

func writeJSONTest(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
