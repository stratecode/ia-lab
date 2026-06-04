package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	initiativesvc "github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

const defaultObjectiveMaxIterations = 3

func (s *Server) createObjective(w http.ResponseWriter, r *http.Request) {
	if s.Initiatives == nil || s.Postgres == nil {
		writeDetail(w, http.StatusServiceUnavailable, "objective service is not configured")
		return
	}
	var body domain.ObjectiveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	body.Objective = strings.TrimSpace(body.Objective)
	body.WorkspaceRoot = strings.TrimSpace(body.WorkspaceRoot)
	body.CreatedBy = strings.TrimSpace(body.CreatedBy)
	if body.Title == "" {
		body.Title = truncateTitle(body.Objective, 80)
	}
	if body.Title == "" || body.Objective == "" || body.WorkspaceRoot == "" {
		writeDetail(w, http.StatusBadRequest, "title, objective and workspace_root are required")
		return
	}
	if body.ExecutionMode == "" {
		body.ExecutionMode = domain.InitiativeExecutionModeSelective
	}
	initiativeRecord, err := s.Postgres.CreateInitiative(r.Context(), store.CreateInitiativeParams{
		Title:         body.Title,
		WorkspaceRoot: body.WorkspaceRoot,
		Goal:          body.Objective,
		CreatedBy:     firstNonEmptyString(body.CreatedBy, "objective"),
		ExecutionMode: body.ExecutionMode,
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	contract, err := s.Initiatives.GenerateObjectiveExecutionContract(r.Context(), initiativesvc.ObjectiveInput{
		Title:         body.Title,
		Objective:     body.Objective,
		WorkspaceRoot: body.WorkspaceRoot,
		CreatedBy:     body.CreatedBy,
	})
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	if _, err := s.persistObjectiveContractArtifact(r.Context(), initiativeRecord.ID, contract); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks, err := s.materializeObjectiveWorkItems(r.Context(), initiativeRecord, contract)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.queueReadyObjectiveTasks(r.Context(), initiativeRecord.ID); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Postgres.ReconcileInitiativeExecution(r.Context(), initiativeRecord.ID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, domain.ObjectiveResponse{
		Initiative: updated,
		Contract:   contract,
		Tasks:      tasks,
		Total:      len(tasks),
	})
}

func (s *Server) persistObjectiveContractArtifact(ctx context.Context, initiativeID string, contract domain.ExecutionContract) (string, error) {
	raw, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return "", err
	}
	return s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: "objective_execution_contract",
		Title:        stringPtr("Objective execution contract"),
		MediaType:    stringPtr("application/json"),
		ContentText:  stringPtr(string(raw)),
		Metadata: map[string]any{
			"initiative_id":  initiativeID,
			"entrypoint":     "objectives",
			"workspace_root": contract.WorkspaceRoot,
			"repo_profile":   contract.RepoProfile,
		},
	})
}

func (s *Server) materializeObjectiveWorkItems(ctx context.Context, item *domain.InitiativeResponse, contract domain.ExecutionContract) ([]domain.TaskResponse, error) {
	if item == nil {
		return nil, fmt.Errorf("initiative is required")
	}
	tasks := make([]domain.TaskResponse, 0, len(contract.WorkItems))
	for idx, workItem := range contract.WorkItems {
		metadata := map[string]any{
			"workspace_root":       contract.WorkspaceRoot,
			"initiative_goal":      contract.NormalizedObjective,
			"objective_title":      contract.Title,
			"execution_backend":    contract.ExecutionBackend,
			"execution_contract":   contract,
			"work_item_id":         workItem.ID,
			"work_item_kind":       string(workItem.Kind),
			"depends_on":           workItem.DependsOn,
			"definition_of_done":   workItem.DefinitionOfDone,
			"validation_commands":  workItem.ValidationCommands,
			"suspected_paths":      workItem.SuspectedPaths,
			"requires_approval":    workItem.ApprovalRequired,
			"objective_entrypoint": true,
		}
		for key, value := range workItem.Metadata {
			metadata[key] = value
		}
		metadata["workspace_root"] = contract.WorkspaceRoot
		metadata["initiative_goal"] = contract.NormalizedObjective
		metadata["objective_title"] = contract.Title
		metadata["execution_backend"] = contract.ExecutionBackend
		metadata["execution_contract"] = contract
		metadata["work_item_id"] = workItem.ID
		metadata["work_item_kind"] = string(workItem.Kind)
		metadata["depends_on"] = workItem.DependsOn
		metadata["definition_of_done"] = workItem.DefinitionOfDone
		metadata["validation_commands"] = workItem.ValidationCommands
		metadata["suspected_paths"] = workItem.SuspectedPaths
		metadata["requires_approval"] = workItem.ApprovalRequired
		metadata["objective_entrypoint"] = true
		if workItem.ToolRequest != nil {
			metadata["tool_request"] = workItem.ToolRequest
		}
		task, err := s.Postgres.CreateTask(ctx, store.CreateTaskParams{
			Description:         workItem.Description,
			Metadata:            metadata,
			Priority:            priorityForWorkItem(workItem),
			AssignedAgent:       workItem.AssignedAgent,
			PlannedAgent:        agentPtr(workItem.AssignedAgent),
			ExecutionTarget:     workItem.ExecutionTarget,
			Entrypoint:          "objectives",
			InitiativeID:        &item.ID,
			TaskKind:            domain.TaskKindPlanStep,
			QueueOnCreate:       false,
			WorkspacePath:       nil,
			AllowedCapabilities: nil,
		})
		if err != nil {
			return nil, err
		}
		if _, err := s.Postgres.LinkInitiativeTask(ctx, store.LinkInitiativeTaskParams{
			InitiativeID:  item.ID,
			TaskID:        task.ID,
			PhaseOrigin:   "objective",
			Epic:          cleanStringPtr(string(workItem.Kind)),
			LaunchGroup:   cleanStringPtr(string(workItem.Kind)),
			ExecutionMode: workItem.ExecutionMode,
			LaunchOrder:   (idx + 1) * 10,
		}); err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, nil
}

func (s *Server) queueReadyObjectiveTasks(ctx context.Context, initiativeID string) error {
	items, err := s.Postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return err
	}
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{}
	for _, item := range items {
		if workItemID := strings.TrimSpace(asString(item.Task.Metadata["work_item_id"])); workItemID != "" {
			byWorkItemID[workItemID] = item
		}
	}
	for _, item := range items {
		if item.Task.State != domain.TaskStateCreated {
			continue
		}
		if !objectiveDependenciesSatisfied(item.Task.Metadata, byWorkItemID) {
			continue
		}
		if s.ContextBuilder != nil {
			task, err := s.Postgres.GetTask(ctx, item.TaskID)
			if err != nil {
				return err
			}
			if task != nil {
				contextPackage, err := s.ContextBuilder.BuildForTask(ctx, task)
				if err != nil {
					if patchErr := s.Postgres.PatchTaskMetadata(ctx, item.TaskID, map[string]any{
						"context_build_error": err.Error(),
					}); patchErr != nil {
						return patchErr
					}
				}
				if contextPackage != nil && len(contextPackage.Chunks) > 0 {
					if err := s.Postgres.PatchTaskMetadata(ctx, item.TaskID, map[string]any{"context_package": contextPackage}); err != nil {
						return err
					}
				}
			}
		}
		queuedTask, err := s.Postgres.QueueTaskForLaunch(ctx, item.TaskID, "objectives:auto", "Queued objective work item after dependency resolution")
		if err != nil {
			return err
		}
		if queuedTask.ExecutionTarget != domain.ExecutionTargetLocal && queuedTask.AssignedAgent != nil {
			if err := s.Redis.EnqueueTask(ctx, queuedTask.ID, *queuedTask.AssignedAgent, queuedTask.Priority); err != nil {
				return err
			}
		}
	}
	return nil
}

func objectiveDependenciesSatisfied(metadata map[string]any, byWorkItemID map[string]domain.InitiativeTaskLinkResponse) bool {
	dependsOn := anyStringSliceHTTPDefault(metadata["depends_on"], []string{})
	if len(dependsOn) == 0 {
		return true
	}
	for _, dep := range dependsOn {
		item, ok := byWorkItemID[dep]
		if !ok || item.Task.State != domain.TaskStateCompleted {
			return false
		}
	}
	return true
}

func priorityForWorkItem(item domain.WorkItem) domain.Priority {
	switch item.Kind {
	case domain.WorkItemKindEdit:
		return domain.PriorityHigh
	case domain.WorkItemKindReview:
		return domain.PriorityNormal
	case domain.WorkItemKindValidate:
		return domain.PriorityNormal
	default:
		return domain.PriorityNormal
	}
}

func agentPtr(value domain.AgentType) *domain.AgentType {
	v := value
	return &v
}

func (s *Server) startObjectiveRepairCycle(ctx context.Context, task *domain.TaskResponse, result domain.LocalBridgeResultRequest) error {
	if !shouldStartObjectiveRepairCycle(task, result) {
		return nil
	}
	iteration := objectiveIteration(task.Metadata)
	maxIterations := objectiveMaxIterations(task.Metadata)
	if iteration >= maxIterations {
		return nil
	}
	if task == nil || task.InitiativeID == nil {
		return nil
	}
	initiative, err := s.Postgres.GetInitiative(ctx, *task.InitiativeID)
	if err != nil {
		return err
	}
	if initiative == nil {
		return fmt.Errorf("initiative %s not found", *task.InitiativeID)
	}
	contract, err := decodeObjectiveExecutionContract(task.Metadata["execution_contract"])
	if err != nil {
		return err
	}
	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		return err
	}
	if err := s.cancelSupersededObjectiveDependents(ctx, *task.InitiativeID, task); err != nil {
		return err
	}
	repairContract := contract
	repairContract.WorkItems = workItems
	if _, err := s.materializeObjectiveWorkItems(ctx, initiative, repairContract); err != nil {
		return err
	}
	return s.queueReadyObjectiveTasks(ctx, initiative.ID)
}

func (s *Server) cancelSupersededObjectiveDependents(ctx context.Context, initiativeID string, task *domain.TaskResponse) error {
	if task == nil {
		return nil
	}
	currentWorkItemID := strings.TrimSpace(asString(task.Metadata["work_item_id"]))
	if currentWorkItemID == "" {
		return nil
	}
	currentIteration := objectiveIteration(task.Metadata)
	items, err := s.Postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return err
	}
	superseded := map[string]struct{}{currentWorkItemID: {}}
	changed := true
	for changed {
		changed = false
		for _, item := range items {
			if item.Task.ID == task.ID {
				continue
			}
			if objectiveIteration(item.Task.Metadata) != currentIteration {
				continue
			}
			workItemID := strings.TrimSpace(asString(item.Task.Metadata["work_item_id"]))
			if workItemID == "" {
				continue
			}
			if _, seen := superseded[workItemID]; seen {
				continue
			}
			dependsOn := anyStringSliceHTTPDefault(item.Task.Metadata["depends_on"], []string{})
			if len(dependsOn) == 0 {
				continue
			}
			dependsOnSuperseded := false
			for _, dep := range dependsOn {
				if _, ok := superseded[dep]; ok {
					dependsOnSuperseded = true
					break
				}
			}
			if !dependsOnSuperseded {
				continue
			}
			switch item.Task.State {
			case domain.TaskStateCreated, domain.TaskStateQueued, domain.TaskStateAssigned, domain.TaskStateWaitingApproval:
				if err := s.Postgres.PatchTaskMetadata(ctx, item.Task.ID, map[string]any{
					"superseded_by_repair_cycle": true,
					"superseded_iteration":       currentIteration + 1,
				}); err != nil {
					return err
				}
				if err := s.Postgres.CancelTask(ctx, item.Task.ID, "objectives:auto", fmt.Sprintf("Superseded by objective repair iteration %d", currentIteration+1)); err != nil {
					return err
				}
			}
			superseded[workItemID] = struct{}{}
			changed = true
		}
	}
	return nil
}

func shouldStartObjectiveRepairCycle(task *domain.TaskResponse, result domain.LocalBridgeResultRequest) bool {
	if task == nil || task.InitiativeID == nil {
		return false
	}
	if !objectiveAsBool(task.Metadata["objective_entrypoint"]) {
		return false
	}
	if objectiveIteration(task.Metadata) >= objectiveMaxIterations(task.Metadata) {
		return false
	}
	kind := strings.TrimSpace(asString(task.Metadata["work_item_kind"]))
	status := strings.ToLower(strings.TrimSpace(result.Status))
	switch kind {
	case string(domain.WorkItemKindValidate):
		return status != "success"
	case string(domain.WorkItemKindReview):
		return strings.EqualFold(derefStringPtr(result.ReviewDecision), "changes_requested")
	default:
		return false
	}
}

func buildObjectiveRepairCycle(contract domain.ExecutionContract, task *domain.TaskResponse, result domain.LocalBridgeResultRequest) ([]domain.WorkItem, error) {
	if task == nil {
		return nil, fmt.Errorf("task is required")
	}
	iteration := objectiveIteration(task.Metadata) + 1
	projectRequest, _ := task.Metadata["project_request"].(map[string]any)
	workspaceRoot := firstNonEmptyString(strings.TrimSpace(contract.WorkspaceRoot), strings.TrimSpace(asString(task.Metadata["workspace_root"])))
	if workspaceRoot == "" {
		return nil, fmt.Errorf("workspace_root is required to build repair cycle")
	}
	projectRoot := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["project_root"])), ".")
	projectName := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["project_name"])), filepath.Base(workspaceRoot), "repo")
	validationCommands := contract.ValidationCommands
	if len(validationCommands) == 0 {
		validationCommands = anyStringSliceHTTPDefault(task.Metadata["validation_commands"], []string{"git", "diff", "--no-ext-diff"})
	}
	suspectedPaths := contract.SuspectedPaths
	if len(suspectedPaths) == 0 {
		suspectedPaths = anyStringSliceHTTPDefault(task.Metadata["suspected_paths"], []string{})
	}
	feedback := objectiveRepairFeedback(result)
	baseMetadata := cloneMap(task.Metadata)
	if baseMetadata == nil {
		baseMetadata = map[string]any{}
	}
	baseMetadata["objective_iteration"] = iteration
	baseMetadata["objective_max_iterations"] = objectiveMaxIterations(task.Metadata)
	baseMetadata["objective_repair_origin_task_id"] = task.ID
	baseMetadata["objective_repair_feedback"] = feedback
	baseMetadata["review_comments"] = result.ReviewComments
	if result.ReviewDecision != nil {
		baseMetadata["review_decision"] = strings.TrimSpace(*result.ReviewDecision)
	}
	if projectRequest == nil {
		projectRequest = map[string]any{}
	}
	replanProjectRequest := cloneMap(projectRequest)
	replanProjectRequest["goal"] = fmt.Sprintf("%s\n\nAddress the latest repair feedback:\n%s", contract.NormalizedObjective, feedback)
	replanProjectRequest["test_focus"] = fmt.Sprintf("objective repair iteration %d", iteration)
	replanProjectRequest["test_command"] = validationCommands
	replanProjectRequest["expected_files"] = suspectedPaths

	replanMetadata := cloneMap(baseMetadata)
	replanMetadata["project_request"] = replanProjectRequest
	editMetadata := cloneMap(baseMetadata)
	editMetadata["project_request"] = replanProjectRequest
	validateMetadata := cloneMap(baseMetadata)
	validateMetadata["project_request"] = replanProjectRequest
	reviewMetadata := cloneMap(baseMetadata)
	reviewMetadata["project_request"] = replanProjectRequest

	replanID := fmt.Sprintf("replan-%d", iteration)
	editID := fmt.Sprintf("edit-%d", iteration)
	validateID := fmt.Sprintf("validate-%d", iteration)
	reviewID := fmt.Sprintf("review-%d", iteration)
	editDescription := fmt.Sprintf("%s\n\nAddress this repair feedback before finishing:\n%s", contract.NormalizedObjective, feedback)

	workItems := []domain.WorkItem{
		{
			ID:               replanID,
			Kind:             domain.WorkItemKindReplan,
			Title:            fmt.Sprintf("Replan objective iteration %d", iteration),
			Description:      fmt.Sprintf("Summarize the latest validation and review findings for repair iteration %d.", iteration),
			AssignedAgent:    domain.AgentTypeResearcher,
			ExecutionMode:    domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:  domain.ExecutionTargetLocal,
			Backend:          "local_bridge",
			DefinitionOfDone: "Repair findings are summarized and persisted for the next edit attempt.",
			SuspectedPaths:   suspectedPaths,
			ToolRequest: map[string]any{
				"tool":                "research_project",
				"project_request":     replanProjectRequest,
				"prior_findings":      result.ReviewComments,
				"prior_test_results":  result.TestResults,
				"prior_changed_files": result.ChangedFiles,
				"repair_feedback":     feedback,
			},
			Metadata: replanMetadata,
		},
		{
			ID:                 editID,
			Kind:               domain.WorkItemKindEdit,
			Title:              fmt.Sprintf("Apply repair iteration %d with aider", iteration),
			Description:        editDescription,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          []string{replanID},
			ApprovalRequired:   true,
			DefinitionOfDone:   "Aider addresses the latest findings and produces a fresh diff.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool": "run_command",
				"argv": []string{
					"aider-task",
					"--task-id", fmt.Sprintf("%s-%d", task.ID, iteration),
					"--repo", projectName,
					"--description", editDescription,
				},
			},
			Metadata: editMetadata,
		},
		{
			ID:                 validateID,
			Kind:               domain.WorkItemKindValidate,
			Title:              fmt.Sprintf("Run validation for iteration %d", iteration),
			Description:        "Execute validation again after the repair edit.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{editID},
			DefinitionOfDone:   "Validation output is persisted for the repaired change set.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool": "run_tests",
				"argv": validationCommands,
			},
			Metadata: validateMetadata,
		},
		{
			ID:                 reviewID,
			Kind:               domain.WorkItemKindReview,
			Title:              fmt.Sprintf("Review repair iteration %d", iteration),
			Description:        "Review the repaired diff and validation evidence.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{validateID},
			DefinitionOfDone:   "Review decides whether the repaired iteration is approved.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool":         "review_workspace",
				"project_root": projectRoot,
				"test_command": validationCommands,
				"execution_contract": map[string]any{
					"title":                contract.Title,
					"normalized_objective": contract.NormalizedObjective,
					"workspace_root":       workspaceRoot,
					"iteration":            iteration,
					"repair_feedback":      feedback,
				},
			},
			Metadata: reviewMetadata,
		},
	}
	return workItems, nil
}

func objectiveRepairFeedback(result domain.LocalBridgeResultRequest) string {
	parts := make([]string, 0, 4)
	if summary := strings.TrimSpace(derefStringPtr(result.Summary)); summary != "" {
		parts = append(parts, summary)
	}
	if message := strings.TrimSpace(derefStringPtr(result.ErrorMessage)); message != "" {
		parts = append(parts, message)
	}
	for _, comment := range result.ReviewComments {
		message := strings.TrimSpace(asString(comment["message"]))
		if message == "" {
			continue
		}
		severity := strings.TrimSpace(asString(comment["severity"]))
		if severity != "" {
			parts = append(parts, fmt.Sprintf("[%s] %s", severity, message))
			continue
		}
		parts = append(parts, message)
	}
	if len(parts) == 0 {
		return "Validation or review reported changes are required."
	}
	return strings.Join(parts, "\n")
}

func objectiveIteration(metadata map[string]any) int {
	iteration := objectiveAsInt(metadata["objective_iteration"], 1)
	if iteration <= 0 {
		return 1
	}
	return iteration
}

func objectiveMaxIterations(metadata map[string]any) int {
	value := objectiveAsInt(metadata["objective_max_iterations"], defaultObjectiveMaxIterations)
	if value <= 0 {
		return defaultObjectiveMaxIterations
	}
	return value
}

func decodeObjectiveExecutionContract(value any) (domain.ExecutionContract, error) {
	var contract domain.ExecutionContract
	raw, err := json.Marshal(value)
	if err != nil {
		return contract, err
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		return contract, err
	}
	return contract, nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func objectiveAsBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	default:
		return false
	}
}

func objectiveAsInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return fallback
	}
}
