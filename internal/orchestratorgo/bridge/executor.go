package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

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
	tool := strings.TrimSpace(asString(toolRequest["tool"]))
	if tool == "" {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "tool_request.tool is required"}
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

	switch tool {
	case "read_file":
		return e.readFile(toolRequest)
	case "list_files":
		return e.listFiles(toolRequest)
	case "write_file":
		return e.writeFile(toolRequest)
	case "research_project":
		return e.researchProject(toolRequest)
	case "scaffold_project":
		return e.scaffoldProject(toolRequest)
	case "review_project":
		return e.reviewProject(ctx, toolRequest)
	case "apply_patch":
		return e.applyPatch(ctx, toolRequest)
	case "run_command":
		return e.runCommand(ctx, toolRequest)
	case "git_status":
		return e.runSubprocess(ctx, []string{"git", "status", "--short"}, "git status")
	case "git_diff":
		return e.runSubprocess(ctx, []string{"git", "diff", "--no-ext-diff"}, "git diff")
	case "run_tests":
		argv, ok := anyStringSlice(toolRequest["argv"])
		if !ok || len(argv) == 0 {
			argv = []string{"pytest", "-q"}
		}
		return e.runAllowedCommand(ctx, argv, "Ran tests")
	default:
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: fmt.Sprintf("unsupported local tool: %s", tool)}
	}
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
	return e.withGitArtifacts(domain.LocalBridgeResultRequest{Status: "success", Summary: &summary})
}

func (e *WorkspaceExecutor) researchProject(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	projectRequest, _ := request["project_request"].(map[string]any)
	projectType := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["project_type"])), strings.TrimSpace(asString(request["project_type"])))
	stack := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["runtime_or_stack"])), strings.TrimSpace(asString(request["runtime_or_stack"])))
	goal := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["goal"])), strings.TrimSpace(asString(request["goal"])))
	testFocus := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["test_focus"])), strings.TrimSpace(asString(request["test_focus"])))
	testCommand := anyStringSliceDefault(request["test_command"], defaultBridgeTestCommand(projectType, stack))
	payload := fmt.Sprintf(`{
  "project_type": %q,
  "runtime_or_stack": %q,
  "goal": %q,
  "test_focus": %q,
  "recommendations": [
    "Keep the project tiny and deterministic.",
    "Expose one obvious entrypoint and one obvious validation path.",
    "Prefer a test command with no external network dependency."
  ],
  "checklist": [
    "README present",
    "At least one test file present",
    "Declared test command is runnable"
  ],
  "test_command": %q
}`, projectType, stack, goal, testFocus, strings.Join(testCommand, " "))
	summary := fmt.Sprintf("Researched scaffold constraints for %s/%s", projectType, stack)
	return domain.LocalBridgeResultRequest{
		Status:  "success",
		Summary: &summary,
		Stdout:  &payload,
		Artifacts: []map[string]any{
			{
				"type":         "research_context",
				"title":        "Project research context",
				"media_type":   "application/json",
				"content_text": payload,
			},
		},
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
	report := fmt.Sprintf("expected_files=%s\ntest_command=%s\n", strings.Join(expectedFiles, ","), strings.Join(testCommand, " "))
	return domain.LocalBridgeResultRequest{
		Status:   "success",
		Summary:  &summary,
		Stdout:   stringPtr(stdout),
		Stderr:   stringPtr(stderr),
		ExitCode: intPtr(exitCode),
		Artifacts: []map[string]any{
			{"type": "review_report", "title": "Project review report", "media_type": "text/plain", "content_text": report},
		},
	}, nil
}

func (e *WorkspaceExecutor) applyPatch(ctx context.Context, request map[string]any) (domain.LocalBridgeResultRequest, error) {
	patch := strings.TrimSpace(asString(request["patch"]))
	if patch == "" {
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
	return e.runAllowedCommand(ctx, argv, "Ran command: "+strings.Join(argv, " "))
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
	result.ChangedFiles = changedFiles
	result.Diff = &diff
	return result, nil
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
