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
	toolRequest, ok := metadata["tool_request"].(map[string]any)
	if !ok || toolRequest == nil {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "local task requires metadata.tool_request"}
	}
	toolRequest = mergeToolRequestWithMetadata(toolRequest, metadata)
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
		return domain.LocalBridgeResultRequest{
			Status:         "waiting_approval",
			Summary:        &summary,
			ActionType:     &actionType,
			TargetResource: &targetResource,
			TimeoutSeconds: &timeout,
		}, nil
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
			if strings.ToLower(strings.TrimSpace(result.Status)) != "success" {
				result.Findings = buildValidationFailureFindings(argv, derefIntPtr(result.ExitCode), firstNonEmptyString(strings.TrimSpace(derefStringPtr(result.ErrorMessage)), strings.TrimSpace(derefStringPtr(result.Stderr)), strings.TrimSpace(derefStringPtr(result.Stdout))))
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
	return result, nil
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
	priorPatchIntentPaths := anyHypothesisPaths(request["patch_intent"])
	retrievalPrecedents := retrievalPrecedentsFromContextPackage(request["context_package"])
	repairFeedback := firstNonEmptyString(strings.TrimSpace(asString(request["repair_feedback"])), strings.TrimSpace(asString(projectRequest["repair_feedback"])))
	repairMode := len(priorFindings) > 0 || len(priorTestResults) > 0 || len(priorChangedFiles) > 0 || repairFeedback != ""
	if evidence["manifest_file"] != nil {
		recommendations = append(recommendations, "Use the repository manifest as the primary contract before inventing extra assumptions.")
	}
	if evidence["readme_excerpt"] != nil {
		recommendations = append(recommendations, "Prefer README-aligned constraints over generic repo folklore.")
	}
	if len(retrievalPrecedents) > 0 {
		recommendations = append(recommendations, "Reuse the retrieved precedents as bounded evidence, not as a substitute for the current diff and validation signals.")
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
	selectedPlannerPaths := anyHypothesisPaths(request["initial_scope_hypotheses"])
	rejectedPlannerPaths := anyHypothesisPaths(request["rejected_scope_hypotheses"])
	scopeCorrection := interpretPlannerRepairFindings(priorFindings, selectedPlannerPaths, rejectedPlannerPaths, priorChangedFiles)
	recommendedScopePaths := recommendResearchScopePaths(
		goal,
		scopeCorrection,
		priorChangedFiles,
		priorPatchIntentPaths,
		anyStringSliceDefault(projectRequest["expected_files"], []string{}),
		anyStringSliceDefault(evidence["tree_files"], []string{}),
		strings.TrimSpace(asString(evidence["readme_excerpt"])),
	)
	failureHypotheses := buildRepairFailureHypotheses(priorFindings, priorTestResults, recommendedScopePaths, scopeCorrection)
	patchIntent := buildRepairPatchIntent(failureHypotheses, recommendedScopePaths, scopeCorrection)
	nextEditBrief := buildRepairEditBrief(goal, repairFeedback, priorFindings, priorTestResults, recommendedScopePaths, failureHypotheses, patchIntent, scopeCorrection, retrievalPrecedents)
	payload := fmt.Sprintf(`{
  "project_type": %q,
  "runtime_or_stack": %q,
  "goal": %q,
  "test_focus": %q,
  "repair_mode": %t,
  "recommendations": %s,
  "recommended_scope_paths": %s,
  "planner_scope_resolution": %q,
  "planner_rejected_scope_resolution": %q,
  "scope_correction_reason": %q,
  "excluded_scope_paths": %s,
  "retrieval_precedents": %s,
  "failure_hypotheses": %s,
  "patch_intent": %s,
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
}`, projectType, stack, goal, testFocus, repairMode, mustJSON(recommendations), mustJSON(recommendedScopePaths), scopeCorrection.PlannerScopeResolution, scopeCorrection.RejectedPlannerScopeResolution, scopeCorrection.ScopeCorrectionReason, mustJSON(scopeCorrection.ExcludedScopePaths), mustJSON(retrievalPrecedents), mustJSON(failureHypotheses), mustJSON(patchIntent), nextEditBrief, strings.Join(testCommand, " "), mustJSON(map[string]any{
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
	diff := ""
	changedFiles := []string(nil)
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
			diff, changedFiles = e.workspaceGitState()
			plannerAlignment := reviewScopeAlignment(anyHypothesisPaths(request["initial_scope_hypotheses"]), changedFiles)
			rejectedPlannerAlignment := reviewRejectedScopeAlignment(anyHypothesisPaths(request["rejected_scope_hypotheses"]), changedFiles)
			researchAlignment := reviewScopeAlignment(reviewResearchPaths(request), changedFiles)
			findings := append(buildValidationFailureFindings(testCommand, exitCode, message), buildReviewAlignmentFindings(plannerAlignment, rejectedPlannerAlignment, researchAlignment, anyHypothesisPaths(request["initial_scope_hypotheses"]), anyHypothesisPaths(request["rejected_scope_hypotheses"]), reviewResearchPaths(request), changedFiles)...)
			retrievalAlignment, retrievalRefs := reviewRetrievalAlignment(anyMapSliceDefault(request["objective_retrieval_precedents"], []map[string]any{}), changedFiles)
			findings = append(findings, buildReviewRetrievalFindings(retrievalAlignment, retrievalRefs, changedFiles)...)
			report := buildReviewPacketReport(decision, changedFiles, testCommand, request["execution_contract"], plannerAlignment, rejectedPlannerAlignment, researchAlignment, retrievalAlignment, retrievalRefs, findings)
			return domain.LocalBridgeResultRequest{
				Status:         "error",
				Summary:        &summary,
				Stdout:         stringPtr(stdout),
				Stderr:         stringPtr(stderr),
				ExitCode:       intPtr(exitCode),
				ErrorMessage:   &message,
				ReviewDecision: &decision,
				ReviewComments: []map[string]any{{"severity": "high", "message": message}},
				Findings:       findings,
				Diff:           &diff,
				ChangedFiles:   changedFiles,
				Artifacts: []map[string]any{
					{
						"type":         "review_packet",
						"title":        "Objective review packet",
						"media_type":   "application/json",
						"content_text": report,
					},
				},
			}, nil
		}
	}

	diff, changedFiles = e.workspaceGitState()
	if strings.TrimSpace(diff) == "" || len(changedFiles) == 0 {
		decision := "changes_requested"
		message := "review requires an actual repository diff before approval"
		summary := fmt.Sprintf("Review requested changes for %s", rel)
		findings := []map[string]any{{
			"kind":               "missing_diff",
			"severity":           "medium",
			"message":            message,
			"source":             "review",
			"project_root":       rel,
			"recommended_action": "Produce a real repository diff before asking for approval.",
		}}
		retrievalAlignment, retrievalRefs := reviewRetrievalAlignment(anyMapSliceDefault(request["objective_retrieval_precedents"], []map[string]any{}), changedFiles)
		findings = append(findings, buildReviewRetrievalFindings(retrievalAlignment, retrievalRefs, changedFiles)...)
		report := buildReviewPacketReport(decision, changedFiles, testCommand, request["execution_contract"], "unknown", "unknown", "unknown", retrievalAlignment, retrievalRefs, findings)
		return domain.LocalBridgeResultRequest{
			Status:         "error",
			Summary:        &summary,
			Stdout:         stringPtr(stdout),
			Stderr:         stringPtr(stderr),
			ExitCode:       intPtr(exitCode),
			ErrorMessage:   &message,
			ReviewDecision: &decision,
			ReviewComments: []map[string]any{{"severity": "medium", "message": message}},
			Findings:       findings,
			Diff:           &diff,
			ChangedFiles:   changedFiles,
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

	decision := "approved"
	summary := fmt.Sprintf("Review approved for %s", rel)
	plannerAlignment := reviewScopeAlignment(anyHypothesisPaths(request["initial_scope_hypotheses"]), changedFiles)
	rejectedPlannerAlignment := reviewRejectedScopeAlignment(anyHypothesisPaths(request["rejected_scope_hypotheses"]), changedFiles)
	researchAlignment := reviewScopeAlignment(reviewResearchPaths(request), changedFiles)
	findings := buildReviewAlignmentFindings(plannerAlignment, rejectedPlannerAlignment, researchAlignment, anyHypothesisPaths(request["initial_scope_hypotheses"]), anyHypothesisPaths(request["rejected_scope_hypotheses"]), reviewResearchPaths(request), changedFiles)
	retrievalAlignment, retrievalRefs := reviewRetrievalAlignment(anyMapSliceDefault(request["objective_retrieval_precedents"], []map[string]any{}), changedFiles)
	findings = append(findings, buildReviewRetrievalFindings(retrievalAlignment, retrievalRefs, changedFiles)...)
	report := buildReviewPacketReport(decision, changedFiles, testCommand, request["execution_contract"], plannerAlignment, rejectedPlannerAlignment, researchAlignment, retrievalAlignment, retrievalRefs, findings)
	return domain.LocalBridgeResultRequest{
		Status:         "success",
		Summary:        &summary,
		Stdout:         stringPtr(stdout),
		Stderr:         stringPtr(stderr),
		ExitCode:       intPtr(exitCode),
		Diff:           &diff,
		ChangedFiles:   changedFiles,
		ReviewDecision: &decision,
		Findings:       findings,
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

func anyHypothesisPaths(value any) []string {
	items := anyMapSliceDefault(value, []map[string]any{})
	if len(items) == 0 {
		switch typed := value.(type) {
		case []domain.ScopeHypothesis:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if path := strings.TrimSpace(item.Path); path != "" {
					out = append(out, path)
				}
			}
			return uniqueNonEmptyStrings(out)
		case []any:
			items = anyMapSliceDefault(typed, []map[string]any{})
		}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if path := strings.TrimSpace(asString(item["path"])); path != "" {
			out = append(out, path)
		}
	}
	return uniqueNonEmptyStrings(out)
}

func reviewResearchPaths(request map[string]any) []string {
	out := anyStringSliceDefault(request["suspected_paths"], []string{})
	for _, item := range anyMapSliceDefault(request["objective_patch_intent"], []map[string]any{}) {
		if path := strings.TrimSpace(asString(item["path"])); path != "" {
			out = append(out, path)
		}
	}
	return uniqueNonEmptyStrings(out)
}

func buildReviewPacketReport(decision string, changedFiles, testCommand []string, executionContract any, plannerAlignment, rejectedPlannerAlignment, researchAlignment, retrievalAlignment string, retrievalRefs []string, findings []map[string]any) string {
	return mustJSON(map[string]any{
		"decision":                         decision,
		"changed_files":                    changedFiles,
		"validation_command":               testCommand,
		"objective":                        executionContract,
		"planner_scope_alignment":          plannerAlignment,
		"planner_rejected_scope_alignment": rejectedPlannerAlignment,
		"research_scope_alignment":         researchAlignment,
		"retrieval_scope_alignment":        retrievalAlignment,
		"retrieval_precedent_refs":         retrievalRefs,
		"findings":                         findings,
	})
}

func buildReviewAlignmentFindings(plannerAlignment, rejectedPlannerAlignment, researchAlignment string, plannerPaths, rejectedPlannerPaths, researchPaths, changedFiles []string) []map[string]any {
	findings := make([]map[string]any, 0, 4)
	if plannerAlignment == "confirmed" {
		findings = append(findings, map[string]any{
			"kind":               "planner_scope_confirmed",
			"severity":           "info",
			"message":            "The observed diff confirms the planner's selected first-pass scope hypothesis.",
			"source":             "review",
			"expected_paths":     plannerPaths,
			"changed_files":      changedFiles,
			"recommended_action": "Use this confirmed planner precedent when narrowing similar future objectives.",
		})
	}
	if plannerAlignment == "contradicted" {
		findings = append(findings, map[string]any{
			"kind":               "planner_scope_contradicted",
			"severity":           "medium",
			"message":            "The observed diff contradicts the planner's initial scope hypothesis.",
			"source":             "review",
			"expected_paths":     plannerPaths,
			"changed_files":      changedFiles,
			"recommended_action": "Update the next plan using the files actually changed or revisit the planner's first-pass scope guess.",
		})
	}
	if rejectedPlannerAlignment == "contradicted" {
		findings = append(findings, map[string]any{
			"kind":               "planner_rejected_scope_contradicted",
			"severity":           "high",
			"message":            "The observed diff landed in a path the planner explicitly rejected in the first-pass shortlist.",
			"source":             "review",
			"expected_paths":     rejectedPlannerPaths,
			"changed_files":      changedFiles,
			"recommended_action": "Revisit the planner's rejection rationale and promote the actually changed file into the next plan.",
		})
	}
	if rejectedPlannerAlignment == "confirmed" {
		findings = append(findings, map[string]any{
			"kind":               "planner_rejected_scope_confirmed",
			"severity":           "info",
			"message":            "The observed diff avoided the planner's rejected competitors.",
			"source":             "review",
			"expected_paths":     rejectedPlannerPaths,
			"changed_files":      changedFiles,
			"recommended_action": "Treat the rejected competitor list as a valid exclusion precedent for similar objectives.",
		})
	}
	if researchAlignment == "contradicted" {
		findings = append(findings, map[string]any{
			"kind":               "research_scope_contradicted",
			"severity":           "high",
			"message":            "The observed diff contradicts the research-guided patch intent.",
			"source":             "review",
			"expected_paths":     researchPaths,
			"changed_files":      changedFiles,
			"recommended_action": "Narrow the next repair cycle to the files that actually need changes and revise the research scope before editing again.",
		})
	}
	return findings
}

func buildReviewRetrievalFindings(retrievalAlignment string, retrievalRefs, changedFiles []string) []map[string]any {
	switch retrievalAlignment {
	case "confirmed":
		return []map[string]any{{
			"kind":               "retrieval_scope_confirmed",
			"severity":           "info",
			"message":            "The observed diff aligns with a retrieved precedent.",
			"source":             "review",
			"precedent_refs":     retrievalRefs,
			"changed_files":      changedFiles,
			"recommended_action": "Reuse this precedent class for similar objectives.",
		}}
	case "contradicted":
		return []map[string]any{{
			"kind":               "retrieval_scope_contradicted",
			"severity":           "medium",
			"message":            "The observed diff does not align with the retrieved precedents that were carried into the edit.",
			"source":             "review",
			"precedent_refs":     retrievalRefs,
			"changed_files":      changedFiles,
			"recommended_action": "Demote the contradicted precedent and rely on the actual diff for the next repair cycle.",
		}}
	default:
		return nil
	}
}

func reviewScopeAlignment(expectedPaths, changedFiles []string) string {
	if len(expectedPaths) == 0 || len(changedFiles) == 0 {
		return "unknown"
	}
	for _, changed := range changedFiles {
		for _, expected := range expectedPaths {
			if strings.TrimSpace(changed) == strings.TrimSpace(expected) {
				return "confirmed"
			}
		}
	}
	return "contradicted"
}

func reviewRejectedScopeAlignment(rejectedPaths, changedFiles []string) string {
	if len(rejectedPaths) == 0 || len(changedFiles) == 0 {
		return "unknown"
	}
	for _, changed := range changedFiles {
		for _, rejected := range rejectedPaths {
			if strings.TrimSpace(changed) == strings.TrimSpace(rejected) {
				return "contradicted"
			}
		}
	}
	return "confirmed"
}

func reviewRetrievalAlignment(precedents []map[string]any, changedFiles []string) (string, []string) {
	if len(precedents) == 0 || len(changedFiles) == 0 {
		return "unknown", nil
	}
	refs := make([]string, 0, len(precedents))
	confirmed := false
	for _, item := range precedents {
		sourceRef := strings.TrimSpace(asString(item["source_ref"]))
		summary := strings.ToLower(strings.TrimSpace(asString(item["summary"])))
		if sourceRef != "" {
			refs = append(refs, sourceRef)
		}
		for _, changed := range changedFiles {
			changed = strings.ToLower(strings.TrimSpace(changed))
			if changed == "" {
				continue
			}
			base := strings.ToLower(filepath.Base(changed))
			if strings.Contains(summary, changed) || strings.Contains(summary, base) {
				confirmed = true
				break
			}
		}
		if confirmed {
			break
		}
	}
	refs = uniqueNonEmptyStrings(refs)
	if len(refs) == 0 {
		return "unknown", nil
	}
	if confirmed {
		return "confirmed", refs
	}
	return "contradicted", refs
}

func buildValidationFailureFindings(argv []string, exitCode int, message string) []map[string]any {
	message = strings.TrimSpace(message)
	if len(argv) == 0 && message == "" {
		return nil
	}
	return []map[string]any{{
		"kind":               "validation_failure",
		"severity":           "high",
		"message":            firstNonEmptyString(message, "Validation command failed."),
		"source":             "validate",
		"validation_command": argv,
		"exit_code":          exitCode,
		"recommended_action": "Reproduce the failing validation command and fix the error before approval.",
	}}
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
	if treeFiles := listProjectFiles(projectRoot); len(treeFiles) > 0 {
		evidence["tree_files"] = treeFiles
		usage = append(usage, map[string]any{
			"capability": "filesystem.read",
			"helped":     true,
			"source":     filepath.ToSlash(projectRootValue),
			"kind":       "local_repo_tree",
		})
	}
	return evidence, usage
}

func listProjectFiles(projectRoot string) []string {
	entries := make([]string, 0, 24)
	_ = filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == projectRoot {
			return nil
		}
		rel, err := filepath.Rel(projectRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch rel {
			case ".git", ".lab", "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !isInterestingProjectFile(rel) {
			return nil
		}
		entries = append(entries, rel)
		if len(entries) >= 24 {
			return filepath.SkipAll
		}
		return nil
	})
	slices.Sort(entries)
	return entries
}

func isInterestingProjectFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".kt", ".rs", ".rb", ".php", ".md", ".txt", ".json", ".toml", ".yaml", ".yml":
		return true
	default:
		return strings.EqualFold(filepath.Base(path), "Dockerfile")
	}
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
	result, err := e.runAllowedCommand(ctx, argv, "Ran command: "+strings.Join(argv, " "))
	if err != nil {
		return result, err
	}
	if len(argv) > 0 && argv[0] == "aider-task" {
		if enriched, enrichErr := e.withGitArtifacts(result); enrichErr == nil {
			result = enriched
		}
		metadata, _ := request["_task_metadata"].(map[string]any)
		result = e.withCoderPacket(result, argv, metadata)
	}
	return result, nil
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

func (e *WorkspaceExecutor) withCoderPacket(result domain.LocalBridgeResultRequest, argv []string, metadata map[string]any) domain.LocalBridgeResultRequest {
	if len(argv) == 0 || argv[0] != "aider-task" {
		return result
	}
	gitStatus, diffStat := e.workspaceGitSummaries()
	hasObservableChanges := strings.TrimSpace(derefStringPtr(result.Diff)) != "" || len(result.ChangedFiles) > 0 || strings.TrimSpace(gitStatus) != "" || strings.TrimSpace(diffStat) != ""
	packet := map[string]any{
		"tool":                "aider-task",
		"status":              strings.ToLower(strings.TrimSpace(result.Status)),
		"summary":             strings.TrimSpace(derefStringPtr(result.Summary)),
		"exit_code":           derefIntPtr(result.ExitCode),
		"changed_files":       append([]string{}, result.ChangedFiles...),
		"changed_file_count":  len(result.ChangedFiles),
		"diff_present":        hasObservableChanges,
		"git_status_short":    gitStatus,
		"git_diff_stat":       diffStat,
		"validation_commands": anyStringSliceDefault(metadata["validation_commands"], []string{}),
		"suspected_paths":     anyStringSliceDefault(metadata["suspected_paths"], []string{}),
		"objective_iteration": asInt(metadata["objective_iteration"], 1),
		"scope_correction": map[string]any{
			"planner_scope_resolution":          strings.TrimSpace(asString(metadata["planner_scope_resolution"])),
			"planner_rejected_scope_resolution": strings.TrimSpace(asString(metadata["planner_rejected_scope_resolution"])),
			"excluded_scope_paths":              anyStringSliceDefault(metadata["excluded_scope_paths"], []string{}),
			"reason":                            strings.TrimSpace(asString(metadata["scope_correction_reason"])),
		},
		"self_check": map[string]any{
			"git_status_short_present": strings.TrimSpace(gitStatus) != "",
			"git_diff_stat_present":    strings.TrimSpace(diffStat) != "",
			"diff_present":             hasObservableChanges,
		},
	}
	if stderr := strings.TrimSpace(derefStringPtr(result.Stderr)); stderr != "" {
		packet["stderr_excerpt"] = truncateForPrompt(stderr, 800)
	}
	if stdout := strings.TrimSpace(derefStringPtr(result.Stdout)); stdout != "" {
		packet["stdout_excerpt"] = truncateForPrompt(stdout, 800)
	}
	result.Artifacts = append(result.Artifacts, map[string]any{
		"type":         "coder_packet",
		"title":        "Coder execution packet",
		"media_type":   "application/json",
		"content_text": mustJSON(packet),
	})
	return result
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

func (e *WorkspaceExecutor) workspaceGitSummaries() (string, string) {
	statusCmd := exec.Command("git", "status", "--short", "--untracked-files=all")
	statusCmd.Dir = e.workspaceRoot
	statusOut, _ := statusCmd.Output()
	diffStatCmd := exec.Command("git", "diff", "--stat", "--no-ext-diff")
	diffStatCmd.Dir = e.workspaceRoot
	diffStatOut, _ := diffStatCmd.Output()
	return string(statusOut), string(diffStatOut)
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

func buildRepairEditBrief(goal, repairFeedback string, priorFindings []map[string]any, priorTestResults map[string]any, scopePaths []string, failureHypotheses, patchIntent []map[string]any, scopeCorrection plannerRepairScopeCorrection, retrievalPrecedents []map[string]any) string {
	lines := []string{strings.TrimSpace(goal)}
	if strings.TrimSpace(repairFeedback) != "" {
		lines = append(lines, "Repair feedback:", strings.TrimSpace(repairFeedback))
	}
	if scopeCorrection.ScopeCorrectionReason != "" {
		lines = append(lines, "Scope correction:", scopeCorrection.ScopeCorrectionReason)
	}
	if len(scopeCorrection.ConfirmedPlannerPaths) > 0 {
		lines = append(lines, "Planner confirmed paths: "+strings.Join(scopeCorrection.ConfirmedPlannerPaths, ", "))
	}
	if len(scopeCorrection.ContradictedPlannerPaths) > 0 {
		lines = append(lines, "Planner selected paths contradicted by review: "+strings.Join(scopeCorrection.ContradictedPlannerPaths, ", "))
	} else if scopeCorrection.PlannerScopeResolution == "contradicted" {
		lines = append(lines, "Planner selected paths contradicted by review: previous shortlist lost priority for the next attempt")
	}
	if len(scopeCorrection.PromotedRejectedPaths) > 0 {
		lines = append(lines, "Planner rejected but observed in diff: "+strings.Join(scopeCorrection.PromotedRejectedPaths, ", "))
	}
	if len(scopeCorrection.ConfirmedRejectedPaths) > 0 {
		lines = append(lines, "Planner rejected paths still excluded: "+strings.Join(scopeCorrection.ConfirmedRejectedPaths, ", "))
	} else if scopeCorrection.RejectedPlannerScopeResolution == "confirmed" && len(scopeCorrection.ExcludedScopePaths) > 0 {
		lines = append(lines, "Planner rejected paths still excluded: "+strings.Join(scopeCorrection.ExcludedScopePaths, ", "))
	}
	if len(scopeCorrection.ExcludedScopePaths) > 0 {
		lines = append(lines, "Excluded paths for next attempt: "+strings.Join(scopeCorrection.ExcludedScopePaths, ", "))
	}
	if len(retrievalPrecedents) > 0 {
		lines = append(lines, "Retrieved precedents:")
		for _, item := range retrievalPrecedents {
			sourceRef := strings.TrimSpace(asString(item["source_ref"]))
			summary := strings.TrimSpace(asString(item["summary"]))
			switch {
			case sourceRef != "" && summary != "":
				lines = append(lines, fmt.Sprintf("- %s -> %s", sourceRef, summary))
			case summary != "":
				lines = append(lines, "- "+summary)
			}
		}
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
	if len(failureHypotheses) > 0 {
		lines = append(lines, "Failure hypotheses:")
		for _, item := range failureHypotheses {
			path := strings.TrimSpace(asString(item["path"]))
			failureMode := strings.TrimSpace(asString(item["failure_mode"]))
			evidence := strings.TrimSpace(asString(item["evidence"]))
			priority := strings.TrimSpace(asString(item["priority"]))
			label := firstNonEmptyString(priority, failureMode, "info")
			switch {
			case path != "" && evidence != "":
				lines = append(lines, fmt.Sprintf("- [%s] %s -> %s", label, path, evidence))
			case evidence != "":
				lines = append(lines, fmt.Sprintf("- [%s] %s", label, evidence))
			}
		}
	}
	if len(patchIntent) > 0 {
		lines = append(lines, "Patch intent:")
		for _, item := range patchIntent {
			path := strings.TrimSpace(asString(item["path"]))
			action := strings.TrimSpace(asString(item["action"]))
			rationale := strings.TrimSpace(asString(item["rationale"]))
			switch {
			case path != "" && action != "" && rationale != "":
				lines = append(lines, fmt.Sprintf("- %s: %s (%s)", path, action, rationale))
			case path != "" && action != "":
				lines = append(lines, fmt.Sprintf("- %s: %s", path, action))
			case action != "":
				lines = append(lines, "- "+action)
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type plannerRepairScopeCorrection struct {
	PlannerScopeResolution         string
	RejectedPlannerScopeResolution string
	ConfirmedPlannerPaths          []string
	ContradictedPlannerPaths       []string
	PromotedRejectedPaths          []string
	ConfirmedRejectedPaths         []string
	ExcludedScopePaths             []string
	ScopeCorrectionReason          string
}

func recommendRepairScopePaths(priorChangedFiles, expectedFiles []string) []string {
	scopePaths := make([]string, 0, len(priorChangedFiles)+len(expectedFiles))
	scopePaths = append(scopePaths, priorChangedFiles...)
	if len(scopePaths) == 0 {
		scopePaths = append(scopePaths, expectedFiles...)
	}
	return uniqueNonEmptyStrings(scopePaths)
}

func recommendResearchScopePaths(goal string, scopeCorrection plannerRepairScopeCorrection, priorChangedFiles, priorPatchIntentPaths, expectedFiles, treeFiles []string, readmeExcerpt string) []string {
	readmeInferred := inferScopePathsFromReadme(goal, readmeExcerpt, treeFiles)
	inferred := inferScopePathsFromGoal(goal, treeFiles)
	scopePaths := make([]string, 0, len(scopeCorrection.ConfirmedPlannerPaths)+len(scopeCorrection.PromotedRejectedPaths)+len(priorChangedFiles)+len(priorPatchIntentPaths)+len(expectedFiles)+len(readmeInferred)+len(inferred))
	scopePaths = append(scopePaths, scopeCorrection.PromotedRejectedPaths...)
	scopePaths = append(scopePaths, scopeCorrection.ConfirmedPlannerPaths...)
	scopePaths = append(scopePaths, priorChangedFiles...)
	scopePaths = append(scopePaths, priorPatchIntentPaths...)
	scopePaths = append(scopePaths, expectedFiles...)
	scopePaths = append(scopePaths, readmeInferred...)
	scopePaths = append(scopePaths, inferred...)
	scopePaths = filterValidationArtifactPaths(uniqueNonEmptyStrings(scopePaths))
	if len(scopeCorrection.ExcludedScopePaths) == 0 {
		return scopePaths
	}
	excluded := map[string]struct{}{}
	for _, path := range scopeCorrection.ExcludedScopePaths {
		excluded[path] = struct{}{}
	}
	filtered := make([]string, 0, len(scopePaths))
	for _, path := range scopePaths {
		if _, skip := excluded[path]; skip {
			continue
		}
		filtered = append(filtered, path)
	}
	if len(filtered) > 0 {
		return filtered
	}
	return scopePaths
}

func filterValidationArtifactPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		switch strings.ToUpper(strings.TrimSpace(path)) {
		case "PASS", "FAIL":
			continue
		default:
			filtered = append(filtered, path)
		}
	}
	return filtered
}

func inferScopePathsFromGoal(goal string, treeFiles []string) []string {
	goal = strings.ToLower(strings.TrimSpace(goal))
	if goal == "" || len(treeFiles) == 0 {
		return nil
	}
	type candidate struct {
		path  string
		score int
	}
	candidates := make([]candidate, 0, len(treeFiles))
	for _, path := range uniqueNonEmptyStrings(treeFiles) {
		lowerPath := strings.ToLower(strings.TrimSpace(path))
		if lowerPath == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(lowerPath))
		score := 0
		switch {
		case strings.Contains(goal, lowerPath):
			score += 10
		case strings.Contains(goal, base):
			score += 8
		}
		nameWithoutExt := strings.TrimSuffix(base, filepath.Ext(base))
		for _, token := range strings.FieldsFunc(nameWithoutExt, func(r rune) bool {
			return r == '_' || r == '-' || r == '.' || r == '/' || r == '\\'
		}) {
			token = strings.TrimSpace(token)
			if len(token) < 4 {
				continue
			}
			if strings.Contains(goal, token) {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, candidate{path: path, score: score})
		}
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.path, b.path)
	})
	out := make([]string, 0, len(candidates))
	for _, item := range candidates {
		out = append(out, item.path)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func inferScopePathsFromReadme(goal, readmeExcerpt string, treeFiles []string) []string {
	readmeExcerpt = strings.ToLower(strings.TrimSpace(readmeExcerpt))
	if readmeExcerpt == "" || len(treeFiles) == 0 {
		return nil
	}
	goalTokens := informativeGoalTokens(goal)
	readmeLines := strings.Split(readmeExcerpt, "\n")
	type candidate struct {
		path  string
		score int
	}
	candidates := make([]candidate, 0, len(treeFiles))
	for _, path := range uniqueNonEmptyStrings(treeFiles) {
		lowerPath := strings.ToLower(strings.TrimSpace(path))
		if lowerPath == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(lowerPath))
		score := 0
		for _, line := range readmeLines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			matchesPath := strings.Contains(line, lowerPath)
			matchesBase := strings.Contains(line, base)
			if !matchesPath && !matchesBase {
				continue
			}
			lineScore := 0
			if matchesPath {
				lineScore += 6
			} else if matchesBase {
				lineScore += 4
			}
			for _, token := range goalTokens {
				if strings.Contains(line, token) {
					lineScore++
				}
			}
			lineScore += readmeCueWeight(line)
			if lineScore > score {
				score = lineScore
			}
		}
		if score <= 0 {
			continue
		}
		candidates = append(candidates, candidate{path: path, score: score})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.path, b.path)
	})
	out := make([]string, 0, len(candidates))
	for _, item := range candidates {
		out = append(out, item.path)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func readmeCueWeight(line string) int {
	weight := 0
	for _, positive := range []string{"current", "primary", "implemented", "lives in", "source of truth", "toggle", "objective"} {
		if strings.Contains(line, positive) {
			weight += 2
		}
	}
	for _, negative := range []string{"legacy", "deprecated", "old", "unused", "do not", "should not", "obsolete"} {
		if strings.Contains(line, negative) {
			weight -= 3
		}
	}
	return weight
}

func informativeGoalTokens(goal string) []string {
	goal = strings.ToLower(strings.TrimSpace(goal))
	if goal == "" {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 8)
	for _, token := range strings.FieldsFunc(goal, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == ',' || r == '.' || r == ':' || r == ';' || r == '-' || r == '_' || r == '/' || r == '\\' || r == '(' || r == ')'
	}) {
		token = strings.TrimSpace(token)
		if len(token) < 4 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func buildRepairFailureHypotheses(priorFindings []map[string]any, priorTestResults map[string]any, scopePaths []string, scopeCorrection plannerRepairScopeCorrection) []map[string]any {
	if len(scopePaths) == 0 && len(priorFindings) == 0 && len(priorTestResults) == 0 {
		return nil
	}
	hypotheses := make([]map[string]any, 0, len(scopePaths)+1)
	for _, path := range scopePaths {
		finding := firstRelevantFindingForPath(priorFindings, path)
		failureMode := "targeted_regression"
		evidence := ""
		priority := "medium"
		if exitCode, ok := asIntOK(priorTestResults["exit_code"]); ok && exitCode != 0 {
			failureMode = "validation_failure"
			evidence = fmt.Sprintf("Declared validation command exited with code %d.", exitCode)
			priority = "high"
		}
		if finding != nil {
			if kind := strings.TrimSpace(asString(finding["kind"])); kind != "" {
				failureMode = kind
			}
			if message := strings.TrimSpace(asString(finding["message"])); message != "" {
				evidence = message
			}
			if severity := strings.TrimSpace(asString(finding["severity"])); severity != "" {
				priority = severity
			}
		}
		hypothesis := map[string]any{
			"path":           path,
			"failure_mode":   failureMode,
			"priority":       priority,
			"cause_category": repairCauseCategoryForPath(path, scopeCorrection),
		}
		if evidence != "" {
			hypothesis["evidence"] = evidence
		}
		hypotheses = append(hypotheses, hypothesis)
	}
	if len(hypotheses) == 0 {
		hypothesis := map[string]any{
			"failure_mode":   "validation_failure",
			"priority":       "high",
			"cause_category": repairCauseCategoryForPath("", scopeCorrection),
		}
		if exitCode, ok := asIntOK(priorTestResults["exit_code"]); ok && exitCode != 0 {
			hypothesis["evidence"] = fmt.Sprintf("Declared validation command exited with code %d.", exitCode)
		}
		hypotheses = append(hypotheses, hypothesis)
	}
	return hypotheses
}

func buildRepairPatchIntent(failureHypotheses []map[string]any, scopePaths []string, scopeCorrection plannerRepairScopeCorrection) []map[string]any {
	if len(scopePaths) == 0 && len(failureHypotheses) == 0 {
		return nil
	}
	fallbackAction := "inspect the failing logic, apply the narrowest fix that satisfies validation, and avoid unrelated edits"
	intents := make([]map[string]any, 0, len(scopePaths)+1)
	for _, path := range scopePaths {
		intent := map[string]any{
			"path":   path,
			"action": fallbackAction,
		}
		hypothesis := firstRepairHypothesisForPath(failureHypotheses, path)
		if hypothesis != nil {
			failureMode := strings.TrimSpace(asString(hypothesis["failure_mode"]))
			if failureMode != "" {
				intent["failure_mode"] = failureMode
			}
			if priority := strings.TrimSpace(asString(hypothesis["priority"])); priority != "" {
				intent["priority"] = priority
			}
			if evidence := strings.TrimSpace(asString(hypothesis["evidence"])); evidence != "" {
				intent["rationale"] = evidence
			}
			causeCategory := strings.TrimSpace(asString(hypothesis["cause_category"]))
			if causeCategory != "" {
				intent["cause_category"] = causeCategory
			}
			switch failureMode {
			case "validation_failure":
				intent["action"] = "repair the validation failure in this file first, then rerun the declared validation command before widening scope"
			case "missing_diff":
				intent["action"] = "produce the missing code change in this file and verify the diff is real before review"
			default:
				intent["action"] = "repair the behavior implicated by the latest finding and keep the patch limited to this file"
			}
			switch causeCategory {
			case "bad_planner_shortlist":
				intent["action"] = "shift the patch toward the files observed in the failed iteration and avoid returning to the planner's contradicted shortlist"
			case "bad_edit_against_good_shortlist":
				intent["action"] = "stay on the planner-confirmed shortlist and correct the implementation without widening scope"
			case "edit_landed_in_rejected_path":
				intent["action"] = "repair the path that the previous edit actually touched, then decide whether that rejected path should remain in scope"
			}
		}
		intents = append(intents, intent)
	}
	if len(intents) == 0 {
		intents = append(intents, map[string]any{
			"action": fallbackAction,
		})
	}
	return intents
}

func interpretPlannerRepairFindings(priorFindings []map[string]any, selectedPlannerPaths, rejectedPlannerPaths, changedFiles []string) plannerRepairScopeCorrection {
	selectedPlannerPaths = uniqueNonEmptyStrings(selectedPlannerPaths)
	rejectedPlannerPaths = uniqueNonEmptyStrings(rejectedPlannerPaths)
	changedFiles = uniqueNonEmptyStrings(changedFiles)
	correction := plannerRepairScopeCorrection{
		PlannerScopeResolution:         "unchanged",
		RejectedPlannerScopeResolution: "unchanged",
	}
	kinds := findingKinds(priorFindings)
	selectedInDiff := intersectStrings(changedFiles, selectedPlannerPaths)
	rejectedInDiff := intersectStrings(changedFiles, rejectedPlannerPaths)
	excluded := []string{}
	reasons := []string{}
	if kinds["planner_scope_confirmed"] {
		correction.PlannerScopeResolution = "confirmed"
		correction.ConfirmedPlannerPaths = selectedPlannerPaths
		if len(selectedPlannerPaths) > 0 {
			reasons = append(reasons, "keep the planner-confirmed shortlist as the primary target")
		}
	}
	if kinds["planner_scope_contradicted"] {
		correction.PlannerScopeResolution = "contradicted"
		for _, path := range selectedPlannerPaths {
			if !containsString(selectedInDiff, path) {
				excluded = append(excluded, path)
				correction.ContradictedPlannerPaths = append(correction.ContradictedPlannerPaths, path)
			}
		}
		if len(excluded) > 0 {
			reasons = append(reasons, "drop planner-selected paths that did not appear in the failed diff")
		}
	}
	if kinds["planner_rejected_scope_confirmed"] {
		correction.RejectedPlannerScopeResolution = "confirmed"
		correction.ConfirmedRejectedPaths = append(correction.ConfirmedRejectedPaths, rejectedPlannerPaths...)
		excluded = append(excluded, rejectedPlannerPaths...)
		if len(rejectedPlannerPaths) > 0 {
			reasons = append(reasons, "keep planner-rejected competitor paths excluded")
		}
	}
	if kinds["planner_rejected_scope_contradicted"] {
		correction.RejectedPlannerScopeResolution = "contradicted"
		correction.PromotedRejectedPaths = rejectedInDiff
		excluded = differenceStrings(excluded, rejectedInDiff)
		if len(rejectedInDiff) > 0 {
			reasons = append(reasons, "promote rejected paths that the failed edit actually touched")
		}
	}
	correction.ContradictedPlannerPaths = uniqueNonEmptyStrings(correction.ContradictedPlannerPaths)
	correction.ConfirmedRejectedPaths = uniqueNonEmptyStrings(correction.ConfirmedRejectedPaths)
	correction.ExcludedScopePaths = uniqueNonEmptyStrings(excluded)
	correction.ScopeCorrectionReason = strings.TrimSpace(strings.Join(uniqueNonEmptyStrings(reasons), "; "))
	return correction
}

func repairCauseCategoryForPath(path string, scopeCorrection plannerRepairScopeCorrection) string {
	switch {
	case path != "" && containsString(scopeCorrection.PromotedRejectedPaths, path):
		return "edit_landed_in_rejected_path"
	case path != "" && containsString(scopeCorrection.ConfirmedPlannerPaths, path):
		return "bad_edit_against_good_shortlist"
	case scopeCorrection.PlannerScopeResolution == "contradicted":
		return "bad_planner_shortlist"
	case scopeCorrection.RejectedPlannerScopeResolution == "contradicted":
		return "edit_landed_in_rejected_path"
	case scopeCorrection.PlannerScopeResolution == "confirmed":
		return "bad_edit_against_good_shortlist"
	default:
		return "bad_planner_shortlist"
	}
}

func findingKinds(findings []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, finding := range findings {
		kind := strings.TrimSpace(asString(finding["kind"]))
		if kind == "" {
			continue
		}
		out[kind] = true
	}
	return out
}

func firstRelevantFindingForPath(findings []map[string]any, path string) map[string]any {
	normalizedPath := strings.TrimSpace(path)
	for _, finding := range findings {
		message := strings.TrimSpace(asString(finding["message"]))
		if message == "" {
			continue
		}
		findingPath := strings.TrimSpace(asString(finding["path"]))
		switch {
		case findingPath != "" && findingPath == normalizedPath:
			return finding
		case normalizedPath != "" && strings.Contains(message, normalizedPath):
			return finding
		}
	}
	if len(findings) > 0 {
		return findings[0]
	}
	return nil
}

func firstRepairHypothesisForPath(hypotheses []map[string]any, path string) map[string]any {
	normalizedPath := strings.TrimSpace(path)
	for _, hypothesis := range hypotheses {
		hypothesisPath := strings.TrimSpace(asString(hypothesis["path"]))
		if hypothesisPath != "" && hypothesisPath == normalizedPath {
			return hypothesis
		}
	}
	if len(hypotheses) > 0 {
		return hypotheses[0]
	}
	return nil
}

func intersectStrings(values, candidates []string) []string {
	if len(values) == 0 || len(candidates) == 0 {
		return nil
	}
	allowed := map[string]struct{}{}
	for _, item := range uniqueNonEmptyStrings(candidates) {
		allowed[item] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, item := range uniqueNonEmptyStrings(values) {
		if _, ok := allowed[item]; ok {
			out = append(out, item)
		}
	}
	return uniqueNonEmptyStrings(out)
}

func differenceStrings(values, remove []string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(remove) == 0 {
		return uniqueNonEmptyStrings(values)
	}
	skip := map[string]struct{}{}
	for _, item := range uniqueNonEmptyStrings(remove) {
		skip[item] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, item := range uniqueNonEmptyStrings(values) {
		if _, ok := skip[item]; ok {
			continue
		}
		out = append(out, item)
	}
	return uniqueNonEmptyStrings(out)
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range values {
		if target != "" && strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func uniqueNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func retrievalPrecedentsFromContextPackage(value any) []map[string]any {
	contextPackage, _ := value.(map[string]any)
	if len(contextPackage) == 0 {
		return nil
	}
	rawChunks, _ := contextPackage["chunks"].([]any)
	if len(rawChunks) == 0 {
		return nil
	}
	precedents := make([]map[string]any, 0, min(3, len(rawChunks)))
	for _, raw := range rawChunks {
		chunk, _ := raw.(map[string]any)
		if len(chunk) == 0 {
			continue
		}
		precedent := map[string]any{
			"source_ref":  strings.TrimSpace(asString(chunk["source_ref"])),
			"source_type": strings.TrimSpace(asString(chunk["source_type"])),
			"source_id":   strings.TrimSpace(asString(chunk["source_id"])),
			"summary":     summarizeBridgeRetrievalChunk(strings.TrimSpace(asString(chunk["content_text"]))),
		}
		if score, ok := chunk["score"].(float64); ok {
			precedent["score"] = score
		}
		if initiativeID := strings.TrimSpace(asString(chunk["initiative_id"])); initiativeID != "" {
			precedent["initiative_id"] = initiativeID
		}
		if taskID := strings.TrimSpace(asString(chunk["task_id"])); taskID != "" {
			precedent["task_id"] = taskID
		}
		if artifactID := strings.TrimSpace(asString(chunk["artifact_id"])); artifactID != "" {
			precedent["artifact_id"] = artifactID
		}
		precedents = append(precedents, precedent)
		if len(precedents) >= 3 {
			break
		}
	}
	if len(precedents) == 0 {
		return nil
	}
	return precedents
}

func summarizeBridgeRetrievalChunk(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= 180 {
		return text
	}
	return strings.TrimSpace(string(runes[:180])) + "..."
}

func buildAiderTaskMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{
		"objective_title":                   strings.TrimSpace(asString(metadata["objective_title"])),
		"initiative_goal":                   strings.TrimSpace(asString(metadata["initiative_goal"])),
		"work_item_kind":                    strings.TrimSpace(asString(metadata["work_item_kind"])),
		"objective_iteration":               asInt(metadata["objective_iteration"], 1),
		"objective_max_iterations":          asInt(metadata["objective_max_iterations"], 3),
		"objective_research_brief":          strings.TrimSpace(asString(metadata["objective_research_brief"])),
		"repair_feedback":                   strings.TrimSpace(asString(metadata["objective_repair_feedback"])),
		"review_decision":                   strings.TrimSpace(asString(metadata["review_decision"])),
		"review_comments":                   anyMapSliceDefault(metadata["review_comments"], []map[string]any{}),
		"objective_findings":                anyMapSliceDefault(metadata["objective_findings"], []map[string]any{}),
		"objective_failure_hypotheses":      anyMapSliceDefault(metadata["objective_failure_hypotheses"], []map[string]any{}),
		"objective_patch_intent":            anyMapSliceDefault(metadata["objective_patch_intent"], []map[string]any{}),
		"objective_baseline_test_results":   nonNilMap(metadata["objective_baseline_test_results"]),
		"objective_baseline_findings":       anyMapSliceDefault(metadata["objective_baseline_findings"], []map[string]any{}),
		"objective_baseline_summary":        strings.TrimSpace(asString(metadata["objective_baseline_summary"])),
		"objective_analysis_findings":       anyMapSliceDefault(metadata["objective_analysis_findings"], []map[string]any{}),
		"objective_analysis_summary":        nonNilMap(metadata["objective_analysis_summary"]),
		"excluded_scope_paths":              anyStringSliceDefault(metadata["excluded_scope_paths"], []string{}),
		"scope_correction_reason":           strings.TrimSpace(asString(metadata["scope_correction_reason"])),
		"planner_scope_resolution":          strings.TrimSpace(asString(metadata["planner_scope_resolution"])),
		"planner_rejected_scope_resolution": strings.TrimSpace(asString(metadata["planner_rejected_scope_resolution"])),
		"objective_retrieval_precedents":    anyMapSliceDefault(metadata["objective_retrieval_precedents"], []map[string]any{}),
		"retrieval_packet_artifact_id":      strings.TrimSpace(asString(metadata["retrieval_packet_artifact_id"])),
		"validation_commands":               anyStringSliceDefault(metadata["validation_commands"], []string{}),
		"suspected_paths":                   anyStringSliceDefault(metadata["suspected_paths"], []string{}),
	}
	if contextPackage, ok := metadata["context_package"].(map[string]any); ok && len(contextPackage) > 0 {
		out["context_prompt_section"] = strings.TrimSpace(asString(contextPackage["prompt_section"]))
		out["context_source_refs"] = contextPackageSourceRefs(contextPackage["source_refs"])
	}
	return out
}

func enrichAiderDescription(description string, metadata map[string]any) string {
	lines := []string{strings.TrimSpace(description)}
	if researchBrief := strings.TrimSpace(asString(metadata["objective_research_brief"])); researchBrief != "" {
		lines = append(lines, "", "Research brief:", researchBrief)
	}
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
	if findings := anyMapSliceDefault(metadata["objective_findings"], []map[string]any{}); len(findings) > 0 {
		lines = append(lines, "", "Structured findings:")
		for _, item := range findings {
			message := strings.TrimSpace(asString(item["message"]))
			if message == "" {
				continue
			}
			severity := strings.TrimSpace(asString(item["severity"]))
			kind := strings.TrimSpace(asString(item["kind"]))
			label := firstNonEmptyString(severity, kind, "info")
			lines = append(lines, fmt.Sprintf("- [%s] %s", label, message))
		}
	}
	if hypotheses := anyMapSliceDefault(metadata["objective_failure_hypotheses"], []map[string]any{}); len(hypotheses) > 0 {
		lines = append(lines, "", "Failure hypotheses:")
		for _, item := range hypotheses {
			path := strings.TrimSpace(asString(item["path"]))
			failureMode := strings.TrimSpace(asString(item["failure_mode"]))
			evidence := strings.TrimSpace(asString(item["evidence"]))
			priority := strings.TrimSpace(asString(item["priority"]))
			label := firstNonEmptyString(priority, failureMode, "info")
			switch {
			case path != "" && evidence != "":
				lines = append(lines, fmt.Sprintf("- [%s] %s -> %s", label, path, evidence))
			case evidence != "":
				lines = append(lines, fmt.Sprintf("- [%s] %s", label, evidence))
			}
		}
	}
	if baselineSummary := strings.TrimSpace(asString(metadata["objective_baseline_summary"])); baselineSummary != "" {
		lines = append(lines, "", "Baseline validation:", baselineSummary)
	}
	if baselineFindings := anyMapSliceDefault(metadata["objective_baseline_findings"], []map[string]any{}); len(baselineFindings) > 0 {
		lines = append(lines, "Baseline validation findings:")
		for _, item := range baselineFindings {
			message := strings.TrimSpace(asString(item["message"]))
			if message == "" {
				continue
			}
			severity := strings.TrimSpace(asString(item["severity"]))
			label := firstNonEmptyString(severity, "info")
			lines = append(lines, fmt.Sprintf("- [%s] %s", label, message))
		}
	}
	if patchIntent := anyMapSliceDefault(metadata["objective_patch_intent"], []map[string]any{}); len(patchIntent) > 0 {
		lines = append(lines, "", "Patch intent:")
		for _, item := range patchIntent {
			path := strings.TrimSpace(asString(item["path"]))
			action := strings.TrimSpace(asString(item["action"]))
			rationale := strings.TrimSpace(asString(item["rationale"]))
			switch {
			case path != "" && action != "" && rationale != "":
				lines = append(lines, fmt.Sprintf("- %s: %s (%s)", path, action, rationale))
			case path != "" && action != "":
				lines = append(lines, fmt.Sprintf("- %s: %s", path, action))
			case action != "":
				lines = append(lines, "- "+action)
			}
		}
	}
	if analysisFindings := anyMapSliceDefault(metadata["objective_analysis_findings"], []map[string]any{}); len(analysisFindings) > 0 {
		lines = append(lines, "", "Static analysis findings:")
		for _, item := range analysisFindings {
			message := strings.TrimSpace(asString(item["message"]))
			if message == "" {
				continue
			}
			location, _ := item["location"].(map[string]any)
			file := strings.TrimSpace(asString(location["file"]))
			severity := strings.TrimSpace(asString(item["severity"]))
			label := firstNonEmptyString(severity, "info")
			if file != "" {
				lines = append(lines, fmt.Sprintf("- [%s] %s (%s)", label, message, file))
			} else {
				lines = append(lines, fmt.Sprintf("- [%s] %s", label, message))
			}
		}
	}
	if reason := strings.TrimSpace(asString(metadata["scope_correction_reason"])); reason != "" {
		lines = append(lines, "", "Scope correction:", reason)
	}
	if resolution := strings.TrimSpace(asString(metadata["planner_scope_resolution"])); resolution == "contradicted" {
		lines = append(lines, "Planner selected paths contradicted by review: previous shortlist lost priority for this attempt")
	} else if resolution == "confirmed" {
		if suspected := anyStringSliceDefault(metadata["suspected_paths"], []string{}); len(suspected) > 0 {
			lines = append(lines, "Planner confirmed paths: "+strings.Join(suspected, ", "))
		}
	}
	if resolution := strings.TrimSpace(asString(metadata["planner_rejected_scope_resolution"])); resolution == "confirmed" {
		if excluded := anyStringSliceDefault(metadata["excluded_scope_paths"], []string{}); len(excluded) > 0 {
			lines = append(lines, "Planner rejected paths still excluded: "+strings.Join(excluded, ", "))
		}
	} else if resolution == "contradicted" {
		if suspected := anyStringSliceDefault(metadata["suspected_paths"], []string{}); len(suspected) > 0 {
			lines = append(lines, "Planner rejected but observed in diff: "+strings.Join(suspected, ", "))
		}
	}
	if excluded := anyStringSliceDefault(metadata["excluded_scope_paths"], []string{}); len(excluded) > 0 {
		lines = append(lines, "Excluded paths: "+strings.Join(excluded, ", "))
	}
	if precedents := anyMapSliceDefault(metadata["objective_retrieval_precedents"], []map[string]any{}); len(precedents) > 0 {
		lines = append(lines, "", "Retrieved precedents:")
		for _, item := range precedents {
			sourceRef := strings.TrimSpace(asString(item["source_ref"]))
			summary := strings.TrimSpace(asString(item["summary"]))
			switch {
			case sourceRef != "" && summary != "":
				lines = append(lines, fmt.Sprintf("- %s -> %s", sourceRef, summary))
			case summary != "":
				lines = append(lines, "- "+summary)
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

func mergeToolRequestWithMetadata(toolRequest, metadata map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range toolRequest {
		out[key] = value
	}
	for _, key := range []string{
		"initial_scope_hypotheses",
		"rejected_scope_hypotheses",
		"objective_patch_intent",
		"objective_baseline_test_results",
		"objective_baseline_findings",
		"objective_baseline_summary",
		"objective_failure_hypotheses",
		"objective_analysis_findings",
		"objective_analysis_summary",
		"excluded_scope_paths",
		"scope_correction_reason",
		"planner_scope_resolution",
		"planner_rejected_scope_resolution",
		"context_package",
		"retrieval_packet_artifact_id",
		"objective_retrieval_precedents",
		"suspected_paths",
		"review_comments",
		"review_decision",
	} {
		if _, exists := out[key]; exists {
			continue
		}
		if value, ok := metadata[key]; ok {
			out[key] = value
		}
	}
	return out
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
