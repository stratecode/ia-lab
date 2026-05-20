package initiative

import (
	"context"
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
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentLocal); err == nil {
		t.Fatal("approval_required task should not auto-launch")
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
