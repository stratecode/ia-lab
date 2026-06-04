package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stratecode/lab/internal/orchestratorgo/bridge"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

func TestObjectiveLifecycleWithRepairLoopIntegration(t *testing.T) {
	if os.Getenv("LAB_RUN_OBJECTIVE_E2E") != "1" {
		t.Skip("set LAB_RUN_OBJECTIVE_E2E=1 to run Docker/Postgres objective integration harness")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for objective integration harness")
	}

	workspaceRoot := setupObjectiveE2EWorkspace(t)
	installFakeAiderTask(t)

	dsn, cleanupContainer := startObjectiveE2EPostgres(t)
	defer cleanupContainer()
	applyObjectiveE2ESchema(t, dsn)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	postgres, err := store.NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	server := &Server{
		Config:      config.Config{},
		Postgres:    postgres,
		Initiatives: initiative.New(config.Config{}, nil),
		Now:         time.Now,
		Version:     "test",
	}
	router := server.Router(nil)

	const bridgeID = "objective-e2e-bridge"
	postJSONStatus(t, router, http.MethodPost, "/bridges/register", domain.LocalBridgeRegisterRequest{
		BridgeID:      bridgeID,
		Name:          "objective-e2e",
		Hostname:      "localhost",
		WorkspaceRoot: workspaceRoot,
		Capabilities:  map[string]any{"tools": []string{"research_project", "run_command", "run_tests", "review_workspace"}},
	}, http.StatusOK, nil)

	var objectiveResp domain.ObjectiveResponse
	postJSONStatus(t, router, http.MethodPost, "/objectives/", domain.ObjectiveRequest{
		Title:         "Objective E2E repair loop",
		Objective:     "Apply a repository change and recover automatically when validation fails.",
		WorkspaceRoot: workspaceRoot,
		CreatedBy:     "integration-test",
	}, http.StatusCreated, &objectiveResp)
	if objectiveResp.Initiative == nil {
		t.Fatalf("expected initiative in objective response: %#v", objectiveResp)
	}

	executor, err := bridge.NewWorkspaceExecutor(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}

	for step := 0; step < 12; step++ {
		claim := claimNextBridgeTask(t, router, bridgeID)
		if claim == nil {
			if approval := firstPendingApproval(t, router); approval != nil {
				approveApprovalForTest(t, router, approval.ID)
				continue
			}
			break
		}
		result, err := executor.Execute(ctx, *claim)
		if err != nil {
			t.Fatalf("bridge execution failed for task %s: %v", claim.TaskID, err)
		}
		postJSONStatus(t, router, http.MethodPost, fmt.Sprintf("/bridges/%s/tasks/%s/result", bridgeID, claim.TaskID), result, http.StatusAccepted, nil)
	}

	initiativeRecord, err := postgres.GetInitiative(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	if initiativeRecord == nil || initiativeRecord.Status != domain.InitiativeStatusCompleted {
		t.Fatalf("expected completed initiative after repair loop, got %#v", initiativeRecord)
	}

	links, err := postgres.ListInitiativeTasks(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) < 8 {
		t.Fatalf("expected initial plus repair-cycle tasks, got %d", len(links))
	}

	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{}
	for _, link := range links {
		workItemID := strings.TrimSpace(asString(link.Task.Metadata["work_item_id"]))
		if workItemID != "" {
			byWorkItemID[workItemID] = link
		}
	}
	for _, required := range []string{"research", "edit", "validate", "replan-2", "edit-2", "validate-2", "review-2"} {
		if _, ok := byWorkItemID[required]; !ok {
			t.Fatalf("expected work item %s in initiative task set: %#v", required, byWorkItemID)
		}
	}

	edit2 := byWorkItemID["edit-2"].Task
	edit2Results, _ := edit2.Results.(map[string]any)
	edit2Stdout := strings.TrimSpace(asString(edit2Results["stdout"]))
	if !strings.Contains(edit2Stdout, "--metadata") || !strings.Contains(edit2Stdout, "--scope-paths") || !strings.Contains(edit2Stdout, "--test-command") {
		t.Fatalf("expected enriched aider-task invocation on repair edit, got %q", edit2Stdout)
	}

	replan2 := byWorkItemID["replan-2"].Task
	replan2Results, _ := replan2.Results.(map[string]any)
	replan2Stdout := strings.TrimSpace(asString(replan2Results["stdout"]))
	if !strings.Contains(replan2Stdout, "\"repair_mode\": true") || !strings.Contains(replan2Stdout, "\"next_edit_brief\"") {
		t.Fatalf("expected repair-aware replanner output, got %q", replan2Stdout)
	}

	artifacts, err := postgres.ListInitiativeArtifacts(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifactTypes := map[string]int{}
	for _, artifact := range artifacts {
		artifactTypes[artifact.ArtifactType]++
	}
	if artifactTypes["objective_repair_signal"] == 0 {
		t.Fatalf("expected objective_repair_signal artifact, got %#v", artifactTypes)
	}
	if artifactTypes["objective_iteration_summary"] == 0 {
		t.Fatalf("expected objective_iteration_summary artifact, got %#v", artifactTypes)
	}
	if artifactTypes["repair_plan"] == 0 {
		t.Fatalf("expected repair_plan artifact, got %#v", artifactTypes)
	}
	if artifactTypes["review_packet"] == 0 {
		t.Fatalf("expected review_packet artifact, got %#v", artifactTypes)
	}
}

func setupObjectiveE2EWorkspace(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "objective-e2e-repo")
	if err := os.MkdirAll(filepath.Join(root, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# objective e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	caseJSON := `{
  "id": "objective-repair-loop",
  "case_type": "integration_repair_loop",
  "repo_profile": "objective_e2e_v1",
  "repo_url": "https://example.invalid/objective-e2e",
  "default_branch": "main",
  "project_type": "existing_repo",
  "runtime_or_stack": "python",
  "project_root": ".",
  "test_focus": "objective repair loop",
  "test_command": ["python3", "-c", "import pathlib,sys; sys.exit(0 if pathlib.Path('PASS').exists() else 1)"],
  "expected_files": ["README.md", "PASS"],
  "benchmark_memory_mode": "on",
  "benchmark_memory_strategy": "repo_specific_first",
  "language": "python",
  "problem_domain": "objective autonomy"
}`
	if err := os.WriteFile(filepath.Join(root, ".lab", "benchmark-case.json"), []byte(caseJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "init")
	runGitInDir(t, root, "checkout", "-b", "main")
	runGitInDir(t, root, "add", "README.md", ".lab/benchmark-case.json")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	return root
}

func installFakeAiderTask(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "aider-task")
	script := `#!/bin/sh
set -eu
countfile="$PWD/.aider-count"
count=0
if [ -f "$countfile" ]; then
  count=$(cat "$countfile")
fi
count=$((count+1))
printf '%s' "$count" > "$countfile"
printf 'aider-run-%s %s\n' "$count" "$*"
printf '\nedit-%s\n' "$count" >> "$PWD/README.md"
if [ "$count" -ge 2 ]; then
  printf 'ok\n' > "$PWD/PASS"
fi
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func startObjectiveE2EPostgres(t *testing.T) (string, func()) {
	t.Helper()
	name := fmt.Sprintf("objective-e2e-%d", time.Now().UnixNano())
	runCommand(t, exec.Command("docker", "run", "-d", "--rm", "--name", name,
		"-e", "POSTGRES_DB=orchestrator",
		"-e", "POSTGRES_USER=orchestrator",
		"-e", "POSTGRES_PASSWORD=orchestrator",
		"-p", "127.0.0.1::5432",
		"pgvector/pgvector:pg16",
	))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	}
	t.Cleanup(cleanup)

	portRaw := strings.TrimSpace(runCommand(t, exec.Command("docker", "port", name, "5432/tcp")))
	parts := strings.Split(portRaw, ":")
	port := parts[len(parts)-1]
	host := objectiveE2EDockerHost()
	dsn := fmt.Sprintf("postgres://orchestrator:orchestrator@%s:%s/orchestrator", host, port)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			err = pool.Ping(ctx)
			pool.Close()
		}
		cancel()
		if err == nil {
			return dsn, cleanup
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("postgres container %s did not become ready", name)
	return "", cleanup
}

func objectiveE2EDockerHost() string {
	if host := strings.TrimSpace(os.Getenv("LAB_OBJECTIVE_E2E_DOCKER_HOST")); host != "" {
		return host
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "host.docker.internal"
	}
	return "127.0.0.1"
}

func applyObjectiveE2ESchema(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	schema := `
CREATE TYPE taskstate AS ENUM ('created','queued','assigned','in_progress','waiting_approval','review','retrying','completed','failed','cancelled');
CREATE TYPE approvalstatus AS ENUM ('pending','approved','rejected','expired');

CREATE TABLE tasks (
  id UUID PRIMARY KEY,
  state taskstate NOT NULL,
  description TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  assigned_agent TEXT NULL,
  priority TEXT NOT NULL,
  execution_target TEXT NOT NULL,
  workspace_path TEXT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  max_retries INTEGER NOT NULL DEFAULT 3,
  idempotency_key TEXT NULL,
  correlation_id UUID NOT NULL,
  results JSONB NULL,
  error_message TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ NULL,
  completed_at TIMESTAMPTZ NULL,
  queued_at TIMESTAMPTZ NULL,
  parent_task_id UUID NULL,
  root_task_id UUID NOT NULL,
  task_kind TEXT NOT NULL,
  initiative_id UUID NULL,
  planned_agent TEXT NULL,
  archived_at TIMESTAMPTZ NULL
);

CREATE TABLE state_transitions (
  id UUID PRIMARY KEY,
  task_id UUID NOT NULL,
  from_state TEXT NOT NULL,
  to_state TEXT NOT NULL,
  actor TEXT NOT NULL,
  reason TEXT NOT NULL,
  timestamp TIMESTAMPTZ NOT NULL
);

CREATE TABLE approvals (
  id UUID PRIMARY KEY,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  action_type TEXT NOT NULL,
  target_resource TEXT NOT NULL,
  status approvalstatus NOT NULL,
  operator TEXT NULL,
  timeout_seconds INTEGER NOT NULL,
  escalation_level INTEGER NOT NULL DEFAULT 1,
  requested_at TIMESTAMPTZ NOT NULL,
  resolved_at TIMESTAMPTZ NULL,
  timeout_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE initiatives (
  id UUID PRIMARY KEY,
  title TEXT NOT NULL,
  workspace_root TEXT NOT NULL,
  goal TEXT NOT NULL,
  status TEXT NOT NULL,
  current_phase TEXT NOT NULL,
  active_requirements_artifact_id UUID NULL,
  active_design_artifact_id UUID NULL,
  active_plan_artifact_id UUID NULL,
  created_by TEXT NOT NULL,
  execution_mode TEXT NOT NULL,
  archived_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE tasks
  ADD CONSTRAINT fk_tasks_initiative
  FOREIGN KEY (initiative_id) REFERENCES initiatives(id) ON DELETE SET NULL;

CREATE TABLE initiative_task_links (
  id UUID PRIMARY KEY,
  initiative_id UUID NOT NULL REFERENCES initiatives(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  phase_origin TEXT NOT NULL,
  epic TEXT NULL,
  launch_group TEXT NULL,
  execution_mode TEXT NOT NULL,
  launch_order INTEGER NOT NULL DEFAULT 10,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (initiative_id, task_id)
);

CREATE TABLE local_bridges (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  hostname TEXT NOT NULL,
  workspace_root TEXT NOT NULL,
  status TEXT NOT NULL,
  capabilities JSONB NOT NULL DEFAULT '{}'::jsonb,
  api_key_name TEXT NULL,
  last_heartbeat TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tool_invocations (
  id UUID PRIMARY KEY,
  task_id UUID NULL REFERENCES tasks(id) ON DELETE SET NULL,
  agent_type TEXT NULL,
  entrypoint TEXT NOT NULL,
  capability TEXT NOT NULL,
  input_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  output_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
  artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
  error_message TEXT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artifacts (
  id UUID PRIMARY KEY,
  task_id UUID NULL REFERENCES tasks(id) ON DELETE SET NULL,
  invocation_id UUID NULL REFERENCES tool_invocations(id) ON DELETE SET NULL,
  artifact_type TEXT NOT NULL,
  title TEXT NULL,
  uri TEXT NULL,
  media_type TEXT NULL,
  content_text TEXT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
}

func postJSONStatus(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int, out any) {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: expected status %d, got %d body=%s", method, path, wantStatus, rec.Code, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode response: %v body=%s", method, path, err, rec.Body.String())
		}
	}
}

func claimNextBridgeTask(t *testing.T, handler http.Handler, bridgeID string) *domain.LocalBridgeTaskClaimResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bridges/%s/claim-next", bridgeID), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim next: expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "null" || body == "" {
		return nil
	}
	var claim domain.LocalBridgeTaskClaimResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &claim); err != nil {
		t.Fatalf("claim next decode failed: %v body=%s", err, rec.Body.String())
	}
	return &claim
}

func firstPendingApproval(t *testing.T, handler http.Handler) *domain.ApprovalResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/approvals?status_filter=pending&limit=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list approvals: expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp domain.ApprovalListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("list approvals decode failed: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Items) == 0 {
		return nil
	}
	return &resp.Items[0]
}

func approveApprovalForTest(t *testing.T, handler http.Handler, approvalID string) {
	t.Helper()
	postJSONStatus(t, handler, http.MethodPost, fmt.Sprintf("/approvals/%s/approve", approvalID), domain.ApprovalResolveRequest{
		Operator: "objective-e2e",
	}, http.StatusOK, nil)
}

func runCommand(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v\n%s", cmd.Args, err, string(output))
	}
	return string(output)
}

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v\n%s", args, dir, err, string(output))
	}
}
