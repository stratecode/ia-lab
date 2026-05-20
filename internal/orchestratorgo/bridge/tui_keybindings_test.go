package bridge

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestChatModeAllowsPlainTypingButUsesCtrlTForTaskCreation(t *testing.T) {
	m := &TUIModel{
		mode:      tuiModeChat,
		chatInput: textInputForTest(),
	}
	m.chatInput.Focus()

	model, _ := m.handleChatMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	updated := model.(*TUIModel)
	if got := updated.chatInput.Value(); got != "t" {
		t.Fatalf("expected chat input to contain typed rune, got %q", got)
	}

	updated.chatInput.SetValue("turn this into a task")
	_, cmd := updated.handleChatMode(tea.KeyMsg{Type: tea.KeyCtrlT})
	if cmd == nil {
		t.Fatal("ctrl+t should create a task command")
	}
}

func TestNormalModeRequiresCtrlShortcuts(t *testing.T) {
	m := &TUIModel{
		mode:        tuiModeNormal,
		viewIdx:     indexOfString(tuiViews, "Overview"),
		state:       defaultTUIState(),
		chatInput:   textInputForTest(),
		projectForm: newProjectForm(CLIOptions{WorkspaceRoot: "/tmp/ws"}, defaultTUIState()),
	}

	model, _ := m.handleNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	updated := model.(*TUIModel)
	if updated.mode != tuiModeNormal {
		t.Fatalf("plain p should not open project mode, got %s", updated.mode)
	}

	model, _ = updated.handleNormalMode(tea.KeyMsg{Type: tea.KeyCtrlP})
	updated = model.(*TUIModel)
	if updated.mode != tuiModeProject {
		t.Fatalf("ctrl+p should open project mode, got %s", updated.mode)
	}

	updated.mode = tuiModeNormal
	updated.viewIdx = indexOfString(tuiViews, "Tasks")
	model, _ = updated.handleNormalMode(tea.KeyMsg{Type: tea.KeyCtrlG})
	updated = model.(*TUIModel)
	if updated.mode != tuiModeChat || updated.currentView() != "Chat" {
		t.Fatalf("ctrl+g should open chat from any view, got mode=%s view=%s", updated.mode, updated.currentView())
	}
}

func TestApprovalKeysRequireCtrlModifier(t *testing.T) {
	m := &TUIModel{
		mode:             tuiModeNormal,
		approvals:        []domain.ApprovalResponse{{ID: "approval-1", TaskID: "task-1", TargetResource: "repo", ActionType: "apply_patch", Status: domain.ApprovalPending}},
		selectedApproval: 0,
		opts:             CLIOptions{Name: "tester"},
	}

	model, cmd := m.handleApprovalKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated := model.(*TUIModel)
	if cmd != nil {
		t.Fatal("plain a should not approve")
	}
	if updated.selectedApproval != 0 {
		t.Fatalf("plain a should not change selection, got %d", updated.selectedApproval)
	}

	_, cmd = updated.handleApprovalKeys(tea.KeyMsg{Type: tea.KeyCtrlA})
	if cmd == nil {
		t.Fatal("ctrl+a should trigger approval command")
	}
}

func textInputForTest() textinput.Model {
	input := textinput.New()
	input.CharLimit = 200
	input.Width = 40
	return input
}
