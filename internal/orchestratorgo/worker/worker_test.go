package worker

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestParsePlannerSubtasksAcceptsTypedSlice(t *testing.T) {
	raw := []map[string]any{
		{
			"title":             "Write file",
			"description":       "Create a harmless output file",
			"assigned_agent":    "coder",
			"priority":          "normal",
			"requires_approval": false,
		},
	}
	items := parsePlannerSubtasks(raw)
	if len(items) != 1 {
		t.Fatalf("expected 1 subtask, got %d", len(items))
	}
	if items[0].AssignedAgent != domain.AgentTypeCoder {
		t.Fatalf("unexpected assigned agent: %s", items[0].AssignedAgent)
	}
	if items[0].Description != "Create a harmless output file" {
		t.Fatalf("unexpected description: %s", items[0].Description)
	}
}
