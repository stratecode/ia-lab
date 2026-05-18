package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type TUIState struct {
	LastView       string            `json:"last_view"`
	LastWorkspace  string            `json:"last_workspace"`
	ShowCompleted  bool              `json:"show_completed"`
	ShowArchived   bool              `json:"show_archived"`
	RecentProjects []RecentProject   `json:"recent_projects"`
	RecentInitiatives []RecentInitiative `json:"recent_initiatives"`
	WizardPresets  map[string]string `json:"wizard_presets"`
}

type RecentProject struct {
	Name            string `json:"name"`
	ParentDirectory string `json:"parent_directory"`
	ProjectType     string `json:"project_type"`
	RuntimeOrStack  string `json:"runtime_or_stack"`
	TaskID          string `json:"task_id"`
	CreatedAt       string `json:"created_at"`
}

type RecentInitiative struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	WorkspaceRoot string `json:"workspace_root"`
	Status        string `json:"status"`
	CreatedAt     string `json:"created_at"`
}

type TUIStateStore struct {
	path string
}

func NewTUIStateStore() (*TUIStateStore, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &TUIStateStore{
		path: filepath.Join(base, "lab-agent", "tui.json"),
	}, nil
}

func (s *TUIStateStore) Load() (TUIState, error) {
	var state TUIState
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultTUIState(), nil
		}
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	normalizeTUIState(&state)
	return state, nil
}

func (s *TUIStateStore) Save(state TUIState) error {
	normalizeTUIState(&state)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

func defaultTUIState() TUIState {
	return TUIState{
		LastView:       "Overview",
		ShowCompleted:  false,
		ShowArchived:   false,
		RecentProjects: []RecentProject{},
		RecentInitiatives: []RecentInitiative{},
		WizardPresets:  map[string]string{},
	}
}

func normalizeTUIState(state *TUIState) {
	if state.LastView == "" {
		state.LastView = "Overview"
	}
	state.LastWorkspace = strings.TrimSpace(state.LastWorkspace)
	if state.RecentProjects == nil {
		state.RecentProjects = []RecentProject{}
	}
	if state.RecentInitiatives == nil {
		state.RecentInitiatives = []RecentInitiative{}
	}
	if state.WizardPresets == nil {
		state.WizardPresets = map[string]string{}
	}
	if len(state.RecentProjects) > 8 {
		state.RecentProjects = state.RecentProjects[:8]
	}
	if len(state.RecentInitiatives) > 8 {
		state.RecentInitiatives = state.RecentInitiatives[:8]
	}
}
