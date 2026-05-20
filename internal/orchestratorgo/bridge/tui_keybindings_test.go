package bridge

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestChatModeAllowsPlainTypingButUsesAltTForTaskCreation(t *testing.T) {
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
	_, cmd := updated.handleChatMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true})
	if cmd == nil {
		t.Fatal("alt+t should create a task command")
	}
}

func TestNormalModeRequiresModifierShortcuts(t *testing.T) {
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

	model, _ = updated.handleNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}, Alt: true})
	updated = model.(*TUIModel)
	if updated.mode != tuiModeProject {
		t.Fatalf("alt+p should open project mode, got %s", updated.mode)
	}

	updated.mode = tuiModeNormal
	updated.viewIdx = indexOfString(tuiViews, "Tasks")
	model, _ = updated.handleNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}, Alt: true})
	updated = model.(*TUIModel)
	if updated.mode != tuiModeChat || updated.currentView() != "Chat" {
		t.Fatalf("alt+g should open chat from any view, got mode=%s view=%s", updated.mode, updated.currentView())
	}
}

func TestApprovalKeysRequireModifier(t *testing.T) {
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

	_, cmd = updated.handleApprovalKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}, Alt: true})
	if cmd == nil {
		t.Fatal("alt+a should trigger approval command")
	}
}

func TestNormalModeUsesAltNForInitiativeWizard(t *testing.T) {
	m := &TUIModel{
		mode:           tuiModeNormal,
		viewIdx:        indexOfString(tuiViews, "Overview"),
		state:          defaultTUIState(),
		initiativeForm: newInitiativeForm(CLIOptions{WorkspaceRoot: "/tmp/ws"}, defaultTUIState()),
	}

	model, _ := m.handleNormalMode(tea.KeyMsg{Type: tea.KeyTab})
	updated := model.(*TUIModel)
	if updated.currentView() == "Initiatives" && updated.mode == tuiModeInitiative {
		t.Fatal("plain tab should switch view, not open initiative wizard")
	}

	model, _ = updated.handleNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}, Alt: true})
	updated = model.(*TUIModel)
	if updated.mode != tuiModeInitiative {
		t.Fatalf("alt+n should open initiative wizard, got %s", updated.mode)
	}
}

func TestWorkspaceDefaultsPreferCurrentWorkspaceOverStalePreset(t *testing.T) {
	current := "/tmp/current-workspace"
	state := defaultTUIState()
	state.WizardPresets["parent_directory"] = "/tmp/old-workspace/projects"
	state.WizardPresets["initiative_workspace_root"] = "/tmp/old-workspace"

	project := newProjectForm(CLIOptions{WorkspaceRoot: current}, state)
	if got := project.ParentDirectory.Value(); got != current {
		t.Fatalf("expected project parent default to current workspace, got %q", got)
	}

	initiative := newInitiativeForm(CLIOptions{WorkspaceRoot: current}, state)
	if got := initiative.WorkspaceRoot.Value(); got != current {
		t.Fatalf("expected initiative workspace default to current workspace, got %q", got)
	}
	if got := initiative.Title.Value(); got != "" {
		t.Fatalf("expected initiative title to reset for a different workspace, got %q", got)
	}
	if got := initiative.Goal.Value(); got != "" {
		t.Fatalf("expected initiative goal to reset for a different workspace, got %q", got)
	}
}

func textInputForTest() textinput.Model {
	input := textinput.New()
	input.CharLimit = 200
	input.Width = 40
	return input
}
