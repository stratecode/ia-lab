package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

var (
	ErrApprovalNotFound        = errors.New("approval not found")
	ErrApprovalAlreadyResolved = errors.New("approval already resolved")
)

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) GetAPIKeyByHash(ctx context.Context, keyHash string) (*domain.APIKeyRecord, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, name, scope::text
		FROM api_keys
		WHERE key_hash = $1 AND is_active = true AND revoked_at IS NULL
	`, keyHash)
	var record domain.APIKeyRecord
	if err := row.Scan(&record.ID, &record.Name, &record.Scope); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &record, nil
}

func (s *PostgresStore) GetTask(ctx context.Context, taskID string) (*domain.TaskResponse, error) {
	row := s.pool.QueryRow(ctx, taskSelectSQL+` WHERE id = $1`, taskID)
	task, err := scanTask(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return task, err
}

func (s *PostgresStore) GetTaskByIdempotencyKey(ctx context.Context, key string) (*domain.TaskResponse, error) {
	row := s.pool.QueryRow(ctx, taskSelectSQL+` WHERE idempotency_key = $1`, key)
	task, err := scanTask(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return task, err
}

type CreateTaskParams struct {
	Description         string
	Metadata            map[string]any
	AllowedCapabilities []string
	Priority            domain.Priority
	AssignedAgent       domain.AgentType
	ExecutionTarget     domain.ExecutionTarget
	IdempotencyKey      *string
	Entrypoint          string
	WorkspacePath       *string
	ParentTaskID        *string
	RootTaskID          *string
	TaskKind            domain.TaskKind
	QueueOnCreate       bool
}

type UpsertLocalBridgeParams struct {
	ID            string
	Name          string
	Hostname      string
	WorkspaceRoot string
	Capabilities  map[string]any
	APIKeyName    *string
	Status        string
}

func (s *PostgresStore) CreateTask(ctx context.Context, params CreateTaskParams) (*domain.TaskResponse, error) {
	taskID := uuid.NewString()
	correlationID := uuid.NewString()
	now := time.Now().UTC()
	metadata := cloneMap(params.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["entrypoint"] = params.Entrypoint
	if len(params.AllowedCapabilities) > 0 {
		metadata["allowed_capabilities"] = params.AllowedCapabilities
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	effectiveWorkspacePath := params.WorkspacePath
	if params.ExecutionTarget == domain.ExecutionTargetRemote && params.WorkspacePath != nil && strings.TrimSpace(*params.WorkspacePath) != "" {
		path := filepath.Join(*params.WorkspacePath, taskID)
		effectiveWorkspacePath = &path
	}
	taskKind := params.TaskKind
	if taskKind == "" {
		taskKind = domain.TaskKindRoot
	}
	rootTaskID := taskID
	if params.RootTaskID != nil && strings.TrimSpace(*params.RootTaskID) != "" {
		rootTaskID = strings.TrimSpace(*params.RootTaskID)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO tasks (
			id, state, description, metadata, assigned_agent, priority, execution_target,
			workspace_path, retry_count, max_retries, idempotency_key, correlation_id, results,
			error_message, created_at, updated_at, started_at, completed_at, queued_at,
			parent_task_id, root_task_id, task_kind
		) VALUES (
			$1, $2, $3, $4::jsonb, $5, $6, $7, $8, 0, 3, $9, $10, NULL, NULL,
			$11, $11, NULL, NULL, NULL, $12, $13, $14
		)
	`, taskID, string(domain.TaskStateCreated), params.Description, string(metadataRaw), string(params.AssignedAgent), string(params.Priority), string(params.ExecutionTarget), effectiveWorkspacePath, params.IdempotencyKey, correlationID, now, params.ParentTaskID, rootTaskID, string(taskKind)); err != nil {
		return nil, err
	}

	if params.QueueOnCreate {
		if err := transitionTaskStateTx(ctx, tx, taskID, domain.TaskStateCreated, domain.TaskStateQueued, params.Entrypoint+":create", "Queued for "+string(params.AssignedAgent)+" via "+string(params.ExecutionTarget), now); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, taskID)
}

func (s *PostgresStore) UpdateTaskResults(ctx context.Context, taskID string, results any) error {
	raw, err := json.Marshal(results)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE tasks
		   SET results = $2::jsonb,
		       updated_at = NOW()
		 WHERE id = $1
	`, taskID, string(raw))
	return err
}

func (s *PostgresStore) CancelTask(ctx context.Context, taskID, actor, reason string) error {
	return s.UpdateTaskState(ctx, taskID, domain.TaskStateCancelled, actor, reason)
}

func (s *PostgresStore) UpdateTaskState(ctx context.Context, taskID string, targetState domain.TaskState, actor, reason string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentState string
	row := tx.QueryRow(ctx, `SELECT state::text FROM tasks WHERE id = $1 FOR UPDATE`, taskID)
	if err := row.Scan(&currentState); err != nil {
		return err
	}
	fromState := domain.TaskState(currentState)
	if !domain.IsValidTransition(fromState, targetState) {
		return fmt.Errorf("invalid_transition:%s:%s", fromState, targetState)
	}
	if err := transitionTaskStateTx(ctx, tx, taskID, fromState, targetState, actor, reason, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ListTasks(ctx context.Context, filter TaskListFilter) ([]domain.TaskResponse, *string, error) {
	query := strings.Builder{}
	query.WriteString(taskSelectSQL)
	var clauses []string
	args := []any{}
	nextArg := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if filter.State != "" {
		clauses = append(clauses, "state = "+nextArg(string(filter.State)))
	}
	if filter.Agent != "" {
		clauses = append(clauses, "assigned_agent = "+nextArg(string(filter.Agent)))
	}
	if filter.Priority != "" {
		clauses = append(clauses, "priority = "+nextArg(string(filter.Priority)))
	}
	if filter.ExecutionTarget != "" {
		clauses = append(clauses, "execution_target = "+nextArg(string(filter.ExecutionTarget)))
	}
	if filter.ParentTaskID != "" {
		clauses = append(clauses, "parent_task_id = "+nextArg(filter.ParentTaskID))
	}
	if filter.RootTaskID != "" {
		clauses = append(clauses, "root_task_id = "+nextArg(filter.RootTaskID))
	}
	if filter.DateFrom != nil {
		clauses = append(clauses, "created_at >= "+nextArg(*filter.DateFrom))
	}
	if filter.DateTo != nil {
		clauses = append(clauses, "created_at <= "+nextArg(*filter.DateTo))
	}
	if filter.Cursor != "" {
		payload, err := domain.DecodeCursor(filter.Cursor)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(
			clauses,
			fmt.Sprintf("(created_at < %s OR (created_at = %s AND id::text < %s))", nextArg(payload.CreatedAt), nextArg(payload.CreatedAt), nextArg(payload.ID)),
		)
	}
	if len(clauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(clauses, " AND "))
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query.WriteString(" ORDER BY created_at DESC, id DESC LIMIT ")
	query.WriteString(nextArg(limit + 1))

	rows, err := s.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	items, err := collectTasks(rows)
	if err != nil {
		return nil, nil, err
	}

	var cursor *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		encoded, err := domain.EncodeCursor(last.CreatedAt, last.ID)
		if err != nil {
			return nil, nil, err
		}
		cursor = &encoded
	}
	return items, cursor, nil
}

func (s *PostgresStore) UpsertLocalBridge(ctx context.Context, params UpsertLocalBridgeParams) (*domain.LocalBridgeResponse, error) {
	capabilitiesRaw, err := json.Marshal(nonNilMap(params.Capabilities))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if strings.TrimSpace(params.Status) == "" {
		params.Status = "active"
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO local_bridges (
			id, name, hostname, workspace_root, status, capabilities, api_key_name, last_heartbeat, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8, $8
		)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			hostname = EXCLUDED.hostname,
			workspace_root = EXCLUDED.workspace_root,
			status = EXCLUDED.status,
			capabilities = EXCLUDED.capabilities,
			api_key_name = EXCLUDED.api_key_name,
			last_heartbeat = EXCLUDED.last_heartbeat,
			updated_at = EXCLUDED.updated_at
	`, params.ID, params.Name, params.Hostname, params.WorkspaceRoot, params.Status, string(capabilitiesRaw), params.APIKeyName, now); err != nil {
		return nil, err
	}
	return s.GetLocalBridge(ctx, params.ID)
}

func (s *PostgresStore) GetLocalBridge(ctx context.Context, bridgeID string) (*domain.LocalBridgeResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, name, hostname, workspace_root, status, capabilities, api_key_name, last_heartbeat, created_at, updated_at
		FROM local_bridges
		WHERE id = $1
	`, bridgeID)
	item, err := scanLocalBridge(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return item, err
}

func (s *PostgresStore) HeartbeatLocalBridge(ctx context.Context, bridgeID, status string) (*domain.LocalBridgeResponse, error) {
	now := time.Now().UTC()
	if strings.TrimSpace(status) == "" {
		status = "active"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE local_bridges
		   SET status = $2,
		       last_heartbeat = $3,
		       updated_at = $3
		 WHERE id = $1
	`, bridgeID, status, now)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}
	return s.GetLocalBridge(ctx, bridgeID)
}

func (s *PostgresStore) ListLocalBridges(ctx context.Context) ([]domain.LocalBridgeResponse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, hostname, workspace_root, status, capabilities, api_key_name, last_heartbeat, created_at, updated_at
		FROM local_bridges
		WHERE status = 'active'
		ORDER BY updated_at DESC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.LocalBridgeResponse, 0)
	for rows.Next() {
		item, err := scanLocalBridge(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ClaimNextLocalBridgeTask(ctx context.Context, bridgeID string) (*domain.LocalBridgeTaskClaimResponse, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bridge, err := getLocalBridgeForUpdateTx(ctx, tx, bridgeID)
	if err != nil {
		return nil, err
	}
	if bridge == nil {
		return nil, nil
	}

	row := tx.QueryRow(ctx, taskSelectSQL+`
		WHERE execution_target = 'local'
		  AND assigned_agent = 'coder'
		  AND state = 'queued'
		  AND (COALESCE(metadata->>'workspace_root', '') = '' OR metadata->>'workspace_root' = $1)
		ORDER BY created_at ASC, id ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`, bridge.WorkspaceRoot)
	task, err := scanTask(row)
	if err == pgx.ErrNoRows {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if err := transitionTaskStateTx(ctx, tx, task.ID, domain.TaskStateQueued, domain.TaskStateAssigned, "bridge:"+bridgeID, "Dequeued for local bridge", now); err != nil {
		return nil, err
	}
	if err := transitionTaskStateTx(ctx, tx, task.ID, domain.TaskStateAssigned, domain.TaskStateInProgress, "bridge:"+bridgeID, "Execution started in local bridge", now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	metadata := task.Metadata
	workspaceRoot := strings.TrimSpace(asString(metadata["workspace_root"]))
	if workspaceRoot == "" {
		workspaceRoot = bridge.WorkspaceRoot
	}
	repoPath := strings.TrimSpace(asString(metadata["repo_path"]))
	if repoPath == "" {
		repoPath = workspaceRoot
	}
	branch := strings.TrimSpace(asString(metadata["branch"]))
	if branch == "" {
		branch = "main"
	}

	assignedAgent := ""
	if task.AssignedAgent != nil {
		assignedAgent = string(*task.AssignedAgent)
	}
	workspacePath := ""
	if task.WorkspacePath != nil {
		workspacePath = *task.WorkspacePath
	}

	return &domain.LocalBridgeTaskClaimResponse{
		TaskID:          task.ID,
		RootTaskID:      task.RootTaskID,
		ParentTaskID:    task.ParentTaskID,
		Description:     task.Description,
		AssignedAgent:   assignedAgent,
		ExecutionTarget: string(task.ExecutionTarget),
		WorkspacePath:   workspacePath,
		WorkspaceRoot:   workspaceRoot,
		Metadata:        metadata,
		RepoPath:        repoPath,
		Branch:          branch,
	}, nil
}

func (s *PostgresStore) ListChildren(ctx context.Context, parentTaskID string) ([]domain.TaskResponse, error) {
	rows, err := s.pool.Query(ctx, taskSelectSQL+` WHERE parent_task_id = $1 ORDER BY created_at ASC, id ASC`, parentTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func (s *PostgresStore) ListByRoot(ctx context.Context, rootTaskID string) ([]domain.TaskResponse, error) {
	rows, err := s.pool.Query(ctx, taskSelectSQL+` WHERE root_task_id = $1 ORDER BY created_at ASC, id ASC`, rootTaskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func (s *PostgresStore) ListWorkspaceCleanupCandidates(ctx context.Context, cutoff time.Time, limit int) ([]domain.TaskResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, taskSelectSQL+`
		WHERE state IN ('completed', 'failed', 'cancelled')
		  AND workspace_path IS NOT NULL
		  AND completed_at IS NOT NULL
		  AND completed_at < $1
		ORDER BY completed_at ASC, id ASC
		LIMIT $2
	`, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func (s *PostgresStore) ClearWorkspacePath(ctx context.Context, taskID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks
		   SET workspace_path = NULL,
		       updated_at = NOW()
		 WHERE id = $1
	`, taskID)
	return err
}

func (s *PostgresStore) GetApproval(ctx context.Context, approvalID string) (*domain.ApprovalResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, task_id::text, action_type, target_resource, status::text, operator,
		       timeout_seconds, escalation_level, requested_at, resolved_at, timeout_at
		FROM approvals
		WHERE id = $1
	`, approvalID)
	approval, err := scanApproval(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return approval, err
}

func (s *PostgresStore) ResolveApproval(ctx context.Context, approvalID string, approve bool, operator string) (*domain.ApprovalResponse, *domain.TaskResponse, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		approval      domain.ApprovalResponse
		taskID        string
		currentStatus string
	)
	row := tx.QueryRow(ctx, `
		SELECT id::text, task_id::text, action_type, target_resource, status::text, operator,
		       timeout_seconds, escalation_level, requested_at, resolved_at, timeout_at
		FROM approvals
		WHERE id = $1
		FOR UPDATE
	`, approvalID)
	if err := row.Scan(
		&approval.ID,
		&taskID,
		&approval.ActionType,
		&approval.TargetResource,
		&currentStatus,
		&approval.Operator,
		&approval.TimeoutSeconds,
		&approval.EscalationLevel,
		&approval.RequestedAt,
		&approval.ResolvedAt,
		&approval.TimeoutAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, ErrApprovalNotFound
		}
		return nil, nil, err
	}
	approval.TaskID = taskID
	approval.Status = domain.ApprovalStatus(currentStatus)
	if approval.Status != domain.ApprovalPending {
		return nil, nil, ErrApprovalAlreadyResolved
	}

	task, err := s.GetTaskForUpdateTx(ctx, tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, fmt.Errorf("task %s not found for approval %s", taskID, approvalID)
	}

	now := time.Now().UTC()
	newApprovalStatus := domain.ApprovalRejected
	targetTaskState := domain.TaskStateCancelled
	actor := "operator:" + operator
	reason := "approval_rejected"
	if approve {
		newApprovalStatus = domain.ApprovalApproved
		targetTaskState = domain.TaskStateQueued
		reason = "Approval granted"
	}

	if _, err := tx.Exec(ctx, `
		UPDATE approvals
		   SET status = $2,
		       operator = $3,
		       resolved_at = $4
		 WHERE id = $1
	`, approvalID, string(newApprovalStatus), operator, now); err != nil {
		return nil, nil, err
	}

	mergedMetadata := cloneMap(task.Metadata)
	if mergedMetadata == nil {
		mergedMetadata = map[string]any{}
	}
	if approve {
		mergedMetadata["approval_granted"] = true
		delete(mergedMetadata, "approval_requested")
	} else {
		mergedMetadata["approval_granted"] = false
	}
	metadataRaw, err := json.Marshal(mergedMetadata)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		   SET metadata = $2::jsonb
		 WHERE id = $1
	`, taskID, string(metadataRaw)); err != nil {
		return nil, nil, err
	}

	if !domain.IsValidTransition(task.State, targetTaskState) {
		return nil, nil, fmt.Errorf("invalid_transition:%s:%s", task.State, targetTaskState)
	}
	if err := transitionTaskStateTx(ctx, tx, taskID, task.State, targetTaskState, actor, reason, now); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	updatedApproval, err := s.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, nil, err
	}
	updatedTask, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	return updatedApproval, updatedTask, nil
}

func (s *PostgresStore) RequestApproval(ctx context.Context, taskID, actionType, targetResource string, timeoutSeconds int, actor, reason string, metadataPatch map[string]any) (*domain.ApprovalResponse, error) {
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
	if !domain.IsValidTransition(task.State, domain.TaskStateWaitingApproval) {
		return nil, fmt.Errorf("invalid_transition:%s:%s", task.State, domain.TaskStateWaitingApproval)
	}

	mergedMetadata := cloneMap(task.Metadata)
	if mergedMetadata == nil {
		mergedMetadata = map[string]any{}
	}
	for key, value := range metadataPatch {
		mergedMetadata[key] = value
	}
	metadataRaw, err := json.Marshal(mergedMetadata)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	timeoutAt := now.Add(time.Duration(timeoutSeconds) * time.Second)
	approvalID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO approvals (
			id, task_id, action_type, target_resource, status, operator,
			timeout_seconds, escalation_level, requested_at, resolved_at, timeout_at
		) VALUES (
			$1, $2, $3, $4, $5::approvalstatus, NULL, $6, 1, $7, NULL, $8
		)
	`, approvalID, taskID, actionType, targetResource, string(domain.ApprovalPending), timeoutSeconds, now, timeoutAt); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		   SET metadata = $2::jsonb
		 WHERE id = $1
	`, taskID, string(metadataRaw)); err != nil {
		return nil, err
	}

	if err := transitionTaskStateTx(ctx, tx, taskID, task.State, domain.TaskStateWaitingApproval, actor, reason, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetApproval(ctx, approvalID)
}

func (s *PostgresStore) CompleteTask(ctx context.Context, taskID, actor, reason string, results any) (*domain.TaskResponse, error) {
	return s.finalizeTask(ctx, taskID, domain.TaskStateCompleted, actor, reason, results, nil)
}

func (s *PostgresStore) FailTask(ctx context.Context, taskID, actor, reason, errorMessage string, results any) (*domain.TaskResponse, error) {
	msg := errorMessage
	return s.finalizeTask(ctx, taskID, domain.TaskStateFailed, actor, reason, results, &msg)
}

type CreateResearchRunParams struct {
	TaskID               *string
	Entrypoint           string
	Query                string
	IntentType           string
	SelectedCapabilities []string
	FinalAnswer          string
	Confidence           *float64
	SourceArtifactIDs    []string
	ToolInvocationIDs    []string
	Metadata             map[string]any
}

type CreateToolInvocationParams struct {
	TaskID        *string
	AgentType     *string
	Entrypoint    string
	Capability    string
	InputPayload  map[string]any
	OutputPayload map[string]any
	Status        string
	DurationMS    int
	SourceRefs    []domain.SourceRef
	ArtifactIDs   []string
	ErrorMessage  *string
}

type CreateArtifactParams struct {
	TaskID       *string
	InvocationID *string
	ArtifactType string
	Title        *string
	URI          *string
	MediaType    *string
	ContentText  *string
	Metadata     map[string]any
}

type CreateEvaluationRunParams struct {
	ResearchRunID     string
	ReferenceProvider string
	ReferenceModel    *string
	ReferenceAnswer   *string
	JudgeModel        *string
}

type CreateEvaluationDatasetItemParams struct {
	ResearchRunID      string
	EvaluationRunID    *string
	Query              string
	OrchestratorAnswer string
	ReferenceAnswer    string
	Sources            []domain.SourceRef
	Scores             map[string]any
	Winner             *string
	Metadata           map[string]any
}

func (s *PostgresStore) CreateResearchRun(ctx context.Context, params CreateResearchRunParams) (*domain.ResearchRunResponse, error) {
	selectedRaw, err := json.Marshal(params.SelectedCapabilities)
	if err != nil {
		return nil, err
	}
	sourceRaw, err := json.Marshal(params.SourceArtifactIDs)
	if err != nil {
		return nil, err
	}
	invocationRaw, err := json.Marshal(params.ToolInvocationIDs)
	if err != nil {
		return nil, err
	}
	metadata := cloneMap(params.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	researchID := uuid.NewString()
	var taskID any
	if params.TaskID != nil && strings.TrimSpace(*params.TaskID) != "" {
		taskID = *params.TaskID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO research_runs (
			id, task_id, entrypoint, query, intent_type, selected_capabilities,
			final_answer, confidence, source_artifact_ids, tool_invocation_ids, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9::jsonb, $10::jsonb, $11::jsonb
		)
	`, researchID, taskID, params.Entrypoint, params.Query, params.IntentType, string(selectedRaw), params.FinalAnswer, params.Confidence, string(sourceRaw), string(invocationRaw), string(metadataRaw)); err != nil {
		return nil, err
	}
	return s.GetResearchRun(ctx, researchID)
}

func (s *PostgresStore) GetResearchRun(ctx context.Context, researchRunID string) (*domain.ResearchRunResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, task_id::text, entrypoint, query, intent_type,
		       selected_capabilities, final_answer, confidence,
		       source_artifact_ids, tool_invocation_ids, metadata, created_at
		FROM research_runs
		WHERE id = $1
	`, researchRunID)
	var (
		item          domain.ResearchRunResponse
		taskID        *string
		selectedRaw   []byte
		sourceRaw     []byte
		invocationRaw []byte
		metadataRaw   []byte
	)
	if err := row.Scan(
		&item.ID,
		&taskID,
		&item.Entrypoint,
		&item.Query,
		&item.IntentType,
		&selectedRaw,
		&item.FinalAnswer,
		&item.Confidence,
		&sourceRaw,
		&invocationRaw,
		&metadataRaw,
		&item.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.TaskID = taskID
	_ = json.Unmarshal(selectedRaw, &item.SelectedCapabilities)
	_ = json.Unmarshal(sourceRaw, &item.SourceArtifactIDs)
	_ = json.Unmarshal(invocationRaw, &item.ToolInvocationIDs)
	_ = json.Unmarshal(metadataRaw, &item.Metadata)
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return &item, nil
}

func (s *PostgresStore) CreateToolInvocation(ctx context.Context, params CreateToolInvocationParams) (string, error) {
	inputRaw, err := json.Marshal(nonNilMap(params.InputPayload))
	if err != nil {
		return "", err
	}
	outputRaw, err := json.Marshal(nonNilMap(params.OutputPayload))
	if err != nil {
		return "", err
	}
	sourceRaw, err := json.Marshal(params.SourceRefs)
	if err != nil {
		return "", err
	}
	artifactRaw, err := json.Marshal(params.ArtifactIDs)
	if err != nil {
		return "", err
	}
	invocationID := uuid.NewString()
	var taskID any
	if params.TaskID != nil && strings.TrimSpace(*params.TaskID) != "" {
		taskID = *params.TaskID
	}
	var agentType any
	if params.AgentType != nil && strings.TrimSpace(*params.AgentType) != "" {
		agentType = *params.AgentType
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO tool_invocations (
			id, task_id, agent_type, entrypoint, capability, input_payload, output_payload,
			status, duration_ms, source_refs, artifact_ids, error_message
		) VALUES (
			$1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10::jsonb, $11::jsonb, $12
		)
	`, invocationID, taskID, agentType, params.Entrypoint, params.Capability, string(inputRaw), string(outputRaw), params.Status, params.DurationMS, string(sourceRaw), string(artifactRaw), params.ErrorMessage); err != nil {
		return "", err
	}
	return invocationID, nil
}

func (s *PostgresStore) UpdateToolInvocationArtifactIDs(ctx context.Context, invocationID string, artifactIDs []string) error {
	raw, err := json.Marshal(artifactIDs)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE tool_invocations SET artifact_ids = $2::jsonb WHERE id = $1`, invocationID, string(raw))
	return err
}

func (s *PostgresStore) CreateArtifact(ctx context.Context, params CreateArtifactParams) (string, error) {
	metadataRaw, err := json.Marshal(nonNilMap(params.Metadata))
	if err != nil {
		return "", err
	}
	artifactID := uuid.NewString()
	var taskID any
	if params.TaskID != nil && strings.TrimSpace(*params.TaskID) != "" {
		taskID = *params.TaskID
	}
	var invocationID any
	if params.InvocationID != nil && strings.TrimSpace(*params.InvocationID) != "" {
		invocationID = *params.InvocationID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO artifacts (
			id, task_id, invocation_id, artifact_type, title, uri, media_type, content_text, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb
		)
	`, artifactID, taskID, invocationID, params.ArtifactType, params.Title, params.URI, params.MediaType, params.ContentText, string(metadataRaw)); err != nil {
		return "", err
	}
	return artifactID, nil
}

func (s *PostgresStore) GetToolInvocation(ctx context.Context, invocationID string) (*domain.ToolInvocationResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, task_id::text, entrypoint, capability, status, duration_ms,
		       output_payload, source_refs, artifact_ids, error_message, created_at
		FROM tool_invocations
		WHERE id = $1
	`, invocationID)
	var (
		item        domain.ToolInvocationResponse
		taskID      *string
		outputRaw   []byte
		sourceRaw   []byte
		artifactRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&taskID,
		&item.EntryPoint,
		&item.Capability,
		&item.Status,
		&item.DurationMS,
		&outputRaw,
		&sourceRaw,
		&artifactRaw,
		&item.ErrorMessage,
		&item.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	item.TaskID = taskID
	_ = json.Unmarshal(outputRaw, &item.Output)
	_ = json.Unmarshal(sourceRaw, &item.SourceRefs)
	_ = json.Unmarshal(artifactRaw, &item.ArtifactIDs)
	if item.Output == nil {
		item.Output = map[string]any{}
	}
	summary := strings.TrimSpace(asString(item.Output["summary"]))
	if summary == "" {
		summary = strings.TrimSpace(asString(item.Output["content_text"]))
	}
	if summary != "" {
		item.Summary = &summary
	}
	return &item, nil
}

func (s *PostgresStore) ListArtifactsByInvocation(ctx context.Context, invocationID string) ([]domain.ArtifactResponse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, artifact_type, title, uri, media_type, content_text, metadata, created_at
		FROM artifacts
		WHERE invocation_id = $1
		ORDER BY created_at ASC
	`, invocationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectArtifacts(rows)
}

func (s *PostgresStore) ListArtifactsByTask(ctx context.Context, taskID string) ([]domain.ArtifactResponse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, artifact_type, title, uri, media_type, content_text, metadata, created_at
		FROM artifacts
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectArtifacts(rows)
}

func (s *PostgresStore) ListArtifactsByIDs(ctx context.Context, ids []string) ([]domain.ArtifactResponse, error) {
	if len(ids) == 0 {
		return []domain.ArtifactResponse{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, artifact_type, title, uri, media_type, content_text, metadata, created_at
		FROM artifacts
		WHERE id::text = ANY($1)
		ORDER BY created_at ASC
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectArtifacts(rows)
}

func (s *PostgresStore) CreateEvaluationRun(ctx context.Context, params CreateEvaluationRunParams) (*domain.EvaluationRunResponse, error) {
	evaluationID := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO evaluation_runs (
			id, research_run_id, reference_provider, reference_model, reference_answer, judge_model
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)
	`, evaluationID, params.ResearchRunID, params.ReferenceProvider, params.ReferenceModel, params.ReferenceAnswer, params.JudgeModel); err != nil {
		return nil, err
	}
	return s.GetEvaluationRun(ctx, evaluationID)
}

func (s *PostgresStore) GetEvaluationRun(ctx context.Context, evaluationRunID string) (*domain.EvaluationRunResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, research_run_id::text, reference_provider, reference_model, reference_answer,
		       judge_model, judge_verdict, judge_scores, winner, created_at
		FROM evaluation_runs
		WHERE id = $1
	`, evaluationRunID)
	var (
		item           domain.EvaluationRunResponse
		verdictRaw     []byte
		judgeScoresRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.ResearchRunID,
		&item.ReferenceProvider,
		&item.ReferenceModel,
		&item.ReferenceAnswer,
		&item.JudgeModel,
		&verdictRaw,
		&judgeScoresRaw,
		&item.Winner,
		&item.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(verdictRaw, &item.JudgeVerdict)
	_ = json.Unmarshal(judgeScoresRaw, &item.JudgeScores)
	if item.JudgeVerdict == nil {
		item.JudgeVerdict = map[string]any{}
	}
	if item.JudgeScores == nil {
		item.JudgeScores = map[string]any{}
	}
	return &item, nil
}

func (s *PostgresStore) UpdateEvaluationRunJudgement(ctx context.Context, evaluationRunID string, judgeModel *string, verdict map[string]any, scores map[string]any, winner *string) (*domain.EvaluationRunResponse, error) {
	verdictRaw, err := json.Marshal(nonNilMap(verdict))
	if err != nil {
		return nil, err
	}
	scoresRaw, err := json.Marshal(nonNilMap(scores))
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE evaluation_runs
		   SET judge_model = $2,
		       judge_verdict = $3::jsonb,
		       judge_scores = $4::jsonb,
		       winner = $5
		 WHERE id = $1
	`, evaluationRunID, judgeModel, string(verdictRaw), string(scoresRaw), winner); err != nil {
		return nil, err
	}
	return s.GetEvaluationRun(ctx, evaluationRunID)
}

func (s *PostgresStore) CreateEvaluationDatasetItem(ctx context.Context, params CreateEvaluationDatasetItemParams) (*domain.EvaluationDatasetItemResponse, error) {
	datasetID := uuid.NewString()
	sourceRaw, err := json.Marshal(params.Sources)
	if err != nil {
		return nil, err
	}
	scoreRaw, err := json.Marshal(nonNilMap(params.Scores))
	if err != nil {
		return nil, err
	}
	metaRaw, err := json.Marshal(nonNilMap(params.Metadata))
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO evaluation_dataset_items (
			id, research_run_id, evaluation_run_id, query, orchestrator_answer, reference_answer,
			sources, scores, winner, metadata
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10::jsonb
		)
	`, datasetID, params.ResearchRunID, params.EvaluationRunID, params.Query, params.OrchestratorAnswer, params.ReferenceAnswer, string(sourceRaw), string(scoreRaw), params.Winner, string(metaRaw)); err != nil {
		return nil, err
	}
	return s.GetEvaluationDatasetItem(ctx, datasetID)
}

func (s *PostgresStore) GetEvaluationDatasetItem(ctx context.Context, datasetID string) (*domain.EvaluationDatasetItemResponse, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id::text, research_run_id::text, evaluation_run_id::text, query, orchestrator_answer,
		       reference_answer, sources, scores, winner, metadata, created_at
		FROM evaluation_dataset_items
		WHERE id = $1
	`, datasetID)
	return scanEvaluationDatasetItem(row)
}

func (s *PostgresStore) ListEvaluationDatasetItems(ctx context.Context, limit int) ([]domain.EvaluationDatasetItemResponse, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, research_run_id::text, evaluation_run_id::text, query, orchestrator_answer,
		       reference_answer, sources, scores, winner, metadata, created_at
		FROM evaluation_dataset_items
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.EvaluationDatasetItemResponse, 0)
	for rows.Next() {
		item, err := scanEvaluationDatasetItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListApprovals(ctx context.Context, filter ApprovalListFilter) ([]domain.ApprovalResponse, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT id::text, task_id::text, action_type, target_resource, status::text, operator,
		       timeout_seconds, escalation_level, requested_at, resolved_at, timeout_at
		FROM approvals
	`
	args := []any{}
	if filter.Status != "" {
		query += ` WHERE status = $1`
		args = append(args, string(filter.Status))
	}
	query += ` ORDER BY requested_at DESC LIMIT $` + fmt.Sprintf("%d", len(args)+1)
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.ApprovalResponse, 0)
	for rows.Next() {
		item, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

type TaskListFilter struct {
	State           domain.TaskState
	Agent           domain.AgentType
	Priority        domain.Priority
	ExecutionTarget domain.ExecutionTarget
	ParentTaskID    string
	RootTaskID      string
	DateFrom        *time.Time
	DateTo          *time.Time
	Cursor          string
	Limit           int
}

type ApprovalListFilter struct {
	Status domain.ApprovalStatus
	Limit  int
}

const taskSelectSQL = `
	SELECT id::text, state::text, description, metadata, assigned_agent::text, priority::text,
	       execution_target::text, workspace_path, retry_count, correlation_id::text, results,
	       error_message, created_at, updated_at, started_at, completed_at,
	       parent_task_id::text, root_task_id::text, task_kind::text
	FROM tasks
`

type taskScanner interface {
	Scan(dest ...any) error
}

type txScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) GetTaskForUpdateTx(ctx context.Context, tx pgx.Tx, taskID string) (*domain.TaskResponse, error) {
	row := tx.QueryRow(ctx, taskSelectSQL+` WHERE id = $1 FOR UPDATE`, taskID)
	task, err := scanTask(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return task, err
}

func (s *PostgresStore) finalizeTask(ctx context.Context, taskID string, targetState domain.TaskState, actor, reason string, results any, errorMessage *string) (*domain.TaskResponse, error) {
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
	if !domain.IsValidTransition(task.State, targetState) {
		return nil, fmt.Errorf("invalid_transition:%s:%s", task.State, targetState)
	}

	var resultsRaw []byte
	if results != nil {
		resultsRaw, err = json.Marshal(results)
		if err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		   SET results = CASE WHEN $2::jsonb IS NULL THEN results ELSE $2::jsonb END,
		       error_message = $3
		 WHERE id = $1
	`, taskID, nullableJSONB(resultsRaw), errorMessage); err != nil {
		return nil, err
	}
	if err := transitionTaskStateTx(ctx, tx, taskID, task.State, targetState, actor, reason, time.Now().UTC()); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetTask(ctx, taskID)
}

func nullableJSONB(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func nonNilMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	return input
}

type artifactScanner interface {
	Scan(dest ...any) error
}

func scanArtifact(row artifactScanner) (*domain.ArtifactResponse, error) {
	var (
		item        domain.ArtifactResponse
		metadataRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.ArtifactType,
		&item.Title,
		&item.URI,
		&item.MediaType,
		&item.ContentText,
		&metadataRaw,
		&item.CreatedAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(metadataRaw, &item.Metadata)
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return &item, nil
}

type evaluationDatasetScanner interface {
	Scan(dest ...any) error
}

func scanEvaluationDatasetItem(row evaluationDatasetScanner) (*domain.EvaluationDatasetItemResponse, error) {
	var (
		item        domain.EvaluationDatasetItemResponse
		sourceRaw   []byte
		scoreRaw    []byte
		metadataRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.ResearchRunID,
		&item.EvaluationRunID,
		&item.Query,
		&item.OrchestratorAnswer,
		&item.ReferenceAnswer,
		&sourceRaw,
		&scoreRaw,
		&item.Winner,
		&metadataRaw,
		&item.CreatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	_ = json.Unmarshal(sourceRaw, &item.Sources)
	_ = json.Unmarshal(scoreRaw, &item.Scores)
	_ = json.Unmarshal(metadataRaw, &item.Metadata)
	if item.Scores == nil {
		item.Scores = map[string]any{}
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	return &item, nil
}

func collectArtifacts(rows pgx.Rows) ([]domain.ArtifactResponse, error) {
	items := make([]domain.ArtifactResponse, 0)
	for rows.Next() {
		item, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func scanTask(row taskScanner) (*domain.TaskResponse, error) {
	var (
		item            domain.TaskResponse
		metadataRaw     []byte
		resultsRaw      []byte
		assignedAgent   *string
		errorMessage    *string
		workspacePath   *string
		parentTaskID    *string
		rootTaskID      *string
		startedAt       *time.Time
		completedAt     *time.Time
		taskKind        string
		executionTarget string
		priority        string
		state           string
		correlationID   string
	)
	err := row.Scan(
		&item.ID,
		&state,
		&item.Description,
		&metadataRaw,
		&assignedAgent,
		&priority,
		&executionTarget,
		&workspacePath,
		&item.RetryCount,
		&correlationID,
		&resultsRaw,
		&errorMessage,
		&item.CreatedAt,
		&item.UpdatedAt,
		&startedAt,
		&completedAt,
		&parentTaskID,
		&rootTaskID,
		&taskKind,
	)
	if err != nil {
		return nil, err
	}
	item.State = domain.TaskState(state)
	item.Priority = domain.Priority(priority)
	item.ExecutionTarget = domain.ExecutionTarget(executionTarget)
	item.TaskKind = domain.TaskKind(taskKind)
	item.CorrelationID = correlationID
	item.WorkspacePath = workspacePath
	item.ErrorMessage = errorMessage
	item.ParentTaskID = parentTaskID
	item.RootTaskID = rootTaskID
	item.StartedAt = startedAt
	item.CompletedAt = completedAt
	if assignedAgent != nil && *assignedAgent != "" {
		value := domain.AgentType(*assignedAgent)
		item.AssignedAgent = &value
	}
	if len(metadataRaw) > 0 {
		_ = json.Unmarshal(metadataRaw, &item.Metadata)
	} else {
		item.Metadata = map[string]any{}
	}
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}
	if allowedRaw, ok := item.Metadata["allowed_capabilities"].([]any); ok {
		for _, entry := range allowedRaw {
			item.AllowedCapabilities = append(item.AllowedCapabilities, fmt.Sprint(entry))
		}
	}
	if len(resultsRaw) > 0 {
		var results any
		_ = json.Unmarshal(resultsRaw, &results)
		item.Results = results
	}
	return &item, nil
}

type localBridgeScanner interface {
	Scan(dest ...any) error
}

func scanLocalBridge(row localBridgeScanner) (*domain.LocalBridgeResponse, error) {
	var (
		item            domain.LocalBridgeResponse
		capabilitiesRaw []byte
	)
	if err := row.Scan(
		&item.ID,
		&item.Name,
		&item.Hostname,
		&item.WorkspaceRoot,
		&item.Status,
		&capabilitiesRaw,
		&item.APIKeyName,
		&item.LastHeartbeat,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(capabilitiesRaw, &item.Capabilities)
	if item.Capabilities == nil {
		item.Capabilities = map[string]any{}
	}
	return &item, nil
}

func getLocalBridgeForUpdateTx(ctx context.Context, tx pgx.Tx, bridgeID string) (*domain.LocalBridgeResponse, error) {
	row := tx.QueryRow(ctx, `
		SELECT id::text, name, hostname, workspace_root, status, capabilities, api_key_name, last_heartbeat, created_at, updated_at
		FROM local_bridges
		WHERE id = $1
		FOR UPDATE
	`, bridgeID)
	item, err := scanLocalBridge(row)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return item, err
}

func collectTasks(rows pgx.Rows) ([]domain.TaskResponse, error) {
	items := make([]domain.TaskResponse, 0)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func transitionTaskStateTx(ctx context.Context, tx pgx.Tx, taskID string, fromState, toState domain.TaskState, actor, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE tasks
		   SET state = $2::taskstate,
		       updated_at = $3,
		       queued_at = CASE WHEN $2::text = 'queued' THEN $3 ELSE queued_at END,
		       started_at = CASE
		           WHEN $2::text = 'in_progress' AND started_at IS NULL THEN $3
		           ELSE started_at
		       END,
		       completed_at = CASE WHEN $2::text IN ('completed','failed','cancelled') THEN $3 ELSE completed_at END
		 WHERE id = $1
	`, taskID, string(toState), now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO state_transitions (id, task_id, from_state, to_state, actor, reason, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, uuid.NewString(), taskID, string(fromState), string(toState), actor, reason, now)
	return err
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

func asString(input any) string {
	switch v := input.(type) {
	case string:
		return v
	default:
		return ""
	}
}

type approvalScanner interface {
	Scan(dest ...any) error
}

func scanApproval(row approvalScanner) (*domain.ApprovalResponse, error) {
	var item domain.ApprovalResponse
	err := row.Scan(
		&item.ID,
		&item.TaskID,
		&item.ActionType,
		&item.TargetResource,
		&item.Status,
		&item.Operator,
		&item.TimeoutSeconds,
		&item.EscalationLevel,
		&item.RequestedAt,
		&item.ResolvedAt,
		&item.TimeoutAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}
