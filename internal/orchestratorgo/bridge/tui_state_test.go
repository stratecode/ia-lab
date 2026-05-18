package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestTUIStateStoreSaveLoadRoundTrip(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(temp, ".config"))

	store, err := NewTUIStateStore()
	if err != nil {
		t.Fatal(err)
	}
	original := TUIState{
		LastView:      "Tasks",
		LastWorkspace: "/tmp/workspace",
		RecentProjects: []RecentProject{
			{Name: "demo", TaskID: "1234"},
		},
		WizardPresets: map[string]string{"project_name": "demo"},
	}
	if err := store.Save(original); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastView != original.LastView {
		t.Fatalf("expected last view %s, got %s", original.LastView, loaded.LastView)
	}
	if loaded.LastWorkspace != original.LastWorkspace {
		t.Fatalf("expected workspace %s, got %s", original.LastWorkspace, loaded.LastWorkspace)
	}
	if len(loaded.RecentProjects) != 1 || loaded.RecentProjects[0].Name != "demo" {
		t.Fatalf("unexpected recent projects: %#v", loaded.RecentProjects)
	}
}

func TestProjectFormBuildsPlannerProjectRequest(t *testing.T) {
	form := newProjectForm(CLIOptions{WorkspaceRoot: "/tmp/ws"}, defaultTUIState())
	form.ParentDirectory.SetValue("/tmp/ws/projects")
	form.ProjectName.SetValue("demo project")
	form.Goal.SetValue("test the project wizard")
	form.TestFocus.SetValue("approvals")
	form.ProjectTypeIdx = indexOfString(projectTypeOptions(), "debug_regression")
	form.StackIdx = indexOfString(stackOptions(), "python")
	form.InitGit = true
	form.NeedsApproval = true

	req, recent := form.toRequest("/tmp/ws")
	if req.ExecutionTarget != domain.ExecutionTargetRemote {
		t.Fatalf("expected remote execution target, got %s", req.ExecutionTarget)
	}
	if recent.Name != "demo-project" {
		t.Fatalf("expected sanitized project name, got %s", recent.Name)
	}
	if req.AssignedAgent == nil || *req.AssignedAgent != domain.AgentTypePlanner {
		t.Fatalf("expected planner root task, got %#v", req.AssignedAgent)
	}
	projectRequest, ok := req.Metadata["project_request"].(map[string]any)
	if !ok {
		t.Fatalf("expected project_request metadata")
	}
	requestedAgents, ok := req.Metadata["requested_agents"].([]string)
	if !ok || len(requestedAgents) != 3 {
		t.Fatalf("unexpected requested_agents: %#v", req.Metadata["requested_agents"])
	}
	if projectRequest["project_name"] != "demo-project" {
		t.Fatalf("unexpected project request: %#v", projectRequest)
	}
	if req.Metadata["project_flow"] != "boilerplate_v1" {
		t.Fatalf("unexpected project flow metadata: %#v", req.Metadata)
	}
}

func TestNewTUIStateStoreUsesConfigDir(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("HOME", temp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(temp, "cfg"))
	store, err := NewTUIStateStore()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(store.path) != "tui.json" {
		t.Fatalf("unexpected store path: %s", store.path)
	}
	if _, err := os.Stat(filepath.Dir(store.path)); !os.IsNotExist(err) {
		t.Fatalf("store should not create directory on init")
	}
}
