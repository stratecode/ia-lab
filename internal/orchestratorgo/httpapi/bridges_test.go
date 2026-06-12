package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			"workspace_root":      root,
			"benchmark_case_id":   "python-slugify-regex-cli",
			"benchmark_case_type": "deterministic_bugfix",
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
	if len(artifacts) != 2 {
		t.Fatalf("expected summary plus status snapshot artifacts, got %#v", artifacts)
	}
	if artifacts[0]["type"] != "objective_iteration_summary" {
		t.Fatalf("expected summary artifact, got %#v", artifacts[0])
	}
	if artifacts[1]["type"] != "objective_status_snapshot" {
		t.Fatalf("expected status snapshot artifact second, got %#v", artifacts[1])
	}
	body := asString(artifacts[0]["content_text"])
	if !strings.Contains(body, "\"review_decision\": \"approved\"") {
		t.Fatalf("expected approved review decision in artifact, got %s", body)
	}
	if !strings.Contains(body, "\"needs_repair_cycle\": false") {
		t.Fatalf("expected no repair cycle in artifact, got %s", body)
	}
	if !strings.Contains(body, "\"findings\": null") {
		t.Fatalf("expected findings field in summary artifact, got %s", body)
	}
	snapshot := asString(artifacts[1]["content_text"])
	if !strings.Contains(snapshot, "\"next_expected_action\": \"close_initiative\"") {
		t.Fatalf("expected close_initiative next action in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"remaining_retries\": 2") {
		t.Fatalf("expected remaining retries in snapshot, got %s", snapshot)
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
		Findings: []map[string]any{
			{"kind": "validation_failure", "severity": "high", "message": "tests still fail", "source": "validate", "exit_code": 1},
		},
		ChangedFiles: []string{"internal/orchestratorgo/httpapi/objectives.go"},
		TestResults:  map[string]any{"exit_code": 1},
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 3 {
		t.Fatalf("expected summary, repair signal, and status snapshot artifacts, got %#v", artifacts)
	}
	body := asString(artifacts[0]["content_text"])
	if !strings.Contains(body, "\"needs_repair_cycle\": true") {
		t.Fatalf("expected repair cycle marker in summary, got %s", body)
	}
	if !strings.Contains(body, "Fix validation before approval.") {
		t.Fatalf("expected review feedback in summary artifact, got %s", body)
	}
	if !strings.Contains(body, "\"kind\": \"validation_failure\"") {
		t.Fatalf("expected structured findings in summary artifact, got %s", body)
	}
	var signal, snapshot string
	for _, artifact := range artifacts[1:] {
		switch asString(artifact["type"]) {
		case "objective_repair_signal":
			signal = asString(artifact["content_text"])
		case "objective_status_snapshot":
			snapshot = asString(artifact["content_text"])
		}
	}
	if signal == "" {
		t.Fatalf("expected objective_repair_signal artifact, got %#v", artifacts)
	}
	if snapshot == "" {
		t.Fatalf("expected objective_status_snapshot artifact, got %#v", artifacts)
	}
	if !strings.Contains(signal, "\"next_expected_action\": \"replan\"") {
		t.Fatalf("expected replan next action in signal, got %s", signal)
	}
	if !strings.Contains(snapshot, "\"next_expected_action\": \"replan\"") {
		t.Fatalf("expected replan next action in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"remaining_retries\": 2") {
		t.Fatalf("expected remaining retries in snapshot, got %s", snapshot)
	}
}

func TestBuildObjectiveIterationArtifactsForTimeBudgetExhaustion(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	deadline := time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":                t.TempDir(),
			"objective_entrypoint":          true,
			"objective_title":               "Ship useful autonomy",
			"initiative_goal":               "Implement autonomous objective execution",
			"objective_iteration":           4,
			"objective_max_iterations":      4,
			"objective_time_budget_seconds": 1,
			"objective_started_at":          time.Now().Add(-3 * time.Second).UTC().Format(time.RFC3339Nano),
			"objective_deadline_at":         deadline,
			"work_item_kind":                string(domain.WorkItemKindValidate),
			"validation_commands":           []string{"go", "test", "./..."},
			"definition_of_done":            "Validation completes inside the objective budget.",
			"execution_contract": map[string]any{
				"title":                "Ship useful autonomy",
				"normalized_objective": "Implement autonomous objective execution",
				"workspace_root":       "/tmp/repo",
				"time_budget_seconds":  1,
			},
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		Summary:      stringPtr("Validation still failing after long edit"),
		ErrorMessage: stringPtr("validation timed out upstream"),
		TestResults:  map[string]any{"exit_code": 1},
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 2 {
		t.Fatalf("expected summary and status snapshot artifacts, got %#v", artifacts)
	}
	var snapshot string
	for _, artifact := range artifacts {
		if asString(artifact["type"]) == "objective_status_snapshot" {
			snapshot = asString(artifact["content_text"])
			break
		}
	}
	if snapshot == "" {
		t.Fatalf("expected objective_status_snapshot artifact, got %#v", artifacts)
	}
	if !strings.Contains(snapshot, "\"stop_reason\": \"time_budget_exhausted\"") {
		t.Fatalf("expected time budget stop reason in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"time_budget_seconds\": 1") {
		t.Fatalf("expected time budget seconds in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"deadline_at\":") {
		t.Fatalf("expected deadline in snapshot, got %s", snapshot)
	}
}

func TestBuildObjectiveIterationArtifactsForExhaustedRepairLoop(t *testing.T) {
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
			"objective_iteration":      3,
			"objective_max_iterations": 3,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"validation_commands":      []string{"go", "test", "./..."},
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
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 2 {
		t.Fatalf("expected summary plus status snapshot artifacts, got %#v", artifacts)
	}
	if artifacts[1]["type"] != "objective_status_snapshot" {
		t.Fatalf("expected status snapshot artifact second, got %#v", artifacts[1])
	}
	snapshot := asString(artifacts[1]["content_text"])
	if !strings.Contains(snapshot, "\"next_expected_action\": \"block_initiative\"") {
		t.Fatalf("expected block_initiative next action in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"remaining_retries\": 0") {
		t.Fatalf("expected zero remaining retries in snapshot, got %s", snapshot)
	}
	if !strings.Contains(snapshot, "\"blocker_reason\":") {
		t.Fatalf("expected blocker reason in snapshot, got %s", snapshot)
	}
}

func TestBuildObjectiveIterationArtifactsForBaselineValidationFailureTreatedAsUsefulSuccess(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-validate-baseline",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":                      t.TempDir(),
			"objective_entrypoint":                true,
			"objective_title":                     "Fix CLI regression",
			"initiative_goal":                     "Fix CLI regression",
			"objective_iteration":                 1,
			"objective_max_iterations":            3,
			"objective_baseline_validation":       true,
			"objective_next_action_after_success": "edit",
			"work_item_kind":                      string(domain.WorkItemKindValidate),
			"validation_commands":                 []string{"pytest", "-q"},
			"execution_contract": map[string]any{
				"title":                "Fix CLI regression",
				"normalized_objective": "Fix CLI regression",
				"workspace_root":       "/tmp/repo",
			},
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		Summary:      stringPtr("Ran tests"),
		ErrorMessage: stringPtr("baseline failure reproduced"),
		TestResults:  map[string]any{"exit_code": 1, "status": "error"},
		Findings: []map[string]any{
			{"kind": "validation_failure", "severity": "high", "message": "CLI regression reproduced before editing."},
		},
	}

	artifacts := buildObjectiveIterationArtifacts(task, result)
	if len(artifacts) != 2 {
		t.Fatalf("expected summary plus status snapshot artifacts, got %#v", artifacts)
	}
	summary := asString(artifacts[0]["content_text"])
	if !strings.Contains(summary, "\"result_status\": \"success\"") {
		t.Fatalf("expected effective success result status in summary, got %s", summary)
	}
	if !strings.Contains(summary, "\"raw_result_status\": \"error\"") {
		t.Fatalf("expected raw error result status in summary, got %s", summary)
	}
	snapshot := asString(artifacts[1]["content_text"])
	if !strings.Contains(snapshot, "\"next_expected_action\": \"edit\"") {
		t.Fatalf("expected next action edit in snapshot, got %s", snapshot)
	}
	if strings.Contains(snapshot, "\"next_expected_action\": \"block_initiative\"") {
		t.Fatalf("did not expect block_initiative in baseline validation snapshot, got %s", snapshot)
	}
}

func TestShouldStopObjectiveAfterSuccessWhenTimeBudgetExpired(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-edit",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":          true,
			"work_item_kind":                string(domain.WorkItemKindEdit),
			"objective_time_budget_seconds": 5,
			"objective_started_at":          time.Now().Add(-7 * time.Second).UTC().Format(time.RFC3339Nano),
			"objective_deadline_at":         time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano),
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:  "success",
		Summary: stringPtr("Edit completed, but too late."),
	}
	stopReason, blockerReason, shouldStop := shouldStopObjectiveAfterSuccess(task, result)
	if !shouldStop {
		t.Fatalf("expected overrun success to stop objective")
	}
	if stopReason != "time_budget_exhausted" {
		t.Fatalf("expected time_budget_exhausted stop reason, got %q", stopReason)
	}
	if !strings.Contains(blockerReason, "Objective time budget exhausted") {
		t.Fatalf("expected blocker reason to mention time budget exhaustion, got %q", blockerReason)
	}
}

func TestShouldNotStopObjectiveAfterApprovedReviewWhenTimeBudgetExpired(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":          true,
			"work_item_kind":                string(domain.WorkItemKindReview),
			"objective_time_budget_seconds": 5,
			"objective_started_at":          time.Now().Add(-7 * time.Second).UTC().Format(time.RFC3339Nano),
			"objective_deadline_at":         time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano),
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "success",
		Summary:        stringPtr("Review approved, just after the bell."),
		ReviewDecision: stringPtr("approved"),
	}
	stopReason, blockerReason, shouldStop := shouldStopObjectiveAfterSuccess(task, result)
	if shouldStop {
		t.Fatalf("expected approved terminal review not to stop objective, got stop_reason=%q blocker=%q", stopReason, blockerReason)
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
