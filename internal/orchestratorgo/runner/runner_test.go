package runner

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestPlannerExecutionUsesRemotePlanner(t *testing.T) {
	plannerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"summary\":\"Deploy API safely\",\"plan\":\"1. Validate env\\n2. Deploy\\n3. Verify\",\"subtasks\":[{\"title\":\"Review env\",\"description\":\"Check secrets and configuration\",\"assigned_agent\":\"coder\",\"priority\":\"normal\",\"requires_approval\":false}]}"}}]}`))
	}))
	defer plannerServer.Close()

	plannerAgent := domain.AgentTypePlanner
	task := &domain.TaskResponse{
		ID:            "task-1",
		Description:   "Plan a safe deployment",
		AssignedAgent: &plannerAgent,
		Metadata:      map[string]any{},
	}
	cfg := config.Config{
		LlamaPlannerBaseURL: plannerServer.URL,
		LlamaPlannerAPIKey:  "test",
		LlamaTimeoutSeconds: 5,
		DefaultGitBranch:    "main",
	}
	r := New(cfg, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	if got := strings.TrimSpace(asString(result.Results["plan_summary"])); got != "Deploy API safely" {
		t.Fatalf("unexpected plan summary: %q", got)
	}
}

func TestCoderWriteFileFallbackStaysAvailable(t *testing.T) {
	coderAgent := domain.AgentTypeCoder
	workspace := t.TempDir()
	task := &domain.TaskResponse{
		ID:            "task-2",
		Description:   "Write a file",
		AssignedAgent: &coderAgent,
		WorkspacePath: &workspace,
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":    "write_file",
				"path":    "notes/out.txt",
				"content": "hello from runner test",
			},
		},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	body, err := os.ReadFile(filepath.Join(workspace, "notes/out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(body)) != "hello from runner test" {
		t.Fatalf("unexpected file content: %q", string(body))
	}
}

func TestAiderExecutionRequiresRepoMetadata(t *testing.T) {
	coderAgent := domain.AgentTypeCoder
	workspace := t.TempDir()
	task := &domain.TaskResponse{
		ID:            "task-3",
		Description:   "Implement change",
		AssignedAgent: &coderAgent,
		WorkspacePath: &workspace,
		Metadata:      map[string]any{},
	}
	r := New(config.Config{DefaultGitBranch: "main"}, nil)
	result := r.Execute(task)
	if result.Status != "success" {
		t.Fatalf("unexpected status: %#v", result)
	}
	_, err := os.Stat(filepath.Join(workspace, "task-result.txt"))
	if err != nil {
		t.Fatal(err)
	}
}
