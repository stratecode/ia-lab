package main

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/bridge"
)

func TestParseCLIRecognizesObjectivesRunFlags(t *testing.T) {
	workspaceRoot := t.TempDir()
	opts, command, err := parseCLI([]string{
		"objectives", "run",
		"--api-key", "secret",
		"--workspace-root", workspaceRoot,
		"--objective", "Make the system do useful work.",
		"--title", "Useful autonomy",
		"--approval-mode", string(bridge.ApprovalModeLocalOnly),
	})
	if err != nil {
		t.Fatalf("parseCLI failed: %v", err)
	}
	if command != "objectives run" {
		t.Fatalf("unexpected command %q", command)
	}
	if opts.Objective != "Make the system do useful work." {
		t.Fatalf("unexpected objective opts: %#v", opts)
	}
	if opts.ObjectiveTitle != "Useful autonomy" {
		t.Fatalf("unexpected title opts: %#v", opts)
	}
	if opts.ApprovalMode != string(bridge.ApprovalModeLocalOnly) {
		t.Fatalf("unexpected approval mode opts: %#v", opts)
	}
}
