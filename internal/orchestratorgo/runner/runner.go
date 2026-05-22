package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	cfg            config.Config
	planner        *planner.Service
	research       *research.Service
	contextBuilder ContextBuilder
}

type ContextBuilder interface {
	BuildForTask(ctx context.Context, task *domain.TaskResponse) (*domain.ContextPackage, error)
}

func New(cfg config.Config, researchService *research.Service, contextBuilder ...ContextBuilder) *TaskRunner {
	var builder ContextBuilder
	if len(contextBuilder) > 0 {
		builder = contextBuilder[0]
	}
	return &TaskRunner{
		cfg:            cfg,
		planner:        planner.New(cfg.LlamaPlannerBaseURL, cfg.LlamaPlannerAPIKey, cfg.LlamaTimeoutSeconds),
		research:       researchService,
		contextBuilder: builder,
	}
}

func (r *TaskRunner) Execute(task *domain.TaskResponse) Result {
	if task == nil {
		return Result{Status: "error", ErrorMessage: "task is nil"}
	}
	task = r.withContextPackage(task)
	var result Result
	switch agent := derefAgent(task.AssignedAgent); agent {
	case domain.AgentTypePlanner:
		result = r.executePlanner(task)
	case domain.AgentTypeResearcher:
		result = r.executeResearcher(task)
	case domain.AgentTypeCoder:
		result = r.executeCoder(task)
	case domain.AgentTypeReviewer:
		result = r.executeReviewer(task)
	default:
		result = Result{
			Status:       "error",
			ErrorMessage: fmt.Sprintf("agent type %q is not supported in orchestrator-go runner", agent),
		}
	}
	return attachContextRefs(result, task)
}

func (r *TaskRunner) withContextPackage(task *domain.TaskResponse) *domain.TaskResponse {
	if r.contextBuilder == nil || task == nil {
		return task
	}
	cloned := *task
	cloned.Metadata = cloneMap(task.Metadata)
	pkg, err := r.contextBuilder.BuildForTask(context.Background(), &cloned)
	if err != nil {
		cloned.Metadata["semantic_context_error"] = err.Error()
		return &cloned
	}
	if pkg == nil || len(pkg.Chunks) == 0 {
		return &cloned
	}
	cloned.Metadata["context_package"] = pkg
	if pkg.PromptSection != "" {
		existing := anyStringSliceDefault(cloned.Metadata["capability_context"], []string{})
		existing = append(existing, pkg.PromptSection)
		cloned.Metadata["capability_context"] = existing
	}
	return &cloned
}

func attachContextRefs(result Result, task *domain.TaskResponse) Result {
	pkg, ok := task.Metadata["context_package"].(*domain.ContextPackage)
	if !ok || pkg == nil {
		return result
	}
	if result.Results == nil {
		result.Results = map[string]any{}
	}
	result.Results["semantic_context_sources"] = pkg.SourceRefs
	result.Results["semantic_context_chunk_count"] = len(pkg.Chunks)
	return result
}

func reviewerSemanticMemory(metadata map[string]any) ([]string, []string, []string) {
	pkg, ok := metadata["context_package"].(*domain.ContextPackage)
	if !ok || pkg == nil || pkg.OperationalIR == nil {
		return []string{}, []string{}, []string{}
	}
	rules := append([]string{}, pkg.OperationalIR.ValidationRules...)
	checks := make([]string, 0, len(pkg.OperationalIR.Invalid))
	for _, item := range pkg.OperationalIR.Invalid {
		if strings.TrimSpace(item.FailureReason) == "" {
			continue
		}
		checks = append(checks, fmt.Sprintf("%s: %s", item.SourceRef, item.FailureReason))
	}
	return append([]string{}, pkg.SourceRefs...), rules, checks
}

func (r *TaskRunner) executePlanner(task *domain.TaskResponse) Result {
	metadata := cloneMap(task.Metadata)
	if repoWorkflow, ok := buildRepoWorkflowPlan(metadata); ok {
		return repoWorkflow
	}
	if boilerplate, ok := buildBoilerplatePlan(task, metadata); ok {
		return boilerplate
	}
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

func (r *TaskRunner) executeResearcher(task *domain.TaskResponse) Result {
	projectRequest, _ := task.Metadata["project_request"].(map[string]any)
	if projectRequest == nil {
		if r.research != nil {
			queryResult, err := r.research.Query(context.Background(), task.Description)
			if err == nil {
				return Result{
					Status:  "success",
					Summary: "researcher completed",
					Results: map[string]any{
						"status":          "success",
						"intent":          string(queryResult.Intent),
						"recommendations": []string{queryResult.Answer},
						"sources":         queryResult.Sources,
						"confidence":      queryResult.Confidence,
					},
				}
			}
		}
		return Result{
			Status:  "success",
			Summary: "researcher produced fallback context",
			Results: map[string]any{
				"status": "success",
				"recommendations": []string{
					"Keep the project intentionally small and deterministic.",
					"Prefer a runnable local test command with zero or near-zero external dependencies.",
				},
			},
		}
	}

	projectType := firstNonEmptyMetadata(projectRequest, "project_type")
	stack := firstNonEmptyMetadata(projectRequest, "runtime_or_stack")
	goal := firstNonEmptyMetadata(projectRequest, "goal")
	testFocus := firstNonEmptyMetadata(projectRequest, "test_focus")
	testCommand := defaultProjectTestCommand(projectType, stack)
	return Result{
		Status:  "success",
		Summary: "researcher prepared project context",
		Results: map[string]any{
			"status":           "success",
			"project_type":     projectType,
			"runtime_or_stack": stack,
			"recommendations": []string{
				fmt.Sprintf("Keep %s minimal and directly testable.", projectType),
				fmt.Sprintf("Bias toward %s for runtime consistency.", stack),
				"Expose exactly one obvious success path for the reviewer.",
			},
			"stack_rationale": []string{
				fmt.Sprintf("Goal: %s", goal),
				fmt.Sprintf("Primary test focus: %s", testFocus),
				fmt.Sprintf("Suggested test command: %s", strings.Join(testCommand, " ")),
			},
			"checklist": []string{
				"Project directory exists",
				"README explains purpose",
				"At least one runnable test file exists",
				"Test command is deterministic",
			},
			"test_command": testCommand,
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

func (r *TaskRunner) executeReviewer(task *domain.TaskResponse) Result {
	metadata := cloneMap(task.Metadata)
	semanticSources, semanticRules, priorFailureChecks := reviewerSemanticMemory(metadata)
	projectRequest, _ := metadata["project_request"].(map[string]any)
	workspaceRoot := strings.TrimSpace(firstNonEmptyMetadata(metadata, "workspace_root"))
	if projectRequest == nil || workspaceRoot == "" {
		return Result{Status: "error", ErrorMessage: "reviewer requires metadata.project_request and metadata.workspace_root"}
	}
	parentDirectory := strings.TrimSpace(asString(projectRequest["parent_directory"]))
	projectName := sanitizeProjectName(asString(projectRequest["project_name"]))
	projectRootInput := filepath.Join(parentDirectory, projectName)
	if filepath.IsAbs(parentDirectory) {
		projectRootInput = parentDirectory
		if filepath.Base(parentDirectory) != projectName {
			projectRootInput = filepath.Join(parentDirectory, projectName)
		}
	}
	projectRoot, err := safeResolve(workspaceRoot, projectRootInput)
	if err != nil {
		return Result{Status: "error", ErrorMessage: err.Error()}
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		return Result{Status: "error", ErrorMessage: fmt.Sprintf("project directory missing: %s", projectRoot)}
	}
	projectType := firstNonEmptyMetadata(projectRequest, "project_type")
	stack := firstNonEmptyMetadata(projectRequest, "runtime_or_stack")
	expectedFiles := anyStringSliceDefault(projectRequest["expected_files"], expectedProjectFiles(projectType))
	missing := make([]string, 0)
	for _, rel := range expectedFiles {
		if _, err := os.Stat(filepath.Join(projectRoot, rel)); err != nil {
			missing = append(missing, rel)
		}
	}
	testCommand := anyStringSliceDefault(projectRequest["test_command"], defaultProjectTestCommand(projectType, stack))
	testResult := map[string]any{
		"argv":     testCommand,
		"executed": false,
	}
	if len(testCommand) > 0 {
		stdout, stderr, exitCode, cmdErr := runCommandInDir(projectRoot, testCommand)
		testResult["executed"] = true
		testResult["stdout"] = truncateOutput(stdout, 8000)
		testResult["stderr"] = truncateOutput(stderr, 8000)
		testResult["exit_code"] = exitCode
		if cmdErr != nil {
			testResult["error"] = cmdErr.Error()
			return Result{
				Status:  "error",
				Summary: "reviewer detected failing project tests",
				ErrorMessage: firstNonEmptyMetadata(
					map[string]any{
						"stderr": strings.TrimSpace(stderr),
						"stdout": strings.TrimSpace(stdout),
						"error":  cmdErr.Error(),
					},
					"stderr", "stdout", "error",
				),
				Results: map[string]any{
					"status":                    "error",
					"validation_summary":        "Project scaffold failed reviewer checks",
					"missing_files":             missing,
					"test_result":               testResult,
					"semantic_context_sources":  semanticSources,
					"semantic_validation_rules": semanticRules,
					"prior_failure_checks":      priorFailureChecks,
				},
			}
		}
	}
	if len(missing) > 0 {
		return Result{
			Status:       "error",
			Summary:      "reviewer detected missing files",
			ErrorMessage: "project scaffold is incomplete",
			Results: map[string]any{
				"status":                    "error",
				"validation_summary":        "Project scaffold is missing expected files",
				"missing_files":             missing,
				"test_result":               testResult,
				"semantic_context_sources":  semanticSources,
				"semantic_validation_rules": semanticRules,
				"prior_failure_checks":      priorFailureChecks,
			},
		}
	}
	return Result{
		Status:  "success",
		Summary: "reviewer validated scaffolded project",
		Results: map[string]any{
			"status":                    "success",
			"validation_summary":        "Project scaffold passed reviewer checks",
			"project_root":              projectRoot,
			"expected_files":            expectedFiles,
			"test_result":               testResult,
			"semantic_context_sources":  semanticSources,
			"semantic_validation_rules": semanticRules,
			"prior_failure_checks":      priorFailureChecks,
			"pass_checks": []string{
				"project directory exists",
				"expected files present",
				"test command succeeded",
			},
		},
	}
}

func buildBoilerplatePlan(task *domain.TaskResponse, metadata map[string]any) (Result, bool) {
	projectRequest, _ := metadata["project_request"].(map[string]any)
	if projectRequest == nil {
		return Result{}, false
	}
	if repoWorkflowRoot(metadata, projectRequest) != "" {
		return Result{}, false
	}
	projectName := sanitizeProjectName(asString(projectRequest["project_name"]))
	projectType := firstNonEmptyMetadata(projectRequest, "project_type")
	stack := firstNonEmptyMetadata(projectRequest, "runtime_or_stack")
	parentDirectory := firstNonEmptyMetadata(projectRequest, "parent_directory")
	testCommand := defaultProjectTestCommand(projectType, stack)
	projectRequest["project_name"] = projectName
	projectRequest["test_command"] = testCommand
	projectRequest["expected_files"] = expectedProjectFiles(projectType)
	projectRootRel := filepath.ToSlash(filepath.Join(parentDirectory, projectName))
	researcherMetadata := map[string]any{
		"project_request": projectRequest,
		"workspace_root":  firstNonEmptyMetadata(metadata, "workspace_root"),
		"tool_request": map[string]any{
			"tool":            "research_project",
			"project_request": projectRequest,
		},
	}
	coderMetadata := map[string]any{
		"project_request": projectRequest,
		"workspace_root":  firstNonEmptyMetadata(metadata, "workspace_root"),
		"tool_request": map[string]any{
			"tool":             "scaffold_project",
			"project_name":     projectName,
			"parent_directory": parentDirectory,
			"project_type":     projectType,
			"runtime_or_stack": stack,
			"goal":             firstNonEmptyMetadata(projectRequest, "goal"),
			"test_focus":       firstNonEmptyMetadata(projectRequest, "test_focus"),
			"initialize_git":   asBool(projectRequest["initialize_git"]),
			"test_command":     testCommand,
			"expected_files":   expectedProjectFiles(projectType),
			"files_plan": []string{
				"README.md",
				"tests/*",
			},
		},
	}
	reviewerMetadata := map[string]any{
		"project_request": projectRequest,
		"workspace_root":  firstNonEmptyMetadata(metadata, "workspace_root"),
		"tool_request": map[string]any{
			"tool":           "review_project",
			"project_root":   projectRootRel,
			"expected_files": expectedProjectFiles(projectType),
			"test_command":   testCommand,
		},
	}

	subtasks := []map[string]any{
		{
			"title":             "Research scaffold constraints",
			"description":       fmt.Sprintf("Prepare constraints and validation checklist for %s", projectRootRel),
			"assigned_agent":    string(domain.AgentTypeResearcher),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": false,
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          researcherMetadata,
			"tool_request": map[string]any{
				"tool":            "research_project",
				"project_request": projectRequest,
			},
		},
		{
			"title":             "Scaffold project files",
			"description":       fmt.Sprintf("Create boilerplate for %s", projectRootRel),
			"assigned_agent":    string(domain.AgentTypeCoder),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": asBool(metadata["requires_approval"]),
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          coderMetadata,
			"tool_request":      coderMetadata["tool_request"],
		},
		{
			"title":             "Review scaffolded project",
			"description":       fmt.Sprintf("Validate generated project at %s", projectRootRel),
			"assigned_agent":    string(domain.AgentTypeReviewer),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": false,
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          reviewerMetadata,
			"tool_request": map[string]any{
				"tool":           "review_project",
				"project_root":   projectRootRel,
				"expected_files": expectedProjectFiles(projectType),
				"test_command":   testCommand,
			},
		},
	}
	return Result{
		Status:  "success",
		Summary: "planner prepared deterministic multi-agent boilerplate flow",
		Results: map[string]any{
			"status":           "success",
			"plan_summary":     fmt.Sprintf("Create and validate boilerplate project %s with planner, researcher, coder, and reviewer", projectName),
			"plan_markdown":    fmt.Sprintf("1. Research constraints\n2. Scaffold files\n3. Review and test\n\nTarget: `%s`", projectRootRel),
			"subtasks":         subtasks,
			"project_flow":     "boilerplate_v1",
			"requested_agents": []string{"researcher", "coder", "reviewer"},
			"project_root_rel": projectRootRel,
			"test_command":     testCommand,
			"plan_only":        false,
		},
	}, true
}

func buildRepoWorkflowPlan(metadata map[string]any) (Result, bool) {
	projectRequest, _ := metadata["project_request"].(map[string]any)
	workspaceRoot := repoWorkflowRoot(metadata, projectRequest)
	if workspaceRoot == "" {
		return Result{}, false
	}
	profile := detectRunnerRepoWorkflowProfile(workspaceRoot)
	projectRequest = cloneMap(projectRequest)
	projectRequest["project_name"] = profile.repoName
	projectRequest["project_root"] = profile.projectRoot
	projectRequest["project_type"] = profile.projectType
	projectRequest["runtime_or_stack"] = profile.stack
	projectRequest["language"] = profile.language
	projectRequest["framework"] = profile.framework
	projectRequest["problem_domain"] = profile.problemDomain
	projectRequest["error_class"] = profile.errorClass
	projectRequest["fix_pattern"] = profile.fixPattern
	projectRequest["validation_pattern"] = profile.validationPattern
	projectRequest["repository_url"] = profile.repositoryURL
	projectRequest["default_branch"] = profile.defaultBranch
	projectRequest["goal"] = firstNonEmptyMetadata(projectRequest, "goal")
	projectRequest["test_focus"] = profile.testFocus
	projectRequest["test_command"] = profile.testCommand
	projectRequest["expected_files"] = profile.expectedFiles
	projectRequest["repo_profile"] = profile.profileName
	if profile.benchmarkCaseID != "" {
		projectRequest["benchmark_case_id"] = profile.benchmarkCaseID
	}
	if profile.benchmarkCaseType != "" {
		projectRequest["benchmark_case_type"] = profile.benchmarkCaseType
	}
	if profile.benchmarkMemoryMode != "" {
		projectRequest["benchmark_memory_mode"] = profile.benchmarkMemoryMode
	}
	if profile.benchmarkMemoryPolicy != "" {
		projectRequest["benchmark_memory_strategy"] = profile.benchmarkMemoryPolicy
	}

	researcherMetadata := map[string]any{
		"project_request": projectRequest,
		"workspace_root":  workspaceRoot,
		"tool_request": map[string]any{
			"tool":            "research_project",
			"project_request": projectRequest,
			"project_root":    profile.projectRoot,
			"goal":            firstNonEmptyMetadata(projectRequest, "goal"),
			"test_command":    profile.testCommand,
		},
	}
	applyRunnerBenchmarkMetadata(researcherMetadata, profile)
	coderToolRequest := map[string]any{
		"tool":              profile.coderTool,
		"project_root":      profile.projectRoot,
		"requires_approval": true,
	}
	if profile.patchTarget != "" {
		coderToolRequest["path"] = profile.patchTarget
	}
	if profile.patch != "" {
		coderToolRequest["patch"] = profile.patch
	}
	if profile.writeContent != "" {
		coderToolRequest["content"] = profile.writeContent
	}
	coderMetadata := map[string]any{
		"project_request":   projectRequest,
		"workspace_root":    workspaceRoot,
		"requires_approval": true,
		"repo_workflow":     "repo_workflow_v1",
		"tool_request":      coderToolRequest,
	}
	applyRunnerBenchmarkMetadata(coderMetadata, profile)
	reviewerMetadata := map[string]any{
		"project_request": projectRequest,
		"workspace_root":  workspaceRoot,
		"tool_request": map[string]any{
			"tool":           "review_project",
			"project_root":   profile.projectRoot,
			"expected_files": profile.expectedFiles,
			"test_command":   profile.testCommand,
		},
	}
	applyRunnerBenchmarkMetadata(reviewerMetadata, profile)

	subtasks := []map[string]any{
		{
			"title":             "Research repository constraints",
			"description":       fmt.Sprintf("Inspect the repository at %s and prepare execution constraints.", workspaceRoot),
			"assigned_agent":    string(domain.AgentTypeResearcher),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": false,
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          researcherMetadata,
			"tool_request":      researcherMetadata["tool_request"],
		},
		{
			"title":             "Apply deterministic repository patch",
			"description":       profile.coderSummary,
			"assigned_agent":    string(domain.AgentTypeCoder),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": true,
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          coderMetadata,
			"tool_request":      coderMetadata["tool_request"],
		},
		{
			"title":             "Review repository changes",
			"description":       fmt.Sprintf("Validate repository state at %s", profile.projectRoot),
			"assigned_agent":    string(domain.AgentTypeReviewer),
			"priority":          string(domain.PriorityNormal),
			"requires_approval": false,
			"execution_target":  string(domain.ExecutionTargetLocal),
			"metadata":          reviewerMetadata,
			"tool_request":      reviewerMetadata["tool_request"],
		},
	}
	return Result{
		Status:  "success",
		Summary: "planner prepared deterministic existing-repo workflow",
		Results: map[string]any{
			"status":           "success",
			"plan_summary":     fmt.Sprintf("Patch and validate existing repository %s", profile.repoName),
			"plan_markdown":    fmt.Sprintf("1. Research repository constraints\n2. Apply deterministic patch\n3. Review and test\n\nTarget: `%s`", profile.projectRoot),
			"subtasks":         subtasks,
			"project_flow":     "repo_workflow_v1",
			"repo_profile":     profile.profileName,
			"requested_agents": []string{"researcher", "coder", "reviewer"},
			"project_root_rel": profile.projectRoot,
			"test_command":     profile.testCommand,
			"plan_only":        false,
		},
	}, true
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
	candidateInput := rawPath
	if !filepath.IsAbs(candidateInput) {
		candidateInput = filepath.Join(root, candidateInput)
	}
	candidate, err := filepath.Abs(candidateInput)
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

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
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

var runnerProjectNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type runnerRepoWorkflowProfile struct {
	profileName           string
	projectType           string
	stack                 string
	language              string
	framework             string
	problemDomain         string
	errorClass            string
	fixPattern            string
	validationPattern     string
	repoName              string
	projectRoot           string
	repositoryURL         string
	defaultBranch         string
	testFocus             string
	testCommand           []string
	expectedFiles         []string
	coderTool             string
	patchTarget           string
	patch                 string
	writeContent          string
	coderSummary          string
	benchmarkCaseID       string
	benchmarkCaseType     string
	benchmarkMemoryMode   string
	benchmarkMemoryPolicy string
}

func sanitizeProjectName(value string) string {
	value = strings.TrimSpace(value)
	value = runnerProjectNameSanitizer.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.")
	return value
}

func repoWorkflowRoot(metadata map[string]any, projectRequest map[string]any) string {
	workspaceRoot := strings.TrimSpace(firstNonEmptyMetadata(metadata, "workspace_root"))
	if workspaceRoot != "" && workspaceHasGitRepo(workspaceRoot) {
		return workspaceRoot
	}
	if projectRequest == nil {
		return ""
	}
	projectRoot := strings.TrimSpace(asString(projectRequest["project_root"]))
	if projectRoot == "" {
		return ""
	}
	if !filepath.IsAbs(projectRoot) && workspaceRoot != "" {
		projectRoot = filepath.Join(workspaceRoot, projectRoot)
	}
	if workspaceHasGitRepo(projectRoot) {
		return projectRoot
	}
	return ""
}

func workspaceHasGitRepo(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil && info != nil
}

func detectRunnerRepoWorkflowProfile(workspaceRoot string) runnerRepoWorkflowProfile {
	profile := runnerRepoWorkflowProfile{
		profileName:   "existing_repo_generic",
		projectType:   "existing_repo",
		stack:         "python",
		repoName:      filepath.Base(strings.TrimSpace(workspaceRoot)),
		projectRoot:   ".",
		repositoryURL: "",
		defaultBranch: "",
		testFocus:     "existing repository review",
		testCommand:   []string{},
		expectedFiles: []string{"README.md"},
		coderTool:     "write_file",
		patchTarget:   ".lab/repo-workflow-marker.txt",
		writeContent:  "repo workflow marker\n",
		coderSummary:  "Write a deterministic marker file inside the repository workspace.",
	}
	if benchmarkProfile, ok := loadRunnerBenchmarkRepoWorkflowProfile(workspaceRoot); ok {
		return benchmarkProfile
	}
	if profile.repoName == "" || profile.repoName == "." || profile.repoName == string(filepath.Separator) {
		profile.repoName = "repo"
	}
	if strings.EqualFold(profile.repoName, "python-slugify") {
		profile.profileName = "python_slugify_v1"
		profile.testFocus = "CLI regex_pattern wiring"
		profile.testCommand = []string{"python3", "-m", "pytest", "-q", "test.py"}
		profile.expectedFiles = []string{"pyproject.toml", "slugify/__main__.py", "slugify/slugify.py", "test.py"}
		profile.repositoryURL = "https://github.com/un33k/python-slugify"
		profile.defaultBranch = "master"
		profile.coderTool = "apply_patch"
		profile.patchTarget = "slugify/__main__.py"
		profile.patch = pythonSlugifyRunnerPatch()
		profile.writeContent = ""
		profile.coderSummary = "Patch python-slugify so the CLI forwards regex_pattern into slugify() and cover it with a deterministic regression test."
	}
	return profile
}

func applyRunnerBenchmarkMetadata(metadata map[string]any, profile runnerRepoWorkflowProfile) {
	if metadata == nil {
		return
	}
	if profile.benchmarkCaseID != "" {
		metadata["benchmark_case_id"] = profile.benchmarkCaseID
	}
	if profile.benchmarkCaseType != "" {
		metadata["benchmark_case_type"] = profile.benchmarkCaseType
	}
	if profile.benchmarkMemoryMode != "" {
		metadata["benchmark_memory_mode"] = profile.benchmarkMemoryMode
		metadata["context_memory_mode"] = profile.benchmarkMemoryMode
	}
	if profile.benchmarkMemoryPolicy != "" {
		metadata["benchmark_memory_strategy"] = profile.benchmarkMemoryPolicy
		metadata["context_memory_strategy"] = profile.benchmarkMemoryPolicy
	}
	if profile.language != "" {
		metadata["language"] = profile.language
	}
	if profile.framework != "" {
		metadata["framework"] = profile.framework
	}
	if profile.problemDomain != "" {
		metadata["problem_domain"] = profile.problemDomain
	}
	if profile.errorClass != "" {
		metadata["error_class"] = profile.errorClass
	}
	if profile.fixPattern != "" {
		metadata["fix_pattern"] = profile.fixPattern
	}
	if profile.validationPattern != "" {
		metadata["validation_pattern"] = profile.validationPattern
	}
}

func loadRunnerBenchmarkRepoWorkflowProfile(workspaceRoot string) (runnerRepoWorkflowProfile, bool) {
	casePath := filepath.Join(strings.TrimSpace(workspaceRoot), ".lab", "benchmark-case.json")
	raw, err := os.ReadFile(casePath)
	if err != nil {
		return runnerRepoWorkflowProfile{}, false
	}
	var payload struct {
		ID             string   `json:"id"`
		CaseType       string   `json:"case_type"`
		RepoProfile    string   `json:"repo_profile"`
		RepoURL        string   `json:"repo_url"`
		DefaultBranch  string   `json:"default_branch"`
		ProjectType    string   `json:"project_type"`
		Runtime        string   `json:"runtime_or_stack"`
		ProjectRoot    string   `json:"project_root"`
		TestFocus      string   `json:"test_focus"`
		TestCommand    []string `json:"test_command"`
		ExpectedFiles  []string `json:"expected_files"`
		CoderTool      string   `json:"coder_tool"`
		PatchTarget    string   `json:"patch_target"`
		Patch          string   `json:"patch"`
		WriteContent   string   `json:"write_content"`
		CoderSummary   string   `json:"coder_summary"`
		MemoryMode     string   `json:"benchmark_memory_mode"`
		MemoryStrategy string   `json:"benchmark_memory_strategy"`
		Language       string   `json:"language"`
		Framework      string   `json:"framework"`
		ProblemDomain  string   `json:"problem_domain"`
		ErrorClass     string   `json:"error_class"`
		FixPattern     string   `json:"fix_pattern"`
		ValidationPattern string `json:"validation_pattern"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return runnerRepoWorkflowProfile{}, false
	}
	profile := runnerRepoWorkflowProfile{
		profileName:           firstNonEmptyString(strings.TrimSpace(payload.RepoProfile), "benchmark_repo_workflow"),
		projectType:           firstNonEmptyString(strings.TrimSpace(payload.ProjectType), "existing_repo"),
		stack:                 firstNonEmptyString(strings.TrimSpace(payload.Runtime), "python"),
		language:              firstNonEmptyString(strings.TrimSpace(payload.Language), strings.TrimSpace(payload.Runtime)),
		framework:             strings.TrimSpace(payload.Framework),
		problemDomain:         strings.TrimSpace(payload.ProblemDomain),
		errorClass:            strings.TrimSpace(payload.ErrorClass),
		fixPattern:            strings.TrimSpace(payload.FixPattern),
		validationPattern:     strings.TrimSpace(payload.ValidationPattern),
		repoName:              filepath.Base(strings.TrimSpace(workspaceRoot)),
		projectRoot:           firstNonEmptyString(strings.TrimSpace(payload.ProjectRoot), "."),
		repositoryURL:         strings.TrimSpace(payload.RepoURL),
		defaultBranch:         strings.TrimSpace(payload.DefaultBranch),
		testFocus:             firstNonEmptyString(strings.TrimSpace(payload.TestFocus), "benchmark workflow"),
		testCommand:           cloneRunnerStrings(payload.TestCommand),
		expectedFiles:         cloneRunnerStrings(payload.ExpectedFiles),
		coderTool:             firstNonEmptyString(strings.TrimSpace(payload.CoderTool), "write_file"),
		patchTarget:           strings.TrimSpace(payload.PatchTarget),
		patch:                 payload.Patch,
		writeContent:          payload.WriteContent,
		coderSummary:          firstNonEmptyString(strings.TrimSpace(payload.CoderSummary), "Apply benchmark-defined repository change."),
		benchmarkCaseID:       strings.TrimSpace(payload.ID),
		benchmarkCaseType:     strings.TrimSpace(payload.CaseType),
		benchmarkMemoryMode:   firstNonEmptyString(strings.TrimSpace(payload.MemoryMode), "on"),
		benchmarkMemoryPolicy: firstNonEmptyString(strings.TrimSpace(payload.MemoryStrategy), "repo_specific_first"),
	}
	if profile.repoName == "" || profile.repoName == "." || profile.repoName == string(filepath.Separator) {
		profile.repoName = "repo"
	}
	if len(profile.expectedFiles) == 0 {
		profile.expectedFiles = []string{"README.md"}
	}
	return profile, true
}

func cloneRunnerStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for _, item := range input {
		text := strings.TrimSpace(item)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func pythonSlugifyRunnerPatch() string {
	return strings.Join([]string{
		"diff --git a/slugify/__main__.py b/slugify/__main__.py",
		"--- a/slugify/__main__.py",
		"+++ b/slugify/__main__.py",
		"@@ -76,6 +76,7 @@ def slugify_params(args: argparse.Namespace) -> dict[str, Any]:",
		"         save_order=args.save_order,",
		"         separator=args.separator,",
		"         stopwords=args.stopwords,",
		"+        regex_pattern=args.regex_pattern,",
		"         lowercase=args.lowercase,",
		"         replacements=args.replacements,",
		"         allow_unicode=args.allow_unicode",
		"diff --git a/test.py b/test.py",
		"--- a/test.py",
		"+++ b/test.py",
		"@@ -612,6 +612,11 @@ class TestCommandParams(unittest.TestCase):",
		"         expected = self.make_params(stopwords=['abba', 'beatles'], max_length=98, separator='+')",
		"         self.assertParamsMatch(expected, params)",
		" ",
		"+    def test_regex_pattern_param(self):",
		"+        params = self.get_params_from_cli('--regex-pattern', '[^a-z]+')",
		"+        expected = self.make_params(regex_pattern='[^a-z]+')",
		"+        self.assertParamsMatch(expected, params)",
		"+",
		"     def test_replacements_right(self):",
		"         params = self.get_params_from_cli('--replacements', 'A->B', 'C->D')",
		"         expected = self.make_params(replacements=[['A', 'B'], ['C', 'D']])",
	}, "\n")
}

func defaultProjectTestCommand(projectType, stack string) []string {
	switch strings.ToLower(strings.TrimSpace(stack)) {
	case "node":
		return []string{"node", "--check", "app.js"}
	case "static":
		return []string{"python3", "-c", "from pathlib import Path; html=Path('index.html').read_text(); assert '<html' in html.lower()"}
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
	case "web_small":
		return []string{"python3", "-c", "from pathlib import Path; html=Path('index.html').read_text(); assert '<html' in html.lower()"}
	default:
		return []string{"python3", "-m", "py_compile", "main.py"}
	}
}

func expectedProjectFiles(projectType string) []string {
	switch strings.ToLower(strings.TrimSpace(projectType)) {
	case "api_http":
		return []string{"README.md", "app.py", "tests/test_app.py", "lab.json"}
	case "web_small":
		return []string{"README.md", "index.html", "app.js", "style.css", "tests/test_static.py", "lab.json"}
	case "worker_background":
		return []string{"README.md", "worker.py", "tests/test_worker.py", "lab.json"}
	case "debug_regression":
		return []string{"README.md", "calculator.py", "tests/test_calculator.py", "lab.json"}
	case "toy_repo":
		return []string{"README.md", "src/service.py", "tests/test_service.py", "docs/notes.md", "lab.json"}
	default:
		return []string{"README.md", "main.py", "tests/test_main.py", "lab.json"}
	}
}

func anyStringSliceDefault(value any, fallback []string) []string {
	switch items := value.(type) {
	case []string:
		if len(items) > 0 {
			return items
		}
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text := strings.TrimSpace(asString(item))
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return fallback
}

func runCommandInDir(dir string, argv []string) (string, string, int, error) {
	if len(argv) == 0 {
		return "", "", -1, fmt.Errorf("argv is required")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	text := string(output)
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		return text, text, exitCode, err
	}
	return text, "", exitCode, nil
}
