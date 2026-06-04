package initiative

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type ObjectiveInput struct {
	Title         string
	Objective     string
	WorkspaceRoot string
	CreatedBy     string
}

func (s *Service) GenerateObjectiveExecutionContract(_ context.Context, input ObjectiveInput) (domain.ExecutionContract, error) {
	workspaceRoot := strings.TrimSpace(input.WorkspaceRoot)
	objective := strings.TrimSpace(input.Objective)
	title := firstNonEmptyString(strings.TrimSpace(input.Title), objective)
	if title == "" || objective == "" || workspaceRoot == "" {
		return domain.ExecutionContract{}, fmt.Errorf("title, objective and workspace_root are required")
	}
	profile := detectRepoWorkflowProfile(workspaceRoot, objective)
	projectRoot := firstNonEmptyString(strings.TrimSpace(profile.projectRoot), ".")
	validationCommands := append([]string{}, profile.testCommand...)
	if len(validationCommands) == 0 {
		validationCommands = []string{"git", "diff", "--no-ext-diff"}
	}
	suspectedPaths := append([]string{}, profile.expected...)
	editMetadata := map[string]any{
		"workspace_root": workspaceRoot,
		"project_request": map[string]any{
			"project_name":       firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
			"project_root":       projectRoot,
			"project_type":       firstNonEmptyString(profile.projectType, "existing_repo"),
			"runtime_or_stack":   firstNonEmptyString(profile.stack, "python"),
			"repository_url":     strings.TrimSpace(profile.repositoryURL),
			"default_branch":     strings.TrimSpace(profile.defaultBranch),
			"repo_profile":       firstNonEmptyString(profile.projectFlow, "existing_repo_generic"),
			"goal":               objective,
			"test_focus":         firstNonEmptyString(profile.testFocus, "objective execution"),
			"test_command":       validationCommands,
			"expected_files":     suspectedPaths,
			"language":           strings.TrimSpace(profile.language),
			"framework":          strings.TrimSpace(profile.framework),
			"problem_domain":     strings.TrimSpace(profile.problemDomain),
			"error_class":        strings.TrimSpace(profile.errorClass),
			"fix_pattern":        strings.TrimSpace(profile.fixPattern),
			"validation_pattern": strings.TrimSpace(profile.validationPattern),
		},
	}
	editRequest := map[string]any{
		"tool": "run_command",
		"argv": []string{
			"aider-task",
			"--task-id", uuid.NewString(),
			"--repo", firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
			"--description", objective,
		},
	}
	validateRequest := map[string]any{
		"tool": "run_tests",
		"argv": validationCommands,
	}
	reviewRequest := map[string]any{
		"tool":         "review_workspace",
		"project_root": projectRoot,
		"test_command": validationCommands,
		"execution_contract": map[string]any{
			"title":                title,
			"normalized_objective": objective,
			"workspace_root":       workspaceRoot,
		},
	}
	workItems := []domain.WorkItem{
		{
			ID:               "research",
			Kind:             domain.WorkItemKindResearch,
			Title:            "Research objective context",
			Description:      "Capture repository context, validation constraints, and likely files before editing.",
			AssignedAgent:    domain.AgentTypeResearcher,
			ExecutionMode:    domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:  domain.ExecutionTargetLocal,
			Backend:          "local_bridge",
			DefinitionOfDone: "Research context and evidence artifacts are persisted for the objective.",
			SuspectedPaths:   suspectedPaths,
			ToolRequest: map[string]any{
				"tool":            "research_project",
				"project_request": editMetadata["project_request"],
			},
			Metadata: cloneObjectiveMetadata(editMetadata),
		},
		{
			ID:                 "edit",
			Kind:               domain.WorkItemKindEdit,
			Title:              "Apply objective changes with aider",
			Description:        objective,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          []string{"research"},
			ApprovalRequired:   true,
			DefinitionOfDone:   "Aider applies changes and leaves a git diff for validation.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest:        editRequest,
			Metadata:           cloneObjectiveMetadata(editMetadata),
		},
		{
			ID:                 "validate",
			Kind:               domain.WorkItemKindValidate,
			Title:              "Run validation commands",
			Description:        "Execute the contract validation commands against the edited repository.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{"edit"},
			DefinitionOfDone:   "Validation command output and exit status are persisted.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest:        validateRequest,
			Metadata:           cloneObjectiveMetadata(editMetadata),
		},
		{
			ID:                 "review",
			Kind:               domain.WorkItemKindReview,
			Title:              "Review diff and validation evidence",
			Description:        "Review the git diff, changed files, and validation results against the execution contract.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{"validate"},
			DefinitionOfDone:   "Review decision is persisted as approved, changes_requested, or rejected.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest:        reviewRequest,
			Metadata:           cloneObjectiveMetadata(editMetadata),
		},
	}
	return domain.ExecutionContract{
		Title:               title,
		NormalizedObjective: objective,
		WorkspaceRoot:       workspaceRoot,
		ExecutionBackend:    "aider-task",
		ChangeStrategy:      "Research the repository, edit through aider-task, run validation commands, then review diff and evidence.",
		ValidationCommands:  validationCommands,
		CompletionCriteria: []string{
			"Repository diff exists for the requested objective.",
			"Validation commands complete successfully.",
			"Review decision is approved.",
		},
		RiskLevel:        "medium",
		ApprovalRequired: true,
		SuspectedPaths:   suspectedPaths,
		RepositoryURL:    strings.TrimSpace(profile.repositoryURL),
		RepoProfile:      firstNonEmptyString(profile.projectFlow, "existing_repo_generic"),
		WorkItems:        workItems,
	}, nil
}

func cloneObjectiveMetadata(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	return out
}
