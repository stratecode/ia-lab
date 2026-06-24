package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
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

func TestCmdAutonomousRequiresWorkspaceAndGoal(t *testing.T) {
	bot := &Bot{}
	if got := bot.cmdAutonomous(context.Background(), "remote", "tester"); got != "El runner autónomo no está configurado." {
		t.Fatalf("unexpected response: %q", got)
	}
	bot.autonomous = &fakeAutonomousStarter{}
	if got := bot.cmdAutonomous(context.Background(), "remote", "tester"); got != "Uso: /autonomous <workspace_alias> <objetivo>" {
		t.Fatalf("unexpected usage response: %q", got)
	}
}

func TestCmdAutonomousStartsRunner(t *testing.T) {
	starter := &fakeAutonomousStarter{
		result: &domain.AutonomousRunResult{
			Summary: "Autonomous initiative queued",
		},
	}
	bot := &Bot{
		cfg:        config.Config{WorkspaceRoot: "/srv/stratecode"},
		autonomous: starter,
	}
	got := bot.cmdAutonomous(context.Background(), "remote arregla el bug del gateway", "tester")
	if got != "Autonomous initiative queued" {
		t.Fatalf("unexpected summary: %q", got)
	}
	if starter.lastReq.WorkspaceRoot != "/srv/stratecode" {
		t.Fatalf("unexpected workspace root: %q", starter.lastReq.WorkspaceRoot)
	}
	if starter.lastReq.Goal != "arregla el bug del gateway" {
		t.Fatalf("unexpected goal: %q", starter.lastReq.Goal)
	}
	if starter.lastReq.Surface != "openclaw.telegram" {
		t.Fatalf("unexpected surface: %q", starter.lastReq.Surface)
	}
}

type fakeAutonomousStarter struct {
	lastReq domain.AutonomousInitiativeRequest
	result  *domain.AutonomousRunResult
	err     error
}

func (f *fakeAutonomousStarter) StartFromChannel(_ context.Context, req domain.AutonomousInitiativeRequest) (*domain.AutonomousRunResult, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &domain.AutonomousRunResult{Summary: fmt.Sprintf("initiative %s", req.Goal)}, nil
}
