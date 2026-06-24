package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/browserverify"
	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/capabilitybroker"
)

func (s *Server) executeCommand(ctx context.Context, argv []string, workdir string, env map[string]string, timeout time.Duration) (CommandExecutionResult, error) {
	if s != nil && s.CommandExecutor != nil {
		return s.CommandExecutor(ctx, argv, workdir, env, timeout)
	}
	if len(argv) == 0 {
		return CommandExecutionResult{}, fmt.Errorf("argv is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = workdir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), allowedEnvPairs(env)...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return CommandExecutionResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, err
}

func allowedEnvPairs(env map[string]string) []string {
	items := make([]string, 0, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		items = append(items, key+"="+value)
	}
	slices.Sort(items)
	return items
}

func (s *Server) ExecuteInternalCapability(ctx context.Context, req capabilitybroker.ExecuteRequest) (*capabilities.Result, error) {
	switch strings.TrimSpace(req.Capability) {
	case "web.search":
		return s.executeWebSearchCapability(ctx, req.Input)
	case "web.fetch":
		return s.executeWebFetchCapability(ctx, req.Input)
	case "document.read":
		return s.executeDocumentReadCapability(ctx, req.Input)
	case "image.analyze":
		return s.executeImageAnalyzeCapability(ctx, req.Input)
	case "code.analysis":
		return s.executeCodeAnalyzeCapability(ctx, req.Input)
	case "filesystem.read", "filesystem.list", "filesystem.write":
		return s.executeFilesystemCapability(ctx, req.Capability, req.Input)
	case "research.query":
		return s.executeResearchQueryCapability(ctx, req.Input)
	case "shell.exec":
		return s.executeShellExecCapability(ctx, req.Input)
	case "git.status", "git.diff", "git.log", "git.branch":
		return s.executeGitCapability(ctx, req.Capability, req.Input)
	case "docker.compose_up", "docker.compose_down", "docker.logs", "docker.ps", "docker.build", "docker.run":
		return s.executeDockerCapability(ctx, req.Capability, req.Input)
	case "http.check":
		return s.executeHTTPCheckCapability(ctx, req.Input)
	case "browser.verify":
		return s.executeBrowserVerifyCapability(ctx, req.Input)
	case "github.read", "jira.read", "slack.read", "slack.notify":
		return s.executeMCPWrapperCapability(ctx, req.Capability, req.Input)
	default:
		return nil, fmt.Errorf("unsupported capability %s", req.Capability)
	}
}

func (s *Server) executeShellExecCapability(ctx context.Context, input map[string]any) (*capabilities.Result, error) {
	argv, err := argvFromInput(input["argv"])
	if err != nil {
		return nil, err
	}
	workdirValue := firstNonEmptyString(strings.TrimSpace(asString(input["working_directory"])), strings.TrimSpace(asString(input["path"])))
	if workdirValue == "" {
		return nil, fmt.Errorf("working_directory is required")
	}
	resolved, rel, err := resolveAllowedLocalPath(workdirValue, s.Config.AllowedLocalRoots)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(objectiveAsInt(input["timeout_seconds"], 30)) * time.Second
	result, runErr := s.executeCommand(ctx, argv, resolved, mapStringString(input["environment"]), timeout)
	output := map[string]any{
		"path":      rel,
		"argv":      argv,
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
	}
	status := "success"
	summary := fmt.Sprintf("Executed %s in %s", strings.Join(argv, " "), rel)
	errMessage := ""
	if runErr != nil {
		status = "error"
		errMessage = firstNonEmptyString(strings.TrimSpace(result.Stderr), strings.TrimSpace(runErr.Error()))
		summary = fmt.Sprintf("Command failed in %s", rel)
	}
	return &capabilities.Result{
		Status:       status,
		Summary:      summary,
		Output:       output,
		ErrorMessage: errMessage,
		Artifacts: []capabilities.Artifact{{
			ArtifactType: "shell_exec",
			Title:        "Shell execution result",
			MediaType:    "application/json",
			ContentText:  truncateForAPI(mustJSON(output), 4000),
			Metadata:     map[string]any{"path": rel},
		}},
	}, nil
}

func (s *Server) executeGitCapability(ctx context.Context, capabilityName string, input map[string]any) (*capabilities.Result, error) {
	repoPath := firstNonEmptyString(strings.TrimSpace(asString(input["repository_path"])), strings.TrimSpace(asString(input["path"])))
	if repoPath == "" {
		return nil, fmt.Errorf("repository_path is required")
	}
	resolved, rel, err := resolveAllowedLocalPath(repoPath, s.Config.AllowedLocalRoots)
	if err != nil {
		return nil, err
	}
	var argv []string
	switch strings.TrimSpace(capabilityName) {
	case "git.status":
		argv = []string{"git", "status", "--short", "--untracked-files=all"}
	case "git.diff":
		argv = []string{"git", "diff", "--no-ext-diff"}
	case "git.log":
		argv = []string{"git", "log", "--oneline", "-n", fmt.Sprint(objectiveAsInt(input["limit"], 10))}
	case "git.branch":
		name := strings.TrimSpace(asString(input["name"]))
		if name == "" {
			argv = []string{"git", "branch", "--list"}
		} else {
			argv = []string{"git", "branch", name}
		}
	default:
		return nil, fmt.Errorf("unsupported git capability %s", capabilityName)
	}
	execResult, runErr := s.executeCommand(ctx, argv, resolved, nil, 20*time.Second)
	output := map[string]any{
		"path":      rel,
		"argv":      argv,
		"stdout":    execResult.Stdout,
		"stderr":    execResult.Stderr,
		"exit_code": execResult.ExitCode,
	}
	status := "success"
	summary := fmt.Sprintf("Executed %s on %s", capabilityName, rel)
	errMessage := ""
	if runErr != nil {
		status = "error"
		errMessage = firstNonEmptyString(strings.TrimSpace(execResult.Stderr), strings.TrimSpace(runErr.Error()))
		summary = fmt.Sprintf("%s failed on %s", capabilityName, rel)
	}
	return &capabilities.Result{
		Status:       status,
		Summary:      summary,
		Output:       output,
		ErrorMessage: errMessage,
		Artifacts: []capabilities.Artifact{{
			ArtifactType: "git_" + strings.ReplaceAll(strings.TrimPrefix(capabilityName, "git."), ".", "_"),
			Title:        "Git capability result",
			MediaType:    "application/json",
			ContentText:  truncateForAPI(mustJSON(output), 4000),
			Metadata:     map[string]any{"path": rel},
		}},
	}, nil
}

func (s *Server) executeDockerCapability(ctx context.Context, capabilityName string, input map[string]any) (*capabilities.Result, error) {
	var argv []string
	switch strings.TrimSpace(capabilityName) {
	case "docker.compose_up":
		composeFile, workdir, rel, err := s.dockerComposeContext(input)
		if err != nil {
			return nil, err
		}
		argv = []string{"docker", "compose", "-f", composeFile, "up", "-d"}
		return s.runDockerCommand(ctx, argv, workdir, rel)
	case "docker.compose_down":
		composeFile, workdir, rel, err := s.dockerComposeContext(input)
		if err != nil {
			return nil, err
		}
		argv = []string{"docker", "compose", "-f", composeFile, "down"}
		return s.runDockerCommand(ctx, argv, workdir, rel)
	case "docker.logs":
		workdir, rel, err := s.allowedWorkdir(input)
		if err != nil {
			return nil, err
		}
		service := strings.TrimSpace(asString(input["service"]))
		if service == "" {
			return nil, fmt.Errorf("service is required")
		}
		argv = []string{"docker", "logs", service}
		return s.runDockerCommand(ctx, argv, workdir, rel)
	case "docker.ps":
		workdir, rel, err := s.allowedWorkdir(input)
		if err != nil {
			return nil, err
		}
		argv = []string{"docker", "ps", "--format", "{{.ID}} {{.Image}} {{.Status}} {{.Names}}"}
		return s.runDockerCommand(ctx, argv, workdir, rel)
	case "docker.build":
		workdir, rel, err := s.allowedWorkdir(input)
		if err != nil {
			return nil, err
		}
		dockerfile := strings.TrimSpace(asString(input["dockerfile"]))
		if dockerfile != "" {
			resolvedDockerfile, _, err := resolveAllowedLocalPath(dockerfile, s.Config.AllowedLocalRoots)
			if err != nil {
				return nil, err
			}
			argv = []string{"docker", "build", "-f", resolvedDockerfile, workdir}
		} else {
			argv = []string{"docker", "build", workdir}
		}
		return s.runDockerCommand(ctx, argv, workdir, rel)
	case "docker.run":
		workdir, rel, err := s.allowedWorkdir(input)
		if err != nil {
			return nil, err
		}
		image := strings.TrimSpace(asString(input["image"]))
		if image == "" {
			return nil, fmt.Errorf("image is required")
		}
		args, err := argvFromInput(input["argv"])
		if err != nil {
			args = nil
		}
		argv = append([]string{"docker", "run", "--rm", image}, args...)
		return s.runDockerCommand(ctx, argv, workdir, rel)
	default:
		return nil, fmt.Errorf("unsupported docker capability %s", capabilityName)
	}
}

func (s *Server) runDockerCommand(ctx context.Context, argv []string, workdir, rel string) (*capabilities.Result, error) {
	execResult, runErr := s.executeCommand(ctx, argv, workdir, nil, 60*time.Second)
	output := map[string]any{
		"path":      rel,
		"argv":      argv,
		"stdout":    execResult.Stdout,
		"stderr":    execResult.Stderr,
		"exit_code": execResult.ExitCode,
	}
	status := "success"
	summary := fmt.Sprintf("Executed docker command in %s", rel)
	errMessage := ""
	if runErr != nil {
		status = "error"
		errMessage = firstNonEmptyString(strings.TrimSpace(execResult.Stderr), strings.TrimSpace(runErr.Error()))
		summary = fmt.Sprintf("Docker command failed in %s", rel)
	}
	return &capabilities.Result{
		Status:       status,
		Summary:      summary,
		Output:       output,
		ErrorMessage: errMessage,
		Artifacts: []capabilities.Artifact{{
			ArtifactType: "docker_command",
			Title:        "Docker capability result",
			MediaType:    "application/json",
			ContentText:  truncateForAPI(mustJSON(output), 4000),
			Metadata:     map[string]any{"path": rel},
		}},
	}, nil
}

func (s *Server) dockerComposeContext(input map[string]any) (string, string, string, error) {
	composeFile := strings.TrimSpace(asString(input["compose_file"]))
	if composeFile == "" {
		return "", "", "", fmt.Errorf("compose_file is required")
	}
	resolvedCompose, _, err := resolveAllowedLocalPath(composeFile, s.Config.AllowedLocalRoots)
	if err != nil {
		return "", "", "", err
	}
	workdir := filepath.Dir(resolvedCompose)
	_, rel, err := resolveAllowedLocalPath(workdir, s.Config.AllowedLocalRoots)
	if err != nil {
		return "", "", "", err
	}
	return resolvedCompose, workdir, rel, nil
}

func (s *Server) allowedWorkdir(input map[string]any) (string, string, error) {
	workdirValue := firstNonEmptyString(strings.TrimSpace(asString(input["working_directory"])), strings.TrimSpace(asString(input["path"])))
	if workdirValue == "" {
		return "", "", fmt.Errorf("working_directory is required")
	}
	return resolveAllowedLocalPath(workdirValue, s.Config.AllowedLocalRoots)
}

func (s *Server) executeHTTPCheckCapability(ctx context.Context, input map[string]any) (*capabilities.Result, error) {
	rawURL := strings.TrimSpace(asString(input["url"]))
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(s.Config.AllowedURLSchemes, parsed.Scheme) {
		return nil, fmt.Errorf("url scheme %s is not allowed", parsed.Scheme)
	}
	method := firstNonEmptyString(strings.ToUpper(strings.TrimSpace(asString(input["method"]))), http.MethodGet)
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if err != nil {
		return nil, err
	}
	expectedStatus := objectiveAsInt(input["expected_status"], 200)
	if expectedStatus > 0 && resp.StatusCode != expectedStatus {
		return &capabilities.Result{
			Status:       "error",
			Summary:      fmt.Sprintf("HTTP check failed for %s", rawURL),
			Output:       map[string]any{"url": rawURL, "method": method, "status_code": resp.StatusCode, "body": string(body)},
			ErrorMessage: fmt.Sprintf("unexpected status %d", resp.StatusCode),
		}, nil
	}
	contains := strings.TrimSpace(asString(input["contains"]))
	if contains != "" && !strings.Contains(string(body), contains) {
		return &capabilities.Result{
			Status:       "error",
			Summary:      fmt.Sprintf("HTTP body assertion failed for %s", rawURL),
			Output:       map[string]any{"url": rawURL, "method": method, "status_code": resp.StatusCode, "body": string(body)},
			ErrorMessage: fmt.Sprintf("response does not contain %q", contains),
		}, nil
	}
	regexPattern := strings.TrimSpace(asString(input["matches_regex"]))
	if regexPattern != "" {
		matched, err := regexp.MatchString(regexPattern, string(body))
		if err != nil {
			return nil, err
		}
		if !matched {
			return &capabilities.Result{
				Status:       "error",
				Summary:      fmt.Sprintf("HTTP regex assertion failed for %s", rawURL),
				Output:       map[string]any{"url": rawURL, "method": method, "status_code": resp.StatusCode, "body": string(body)},
				ErrorMessage: fmt.Sprintf("response does not match regex %q", regexPattern),
			}, nil
		}
	}
	output := map[string]any{
		"url":          rawURL,
		"method":       method,
		"status_code":  resp.StatusCode,
		"body":         string(body),
		"latency_ms":   time.Since(started).Milliseconds(),
		"content_type": resp.Header.Get("Content-Type"),
	}
	return &capabilities.Result{
		Status:  "success",
		Summary: fmt.Sprintf("HTTP check passed for %s", rawURL),
		Output:  output,
		SourceRefs: []capabilities.SourceRef{{
			Title: rawURL,
			URI:   rawURL,
			Kind:  "http",
		}},
		Artifacts: []capabilities.Artifact{{
			ArtifactType: "http_check",
			Title:        "HTTP check result",
			URI:          rawURL,
			MediaType:    resp.Header.Get("Content-Type"),
			ContentText:  truncateForAPI(mustJSON(output), 4000),
		}},
	}, nil
}

func (s *Server) executeBrowserVerifyCapability(ctx context.Context, input map[string]any) (*capabilities.Result, error) {
	if s == nil || s.BrowserVerifier == nil {
		return nil, fmt.Errorf("browser verifier is not configured")
	}
	rawURL := strings.TrimSpace(asString(input["url"]))
	if rawURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(s.Config.AllowedURLSchemes, parsed.Scheme) {
		return nil, fmt.Errorf("url scheme %s is not allowed", parsed.Scheme)
	}
	return s.BrowserVerifier.Verify(ctx, browserverify.Request{
		URL:            rawURL,
		WaitFor:        strings.TrimSpace(asString(input["wait_for"])),
		AssertText:     strings.TrimSpace(asString(input["assert_text"])),
		Click:          strings.TrimSpace(asString(input["click"])),
		Fill:           mapAny(input["fill"]),
		Screenshot:     objectiveAsBool(input["screenshot"]),
		ConsoleErrors:  objectiveAsBool(input["console_errors"]),
		TimeoutSeconds: objectiveAsInt(input["timeout_seconds"], 20),
	})
}

func (s *Server) executeMCPWrapperCapability(ctx context.Context, capabilityName string, input map[string]any) (*capabilities.Result, error) {
	if s == nil || s.MCPWrappers == nil {
		return nil, fmt.Errorf("mcp wrappers are not configured")
	}
	return s.MCPWrappers.Execute(ctx, capabilityName, input)
}

func (s *Server) shellExec(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "shell.exec")
}

func (s *Server) gitTool(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	operation := strings.TrimSpace(asString(body["operation"]))
	capability := "git." + operation
	s.executeDecodedCapabilityToolEndpoint(w, r, capability, body)
}

func (s *Server) dockerTool(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	operation := strings.TrimSpace(asString(body["operation"]))
	capability := "docker." + operation
	s.executeDecodedCapabilityToolEndpoint(w, r, capability, body)
}

func (s *Server) httpCheckTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "http.check")
}

func (s *Server) browserVerifyTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "browser.verify")
}

func (s *Server) githubReadTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "github.read")
}

func (s *Server) jiraReadTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "jira.read")
}

func (s *Server) slackReadTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "slack.read")
}

func (s *Server) slackNotifyTool(w http.ResponseWriter, r *http.Request) {
	s.executeCapabilityToolEndpoint(w, r, "slack.notify")
}

func (s *Server) executeCapabilityToolEndpoint(w http.ResponseWriter, r *http.Request, capability string) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	s.executeDecodedCapabilityToolEndpoint(w, r, capability, body)
}

func (s *Server) executeDecodedCapabilityToolEndpoint(w http.ResponseWriter, r *http.Request, capability string, body map[string]any) {
	taskID := normalizedStringPtr(stringPtr(strings.TrimSpace(asString(body["task_id"]))))
	result, err := s.ExecuteInternalCapability(r.Context(), capabilitybroker.ExecuteRequest{
		Capability: capability,
		Input:      body,
	})
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	response, err := s.persistCapabilityExecution(r.Context(), "api", taskID, capability, body, result)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func argvFromInput(value any) ([]string, error) {
	items := asStringSlice(value)
	if len(items) == 0 {
		return nil, fmt.Errorf("argv is required")
	}
	return items, nil
}

func mapStringString(value any) map[string]string {
	input, _ := value.(map[string]any)
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, item := range input {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = strings.TrimSpace(asString(item))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapAny(value any) map[string]any {
	input, _ := value.(map[string]any)
	if len(input) == 0 {
		return nil
	}
	return input
}
