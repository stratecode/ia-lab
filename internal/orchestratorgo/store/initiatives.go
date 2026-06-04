package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type CreateInitiativeParams struct {
	Title         string
	WorkspaceRoot string
	Goal          string
	CreatedBy     string
	ExecutionMode domain.InitiativeExecutionMode
}

type InitiativeListFilter struct {
	IncludeArchived bool
	Limit           int
	WorkspaceRoot   string
}

type UpdateInitiativeParams struct {
	Status                       *domain.InitiativeStatus
	CurrentPhase                 *domain.InitiativePhase
	ActiveRequirementsArtifactID *string
	ActiveDesignArtifactID       *string
	ActivePlanArtifactID         *string
	ArchivedAt                   *time.Time
}

type CreateInitiativePhaseReviewParams struct {
	InitiativeID       string
	Phase              domain.InitiativePhase
	Decision           domain.InitiativeReviewDecision
	Feedback           *string
	GeneratedBy        *string
	ArtifactMarkdownID *string
	ArtifactJSONID     *string
}

type LinkInitiativeTaskParams struct {
	InitiativeID  string
	TaskID        string
	PhaseOrigin   string
	Epic          *string
	LaunchGroup   *string
	ExecutionMode string
	LaunchOrder   int
}

func (s *PostgresStore) CreateInitiative(ctx context.Context, params CreateInitiativeParams) (*domain.InitiativeResponse, error) {
	id := uuid.NewString()
	status := domain.InitiativeStatusIdeaSubmitted
	phase := domain.InitiativePhaseRequirements
	executionMode := params.ExecutionMode
	if executionMode == "" {
		executionMode = domain.InitiativeExecutionModeSelective
	}
	createdBy := strings.TrimSpace(params.CreatedBy)
	if createdBy == "" {
		createdBy = "operator"
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO initiatives (
			id, title, workspace_root, goal, status, current_phase, created_by, execution_mode
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, id, params.Title, params.WorkspaceRoot, params.Goal, string(status), string(phase), createdBy, string(executionMode)); err != nil {
		return nil, err
	}
	return s.GetInitiative(ctx, id)
}

func (s *PostgresStore) GetInitiative(ctx context.Context, initiativeID string) (*domain.InitiativeResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, title, workspace_root, goal, status, current_phase,
		       active_requirements_artifact_id::text, active_design_artifact_id::text,
		       active_plan_artifact_id::text, created_by, execution_mode,
		       archived_at, created_at, updated_at
		FROM initiatives
		WHERE id = $1
	`, initiativeID)
	return scanInitiative(row)
}

func (s *PostgresStore) ListInitiatives(ctx context.Context, filter InitiativeListFilter) ([]domain.InitiativeResponse, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	whereParts := make([]string, 0, 2)
	args := make([]any, 0, 2)
	query := `
		SELECT id::text, title, workspace_root, goal, status, current_phase,
		       active_requirements_artifact_id::text, active_design_artifact_id::text,
		       active_plan_artifact_id::text, created_by, execution_mode,
		       archived_at, created_at, updated_at
		FROM initiatives
	`
	if !filter.IncludeArchived {
		whereParts = append(whereParts, "archived_at IS NULL")
	}
	if workspaceRoot := strings.TrimSpace(filter.WorkspaceRoot); workspaceRoot != "" {
		whereParts = append(whereParts, fmt.Sprintf("workspace_root = $%d", len(args)+1))
		args = append(args, workspaceRoot)
	}
	if len(whereParts) > 0 {
		query += " WHERE " + strings.Join(whereParts, " AND ")
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY updated_at DESC, id ASC LIMIT $%d", len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.InitiativeResponse, 0)
	for rows.Next() {
		item, err := scanInitiative(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateInitiative(ctx context.Context, initiativeID string, params UpdateInitiativeParams) (*domain.InitiativeResponse, error) {
	_, err := s.pool.Exec(ctx, `
		UPDATE initiatives
		   SET status = COALESCE($2, status),
		       current_phase = COALESCE($3, current_phase),
		       active_requirements_artifact_id = COALESCE($4, active_requirements_artifact_id),
		       active_design_artifact_id = COALESCE($5, active_design_artifact_id),
		       active_plan_artifact_id = COALESCE($6, active_plan_artifact_id),
		       archived_at = $7,
		       updated_at = NOW()
		 WHERE id = $1
	`, initiativeID,
		statusOrNil(params.Status),
		phaseOrNil(params.CurrentPhase),
		params.ActiveRequirementsArtifactID,
		params.ActiveDesignArtifactID,
		params.ActivePlanArtifactID,
		params.ArchivedAt,
	)
	if err != nil {
		return nil, err
	}
	return s.GetInitiative(ctx, initiativeID)
}

func (s *PostgresStore) CreateInitiativePhaseReview(ctx context.Context, params CreateInitiativePhaseReviewParams) (*domain.InitiativePhaseReviewResponse, error) {
	id := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO initiative_phase_reviews (
			id, initiative_id, phase, decision, feedback, generated_by, artifact_markdown_id, artifact_json_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
	`, id, params.InitiativeID, string(params.Phase), string(params.Decision), params.Feedback, params.GeneratedBy, params.ArtifactMarkdownID, params.ArtifactJSONID); err != nil {
		return nil, err
	}
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, initiative_id::text, phase, decision, feedback, generated_by,
		       artifact_markdown_id::text, artifact_json_id::text, created_at
		FROM initiative_phase_reviews
		WHERE id = $1
	`, id)
	return scanInitiativePhaseReview(row)
}

func (s *PostgresStore) ListInitiativePhaseReviews(ctx context.Context, initiativeID string) ([]domain.InitiativePhaseReviewResponse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, initiative_id::text, phase, decision, feedback, generated_by,
		       artifact_markdown_id::text, artifact_json_id::text, created_at
		FROM initiative_phase_reviews
		WHERE initiative_id = $1
		ORDER BY created_at DESC, id DESC
	`, initiativeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.InitiativePhaseReviewResponse, 0)
	for rows.Next() {
		item, err := scanInitiativePhaseReview(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetInitiativePhaseReview(ctx context.Context, reviewID string) (*domain.InitiativePhaseReviewResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, initiative_id::text, phase, decision, feedback, generated_by,
		       artifact_markdown_id::text, artifact_json_id::text, created_at
		FROM initiative_phase_reviews
		WHERE id = $1
	`, reviewID)
	item, err := scanInitiativePhaseReview(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return item, err
}

func (s *PostgresStore) LinkInitiativeTask(ctx context.Context, params LinkInitiativeTaskParams) (*domain.InitiativeTaskLinkResponse, error) {
	id := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO initiative_task_links (
			id, initiative_id, task_id, phase_origin, epic, launch_group, execution_mode, launch_order
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)
		ON CONFLICT (initiative_id, task_id) DO UPDATE SET
			phase_origin = EXCLUDED.phase_origin,
			epic = EXCLUDED.epic,
			launch_group = EXCLUDED.launch_group,
			execution_mode = EXCLUDED.execution_mode,
			launch_order = EXCLUDED.launch_order
	`, id, params.InitiativeID, params.TaskID, params.PhaseOrigin, params.Epic, params.LaunchGroup, params.ExecutionMode, params.LaunchOrder); err != nil {
		return nil, err
	}
	return s.GetInitiativeTaskLink(ctx, params.InitiativeID, params.TaskID)
}

func (s *PostgresStore) GetInitiativeTaskLink(ctx context.Context, initiativeID, taskID string) (*domain.InitiativeTaskLinkResponse, error) {
	row := s.pool.QueryRow(ctx, initiativeTaskSelectSQL+` WHERE l.initiative_id = $1 AND l.task_id = $2`, initiativeID, taskID)
	return scanInitiativeTaskLink(row)
}

func (s *PostgresStore) UpdateInitiativeTaskMode(ctx context.Context, initiativeID, taskID, mode string) (*domain.InitiativeTaskLinkResponse, error) {
	if _, err := s.pool.Exec(ctx, `
		UPDATE initiative_task_links
		   SET execution_mode = $3
		 WHERE initiative_id = $1 AND task_id = $2
	`, initiativeID, taskID, mode); err != nil {
		return nil, err
	}
	return s.GetInitiativeTaskLink(ctx, initiativeID, taskID)
}

func (s *PostgresStore) ListInitiativeTasks(ctx context.Context, initiativeID string) ([]domain.InitiativeTaskLinkResponse, error) {
	rows, err := s.pool.Query(ctx, initiativeTaskSelectSQL+`
		WHERE l.initiative_id = $1
		ORDER BY l.launch_order ASC, t.created_at ASC, t.id ASC
	`, initiativeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.InitiativeTaskLinkResponse, 0)
	for rows.Next() {
		item, err := scanInitiativeTaskLink(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListInitiativeArtifacts(ctx context.Context, initiativeID string) ([]domain.ArtifactResponse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, artifact_type, title, uri, media_type, content_text, metadata, created_at
		FROM artifacts
		WHERE metadata->>'initiative_id' = $1
		ORDER BY created_at DESC, id DESC
	`, initiativeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectArtifacts(rows)
}

func (s *PostgresStore) UpdateTaskLaunchMode(ctx context.Context, taskID, mode string) error {
	executionTarget := domain.ExecutionTargetRemote
	switch mode {
	case domain.TaskLaunchModeAgentLocal:
		executionTarget = domain.ExecutionTargetLocal
	case domain.TaskLaunchModeAgentRemote:
		executionTarget = domain.ExecutionTargetRemote
	case domain.TaskLaunchModeManual:
		// Preserve current target for manual mode.
	default:
		return fmt.Errorf("unsupported launch mode %q", mode)
	}
	if mode == domain.TaskLaunchModeManual {
		_, err := s.pool.Exec(ctx, `
			UPDATE tasks
			   SET updated_at = NOW()
			 WHERE id = $1
		`, taskID)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks
		   SET execution_target = $2,
		       updated_at = NOW()
		 WHERE id = $1
	`, taskID, string(executionTarget))
	return err
}

func (s *PostgresStore) QueueTaskForLaunch(ctx context.Context, taskID, actor, reason string) (*domain.TaskResponse, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := s.GetTaskForUpdateTx(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}
	if task.State != domain.TaskStateCreated && task.State != domain.TaskStateFailed && task.State != domain.TaskStateCancelled {
		return nil, fmt.Errorf("task %s is not launchable from state %s", taskID, task.State)
	}
	if err := transitionTaskStateTx(ctx, tx, taskID, task.State, domain.TaskStateQueued, actor, reason, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, taskID)
}

func (s *PostgresStore) ReconcileInitiativeExecution(ctx context.Context, initiativeID string) (*domain.InitiativeResponse, error) {
	initiative, err := s.GetInitiative(ctx, initiativeID)
	if err != nil || initiative == nil {
		return initiative, err
	}
	items, err := s.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return initiative, nil
	}
	status := deriveInitiativeExecutionStatus(initiative.Status, items)
	return s.UpdateInitiative(ctx, initiativeID, UpdateInitiativeParams{Status: &status, CurrentPhase: phasePtr(domain.InitiativePhaseExecution)})
}

func deriveInitiativeExecutionStatus(current domain.InitiativeStatus, items []domain.InitiativeTaskLinkResponse) domain.InitiativeStatus {
	if len(items) == 0 {
		return current
	}
	hasFailed := false
	hasRunning := false
	hasPendingManual := false
	allTerminal := true
	for _, item := range items {
		if item.ExecutionMode == domain.TaskLaunchModeManual && item.Task.State == domain.TaskStateCreated {
			hasPendingManual = true
			allTerminal = false
			continue
		}
		switch item.Task.State {
		case domain.TaskStateFailed:
			hasFailed = true
			allTerminal = false
		case domain.TaskStateCancelled:
			if taskMetadataBool(item.Task.Metadata, "superseded_by_repair_cycle") {
				continue
			}
			hasFailed = true
			allTerminal = false
		case domain.TaskStateCompleted:
		case domain.TaskStateCreated, domain.TaskStateQueued, domain.TaskStateAssigned, domain.TaskStateInProgress, domain.TaskStateWaitingApproval, domain.TaskStateRetrying, domain.TaskStateReview:
			hasRunning = true
			allTerminal = false
		default:
			allTerminal = false
		}
	}
	switch {
	case hasFailed:
		return domain.InitiativeStatusBlocked
	case hasRunning:
		return domain.InitiativeStatusExecuting
	case hasPendingManual:
		return domain.InitiativeStatusExecutionReady
	case allTerminal:
		return domain.InitiativeStatusCompleted
	default:
		return current
	}
}

func taskMetadataBool(metadata map[string]any, key string) bool {
	if metadata == nil {
		return false
	}
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

const initiativeTaskSelectSQL = `
	SELECT l.initiative_id::text, l.task_id::text, l.phase_origin, l.epic, l.launch_group, l.execution_mode, l.launch_order, l.created_at,
	       t.id::text, t.state::text, t.description, t.metadata, t.assigned_agent::text, t.planned_agent::text, t.priority::text,
	       t.execution_target::text, t.initiative_id::text, t.workspace_path, t.retry_count, t.correlation_id::text, t.results,
	       t.error_message, t.created_at, t.updated_at, t.started_at, t.completed_at, t.archived_at,
	       t.parent_task_id::text, t.root_task_id::text, t.task_kind::text
	FROM initiative_task_links l
	JOIN tasks t ON t.id = l.task_id
`

type initiativeScanner interface {
	Scan(dest ...any) error
}

func scanInitiative(row initiativeScanner) (*domain.InitiativeResponse, error) {
	var item domain.InitiativeResponse
	var status, phase, executionMode string
	if err := row.Scan(
		&item.ID,
		&item.Title,
		&item.WorkspaceRoot,
		&item.Goal,
		&status,
		&phase,
		&item.ActiveRequirementsArtifactID,
		&item.ActiveDesignArtifactID,
		&item.ActivePlanArtifactID,
		&item.CreatedBy,
		&executionMode,
		&item.ArchivedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Status = domain.InitiativeStatus(status)
	item.CurrentPhase = domain.InitiativePhase(phase)
	item.ExecutionMode = domain.InitiativeExecutionMode(executionMode)
	return &item, nil
}

type initiativePhaseReviewScanner interface {
	Scan(dest ...any) error
}

func scanInitiativePhaseReview(row initiativePhaseReviewScanner) (*domain.InitiativePhaseReviewResponse, error) {
	var item domain.InitiativePhaseReviewResponse
	var phase, decision string
	if err := row.Scan(
		&item.ID,
		&item.InitiativeID,
		&phase,
		&decision,
		&item.Feedback,
		&item.GeneratedBy,
		&item.ArtifactMarkdownID,
		&item.ArtifactJSONID,
		&item.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.Phase = domain.InitiativePhase(phase)
	item.Decision = domain.InitiativeReviewDecision(decision)
	return &item, nil
}

type initiativeTaskLinkScanner interface {
	Scan(dest ...any) error
}

func scanInitiativeTaskLink(row initiativeTaskLinkScanner) (*domain.InitiativeTaskLinkResponse, error) {
	var (
		item            domain.InitiativeTaskLinkResponse
		task            domain.TaskResponse
		metadataRaw     []byte
		resultsRaw      []byte
		assignedAgent   *string
		plannedAgent    *string
		initiativeID    *string
		errorMessage    *string
		workspacePath   *string
		parentTaskID    *string
		rootTaskID      *string
		startedAt       *time.Time
		completedAt     *time.Time
		archivedAt      *time.Time
		taskKind        string
		executionTarget string
		priority        string
		state           string
		correlationID   string
	)
	err := row.Scan(
		&item.InitiativeID,
		&item.TaskID,
		&item.PhaseOrigin,
		&item.Epic,
		&item.LaunchGroup,
		&item.ExecutionMode,
		&item.LaunchOrder,
		&item.CreatedAt,
		&task.ID,
		&state,
		&task.Description,
		&metadataRaw,
		&assignedAgent,
		&plannedAgent,
		&priority,
		&executionTarget,
		&initiativeID,
		&workspacePath,
		&task.RetryCount,
		&correlationID,
		&resultsRaw,
		&errorMessage,
		&task.CreatedAt,
		&task.UpdatedAt,
		&startedAt,
		&completedAt,
		&archivedAt,
		&parentTaskID,
		&rootTaskID,
		&taskKind,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	task.State = domain.TaskState(state)
	task.Priority = domain.Priority(priority)
	task.ExecutionTarget = domain.ExecutionTarget(executionTarget)
	task.InitiativeID = initiativeID
	task.WorkspacePath = workspacePath
	task.CorrelationID = correlationID
	task.ErrorMessage = errorMessage
	task.ParentTaskID = parentTaskID
	task.RootTaskID = rootTaskID
	task.StartedAt = startedAt
	task.CompletedAt = completedAt
	task.ArchivedAt = archivedAt
	task.TaskKind = domain.TaskKind(taskKind)
	if assignedAgent != nil && *assignedAgent != "" {
		value := domain.AgentType(*assignedAgent)
		task.AssignedAgent = &value
	}
	if plannedAgent != nil && *plannedAgent != "" {
		value := domain.AgentType(*plannedAgent)
		task.PlannedAgent = &value
	}
	_ = json.Unmarshal(metadataRaw, &task.Metadata)
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	if allowedRaw, ok := task.Metadata["allowed_capabilities"].([]any); ok {
		for _, entry := range allowedRaw {
			task.AllowedCapabilities = append(task.AllowedCapabilities, fmt.Sprint(entry))
		}
	}
	if len(resultsRaw) > 0 {
		var results any
		_ = json.Unmarshal(resultsRaw, &results)
		task.Results = results
	}
	item.Task = task
	return &item, nil
}

func statusOrNil(value *domain.InitiativeStatus) any {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return nil
	}
	return string(*value)
}

func phaseOrNil(value *domain.InitiativePhase) any {
	if value == nil || strings.TrimSpace(string(*value)) == "" {
		return nil
	}
	return string(*value)
}

func phasePtr(value domain.InitiativePhase) *domain.InitiativePhase {
	return &value
}
