package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestBuildRepoWorkflowMemoryArtifactsForReviewer(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "checkout", "-b", "master")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")

	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		WorkspacePath: stringPtr(root),
		Metadata: map[string]any{
			"workspace_root":            root,
			"benchmark_case_id":         "python-slugify-regex-cli",
			"benchmark_case_type":       "deterministic_bugfix",
			"benchmark_memory_mode":     "on",
			"benchmark_memory_strategy": "repo_specific_first",
			"project_request": map[string]any{
				"project_name":     "python-slugify",
				"project_root":     ".",
				"project_type":     "existing_repo",
				"runtime_or_stack": "python",
				"repo_profile":     "python_slugify_v1",
				"repository_url":   "https://github.com/un33k/python-slugify",
				"default_branch":   "master",
				"test_command":     []string{"python3", "-m", "pytest", "-q", "test.py"},
				"expected_files":   []string{"pyproject.toml", "slugify/__main__.py", "test.py"},
			},
			"definition_of_done": "Tests pass and review artifacts are persisted.",
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "success",
		Summary:      stringPtr("Review passed for python-slugify"),
		ChangedFiles: []string{"slugify/__main__.py", "test.py"},
		TestResults:  map[string]any{"status": "passed", "count": 83},
	}

	artifacts := buildRepoWorkflowMemoryArtifacts(task, result)
	if len(artifacts) != 4 {
		t.Fatalf("expected 4 synthesized artifacts, got %#v", artifacts)
	}
	if artifacts[0]["type"] != "repo_workflow_case" {
		t.Fatalf("expected case artifact first, got %#v", artifacts[0])
	}
	if artifacts[1]["type"] != "repo_workflow_lesson" {
		t.Fatalf("expected lesson artifact second, got %#v", artifacts[1])
	}
	if artifacts[2]["type"] != "benchmark_case" {
		t.Fatalf("expected benchmark case artifact third, got %#v", artifacts[2])
	}
	if artifacts[3]["type"] != "benchmark_run" {
		t.Fatalf("expected benchmark run artifact fourth, got %#v", artifacts[3])
	}
	if got := asString(artifacts[0]["repository_url"]); got != "https://github.com/un33k/python-slugify" {
		t.Fatalf("expected repository_url in case artifact metadata, got %q", got)
	}
	if got := asString(artifacts[0]["repo_profile"]); got != "python_slugify_v1" {
		t.Fatalf("expected repo_profile from project_request, got %q", got)
	}
	if got := asString(artifacts[0]["benchmark_case_type"]); got != "deterministic_bugfix" {
		t.Fatalf("expected benchmark_case_type in case artifact metadata, got %q", got)
	}
	caseBody := asString(artifacts[0]["content_text"])
	if !strings.Contains(caseBody, "\"resolved_branch\": \"master\"") {
		t.Fatalf("expected resolved branch in case artifact, got %s", caseBody)
	}
	if !strings.Contains(caseBody, "\"repository_url\": \"https://github.com/un33k/python-slugify\"") {
		t.Fatalf("expected repository url in case artifact, got %s", caseBody)
	}
	benchmarkBody := asString(artifacts[3]["content_text"])
	if !strings.Contains(benchmarkBody, "\"benchmark_case_id\": \"python-slugify-regex-cli\"") {
		t.Fatalf("expected benchmark case id in benchmark artifact, got %s", benchmarkBody)
	}
	lesson := asString(artifacts[1]["content_text"])
	if !strings.Contains(lesson, "Reuse this case as a trusted precedent") {
		t.Fatalf("expected trusted reuse guidance, got %s", lesson)
	}
}

func TestBuildRepoWorkflowMemoryArtifactsSkipsNonRepoWorkflow(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-coder",
		AssignedAgent: &coder,
		Metadata: map[string]any{
			"workspace_root": t.TempDir(),
		},
	}
	artifacts := buildRepoWorkflowMemoryArtifacts(task, domain.LocalBridgeResultRequest{Status: "success"})
	if len(artifacts) != 0 {
		t.Fatalf("expected no artifacts for non repo workflow, got %#v", artifacts)
	}
}

func TestBuildObjectiveIterationArtifactsForApprovedReview(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":           t.TempDir(),
			"objective_entrypoint":     true,
			"objective_title":          "Ship useful autonomy",
			"initiative_goal":          "Implement autonomous objective execution",
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"validation_commands":      []string{"go", "test", "./..."},
			"definition_of_done":       "Review approves the repaired iteration.",
			"execution_contract": map[string]any{
				"title":                "Ship useful autonomy",
				"normalized_objective": "Implement autonomous objective execution",
				"workspace_root":       "/tmp/repo",
			},
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "success",
		Summary:        stringPtr("Review approved"),
		ReviewDecision: stringPtr("approved"),
		ChangedFiles:   []string{"internal/orchestratorgo/httpapi/objectives.go"},
		TestResults:    map[string]any{"exit_code": 0},
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 1 {
		t.Fatalf("expected one objective iteration artifact, got %#v", artifacts)
	}
	if artifacts[0]["type"] != "objective_iteration_summary" {
		t.Fatalf("expected summary artifact, got %#v", artifacts[0])
	}
	body := asString(artifacts[0]["content_text"])
	if !strings.Contains(body, "\"review_decision\": \"approved\"") {
		t.Fatalf("expected approved review decision in artifact, got %s", body)
	}
	if !strings.Contains(body, "\"needs_repair_cycle\": false") {
		t.Fatalf("expected no repair cycle in artifact, got %s", body)
	}
}

func TestBuildObjectiveIterationArtifactsForRepairSignal(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":           t.TempDir(),
			"objective_entrypoint":     true,
			"objective_title":          "Ship useful autonomy",
			"initiative_goal":          "Implement autonomous objective execution",
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"validation_commands":      []string{"go", "test", "./..."},
			"definition_of_done":       "Review approves the repaired iteration.",
			"execution_contract": map[string]any{
				"title":                "Ship useful autonomy",
				"normalized_objective": "Implement autonomous objective execution",
				"workspace_root":       "/tmp/repo",
			},
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "error",
		Summary:        stringPtr("Review requested changes"),
		ErrorMessage:   stringPtr("tests still fail"),
		ReviewDecision: stringPtr("changes_requested"),
		ReviewComments: []map[string]any{
			{"severity": "high", "message": "Fix validation before approval."},
		},
		ChangedFiles: []string{"internal/orchestratorgo/httpapi/objectives.go"},
		TestResults:  map[string]any{"exit_code": 1},
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 2 {
		t.Fatalf("expected summary plus repair signal artifacts, got %#v", artifacts)
	}
	if artifacts[1]["type"] != "objective_repair_signal" {
		t.Fatalf("expected repair signal artifact second, got %#v", artifacts[1])
	}
	body := asString(artifacts[0]["content_text"])
	if !strings.Contains(body, "\"needs_repair_cycle\": true") {
		t.Fatalf("expected repair cycle marker in summary, got %s", body)
	}
	if !strings.Contains(body, "Fix validation before approval.") {
		t.Fatalf("expected review feedback in summary artifact, got %s", body)
	}
	signal := asString(artifacts[1]["content_text"])
	if !strings.Contains(signal, "\"next_expected_action\": \"replan\"") {
		t.Fatalf("expected replan next action in signal, got %s", signal)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}
