package runner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestPlannerExecutionUsesRemotePlanner(t *testing.T) {
	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"Deploy API safely\",\"plan\":\"1. Validate env\\n2. Deploy\\n3. Verify\",\"subtasks\":[{\"title\":\"Review env\",\"description\":\"Check secrets and configuration\",\"assigned_agent\":\"coder\",\"priority\":\"normal\",\"requires_approval\":false}]}"}}]}`))
	}))
	defer plannerServer.Close()

	plannerAgent := domain.AgentTypePlanner
	task := &domain.TaskResponse{
		ID:            "task-1",
		Description:   "Plan a safe deployment",
		AssignedAgent: &plannerAgent,
		Metadata:      map[string]any{},
	}
	cfg := config.Config{
		LlamaPlannerBaseURL: plannerServer.URL,
		LlamaPlannerAPIKey:  "test",
		LlamaTimeoutSeconds: 5,
		DefaultGitBranch:    "main",
	}
	r := New(cfg, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	if got := strings.TrimSpace(asString(result.Results["plan_summary"])); got != "Deploy API safely" {
		t.Fatalf("unexpected plan summary: %q", got)
	}
}

func TestCoderWriteFileFallbackStaysAvailable(t *testing.T) {
	coderAgent := domain.AgentTypeCoder
	workspace := t.TempDir()
	task := &domain.TaskResponse{
		ID:            "task-2",
		Description:   "Write a file",
		AssignedAgent: &coderAgent,
		WorkspacePath: &workspace,
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":    "write_file",
				"path":    "notes/out.txt",
				"content": "hello from runner test",
			},
		},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace, "notes/out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "hello from runner test" {
		t.Fatalf("unexpected file content: %q", string(body))
	}
}

func TestAiderExecutionRequiresRepoMetadata(t *testing.T) {
	coderAgent := domain.AgentTypeCoder
	workspace := t.TempDir()
	task := &domain.TaskResponse{
		ID:            "task-3",
		Description:   "Implement change",
		AssignedAgent: &coderAgent,
		WorkspacePath: &workspace,
		Metadata:      map[string]any{},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	_, err := os.Stat(filepath.Join(workspace, "task-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestPlannerBoilerplateFlowGeneratesMultiAgentSubtasks(t *testing.T) {
	plannerAgent := domain.AgentTypePlanner
	task := &domain.TaskResponse{
		ID:            "task-project-root",
		Description:   "Create a CLI boilerplate",
		AssignedAgent: &plannerAgent,
		Metadata: map[string]any{
			"workspace_root": "/tmp/lab-tui-e2e",
			"project_request": map[string]any{
				"project_name":     "demo-cli",
				"parent_directory": "/tmp/lab-tui-e2e/projects",
				"project_type":     "cli_simple",
				"runtime_or_stack": "python",
				"goal":             "Validate project scaffolding",
				"test_focus":       "bridge execution",
				"initialize_git":   true,
			},
		},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected planner status: %#v", result)
	}
	subtasks, ok := result.Results["subtasks"].([]map[string]any)
	if !ok {
		t.Fatalf("expected structured subtasks, got %#v", result.Results["subtasks"])
	}
	if len(subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %#v", subtasks)
	}
	if subtasks[0]["assigned_agent"] != "researcher" || subtasks[1]["assigned_agent"] != "coder" || subtasks[2]["assigned_agent"] != "reviewer" {
		t.Fatalf("unexpected planner subtasks: %#v", subtasks)
	}
	requestedAgents, ok := result.Results["requested_agents"].([]string)
	if !ok || len(requestedAgents) != 3 {
		t.Fatalf("unexpected requested_agents payload: %#v", result.Results["requested_agents"])
	}
}

func TestPlannerRepoWorkflowGeneratesDeterministicRepoSubtasks(t *testing.T) {
	plannerAgent := domain.AgentTypePlanner
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "python-slugify")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	task := &domain.TaskResponse{
		ID:            "task-existing-repo",
		Description:   "Patch an existing repository",
		AssignedAgent: &plannerAgent,
		Metadata: map[string]any{
			"workspace_root": workspaceRoot,
			"project_request": map[string]any{
				"project_root": ".",
				"goal":         "Validate regex pattern wiring",
			},
		},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected planner status: %#v", result)
	}
	if got := asString(result.Results["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1, got %q", got)
	}
	subtasks, ok := result.Results["subtasks"].([]map[string]any)
	if !ok || len(subtasks) != 3 {
		t.Fatalf("expected 3 subtasks, got %#v", result.Results["subtasks"])
	}
	if subtasks[1]["assigned_agent"] != "coder" || !asBool(subtasks[1]["requires_approval"]) {
		t.Fatalf("expected approval-gated coder task, got %#v", subtasks[1])
	}
	coderMetadata := subtasks[1]["metadata"].(map[string]any)
	toolRequest := coderMetadata["tool_request"].(map[string]any)
	if asString(toolRequest["tool"]) != "apply_patch" {
		t.Fatalf("expected apply_patch tool request, got %#v", toolRequest["tool"])
	}
	if asString(toolRequest["patch"]) == "" {
		t.Fatal("expected deterministic patch payload")
	}
}

func TestReviewerValidatesGeneratedProject(t *testing.T) {
	root := t.TempDir()
	projectRoot := filepath.Join(root, "demo-cli")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"README.md":           "# demo\n",
		"main.py":             "print('hello')\n",
		"tests/test_smoke.py": "def test_smoke():\n    assert True\n",
		"lab.json":            "{\"project\":\"demo-cli\"}\n",
	}
	for rel, content := range files {
		path := filepath.Join(projectRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reviewerAgent := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review-1",
		Description:   "Review generated project",
		AssignedAgent: &reviewerAgent,
		Metadata: map[string]any{
			"workspace_root": root,
			"project_request": map[string]any{
				"project_name":     "demo-cli",
				"parent_directory": root,
				"project_type":     "cli_simple",
				"runtime_or_stack": "python",
				"test_command":     []string{"python3", "-c", "print('ok')"},
				"expected_files":   []string{"README.md", "main.py", "tests/test_smoke.py", "lab.json"},
			},
		},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected reviewer status: %#v", result)
	}
	if asString(result.Results["validation_summary"]) == "" {
		t.Fatalf("expected validation summary, got %#v", result.Results)
	}
}

func TestReviewerIncludesSemanticFailureMemory(t *testing.T) {
	metadata := map[string]any{
		"context_package": &domain.ContextPackage{
			SourceRefs: []string{"task:failed#0"},
			OperationalIR: &domain.OperationalIR{
				ValidationRules: []string{"Avoid missing lab.json"},
				Invalid: []domain.OperationalItem{{
					SourceRef:     "task:failed#0",
					Outcome:       "failed",
					FailureReason: "lab.json missing",
				}},
			},
		},
	}
	sources, rules, checks := reviewerSemanticMemory(metadata)
	if len(sources) != 1 || sources[0] != "task:failed#0" {
		t.Fatalf("unexpected sources: %#v", sources)
	}
	if len(rules) != 1 || rules[0] != "Avoid missing lab.json" {
		t.Fatalf("unexpected rules: %#v", rules)
	}
	if len(checks) != 1 || !strings.Contains(checks[0], "lab.json missing") {
		t.Fatalf("unexpected checks: %#v", checks)
	}
}

func TestBuildRepoWorkflowPlanUsesBenchmarkCaseFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	casePayload := map[string]any{
		"id":                        "bench-case-runner",
		"case_type":                 "deterministic_bugfix",
		"repo_profile":              "go_cli_tui",
		"repo_url":                  "https://github.com/charmbracelet/glow",
		"default_branch":            "master",
		"project_type":              "existing_repo",
		"runtime_or_stack":          "go",
		"project_root":              ".",
		"test_focus":                "markdown rendering regression",
		"test_command":              []string{"go", "test", "./..."},
		"expected_files":            []string{"main.go", "glow_test.go"},
		"coder_tool":                "write_file",
		"patch_target":              ".lab/repo-workflow-marker.txt",
		"write_content":             "benchmark marker\n",
		"coder_summary":             "Apply benchmark-defined deterministic repo change.",
		"benchmark_memory_mode":     "on",
		"benchmark_memory_strategy": "repo_specific_first",
	}
	raw, _ := json.Marshal(casePayload)
	if err := os.WriteFile(filepath.Join(root, ".lab", "benchmark-case.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	result, ok := buildRepoWorkflowPlan(map[string]any{
		"workspace_root": root,
		"project_request": map[string]any{
			"goal": "Run benchmark workflow",
		},
	})
	if !ok {
		t.Fatal("expected repo workflow plan")
	}
	subtasks := result.Results["subtasks"].([]map[string]any)
	coder := subtasks[1]
	metadata := coder["metadata"].(map[string]any)
	if got := asString(metadata["benchmark_case_id"]); got != "bench-case-runner" {
		t.Fatalf("expected benchmark case id, got %q", got)
	}
	projectRequest := metadata["project_request"].(map[string]any)
	if got := asString(projectRequest["runtime_or_stack"]); got != "go" {
		t.Fatalf("expected runtime_or_stack from benchmark case, got %q", got)
	}
	toolRequest := metadata["tool_request"].(map[string]any)
	if got := asString(toolRequest["path"]); got != ".lab/repo-workflow-marker.txt" {
		t.Fatalf("expected benchmark patch target, got %q", got)
	}
}
