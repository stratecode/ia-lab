package worker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/runner"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type ShadowWorker struct {
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

func New(cfg config.Config, postgres *store.PostgresStore, redis *store.RedisStore) *ShadowWorker {
	return &ShadowWorker{
		cfg:       cfg,
		postgres:  postgres,
		redis:     redis,
		runner:    runner.New(),
		workerID:  store.GenerateWorkerID(),
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
}

func (w *ShadowWorker) Start(parent context.Context) error {
	if !w.cfg.WorkerEnabled() {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	if err := w.redis.RegisterWorker(ctx, w.workerID, "planner,coder"); err != nil {
		return err
	}
	if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, nil, w.startedAt); err != nil {
		return err
	}
	go w.run(ctx)
	return nil
}

func (w *ShadowWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
		<-w.done
	}
}

func (w *ShadowWorker) run(ctx context.Context) {
	defer close(w.done)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.redis.DeregisterWorker(cleanupCtx, w.workerID); err != nil {
			log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to deregister shadow worker")
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
				w.tryClaimTask(ctx, domain.AgentTypeCoder)
			}
		}
	}
}

func (w *ShadowWorker) tryClaimTask(ctx context.Context, agentType domain.AgentType) {
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
	if err := w.postgres.UpdateTaskState(ctx, taskID, domain.TaskStateAssigned, "worker:"+w.workerID, "Dequeued for "+string(agentType)); err != nil {
		if !strings.HasPrefix(err.Error(), "invalid_transition:") {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed queued->assigned transition")
		}
		return
	}
	if err := w.postgres.UpdateTaskState(ctx, taskID, domain.TaskStateInProgress, "worker:"+w.workerID, "Execution started in Go shadow worker"); err != nil {
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
	log.Info().Str("worker_id", w.workerID).Str("task_id", taskID).Str("agent_type", string(agentType)).Msg("shadow worker claimed task")
	go w.executeTask(context.Background(), taskID)
}

func (w *ShadowWorker) executeTask(ctx context.Context, taskID string) {
	task, err := w.postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		log.Warn().Err(err).Str("task_id", taskID).Msg("failed to load task for execution")
		w.releaseTask(ctx)
		return
	}

	result := w.runner.Execute(task)
	switch result.Status {
	case "success":
		if _, err := w.postgres.CompleteTask(ctx, taskID, "worker:"+w.workerID, result.Summary, result.Results); err != nil {
			log.Warn().Err(err).Str("task_id", taskID).Msg("failed to complete task")
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
	}
	w.releaseTask(ctx)
}

func (w *ShadowWorker) getCurrentTask() *string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.currentTask == nil {
		return nil
	}
	value := *w.currentTask
	return &value
}

func (w *ShadowWorker) setCurrentTask(taskID *string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if taskID == nil {
		w.currentTask = nil
		return
	}
	value := *taskID
	w.currentTask = &value
}

func (w *ShadowWorker) releaseTask(ctx context.Context) {
	w.setCurrentTask(nil)
	if err := w.redis.UpdateWorkerCurrentTask(ctx, w.workerID, nil); err != nil {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to clear current task")
	}
	if err := w.redis.EmitWorkerHeartbeat(ctx, w.workerID, w.cfg.WorkerHeartbeatInterval, nil, w.startedAt); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn().Err(err).Str("worker_id", w.workerID).Msg("failed to emit heartbeat after releasing task")
	}
}
