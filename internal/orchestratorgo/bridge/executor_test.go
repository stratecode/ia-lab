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

func TestWorkspaceExecutorFilesystemWriteRequiresCapabilityWhenAllowlistPresent(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"allowed_capabilities": []any{"code.analysis"},
			"tool_request": map[string]any{
				"tool":    "filesystem.write",
				"path":    "notes/output.txt",
				"content": "denied\n",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "requires allowed capability filesystem.write") {
		t.Fatalf("expected capability denial, got %v", err)
	}
}

func TestWorkspaceExecutorIgnoresParentRepoArtifactsWhenWorkspaceIsNested(t *testing.T) {
	root := initGitWorkspace(t)
	workspace := filepath.Join(root, "nested-workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.txt"), []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":    "write_file",
				"path":    "notes/output.txt",
				"content": "nested workspace\n",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "notes/output.txt" {
		t.Fatalf("expected only nested workspace change, got %#v", result.ChangedFiles)
	}
	if result.Diff != nil {
		t.Fatalf("expected no git diff leakage from parent repo, got %q", *result.Diff)
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

func TestWorkspaceExecutorPropagatesSemanticContextHits(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"context_package": map[string]any{
				"source_refs": []any{
					"artifact:repo_workflow_case:1",
					"artifact:repo_workflow_lesson:1",
				},
				"chunks": []any{
					map[string]any{
						"source_ref": "artifact:repo_workflow_case:1",
						"metadata": map[string]any{
							"memory_match_type": "technology_similar",
							"repo_profile":      "php_logging_library",
							"repository_url":    "https://github.com/Seldaek/monolog",
							"benchmark_case_id": "monolog-experience-review",
						},
					},
				},
			},
			"tool_request": map[string]any{
				"tool": "run_command",
				"argv": []any{"python3", "-c", "print('semantic-ok')"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.SemanticContextChunkCount; got != 1 {
		t.Fatalf("expected semantic chunk count 1, got %d", got)
	}
	if len(result.SemanticContextSources) != 2 {
		t.Fatalf("expected semantic sources, got %#v", result.SemanticContextSources)
	}
	if len(result.SemanticContextHits) != 1 {
		t.Fatalf("expected semantic hits, got %#v", result.SemanticContextHits)
	}
	if got := result.SemanticContextHits[0]["match_type"]; got != "technology_similar" {
		t.Fatalf("expected technology_similar hit, got %#v", result.SemanticContextHits[0])
	}
	if got := result.SemanticContextHits[0]["benchmark_case_id"]; got != "monolog-experience-review" {
		t.Fatalf("expected benchmark_case_id propagated, got %#v", result.SemanticContextHits[0])
	}
}

func TestWorkspaceExecutorScaffoldProject(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":             "scaffold_project",
				"parent_directory": "projects",
				"project_name":     "demo-tui",
				"project_type":     "cli_simple",
				"runtime_or_stack": "python",
				"goal":             "test the TUI project wizard",
				"test_focus":       "bridge execution",
				"initialize_git":   true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	readmePath := filepath.Join(root, "projects", "demo-tui", "README.md")
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "test the TUI project wizard") {
		t.Fatalf("unexpected README content: %s", string(raw))
	}
	gitDir := filepath.Join(root, "projects", "demo-tui", ".git")
	if _, err := os.Stat(gitDir); err != nil {
		t.Fatalf("expected initialized git repo: %v", err)
	}
}

func TestWorkspaceExecutorResearchAndReviewProject(t *testing.T) {
	root := initGitWorkspace(t)
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	projectRequest := map[string]any{
		"project_name":     "demo-review",
		"parent_directory": "projects",
		"project_type":     "cli_simple",
		"runtime_or_stack": "python",
		"goal":             "verify multiagent boilerplate",
		"test_focus":       "review flow",
		"initialize_git":   true,
		"test_command":     []string{"python3", "-c", "print('ok')"},
		"expected_files":   []string{"README.md", "main.py", "tests/test_main.py", "lab.json"},
	}
	if _, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":             "scaffold_project",
				"parent_directory": "projects",
				"project_name":     "demo-review",
				"project_type":     "cli_simple",
				"runtime_or_stack": "python",
				"goal":             "verify multiagent boilerplate",
				"test_focus":       "review flow",
				"initialize_git":   true,
				"test_command":     []any{"python3", "-c", "print('ok')"},
				"expected_files":   []any{"README.md", "main.py", "tests/test_main.py", "lab.json"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	researchResult, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":            "research_project",
				"project_request": projectRequest,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if researchResult.Status != "success" || len(researchResult.Artifacts) == 0 {
		t.Fatalf("unexpected research result: %#v", researchResult)
	}
	if len(researchResult.CapabilityUsage) == 0 {
		t.Fatalf("expected capability usage for research, got %#v", researchResult)
	}
	if len(researchResult.CapabilityHelped) == 0 || researchResult.CapabilityHelped[0] != "filesystem.read" {
		t.Fatalf("expected filesystem.read helped for research, got %#v", researchResult.CapabilityHelped)
	}

	reviewResult, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":           "review_project",
				"project_root":   "projects/demo-review",
				"expected_files": []any{"README.md", "main.py", "tests/test_main.py", "lab.json"},
				"test_command":   []any{"python3", "-c", "print('ok')"},
				"analysis_types": []any{"dependencies", "security"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reviewResult.Status != "success" {
		t.Fatalf("unexpected review result: %#v", reviewResult)
	}
	if len(reviewResult.Artifacts) == 0 {
		t.Fatalf("expected review artifacts, got %#v", reviewResult)
	}
	foundAnalysis := false
	for _, artifact := range reviewResult.Artifacts {
		if artifact["type"] == "code_analysis_report" {
			foundAnalysis = true
			break
		}
	}
	if !foundAnalysis {
		t.Fatalf("expected code analysis artifact, got %#v", reviewResult.Artifacts)
	}
	if len(reviewResult.CapabilityUsage) < 2 {
		t.Fatalf("expected multiple capability usage entries for review, got %#v", reviewResult.CapabilityUsage)
	}
	if !strings.Contains(strings.Join(reviewResult.CapabilityHelped, ","), "filesystem.read") || !strings.Contains(strings.Join(reviewResult.CapabilityHelped, ","), "code.analysis") {
		t.Fatalf("expected filesystem.read and code.analysis helped for review, got %#v", reviewResult.CapabilityHelped)
	}
}

func TestWorkspaceExecutorCodeAnalysis(t *testing.T) {
	root := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"left-pad":"*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":           "code_analysis",
				"project_root":   ".",
				"analysis_types": []any{"dependencies"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Stdout == nil || !strings.Contains(*result.Stdout, "package.json present without") {
		t.Fatalf("expected analysis output, got %#v", result)
	}
	if len(result.CapabilityUsage) != 1 || result.CapabilityUsage[0]["capability"] != "code.analysis" {
		t.Fatalf("expected code.analysis capability usage, got %#v", result.CapabilityUsage)
	}
	if len(result.CapabilityHelped) != 1 || result.CapabilityHelped[0] != "code.analysis" {
		t.Fatalf("expected code.analysis helped, got %#v", result.CapabilityHelped)
	}
}

func TestWorkspaceExecutorAllowsAiderTaskCommand(t *testing.T) {
	root := initGitWorkspace(t)
	binDir := t.TempDir()
	script := filepath.Join(binDir, "aider-task")
	body := "#!/bin/sh\nprintf 'aider-ok\\n'\nprintf '%s\\n' \"$@\" > \"$PWD/.lab-aider-args\"\nprintf 'marker\\n' > \"$PWD/.lab-aider-marker\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"objective_iteration":       2,
			"objective_max_iterations":  3,
			"objective_repair_feedback": "Fix the failing validation before approval.",
			"review_comments": []map[string]any{
				{"severity": "high", "message": "Keep the patch narrow."},
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"internal/orchestratorgo/httpapi/objectives.go"},
			"context_package": map[string]any{
				"prompt_section": "Previous successful objective iterations fixed similar validation drift.",
				"source_refs":    []any{"artifact:objective_iteration_summary:1"},
			},
			"tool_request": map[string]any{
				"tool": "run_command",
				"argv": []any{"aider-task", "--description", "Implement the objective cleanly."},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected aider-task result: %#v", result)
	}
	if result.Stdout == nil || !strings.Contains(*result.Stdout, "aider-ok") {
		t.Fatalf("expected aider-task stdout, got %#v", result.Stdout)
	}
	if len(result.ChangedFiles) == 0 {
		t.Fatalf("expected git artifacts after aider-task, got %#v", result)
	}
	argsRaw, err := os.ReadFile(filepath.Join(root, ".lab-aider-args"))
	if err != nil {
		t.Fatal(err)
	}
	argsText := string(argsRaw)
	if !strings.Contains(argsText, "--metadata") {
		t.Fatalf("expected aider-task metadata injection, got %s", argsText)
	}
	if !strings.Contains(argsText, "--scope-paths") {
		t.Fatalf("expected aider-task scope-paths injection, got %s", argsText)
	}
	if !strings.Contains(argsText, "--test-command") {
		t.Fatalf("expected aider-task test-command injection, got %s", argsText)
	}
	if !strings.Contains(argsText, "Fix the failing validation before approval.") {
		t.Fatalf("expected enriched description with repair feedback, got %s", argsText)
	}
}

func TestWorkspaceExecutorReviewWorkspaceDecidesApproved(t *testing.T) {
	root := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "add", "README.md")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\nupdated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":         "review_workspace",
				"project_root": ".",
				"test_command": []any{"python3", "-c", "print('ok')"},
				"execution_contract": map[string]any{
					"normalized_objective": "Update README and keep tests green.",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("expected approved review, got %#v", result)
	}
	if result.ReviewDecision == nil || *result.ReviewDecision != "approved" {
		t.Fatalf("expected approved review decision, got %#v", result.ReviewDecision)
	}
	if result.Diff == nil || !strings.Contains(*result.Diff, "updated") {
		t.Fatalf("expected diff in review result, got %#v", result.Diff)
	}
}

func TestWorkspaceExecutorReviewWorkspaceRequestsChangesWhenNoDiff(t *testing.T) {
	root := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "add", "README.md")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":         "review_workspace",
				"project_root": ".",
				"test_command": []any{"python3", "-c", "print('ok')"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected changes_requested style error, got %#v", result)
	}
	if result.ReviewDecision == nil || *result.ReviewDecision != "changes_requested" {
		t.Fatalf("expected changes_requested review decision, got %#v", result.ReviewDecision)
	}
}

func TestWorkspaceExecutorResearchProjectBuildsRepairPlan(t *testing.T) {
	root := initGitWorkspace(t)
	projectDir := filepath.Join(root, "projects", "repair-demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte("# repair-demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module repair-demo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool": "research_project",
				"project_request": map[string]any{
					"project_name":     "repair-demo",
					"project_root":     "projects/repair-demo",
					"project_type":     "existing_repo",
					"runtime_or_stack": "go",
					"goal":             "Fix the autonomous objective repair loop.",
					"test_focus":       "objective repair iteration 2",
				},
				"prior_findings": []any{
					map[string]any{"severity": "high", "message": "Validation still fails in the objective loop."},
				},
				"prior_test_results": map[string]any{
					"exit_code": 1,
				},
				"prior_changed_files": []any{"internal/orchestratorgo/httpapi/objectives.go"},
				"repair_feedback":     "Narrow the patch and keep validation green.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected research repair result: %#v", result)
	}
	if result.Stdout == nil || !strings.Contains(*result.Stdout, "\"repair_mode\": true") {
		t.Fatalf("expected repair_mode in research output, got %#v", result.Stdout)
	}
	if result.Stdout == nil || !strings.Contains(*result.Stdout, "\"next_edit_brief\"") {
		t.Fatalf("expected next_edit_brief in research output, got %#v", result.Stdout)
	}
	foundRepairPlan := false
	for _, artifact := range result.Artifacts {
		if artifact["type"] == "repair_plan" {
			foundRepairPlan = true
			break
		}
	}
	if !foundRepairPlan {
		t.Fatalf("expected repair_plan artifact, got %#v", result.Artifacts)
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

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}
