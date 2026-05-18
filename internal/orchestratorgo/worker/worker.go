package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/runner"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type RuntimeWorker struct {
	cfg         config.Config
	postgres    *store.PostgresStore
	redis       *store.RedisStore
	runner      *runner.TaskRunner
	workerID    string
	startedAt   time.Time
	cancel      context.CancelFunc
	done        chan struct{}
	currentTask *string
	mu          sync.RWMutex
}

func New(cfg config.Config, postgres *store.PostgresStore, redis *store.RedisStore, researchService *research.Service) *RuntimeWorker {
	return &RuntimeWorker{
		cfg:       cfg,
		postgres:  postgres,
		redis:     redis,
		runner:    runner.New(cfg, researchService),
		workerID:  store.GenerateWorkerID(),
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
}

func (w *RuntimeWorker) Start(parent context.Context) error {
	if !w.cfg.WorkerEnabled() {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	if err := w.redis.RegisterWorker(ctx, w.workerID, "planner,researcher,coder,reviewer"); err != nil {
		return err
	}
	if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, nil, w.startedAt); err != nil {
		return err
	}
	go w.run(ctx)
	return nil
}

func (w *RuntimeWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
		<-w.done
	}
}

func (w *RuntimeWorker) run(ctx context.Context) {
	defer close(w.done)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.redis.DeregisterWorker(cleanupCtx, w.workerID); err != nil {
			log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to deregister runtime worker")
		}
	}()

	heartbeatTicker := time.NewTicker(time.Duration(w.cfg.WorkerHeartbeatInterval) * time.Second)
	defer heartbeatTicker.Stop()
	pollTicker := time.NewTicker(time.Duration(w.cfg.WorkerDequeueTimeoutSeconds) * time.Second)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeatTicker.C:
			if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, w.getCurrentTask(), w.startedAt); err != nil {
				log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to emit worker heartbeat")
			}
		case <-pollTicker.C:
			if w.getCurrentTask() != nil {
				continue
			}
			w.tryClaimTask(ctx, domain.AgentTypePlanner)
			if w.getCurrentTask() == nil {
				w.tryClaimTask(ctx, domain.AgentTypeResearcher)
			}
			if w.getCurrentTask() == nil {
				w.tryClaimTask(ctx, domain.AgentTypeCoder)
			}
			if w.getCurrentTask() == nil {
				w.tryClaimTask(ctx, domain.AgentTypeReviewer)
			}
		}
	}
}

func (w *RuntimeWorker) tryClaimTask(ctx context.Context, agentType domain.AgentType) {
	taskID, ok, err := w.redis.DequeueTask(ctx, agentType)
	if err != nil {
		log.Warn().Err(err).Str("worker_id", w.workerID).Str("agent_type", string(agentType)).Msg("failed to dequeue task")
		return
	}
	if !ok {
		return
	}
	task, err := w.postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		log.Warn().Err(err).Str("task_id", taskID).Msg("dequeued task missing in postgres")
		return
	}
	if task.State != domain.TaskStateQueued {
		log.Warn().Str("task_id", taskID).Str("state", string(task.State)).Msg("dequeued task not queued anymore")
		return
	}
	if task.ExecutionTarget == domain.ExecutionTargetLocal {
		log.Info().Str("task_id", taskID).Msg("skipping local task in embedded worker; reserved for local bridge")
		return
	}
	if err := w.postgres.UpdateTaskState(ctx, taskID, domain.TaskStateAssigned, "worker:"+w.workerID, "Dequeued for "+string(agentType)); err != nil {
		if !strings.HasPrefix(err.Error(), "invalid_transition:") {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed queued->assigned transition")
		}
		return
	}
	if err := w.postgres.UpdateTaskState(ctx, taskID, domain.TaskStateInProgress, "worker:"+w.workerID, "Execution started in Go worker"); err != nil {
		if !strings.HasPrefix(err.Error(), "invalid_transition:") {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed assigned->in_progress transition")
		}
		return
	}
	w.setCurrentTask(&taskID)
	if err := w.redis.UpdateWorkerCurrentTask(ctx, w.workerID, w.getCurrentTask()); err != nil {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to update current task")
	}
	if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, w.getCurrentTask(), w.startedAt); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to emit immediate heartbeat after claim")
	}
	log.Info().Str("worker_id", w.workerID).Str("task_id", taskID).Str("agent_type", string(agentType)).Msg("runtime worker claimed task")
	go w.executeTask(context.Background(), taskID)
}

func (w *RuntimeWorker) executeTask(ctx context.Context, taskID string) {
	task, err := w.postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		log.Warn().Err(err).Str("task_id", taskID).Msg("failed to load task for execution")
		w.releaseTask(ctx)
		return
	}

	result := w.runner.Execute(task)
	switch result.Status {
	case "success":
		if derefAgent(task.AssignedAgent) == domain.AgentTypePlanner {
			if err := w.handlePlannerSuccess(ctx, task, result); err != nil {
				log.Warn().Err(err).Str("task_id", taskID).Msg("failed to handle planner success")
				if _, failErr := w.postgres.FailTask(ctx, taskID, "worker:"+w.workerID, err.Error(), err.Error(), map[string]any{"status": "error"}); failErr != nil {
					log.Warn().Err(failErr).Str("task_id", taskID).Msg("failed to fail planner task after planner success handling error")
				}
			}
		} else {
			if _, err := w.postgres.CompleteTask(ctx, taskID, "worker:"+w.workerID, result.Summary, result.Results); err != nil {
				log.Warn().Err(err).Str("task_id", taskID).Msg("failed to complete task")
			}
			w.reconcileRootTask(ctx, taskID)
		}
	case "waiting_approval":
		timeoutSeconds := 300
		actionType := "task_execution"
		targetResource := "task"
		if result.Approval != nil {
			timeoutSeconds = result.Approval.TimeoutSeconds
			actionType = result.Approval.ActionType
			targetResource = result.Approval.TargetResource
		}
		if _, err := w.postgres.RequestApproval(
			ctx,
			taskID,
			actionType,
			targetResource,
			timeoutSeconds,
			"worker:"+w.workerID,
			result.Summary,
			map[string]any{"approval_requested": true},
		); err != nil {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed to request approval")
			if _, failErr := w.postgres.FailTask(ctx, taskID, "worker:"+w.workerID, "approval request failed", err.Error(), map[string]any{"status": "error"}); failErr != nil {
				log.Warn().Err(failErr).Str("task_id", taskID).Msg("failed to fail task after approval request error")
			}
		}
	default:
		errorMessage := strings.TrimSpace(result.ErrorMessage)
		if errorMessage == "" {
			errorMessage = "task execution failed"
		}
		if _, err := w.postgres.FailTask(ctx, taskID, "worker:"+w.workerID, errorMessage, errorMessage, result.Results); err != nil {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed to fail task")
		}
		w.reconcileRootTask(ctx, taskID)
	}
	w.releaseTask(ctx)
}

func (w *RuntimeWorker) handlePlannerSuccess(ctx context.Context, task *domain.TaskResponse, result runner.Result) error {
	if task == nil {
		return fmt.Errorf("planner task is nil")
	}
	if err := w.postgres.UpdateTaskResults(ctx, task.ID, result.Results); err != nil {
		return err
	}

	subtasks := parsePlannerSubtasks(result.Results["subtasks"])
	planOnly := asBoolMap(result.Results, "plan_only")
	if planOnly || len(subtasks) == 0 {
		_, err := w.postgres.CompleteTask(ctx, task.ID, "worker:"+w.workerID, "planner completed without execution subtasks", result.Results)
		return err
	}

	createdIDs := make([]string, 0, len(subtasks))
	rootID := task.ID
	if task.RootTaskID != nil && strings.TrimSpace(*task.RootTaskID) != "" {
		rootID = strings.TrimSpace(*task.RootTaskID)
	}

	for idx, spec := range subtasks {
		title := strings.TrimSpace(spec.Title)
		if title == "" {
			title = fmt.Sprintf("Step %d", idx+1)
		}
		description := strings.TrimSpace(spec.Description)
		if description == "" {
			description = title
		}
		agent := spec.AssignedAgent
		if agent != domain.AgentTypePlanner && agent != domain.AgentTypeCoder && agent != domain.AgentTypeResearcher && agent != domain.AgentTypeReviewer {
			agent = domain.AgentTypeCoder
		}
		priority := spec.Priority
		if priority == "" {
			priority = domain.PriorityNormal
		}

		childMetadata := map[string]any{
			"title":                title,
			"requires_approval":    spec.RequiresApproval,
			"planner_generated":    true,
			"parent_task_id":       task.ID,
			"entrypoint":           firstNonEmptyMetadata(task.Metadata, "entrypoint"),
			"workspace_root":       firstNonEmptyMetadata(task.Metadata, "workspace_root"),
			"allowed_capabilities": task.AllowedCapabilities,
		}
		if spec.Metadata != nil {
			for key, value := range spec.Metadata {
				childMetadata[key] = value
			}
		}
		if spec.ToolRequest != nil {
			childMetadata["tool_request"] = spec.ToolRequest
		}
		executionTarget := task.ExecutionTarget
		if spec.ExecutionTarget != "" {
			executionTarget = spec.ExecutionTarget
		}
		var workspacePath *string
		if executionTarget == domain.ExecutionTargetRemote {
			workspacePath = w.childWorkspacePath(task, rootID)
		}
		child, err := w.postgres.CreateTask(ctx, store.CreateTaskParams{
			Description:         description,
			Metadata:            childMetadata,
			AllowedCapabilities: task.AllowedCapabilities,
			Priority:            priority,
			AssignedAgent:       agent,
			ExecutionTarget:     executionTarget,
			Entrypoint:          firstNonEmptyMetadata(task.Metadata, "entrypoint"),
			WorkspacePath:       workspacePath,
			ParentTaskID:        &task.ID,
			RootTaskID:          &rootID,
			TaskKind:            domain.TaskKindPlanStep,
			QueueOnCreate:       false,
		})
		if err != nil {
			return err
		}
		createdIDs = append(createdIDs, child.ID)

		if agent == domain.AgentTypePlanner {
			_, err = w.postgres.CompleteTask(ctx, child.ID, "worker:"+w.workerID, "planner coordination step recorded without direct execution", map[string]any{
				"status":            "success",
				"summary":           "Planner coordination step recorded without direct execution",
				"coordination_only": true,
			})
			if err != nil {
				return err
			}
			continue
		}

		if spec.RequiresApproval {
			if _, err := w.postgres.RequestApproval(ctx, child.ID, "planner_subtask", title, 300, "worker:"+w.workerID, "planner generated approval-gated subtask", map[string]any{"approval_requested": true}); err != nil {
				return err
			}
			continue
		}

		if executionTarget == domain.ExecutionTargetLocal {
			if err := w.postgres.UpdateTaskState(ctx, child.ID, domain.TaskStateQueued, "worker:"+w.workerID, "Queued planner-generated local subtask"); err != nil {
				return err
			}
		} else {
			if err := w.redis.EnqueueTask(ctx, child.ID, agent, priority); err != nil {
				_, _ = w.postgres.FailTask(ctx, child.ID, "worker:"+w.workerID, "queue enqueue failed", err.Error(), map[string]any{"status": "error"})
				return err
			}
			if err := w.postgres.UpdateTaskState(ctx, child.ID, domain.TaskStateQueued, "worker:"+w.workerID, "Queued planner-generated subtask"); err != nil {
				return err
			}
		}
	}

	mergedResults := cloneMapAny(result.Results)
	mergedResults["created_subtask_ids"] = createdIDs
	if err := w.postgres.UpdateTaskResults(ctx, task.ID, mergedResults); err != nil {
		return err
	}
	w.reconcileRootTask(ctx, task.ID)
	return nil
}

func (w *RuntimeWorker) childWorkspacePath(task *domain.TaskResponse, rootID string) *string {
	if task == nil {
		return nil
	}
	if task.ExecutionTarget == domain.ExecutionTargetRemote {
		if task.WorkspacePath != nil && strings.TrimSpace(*task.WorkspacePath) != "" {
			value := filepath.Clean(strings.TrimSpace(*task.WorkspacePath))
			return &value
		}
		base := strings.TrimSpace(w.cfg.WorkspaceRoot)
		if base == "" {
			return nil
		}
		value := filepath.Clean(filepath.Join(base, rootID))
		return &value
	}
	return nil
}

func (w *RuntimeWorker) reconcileRootTask(ctx context.Context, taskID string) {
	task, err := w.postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	rootID := task.ID
	if task.RootTaskID != nil && strings.TrimSpace(*task.RootTaskID) != "" {
		rootID = strings.TrimSpace(*task.RootTaskID)
	}
	if rootID == task.ID && task.ParentTaskID == nil {
		return
	}
	tasks, err := w.postgres.ListByRoot(ctx, rootID)
	if err != nil || len(tasks) == 0 {
		return
	}
	var root *domain.TaskResponse
	children := make([]domain.TaskResponse, 0, len(tasks))
	for _, item := range tasks {
		item := item
		if item.ID == rootID {
			root = &item
			continue
		}
		children = append(children, item)
	}
	if root == nil || len(children) == 0 {
		return
	}
	hasFailed := false
	allCompleted := true
	for _, child := range children {
		switch child.State {
		case domain.TaskStateFailed, domain.TaskStateCancelled:
			hasFailed = true
		case domain.TaskStateCompleted:
		default:
			allCompleted = false
		}
	}
	if hasFailed {
		if !domain.IsTerminalState(root.State) {
			_, _ = w.postgres.FailTask(ctx, root.ID, "worker:"+w.workerID, "One or more subtasks failed", "One or more subtasks failed", root.Results)
		}
		return
	}
	if allCompleted && root.State != domain.TaskStateCompleted {
		_, _ = w.postgres.CompleteTask(ctx, root.ID, "worker:"+w.workerID, "All subtasks completed", root.Results)
	}
}

type plannerSubtask struct {
	Title            string
	Description      string
	AssignedAgent    domain.AgentType
	Priority         domain.Priority
	ExecutionTarget  domain.ExecutionTarget
	RequiresApproval bool
	ToolRequest      map[string]any
	Metadata         map[string]any
}

func parsePlannerSubtasks(raw any) []plannerSubtask {
	var entries []map[string]any
	switch items := raw.(type) {
	case []map[string]any:
		entries = items
	case []any:
		entries = make([]map[string]any, 0, len(items))
		for _, item := range items {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			entries = append(entries, entry)
		}
	default:
		return nil
	}
	out := make([]plannerSubtask, 0, len(entries))
	for _, entry := range entries {
		spec := plannerSubtask{
			Title:            strings.TrimSpace(asStringMap(entry, "title")),
			Description:      strings.TrimSpace(asStringMap(entry, "description")),
			AssignedAgent:    domain.AgentType(strings.TrimSpace(asStringMap(entry, "assigned_agent"))),
			Priority:         domain.Priority(strings.TrimSpace(asStringMap(entry, "priority"))),
			ExecutionTarget:  domain.ExecutionTarget(strings.TrimSpace(asStringMap(entry, "execution_target"))),
			RequiresApproval: asBoolAny(entry["requires_approval"]),
		}
		if toolRequest, ok := entry["tool_request"].(map[string]any); ok {
			spec.ToolRequest = toolRequest
		}
		if metadata, ok := entry["metadata"].(map[string]any); ok {
			spec.Metadata = metadata
		}
		out = append(out, spec)
	}
	return out
}

func asStringMap(input map[string]any, key string) string {
	if input == nil {
		return ""
	}
	value, _ := input[key]
	text, _ := value.(string)
	return text
}

func asBoolMap(input map[string]any, key string) bool {
	if input == nil {
		return false
	}
	return asBoolAny(input[key])
}

func asBoolAny(value any) bool {
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

func cloneMapAny(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func firstNonEmptyMetadata(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if metadata == nil {
			return ""
		}
		if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func derefAgent(agent *domain.AgentType) domain.AgentType {
	if agent == nil {
		return ""
	}
	return *agent
}

func (w *RuntimeWorker) getCurrentTask() *string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.currentTask == nil {
		return nil
	}
	value := *w.currentTask
	return &value
}

func (w *RuntimeWorker) setCurrentTask(taskID *string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if taskID == nil {
		w.currentTask = nil
		return
	}
	value := *taskID
	w.currentTask = &value
}

func (w *RuntimeWorker) releaseTask(ctx context.Context) {
	w.setCurrentTask(nil)
	if err := w.redis.UpdateWorkerCurrentTask(ctx, w.workerID, nil); err != nil {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to clear current task")
	}
	if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, nil, w.startedAt); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to emit heartbeat after releasing task")
	}
}
