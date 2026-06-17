package bridge

import (
	"context"
	"encoding/json"
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

func TestWorkspaceExecutorScaffoldProjectInPlace(t *testing.T) {
	root := t.TempDir()
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool":             "scaffold_project",
				"project_root":     ".",
				"project_name":     "demo-inline",
				"project_type":     "cli_simple",
				"runtime_or_stack": "python",
				"goal":             "bootstrap the workspace in place",
				"test_focus":       "greenfield objective flow",
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
	if _, err := os.Stat(filepath.Join(root, "README.md")); err != nil {
		t.Fatalf("expected in-place README scaffold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "main.py")); err != nil {
		t.Fatalf("expected in-place main.py scaffold: %v", err)
	}
	if containsString(result.ChangedFiles, "./README.md") {
		t.Fatalf("expected normalized changed files, got %#v", result.ChangedFiles)
	}
	if !containsString(result.ChangedFiles, "README.md") || !containsString(result.ChangedFiles, "main.py") {
		t.Fatalf("expected in-place changed files, got %#v", result.ChangedFiles)
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
			"objective_failure_hypotheses": []map[string]any{
				{
					"path":         "internal/orchestratorgo/httpapi/objectives.go",
					"failure_mode": "validation_failure",
					"priority":     "high",
					"evidence":     "Declared validation command exited with code 1.",
				},
			},
			"objective_patch_intent": []map[string]any{
				{
					"path":   "internal/orchestratorgo/httpapi/objectives.go",
					"action": "narrow the repair loop to the file implicated by the failed iteration",
				},
			},
			"objective_analysis_findings": []map[string]any{
				{
					"severity": "warning",
					"message":  "large source file (540 lines)",
					"location": map[string]any{"file": "internal/orchestratorgo/httpapi/legacy_objectives.go"},
				},
			},
			"objective_baseline_summary": "Baseline validation failed before editing.",
			"objective_baseline_findings": []map[string]any{
				{
					"severity": "high",
					"message":  "CLI regression reproduced before editing.",
				},
			},
			"excluded_scope_paths":              []string{"internal/orchestratorgo/httpapi/legacy_objectives.go"},
			"scope_correction_reason":           "drop the contradicted shortlist and stay on the observed diff",
			"planner_scope_resolution":          "contradicted",
			"planner_rejected_scope_resolution": "confirmed",
			"retrieval_packet_artifact_id":      "artifact-retrieval-1",
			"objective_retrieval_precedents": []map[string]any{
				{
					"source_ref": "artifact:objective_iteration_summary:1",
					"summary":    "A previous objective narrowed the patch to the validation target and passed on the second attempt.",
				},
			},
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
	if !strings.Contains(argsText, "Failure hypotheses:") || !strings.Contains(argsText, "Declared validation command exited with code 1.") {
		t.Fatalf("expected failure hypotheses in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Baseline validation:") || !strings.Contains(argsText, "CLI regression reproduced before editing.") {
		t.Fatalf("expected baseline validation evidence in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Patch intent:") || !strings.Contains(argsText, "internal/orchestratorgo/httpapi/objectives.go") {
		t.Fatalf("expected patch intent in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Static analysis findings:") || !strings.Contains(argsText, "legacy_objectives.go") {
		t.Fatalf("expected static analysis findings in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Scope correction:") || !strings.Contains(argsText, "drop the contradicted shortlist") {
		t.Fatalf("expected scope correction guidance in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Planner selected paths contradicted by review:") {
		t.Fatalf("expected explicit contradicted planner guidance in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Planner rejected paths still excluded: internal/orchestratorgo/httpapi/legacy_objectives.go") {
		t.Fatalf("expected explicit rejected-scope exclusion guidance in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Excluded paths: internal/orchestratorgo/httpapi/legacy_objectives.go") {
		t.Fatalf("expected excluded scope paths in aider-task description, got %s", argsText)
	}
	if !strings.Contains(argsText, "Retrieved precedents:") || !strings.Contains(argsText, "artifact:objective_iteration_summary:1") {
		t.Fatalf("expected retrieved precedents in aider-task description, got %s", argsText)
	}
	foundCoderPacket := false
	for _, artifact := range result.Artifacts {
		if artifact["type"] != "coder_packet" {
			continue
		}
		foundCoderPacket = true
		content := strings.TrimSpace(asString(artifact["content_text"]))
		if content == "" {
			t.Fatalf("expected coder packet content, got %#v", artifact)
		}
		var packet map[string]any
		if err := json.Unmarshal([]byte(content), &packet); err != nil {
			t.Fatalf("expected coder packet JSON, got %v", err)
		}
		if packet["tool"] != "aider-task" {
			t.Fatalf("expected aider-task coder packet, got %#v", packet)
		}
		if packet["diff_present"] != true {
			t.Fatalf("expected coder packet diff_present=true, got %#v", packet)
		}
		selfCheck, _ := packet["self_check"].(map[string]any)
		if selfCheck["diff_present"] != true {
			t.Fatalf("expected self_check diff_present=true, got %#v", selfCheck)
		}
		if packet["changed_file_count"] == nil {
			t.Fatalf("expected changed_file_count in coder packet, got %#v", packet)
		}
		break
	}
	if !foundCoderPacket {
		t.Fatalf("expected coder_packet artifact, got %#v", result.Artifacts)
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
			"initial_scope_hypotheses": []map[string]any{
				{"path": "README.md", "rationale": "planner sees README as primary target", "confidence": "high"},
			},
			"rejected_scope_hypotheses": []map[string]any{
				{"path": "legacy.md", "rationale": "planner rejected legacy docs path", "confidence": "low", "rejected_because": "ranked below README.md"},
			},
			"objective_retrieval_precedents": []map[string]any{
				{"source_ref": "artifact:repair_plan:2", "summary": "Previous README.md repair kept the patch narrow and passed validation."},
			},
			"objective_patch_intent": []map[string]any{
				{"path": "README.md", "action": "update documentation"},
			},
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
	if len(result.Artifacts) == 0 {
		t.Fatalf("expected review_packet artifact, got %#v", result.Artifacts)
	}
	packet := firstArtifactByType(result.Artifacts, "review_packet")
	if packet == nil {
		t.Fatalf("expected review_packet artifact, got %#v", result.Artifacts)
	}
	body := asString(packet["content_text"])
	if !strings.Contains(body, "\"planner_scope_alignment\":\"confirmed\"") {
		t.Fatalf("expected planner scope alignment in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"planner_rejected_scope_alignment\":\"confirmed\"") {
		t.Fatalf("expected planner rejected scope alignment in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"research_scope_alignment\":\"confirmed\"") {
		t.Fatalf("expected research scope alignment in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"retrieval_scope_alignment\":\"confirmed\"") {
		t.Fatalf("expected retrieval scope alignment in review packet, got %s", body)
	}
	if !strings.Contains(body, "artifact:repair_plan:2") {
		t.Fatalf("expected retrieval precedent refs in review packet, got %s", body)
	}
	if !strings.Contains(body, "planner_scope_confirmed") {
		t.Fatalf("expected planner_scope_confirmed finding in review packet, got %s", body)
	}
	if !strings.Contains(body, "planner_rejected_scope_confirmed") {
		t.Fatalf("expected planner_rejected_scope_confirmed finding in review packet, got %s", body)
	}
	if !strings.Contains(body, "retrieval_scope_confirmed") {
		t.Fatalf("expected retrieval_scope_confirmed finding in review packet, got %s", body)
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
	packet := firstArtifactByType(result.Artifacts, "review_packet")
	if packet == nil {
		t.Fatalf("expected review_packet artifact on rejected review, got %#v", result.Artifacts)
	}
}

func TestWorkspaceExecutorReviewWorkspaceFlagsPlannerContradiction(t *testing.T) {
	root := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "actual.py"), []byte("updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "add", "README.md", "actual.py")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "actual.py"), []byte("updated again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"initial_scope_hypotheses": []map[string]any{
				{"path": "wrong.py", "rationale": "planner guessed wrong", "confidence": "medium"},
			},
			"rejected_scope_hypotheses": []map[string]any{
				{"path": "actual.py", "rationale": "planner wrongly rejected actual.py", "confidence": "low", "rejected_because": "legacy tie-break"},
			},
			"objective_retrieval_precedents": []map[string]any{
				{"source_ref": "artifact:repair_plan:9", "summary": "Previous README.md repair stayed in docs only."},
			},
			"objective_patch_intent": []map[string]any{
				{"path": "actual.py", "action": "update implementation"},
			},
			"tool_request": map[string]any{
				"tool":         "review_workspace",
				"project_root": ".",
				"test_command": []any{"python3", "-c", "print('ok')"},
				"execution_contract": map[string]any{
					"title": "Objective",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	packet := firstArtifactByType(result.Artifacts, "review_packet")
	if packet == nil {
		t.Fatalf("expected review_packet artifact, got %#v", result.Artifacts)
	}
	body := asString(packet["content_text"])
	if !strings.Contains(body, "\"planner_scope_alignment\":\"contradicted\"") {
		t.Fatalf("expected contradicted planner scope alignment, got %s", body)
	}
	if !strings.Contains(body, "\"planner_rejected_scope_alignment\":\"contradicted\"") {
		t.Fatalf("expected planner rejected scope alignment in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"research_scope_alignment\":\"confirmed\"") {
		t.Fatalf("expected confirmed research scope alignment, got %s", body)
	}
	if !strings.Contains(body, "\"retrieval_scope_alignment\":\"contradicted\"") {
		t.Fatalf("expected contradicted retrieval scope alignment, got %s", body)
	}
	if !strings.Contains(body, "\"memory_utility\":") {
		t.Fatalf("expected memory utility block in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"precedents_contradicted\":[\"artifact:repair_plan:9\"]") {
		t.Fatalf("expected contradicted precedent tracking in review packet, got %s", body)
	}
	if !strings.Contains(body, "\"recommendation\":\"demote\"") {
		t.Fatalf("expected demote recommendation in review packet, got %s", body)
	}
	if len(result.Findings) == 0 {
		t.Fatalf("expected structured review findings, got %#v", result.Findings)
	}
	if asString(result.Findings[0]["kind"]) != "planner_scope_contradicted" {
		t.Fatalf("expected planner_scope_contradicted finding, got %#v", result.Findings[0])
	}
	if len(result.Findings) < 2 || asString(result.Findings[1]["kind"]) != "planner_rejected_scope_contradicted" {
		t.Fatalf("expected planner_rejected_scope_contradicted finding, got %#v", result.Findings)
	}
	foundRetrievalContradiction := false
	for _, finding := range result.Findings {
		if asString(finding["kind"]) == "retrieval_scope_contradicted" {
			foundRetrievalContradiction = true
			break
		}
	}
	if !foundRetrievalContradiction {
		t.Fatalf("expected retrieval_scope_contradicted finding, got %#v", result.Findings)
	}
}

func TestWorkspaceExecutorReviewWorkspaceFlagsRejectedPlannerContradiction(t *testing.T) {
	root := initGitWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "legacy.py"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "add", "README.md", "legacy.py")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(root, "legacy.py"), []byte("old again\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"initial_scope_hypotheses": []map[string]any{
				{"path": "README.md", "rationale": "planner selected README", "confidence": "medium"},
			},
			"rejected_scope_hypotheses": []map[string]any{
				{"path": "legacy.py", "rationale": "planner rejected legacy path", "confidence": "low", "rejected_because": "ranked below README.md"},
			},
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
	packet := firstArtifactByType(result.Artifacts, "review_packet")
	if packet == nil {
		t.Fatalf("expected review_packet artifact, got %#v", result.Artifacts)
	}
	body := asString(packet["content_text"])
	if !strings.Contains(body, "\"planner_rejected_scope_alignment\":\"contradicted\"") {
		t.Fatalf("expected contradicted rejected planner scope alignment, got %s", body)
	}
	found := false
	for _, finding := range result.Findings {
		if asString(finding["kind"]) == "planner_rejected_scope_contradicted" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected planner_rejected_scope_contradicted finding, got %#v", result.Findings)
	}
}

func firstArtifactByType(items []map[string]any, kind string) map[string]any {
	for _, item := range items {
		if asString(item["type"]) == kind {
			return item
		}
	}
	return nil
}

func TestInterpretPlannerRepairFindingsPromotesRejectedChangedPaths(t *testing.T) {
	correction := interpretPlannerRepairFindings(
		[]map[string]any{
			{"kind": "planner_scope_contradicted"},
			{"kind": "planner_rejected_scope_contradicted"},
		},
		[]string{"internal/orchestratorgo/initiative/objectives.go"},
		[]string{"internal/orchestratorgo/httpapi/objectives.go"},
		[]string{"internal/orchestratorgo/httpapi/objectives.go"},
	)
	if correction.PlannerScopeResolution != "contradicted" {
		t.Fatalf("expected planner scope contradiction, got %#v", correction)
	}
	if correction.RejectedPlannerScopeResolution != "contradicted" {
		t.Fatalf("expected rejected planner contradiction, got %#v", correction)
	}
	if len(correction.PromotedRejectedPaths) != 1 || correction.PromotedRejectedPaths[0] != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected rejected changed path to be promoted, got %#v", correction.PromotedRejectedPaths)
	}
	if len(correction.ExcludedScopePaths) != 1 || correction.ExcludedScopePaths[0] != "internal/orchestratorgo/initiative/objectives.go" {
		t.Fatalf("expected contradicted shortlist path to be excluded, got %#v", correction.ExcludedScopePaths)
	}
}

func TestInterpretPlannerRepairFindingsKeepsConfirmedAndExcludedPaths(t *testing.T) {
	correction := interpretPlannerRepairFindings(
		[]map[string]any{
			{"kind": "planner_scope_confirmed"},
			{"kind": "planner_rejected_scope_confirmed"},
		},
		[]string{"src/feature_flag.py"},
		[]string{"src/legacy_flag.py"},
		[]string{"src/feature_flag.py"},
	)
	if correction.PlannerScopeResolution != "confirmed" {
		t.Fatalf("expected planner scope confirmation, got %#v", correction)
	}
	if len(correction.ConfirmedPlannerPaths) != 1 || correction.ConfirmedPlannerPaths[0] != "src/feature_flag.py" {
		t.Fatalf("expected confirmed planner path, got %#v", correction.ConfirmedPlannerPaths)
	}
	if correction.RejectedPlannerScopeResolution != "confirmed" {
		t.Fatalf("expected rejected planner scope confirmation, got %#v", correction)
	}
	if len(correction.ExcludedScopePaths) != 1 || correction.ExcludedScopePaths[0] != "src/legacy_flag.py" {
		t.Fatalf("expected rejected competitor to stay excluded, got %#v", correction.ExcludedScopePaths)
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
					map[string]any{"kind": "planner_scope_contradicted", "severity": "medium", "message": "The observed diff contradicts the planner shortlist."},
					map[string]any{"kind": "planner_rejected_scope_contradicted", "severity": "medium", "message": "The observed diff landed in a path the planner rejected."},
				},
				"prior_test_results": map[string]any{
					"exit_code": 1,
				},
				"prior_changed_files": []any{"internal/orchestratorgo/httpapi/objectives.go"},
				"repair_feedback":     "Narrow the patch and keep validation green.",
				"initial_scope_hypotheses": []any{
					map[string]any{"path": "internal/orchestratorgo/initiative/objectives.go"},
				},
				"rejected_scope_hypotheses": []any{
					map[string]any{"path": "internal/orchestratorgo/httpapi/objectives.go"},
				},
				"patch_intent": []any{
					map[string]any{"path": "internal/orchestratorgo/initiative/objectives.go", "action": "retry the original shortlist"},
				},
			},
			"context_package": map[string]any{
				"chunks": []any{
					map[string]any{
						"source_ref":    "artifact:repair_plan:1",
						"source_type":   "artifact",
						"source_id":     "repair-plan-1",
						"content_text":  "Previous repair narrowed scope to objectives.go and passed after focusing on the failed validation path.",
						"initiative_id": "initiative-prior",
						"task_id":       "task-prior",
						"artifact_id":   "artifact-prior",
						"score":         0.91,
					},
				},
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
	var payload map[string]any
	if err := json.Unmarshal([]byte(*result.Stdout), &payload); err != nil {
		t.Fatalf("expected valid research payload json: %v", err)
	}
	patchIntent, ok := payload["patch_intent"].([]any)
	if !ok || len(patchIntent) == 0 {
		t.Fatalf("expected patch_intent entries, got %#v", payload["patch_intent"])
	}
	firstIntent, ok := patchIntent[0].(map[string]any)
	if !ok || asString(firstIntent["path"]) != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected patch intent for changed file, got %#v", patchIntent[0])
	}
	if asString(firstIntent["failure_mode"]) != "validation_failure" {
		t.Fatalf("expected validation_failure patch intent, got %#v", firstIntent)
	}
	if asString(firstIntent["cause_category"]) != "edit_landed_in_rejected_path" {
		t.Fatalf("expected rejected-path cause category, got %#v", firstIntent)
	}
	hypotheses, ok := payload["failure_hypotheses"].([]any)
	if !ok || len(hypotheses) == 0 {
		t.Fatalf("expected failure_hypotheses entries, got %#v", payload["failure_hypotheses"])
	}
	firstHypothesis, ok := hypotheses[0].(map[string]any)
	if !ok || asString(firstHypothesis["path"]) != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected first hypothesis to target changed file, got %#v", hypotheses[0])
	}
	if asString(firstHypothesis["failure_mode"]) != "validation_failure" {
		t.Fatalf("expected validation_failure hypothesis, got %#v", firstHypothesis)
	}
	if asString(firstHypothesis["priority"]) != "high" {
		t.Fatalf("expected high-priority hypothesis, got %#v", firstHypothesis)
	}
	if asString(payload["planner_scope_resolution"]) != "contradicted" {
		t.Fatalf("expected contradicted planner scope resolution, got %#v", payload["planner_scope_resolution"])
	}
	if asString(payload["planner_rejected_scope_resolution"]) != "contradicted" {
		t.Fatalf("expected contradicted rejected planner scope resolution, got %#v", payload["planner_rejected_scope_resolution"])
	}
	excluded, ok := payload["excluded_scope_paths"].([]any)
	if !ok || len(excluded) == 0 || asString(excluded[0]) != "internal/orchestratorgo/initiative/objectives.go" {
		t.Fatalf("expected excluded contradicted shortlist path, got %#v", payload["excluded_scope_paths"])
	}
	if !strings.Contains(asString(payload["scope_correction_reason"]), "promote rejected paths") {
		t.Fatalf("expected scope correction reason, got %#v", payload["scope_correction_reason"])
	}
	if brief := asString(payload["next_edit_brief"]); !strings.Contains(brief, "Excluded paths for next attempt: internal/orchestratorgo/initiative/objectives.go") {
		t.Fatalf("expected next_edit_brief to mention excluded scope, got %q", brief)
	}
	precedents, ok := payload["retrieval_precedents"].([]any)
	if !ok || len(precedents) == 0 {
		t.Fatalf("expected retrieval_precedents in repair payload, got %#v", payload["retrieval_precedents"])
	}
	firstPrecedent, _ := precedents[0].(map[string]any)
	if asString(firstPrecedent["source_ref"]) != "artifact:repair_plan:1" {
		t.Fatalf("expected retrieval precedent source_ref, got %#v", firstPrecedent)
	}
	if !strings.Contains(asString(payload["next_edit_brief"]), "artifact:repair_plan:1") {
		t.Fatalf("expected next_edit_brief to cite retrieval precedent, got %q", asString(payload["next_edit_brief"]))
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

func TestRecommendResearchScopePathsPrioritizesPlannerCorrections(t *testing.T) {
	scope := recommendResearchScopePaths(
		"Fix the objective repair loop",
		plannerRepairScopeCorrection{
			PlannerScopeResolution:         "confirmed",
			RejectedPlannerScopeResolution: "contradicted",
			ConfirmedPlannerPaths:          []string{"internal/orchestratorgo/bridge/executor.go"},
			PromotedRejectedPaths:          []string{"internal/orchestratorgo/httpapi/objectives.go"},
			ExcludedScopePaths:             []string{"internal/orchestratorgo/initiative/objectives.go"},
		},
		[]string{"internal/orchestratorgo/httpapi/objectives.go", "internal/orchestratorgo/initiative/objectives.go"},
		[]string{"internal/orchestratorgo/initiative/objectives.go"},
		[]string{"README.md"},
		[]string{"internal/orchestratorgo/httpapi/objectives.go", "internal/orchestratorgo/bridge/executor.go", "README.md"},
		"",
	)

	if len(scope) < 3 {
		t.Fatalf("expected prioritized scope paths, got %#v", scope)
	}
	if scope[0] != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected promoted rejected path to lead scope, got %#v", scope)
	}
	if scope[1] != "internal/orchestratorgo/bridge/executor.go" {
		t.Fatalf("expected confirmed planner path to remain high priority, got %#v", scope)
	}
	if containsString(scope, "internal/orchestratorgo/initiative/objectives.go") {
		t.Fatalf("expected excluded contradicted shortlist path to be removed, got %#v", scope)
	}
}

func TestRetrievalPrecedentsFromContextPackageSelectsDiverseCompactPrecedents(t *testing.T) {
	longSummary := strings.Repeat("auth-service precedent detail ", 20)
	input := map[string]any{
		"chunks": []any{
			map[string]any{
				"source_ref":   "artifact:repo:1",
				"source_type":  "artifact",
				"source_id":    "repo-1",
				"content_text": longSummary + "internal/auth_service.go",
				"score":        0.99,
				"memory_class": "repo_specific",
			},
			map[string]any{
				"source_ref":   "artifact:repo:2",
				"source_type":  "artifact",
				"source_id":    "repo-2",
				"content_text": "recent execution touched internal/session_store.go cleanly",
				"score":        0.91,
				"memory_class": "recent_execution",
			},
			map[string]any{
				"source_ref":   "artifact:tech:1",
				"source_type":  "artifact",
				"source_id":    "tech-1",
				"content_text": "technology transfer precedent for auth middleware validation",
				"score":        0.83,
				"memory_class": "technology_similar",
			},
			map[string]any{
				"source_ref":   "artifact:pattern:1",
				"source_type":  "artifact",
				"source_id":    "pattern-1",
				"content_text": "pattern precedent for regex forwarding in auth_service.go",
				"score":        0.88,
				"memory_class": "pattern_similar",
			},
			map[string]any{
				"source_ref":   "artifact:guardrail:1",
				"source_type":  "artifact",
				"source_id":    "guardrail-1",
				"content_text": "negative guardrail: avoid touching unrelated legacy_auth.go",
				"score":        0.82,
				"memory_class": "negative_guardrail",
			},
			map[string]any{
				"source_ref":   "artifact:repo:3",
				"source_type":  "artifact",
				"source_id":    "repo-3",
				"content_text": "third repo-specific candidate that should be dropped by class cap",
				"score":        0.79,
				"memory_class": "repo_specific",
			},
		},
	}

	precedents := retrievalPrecedentsFromContextPackage(input)
	if len(precedents) == 0 {
		t.Fatalf("expected precedents, got %#v", precedents)
	}
	if len(precedents) > 5 {
		t.Fatalf("expected at most 5 precedents, got %#v", precedents)
	}
	perClass := map[string]int{}
	for _, item := range precedents {
		className := strings.TrimSpace(asString(item["memory_class"]))
		perClass[className]++
		if perClass[className] > 2 {
			t.Fatalf("expected class cap of 2, got %#v", precedents)
		}
		if len(asString(item["summary"])) > 220 {
			t.Fatalf("expected compact summary, got %q", asString(item["summary"]))
		}
		if strings.TrimSpace(asString(item["selection_reason"])) == "" {
			t.Fatalf("expected selection reason, got %#v", item)
		}
	}
	if perClass["repo_specific"] == 0 || perClass["pattern_similar"] == 0 {
		t.Fatalf("expected diverse memory classes, got %#v", precedents)
	}
}

func TestInterpretPlannerRepairFindingsKeepsRejectedExclusionsConfirmed(t *testing.T) {
	correction := interpretPlannerRepairFindings(
		[]map[string]any{
			{"kind": "planner_scope_confirmed"},
			{"kind": "planner_rejected_scope_confirmed"},
		},
		[]string{"internal/orchestratorgo/bridge/executor.go"},
		[]string{"internal/orchestratorgo/httpapi/legacy_objectives.go"},
		[]string{"internal/orchestratorgo/bridge/executor.go"},
	)

	if correction.PlannerScopeResolution != "confirmed" {
		t.Fatalf("expected confirmed planner resolution, got %#v", correction)
	}
	if correction.RejectedPlannerScopeResolution != "confirmed" {
		t.Fatalf("expected confirmed rejected-scope resolution, got %#v", correction)
	}
	if len(correction.ConfirmedPlannerPaths) != 1 || correction.ConfirmedPlannerPaths[0] != "internal/orchestratorgo/bridge/executor.go" {
		t.Fatalf("expected confirmed planner path, got %#v", correction.ConfirmedPlannerPaths)
	}
	if len(correction.ExcludedScopePaths) != 1 || correction.ExcludedScopePaths[0] != "internal/orchestratorgo/httpapi/legacy_objectives.go" {
		t.Fatalf("expected rejected path to remain excluded, got %#v", correction.ExcludedScopePaths)
	}
	if !strings.Contains(correction.ScopeCorrectionReason, "keep planner-rejected competitor paths excluded") {
		t.Fatalf("expected exclusion reason, got %q", correction.ScopeCorrectionReason)
	}
}

func TestBuildRepairEditBriefExplicitlyCallsOutPlannerResolution(t *testing.T) {
	brief := buildRepairEditBrief(
		"Repair the objective flow",
		"Keep the patch narrow.",
		[]map[string]any{
			{"kind": "planner_scope_contradicted", "severity": "medium", "message": "Planner shortlist missed the changed file."},
			{"kind": "planner_rejected_scope_confirmed", "severity": "info", "message": "Legacy file stayed untouched."},
		},
		map[string]any{"exit_code": 1},
		[]string{"internal/orchestratorgo/httpapi/objectives.go"},
		[]map[string]any{
			{
				"path":           "internal/orchestratorgo/httpapi/objectives.go",
				"failure_mode":   "validation_failure",
				"priority":       "high",
				"cause_category": "bad_edit_against_good_shortlist",
				"evidence":       "Validation still fails in the corrected scope.",
			},
		},
		[]map[string]any{
			{
				"path":   "internal/orchestratorgo/httpapi/objectives.go",
				"action": "repair the validation failure in this file first",
			},
		},
		plannerRepairScopeCorrection{
			PlannerScopeResolution:         "contradicted",
			RejectedPlannerScopeResolution: "confirmed",
			ConfirmedPlannerPaths:          []string{"internal/orchestratorgo/httpapi/objectives.go"},
			ExcludedScopePaths:             []string{"internal/orchestratorgo/httpapi/legacy_objectives.go"},
			ScopeCorrectionReason:          "drop the contradicted shortlist and keep the rejected competitor excluded",
		},
		nil,
	)

	if !strings.Contains(brief, "Planner confirmed paths: internal/orchestratorgo/httpapi/objectives.go") {
		t.Fatalf("expected explicit confirmed planner section, got %q", brief)
	}
	if !strings.Contains(brief, "Planner selected paths contradicted by review:") {
		t.Fatalf("expected explicit contradicted planner section, got %q", brief)
	}
	if !strings.Contains(brief, "Planner rejected paths still excluded: internal/orchestratorgo/httpapi/legacy_objectives.go") {
		t.Fatalf("expected explicit rejected exclusion section, got %q", brief)
	}
	if !strings.Contains(brief, "Excluded paths for next attempt: internal/orchestratorgo/httpapi/legacy_objectives.go") {
		t.Fatalf("expected excluded scope paths in brief, got %q", brief)
	}
}

func TestWorkspaceExecutorResearchProjectInfersScopeFromReadmeEvidence(t *testing.T) {
	root := initGitWorkspace(t)
	projectDir := filepath.Join(root, "projects", "readme-scope-demo")
	if err := os.MkdirAll(filepath.Join(projectDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# readme-scope-demo\n\nThe objective toggle is implemented in src/switchboard.py.\n"
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
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
					"project_name":     "readme-scope-demo",
					"project_root":     "projects/readme-scope-demo",
					"project_type":     "existing_repo",
					"runtime_or_stack": "python",
					"goal":             "Enable the objective toggle cleanly for the autonomous flow.",
					"test_focus":       "initial objective scope inference",
				},
				"expected_files": []any{"README.md", "PASS"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Stdout == nil {
		t.Fatalf("unexpected research result: %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*result.Stdout), &payload); err != nil {
		t.Fatalf("expected valid research payload json: %v", err)
	}
	scope, ok := payload["recommended_scope_paths"].([]any)
	if !ok || len(scope) == 0 {
		t.Fatalf("expected recommended_scope_paths, got %#v", payload["recommended_scope_paths"])
	}
	if asString(scope[0]) != "src/switchboard.py" {
		t.Fatalf("expected README-derived primary scope, got %#v", scope)
	}
	if brief := asString(payload["next_edit_brief"]); !strings.Contains(brief, "src/switchboard.py") {
		t.Fatalf("expected next_edit_brief to mention README-derived file, got %q", brief)
	}
}

func TestWorkspaceExecutorResearchProjectReadmeDisambiguatesCompetingFiles(t *testing.T) {
	root := initGitWorkspace(t)
	projectDir := filepath.Join(root, "projects", "readme-ambiguity-demo")
	if err := os.MkdirAll(filepath.Join(projectDir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# readme-ambiguity-demo\n\nThe current objective toggle lives in switchboard.py.\nfeature_toggle.py is legacy and should not be the first target.\n"
	if err := os.WriteFile(filepath.Join(projectDir, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "src", "feature_toggle.py"), []byte("LEGACY = True\n"), 0o644); err != nil {
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
					"project_name":     "readme-ambiguity-demo",
					"project_root":     "projects/readme-ambiguity-demo",
					"project_type":     "existing_repo",
					"runtime_or_stack": "python",
					"goal":             "Enable the objective toggle cleanly for the autonomous flow.",
					"test_focus":       "ambiguous initial objective scope inference",
				},
				"expected_files": []any{"README.md", "PASS"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Stdout == nil {
		t.Fatalf("unexpected research result: %#v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*result.Stdout), &payload); err != nil {
		t.Fatalf("expected valid research payload json: %v", err)
	}
	scope, ok := payload["recommended_scope_paths"].([]any)
	if !ok || len(scope) < 2 {
		t.Fatalf("expected ranked recommended_scope_paths, got %#v", payload["recommended_scope_paths"])
	}
	if asString(scope[0]) != "src/switchboard.py" {
		t.Fatalf("expected README to rank current file first, got %#v", scope)
	}
	if asString(scope[1]) == "src/switchboard.py" {
		t.Fatalf("expected competing file after primary scope, got %#v", scope)
	}
}

func TestWorkspaceExecutorRunTestsProducesStructuredFindings(t *testing.T) {
	root := t.TempDir()
	executor, err := NewWorkspaceExecutor(root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(context.Background(), domain.LocalBridgeTaskClaimResponse{
		Metadata: map[string]any{
			"tool_request": map[string]any{
				"tool": "run_tests",
				"argv": []string{"python3", "-c", "import sys; sys.exit(1)"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error result, got %#v", result)
	}
	if len(result.Findings) != 1 {
		t.Fatalf("expected one structured finding, got %#v", result.Findings)
	}
	if asString(result.Findings[0]["kind"]) != "validation_failure" {
		t.Fatalf("expected validation_failure finding, got %#v", result.Findings[0])
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
