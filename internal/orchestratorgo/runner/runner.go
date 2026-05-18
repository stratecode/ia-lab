package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/planner"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
)

type ApprovalRequest struct {
	ActionType     string
	TargetResource string
	TimeoutSeconds int
}

type Result struct {
	Status       string
	Summary      string
	Results      map[string]any
	ErrorMessage string
	Approval     *ApprovalRequest
}

type TaskRunner struct {
	cfg      config.Config
	planner  *planner.Service
	research *research.Service
}

func New(cfg config.Config, researchService *research.Service) *TaskRunner {
	return &TaskRunner{
		cfg:      cfg,
		planner:  planner.New(cfg.LlamaPlannerBaseURL, cfg.LlamaPlannerAPIKey, cfg.LlamaTimeoutSeconds),
		research: researchService,
	}
}

func (r *TaskRunner) Execute(task *domain.TaskResponse) Result {
	if task == nil {
		return Result{Status: "error", ErrorMessage: "task is nil"}
	}
	switch agent := derefAgent(task.AssignedAgent); agent {
	case domain.AgentTypePlanner:
		return r.executePlanner(task)
	case domain.AgentTypeCoder:
		return r.executeCoder(task)
	default:
		return Result{
			Status:       "error",
			ErrorMessage: fmt.Sprintf("agent type %q is not supported in orchestrator-go runner", agent),
		}
	}
}

func (r *TaskRunner) executePlanner(task *domain.TaskResponse) Result {
	metadata := cloneMap(task.Metadata)
	if asBool(metadata["research_mode"]) && r.research != nil {
		queryResult, err := r.research.Query(context.Background(), task.Description)
		if err == nil {
			metadata["capability_context"] = []string{queryResult.Answer}
			metadata["research_intent"] = string(queryResult.Intent)
		}
	}
	if r.planner != nil {
		result, err := r.planner.CreatePlan(context.Background(), task.Description, metadata)
		if err == nil {
			planOnly := asBool(task.Metadata["plan_only"])
			return Result{
				Status:  "success",
				Summary: "planner completed",
				Results: map[string]any{
					"status":        "success",
					"plan_summary":  result.Summary,
					"plan_markdown": result.Plan,
					"subtasks":      result.Subtasks,
					"raw_response":  result.Raw,
					"plan_only":     planOnly,
				},
			}
		}
		return Result{Status: "error", Summary: "planner failed", ErrorMessage: err.Error()}
	}

	return Result{
		Status:  "success",
		Summary: "planner completed in fallback mode",
		Results: map[string]any{
			"status":        "success",
			"plan_summary":  fmt.Sprintf("Fallback plan generated for task %s", task.ID),
			"plan_markdown": "# Execution plan\n\n1. Scope\n2. Prepare\n3. Deliver",
			"subtasks":      []map[string]any{},
			"plan_only":     asBool(task.Metadata["plan_only"]),
		},
	}
}

func (r *TaskRunner) executeCoder(task *domain.TaskResponse) Result {
	metadata := task.Metadata
	requiresApproval := asBool(metadata["requires_approval"])
	approvalRequested := asBool(metadata["approval_requested"])
	toolRequest, _ := metadata["tool_request"].(map[string]any)

	if requiresApproval && !approvalRequested {
		target := "coder-task"
		if path := strings.TrimSpace(asString(toolRequest["path"])); path != "" {
			target = path
		}
		return Result{
			Status:  "waiting_approval",
			Summary: "approval required before executing coder task",
			Approval: &ApprovalRequest{
				ActionType:     "coder_execution",
				TargetResource: target,
				TimeoutSeconds: 300,
			},
		}
	}

	workspaceRoot := strings.TrimSpace(derefString(task.WorkspacePath))
	if workspaceRoot == "" {
		return Result{Status: "error", ErrorMessage: "workspace_path is required for coder execution"}
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return Result{Status: "error", ErrorMessage: err.Error()}
	}

	if toolRequest != nil {
		if result, handled := r.executeToolRequest(workspaceRoot, toolRequest); handled {
			return result
		}
	}

	if repo := strings.TrimSpace(firstNonEmptyMetadata(task.Metadata, "repo_name", "repo_path", "repository_path")); repo != "" {
		return r.executeAiderTask(task, repo)
	}

	defaultFile := filepath.Join(workspaceRoot, "task-result.txt")
	content := fmt.Sprintf("task_id=%s\nagent=coder\ndescription=%s\ncompleted_at=%s\n", task.ID, task.Description, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(defaultFile, []byte(content), 0o644); err != nil {
		return Result{Status: "error", ErrorMessage: err.Error()}
	}
	return Result{
		Status:  "success",
		Summary: "coder wrote execution report",
		Results: map[string]any{
			"status":        "success",
			"summary":       "coder wrote execution report",
			"changed_files": []string{"task-result.txt"},
			"artifacts": []map[string]any{
				{"type": "file", "path": defaultFile},
			},
		},
	}
}

func (r *TaskRunner) executeAiderTask(task *domain.TaskResponse, repo string) Result {
	branch := strings.TrimSpace(firstNonEmptyMetadata(task.Metadata, "branch", "default_branch"))
	if branch == "" {
		branch = r.cfg.DefaultGitBranch
	}
	timeoutSeconds := r.cfg.AiderTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 1800
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	aiderPath := strings.TrimSpace(r.cfg.AiderTaskPath)
	if aiderPath == "" {
		aiderPath = "/usr/local/bin/aider-task"
	}
	cmd := exec.CommandContext(
		ctx,
		"sudo", "-u", "aider", "--",
		aiderPath,
		"--task-id", task.ID,
		"--repo", repo,
		"--branch", branch,
		"--prompt", task.Description,
	)
	output, err := cmd.CombinedOutput()
	stdout := strings.TrimSpace(string(output))
	if ctx.Err() == context.DeadlineExceeded {
		return Result{
			Status:       "error",
			Summary:      "coder timed out",
			ErrorMessage: "aider-task exceeded timeout",
			Results: map[string]any{
				"status": "error",
				"stdout": truncateOutput(stdout, 64000),
			},
		}
	}
	if err != nil {
		return Result{
			Status:       "error",
			Summary:      "coder execution failed",
			ErrorMessage: err.Error(),
			Results: map[string]any{
				"status": "error",
				"stdout": truncateOutput(stdout, 64000),
			},
		}
	}
	return Result{
		Status:  "success",
		Summary: "coder executed aider-task",
		Results: map[string]any{
			"status":        "success",
			"summary":       "coder executed aider-task",
			"stdout":        truncateOutput(stdout, 64000),
			"changed_files": []string{},
		},
	}
}

func (r *TaskRunner) executeToolRequest(workspaceRoot string, request map[string]any) (Result, bool) {
	tool := strings.TrimSpace(asString(request["tool"]))
	switch tool {
	case "write_file":
		rawPath := strings.TrimSpace(asString(request["path"]))
		if rawPath == "" {
			return Result{Status: "error", ErrorMessage: "tool_request.path is required for write_file"}, true
		}
		content := asString(request["content"])
		path, err := safeResolve(workspaceRoot, rawPath)
		if err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		return Result{
			Status:  "success",
			Summary: "coder wrote file",
			Results: map[string]any{
				"status":        "success",
				"summary":       fmt.Sprintf("Wrote %s", rawPath),
				"changed_files": []string{rawPath},
				"artifacts": []map[string]any{
					{"type": "file", "path": path},
				},
			},
		}, true
	case "append_file":
		rawPath := strings.TrimSpace(asString(request["path"]))
		if rawPath == "" {
			return Result{Status: "error", ErrorMessage: "tool_request.path is required for append_file"}, true
		}
		content := asString(request["content"])
		path, err := safeResolve(workspaceRoot, rawPath)
		if err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		defer file.Close()
		if _, err := file.WriteString(content); err != nil {
			return Result{Status: "error", ErrorMessage: err.Error()}, true
		}
		return Result{
			Status:  "success",
			Summary: "coder appended file",
			Results: map[string]any{
				"status":        "success",
				"summary":       fmt.Sprintf("Appended %s", rawPath),
				"changed_files": []string{rawPath},
				"artifacts": []map[string]any{
					{"type": "file", "path": path},
				},
			},
		}, true
	default:
		return Result{}, false
	}
}

func safeResolve(workspaceRoot, rawPath string) (string, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", err
	}
	candidate, err := filepath.Abs(filepath.Join(root, rawPath))
	if err != nil {
		return "", err
	}
	if candidate != root && !strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes workspace: %s", rawPath)
	}
	return candidate, nil
}

func asString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		if value == nil {
			return ""
		}
		raw, _ := json.Marshal(v)
		if string(raw) == "null" {
			return ""
		}
		return strings.Trim(string(raw), "\"")
	}
}

func asBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func derefAgent(agent *domain.AgentType) domain.AgentType {
	if agent == nil {
		return ""
	}
	return *agent
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func firstNonEmptyMetadata(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(asString(metadata[key])); value != "" {
			return value
		}
	}
	return ""
}

func truncateOutput(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "\n... [output truncated]"
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
