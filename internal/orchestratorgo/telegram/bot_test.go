package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestParseCommand(t *testing.T) {
	if got := parseCommand("/tasks"); got != "tasks" {
		t.Fatalf("expected tasks, got %q", got)
	}
	if got := parseCommand("/approve 123"); got != "approve" {
		t.Fatalf("expected approve, got %q", got)
	}
	if got := parseCommand("hola"); got != "" {
		t.Fatalf("expected empty command, got %q", got)
	}
}

func TestTrimForButton(t *testing.T) {
	if got := trimForButton("short", 10); got != "short" {
		t.Fatalf("unexpected trim result: %q", got)
	}
	if got := trimForButton("this is a fairly long label", 12); got != "this is a..." {
		t.Fatalf("unexpected trimmed label: %q", got)
	}
}

func TestUpdateUnmarshalCallbackQuery(t *testing.T) {
	raw := []byte(`{
		"update_id": 1,
		"callback_query": {
			"id": "cb1",
			"data": "approve:123",
			"message": {
				"message_id": 7,
				"text": "/approvals",
				"chat": {"id": 42},
				"from": {"id": 99, "username": "bot-user"}
			},
			"from": {"id": 99, "username": "bot-user"}
		}
	}`)
	var up update
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if up.CallbackQuery == nil || up.CallbackQuery.Data != "approve:123" {
		t.Fatalf("callback query not parsed correctly: %#v", up.CallbackQuery)
	}
	if up.CallbackQuery.Message == nil || up.CallbackQuery.Message.Chat.ID != 42 {
		t.Fatalf("callback message not parsed correctly: %#v", up.CallbackQuery.Message)
	}
}

func TestFormatInitiativeSummaryMentionsBacklog(t *testing.T) {
	item := &domain.InitiativeResponse{
		ID:            "initiative-1",
		Status:        domain.InitiativeStatusPlanReview,
		CurrentPhase:  domain.InitiativePhasePlan,
		WorkspaceRoot: "/tmp/workspace",
		Goal:          "Ship something useful",
	}
	withoutBacklog := formatInitiativeSummary(item, nil)
	if !strings.Contains(withoutBacklog, "Backlog: no") {
		t.Fatalf("expected backlog=no summary, got %q", withoutBacklog)
	}
	withBacklog := formatInitiativeSummary(item, []domain.InitiativeTaskLinkResponse{{TaskID: "task-1", ExecutionMode: domain.TaskLaunchModeAgentLocal}})
	if !strings.Contains(withBacklog, "Backlog: sí") {
		t.Fatalf("expected backlog=yes summary, got %q", withBacklog)
	}
	if !strings.Contains(withBacklog, "Modes: manual=0 local=1 remote=0") {
		t.Fatalf("expected mode summary, got %q", withBacklog)
	}
}
