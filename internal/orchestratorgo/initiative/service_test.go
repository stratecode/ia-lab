package initiative

import (
	"context"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
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
