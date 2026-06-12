package initiative

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type ObjectiveInput struct {
	Title                       string
	Objective                   string
	WorkspaceRoot               string
	CreatedBy                   string
	TimeBudgetSeconds           int
	PlanningContext             *domain.ContextPackage
	PlanningRetrievalArtifactID string
}

func (s *Service) GenerateObjectiveExecutionContract(_ context.Context, input ObjectiveInput) (domain.ExecutionContract, error) {
	workspaceRoot := strings.TrimSpace(input.WorkspaceRoot)
	objective := strings.TrimSpace(input.Objective)
	title := firstNonEmptyString(strings.TrimSpace(input.Title), objective)
	if title == "" || objective == "" || workspaceRoot == "" {
		return domain.ExecutionContract{}, fmt.Errorf("title, objective and workspace_root are required")
	}
	bootstrap := detectObjectiveBootstrapProfile(workspaceRoot, title, objective)
	profile := bootstrap.repoProfile
	if !bootstrap.Enabled {
		profile = detectRepoWorkflowProfile(workspaceRoot, objective)
	}
	projectRoot := firstNonEmptyString(strings.TrimSpace(profile.projectRoot), ".")
	validationCommands := append([]string{}, profile.testCommand...)
	if len(validationCommands) == 0 {
		validationCommands = []string{"git", "diff", "--no-ext-diff"}
	}
	planningPrecedents := objectiveRetrievalPrecedentsFromContext(input.PlanningContext)
	scopeHypotheses, rejectedScopeHypotheses := buildInitialScopeHypotheses(workspaceRoot, objective, profile.expected, planningPrecedents)
	suspectedPaths := scopeHypothesisPaths(scopeHypotheses)
	if len(suspectedPaths) == 0 {
		suspectedPaths = append([]string{}, profile.expected...)
	}
	documentationWorkstream := detectObjectiveDocumentationWorkstream(workspaceRoot, objective, suspectedPaths, bootstrap.Enabled)
	infraWorkstream := detectObjectiveInfraWorkstream(workspaceRoot, objective, suspectedPaths, bootstrap.Enabled)
	dependencyWorkstream := detectObjectiveDependencyWorkstream(workspaceRoot, objective, suspectedPaths, bootstrap.Enabled)
	initialAnalysis := detectInitialObjectiveAnalysis(objective, scopeHypotheses, planningPrecedents)
	initialBaselineValidate := detectInitialBaselineValidation(objective, planningPrecedents, bootstrap.Enabled)
	editMetadata := map[string]any{
		"workspace_root":                        workspaceRoot,
		"initial_scope_hypotheses":              scopeHypotheses,
		"rejected_scope_hypotheses":             rejectedScopeHypotheses,
		"planning_retrieval_precedents":         planningPrecedents,
		"planning_retrieval_packet_artifact_id": strings.TrimSpace(input.PlanningRetrievalArtifactID),
		"project_request": map[string]any{
			"project_name":        firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
			"project_root":        projectRoot,
			"project_type":        firstNonEmptyString(profile.projectType, "existing_repo"),
			"runtime_or_stack":    firstNonEmptyString(profile.stack, "python"),
			"repository_url":      strings.TrimSpace(profile.repositoryURL),
			"default_branch":      strings.TrimSpace(profile.defaultBranch),
			"repo_profile":        firstNonEmptyString(profile.projectFlow, "existing_repo_generic"),
			"goal":                objective,
			"test_focus":          firstNonEmptyString(profile.testFocus, "objective execution"),
			"test_command":        validationCommands,
			"expected_files":      suspectedPaths,
			"language":            strings.TrimSpace(profile.language),
			"framework":           strings.TrimSpace(profile.framework),
			"problem_domain":      strings.TrimSpace(profile.problemDomain),
			"error_class":         strings.TrimSpace(profile.errorClass),
			"fix_pattern":         strings.TrimSpace(profile.fixPattern),
			"validation_pattern":  strings.TrimSpace(profile.validationPattern),
			"bootstrap_workspace": bootstrap.Enabled,
		},
	}
	if input.PlanningContext != nil && len(input.PlanningContext.Chunks) > 0 {
		editMetadata["context_package"] = input.PlanningContext
		editMetadata["objective_retrieval_precedents"] = planningPrecedents
		editMetadata["planning_context_source_refs"] = append([]string{}, input.PlanningContext.SourceRefs...)
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
	}
	editDependsOn := []string{"research"}
	if initialBaselineValidate.Enabled {
		baselineMetadata := cloneObjectiveMetadata(editMetadata)
		baselineMetadata["objective_next_action_after_success"] = "edit"
		baselineMetadata["objective_baseline_validation"] = true
		baselineMetadata["objective_baseline_reason"] = initialBaselineValidate.Reason
		workItems = append(workItems, domain.WorkItem{
			ID:                 "validate-baseline",
			Kind:               domain.WorkItemKindValidate,
			Title:              "Reproduce baseline validation before editing",
			Description:        initialBaselineValidate.Reason,
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{"research"},
			DefinitionOfDone:   "Baseline validation evidence is captured before the first edit.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool": "run_tests",
				"argv": validationCommands,
			},
			Metadata: baselineMetadata,
		})
		editDependsOn = append(editDependsOn, "validate-baseline")
	}
	if bootstrap.Enabled {
		bootstrapMetadata := cloneObjectiveMetadata(editMetadata)
		bootstrapMetadata["bootstrap_workspace"] = true
		workItems = append(workItems, domain.WorkItem{
			ID:                 "bootstrap",
			Kind:               domain.WorkItemKindEdit,
			Title:              "Bootstrap runnable workspace scaffold",
			Description:        "Create the minimal runnable scaffold before objective-specific edits.",
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{"research"},
			ApprovalRequired:   false,
			DefinitionOfDone:   "Workspace contains an in-place scaffold with deterministic validation targets.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool":                    "scaffold_project",
				"project_root":            ".",
				"project_name":            profile.repoName,
				"project_type":            profile.projectType,
				"runtime_or_stack":        profile.stack,
				"goal":                    objective,
				"test_focus":              firstNonEmptyString(profile.testFocus, "objective bootstrap"),
				"initialize_git":          true,
				"commit_initial_scaffold": true,
				"test_command":            validationCommands,
				"expected_files":          suspectedPaths,
			},
			Metadata: bootstrapMetadata,
		})
		editDependsOn = append(editDependsOn, "bootstrap")
	}
	if initialAnalysis.Enabled {
		analysisMetadata := cloneObjectiveMetadata(editMetadata)
		analysisMetadata["objective_analysis_reason"] = initialAnalysis.Reason
		analysisMetadata["objective_analysis_types"] = initialAnalysis.Types
		workItems = append(workItems, domain.WorkItem{
			ID:               "analyze",
			Kind:             domain.WorkItemKindAnalyze,
			Title:            "Analyze structural risks before editing",
			Description:      initialAnalysis.Reason,
			AssignedAgent:    domain.AgentTypeReviewer,
			ExecutionMode:    domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:  domain.ExecutionTargetLocal,
			Backend:          "local_bridge",
			DependsOn:        append([]string{}, editDependsOn...),
			DefinitionOfDone: "Static analysis findings are persisted before the first edit.",
			SuspectedPaths:   suspectedPaths,
			ToolRequest: map[string]any{
				"tool":           "code_analysis",
				"project_root":   projectRoot,
				"analysis_types": initialAnalysis.Types,
			},
			Metadata: analysisMetadata,
		})
		editDependsOn = append(editDependsOn, "analyze")
	}
	workItems = append(workItems, domain.WorkItem{
		ID:                 "edit",
		Kind:               domain.WorkItemKindEdit,
		Title:              "Apply objective changes with aider",
		Description:        objective,
		AssignedAgent:      domain.AgentTypeCoder,
		ExecutionMode:      domain.TaskLaunchModeAgentLocal,
		ExecutionTarget:    domain.ExecutionTargetLocal,
		Backend:            "aider-task",
		DependsOn:          editDependsOn,
		ApprovalRequired:   true,
		DefinitionOfDone:   "Aider applies changes and leaves a git diff for validation.",
		ValidationCommands: validationCommands,
		SuspectedPaths:     suspectedPaths,
		ToolRequest:        editRequest,
		Metadata:           cloneObjectiveMetadata(editMetadata),
	})
	finalEditID := "edit"
	finalValidatePaths := suspectedPaths
	if documentationWorkstream.Enabled {
		docsMetadata := cloneObjectiveMetadata(editMetadata)
		docsMetadata["objective_workstream"] = "documentation_sync"
		docsMetadata["suspected_paths"] = documentationWorkstream.Paths
		docsProjectRequest := cloneObjectiveMetadata(editMetadata["project_request"].(map[string]any))
		docsProjectRequest["expected_files"] = documentationWorkstream.Paths
		docsProjectRequest["goal"] = documentationWorkstream.Description
		docsMetadata["project_request"] = docsProjectRequest
		docsRequest := map[string]any{
			"tool": "run_command",
			"argv": []string{
				"aider-task",
				"--task-id", uuid.NewString(),
				"--repo", firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
				"--description", documentationWorkstream.Description,
			},
		}
		workItems = append(workItems, domain.WorkItem{
			ID:                 "edit-docs",
			Kind:               domain.WorkItemKindEdit,
			Title:              "Synchronize documentation with the code change",
			Description:        documentationWorkstream.Description,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          append(append([]string{}, editDependsOn...), "edit"),
			ApprovalRequired:   true,
			DefinitionOfDone:   "Documentation and operator-facing notes reflect the implemented change without widening scope.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     documentationWorkstream.Paths,
			ToolRequest:        docsRequest,
			Metadata:           docsMetadata,
		})
		finalEditID = "edit-docs"
		finalValidatePaths = uniqueObjectivePaths(append(append([]string{}, suspectedPaths...), documentationWorkstream.Paths...))
	}
	if infraWorkstream.Enabled {
		infraMetadata := cloneObjectiveMetadata(editMetadata)
		infraMetadata["objective_workstream"] = "config_sync"
		infraMetadata["suspected_paths"] = infraWorkstream.Paths
		infraProjectRequest := cloneObjectiveMetadata(editMetadata["project_request"].(map[string]any))
		infraProjectRequest["expected_files"] = infraWorkstream.Paths
		infraProjectRequest["goal"] = infraWorkstream.Description
		infraMetadata["project_request"] = infraProjectRequest
		infraRequest := map[string]any{
			"tool": "run_command",
			"argv": []string{
				"aider-task",
				"--task-id", uuid.NewString(),
				"--repo", firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
				"--description", infraWorkstream.Description,
			},
		}
		workItems = append(workItems, domain.WorkItem{
			ID:                 "edit-config",
			Kind:               domain.WorkItemKindEdit,
			Title:              "Synchronize configuration and infra with the code change",
			Description:        infraWorkstream.Description,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          append(append([]string{}, editDependsOn...), finalEditID),
			ApprovalRequired:   true,
			DefinitionOfDone:   "Configuration and infra-facing files reflect the implemented change without diverging from the code path.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     infraWorkstream.Paths,
			ToolRequest:        infraRequest,
			Metadata:           infraMetadata,
		})
		finalEditID = "edit-config"
		finalValidatePaths = uniqueObjectivePaths(append(finalValidatePaths, infraWorkstream.Paths...))
	}
	if dependencyWorkstream.Enabled {
		dependencyMetadata := cloneObjectiveMetadata(editMetadata)
		dependencyMetadata["objective_workstream"] = "dependency_sync"
		dependencyMetadata["suspected_paths"] = dependencyWorkstream.Paths
		dependencyProjectRequest := cloneObjectiveMetadata(editMetadata["project_request"].(map[string]any))
		dependencyProjectRequest["expected_files"] = dependencyWorkstream.Paths
		dependencyProjectRequest["goal"] = dependencyWorkstream.Description
		dependencyMetadata["project_request"] = dependencyProjectRequest
		dependencyRequest := map[string]any{
			"tool": "run_command",
			"argv": []string{
				"aider-task",
				"--task-id", uuid.NewString(),
				"--repo", firstNonEmptyString(profile.repoName, filepath.Base(workspaceRoot), "repo"),
				"--description", dependencyWorkstream.Description,
			},
		}
		workItems = append(workItems, domain.WorkItem{
			ID:                 "edit-deps",
			Kind:               domain.WorkItemKindEdit,
			Title:              "Synchronize dependency manifests and lockfiles with the code change",
			Description:        dependencyWorkstream.Description,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          append(append([]string{}, editDependsOn...), finalEditID),
			ApprovalRequired:   true,
			DefinitionOfDone:   "Dependency manifests and lockfiles reflect the implemented change without drifting from the main code path.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     dependencyWorkstream.Paths,
			ToolRequest:        dependencyRequest,
			Metadata:           dependencyMetadata,
		})
		finalEditID = "edit-deps"
		finalValidatePaths = uniqueObjectivePaths(append(finalValidatePaths, dependencyWorkstream.Paths...))
	}
	workItems = append(workItems,
		domain.WorkItem{
			ID:                 "validate",
			Kind:               domain.WorkItemKindValidate,
			Title:              "Run validation commands",
			Description:        "Execute the contract validation commands against the edited repository.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{finalEditID},
			DefinitionOfDone:   "Validation command output and exit status are persisted.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     finalValidatePaths,
			ToolRequest:        validateRequest,
			Metadata:           cloneObjectiveMetadata(editMetadata),
		},
		domain.WorkItem{
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
			SuspectedPaths:     finalValidatePaths,
			ToolRequest:        reviewRequest,
			Metadata:           cloneObjectiveMetadata(editMetadata),
		},
	)
	return domain.ExecutionContract{
		Title:               title,
		NormalizedObjective: objective,
		WorkspaceRoot:       workspaceRoot,
		ExecutionBackend:    objectiveExecutionBackend(bootstrap.Enabled),
		ChangeStrategy:      objectiveChangeStrategy(bootstrap.Enabled),
		ValidationCommands:  validationCommands,
		CompletionCriteria: []string{
			"Repository diff exists for the requested objective.",
			"Validation commands complete successfully.",
			"Review decision is approved.",
		},
		TimeBudgetSeconds:       input.TimeBudgetSeconds,
		RiskLevel:               "medium",
		ApprovalRequired:        true,
		SuspectedPaths:          suspectedPaths,
		InitialScopeHypotheses:  scopeHypotheses,
		RejectedScopeHypotheses: rejectedScopeHypotheses,
		RepositoryURL:           strings.TrimSpace(profile.repositoryURL),
		RepoProfile:             firstNonEmptyString(profile.projectFlow, "existing_repo_generic"),
		WorkItems:               workItems,
	}, nil
}

type objectiveInitialAnalysis struct {
	Enabled bool
	Types   []string
	Reason  string
}

type objectiveInitialBaselineValidate struct {
	Enabled bool
	Reason  string
}

type objectiveDocumentationWorkstream struct {
	Enabled     bool
	Paths       []string
	Description string
}

type objectiveInfraWorkstream struct {
	Enabled     bool
	Paths       []string
	Description string
}

type objectiveDependencyWorkstream struct {
	Enabled     bool
	Paths       []string
	Description string
}

func detectInitialObjectiveAnalysis(objective string, scopeHypotheses []domain.ScopeHypothesis, precedents []map[string]any) objectiveInitialAnalysis {
	goal := strings.ToLower(strings.TrimSpace(objective))
	types := make([]string, 0, 4)
	addType := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range types {
			if existing == value {
				return
			}
		}
		types = append(types, value)
	}
	addFromText := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		matched := false
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "security") || strings.Contains(lower, "secret") || strings.Contains(lower, "hardcoded") || strings.Contains(lower, "credential") {
			addType("security")
			matched = true
		}
		if strings.Contains(lower, "dependency") || strings.Contains(lower, "lockfile") || strings.Contains(lower, "go.sum") || strings.Contains(lower, "package.json") {
			addType("dependencies")
			matched = true
		}
		if strings.Contains(lower, "lint") || strings.Contains(lower, "whitespace") || strings.Contains(lower, "format") {
			addType("lint")
			matched = true
		}
		if strings.Contains(lower, "complexity") || strings.Contains(lower, "large source file") || strings.Contains(lower, "refactor") || strings.Contains(lower, "architecture") || strings.Contains(lower, "legacy") || strings.Contains(lower, "structural") {
			addType("complexity")
			matched = true
		}
		return matched
	}
	objectiveMatched := addFromText(goal)
	precedentMatched := false
	ambiguousScope := objectiveScopeIsAmbiguous(scopeHypotheses)
	for _, item := range precedents {
		text := strings.TrimSpace(asString(item["summary"]))
		if text == "" {
			text = strings.TrimSpace(asString(item["source_ref"]))
		}
		if addFromText(text) {
			precedentMatched = true
		}
	}
	if len(types) == 0 || (!objectiveMatched && !precedentMatched) {
		if !ambiguousScope {
			return objectiveInitialAnalysis{}
		}
	}
	reasonParts := []string{}
	if objectiveMatched {
		reasonParts = append(reasonParts, "the objective already signals structural analysis work")
	}
	if precedentMatched {
		reasonParts = append(reasonParts, "retrieved precedents point to the same class of structural issue")
	}
	if ambiguousScope {
		addType("complexity")
		reasonParts = append(reasonParts, objectiveAmbiguousScopeReason(scopeHypotheses))
	}
	return objectiveInitialAnalysis{
		Enabled: true,
		Types:   types,
		Reason:  "Run targeted static analysis before the first edit because " + strings.Join(reasonParts, " and ") + ".",
	}
}

func objectiveScopeIsAmbiguous(scopeHypotheses []domain.ScopeHypothesis) bool {
	if len(scopeHypotheses) < 2 {
		return false
	}
	first := scopeHypotheses[0]
	second := scopeHypotheses[1]
	if strings.TrimSpace(first.Path) == "" || strings.TrimSpace(second.Path) == "" {
		return false
	}
	scoreGap := first.Score - second.Score
	if scoreGap < 0 {
		scoreGap = -scoreGap
	}
	if scoreGap > 2 {
		return false
	}
	return objectiveScopeHasStrongEvidence(first) && objectiveScopeHasStrongEvidence(second)
}

func objectiveAmbiguousScopeReason(scopeHypotheses []domain.ScopeHypothesis) string {
	if len(scopeHypotheses) < 2 {
		return "the planner shortlist remains ambiguous"
	}
	first := strings.TrimSpace(scopeHypotheses[0].Path)
	second := strings.TrimSpace(scopeHypotheses[1].Path)
	return fmt.Sprintf("the planner shortlist remains ambiguous between %s and %s", first, second)
}

func objectiveScopeHasStrongEvidence(hypothesis domain.ScopeHypothesis) bool {
	if hypothesis.Score >= 6 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(hypothesis.Confidence)) {
	case "medium", "high":
		return true
	default:
		return false
	}
}

func detectInitialBaselineValidation(objective string, precedents []map[string]any, bootstrap bool) objectiveInitialBaselineValidate {
	if bootstrap {
		return objectiveInitialBaselineValidate{}
	}
	goal := strings.ToLower(strings.TrimSpace(objective))
	explicitBugfix := strings.Contains(goal, "fix") || strings.Contains(goal, "failing") || strings.Contains(goal, "regression") || strings.Contains(goal, "broken") || strings.Contains(goal, "repair")
	explicitTests := strings.Contains(goal, "test") || strings.Contains(goal, "validation")
	precedentMatched := false
	for _, item := range precedents {
		text := strings.ToLower(strings.TrimSpace(asString(item["summary"])))
		if strings.Contains(text, "validation failed") || strings.Contains(text, "failing test") || strings.Contains(text, "regression") || strings.Contains(text, "repair loop") {
			precedentMatched = true
			break
		}
	}
	if !(explicitBugfix && explicitTests) && !precedentMatched {
		return objectiveInitialBaselineValidate{}
	}
	reasonParts := []string{}
	if explicitBugfix && explicitTests {
		reasonParts = append(reasonParts, "the objective reads like a bugfix/regression and names validation directly")
	}
	if precedentMatched {
		reasonParts = append(reasonParts, "retrieved precedents suggest reproducing the failure before editing")
	}
	return objectiveInitialBaselineValidate{
		Enabled: true,
		Reason:  "Reproduce the current validation state before the first edit because " + strings.Join(reasonParts, " and ") + ".",
	}
}

func detectObjectiveDocumentationWorkstream(workspaceRoot, objective string, suspectedPaths []string, bootstrap bool) objectiveDocumentationWorkstream {
	goal := strings.ToLower(strings.TrimSpace(objective))
	if goal == "" {
		return objectiveDocumentationWorkstream{}
	}
	docMentioned := strings.Contains(goal, "readme") || strings.Contains(goal, "documentation") || strings.Contains(goal, "docs") || strings.Contains(goal, "document")
	if !docMentioned {
		return objectiveDocumentationWorkstream{}
	}
	hasNonDocScope := false
	for _, path := range suspectedPaths {
		if !objectiveIsDocumentationPath(path) {
			hasNonDocScope = true
			break
		}
	}
	if !hasNonDocScope {
		return objectiveDocumentationWorkstream{}
	}
	docPaths := objectiveDocumentationPaths(workspaceRoot, bootstrap)
	if len(docPaths) == 0 {
		return objectiveDocumentationWorkstream{}
	}
	return objectiveDocumentationWorkstream{
		Enabled:     true,
		Paths:       docPaths,
		Description: "Synchronize the repository documentation and operator-facing notes with the implemented objective change.",
	}
}

func detectObjectiveInfraWorkstream(workspaceRoot, objective string, suspectedPaths []string, bootstrap bool) objectiveInfraWorkstream {
	goal := strings.ToLower(strings.TrimSpace(objective))
	if goal == "" {
		return objectiveInfraWorkstream{}
	}
	infraMentioned := strings.Contains(goal, "docker") ||
		strings.Contains(goal, "compose") ||
		strings.Contains(goal, "kubernetes") ||
		strings.Contains(goal, "helm") ||
		strings.Contains(goal, "deploy") ||
		strings.Contains(goal, "deployment") ||
		strings.Contains(goal, "config") ||
		strings.Contains(goal, "configuration") ||
		strings.Contains(goal, "workflow") ||
		strings.Contains(goal, "ci") ||
		strings.Contains(goal, "infra") ||
		strings.Contains(goal, "environment") ||
		strings.Contains(goal, ".env")
	if !infraMentioned {
		return objectiveInfraWorkstream{}
	}
	hasNonInfraScope := false
	for _, path := range suspectedPaths {
		if !objectiveIsInfraPath(path) {
			hasNonInfraScope = true
			break
		}
	}
	if !hasNonInfraScope {
		return objectiveInfraWorkstream{}
	}
	infraPaths := objectiveInfraPaths(workspaceRoot, bootstrap)
	if len(infraPaths) == 0 {
		return objectiveInfraWorkstream{}
	}
	return objectiveInfraWorkstream{
		Enabled:     true,
		Paths:       infraPaths,
		Description: "Synchronize configuration, deployment, or environment files with the implemented objective change.",
	}
}

func detectObjectiveDependencyWorkstream(workspaceRoot, objective string, suspectedPaths []string, bootstrap bool) objectiveDependencyWorkstream {
	goal := strings.ToLower(strings.TrimSpace(objective))
	if goal == "" {
		return objectiveDependencyWorkstream{}
	}
	dependencyMentioned := strings.Contains(goal, "dependency") ||
		strings.Contains(goal, "dependencies") ||
		strings.Contains(goal, "lockfile") ||
		strings.Contains(goal, "package.json") ||
		strings.Contains(goal, "package-lock") ||
		strings.Contains(goal, "yarn.lock") ||
		strings.Contains(goal, "pnpm-lock") ||
		strings.Contains(goal, "go.mod") ||
		strings.Contains(goal, "go.sum") ||
		strings.Contains(goal, "requirements") ||
		strings.Contains(goal, "pyproject") ||
		strings.Contains(goal, "poetry.lock") ||
		strings.Contains(goal, "cargo") ||
		strings.Contains(goal, "gemfile") ||
		strings.Contains(goal, "composer") ||
		strings.Contains(goal, "upgrade") ||
		strings.Contains(goal, "bump") ||
		strings.Contains(goal, "vulnerab")
	if !dependencyMentioned {
		return objectiveDependencyWorkstream{}
	}
	hasNonDependencyScope := false
	for _, path := range suspectedPaths {
		if !objectiveIsDependencyPath(path) {
			hasNonDependencyScope = true
			break
		}
	}
	if !hasNonDependencyScope {
		return objectiveDependencyWorkstream{}
	}
	dependencyPaths := objectiveDependencyPaths(workspaceRoot, bootstrap)
	if len(dependencyPaths) == 0 {
		return objectiveDependencyWorkstream{}
	}
	return objectiveDependencyWorkstream{
		Enabled:     true,
		Paths:       dependencyPaths,
		Description: "Synchronize dependency manifests, lockfiles, or package metadata with the implemented objective change.",
	}
}

func objectiveRetrievalPrecedentsFromContext(pkg *domain.ContextPackage) []map[string]any {
	if pkg == nil || len(pkg.Chunks) == 0 {
		return nil
	}
	precedents := make([]map[string]any, 0, min(3, len(pkg.Chunks)))
	for _, chunk := range pkg.Chunks {
		item := map[string]any{
			"source_ref":  strings.TrimSpace(chunk.SourceRef),
			"source_type": strings.TrimSpace(chunk.SourceType),
			"source_id":   strings.TrimSpace(chunk.SourceID),
			"summary":     objectiveSummarizeContextChunk(chunk.ContentText),
		}
		if chunk.Score != nil {
			item["score"] = *chunk.Score
		}
		if chunk.InitiativeID != nil {
			item["initiative_id"] = *chunk.InitiativeID
		}
		if chunk.TaskID != nil {
			item["task_id"] = *chunk.TaskID
		}
		if chunk.ArtifactID != nil {
			item["artifact_id"] = *chunk.ArtifactID
		}
		precedents = append(precedents, item)
		if len(precedents) >= 3 {
			break
		}
	}
	if len(precedents) == 0 {
		return nil
	}
	return precedents
}

func objectiveSummarizeContextChunk(text string) string {
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

func cloneObjectiveMetadata(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	return out
}

func buildInitialScopeHypotheses(workspaceRoot, objective string, expectedPaths []string, planningPrecedents []map[string]any) ([]domain.ScopeHypothesis, []domain.ScopeHypothesis) {
	readmeExcerpt := readObjectiveReadme(workspaceRoot)
	treeFiles := listObjectiveCandidateFiles(workspaceRoot)
	type candidate struct {
		path       string
		score      int
		rationale  string
		evidence   string
		confidence string
		citations  []string
	}
	candidates := make([]candidate, 0, len(treeFiles)+len(expectedPaths))
	goal := strings.ToLower(strings.TrimSpace(objective))
	readmeLines := strings.Split(strings.ToLower(readmeExcerpt), "\n")
	for _, path := range uniqueObjectivePaths(append(treeFiles, expectedPaths...)) {
		lowerPath := strings.ToLower(strings.TrimSpace(path))
		if lowerPath == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(lowerPath))
		score := 0
		rationale := ""
		evidence := ""
		confidence := "low"
		if containsExactPath(goal, lowerPath) || containsExactPath(goal, base) {
			score += 12
			if objectivePrimaryCodeCue(goal, lowerPath) {
				score += 3
			}
			rationale = "objective references the file directly"
			evidence = path
			confidence = "high"
		}
		for _, line := range readmeLines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lineScore := 0
			if strings.Contains(line, lowerPath) {
				lineScore += 8
			} else if strings.Contains(line, base) {
				lineScore += 6
			}
			if lineScore == 0 {
				continue
			}
			lineScore += objectiveReadmeCueWeight(line)
			if lineScore > score {
				score = lineScore
				rationale = "repository documentation points to this file"
				evidence = strings.TrimSpace(line)
				if lineScore >= 9 {
					confidence = "high"
				} else {
					confidence = "medium"
				}
			}
		}
		if score == 0 && slices.Contains(expectedPaths, path) {
			score = 3
			rationale = "repo profile expected file"
			evidence = path
			confidence = "low"
		}
		citations, precedentReason, precedentEvidence := objectivePlannerPrecedentSupport(planningPrecedents, lowerPath, base)
		if len(citations) > 0 {
			score += 4
			if rationale == "" {
				rationale = precedentReason
			} else {
				rationale += "; " + precedentReason
			}
			if evidence == "" {
				evidence = precedentEvidence
			}
			if confidence == "low" {
				confidence = "medium"
			}
		}
		if score == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			path:       path,
			score:      score,
			rationale:  rationale,
			evidence:   evidence,
			confidence: confidence,
			citations:  citations,
		})
	}
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return strings.Compare(a.path, b.path)
	})
	if len(candidates) == 0 {
		return nil, nil
	}
	selectedLimit := 2
	selected := make([]domain.ScopeHypothesis, 0, min(selectedLimit, len(candidates)))
	rejected := make([]domain.ScopeHypothesis, 0, max(len(candidates)-selectedLimit, 0))
	winner := candidates[0]
	for idx, item := range candidates {
		hypothesis := domain.ScopeHypothesis{
			Path:         item.path,
			Rationale:    item.rationale,
			Evidence:     item.evidence,
			Confidence:   item.confidence,
			CitationRefs: append([]string{}, item.citations...),
			Rank:         idx + 1,
			Score:        item.score,
		}
		if idx < selectedLimit {
			hypothesis.Disposition = "selected"
			selected = append(selected, hypothesis)
			continue
		}
		hypothesis.Disposition = "rejected"
		hypothesis.RejectedBecause = rejectedScopeReason(item.path, item.score, winner.path, winner.score, idx+1)
		rejected = append(rejected, hypothesis)
	}
	return selected, rejected
}

func objectivePlannerPrecedentSupport(precedents []map[string]any, lowerPath, base string) ([]string, string, string) {
	if len(precedents) == 0 {
		return nil, "", ""
	}
	citations := make([]string, 0, 2)
	evidence := ""
	for _, item := range precedents {
		summary := strings.ToLower(strings.TrimSpace(asString(item["summary"])))
		sourceRef := strings.TrimSpace(asString(item["source_ref"]))
		if summary == "" || sourceRef == "" {
			continue
		}
		if !strings.Contains(summary, lowerPath) && !strings.Contains(summary, base) {
			continue
		}
		citations = append(citations, sourceRef)
		if evidence == "" {
			evidence = strings.TrimSpace(asString(item["summary"]))
		}
		if len(citations) >= 2 {
			break
		}
	}
	if len(citations) == 0 {
		return nil, "", ""
	}
	return uniqueObjectivePaths(citations), "retrieved precedent supports this path", evidence
}

func rejectedScopeReason(path string, score int, winnerPath string, winnerScore int, rank int) string {
	if winnerPath == "" {
		return fmt.Sprintf("ranked %d and excluded from the first-pass scope shortlist", rank)
	}
	if score < winnerScore {
		return fmt.Sprintf("ranked below %s because its evidence score (%d) was weaker than the winner (%d)", winnerPath, score, winnerScore)
	}
	if score == winnerScore && path != winnerPath {
		return fmt.Sprintf("rank tie lost to %s; the planner kept the earlier top-ranked path as the safer first target", winnerPath)
	}
	return fmt.Sprintf("ranked %d and excluded from the first-pass scope shortlist", rank)
}

func scopeHypothesisPaths(hypotheses []domain.ScopeHypothesis) []string {
	out := make([]string, 0, len(hypotheses))
	for _, item := range hypotheses {
		if path := strings.TrimSpace(item.Path); path != "" {
			out = append(out, path)
		}
	}
	return uniqueObjectivePaths(out)
}

func readObjectiveReadme(workspaceRoot string) string {
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(workspaceRoot), "README.md"))
	if err != nil {
		return ""
	}
	text := string(raw)
	if len(text) > 1200 {
		text = text[:1200]
	}
	return text
}

func listObjectiveCandidateFiles(workspaceRoot string) []string {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return nil
	}
	entries := make([]string, 0, 24)
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
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
		if !isObjectiveInterestingFile(rel) {
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

func objectiveDocumentationPaths(workspaceRoot string, bootstrap bool) []string {
	candidates := listObjectiveCandidateFiles(workspaceRoot)
	out := make([]string, 0, 4)
	for _, path := range candidates {
		if objectiveIsDocumentationPath(path) {
			out = append(out, path)
		}
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 && bootstrap {
		out = append(out, "README.md")
	}
	return uniqueObjectivePaths(out)
}

func objectiveIsDocumentationPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	if path == "readme.md" || strings.HasPrefix(path, "docs/") {
		return true
	}
	return strings.HasSuffix(path, ".md")
}

func objectiveInfraPaths(workspaceRoot string, bootstrap bool) []string {
	candidates := listObjectiveCandidateFiles(workspaceRoot)
	out := make([]string, 0, 4)
	for _, path := range candidates {
		if objectiveIsInfraPath(path) {
			out = append(out, path)
		}
		if len(out) >= 3 {
			break
		}
	}
	if len(out) == 0 && bootstrap {
		out = append(out, "Dockerfile")
	}
	return uniqueObjectivePaths(out)
}

func objectiveDependencyPaths(workspaceRoot string, bootstrap bool) []string {
	candidates := listObjectiveCandidateFiles(workspaceRoot)
	out := make([]string, 0, 4)
	for _, path := range candidates {
		if objectiveIsDependencyPath(path) {
			out = append(out, path)
		}
	}
	slices.SortStableFunc(out, func(a, b string) int {
		if dependencyPathPriority(a) != dependencyPathPriority(b) {
			return dependencyPathPriority(a) - dependencyPathPriority(b)
		}
		return strings.Compare(a, b)
	})
	if len(out) > 3 {
		out = out[:3]
	}
	if len(out) == 0 && bootstrap {
		out = append(out, "package.json")
	}
	return uniqueObjectivePaths(out)
}

func dependencyPathPriority(path string) int {
	switch filepath.Base(strings.ToLower(strings.TrimSpace(path))) {
	case "package.json", "go.mod", "requirements.txt", "pyproject.toml", "cargo.toml", "gemfile", "composer.json":
		return 0
	case "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "go.sum", "poetry.lock", "cargo.lock", "gemfile.lock", "composer.lock":
		return 1
	default:
		return 2
	}
}

func objectiveIsInfraPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	base := filepath.Base(path)
	switch {
	case base == "dockerfile":
		return true
	case strings.HasPrefix(path, ".github/workflows/"):
		return true
	case strings.Contains(path, "docker-compose"):
		return true
	case strings.Contains(path, "compose.") && (strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".yaml")):
		return true
	case strings.HasSuffix(path, ".env") || strings.Contains(path, ".env."):
		return true
	case strings.HasPrefix(path, "deploy/"), strings.HasPrefix(path, "deployment/"), strings.HasPrefix(path, "infra/"), strings.HasPrefix(path, "k8s/"), strings.HasPrefix(path, "helm/"), strings.HasPrefix(path, "config/"):
		return true
	case base == "docker-compose.yml" || base == "docker-compose.yaml":
		return true
	case strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".toml"):
		return true
	default:
		return false
	}
}

func objectiveIsDependencyPath(path string) bool {
	path = strings.ToLower(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	switch filepath.Base(path) {
	case "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "requirements.txt", "pyproject.toml", "poetry.lock", "cargo.toml", "cargo.lock", "gemfile", "gemfile.lock", "composer.json", "composer.lock":
		return true
	default:
		return false
	}
}

func isObjectiveInterestingFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".java", ".kt", ".rs", ".rb", ".php", ".md", ".txt", ".json", ".toml", ".yaml", ".yml":
		return true
	default:
		return strings.EqualFold(filepath.Base(path), "Dockerfile")
	}
}

func containsExactPath(goal, candidate string) bool {
	return strings.Contains(goal, candidate)
}

func objectivePrimaryCodeCue(goal, lowerPath string) bool {
	if objectiveIsDocumentationPath(lowerPath) || objectiveIsInfraPath(lowerPath) || objectiveIsDependencyPath(lowerPath) {
		return false
	}
	return strings.Contains(goal, "fix") ||
		strings.Contains(goal, "implement") ||
		strings.Contains(goal, "repair") ||
		strings.Contains(goal, "change") ||
		strings.Contains(goal, "update")
}

func objectiveReadmeCueWeight(line string) int {
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

func uniqueObjectivePaths(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

type objectiveBootstrapProfile struct {
	Enabled     bool
	repoProfile repoWorkflowProfile
}

func detectObjectiveBootstrapProfile(workspaceRoot, title, objective string) objectiveBootstrapProfile {
	if !shouldBootstrapObjectiveWorkspace(workspaceRoot) {
		return objectiveBootstrapProfile{}
	}
	projectType, stack := inferObjectiveScaffoldType(objective)
	repoName := sanitizeProjectName(filepath.Base(strings.TrimSpace(workspaceRoot)))
	if repoName == "" {
		repoName = sanitizeProjectName(title)
	}
	if repoName == "" {
		repoName = "objective-workspace"
	}
	return objectiveBootstrapProfile{
		Enabled: true,
		repoProfile: repoWorkflowProfile{
			projectFlow:   "greenfield_" + projectType,
			projectType:   projectType,
			stack:         stack,
			repoName:      repoName,
			projectRoot:   ".",
			testFocus:     "greenfield objective bootstrap",
			testCommand:   defaultInitiativeTestCommand(projectType, stack),
			expected:      expectedInitiativeFiles(projectType),
			coderTool:     "scaffold_project",
			coderSummary:  "Bootstrap a minimal runnable project in place before applying objective-specific edits.",
			language:      firstNonEmptyString(strings.TrimSpace(stack), "python"),
			problemDomain: "greenfield objective execution",
		},
	}
}

func shouldBootstrapObjectiveWorkspace(workspaceRoot string) bool {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return false
	}
	return len(listObjectiveCandidateFiles(root)) == 0
}

func inferObjectiveScaffoldType(objective string) (string, string) {
	goal := strings.ToLower(strings.TrimSpace(objective))
	switch {
	case strings.Contains(goal, "landing page") || strings.Contains(goal, "frontend") || strings.Contains(goal, "web") || strings.Contains(goal, "ui"):
		return "web_small", "static"
	case strings.Contains(goal, "api") || strings.Contains(goal, "http") || strings.Contains(goal, "endpoint"):
		return "api_http", "python"
	case strings.Contains(goal, "worker") || strings.Contains(goal, "background job") || strings.Contains(goal, "queue"):
		return "worker_background", "python"
	case strings.Contains(goal, "regression") || strings.Contains(goal, "debug") || strings.Contains(goal, "bugfix"):
		return "debug_regression", "python"
	case strings.Contains(goal, "library") || strings.Contains(goal, "package") || strings.Contains(goal, "module"):
		return "toy_repo", "python"
	default:
		return "cli_simple", "python"
	}
}

func objectiveExecutionBackend(needsBootstrap bool) string {
	if needsBootstrap {
		return "local_bridge+aider-task"
	}
	return "aider-task"
}

func objectiveChangeStrategy(needsBootstrap bool) string {
	if needsBootstrap {
		return "Research the empty workspace, scaffold a minimal runnable project in place, edit through aider-task, run validation commands, then review diff and evidence."
	}
	return "Research the repository, edit through aider-task, run validation commands, then review diff and evidence."
}
