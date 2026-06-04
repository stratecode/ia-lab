package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/codeanalysis"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

var allowedCommands = []string{
	"pytest",
	"python",
	"python3",
	"uv",
	"npm",
	"pnpm",
	"yarn",
	"make",
	"composer",
	"php",
	"git",
	"aider-task",
}

type LocalExecutionError struct {
	Message string
}

func (e LocalExecutionError) Error() string {
	return e.Message
}

type WorkspaceExecutor struct {
	workspaceRoot string
}

var toolCapabilityMap = map[string]string{
	"read_file":        "filesystem.read",
	"filesystem.read":  "filesystem.read",
	"list_files":       "filesystem.list",
	"filesystem.list":  "filesystem.list",
	"write_file":       "filesystem.write",
	"filesystem.write": "filesystem.write",
	"code_analysis":    "code.analysis",
}

func NewWorkspaceExecutor(workspaceRoot string) (*WorkspaceExecutor, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return nil, err
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace root is not a directory: %s", root)
	}
	return &WorkspaceExecutor{workspaceRoot: root}, nil
}

func (e *WorkspaceExecutor) Execute(ctx context.Context, claim domain.LocalBridgeTaskClaimResponse) (domain.LocalBridgeResultRequest, error) {
	metadata := claim.Metadata
	contextSources, contextHits, contextChunkCount := semanticContextRefs(metadata)
	toolRequest, ok := metadata["tool_request"].(map[string]any)
	if !ok || toolRequest == nil {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "local task requires metadata.tool_request"}
	}
	tool := strings.TrimSpace(asString(toolRequest["tool"]))
	if tool == "" {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "tool_request.tool is required"}
	}
	toolRequest["_task_metadata"] = metadata
	if err := ensureToolCapabilityAllowed(metadata, tool); err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	approvalRequired := asBool(metadata["requires_approval"]) || asBool(toolRequest["requires_approval"])
	approvalGranted := asBool(metadata["approval_granted"])
	if approvalRequired && !approvalGranted {
		actionType := "local_bridge_tool"
		targetResource := firstNonEmptyString(strings.TrimSpace(asString(toolRequest["path"])), tool)
		timeout := asInt(toolRequest["timeout_seconds"], 300)
		summary := fmt.Sprintf("Approval required before executing tool %s", tool)
		return withSemanticRefs(domain.LocalBridgeResultRequest{
			Status:         "waiting_approval",
			Summary:        &summary,
			ActionType:     &actionType,
			TargetResource: &targetResource,
			TimeoutSeconds: &timeout,
		}, contextSources, contextHits, contextChunkCount), nil
	}

	var result domain.LocalBridgeResultRequest
	var err error
	switch tool {
	case "read_file", "filesystem.read":
		result, err = e.readFile(toolRequest)
	case "list_files", "filesystem.list":
		result, err = e.listFiles(toolRequest)
	case "write_file", "filesystem.write":
		result, err = e.writeFile(toolRequest)
	case "research_project":
		result, err = e.researchProject(toolRequest)
	case "scaffold_project":
		result, err = e.scaffoldProject(toolRequest)
	case "review_project":
		result, err = e.reviewProject(ctx, toolRequest)
	case "review_workspace":
		result, err = e.reviewWorkspace(ctx, toolRequest)
	case "apply_patch":
		result, err = e.applyPatch(ctx, toolRequest)
	case "run_command":
		result, err = e.runCommand(ctx, toolRequest)
	case "git_status":
		result, err = e.runSubprocess(ctx, []string{"git", "status", "--short"}, "git status")
	case "git_diff":
		result, err = e.runSubprocess(ctx, []string{"git", "diff", "--no-ext-diff"}, "git diff")
	case "run_tests":
		argv, ok := anyStringSlice(toolRequest["argv"])
		if !ok || len(argv) == 0 {
			argv = []string{"pytest", "-q"}
		}
		result, err = e.runAllowedCommand(ctx, argv, "Ran tests")
		if err == nil {
			result.TestResults = map[string]any{
				"argv":      argv,
				"exit_code": derefIntPtr(result.ExitCode),
				"status":    strings.ToLower(strings.TrimSpace(result.Status)),
				"stdout":    derefStringPtr(result.Stdout),
				"stderr":    derefStringPtr(result.Stderr),
			}
		}
	case "code_analysis":
		result, err = e.codeAnalysis(toolRequest)
	default:
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: fmt.Sprintf("unsupported local tool: %s", tool)}
	}
	if err != nil {
		return result, err
	}
	return withSemanticRefs(result, contextSources, contextHits, contextChunkCount), nil
}

func (e *WorkspaceExecutor) readFile(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	path, rel, err := e.resolve(request["path"])
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	summary := fmt.Sprintf("Read %s", rel)
	stdout := string(raw)
	return domain.LocalBridgeResultRequest{
		Status:       "success",
		Summary:      &summary,
		Stdout:       &stdout,
		ChangedFiles: []string{},
	}, nil
}

func (e *WorkspaceExecutor) listFiles(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	path, _, err := e.resolveWithDefault(request["path"], ".")
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	pattern := firstNonEmptyString(strings.TrimSpace(asString(request["pattern"])), "*")
	items := make([]string, 0)
	err = filepath.Walk(path, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		matched, matchErr := filepath.Match(pattern, filepath.Base(current))
		if matchErr != nil {
			return matchErr
		}
		if matched {
			rel, err := filepath.Rel(e.workspaceRoot, current)
			if err != nil {
				return err
			}
			items = append(items, rel)
		}
		return nil
	})
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	slices.Sort(items)
	summary := fmt.Sprintf("Listed %d files", len(items))
	stdout := strings.Join(items, "\n")
	return domain.LocalBridgeResultRequest{
		Status:       "success",
		Summary:      &summary,
		Stdout:       &stdout,
		ChangedFiles: []string{},
	}, nil
}

func (e *WorkspaceExecutor) writeFile(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	path, rel, err := e.resolve(request["path"])
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	content := asString(request["content"])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	summary := fmt.Sprintf("Wrote %s", rel)
	return e.withGitArtifacts(domain.LocalBridgeResultRequest{Status: "success", Summary: &summary, ChangedFiles: []string{rel}})
}

func (e *WorkspaceExecutor) researchProject(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	projectRequest, _ := request["project_request"].(map[string]any)
	projectType := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["project_type"])), strings.TrimSpace(asString(request["project_type"])))
	stack := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["runtime_or_stack"])), strings.TrimSpace(asString(request["runtime_or_stack"])))
	goal := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["goal"])), strings.TrimSpace(asString(request["goal"])))
	testFocus := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["test_focus"])), strings.TrimSpace(asString(request["test_focus"])))
	testCommand := anyStringSliceDefault(request["test_command"], defaultBridgeTestCommand(projectType, stack))
	inferredProjectRoot := strings.TrimSpace(asString(projectRequest["project_root"]))
	if inferredProjectRoot == "" {
		parentDirectory := strings.TrimSpace(asString(projectRequest["parent_directory"]))
		projectName := strings.TrimSpace(asString(projectRequest["project_name"]))
		if parentDirectory != "" && projectName != "" {
			inferredProjectRoot = filepath.ToSlash(filepath.Join(parentDirectory, projectName))
		}
	}
	projectRootValue := firstNonEmptyString(inferredProjectRoot, strings.TrimSpace(asString(request["project_root"])), ".")
	evidence, capabilityUsage := e.collectProjectReadContext(projectRootValue)
	recommendations := []string{
		"Keep the project tiny and deterministic.",
		"Expose one obvious entrypoint and one obvious validation path.",
		"Prefer a test command with no external network dependency.",
	}
	priorFindings := anyMapSliceDefault(request["prior_findings"], []map[string]any{})
	priorTestResults := nonNilMap(request["prior_test_results"])
	priorChangedFiles := anyStringSliceDefault(request["prior_changed_files"], []string{})
	repairFeedback := firstNonEmptyString(strings.TrimSpace(asString(request["repair_feedback"])), strings.TrimSpace(asString(projectRequest["repair_feedback"])))
	repairMode := len(priorFindings) > 0 || len(priorTestResults) > 0 || len(priorChangedFiles) > 0 || repairFeedback != ""
	if evidence["manifest_file"] != nil {
		recommendations = append(recommendations, "Use the repository manifest as the primary contract before inventing extra assumptions.")
	}
	if evidence["readme_excerpt"] != nil {
		recommendations = append(recommendations, "Prefer README-aligned constraints over generic repo folklore.")
	}
	if repairMode {
		recommendations = append(recommendations,
			"Treat the previous validation and review findings as the primary contract for the next edit.",
			"Fix the highest-severity failure first, then rerun the declared validation command before changing scope again.",
			"Keep the next patch narrow and focused on the files implicated by the failing iteration.",
		)
		if exitCode, ok := asIntOK(priorTestResults["exit_code"]); ok && exitCode != 0 {
			recommendations = append(recommendations, "The previous validation command returned a non-zero exit code. Reproduce that failure before broadening the change.")
		}
	}
	recommendedScopePaths := make([]string, 0, len(priorChangedFiles))
	recommendedScopePaths = append(recommendedScopePaths, priorChangedFiles...)
	if len(recommendedScopePaths) == 0 {
		recommendedScopePaths = append(recommendedScopePaths, anyStringSliceDefault(projectRequest["expected_files"], []string{})...)
	}
	nextEditBrief := buildRepairEditBrief(goal, repairFeedback, priorFindings, priorTestResults, recommendedScopePaths)
	payload := fmt.Sprintf(`{
  "project_type": %q,
  "runtime_or_stack": %q,
  "goal": %q,
  "test_focus": %q,
  "repair_mode": %t,
  "recommendations": %s,
  "recommended_scope_paths": %s,
  "next_edit_brief": %q,
  "checklist": [
    "README present",
    "At least one test file present",
    "Declared test command is runnable"
  ],
  "test_command": %q,
  "failure_signals": %s,
  "prior_findings": %s,
  "local_evidence": %s
}`, projectType, stack, goal, testFocus, repairMode, mustJSON(recommendations), mustJSON(recommendedScopePaths), nextEditBrief, strings.Join(testCommand, " "), mustJSON(map[string]any{
		"repair_feedback":     repairFeedback,
		"prior_test_results":  priorTestResults,
		"prior_changed_files": priorChangedFiles,
	}), mustJSON(priorFindings), mustJSON(evidence))
	summary := fmt.Sprintf("Researched scaffold constraints for %s/%s", projectType, stack)
	if repairMode {
		summary = fmt.Sprintf("Built repair plan for %s/%s", projectType, stack)
	}
	artifacts := []map[string]any{
		{
			"type":         "research_context",
			"title":        "Project research context",
			"media_type":   "application/json",
			"content_text": payload,
		},
		{
			"type":         "research_evidence",
			"title":        "Local repository evidence",
			"media_type":   "application/json",
			"content_text": mustJSON(evidence),
		},
	}
	if repairMode {
		artifacts = append(artifacts, map[string]any{
			"type":         "repair_plan",
			"title":        "Objective repair plan",
			"media_type":   "application/json",
			"content_text": payload,
		})
	}
	return domain.LocalBridgeResultRequest{
		Status:           "success",
		Summary:          &summary,
		Stdout:           &payload,
		CapabilityUsage:  capabilityUsage,
		CapabilityHelped: uniqueCapabilities(capabilityUsage, true),
		Artifacts:        artifacts,
	}, nil
}

func (e *WorkspaceExecutor) reviewProject(ctx context.Context, request map[string]any) (domain.LocalBridgeResultRequest, error) {
	projectRootValue := strings.TrimSpace(asString(request["project_root"]))
	if projectRootValue == "" {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "project_root is required"}
	}
	projectRoot, rel, err := e.resolve(projectRootValue)
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "project_root does not exist"}
	}
	expectedFiles := anyStringSliceDefault(request["expected_files"], []string{"README.md"})
	missing := make([]string, 0)
	for _, file := range expectedFiles {
		target, _, err := e.resolve(filepath.ToSlash(filepath.Join(projectRootValue, file)))
		if err != nil {
			return domain.LocalBridgeResultRequest{}, err
		}
		if _, err := os.Stat(target); err != nil {
			missing = append(missing, file)
		}
	}
	testCommand := anyStringSliceDefault(request["test_command"], []string{"pytest", "-q"})
	stdout := ""
	stderr := ""
	exitCode := 0
	if len(testCommand) > 0 {
		cmd := exec.CommandContext(ctx, testCommand[0], testCommand[1:]...)
		cmd.Dir = projectRoot
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		stdout = outBuf.String()
		stderr = errBuf.String()
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		if runErr != nil {
			message := firstNonEmptyString(strings.TrimSpace(stderr), strings.TrimSpace(stdout), runErr.Error())
			summary := fmt.Sprintf("Review failed for %s", rel)
			return domain.LocalBridgeResultRequest{
				Status:       "error",
				Summary:      &summary,
				Stdout:       stringPtr(stdout),
				Stderr:       stringPtr(stderr),
				ExitCode:     intPtr(exitCode),
				ErrorMessage: &message,
				Artifacts: []map[string]any{
					{"type": "review_report", "title": "Project review failure", "media_type": "text/plain", "content_text": message},
				},
			}, nil
		}
	}
	if len(missing) > 0 {
		message := "missing expected files: " + strings.Join(missing, ", ")
		summary := fmt.Sprintf("Review failed for %s", rel)
		return domain.LocalBridgeResultRequest{
			Status:       "error",
			Summary:      &summary,
			Stdout:       stringPtr(stdout),
			Stderr:       stringPtr(stderr),
			ExitCode:     intPtr(exitCode),
			ErrorMessage: &message,
			Artifacts: []map[string]any{
				{"type": "review_report", "title": "Project review failure", "media_type": "text/plain", "content_text": message},
			},
		}, nil
	}
	summary := fmt.Sprintf("Review passed for %s", rel)
	analysisTypes := anyStringSliceDefault(request["analysis_types"], nil)
	evidence, capabilityUsage := e.collectProjectReadContext(projectRootValue)
	report := fmt.Sprintf("expected_files=%s\ntest_command=%s\n", strings.Join(expectedFiles, ","), strings.Join(testCommand, " "))
	artifacts := []map[string]any{
		{"type": "review_report", "title": "Project review report", "media_type": "text/plain", "content_text": report},
	}
	if len(evidence) > 0 {
		report += fmt.Sprintf("manifest_file=%s\n", firstNonEmptyString(asString(evidence["manifest_file"]), ""))
		artifacts = append(artifacts, map[string]any{
			"type":         "review_evidence",
			"title":        "Repository evidence snapshot",
			"media_type":   "application/json",
			"content_text": mustJSON(evidence),
		})
	}
	if len(analysisTypes) > 0 {
		analysis, analysisErr := codeanalysis.Analyze(projectRoot, analysisTypes)
		if analysisErr != nil {
			report += fmt.Sprintf("analysis_error=%s\n", analysisErr.Error())
			artifacts = append(artifacts, map[string]any{
				"type":         "code_analysis_report",
				"title":        "Code analysis failure",
				"media_type":   "text/plain",
				"content_text": analysisErr.Error(),
			})
		} else {
			report += fmt.Sprintf("analysis_types=%s\nanalysis_findings=%d\n", strings.Join(analysisTypes, ","), analysis.Summary.TotalFindings)
			capabilityUsage = append(capabilityUsage, map[string]any{
				"capability":    "code.analysis",
				"helped":        true,
				"source":        rel,
				"finding_count": analysis.Summary.TotalFindings,
				"kind":          "static_analysis",
			})
			artifacts = append(artifacts, map[string]any{
				"type":         "code_analysis_report",
				"title":        "Code analysis report",
				"media_type":   "application/json",
				"content_text": mustJSON(analysis),
				"metadata": map[string]any{
					"analysis_types": analysisTypes,
					"summary":        analysis.Summary,
				},
			})
		}
		artifacts[0]["content_text"] = report
	}
	return domain.LocalBridgeResultRequest{
		Status:           "success",
		Summary:          &summary,
		Stdout:           stringPtr(stdout),
		Stderr:           stringPtr(stderr),
		ExitCode:         intPtr(exitCode),
		Artifacts:        artifacts,
		CapabilityUsage:  capabilityUsage,
		CapabilityHelped: uniqueCapabilities(capabilityUsage, true),
	}, nil
}

func (e *WorkspaceExecutor) reviewWorkspace(ctx context.Context, request map[string]any) (domain.LocalBridgeResultRequest, error) {
	projectRootValue := firstNonEmptyString(strings.TrimSpace(asString(request["project_root"])), ".")
	projectRoot, rel, err := e.resolveWithDefault(projectRootValue, ".")
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "project_root does not exist"}
	}

	testCommand := anyStringSliceDefault(request["test_command"], []string{"python3", "-c", "print('ok')"})
	stdout := ""
	stderr := ""
	exitCode := 0
	if len(testCommand) > 0 {
		cmd := exec.CommandContext(ctx, testCommand[0], testCommand[1:]...)
		cmd.Dir = projectRoot
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		stdout = outBuf.String()
		stderr = errBuf.String()
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		if runErr != nil {
			decision := "changes_requested"
			message := firstNonEmptyString(strings.TrimSpace(stderr), strings.TrimSpace(stdout), runErr.Error())
			summary := fmt.Sprintf("Review requested changes for %s", rel)
			return domain.LocalBridgeResultRequest{
				Status:         "error",
				Summary:        &summary,
				Stdout:         stringPtr(stdout),
				Stderr:         stringPtr(stderr),
				ExitCode:       intPtr(exitCode),
				ErrorMessage:   &message,
				ReviewDecision: &decision,
				ReviewComments: []map[string]any{{"severity": "high", "message": message}},
			}, nil
		}
	}

	diff, changedFiles := e.workspaceGitState()
	if strings.TrimSpace(diff) == "" || len(changedFiles) == 0 {
		decision := "changes_requested"
		message := "review requires an actual repository diff before approval"
		summary := fmt.Sprintf("Review requested changes for %s", rel)
		return domain.LocalBridgeResultRequest{
			Status:         "error",
			Summary:        &summary,
			Stdout:         stringPtr(stdout),
			Stderr:         stringPtr(stderr),
			ExitCode:       intPtr(exitCode),
			ErrorMessage:   &message,
			ReviewDecision: &decision,
			ReviewComments: []map[string]any{{"severity": "medium", "message": message}},
			Diff:           &diff,
			ChangedFiles:   changedFiles,
			TestResults: map[string]any{
				"argv":      testCommand,
				"exit_code": exitCode,
			},
		}, nil
	}

	decision := "approved"
	summary := fmt.Sprintf("Review approved for %s", rel)
	report := mustJSON(map[string]any{
		"decision":           decision,
		"changed_files":      changedFiles,
		"validation_command": testCommand,
		"objective":          request["execution_contract"],
	})
	return domain.LocalBridgeResultRequest{
		Status:         "success",
		Summary:        &summary,
		Stdout:         stringPtr(stdout),
		Stderr:         stringPtr(stderr),
		ExitCode:       intPtr(exitCode),
		Diff:           &diff,
		ChangedFiles:   changedFiles,
		ReviewDecision: &decision,
		Artifacts: []map[string]any{
			{
				"type":         "review_packet",
				"title":        "Objective review packet",
				"media_type":   "application/json",
				"content_text": report,
			},
		},
		TestResults: map[string]any{
			"argv":      testCommand,
			"exit_code": exitCode,
		},
	}, nil
}

func (e *WorkspaceExecutor) codeAnalysis(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	rootValue := firstNonEmptyString(strings.TrimSpace(asString(request["project_root"])), strings.TrimSpace(asString(request["path"])), ".")
	projectRoot, rel, err := e.resolve(rootValue)
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	analysisTypes := anyStringSliceDefault(request["analysis_types"], []string{strings.TrimSpace(asString(request["analysis_type"]))})
	result, err := codeanalysis.Analyze(projectRoot, analysisTypes)
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	summary := fmt.Sprintf("Analyzed %s with %d finding(s)", rel, result.Summary.TotalFindings)
	stdout := mustJSON(result)
	return domain.LocalBridgeResultRequest{
		Status:           "success",
		Summary:          &summary,
		Stdout:           &stdout,
		CapabilityUsage:  []map[string]any{{"capability": "code.analysis", "helped": true, "source": rel, "kind": "static_analysis", "finding_count": result.Summary.TotalFindings}},
		CapabilityHelped: []string{"code.analysis"},
		Artifacts: []map[string]any{
			{
				"type":         "code_analysis_report",
				"title":        "Code analysis report",
				"media_type":   "application/json",
				"content_text": stdout,
				"metadata": map[string]any{
					"analysis_types": analysisTypes,
				},
			},
		},
	}, nil
}

func (e *WorkspaceExecutor) collectProjectReadContext(projectRootValue string) (map[string]any, []map[string]any) {
	projectRoot, _, err := e.resolveWithDefault(projectRootValue, ".")
	if err != nil {
		return map[string]any{}, nil
	}
	usage := make([]map[string]any, 0)
	evidence := map[string]any{}
	readCandidate := func(relPath string, key string, maxChars int) {
		target := filepath.Join(projectRoot, relPath)
		raw, err := os.ReadFile(target)
		if err != nil {
			return
		}
		text := string(raw)
		if len(text) > maxChars {
			text = text[:maxChars]
		}
		evidence[key] = text
		usage = append(usage, map[string]any{
			"capability": "filesystem.read",
			"helped":     true,
			"source":     filepath.ToSlash(filepath.Join(strings.TrimSpace(projectRootValue), relPath)),
			"kind":       "local_repo_context",
		})
	}
	readCandidate("README.md", "readme_excerpt", 600)
	for _, name := range []string{"package.json", "composer.json", "pyproject.toml", "go.mod"} {
		target := filepath.Join(projectRoot, name)
		if _, err := os.Stat(target); err == nil {
			evidence["manifest_file"] = name
			readCandidate(name, "manifest_excerpt", 600)
			break
		}
	}
	return evidence, usage
}

func uniqueCapabilities(items []map[string]any, helpedOnly bool) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, item := range items {
		if helpedOnly && !asBool(item["helped"]) {
			continue
		}
		name := strings.TrimSpace(asString(item["capability"]))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func (e *WorkspaceExecutor) applyPatch(ctx context.Context, request map[string]any) (domain.LocalBridgeResultRequest, error) {
	patch := asString(request["patch"])
	if strings.TrimSpace(patch) == "" {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "patch is required"}
	}
	cmd := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", "-")
	cmd.Dir = e.workspaceRoot
	cmd.Stdin = strings.NewReader(patch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(firstNonEmptyString(stderr.String(), stdout.String(), err.Error()))
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: msg}
	}
	summary := "Applied patch"
	result := domain.LocalBridgeResultRequest{
		Status:   "success",
		Summary:  &summary,
		Stdout:   stringPtr(stdout.String()),
		Stderr:   stringPtr(stderr.String()),
		ExitCode: intPtr(0),
	}
	return e.withGitArtifacts(result)
}

func (e *WorkspaceExecutor) runCommand(ctx context.Context, request map[string]any) (domain.LocalBridgeResultRequest, error) {
	argv, ok := anyStringSlice(request["argv"])
	if !ok || len(argv) == 0 {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "run_command requires argv list"}
	}
	argv = e.prepareCommandArgs(argv, request)
	return e.runAllowedCommand(ctx, argv, "Ran command: "+strings.Join(argv, " "))
}

func (e *WorkspaceExecutor) prepareCommandArgs(argv []string, request map[string]any) []string {
	if len(argv) == 0 {
		return argv
	}
	if argv[0] != "aider-task" {
		return argv
	}
	metadata, _ := request["_task_metadata"].(map[string]any)
	if len(metadata) == 0 {
		return argv
	}
	enriched := append([]string{}, argv...)
	if idx := flagValueIndex(enriched, "--description"); idx >= 0 && idx+1 < len(enriched) {
		enriched[idx+1] = enrichAiderDescription(enriched[idx+1], metadata)
	}
	if flagValueIndex(enriched, "--test-command") < 0 {
		if commands := anyStringSliceDefault(metadata["validation_commands"], []string{}); len(commands) > 0 {
			enriched = append(enriched, "--test-command", strings.Join(commands, " "))
		}
	}
	if flagValueIndex(enriched, "--scope-paths") < 0 {
		if scopePaths := anyStringSliceDefault(metadata["suspected_paths"], []string{}); len(scopePaths) > 0 {
			enriched = append(enriched, "--scope-paths", strings.Join(scopePaths, ","))
		}
	}
	if flagValueIndex(enriched, "--metadata") < 0 {
		enriched = append(enriched, "--metadata", mustJSON(buildAiderTaskMetadata(metadata)))
	}
	return enriched
}

func (e *WorkspaceExecutor) runAllowedCommand(ctx context.Context, argv []string, summary string) (domain.LocalBridgeResultRequest, error) {
	if len(argv) == 0 {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "argv is required"}
	}
	if !slices.Contains(allowedCommands, argv[0]) {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: fmt.Sprintf("command not allowed: %s", argv[0])}
	}
	return e.runSubprocess(ctx, argv, summary)
}

func (e *WorkspaceExecutor) runSubprocess(ctx context.Context, argv []string, summary string) (domain.LocalBridgeResultRequest, error) {
	if len(argv) == 0 {
		return domain.LocalBridgeResultRequest{}, errors.New("argv is required")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = e.workspaceRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	result := domain.LocalBridgeResultRequest{
		Status:   "success",
		Summary:  &summary,
		Stdout:   stringPtr(stdout.String()),
		Stderr:   stringPtr(stderr.String()),
		ExitCode: intPtr(exitCode),
	}
	if err != nil {
		result.Status = "error"
		msg := firstNonEmptyString(strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String()), err.Error())
		result.ErrorMessage = &msg
		return result, nil
	}
	return e.withGitArtifacts(result)
}

func (e *WorkspaceExecutor) withGitArtifacts(result domain.LocalBridgeResultRequest) (domain.LocalBridgeResultRequest, error) {
	gitRoot, ok := e.gitTopLevel()
	if !ok || gitRoot != e.workspaceRoot {
		return result, nil
	}
	diff, changedFiles := e.workspaceGitState()
	result.ChangedFiles = changedFiles
	result.Diff = &diff
	return result, nil
}

func (e *WorkspaceExecutor) workspaceGitState() (string, []string) {
	statusCmd := exec.Command("git", "status", "--short", "--untracked-files=all")
	statusCmd.Dir = e.workspaceRoot
	statusOut, _ := statusCmd.Output()
	diffCmd := exec.Command("git", "diff", "--no-ext-diff")
	diffCmd.Dir = e.workspaceRoot
	diffOut, _ := diffCmd.Output()

	changedFiles := make([]string, 0)
	for _, rawLine := range strings.Split(string(statusOut), "\n") {
		line := strings.TrimRight(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 3 {
			changedFiles = append(changedFiles, strings.TrimSpace(line[3:]))
		} else {
			changedFiles = append(changedFiles, strings.TrimSpace(line))
		}
	}
	diff := string(diffOut)
	return diff, changedFiles
}

func (e *WorkspaceExecutor) gitTopLevel() (string, bool) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = e.workspaceRoot
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := filepath.Clean(strings.TrimSpace(string(output)))
	if root == "" {
		return "", false
	}
	return root, true
}

func (e *WorkspaceExecutor) resolve(raw any) (string, string, error) {
	return e.resolveWithDefault(raw, "")
}

func (e *WorkspaceExecutor) resolveWithDefault(raw any, fallback string) (string, string, error) {
	path := strings.TrimSpace(asString(raw))
	if path == "" {
		path = fallback
	}
	if path == "" {
		return "", "", LocalExecutionError{Message: "path is required"}
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(e.workspaceRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(e.workspaceRoot, candidate)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", LocalExecutionError{Message: fmt.Sprintf("path escapes workspace: %s", path)}
	}
	return candidate, rel, nil
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func anyStringSlice(value any) ([]string, bool) {
	switch v := value.(type) {
	case []string:
		return v, true
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(asString(item))
			if text == "" {
				return nil, false
			}
			out = append(out, text)
		}
		return out, true
	default:
		return nil, false
	}
}

func anyStringSliceDefault(value any, fallback []string) []string {
	if items, ok := anyStringSlice(value); ok && len(items) > 0 {
		return items
	}
	return fallback
}

func asBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	default:
		return false
	}
}

func asInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return fallback
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringPtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func intPtr(value int) *int {
	return &value
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefIntPtr(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func buildRepairEditBrief(goal, repairFeedback string, priorFindings []map[string]any, priorTestResults map[string]any, scopePaths []string) string {
	lines := []string{strings.TrimSpace(goal)}
	if strings.TrimSpace(repairFeedback) != "" {
		lines = append(lines, "Repair feedback:", strings.TrimSpace(repairFeedback))
	}
	if len(priorFindings) > 0 {
		lines = append(lines, "Prior findings:")
		for _, finding := range priorFindings {
			severity := strings.TrimSpace(asString(finding["severity"]))
			message := strings.TrimSpace(asString(finding["message"]))
			if message == "" {
				continue
			}
			if severity != "" {
				lines = append(lines, fmt.Sprintf("- [%s] %s", severity, message))
			} else {
				lines = append(lines, "- "+message)
			}
		}
	}
	if exitCode, ok := asIntOK(priorTestResults["exit_code"]); ok {
		lines = append(lines, fmt.Sprintf("Previous validation exit code: %d", exitCode))
	}
	if len(scopePaths) > 0 {
		lines = append(lines, "Likely scope: "+strings.Join(scopePaths, ", "))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildAiderTaskMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{
		"objective_title":          strings.TrimSpace(asString(metadata["objective_title"])),
		"initiative_goal":          strings.TrimSpace(asString(metadata["initiative_goal"])),
		"work_item_kind":           strings.TrimSpace(asString(metadata["work_item_kind"])),
		"objective_iteration":      asInt(metadata["objective_iteration"], 1),
		"objective_max_iterations": asInt(metadata["objective_max_iterations"], 3),
		"repair_feedback":          strings.TrimSpace(asString(metadata["objective_repair_feedback"])),
		"review_decision":          strings.TrimSpace(asString(metadata["review_decision"])),
		"review_comments":          anyMapSliceDefault(metadata["review_comments"], []map[string]any{}),
		"validation_commands":      anyStringSliceDefault(metadata["validation_commands"], []string{}),
		"suspected_paths":          anyStringSliceDefault(metadata["suspected_paths"], []string{}),
	}
	if contextPackage, ok := metadata["context_package"].(map[string]any); ok && len(contextPackage) > 0 {
		out["context_prompt_section"] = strings.TrimSpace(asString(contextPackage["prompt_section"]))
		out["context_source_refs"] = contextPackageSourceRefs(contextPackage["source_refs"])
	}
	return out
}

func enrichAiderDescription(description string, metadata map[string]any) string {
	lines := []string{strings.TrimSpace(description)}
	if feedback := strings.TrimSpace(asString(metadata["objective_repair_feedback"])); feedback != "" {
		lines = append(lines, "", "Repair feedback:", feedback)
	}
	if reviewComments := anyMapSliceDefault(metadata["review_comments"], []map[string]any{}); len(reviewComments) > 0 {
		lines = append(lines, "", "Review findings:")
		for _, item := range reviewComments {
			message := strings.TrimSpace(asString(item["message"]))
			if message == "" {
				continue
			}
			severity := strings.TrimSpace(asString(item["severity"]))
			if severity != "" {
				lines = append(lines, fmt.Sprintf("- [%s] %s", severity, message))
			} else {
				lines = append(lines, "- "+message)
			}
		}
	}
	if contextPackage, ok := metadata["context_package"].(map[string]any); ok {
		if promptSection := strings.TrimSpace(asString(contextPackage["prompt_section"])); promptSection != "" {
			lines = append(lines, "", "Retrieved context:", truncateForPrompt(promptSection, 1600))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func truncateForPrompt(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "..."
}

func flagValueIndex(argv []string, flag string) int {
	for idx := 0; idx < len(argv); idx++ {
		if argv[idx] == flag {
			return idx
		}
	}
	return -1
}

func anyMapSliceDefault(value any, fallback []map[string]any) []map[string]any {
	switch v := value.(type) {
	case []map[string]any:
		if len(v) == 0 {
			return fallback
		}
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			entry, _ := item.(map[string]any)
			if len(entry) == 0 {
				continue
			}
			out = append(out, entry)
		}
		if len(out) == 0 {
			return fallback
		}
		return out
	default:
		return fallback
	}
}

func nonNilMap(value any) map[string]any {
	if raw, ok := value.(map[string]any); ok && raw != nil {
		return raw
	}
	return map[string]any{}
}

func asIntOK(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func ensureToolCapabilityAllowed(metadata map[string]any, tool string) error {
	capability := strings.TrimSpace(toolCapabilityMap[tool])
	if capability == "" {
		return nil
	}
	allowed := allowedCapabilities(metadata)
	if len(allowed) == 0 {
		return nil
	}
	if allowed[capability] {
		return nil
	}
	if strings.HasPrefix(capability, "filesystem.") && allowed["filesystem"] {
		return nil
	}
	return LocalExecutionError{Message: fmt.Sprintf("tool %s requires allowed capability %s", tool, capability)}
}

func allowedCapabilities(metadata map[string]any) map[string]bool {
	out := map[string]bool{}
	if metadata == nil {
		return out
	}
	switch raw := metadata["allowed_capabilities"].(type) {
	case []string:
		for _, item := range raw {
			if text := strings.TrimSpace(item); text != "" {
				out[text] = true
			}
		}
	case []any:
		for _, item := range raw {
			if text := strings.TrimSpace(asString(item)); text != "" {
				out[text] = true
			}
		}
	}
	return out
}

func withSemanticRefs(result domain.LocalBridgeResultRequest, sources []string, hits []map[string]any, chunkCount int) domain.LocalBridgeResultRequest {
	if len(sources) > 0 {
		result.SemanticContextSources = append([]string{}, sources...)
	}
	if len(hits) > 0 {
		result.SemanticContextHits = copyMapSlice(hits)
	}
	if chunkCount > 0 {
		result.SemanticContextChunkCount = chunkCount
	}
	return result
}

func semanticContextRefs(metadata map[string]any) ([]string, []map[string]any, int) {
	contextPackage, _ := metadata["context_package"].(map[string]any)
	if len(contextPackage) == 0 {
		return nil, nil, 0
	}
	sources := contextPackageSourceRefs(contextPackage["source_refs"])
	chunks, _ := contextPackage["chunks"].([]any)
	hits := make([]map[string]any, 0, len(chunks))
	for _, rawChunk := range chunks {
		chunk, _ := rawChunk.(map[string]any)
		if len(chunk) == 0 {
			continue
		}
		hit := map[string]any{}
		if sourceRef := strings.TrimSpace(asString(chunk["source_ref"])); sourceRef != "" {
			hit["source_ref"] = sourceRef
		}
		chunkMetadata, _ := chunk["metadata"].(map[string]any)
		if matchType := strings.TrimSpace(asString(chunkMetadata["memory_match_type"])); matchType != "" {
			hit["match_type"] = matchType
		}
		if repoProfile := strings.TrimSpace(asString(chunkMetadata["repo_profile"])); repoProfile != "" {
			hit["repo_profile"] = repoProfile
		}
		if repositoryURL := strings.TrimSpace(firstNonEmptyString(asString(chunkMetadata["repository_url"]), asString(chunkMetadata["repo_url"]))); repositoryURL != "" {
			hit["repository_url"] = repositoryURL
		}
		if benchmarkCaseID := strings.TrimSpace(asString(chunkMetadata["benchmark_case_id"])); benchmarkCaseID != "" {
			hit["benchmark_case_id"] = benchmarkCaseID
		}
		if len(hit) > 0 {
			hits = append(hits, hit)
		}
	}
	return sources, hits, len(chunks)
}

func contextPackageSourceRefs(value any) []string {
	switch refs := value.(type) {
	case []string:
		return append([]string{}, refs...)
	case []any:
		out := make([]string, 0, len(refs))
		for _, ref := range refs {
			if text := strings.TrimSpace(asString(ref)); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func copyMapSlice(items []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		cloned := make(map[string]any, len(item))
		for key, value := range item {
			cloned[key] = value
		}
		out = append(out, cloned)
	}
	return out
}

func defaultBridgeTestCommand(projectType, stack string) []string {
	switch strings.ToLower(strings.TrimSpace(stack)) {
	case "static":
		return []string{"python3", "-c", "from pathlib import Path; html=Path('index.html').read_text(); assert '<html' in html.lower()"}
	case "node":
		return []string{"node", "--check", "app.js"}
	}
	switch strings.ToLower(strings.TrimSpace(projectType)) {
	case "api_http":
		return []string{"python3", "-m", "py_compile", "app.py"}
	case "worker_background":
		return []string{"python3", "-m", "py_compile", "worker.py"}
	case "debug_regression":
		return []string{"python3", "-m", "py_compile", "calculator.py"}
	case "toy_repo":
		return []string{"python3", "-m", "py_compile", "src/service.py"}
	default:
		return []string{"python3", "-m", "py_compile", "main.py"}
	}
}
