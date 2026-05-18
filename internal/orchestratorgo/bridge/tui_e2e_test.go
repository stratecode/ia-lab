package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/runner"
)

func TestBoilerplateFlowCreatesProjectInTempDirAndTUIHidesArchivedCompleted(t *testing.T) {
	workspaceRoot, err := os.MkdirTemp("", "lab-tui-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspaceRoot) })

	projectRequest := map[string]any{
		"project_name":      "demo-e2e",
		"parent_directory":  filepath.Join(workspaceRoot, "projects"),
		"project_type":      "cli_simple",
		"runtime_or_stack":  "python",
		"goal":              "Verify TUI boilerplate flow end to end",
		"test_focus":        "multiagent execution",
		"initialize_git":    true,
		"requires_approval": false,
	}
	plannerAgent := domain.AgentTypePlanner
	rootTask := &domain.TaskResponse{
		ID:            "root-task-1",
		Description:   "Create a boilerplate project",
		AssignedAgent: &plannerAgent,
		Metadata: map[string]any{
			"workspace_root":  workspaceRoot,
			"project_request": projectRequest,
		},
	}
	r := runner.New(config.Config{DefaultGitBranch: "main"}, nil)
	plan := r.Execute(rootTask)
	if plan.Status != "success" {
		t.Fatalf("unexpected planner result: %#v", plan)
	}
	subtasks, ok := plan.Results["subtasks"].([]map[string]any)
	if !ok || len(subtasks) != 3 {
		t.Fatalf("unexpected subtasks payload: %#v", plan.Results["subtasks"])
	}

	executor, err := NewWorkspaceExecutor(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	executed := make([]domain.TaskResponse, 0, len(subtasks))
	for idx, spec := range subtasks {
		agent := domain.AgentType(asString(spec["assigned_agent"]))
		metadata, _ := spec["metadata"].(map[string]any)
		result, execErr := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
			TaskID:   spec["title"].(string),
			Metadata: metadata,
		})
		if execErr != nil {
			t.Fatalf("subtask %d failed: %v", idx, execErr)
		}
		if result.Status != "success" {
			t.Fatalf("subtask %d unexpected status: %#v", idx, result)
		}
		task := domain.TaskResponse{
			ID:              spec["title"].(string),
			Description:     asString(spec["description"]),
			AssignedAgent:   &agent,
			ExecutionTarget: domain.ExecutionTargetLocal,
			State:           domain.TaskStateCompleted,
			Metadata:        metadata,
			Results: map[string]any{
				"status":        result.Status,
				"summary":       derefString(result.Summary),
				"stdout":        derefString(result.Stdout),
				"stderr":        derefString(result.Stderr),
				"changed_files": result.ChangedFiles,
				"artifacts":     result.Artifacts,
			},
		}
		executed = append(executed, task)
	}

	projectRoot := filepath.Join(workspaceRoot, "projects", "demo-e2e")
	for _, rel := range []string{"README.md", "main.py", "tests/test_main.py", "lab.json"} {
		if _, err := os.Stat(filepath.Join(projectRoot, rel)); err != nil {
			t.Fatalf("expected generated file %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(projectRoot, ".git")); err != nil {
		t.Fatalf("expected project git repo: %v", err)
	}

	archivedAt := executed[0].CreatedAt
	executed[0].ArchivedAt = &archivedAt
	activeAgent := domain.AgentTypeCoder
	executed = append(executed, domain.TaskResponse{
		ID:              "active-task",
		Description:     "Still running",
		AssignedAgent:   &activeAgent,
		ExecutionTarget: domain.ExecutionTargetLocal,
		State:           domain.TaskStateInProgress,
	})
	model := &TUIModel{
		state: defaultTUIState(),
		tasks: executed,
	}
	visible := model.filteredTasks()
	if len(visible) != 1 {
		t.Fatalf("expected only one visible task by default, got %#v", visible)
	}
	model.state.ShowCompleted = true
	visible = model.filteredTasks()
	if len(visible) != 3 {
		t.Fatalf("expected completed non-archived tasks to become visible, got %#v", visible)
	}
	model.state.ShowArchived = true
	visible = model.filteredTasks()
	if len(visible) != 4 {
		t.Fatalf("expected archived tasks to become visible, got %#v", visible)
	}
}
