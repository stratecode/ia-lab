package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

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
	if body.TimeBudgetSeconds < 0 {
		writeDetail(w, http.StatusBadRequest, "time_budget_seconds must be greater than or equal to zero")
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
	planningContext, err := s.buildObjectivePlanningContext(r.Context(), initiativeRecord, body)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	planningRetrievalArtifactID := ""
	if planningContext != nil && len(planningContext.Chunks) > 0 {
		planningRetrievalArtifactID, err = s.persistInitiativeRetrievalPacketArtifact(r.Context(), initiativeRecord, string(domain.AgentTypePlanner), planningContext)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	contract, err := s.Initiatives.GenerateObjectiveExecutionContract(r.Context(), initiativesvc.ObjectiveInput{
		Title:                       body.Title,
		Objective:                   body.Objective,
		WorkspaceRoot:               body.WorkspaceRoot,
		CreatedBy:                   body.CreatedBy,
		TimeBudgetSeconds:           body.TimeBudgetSeconds,
		PlanningContext:             planningContext,
		PlanningRetrievalArtifactID: planningRetrievalArtifactID,
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

func (s *Server) buildObjectivePlanningContext(ctx context.Context, initiative *domain.InitiativeResponse, body domain.ObjectiveRequest) (*domain.ContextPackage, error) {
	_ = s
	_ = ctx
	_ = initiative
	_ = body
	return nil, nil
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
	objectiveStartedAt := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = objectiveStartedAt
	} else {
		objectiveStartedAt = item.CreatedAt.UTC()
	}
	deadlineAt := objectiveStartedAt
	hasDeadline := contract.TimeBudgetSeconds > 0
	if hasDeadline {
		deadlineAt = objectiveStartedAt.Add(time.Duration(contract.TimeBudgetSeconds) * time.Second)
	}
	for idx, workItem := range contract.WorkItems {
		metadata := map[string]any{
			"workspace_root":                contract.WorkspaceRoot,
			"initiative_goal":               contract.NormalizedObjective,
			"objective_title":               contract.Title,
			"execution_backend":             contract.ExecutionBackend,
			"execution_contract":            contract,
			"work_item_id":                  workItem.ID,
			"work_item_kind":                string(workItem.Kind),
			"depends_on":                    workItem.DependsOn,
			"definition_of_done":            workItem.DefinitionOfDone,
			"validation_commands":           workItem.ValidationCommands,
			"suspected_paths":               workItem.SuspectedPaths,
			"requires_approval":             workItem.ApprovalRequired,
			"objective_entrypoint":          true,
			"objective_time_budget_seconds": contract.TimeBudgetSeconds,
			"objective_started_at":          objectiveStartedAt.Format(time.RFC3339Nano),
		}
		if hasDeadline {
			metadata["objective_deadline_at"] = deadlineAt.Format(time.RFC3339Nano)
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
		task, err := s.Postgres.GetTask(ctx, item.TaskID)
		if err != nil {
			return err
		}
		if objectiveTimeBudgetExceeded(task.Metadata, time.Now().UTC()) {
			if err := s.failObjectiveTaskDueToTimeBudget(ctx, task); err != nil {
				return err
			}
			continue
		}
		if patch := objectiveQueueMetadataPatch(task, byWorkItemID); len(patch) > 0 {
			if err := s.Postgres.PatchTaskMetadata(ctx, item.TaskID, patch); err != nil {
				return err
			}
			task, err = s.Postgres.GetTask(ctx, item.TaskID)
			if err != nil {
				return err
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

func objectiveQueueMetadataPatch(task *domain.TaskResponse, byWorkItemID map[string]domain.InitiativeTaskLinkResponse) map[string]any {
	if task == nil {
		return nil
	}
	if strings.TrimSpace(asString(task.Metadata["work_item_kind"])) != string(domain.WorkItemKindEdit) {
		return nil
	}
	dependsOn := anyStringSliceHTTPDefault(task.Metadata["depends_on"], []string{})
	if len(dependsOn) == 0 {
		return nil
	}
	patch := map[string]any{}
	for _, depID := range dependsOn {
		link, ok := byWorkItemID[depID]
		if !ok || link.Task.State != domain.TaskStateCompleted {
			continue
		}
		switch strings.TrimSpace(asString(link.Task.Metadata["work_item_kind"])) {
		case string(domain.WorkItemKindResearch), string(domain.WorkItemKindReplan):
			applyObjectiveResearchPatch(patch, task, link)
		case string(domain.WorkItemKindAnalyze):
			applyObjectiveAnalysisPatch(patch, task, link)
		case string(domain.WorkItemKindValidate):
			if objectiveAsBool(link.Task.Metadata["objective_baseline_validation"]) {
				applyObjectiveBaselineValidatePatch(patch, link)
			}
		}
	}
	if len(patch) == 0 {
		return nil
	}
	return patch
}

func applyObjectiveResearchPatch(patch map[string]any, task *domain.TaskResponse, link domain.InitiativeTaskLinkResponse) {
	payload := decodeObjectiveResearchPayload(link.Task.Results)
	if len(payload) == 0 {
		return
	}
	if scope := anyStringSliceHTTPDefault(payload["recommended_scope_paths"], nil); len(scope) > 0 {
		scope = objectiveSanitizeScopePaths(scope)
		scope = objectivePrioritizeWorkstreamScope(task, scope)
		patch["suspected_paths"] = scope
		projectRequest := cloneMetadataMap(task.Metadata["project_request"])
		projectRequest["expected_files"] = scope
		patch["project_request"] = projectRequest
	}
	if brief := strings.TrimSpace(asString(payload["next_edit_brief"])); brief != "" {
		patch["objective_research_brief"] = brief
	}
	if recommendations := anyStringSliceHTTPDefault(payload["recommendations"], nil); len(recommendations) > 0 {
		patch["objective_research_recommendations"] = recommendations
	}
	if hypotheses := anyMapSliceHTTPDefault(payload["failure_hypotheses"], nil); len(hypotheses) > 0 {
		patch["objective_failure_hypotheses"] = hypotheses
	}
	if intent := anyMapSliceHTTPDefault(payload["patch_intent"], nil); len(intent) > 0 {
		patch["objective_patch_intent"] = intent
	}
	if precedents := anyMapSliceHTTPDefault(payload["retrieval_precedents"], nil); len(precedents) > 0 {
		patch["objective_retrieval_precedents"] = precedents
	}
	if excluded := anyStringSliceHTTPDefault(payload["excluded_scope_paths"], nil); len(excluded) > 0 {
		patch["excluded_scope_paths"] = excluded
	}
	if resolution := strings.TrimSpace(asString(payload["planner_scope_resolution"])); resolution != "" {
		patch["planner_scope_resolution"] = resolution
	}
	if resolution := strings.TrimSpace(asString(payload["planner_rejected_scope_resolution"])); resolution != "" {
		patch["planner_rejected_scope_resolution"] = resolution
	}
	if reason := strings.TrimSpace(asString(payload["scope_correction_reason"])); reason != "" {
		patch["scope_correction_reason"] = reason
	}
}

func objectiveSanitizeScopePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		switch strings.ToUpper(strings.TrimSpace(path)) {
		case "", "PASS", "FAIL":
			continue
		default:
			filtered = append(filtered, path)
		}
	}
	return objectiveUniqueStrings(filtered)
}

func objectivePrioritizeWorkstreamScope(task *domain.TaskResponse, recommended []string) []string {
	if task == nil || len(recommended) == 0 {
		return recommended
	}
	workstream := strings.TrimSpace(asString(task.Metadata["objective_workstream"]))
	switch workstream {
	case "documentation_sync", "config_sync", "dependency_sync":
	default:
		return recommended
	}
	currentScope := objectiveSanitizeScopePaths(anyStringSliceHTTPDefault(task.Metadata["suspected_paths"], nil))
	if len(currentScope) == 0 {
		return recommended
	}
	return objectiveUniqueStrings(append(append([]string{}, currentScope...), recommended...))
}

func applyObjectiveAnalysisPatch(patch map[string]any, task *domain.TaskResponse, link domain.InitiativeTaskLinkResponse) {
	payload := decodeObjectiveAnalysisPayload(link.Task.Results)
	if len(payload) == 0 {
		return
	}
	findings := anyMapSliceHTTPDefault(payload["findings"], nil)
	if len(findings) > 0 {
		patch["objective_analysis_findings"] = findings
	}
	if summary, ok := payload["summary"].(map[string]any); ok && len(summary) > 0 {
		patch["objective_analysis_summary"] = summary
	}
	scopeFromAnalysis := objectiveAnalysisScopePaths(findings)
	if len(scopeFromAnalysis) > 0 {
		existing := anyStringSliceHTTPDefault(patch["suspected_paths"], anyStringSliceHTTPDefault(task.Metadata["suspected_paths"], nil))
		merged := objectiveUniqueStrings(append(scopeFromAnalysis, existing...))
		patch["suspected_paths"] = merged
		projectRequest := cloneMetadataMap(firstNonNilMap(patch["project_request"], task.Metadata["project_request"]))
		projectRequest["expected_files"] = merged
		patch["project_request"] = projectRequest
	}
}

func applyObjectiveBaselineValidatePatch(patch map[string]any, link domain.InitiativeTaskLinkResponse) {
	results, _ := link.Task.Results.(map[string]any)
	if len(results) == 0 {
		return
	}
	if testResults, ok := results["test_results"].(map[string]any); ok && len(testResults) > 0 {
		patch["objective_baseline_test_results"] = testResults
	}
	if findings := anyMapSliceHTTPDefault(results["findings"], nil); len(findings) > 0 {
		patch["objective_baseline_findings"] = findings
	}
	if summary := strings.TrimSpace(asString(results["summary"])); summary != "" {
		patch["objective_baseline_summary"] = summary
	}
	if stdout := strings.TrimSpace(asString(results["stdout"])); stdout != "" {
		patch["objective_baseline_stdout"] = stdout
	}
}

func decodeObjectiveResearchPayload(results any) map[string]any {
	resultMap, _ := results.(map[string]any)
	if len(resultMap) == 0 {
		return nil
	}
	stdout := strings.TrimSpace(asString(resultMap["stdout"]))
	if stdout == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil
	}
	return payload
}

func decodeObjectiveAnalysisPayload(results any) map[string]any {
	resultMap, _ := results.(map[string]any)
	if len(resultMap) == 0 {
		return nil
	}
	stdout := strings.TrimSpace(asString(resultMap["stdout"]))
	if stdout == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		return nil
	}
	return payload
}

func objectiveAnalysisScopePaths(findings []map[string]any) []string {
	paths := make([]string, 0, len(findings))
	for _, finding := range findings {
		location, _ := finding["location"].(map[string]any)
		path := strings.TrimSpace(asString(location["file"]))
		if path == "" {
			continue
		}
		paths = append(paths, path)
	}
	return objectiveUniqueStrings(paths)
}

func cloneMetadataMap(value any) map[string]any {
	input, _ := value.(map[string]any)
	out := map[string]any{}
	for key, item := range input {
		out[key] = item
	}
	return out
}

func firstNonNilMap(values ...any) any {
	for _, value := range values {
		if candidate, ok := value.(map[string]any); ok && len(candidate) > 0 {
			return candidate
		}
	}
	return map[string]any{}
}

func anyMapSliceHTTPDefault(value any, fallback []map[string]any) []map[string]any {
	switch raw := value.(type) {
	case []map[string]any:
		if len(raw) == 0 {
			return fallback
		}
		return raw
	case []any:
		out := make([]map[string]any, 0, len(raw))
		for _, item := range raw {
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

func (s *Server) failObjectiveTaskDueToTimeBudget(ctx context.Context, task *domain.TaskResponse) error {
	if task == nil || task.InitiativeID == nil {
		return nil
	}
	budgetSeconds, deadline, ok := objectiveTimeBudgetDetails(task.Metadata)
	if !ok {
		return nil
	}
	reason := objectiveTimeBudgetExceededReason(budgetSeconds, deadline)
	patch := map[string]any{
		"objective_stop_reason":              "time_budget_exhausted",
		"objective_time_budget_exhausted":    true,
		"objective_time_budget_exhausted_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.Postgres.PatchTaskMetadata(ctx, task.ID, patch); err != nil {
		return err
	}
	results := map[string]any{
		"status":      "error",
		"stop_reason": "time_budget_exhausted",
		"summary":     reason,
		"error":       reason,
	}
	failed, err := s.Postgres.FailTask(ctx, task.ID, "objectives:auto", reason, reason, results)
	if err != nil {
		return err
	}
	if failed != nil {
		s.indexTask(ctx, failed.ID)
	}
	if err := s.persistObjectiveStopSnapshot(ctx, task, "time_budget_exhausted", reason, deadline, budgetSeconds); err != nil {
		return err
	}
	_, err = s.Postgres.ReconcileInitiativeExecution(ctx, *task.InitiativeID)
	return err
}

func (s *Server) persistObjectiveStopSnapshot(ctx context.Context, task *domain.TaskResponse, stopReason, blockerReason string, deadline time.Time, budgetSeconds int) error {
	if task == nil || task.InitiativeID == nil {
		return nil
	}
	contract, _ := decodeObjectiveExecutionContract(task.Metadata["execution_contract"])
	snapshot := domain.ObjectiveStatusSnapshot{
		InitiativeID:           strings.TrimSpace(*task.InitiativeID),
		ObjectiveTitle:         firstNonEmptyString(strings.TrimSpace(contract.Title), strings.TrimSpace(asString(task.Metadata["objective_title"]))),
		NormalizedObjective:    firstNonEmptyString(strings.TrimSpace(contract.NormalizedObjective), strings.TrimSpace(asString(task.Metadata["initiative_goal"]))),
		CurrentPhase:           string(domain.InitiativePhaseExecution),
		Iteration:              objectiveIteration(task.Metadata),
		MaxIterations:          objectiveMaxIterations(task.Metadata),
		RemainingRetries:       objectiveRemainingRetries(task.Metadata),
		WorkItemKind:           strings.TrimSpace(asString(task.Metadata["work_item_kind"])),
		ResultStatus:           "error",
		NeedsRepairCycle:       false,
		NextExpectedAction:     "block_initiative",
		StopReason:             stopReason,
		BlockerReason:          blockerReason,
		TimeBudgetSeconds:      budgetSeconds,
		RemainingBudgetSeconds: 0,
		ValidationCommands:     anyStringSliceHTTPDefault(task.Metadata["validation_commands"], []string{}),
	}
	if !deadline.IsZero() {
		snapshot.DeadlineAt = deadline.UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: "objective_status_snapshot",
		Title:        stringPtr(fmt.Sprintf("Objective status snapshot %d %s", snapshot.Iteration, snapshot.WorkItemKind)),
		MediaType:    stringPtr("application/json"),
		ContentText:  stringPtr(string(raw)),
		Metadata: map[string]any{
			"initiative_id":            snapshot.InitiativeID,
			"objective_title":          snapshot.ObjectiveTitle,
			"normalized_objective":     snapshot.NormalizedObjective,
			"iteration":                snapshot.Iteration,
			"max_iterations":           snapshot.MaxIterations,
			"work_item_kind":           snapshot.WorkItemKind,
			"result_status":            snapshot.ResultStatus,
			"next_expected_action":     snapshot.NextExpectedAction,
			"remaining_retries":        snapshot.RemainingRetries,
			"blocker_reason":           snapshot.BlockerReason,
			"stop_reason":              snapshot.StopReason,
			"time_budget_seconds":      snapshot.TimeBudgetSeconds,
			"remaining_budget_seconds": snapshot.RemainingBudgetSeconds,
			"deadline_at":              snapshot.DeadlineAt,
			"memory_kind":              "objective_status_snapshot",
		},
	})
	return err
}

func objectiveTimeBudgetDetails(metadata map[string]any) (int, time.Time, bool) {
	budgetSeconds := objectiveAsInt(metadata["objective_time_budget_seconds"], 0)
	if budgetSeconds <= 0 {
		return 0, time.Time{}, false
	}
	deadlineRaw := strings.TrimSpace(asString(metadata["objective_deadline_at"]))
	if deadlineRaw == "" {
		startedRaw := strings.TrimSpace(asString(metadata["objective_started_at"]))
		if startedRaw == "" {
			return budgetSeconds, time.Time{}, false
		}
		startedAt, err := time.Parse(time.RFC3339Nano, startedRaw)
		if err != nil {
			return budgetSeconds, time.Time{}, false
		}
		return budgetSeconds, startedAt.Add(time.Duration(budgetSeconds) * time.Second), true
	}
	deadline, err := time.Parse(time.RFC3339Nano, deadlineRaw)
	if err != nil {
		return budgetSeconds, time.Time{}, false
	}
	return budgetSeconds, deadline, true
}

func objectiveTimeBudgetExceeded(metadata map[string]any, now time.Time) bool {
	_, deadline, ok := objectiveTimeBudgetDetails(metadata)
	if !ok || deadline.IsZero() {
		return false
	}
	return !now.Before(deadline)
}

func objectiveRemainingBudgetSeconds(metadata map[string]any, now time.Time) (int, int, time.Time, bool) {
	budgetSeconds, deadline, ok := objectiveTimeBudgetDetails(metadata)
	if !ok || deadline.IsZero() {
		return 0, 0, time.Time{}, false
	}
	remaining := 0
	if now.Before(deadline) {
		remaining = int(deadline.Sub(now).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	return budgetSeconds, remaining, deadline, true
}

func objectiveTimeBudgetExceededReason(budgetSeconds int, deadline time.Time) string {
	if budgetSeconds <= 0 {
		return "Objective time budget exhausted."
	}
	if deadline.IsZero() {
		return fmt.Sprintf("Objective time budget exhausted after %d seconds.", budgetSeconds)
	}
	return fmt.Sprintf("Objective time budget exhausted after %d seconds (deadline %s).", budgetSeconds, deadline.UTC().Format(time.RFC3339))
}

func objectiveRemainingRetries(metadata map[string]any) int {
	maxIterations := objectiveMaxIterations(metadata)
	iteration := objectiveIteration(metadata)
	if maxIterations < iteration {
		maxIterations = iteration
	}
	remaining := maxIterations - iteration
	if remaining < 0 {
		return 0
	}
	return remaining
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
	if objectiveAsBool(task.Metadata["objective_baseline_validation"]) {
		return false
	}
	if objectiveTimeBudgetExceeded(task.Metadata, time.Now().UTC()) {
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
	if len(result.ChangedFiles) > 0 {
		suspectedPaths = objectiveUniqueStrings(result.ChangedFiles)
	} else {
		suspectedPaths = objectiveUniqueStrings(suspectedPaths)
	}
	feedback := objectiveRepairFeedback(result)
	failureHypotheses := objectiveRepairFailureHypotheses(result, suspectedPaths)
	patchIntent := objectiveRepairPatchIntent(result, suspectedPaths)
	baseMetadata := cloneMap(task.Metadata)
	if baseMetadata == nil {
		baseMetadata = map[string]any{}
	}
	baseMetadata["objective_iteration"] = iteration
	baseMetadata["objective_max_iterations"] = objectiveMaxIterations(task.Metadata)
	baseMetadata["objective_repair_origin_task_id"] = task.ID
	baseMetadata["objective_repair_feedback"] = feedback
	baseMetadata["review_comments"] = result.ReviewComments
	baseMetadata["objective_findings"] = result.Findings
	baseMetadata["objective_failure_hypotheses"] = failureHypotheses
	baseMetadata["objective_patch_intent"] = patchIntent
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
	analyzeID := fmt.Sprintf("analyze-%d", iteration)
	editID := fmt.Sprintf("edit-%d", iteration)
	validateID := fmt.Sprintf("validate-%d", iteration)
	reviewID := fmt.Sprintf("review-%d", iteration)
	editDescription := fmt.Sprintf("%s\n\nAddress this repair feedback before finishing:\n%s", contract.NormalizedObjective, feedback)
	analysisTypes := objectiveRepairAnalysisTypes(result)
	insertAnalysis := shouldInsertObjectiveCodeAnalysis(result, analysisTypes)
	insertBaselineValidate := shouldInsertObjectiveRepairBaselineValidation(result)
	secondaryWorkstreams := objectiveRepairSecondaryWorkstreams(contract)

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
				"prior_findings":      objectivePriorFindings(result),
				"prior_test_results":  result.TestResults,
				"prior_changed_files": result.ChangedFiles,
				"repair_feedback":     feedback,
				"failure_hypotheses":  failureHypotheses,
				"patch_intent":        patchIntent,
			},
			Metadata: replanMetadata,
		},
	}
	editDependsOn := []string{replanID}
	if insertBaselineValidate {
		baselineMetadata := cloneMap(baseMetadata)
		baselineMetadata["project_request"] = replanProjectRequest
		baselineMetadata["objective_baseline_validation"] = true
		baselineMetadata["objective_baseline_reason"] = objectiveRepairBaselineReason(result)
		baselineMetadata["objective_next_action_after_success"] = "edit"
		baselineID := fmt.Sprintf("validate-baseline-%d", iteration)
		workItems = append(workItems, domain.WorkItem{
			ID:                 baselineID,
			Kind:               domain.WorkItemKindValidate,
			Title:              fmt.Sprintf("Reproduce baseline before repair iteration %d", iteration),
			Description:        objectiveRepairBaselineReason(result),
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{replanID},
			DefinitionOfDone:   "Baseline validation evidence is refreshed before the next repair edit.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     suspectedPaths,
			ToolRequest: map[string]any{
				"tool": "run_tests",
				"argv": validationCommands,
			},
			Metadata: baselineMetadata,
		})
		editDependsOn = append(editDependsOn, baselineID)
	}
	if insertAnalysis {
		analyzeMetadata := cloneMap(baseMetadata)
		analyzeMetadata["project_request"] = replanProjectRequest
		analyzeMetadata["objective_analysis_types"] = analysisTypes
		analyzeMetadata["objective_next_action_after_success"] = "edit"
		workItems = append(workItems, domain.WorkItem{
			ID:               analyzeID,
			Kind:             domain.WorkItemKindAnalyze,
			Title:            fmt.Sprintf("Analyze repair iteration %d", iteration),
			Description:      "Run static analysis before the next repair edit because the latest findings suggest a structural issue.",
			AssignedAgent:    domain.AgentTypeReviewer,
			ExecutionMode:    domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:  domain.ExecutionTargetLocal,
			Backend:          "local_bridge",
			DependsOn:        []string{replanID},
			DefinitionOfDone: "Static analysis findings are persisted and available to the next edit.",
			SuspectedPaths:   suspectedPaths,
			ToolRequest: map[string]any{
				"tool":           "code_analysis",
				"project_root":   projectRoot,
				"analysis_types": analysisTypes,
			},
			Metadata: analyzeMetadata,
		})
		editDependsOn = append(editDependsOn, analyzeID)
	}
	workItems = append(workItems,
		domain.WorkItem{
			ID:                 editID,
			Kind:               domain.WorkItemKindEdit,
			Title:              fmt.Sprintf("Apply repair iteration %d with aider", iteration),
			Description:        editDescription,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          editDependsOn,
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
	)
	finalEditID := editID
	finalValidatePaths := objectiveUniqueStrings(suspectedPaths)
	for _, workstream := range secondaryWorkstreams {
		workstreamMetadata := cloneMap(editMetadata)
		workstreamMetadata["objective_workstream"] = workstream.Name
		workstreamMetadata["suspected_paths"] = workstream.Paths
		workstreamProjectRequest := cloneMap(replanProjectRequest)
		workstreamProjectRequest["expected_files"] = workstream.Paths
		workstreamProjectRequest["goal"] = workstream.Description
		workstreamMetadata["project_request"] = workstreamProjectRequest
		workItems = append(workItems, domain.WorkItem{
			ID:                 fmt.Sprintf("%s-%d", workstream.ID, iteration),
			Kind:               domain.WorkItemKindEdit,
			Title:              workstream.Title,
			Description:        workstream.Description,
			AssignedAgent:      domain.AgentTypeCoder,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "aider-task",
			DependsOn:          []string{finalEditID},
			ApprovalRequired:   true,
			DefinitionOfDone:   workstream.DefinitionOfDone,
			ValidationCommands: validationCommands,
			SuspectedPaths:     workstream.Paths,
			ToolRequest: map[string]any{
				"tool": "run_command",
				"argv": []string{
					"aider-task",
					"--task-id", fmt.Sprintf("%s-%s-%d", task.ID, workstream.ID, iteration),
					"--repo", projectName,
					"--description", workstream.Description,
				},
			},
			Metadata: workstreamMetadata,
		})
		finalEditID = fmt.Sprintf("%s-%d", workstream.ID, iteration)
		finalValidatePaths = objectiveUniqueStrings(append(finalValidatePaths, workstream.Paths...))
	}
	workItems = append(workItems,
		domain.WorkItem{
			ID:                 validateID,
			Kind:               domain.WorkItemKindValidate,
			Title:              fmt.Sprintf("Run validation for iteration %d", iteration),
			Description:        "Execute validation again after the repair edit.",
			AssignedAgent:      domain.AgentTypeReviewer,
			ExecutionMode:      domain.TaskLaunchModeAgentLocal,
			ExecutionTarget:    domain.ExecutionTargetLocal,
			Backend:            "local_bridge",
			DependsOn:          []string{finalEditID},
			DefinitionOfDone:   "Validation output is persisted for the repaired change set.",
			ValidationCommands: validationCommands,
			SuspectedPaths:     finalValidatePaths,
			ToolRequest: map[string]any{
				"tool": "run_tests",
				"argv": validationCommands,
			},
			Metadata: validateMetadata,
		},
		domain.WorkItem{
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
			SuspectedPaths:     finalValidatePaths,
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
	)
	return workItems, nil
}

type objectiveRepairWorkstream struct {
	ID               string
	Name             string
	Title            string
	Description      string
	DefinitionOfDone string
	Paths            []string
}

func objectiveRepairSecondaryWorkstreams(contract domain.ExecutionContract) []objectiveRepairWorkstream {
	if len(contract.WorkItems) == 0 {
		return nil
	}
	workstreams := make([]objectiveRepairWorkstream, 0, 2)
	for _, item := range contract.WorkItems {
		if item.Kind != domain.WorkItemKindEdit {
			continue
		}
		name := strings.TrimSpace(asString(item.Metadata["objective_workstream"]))
		switch name {
		case "documentation_sync", "config_sync", "dependency_sync":
		default:
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" || id == "edit" {
			continue
		}
		paths := objectiveUniqueStrings(item.SuspectedPaths)
		if len(paths) == 0 {
			continue
		}
		workstreams = append(workstreams, objectiveRepairWorkstream{
			ID:               id,
			Name:             name,
			Title:            item.Title,
			Description:      item.Description,
			DefinitionOfDone: item.DefinitionOfDone,
			Paths:            paths,
		})
	}
	return workstreams
}

func shouldInsertObjectiveCodeAnalysis(result domain.LocalBridgeResultRequest, analysisTypes []string) bool {
	if len(analysisTypes) == 0 {
		return false
	}
	findings := result.Findings
	if len(findings) == 0 {
		return false
	}
	for _, finding := range findings {
		switch strings.TrimSpace(asString(finding["kind"])) {
		case "planner_rejected_scope_contradicted", "planner_scope_contradicted", "research_scope_contradicted", "retrieval_scope_contradicted", "missing_diff":
			return true
		case "validation_failure":
			if len(result.ChangedFiles) == 0 {
				return true
			}
		}
	}
	return false
}

func objectiveRepairAnalysisTypes(result domain.LocalBridgeResultRequest) []string {
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
	for _, finding := range result.Findings {
		switch strings.TrimSpace(asString(finding["kind"])) {
		case "planner_rejected_scope_contradicted", "research_scope_contradicted", "missing_diff", "planner_scope_contradicted", "retrieval_scope_contradicted":
			addType("complexity")
			addType("lint")
		case "validation_failure":
			addType("dependencies")
			addType("security")
		}
	}
	return types
}

func shouldInsertObjectiveRepairBaselineValidation(result domain.LocalBridgeResultRequest) bool {
	for _, finding := range result.Findings {
		switch strings.TrimSpace(asString(finding["kind"])) {
		case "planner_scope_contradicted", "planner_rejected_scope_contradicted", "retrieval_scope_contradicted", "missing_diff":
			return true
		case "validation_failure":
			if len(result.ChangedFiles) == 0 {
				return true
			}
		}
	}
	return false
}

func objectiveRepairBaselineReason(result domain.LocalBridgeResultRequest) string {
	for _, finding := range result.Findings {
		switch strings.TrimSpace(asString(finding["kind"])) {
		case "retrieval_scope_contradicted":
			return "Reproduce the current validation state before the next repair edit because the latest diff contradicted the retrieved precedent carried into the attempt."
		case "planner_scope_contradicted":
			return "Reproduce the current validation state before the next repair edit because the latest diff contradicted the planner shortlist and the next scope must be re-anchored."
		case "planner_rejected_scope_contradicted":
			return "Reproduce the current validation state before the next repair edit because the last patch landed in a path the planner had rejected."
		case "missing_diff":
			return "Reproduce the current validation state before the next repair edit because the previous attempt did not leave a usable diff."
		case "validation_failure":
			if len(result.ChangedFiles) == 0 {
				return "Reproduce the current validation state before the next repair edit because validation failed without leaving a narrowed diff."
			}
		}
	}
	return "Reproduce the current validation state before the next repair edit."
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

func objectivePriorFindings(result domain.LocalBridgeResultRequest) []map[string]any {
	if len(result.Findings) > 0 {
		return result.Findings
	}
	return result.ReviewComments
}

func objectiveRepairPatchIntent(result domain.LocalBridgeResultRequest, suspectedPaths []string) []map[string]any {
	hypotheses := objectiveRepairFailureHypotheses(result, suspectedPaths)
	paths := objectiveUniqueStrings(suspectedPaths)
	if len(paths) == 0 && len(result.Findings) == 0 {
		return nil
	}
	intents := make([]map[string]any, 0, len(paths)+1)
	for _, path := range paths {
		intent := map[string]any{
			"path":   path,
			"action": "repair the failing behavior in this file and avoid unrelated edits",
		}
		hypothesis := objectiveFirstFailureHypothesisForPath(hypotheses, path)
		if hypothesis != nil {
			if failureMode := strings.TrimSpace(asString(hypothesis["failure_mode"])); failureMode != "" {
				intent["failure_mode"] = failureMode
				switch failureMode {
				case "validation_failure":
					intent["action"] = "repair the validation failure in this file and rerun the declared test command before widening scope"
				case "missing_diff":
					intent["action"] = "produce the missing code change in this file and verify the diff exists before review"
				}
			}
			if priority := strings.TrimSpace(asString(hypothesis["priority"])); priority != "" {
				intent["priority"] = priority
			}
			if rationale := strings.TrimSpace(asString(hypothesis["evidence"])); rationale != "" {
				intent["rationale"] = rationale
			}
		} else if rationale := objectiveFindingMessageForPath(result.Findings, path); rationale != "" {
			intent["rationale"] = rationale
		}
		intents = append(intents, intent)
	}
	if len(intents) == 0 {
		intents = append(intents, map[string]any{
			"action":    "repair the highest-severity finding and keep the patch narrow",
			"rationale": objectiveFindingMessageForPath(result.Findings, ""),
		})
	}
	return intents
}

func objectiveRepairFailureHypotheses(result domain.LocalBridgeResultRequest, suspectedPaths []string) []map[string]any {
	paths := objectiveUniqueStrings(suspectedPaths)
	if len(paths) == 0 && len(result.Findings) == 0 && len(result.TestResults) == 0 {
		return nil
	}
	hypotheses := make([]map[string]any, 0, len(paths)+1)
	for _, path := range paths {
		hypothesis := map[string]any{
			"path":         path,
			"failure_mode": "targeted_regression",
			"priority":     "medium",
		}
		if exitCode, ok := objectiveAsIntOK(result.TestResults["exit_code"]); ok && exitCode != 0 {
			hypothesis["failure_mode"] = "validation_failure"
			hypothesis["priority"] = "high"
			hypothesis["evidence"] = fmt.Sprintf("Declared validation command exited with code %d.", exitCode)
		}
		if finding := objectiveFirstFindingForPath(result.Findings, path); finding != nil {
			if kind := strings.TrimSpace(asString(finding["kind"])); kind != "" {
				hypothesis["failure_mode"] = kind
			}
			if severity := strings.TrimSpace(asString(finding["severity"])); severity != "" {
				hypothesis["priority"] = severity
			}
			if message := strings.TrimSpace(asString(finding["message"])); message != "" {
				hypothesis["evidence"] = message
			}
		}
		hypotheses = append(hypotheses, hypothesis)
	}
	if len(hypotheses) == 0 {
		hypothesis := map[string]any{
			"failure_mode": "validation_failure",
			"priority":     "high",
		}
		if exitCode, ok := objectiveAsIntOK(result.TestResults["exit_code"]); ok && exitCode != 0 {
			hypothesis["evidence"] = fmt.Sprintf("Declared validation command exited with code %d.", exitCode)
		}
		hypotheses = append(hypotheses, hypothesis)
	}
	return hypotheses
}

func objectiveFindingMessageForPath(findings []map[string]any, path string) string {
	for _, finding := range findings {
		message := strings.TrimSpace(asString(finding["message"]))
		if message == "" {
			continue
		}
		findingPath := strings.TrimSpace(asString(finding["path"]))
		switch {
		case path == "":
			return message
		case findingPath != "" && findingPath == path:
			return message
		case strings.Contains(message, path):
			return message
		}
	}
	return ""
}

func objectiveFirstFindingForPath(findings []map[string]any, path string) map[string]any {
	for _, finding := range findings {
		message := strings.TrimSpace(asString(finding["message"]))
		if message == "" {
			continue
		}
		findingPath := strings.TrimSpace(asString(finding["path"]))
		switch {
		case path == "":
			return finding
		case findingPath != "" && findingPath == path:
			return finding
		case strings.Contains(message, path):
			return finding
		}
	}
	if len(findings) > 0 {
		return findings[0]
	}
	return nil
}

func objectiveFirstFailureHypothesisForPath(hypotheses []map[string]any, path string) map[string]any {
	for _, hypothesis := range hypotheses {
		hypothesisPath := strings.TrimSpace(asString(hypothesis["path"]))
		if hypothesisPath != "" && hypothesisPath == path {
			return hypothesis
		}
	}
	if len(hypotheses) > 0 {
		return hypotheses[0]
	}
	return nil
}

func objectiveUniqueStrings(values []string) []string {
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

func objectiveAsIntOK(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
