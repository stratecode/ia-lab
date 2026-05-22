package initiative

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
)

func TestGenerateRequirementsFallsBackToStructuredShape(t *testing.T) {
	svc := New(config.Config{}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-1",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Turn an idea into approved requirements",
	}
	artifacts, err := svc.GenerateRequirements(context.Background(), item, "Focus on operator approvals")
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.JSON["objective"] != item.Goal {
		t.Fatalf("unexpected requirements payload: %#v", artifacts.JSON)
	}
	if artifacts.Markdown == "" {
		t.Fatal("expected markdown output")
	}
}

func TestGenerateRequirementsFallsBackWhenPlannerPayloadIsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"title":"broken","scope":["x"]}`}},
			},
		})
	}))
	defer server.Close()

	svc := New(config.Config{
		LlamaPlannerBaseURL: server.URL + "/v1",
		LlamaTimeoutSeconds: 5,
		LlamaPlannerAPIKey:  "",
	}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-invalid-req",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Turn an idea into approved requirements",
	}
	artifacts, err := svc.GenerateRequirements(context.Background(), item, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["objective"]); got != item.Goal {
		t.Fatalf("expected fallback objective %q, got %q", item.Goal, got)
	}
}

func TestGenerateExecutionPlanFallsBackToMultiAgentBacklog(t *testing.T) {
	svc := New(config.Config{}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-2",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Create a runnable MVP scaffold",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	foundAgents := map[string]bool{}
	switch rawEpics := artifacts.JSON["epics"].(type) {
	case []any:
		for _, epicRaw := range rawEpics {
			epic, _ := epicRaw.(map[string]any)
			rawTasks, _ := epic["tasks"].([]any)
			for _, taskRaw := range rawTasks {
				task, _ := taskRaw.(map[string]any)
				foundAgents[asString(task["suggested_agent"])] = true
			}
		}
	case []map[string]any:
		for _, epic := range rawEpics {
			switch rawTasks := epic["tasks"].(type) {
			case []any:
				for _, taskRaw := range rawTasks {
					task, _ := taskRaw.(map[string]any)
					foundAgents[asString(task["suggested_agent"])] = true
				}
			case []map[string]any:
				for _, task := range rawTasks {
					foundAgents[asString(task["suggested_agent"])] = true
				}
			}
		}
	default:
		t.Fatalf("unexpected execution plan payload: %#v", artifacts.JSON)
	}
	for _, agent := range []string{"researcher", "coder", "reviewer"} {
		if !foundAgents[agent] {
			t.Fatalf("expected %s in generated backlog, got %#v", agent, artifacts.JSON)
		}
	}
}

func TestGenerateExecutionPlanFallsBackWhenPlannerPayloadIsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"title":"broken","epics":[]}`}},
			},
		})
	}))
	defer server.Close()

	svc := New(config.Config{
		LlamaPlannerBaseURL: server.URL + "/v1",
		LlamaTimeoutSeconds: 5,
	}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-invalid-plan",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Create a runnable MVP scaffold",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["title"]); got != item.Title {
		t.Fatalf("expected fallback title %q, got %q", item.Title, got)
	}
}

func TestGenerateExecutionPlanUsesRepoWorkflowForExistingGitRepo(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-3",
		Title:         "Repo maintenance MVP",
		WorkspaceRoot: filepath.Join(root, "python-slugify"),
		Goal:          "Patch the CLI regression and validate the repository",
	}
	if err := os.MkdirAll(item.WorkspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(item.WorkspaceRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1, got %q", got)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	if !asBool(coderTask["approval_required"]) {
		t.Fatal("expected coder task to require approval")
	}
	metadata := coderTask["metadata"].(map[string]any)
	toolRequest := metadata["tool_request"].(map[string]any)
	if asString(toolRequest["tool"]) != "apply_patch" {
		t.Fatalf("expected apply_patch tool, got %#v", toolRequest["tool"])
	}
	if asString(toolRequest["patch"]) == "" {
		t.Fatal("expected deterministic patch payload")
	}
}

func TestGenerateExecutionPlanUsesBenchmarkCaseFileWhenPresent(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "bench-repo")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	casePayload := map[string]any{
		"id":                        "bench-case-1",
		"case_type":                 "review_only",
		"repo_profile":              "python_cli_framework",
		"repo_url":                  "https://github.com/pallets/click",
		"default_branch":            "main",
		"project_type":              "existing_repo",
		"runtime_or_stack":          "python",
		"project_root":              ".",
		"test_focus":                "option parsing review",
		"test_command":              []string{"python3", "-m", "pytest", "-q", "tests/test_arguments.py"},
		"expected_files":            []string{"src/click/core.py", "tests/test_arguments.py"},
		"coder_tool":                "write_file",
		"patch_target":              ".lab/repo-workflow-marker.txt",
		"write_content":             "benchmark marker\n",
		"coder_summary":             "Apply the benchmark-defined deterministic repo change.",
		"benchmark_memory_mode":     "on",
		"benchmark_memory_strategy": "pattern_first",
	}
	raw, _ := json.Marshal(casePayload)
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".lab", "benchmark-case.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-4",
		Title:         "Repo benchmark",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	metadata := coderTask["metadata"].(map[string]any)
	if got := asString(metadata["benchmark_case_id"]); got != "bench-case-1" {
		t.Fatalf("expected benchmark case id in metadata, got %q", got)
	}
	if got := asString(metadata["context_memory_strategy"]); got != "pattern_first" {
		t.Fatalf("expected benchmark strategy in metadata, got %q", got)
	}
	projectRequest := metadata["project_request"].(map[string]any)
	if got := asString(projectRequest["repository_url"]); got != "https://github.com/pallets/click" {
		t.Fatalf("expected repository_url from benchmark case, got %q", got)
	}
}

func TestGenerateExecutionPlanUsesRepoWorkflowForBenchmarkTitleWithoutGitVisibility(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "bench-repo")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"id":                        "bench-case-2",
		"case_type":                 "deterministic_bugfix",
		"repo_profile":              "benchmark_repo_workflow",
		"repo_url":                  "https://github.com/example/demo",
		"default_branch":            "main",
		"project_type":              "existing_repo",
		"runtime_or_stack":          "python",
		"project_root":              ".",
		"coder_tool":                "write_file",
		"patch_target":              ".lab/repo-workflow-marker.txt",
		"write_content":             "benchmark marker\n",
		"coder_summary":             "Apply the benchmark-defined deterministic repo change.",
		"benchmark_memory_mode":     "on",
		"benchmark_memory_strategy": "repo_specific_first",
	})
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".lab", "benchmark-case.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-5",
		Title:         "Benchmark demo-case",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1 for benchmark title, got %q", got)
	}
}

func TestGenerateExecutionPlanUsesEmbeddedBenchmarkCaseWhenWorkspaceFileIsInvisible(t *testing.T) {
	svc := New(config.Config{}, nil)
	workspaceRoot := "/tmp/private-benchmark-workspace/repo"
	casePayload := map[string]any{
		"id":                        "bench-case-inline",
		"case_type":                 "review_only",
		"repo_profile":              "go_cli_tui",
		"repo_url":                  "https://github.com/charmbracelet/glow",
		"default_branch":            "main",
		"project_type":              "existing_repo",
		"runtime_or_stack":          "go",
		"project_root":              ".",
		"test_focus":                "rendering review",
		"test_command":              []string{"docker", "run", "--rm", "golang:1.25"},
		"expected_files":            []string{"main.go", "glow_test.go"},
		"coder_tool":                "write_file",
		"patch_target":              ".lab/repo-workflow-marker.txt",
		"write_content":             "benchmark marker\n",
		"coder_summary":             "Write a deterministic benchmark marker file before repository review.",
		"benchmark_memory_mode":     "on",
		"benchmark_memory_strategy": "pattern_first",
	}
	raw, _ := json.Marshal(casePayload)
	item := &domain.InitiativeResponse{
		ID:            "initiative-6",
		Title:         "Benchmark glow-render-review",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow.\n\n[BENCHMARK_CASE_JSON]\n" + string(raw) + "\n[/BENCHMARK_CASE_JSON]",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1 for embedded benchmark case, got %q", got)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	metadata := coderTask["metadata"].(map[string]any)
	projectRequest := metadata["project_request"].(map[string]any)
	if got := asString(projectRequest["repo_profile"]); got != "go_cli_tui" {
		t.Fatalf("expected repo_profile from embedded benchmark case, got %q", got)
	}
	if got := asString(projectRequest["repository_url"]); got != "https://github.com/charmbracelet/glow" {
		t.Fatalf("expected repository_url from embedded benchmark case, got %q", got)
	}
	if got := asString(metadata["context_memory_strategy"]); got != "pattern_first" {
		t.Fatalf("expected memory strategy from embedded benchmark case, got %q", got)
	}
}

func TestValidateContracts(t *testing.T) {
	if err := ValidateRequirementsPayload(map[string]any{
		"objective":           "Ship something",
		"scope":               []any{"one"},
		"out_of_scope":        []any{"two"},
		"constraints":         []any{"three"},
		"risks":               []any{"four"},
		"acceptance_criteria": []any{"five"},
		"open_questions":      []any{"six"},
		"assumptions":         []any{"seven"},
	}); err != nil {
		t.Fatalf("requirements contract should validate: %v", err)
	}
	if err := ValidateDesignPayload(map[string]any{
		"architecture":      "Go control plane",
		"components":        []any{"api"},
		"interfaces":        []any{"POST /initiatives"},
		"data_model":        map[string]any{"initiative": []any{"status"}},
		"testing_strategy":  []any{"integration"},
		"technical_risks":   []any{"drift"},
		"pending_decisions": []any{"none"},
	}); err != nil {
		t.Fatalf("design contract should validate: %v", err)
	}
	if err := ValidateExecutionPlanPayload(map[string]any{
		"epics": []any{
			map[string]any{
				"name": "Delivery",
				"tasks": []any{
					map[string]any{
						"title":              "Create scaffold",
						"description":        "Build the initial project",
						"suggested_agent":    "coder",
						"priority":           "normal",
						"execution_mode":     "agent_local",
						"execution_target":   "local",
						"definition_of_done": "Files exist",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("execution plan contract should validate: %v", err)
	}
}

func TestResolveExecutionPolicyAndModeValidation(t *testing.T) {
	policy := ResolveExecutionPolicy("/srv/ai-lab/orchestrator/workspaces", "/tmp/demo")
	if policy.Scope != "local_bridge" {
		t.Fatalf("expected local_bridge scope, got %q", policy.Scope)
	}
	task := domain.InitiativeTaskLinkResponse{
		TaskID:        "task-1",
		ExecutionMode: domain.TaskLaunchModeAgentLocal,
		Task: domain.TaskResponse{
			Metadata: map[string]any{},
		},
	}
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentLocal); err != nil {
		t.Fatalf("agent_local should be allowed: %v", err)
	}
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentRemote); err == nil {
		t.Fatal("agent_remote should be blocked for local_bridge scope")
	}
	task.Task.Metadata["approval_required"] = true
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentLocal); err != nil {
		t.Fatalf("approval-gated local task should still launch and request execution approval later: %v", err)
	}
}

func TestResearchNotesSkipsNonResearchGoals(t *testing.T) {
	svc := New(config.Config{}, research.New(research.Options{}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if got := svc.researchNotes(ctx, "Validate initiative hardening with local execution"); got != "" {
		t.Fatalf("expected no research notes for non-research goal, got %q", got)
	}
}
