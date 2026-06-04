package httpapi

import (
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestShouldStartObjectiveRepairCycleForReviewChangesRequested(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "error",
		ReviewDecision: stringPtr("changes_requested"),
	}
	if !shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected review changes_requested to trigger repair cycle")
	}
}

func TestShouldStartObjectiveRepairCycleForValidateFailure(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-validate",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindValidate),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{Status: "error"}
	if !shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected validation failure to trigger repair cycle")
	}
}

func TestShouldStartObjectiveRepairCycleStopsAtMaxIterations(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      3,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "error",
		ReviewDecision: stringPtr("changes_requested"),
	}
	if shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected repair cycle to stop at max iterations")
	}
}

func TestBuildObjectiveRepairCycleCreatesSequentialWorkItems(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-review-1",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":           "/tmp/repo",
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"project_request": map[string]any{
				"project_name": "demo-repo",
				"project_root": ".",
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"internal/orchestratorgo/httpapi/objectives.go"},
		},
	}
	contract := domain.ExecutionContract{
		Title:               "Fix demo repo",
		NormalizedObjective: "Fix the failing objective loop",
		WorkspaceRoot:       "/tmp/repo",
		ExecutionBackend:    "aider-task",
		ValidationCommands:  []string{"go", "test", "./..."},
		SuspectedPaths:      []string{"internal/orchestratorgo/httpapi/objectives.go"},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		Summary:      stringPtr("Review requested changes"),
		ErrorMessage: stringPtr("tests still fail"),
		ReviewComments: []map[string]any{
			{"severity": "high", "message": "Fix the validation failure before approval."},
		},
		ReviewDecision: stringPtr("changes_requested"),
	}

	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(workItems) != 4 {
		t.Fatalf("expected 4 repair work items, got %d", len(workItems))
	}
	if workItems[0].Kind != domain.WorkItemKindReplan || workItems[0].ID != "replan-2" {
		t.Fatalf("unexpected replan item: %#v", workItems[0])
	}
	if workItems[1].Kind != domain.WorkItemKindEdit || workItems[1].ID != "edit-2" {
		t.Fatalf("unexpected edit item: %#v", workItems[1])
	}
	if len(workItems[1].DependsOn) != 1 || workItems[1].DependsOn[0] != "replan-2" {
		t.Fatalf("unexpected edit dependencies: %#v", workItems[1].DependsOn)
	}
	if tool := strings.TrimSpace(asString(workItems[1].ToolRequest["tool"])); tool != "run_command" {
		t.Fatalf("expected edit tool run_command, got %#v", workItems[1].ToolRequest)
	}
	argv := anyStringSliceHTTPDefault(workItems[1].ToolRequest["argv"], nil)
	if len(argv) == 0 || argv[0] != "aider-task" {
		t.Fatalf("expected aider-task invocation, got %#v", argv)
	}
	if !strings.Contains(workItems[1].Description, "tests still fail") {
		t.Fatalf("expected edit description to include repair feedback, got %q", workItems[1].Description)
	}
	if got := objectiveIteration(workItems[3].Metadata); got != 2 {
		t.Fatalf("expected iteration metadata to advance to 2, got %d", got)
	}
	if reviewTool := strings.TrimSpace(asString(workItems[3].ToolRequest["tool"])); reviewTool != "review_workspace" {
		t.Fatalf("expected review_workspace tool, got %#v", workItems[3].ToolRequest)
	}
}

func TestDecodeObjectiveExecutionContractFromMetadataMap(t *testing.T) {
	value := map[string]any{
		"title":                "Objective",
		"normalized_objective": "Ship autonomous objective flow",
		"workspace_root":       "/tmp/repo",
		"execution_backend":    "aider-task",
		"change_strategy":      "edit, validate, review",
		"validation_commands":  []string{"go", "test", "./..."},
		"completion_criteria":  []string{"tests pass", "review approved"},
		"risk_level":           "medium",
		"approval_required":    true,
		"work_items": []map[string]any{
			{"id": "edit", "kind": "edit", "title": "Edit", "description": "desc", "assigned_agent": "coder", "execution_mode": "agent_local", "execution_target": "local", "backend": "aider-task", "approval_required": true, "definition_of_done": "done"},
		},
	}
	contract, err := decodeObjectiveExecutionContract(value)
	if err != nil {
		t.Fatal(err)
	}
	if contract.NormalizedObjective != "Ship autonomous objective flow" {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	if len(contract.WorkItems) != 1 || contract.WorkItems[0].Kind != domain.WorkItemKindEdit {
		t.Fatalf("unexpected work items: %#v", contract.WorkItems)
	}
}
