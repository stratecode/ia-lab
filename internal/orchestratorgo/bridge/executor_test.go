package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestWorkspaceExecutorRejectsEscapingPath(t *testing.T) {
	root := t.TempDir()
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool": "read_file",
				"path": "../secret.txt",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "path escapes workspace") {
		t.Fatalf("expected workspace escape error, got %v", err)
	}
}

func TestWorkspaceExecutorRejectsDisallowedCommand(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool": "run_command",
				"argv": []any{"rm", "-rf", "/"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "command not allowed") {
		t.Fatalf("expected command not allowed error, got %v", err)
	}
}

func TestWorkspaceExecutorWriteFileCollectsArtifacts(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":    "write_file",
				"path":    "notes/output.txt",
				"content": "hello from bridge\n",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	target := filepath.Join(root, "notes", "output.txt")
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "hello from bridge\n" {
		t.Fatalf("unexpected file content: %q", string(raw))
	}
	if len(result.ChangedFiles) == 0 || result.ChangedFiles[0] != "notes/output.txt" {
		t.Fatalf("expected changed file, got %#v", result.ChangedFiles)
	}
	if result.Diff == nil {
		t.Fatalf("expected diff field to be present")
	}
}

func TestWorkspaceExecutorSkipsRepeatApprovalAfterGrant(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"approval_granted": true,
			"tool_request": map[string]any{
				"tool":              "write_file",
				"path":              "notes/approved.txt",
				"content":           "approved\n",
				"requires_approval": true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success after prior approval, got %s", result.Status)
	}
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(output))
	}
	return root
}
