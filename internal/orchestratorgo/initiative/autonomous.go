package initiative

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type InitiativeStore interface {
	CreateInitiative(ctx context.Context, params store.CreateInitiativeParams) (*domain.InitiativeResponse, error)
	GetInitiative(ctx context.Context, initiativeID string) (*domain.InitiativeResponse, error)
	UpdateInitiative(ctx context.Context, initiativeID string, params store.UpdateInitiativeParams) (*domain.InitiativeResponse, error)
	ListInitiativeTasks(ctx context.Context, initiativeID string) ([]domain.InitiativeTaskLinkResponse, error)
	UpdateTaskLaunchMode(ctx context.Context, taskID, mode string) error
	UpdateInitiativeTaskMode(ctx context.Context, initiativeID, taskID, mode string) (*domain.InitiativeTaskLinkResponse, error)
	QueueTaskForLaunch(ctx context.Context, taskID, operator, reason string) (*domain.TaskResponse, error)
	ReconcileInitiativeExecution(ctx context.Context, initiativeID string) (*domain.InitiativeResponse, error)
	CreateInitiativePhaseReview(ctx context.Context, params store.CreateInitiativePhaseReviewParams) (*domain.InitiativePhaseReviewResponse, error)
	CreateArtifact(ctx context.Context, params store.CreateArtifactParams) (string, error)
	ListArtifactsByIDs(ctx context.Context, ids []string) ([]domain.ArtifactResponse, error)
	CreateTask(ctx context.Context, params store.CreateTaskParams) (*domain.TaskResponse, error)
	LinkInitiativeTask(ctx context.Context, params store.LinkInitiativeTaskParams) (*domain.InitiativeTaskLinkResponse, error)
}

type TaskQueue interface {
	EnqueueTask(ctx context.Context, taskID string, agent domain.AgentType, priority domain.Priority) error
}

type PhaseGenerator interface {
	GenerateRequirements(ctx context.Context, initiative *domain.InitiativeResponse, feedback string) (PhaseArtifacts, error)
	GenerateDesign(ctx context.Context, initiative *domain.InitiativeResponse, requirements map[string]any, feedback string) (PhaseArtifacts, error)
	GenerateExecutionPlan(ctx context.Context, initiative *domain.InitiativeResponse, design map[string]any, feedback string) (PhaseArtifacts, error)
}

type AutonomousRunner struct {
	cfg       config.Config
	store     InitiativeStore
	queue     TaskQueue
	generator PhaseGenerator
}

type phaseArtifactIDs struct {
	MarkdownID *string
	JSONID     *string
}

func NewAutonomousRunner(cfg config.Config, store InitiativeStore, queue TaskQueue, generator PhaseGenerator) *AutonomousRunner {
	return &AutonomousRunner{
		cfg:       cfg,
		store:     store,
		queue:     queue,
		generator: generator,
	}
}

func (r *AutonomousRunner) StartFromChannel(ctx context.Context, req domain.AutonomousInitiativeRequest) (*domain.AutonomousRunResult, error) {
	if r == nil || r.store == nil || r.generator == nil {
		return nil, fmt.Errorf("autonomous runner is not configured")
	}
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		return nil, fmt.Errorf("goal is required")
	}
	workspaceRoot := strings.TrimSpace(req.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = strings.TrimSpace(r.cfg.WorkspaceRoot)
	}
	if workspaceRoot == "" {
		return nil, fmt.Errorf("workspace_root is required")
	}
	item, err := r.store.CreateInitiative(ctx, store.CreateInitiativeParams{
		Title:         truncateAutonomousTitle(goal, 72),
		WorkspaceRoot: workspaceRoot,
		Goal:          goal,
		CreatedBy:     firstAutonomousNonEmpty(strings.TrimSpace(req.OperatorID), strings.TrimSpace(req.Surface), "autonomous"),
		ExecutionMode: domain.InitiativeExecutionModeSelective,
	})
	if err != nil {
		return nil, err
	}
	return r.advanceAndLaunch(ctx, item, req)
}

func (r *AutonomousRunner) advanceAndLaunch(ctx context.Context, item *domain.InitiativeResponse, req domain.AutonomousInitiativeRequest) (*domain.AutonomousRunResult, error) {
	current := item
	var err error
	current, err = r.generateRequirementsPhase(ctx, current, req)
	if err != nil {
		return nil, err
	}
	current, err = r.generateDesignPhase(ctx, current, req)
	if err != nil {
		return nil, err
	}
	created, current, err := r.generatePlanTasks(ctx, current, req)
	if err != nil {
		return nil, err
	}
	launched, current, err := r.launchGeneratedTasks(ctx, current, req)
	if err != nil {
		return nil, err
	}
	result := &domain.AutonomousRunResult{
		InitiativeID:  current.ID,
		WorkspaceRoot: current.WorkspaceRoot,
		Status:        current.Status,
		CurrentPhase:  current.CurrentPhase,
		TasksCreated:  created,
		TasksLaunched: launched,
		PendingAction: deriveAutonomousPendingAction(current.Status, created, launched),
	}
	result.Summary = formatAutonomousSummary(result)
	return result, nil
}

func (r *AutonomousRunner) generateRequirementsPhase(ctx context.Context, item *domain.InitiativeResponse, req domain.AutonomousInitiativeRequest) (*domain.InitiativeResponse, error) {
	payload, err := r.generator.GenerateRequirements(ctx, item, "")
	if err != nil {
		return nil, err
	}
	artifacts, err := r.persistInitiativePhaseArtifacts(ctx, item, domain.InitiativePhaseRequirements, payload)
	if err != nil {
		return nil, err
	}
	if err := r.recordGeneratedReview(ctx, item.ID, domain.InitiativePhaseRequirements, artifacts); err != nil {
		return nil, err
	}
	current, err := r.updateInitiativeActiveArtifacts(ctx, item, domain.InitiativePhaseRequirements, domain.InitiativeStatusRequirementsReview, artifacts)
	if err != nil {
		return nil, err
	}
	if req.AutoApprovePhases {
		return r.approvePhase(ctx, current, domain.InitiativePhaseRequirements, req.OperatorID)
	}
	return current, nil
}

func (r *AutonomousRunner) generateDesignPhase(ctx context.Context, item *domain.InitiativeResponse, req domain.AutonomousInitiativeRequest) (*domain.InitiativeResponse, error) {
	requirements, err := r.getActiveInitiativeArtifactJSON(ctx, item.ActiveRequirementsArtifactID)
	if err != nil {
		return nil, err
	}
	payload, err := r.generator.GenerateDesign(ctx, item, requirements, "")
	if err != nil {
		return nil, err
	}
	artifacts, err := r.persistInitiativePhaseArtifacts(ctx, item, domain.InitiativePhaseDesign, payload)
	if err != nil {
		return nil, err
	}
	if err := r.recordGeneratedReview(ctx, item.ID, domain.InitiativePhaseDesign, artifacts); err != nil {
		return nil, err
	}
	current, err := r.updateInitiativeActiveArtifacts(ctx, item, domain.InitiativePhaseDesign, domain.InitiativeStatusDesignReview, artifacts)
	if err != nil {
		return nil, err
	}
	if req.AutoApprovePhases {
		return r.approvePhase(ctx, current, domain.InitiativePhaseDesign, req.OperatorID)
	}
	return current, nil
}

func (r *AutonomousRunner) generatePlanTasks(ctx context.Context, item *domain.InitiativeResponse, req domain.AutonomousInitiativeRequest) (int, *domain.InitiativeResponse, error) {
	designJSON, err := r.getActiveInitiativeArtifactJSON(ctx, item.ActiveDesignArtifactID)
	if err != nil {
		return 0, nil, err
	}
	payload, err := r.generator.GenerateExecutionPlan(ctx, item, designJSON, "")
	if err != nil {
		return 0, nil, err
	}
	artifacts, err := r.persistInitiativePhaseArtifacts(ctx, item, domain.InitiativePhasePlan, payload)
	if err != nil {
		return 0, nil, err
	}
	if err := r.recordGeneratedReview(ctx, item.ID, domain.InitiativePhasePlan, artifacts); err != nil {
		return 0, nil, err
	}
	current, err := r.updateInitiativeActiveArtifacts(ctx, item, domain.InitiativePhasePlan, domain.InitiativeStatusPlanReview, artifacts)
	if err != nil {
		return 0, nil, err
	}
	createdTasks, err := r.materializeInitiativePlanTasks(ctx, current, payload.JSON)
	if err != nil {
		return 0, nil, err
	}
	if req.AutoApprovePhases {
		current, err = r.approvePhase(ctx, current, domain.InitiativePhasePlan, req.OperatorID)
		if err != nil {
			return 0, nil, err
		}
	}
	return len(createdTasks), current, nil
}

func (r *AutonomousRunner) launchGeneratedTasks(ctx context.Context, item *domain.InitiativeResponse, req domain.AutonomousInitiativeRequest) (int, *domain.InitiativeResponse, error) {
	allItems, err := r.store.ListInitiativeTasks(ctx, item.ID)
	if err != nil {
		return 0, nil, err
	}
	policy := ResolveExecutionPolicy(r.cfg.WorkspaceRoot, item.WorkspaceRoot)
	launched := 0
	for _, link := range allItems {
		mode := strings.TrimSpace(link.ExecutionMode)
		if mode == "" {
			mode = domain.TaskLaunchModeAgentLocal
		}
		mode = normalizeAutonomousLaunchMode(policy, mode)
		if err := ValidateTaskLaunchAgainstPolicy(policy, link, mode); err != nil {
			return launched, nil, err
		}
		if _, err := r.store.UpdateInitiativeTaskMode(ctx, item.ID, link.TaskID, mode); err != nil {
			return launched, nil, err
		}
		if err := r.store.UpdateTaskLaunchMode(ctx, link.TaskID, mode); err != nil {
			return launched, nil, err
		}
		if mode == domain.TaskLaunchModeManual {
			continue
		}
		queuedTask, err := r.store.QueueTaskForLaunch(ctx, link.TaskID, "autonomous:initiative_launch", "Queued from autonomous initiative")
		if err != nil {
			return launched, nil, err
		}
		if queuedTask.ExecutionTarget != domain.ExecutionTargetLocal && queuedTask.AssignedAgent != nil && r.queue != nil {
			if err := r.queue.EnqueueTask(ctx, queuedTask.ID, *queuedTask.AssignedAgent, queuedTask.Priority); err != nil {
				return launched, nil, err
			}
		}
		launched++
	}
	updated, err := r.store.ReconcileInitiativeExecution(ctx, item.ID)
	if err != nil {
		return launched, nil, err
	}
	return launched, updated, nil
}

func (r *AutonomousRunner) approvePhase(ctx context.Context, item *domain.InitiativeResponse, phase domain.InitiativePhase, operator string) (*domain.InitiativeResponse, error) {
	if _, err := r.store.CreateInitiativePhaseReview(ctx, store.CreateInitiativePhaseReviewParams{
		InitiativeID: item.ID,
		Phase:        phase,
		Decision:     domain.InitiativeReviewApproved,
		GeneratedBy:  cleanAutonomousStringPtr(firstAutonomousNonEmpty(strings.TrimSpace(operator), "autonomous")),
	}); err != nil {
		return nil, err
	}
	var nextStatus domain.InitiativeStatus
	var nextPhase domain.InitiativePhase
	switch phase {
	case domain.InitiativePhaseRequirements:
		nextStatus = domain.InitiativeStatusDesignDraft
		nextPhase = domain.InitiativePhaseDesign
	case domain.InitiativePhaseDesign:
		nextStatus = domain.InitiativeStatusPlanDraft
		nextPhase = domain.InitiativePhasePlan
	case domain.InitiativePhasePlan:
		nextStatus = domain.InitiativeStatusExecutionReady
		nextPhase = domain.InitiativePhaseExecution
	default:
		return nil, fmt.Errorf("unsupported phase %s", phase)
	}
	return r.store.UpdateInitiative(ctx, item.ID, store.UpdateInitiativeParams{
		Status:       &nextStatus,
		CurrentPhase: &nextPhase,
	})
}

func (r *AutonomousRunner) recordGeneratedReview(ctx context.Context, initiativeID string, phase domain.InitiativePhase, artifacts phaseArtifactIDs) error {
	generatedBy := "initiative-service"
	_, err := r.store.CreateInitiativePhaseReview(ctx, store.CreateInitiativePhaseReviewParams{
		InitiativeID:       initiativeID,
		Phase:              phase,
		Decision:           domain.InitiativeReviewGenerated,
		GeneratedBy:        &generatedBy,
		ArtifactMarkdownID: artifacts.MarkdownID,
		ArtifactJSONID:     artifacts.JSONID,
	})
	return err
}

func (r *AutonomousRunner) persistInitiativePhaseArtifacts(ctx context.Context, item *domain.InitiativeResponse, phase domain.InitiativePhase, payload PhaseArtifacts) (phaseArtifactIDs, error) {
	metaBase := map[string]any{
		"initiative_id":    item.ID,
		"initiative_phase": string(phase),
		"generated_role":   generatedRoleForAutonomousPhase(phase),
	}
	jsonRaw, _ := json.Marshal(payload.JSON)
	mdID, err := r.store.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: "initiative_" + string(phase) + ".md",
		Title:        cleanAutonomousStringPtr(item.Title + " " + string(phase) + " markdown"),
		MediaType:    cleanAutonomousStringPtr("text/markdown"),
		ContentText:  cleanAutonomousStringPtr(payload.Markdown),
		Metadata:     mergeAutonomousMaps(metaBase, map[string]any{"format": "markdown"}),
	})
	if err != nil {
		return phaseArtifactIDs{}, err
	}
	jsonID, err := r.store.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: "initiative_" + string(phase) + ".json",
		Title:        cleanAutonomousStringPtr(item.Title + " " + string(phase) + " json"),
		MediaType:    cleanAutonomousStringPtr("application/json"),
		ContentText:  cleanAutonomousStringPtr(string(jsonRaw)),
		Metadata:     mergeAutonomousMaps(metaBase, map[string]any{"format": "json", "payload": payload.JSON}),
	})
	if err != nil {
		return phaseArtifactIDs{}, err
	}
	return phaseArtifactIDs{MarkdownID: &mdID, JSONID: &jsonID}, nil
}

func (r *AutonomousRunner) getActiveInitiativeArtifactJSON(ctx context.Context, artifactID *string) (map[string]any, error) {
	if artifactID == nil || strings.TrimSpace(*artifactID) == "" {
		return nil, fmt.Errorf("required initiative artifact is missing")
	}
	items, err := r.store.ListArtifactsByIDs(ctx, []string{strings.TrimSpace(*artifactID)})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("initiative artifact not found")
	}
	artifact := items[0]
	var payload map[string]any
	if raw := strings.TrimSpace(autonomousStringValue(artifact.ContentText)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err == nil && len(payload) > 0 {
			return payload, nil
		}
	}
	if metaPayload, ok := artifact.Metadata["payload"].(map[string]any); ok {
		return metaPayload, nil
	}
	return nil, fmt.Errorf("initiative artifact JSON payload is missing")
}

func (r *AutonomousRunner) updateInitiativeActiveArtifacts(ctx context.Context, item *domain.InitiativeResponse, phase domain.InitiativePhase, status domain.InitiativeStatus, ids phaseArtifactIDs) (*domain.InitiativeResponse, error) {
	params := store.UpdateInitiativeParams{
		Status: &status,
	}
	switch phase {
	case domain.InitiativePhaseRequirements:
		params.ActiveRequirementsArtifactID = ids.JSONID
	case domain.InitiativePhaseDesign:
		params.ActiveDesignArtifactID = ids.JSONID
	case domain.InitiativePhasePlan:
		params.ActivePlanArtifactID = ids.JSONID
	}
	return r.store.UpdateInitiative(ctx, item.ID, params)
}

func (r *AutonomousRunner) materializeInitiativePlanTasks(ctx context.Context, item *domain.InitiativeResponse, payload map[string]any) ([]domain.InitiativeTaskLinkResponse, error) {
	epics := normalizedMapSlice(payload["epics"])
	created := make([]domain.InitiativeTaskLinkResponse, 0)
	launchOrder := 10
	for _, epic := range epics {
		epicName := strings.TrimSpace(asString(epic["name"]))
		group := strings.TrimSpace(asString(epic["group"]))
		tasks := normalizedMapSlice(epic["tasks"])
		for _, taskSpec := range tasks {
			description := firstAutonomousNonEmpty(strings.TrimSpace(asString(taskSpec["description"])), strings.TrimSpace(asString(taskSpec["title"])))
			assigned := domain.AgentType(strings.TrimSpace(asString(taskSpec["suggested_agent"])))
			if assigned == "" {
				assigned = domain.AgentTypeCoder
			}
			planned := assigned
			priority := domain.Priority(strings.TrimSpace(asString(taskSpec["priority"])))
			if !isAutonomousSupportedPriority(priority) {
				priority = domain.PriorityNormal
			}
			executionTarget := domain.ExecutionTarget(strings.TrimSpace(asString(taskSpec["execution_target"])))
			if executionTarget == "" {
				executionTarget = domain.ExecutionTargetLocal
			}
			metadata := map[string]any{
				"initiative_id":           item.ID,
				"initiative_phase_origin": "plan",
				"workspace_root":          item.WorkspaceRoot,
				"definition_of_done":      asString(taskSpec["definition_of_done"]),
				"approval_required":       autonomousAsBool(taskSpec["approval_required"]),
				"planned_execution_mode":  strings.TrimSpace(asString(taskSpec["execution_mode"])),
				"launch_group":            group,
				"epic":                    epicName,
				"title":                   asString(taskSpec["title"]),
				"initiative_goal":         item.Goal,
			}
			if specMeta, ok := taskSpec["metadata"].(map[string]any); ok {
				for key, value := range specMeta {
					metadata[key] = value
				}
			}
			workspacePath := autonomousWorkspacePath(r.cfg.WorkspaceRoot, executionTarget, metadata)
			task, err := r.store.CreateTask(ctx, store.CreateTaskParams{
				Description:     description,
				Metadata:        metadata,
				Priority:        priority,
				AssignedAgent:   assigned,
				PlannedAgent:    &planned,
				ExecutionTarget: executionTarget,
				Entrypoint:      "initiative:autonomous",
				WorkspacePath:   workspacePath,
				InitiativeID:    &item.ID,
				TaskKind:        domain.TaskKindPlanStep,
				QueueOnCreate:   false,
			})
			if err != nil {
				return nil, err
			}
			mode := firstAutonomousNonEmpty(strings.TrimSpace(asString(taskSpec["execution_mode"])), domain.TaskLaunchModeAgentLocal)
			link, err := r.store.LinkInitiativeTask(ctx, store.LinkInitiativeTaskParams{
				InitiativeID:  item.ID,
				TaskID:        task.ID,
				PhaseOrigin:   "plan",
				Epic:          cleanAutonomousStringPtr(epicName),
				LaunchGroup:   cleanAutonomousStringPtr(group),
				ExecutionMode: mode,
				LaunchOrder:   launchOrder,
			})
			if err != nil {
				return nil, err
			}
			created = append(created, *link)
			launchOrder += 10
		}
	}
	return created, nil
}

func deriveAutonomousPendingAction(status domain.InitiativeStatus, tasksCreated, tasksLaunched int) string {
	switch status {
	case domain.InitiativeStatusBlocked:
		return "blocked"
	case domain.InitiativeStatusExecutionReady:
		if tasksCreated > 0 && tasksLaunched == 0 {
			return "manual_or_policy_gate"
		}
		return "launch_ready"
	case domain.InitiativeStatusExecuting:
		return "running"
	case domain.InitiativeStatusCompleted:
		return "completed"
	default:
		return "phase_progress"
	}
}

func formatAutonomousSummary(result *domain.AutonomousRunResult) string {
	if result == nil {
		return "Autonomous initiative failed"
	}
	return fmt.Sprintf(
		"Autonomous initiative queued\n- Initiative: %s\n- Workspace: %s\n- Status: %s\n- Phase: %s\n- Tasks created: %d\n- Tasks launched: %d\n- Pending action: %s",
		result.InitiativeID,
		result.WorkspaceRoot,
		result.Status,
		result.CurrentPhase,
		result.TasksCreated,
		result.TasksLaunched,
		result.PendingAction,
	)
}

func generatedRoleForAutonomousPhase(phase domain.InitiativePhase) string {
	switch phase {
	case domain.InitiativePhaseRequirements:
		return "analyst"
	case domain.InitiativePhaseDesign:
		return "architect"
	case domain.InitiativePhasePlan:
		return "planner"
	default:
		return "agent"
	}
}

func cleanAutonomousStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func autonomousStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncateAutonomousTitle(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max || max <= 0 {
		return value
	}
	return strings.TrimSpace(value[:max]) + "..."
}

func firstAutonomousNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func mergeAutonomousMaps(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func autonomousWorkspacePath(orchestratorWorkspaceRoot string, target domain.ExecutionTarget, metadata map[string]any) *string {
	if target == domain.ExecutionTargetLocal {
		return nil
	}
	base := strings.TrimSpace(orchestratorWorkspaceRoot)
	if base == "" {
		return nil
	}
	value := filepath.Clean(base)
	return &value
}

func autonomousAsBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		typed = strings.TrimSpace(strings.ToLower(typed))
		return typed == "true" || typed == "1" || typed == "yes" || typed == "on"
	default:
		return false
	}
}

func isAutonomousSupportedPriority(priority domain.Priority) bool {
	switch priority {
	case domain.PriorityCritical, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow:
		return true
	default:
		return false
	}
}

func normalizeAutonomousLaunchMode(policy ExecutionPolicy, mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == domain.TaskLaunchModeManual {
		return mode
	}
	if policy.AllowsMode(mode) {
		return mode
	}
	switch policy.Scope {
	case "orchestrator_remote":
		return domain.TaskLaunchModeAgentRemote
	default:
		return domain.TaskLaunchModeAgentLocal
	}
}
