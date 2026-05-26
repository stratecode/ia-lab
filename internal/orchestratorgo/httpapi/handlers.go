package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/contextbuilder"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/semantic"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type Server struct {
	Config          config.Config
	Postgres        *store.PostgresStore
	Redis           *store.RedisStore
	Research        *research.Service
	Initiatives     *initiative.Service
	Capabilities    *capabilities.Client
	Semantic        *semantic.Service
	SemanticIndexer *semantic.Indexer
	ContextBuilder  *contextbuilder.Service
	SafeMode        *SafeModeState
	Now             func() time.Time
	Version         string
	OpenAIToolsID   string
}

func (s *Server) Router(auth *Authenticator) http.Handler {
	r := chi.NewRouter()
	r.Use(chiMiddleware)
	r.Use(CorrelationMiddleware)
	if auth != nil {
		r.Use(auth.Middleware)
	}
	r.Get("/health", s.health)
	r.Get("/ready", s.ready)
	r.Handle("/metrics", promhttp.Handler())
	r.Route("/tasks", func(r chi.Router) {
		r.Get("/", s.listTasks)
		r.Get("/{taskID}", s.getTask)
		r.Get("/{taskID}/children", s.listTaskChildren)
		r.Get("/{taskID}/tree", s.getTaskTree)
		r.Post("/", s.createTask)
		r.Patch("/{taskID}", s.updateTask)
		r.Post("/{taskID}/cancel", s.cancelTask)
		r.Post("/{taskID}/archive", s.archiveTask)
		r.Post("/{taskID}/unarchive", s.unarchiveTask)
	})
	r.Route("/approvals", func(r chi.Router) {
		r.Get("/", s.listApprovals)
		r.Get("/{approvalID}", s.getApproval)
		r.Post("/{approvalID}/approve", s.approveApproval)
		r.Post("/{approvalID}/reject", s.rejectApproval)
	})
	r.Route("/bridges", func(r chi.Router) {
		r.Get("/", s.listBridges)
		r.Post("/register", s.registerBridge)
		r.Post("/{bridgeID}/heartbeat", s.heartbeatBridge)
		r.Post("/{bridgeID}/claim-next", s.claimNextBridgeTask)
		r.Post("/{bridgeID}/tasks/{taskID}/result", s.submitBridgeTaskResult)
	})
	r.Get("/workers", s.listWorkers)
	r.Post("/config/safe-mode", s.toggleSafeMode)
	r.Post("/workspaces/cleanup", s.cleanupWorkspaces)
	r.Get("/capabilities", s.listCapabilities)
	r.Route("/semantic", func(r chi.Router) {
		r.Post("/index", s.indexSemanticSource)
		r.Post("/search", s.searchSemanticChunks)
		r.Get("/chunks/{chunkID}", s.getSemanticChunk)
	})
	r.Route("/context", func(r chi.Router) {
		r.Post("/build", s.buildContextPackage)
	})
	r.Route("/initiatives", func(r chi.Router) {
		r.Get("/", s.listInitiatives)
		r.Post("/", s.createInitiative)
		r.Get("/{initiativeID}", s.getInitiative)
		r.Get("/{initiativeID}/history/{phase}", s.getInitiativePhaseHistory)
		r.Get("/{initiativeID}/artifacts", s.getInitiativeArtifacts)
		r.Get("/{initiativeID}/tasks", s.listInitiativeTasks)
		r.Post("/{initiativeID}/advance", s.advanceInitiative)
		r.Post("/{initiativeID}/approve/{phase}", s.approveInitiativePhase)
		r.Post("/{initiativeID}/reject/{phase}", s.rejectInitiativePhase)
		r.Post("/{initiativeID}/tasks/generate", s.generateInitiativeTasks)
		r.Post("/{initiativeID}/tasks/launch", s.launchInitiativeTasks)
		r.Post("/{initiativeID}/tasks/{taskID}/mode", s.updateInitiativeTaskMode)
	})
	r.Post("/tools/web/search", s.webSearch)
	r.Post("/tools/web/fetch", s.webFetch)
	r.Post("/tools/documents/read", s.documentRead)
	r.Post("/tools/images/analyze", s.imageAnalyze)
	r.Get("/tools/invocations/{invocationID}", s.getToolInvocation)
	r.Post("/research/query", s.researchQuery)
	r.Get("/research/runs/{researchRunID}", s.getResearchRun)
	r.Get("/tasks/{taskID}/sources", s.getTaskSources)
	r.Post("/evaluations/reference", s.createReferenceEvaluation)
	r.Post("/evaluations/judge", s.judgeEvaluation)
	r.Get("/evaluations/{evaluationRunID}", s.getEvaluationRun)
	r.Get("/datasets/evaluation-items", s.listEvaluationDatasetItems)
	r.Route("/v1", func(r chi.Router) {
		r.Get("/models", s.listModels)
		r.Post("/chat/completions", s.chatCompletions)
	})
	return r
}

func chiMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext())))
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	now := s.currentTime()
	dbStatus, dbLatency := s.componentCheck(r.Context(), func(ctx context.Context) error {
		return s.Postgres.Ping(ctx)
	})
	redisStatus, redisLatency := s.componentCheck(r.Context(), func(ctx context.Context) error {
		return s.Redis.Ping(ctx)
	})
	workerCount, workerErr := s.Redis.CountWorkers(r.Context())
	workersStatus := domain.ComponentHealth{Status: "degraded", Details: map[string]any{"registered_workers": workerCount, "note": "No workers registered"}}
	if workerErr != nil {
		workersStatus = domain.ComponentHealth{Status: "unhealthy", Details: map[string]any{"error": workerErr.Error()}}
	} else if workerCount > 0 {
		workersStatus = domain.ComponentHealth{Status: "healthy", Details: map[string]any{"registered_workers": workerCount}}
	}
	components := map[string]domain.ComponentHealth{
		"database": withLatency(dbStatus, dbLatency),
		"redis":    withLatency(redisStatus, redisLatency),
		"workers":  workersStatus,
	}
	overall := "healthy"
	for _, component := range components {
		if component.Status == "unhealthy" {
			overall = "unhealthy"
			break
		}
		if component.Status == "degraded" {
			overall = "degraded"
		}
	}
	statusCode := http.StatusOK
	if overall == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, domain.HealthResponse{
		Status:     overall,
		Timestamp:  now.UTC(),
		Version:    s.Version,
		SafeMode:   s.safeModeEnabled(),
		Components: components,
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	_, dbLatency := s.componentCheck(r.Context(), func(ctx context.Context) error { return s.Postgres.Ping(ctx) })
	_ = dbLatency
	dbErr := s.Postgres.Ping(r.Context())
	redisErr := s.Redis.Ping(r.Context())
	ready := dbErr == nil && redisErr == nil
	statusCode := http.StatusOK
	if !ready {
		statusCode = http.StatusServiceUnavailable
	}
	writeJSON(w, statusCode, domain.ReadyResponse{
		Ready:     ready,
		Timestamp: s.currentTime().UTC(),
		Checks: map[string]bool{
			"database": dbErr == nil,
			"redis":    redisErr == nil,
		},
	})
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{
				"id":       s.OpenAIToolsID,
				"object":   "model",
				"created":  s.currentTime().Unix(),
				"owned_by": "orchestrator-go",
			},
		},
	})
}

func (s *Server) listCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"capabilities": []string{"web.search", "web.fetch", "document.read", "image.analyze", "research.query"},
		"total":        5,
	})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	filter := store.TaskListFilter{
		State:           domain.TaskState(r.URL.Query().Get("state")),
		Agent:           domain.AgentType(r.URL.Query().Get("agent")),
		Priority:        domain.Priority(r.URL.Query().Get("priority")),
		ExecutionTarget: domain.ExecutionTarget(r.URL.Query().Get("execution_target")),
		InitiativeID:    r.URL.Query().Get("initiative_id"),
		IncludeArchived: strings.EqualFold(r.URL.Query().Get("archived"), "include"),
		OnlyArchived:    strings.EqualFold(r.URL.Query().Get("archived"), "only"),
		ParentTaskID:    r.URL.Query().Get("parent_task_id"),
		RootTaskID:      r.URL.Query().Get("root_task_id"),
		Cursor:          r.URL.Query().Get("cursor"),
		Limit:           limit,
	}
	if value := r.URL.Query().Get("date_from"); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			filter.DateFrom = &parsed
		}
	}
	if value := r.URL.Query().Get("date_to"); value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			filter.DateTo = &parsed
		}
	}
	items, cursor, err := s.Postgres.ListTasks(r.Context(), filter)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.TaskListResponse{
		Items:    items,
		Cursor:   cursor,
		Total:    len(items),
		PageSize: limit,
	})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var body domain.TaskCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Description = strings.TrimSpace(body.Description)
	if body.Description == "" || len(body.Description) > 10000 {
		writeDetail(w, http.StatusBadRequest, "description must be between 1 and 10000 characters")
		return
	}
	if body.Metadata == nil {
		body.Metadata = map[string]any{}
	}
	if body.Priority == "" {
		body.Priority = domain.PriorityNormal
	}
	if !isSupportedPriority(body.Priority) {
		writeDetail(w, http.StatusBadRequest, "invalid priority")
		return
	}
	if body.ExecutionTarget == "" {
		body.ExecutionTarget = domain.ExecutionTargetRemote
	}
	if body.ExecutionTarget != domain.ExecutionTargetRemote && body.ExecutionTarget != domain.ExecutionTargetLocal {
		writeDetail(w, http.StatusBadRequest, "invalid execution_target")
		return
	}

	agentType := classifyAgent(body.Description, body.AssignedAgent)
	if !isSupportedAgent(agentType) {
		writeDetail(w, http.StatusBadRequest, "Current Go runtime only supports planner, researcher, coder and reviewer")
		return
	}

	workspacePath, err := s.workspacePath(body.ExecutionTarget, body.Metadata)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}

	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey != "" {
		task, err := s.Postgres.GetTaskByIdempotencyKey(r.Context(), idempotencyKey)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if task != nil {
			writeJSON(w, http.StatusCreated, task)
			return
		}
	}

	shouldEnqueueRedis := body.ExecutionTarget != domain.ExecutionTargetLocal
	if shouldEnqueueRedis {
		totalDepth, err := s.Redis.TotalQueueDepth(r.Context())
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if totalDepth >= int64(s.Config.QueueMaxGlobal) {
			w.Header().Set("Retry-After", "30")
			writeDetail(w, http.StatusTooManyRequests, "global queue capacity reached")
			return
		}
		agentDepth, err := s.Redis.QueueDepth(r.Context(), agentType)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if agentDepth >= int64(s.Config.QueueMaxPerAgent) {
			w.Header().Set("Retry-After", "30")
			writeDetail(w, http.StatusTooManyRequests, "agent queue capacity reached")
			return
		}
	}

	var idempotencyPtr *string
	if idempotencyKey != "" {
		idempotencyPtr = &idempotencyKey
	}
	task, err := s.Postgres.CreateTask(r.Context(), store.CreateTaskParams{
		Description:         body.Description,
		Metadata:            body.Metadata,
		AllowedCapabilities: body.AllowedCapabilities,
		Priority:            body.Priority,
		AssignedAgent:       agentType,
		PlannedAgent:        body.PlannedAgent,
		ExecutionTarget:     body.ExecutionTarget,
		IdempotencyKey:      idempotencyPtr,
		Entrypoint:          "api",
		WorkspacePath:       workspacePath,
		InitiativeID:        body.InitiativeID,
		QueueOnCreate:       true,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && idempotencyKey != "" {
			existing, lookupErr := s.Postgres.GetTaskByIdempotencyKey(r.Context(), idempotencyKey)
			if lookupErr == nil && existing != nil {
				writeJSON(w, http.StatusCreated, existing)
				return
			}
		}
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}

	if shouldEnqueueRedis {
		if err := s.Redis.EnqueueTask(r.Context(), task.ID, agentType, body.Priority); err != nil {
			_ = s.Postgres.CancelTask(r.Context(), task.ID, "api:create", "queue enqueue failed: "+err.Error())
			writeDetail(w, http.StatusServiceUnavailable, "failed to enqueue task")
			return
		}
	}
	updatedTask, err := s.Postgres.GetTask(r.Context(), task.ID)
	if err != nil || updatedTask == nil {
		writeJSON(w, http.StatusCreated, task)
		return
	}
	writeJSON(w, http.StatusCreated, updatedTask)
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.Postgres.GetTask(r.Context(), chi.URLParam(r, "taskID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}

	var body domain.TaskUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.State == "" {
		writeDetail(w, http.StatusBadRequest, "state is required")
		return
	}
	if !isRecognizedTaskState(body.State) {
		writeDetail(w, http.StatusBadRequest, "invalid state")
		return
	}
	currentState := task.State
	if !domain.IsValidTransition(currentState, body.State) {
		writeTaskStateConflict(w, currentState, body.State, taskID)
		return
	}
	reason := ""
	if body.Reason != nil {
		reason = strings.TrimSpace(*body.Reason)
	}
	if err := s.Postgres.UpdateTaskState(r.Context(), taskID, body.State, "api", reason); err != nil {
		if strings.HasPrefix(err.Error(), "invalid_transition:") {
			writeTaskStateConflict(w, currentState, body.State, taskID)
			return
		}
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedTask, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updatedTask == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	if domain.IsTerminalState(updatedTask.State) {
		s.indexTask(r.Context(), updatedTask.ID)
	}
	s.reconcileRootTask(r.Context(), updatedTask.ID)
	writeJSON(w, http.StatusOK, updatedTask)
}

func (s *Server) listTaskChildren(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListChildren(r.Context(), chi.URLParam(r, "taskID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	if !isCancellableState(task.State) {
		writeTaskStateConflict(w, task.State, domain.TaskStateCancelled, taskID)
		return
	}
	if err := s.Postgres.CancelTask(r.Context(), taskID, "api", "Cancelled via API"); err != nil {
		if strings.HasPrefix(err.Error(), "invalid_transition:") {
			writeTaskStateConflict(w, task.State, domain.TaskStateCancelled, taskID)
			return
		}
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedTask, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updatedTask == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, updatedTask)
}

func (s *Server) archiveTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	if !domain.IsTerminalState(task.State) {
		writeDetail(w, http.StatusConflict, "only terminal tasks can be archived")
		return
	}
	if err := s.Postgres.ArchiveTask(r.Context(), taskID); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedTask, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updatedTask)
}

func (s *Server) unarchiveTask(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	task, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	if err := s.Postgres.UnarchiveTask(r.Context(), taskID); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedTask, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updatedTask)
}

func (s *Server) getTaskTree(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	root, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if root == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}
	rootID := root.ID
	if root.RootTaskID != nil && *root.RootTaskID != "" {
		rootID = *root.RootTaskID
	}
	items, err := s.Postgres.ListByRoot(r.Context(), rootID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodes := map[string]*domain.TaskTreeResponse{}
	for _, item := range items {
		copyItem := item
		nodes[item.ID] = &domain.TaskTreeResponse{TaskResponse: copyItem}
	}
	for _, node := range nodes {
		if node.ParentTaskID == nil || *node.ParentTaskID == "" {
			continue
		}
		parent := nodes[*node.ParentTaskID]
		if parent != nil {
			parent.Children = append(parent.Children, *node)
		}
	}
	target := nodes[taskID]
	if target == nil {
		target = nodes[rootID]
	}
	if target == nil {
		writeDetail(w, http.StatusNotFound, "task tree not found")
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListApprovals(r.Context(), store.ApprovalListFilter{
		Status: domain.ApprovalStatus(r.URL.Query().Get("status_filter")),
		Limit:  parseIntDefault(r.URL.Query().Get("limit"), 50),
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.ApprovalListResponse{Items: items, Total: len(items)})
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	item, err := s.Postgres.GetApproval(r.Context(), chi.URLParam(r, "approvalID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "approval not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) approveApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, true)
}

func (s *Server) rejectApproval(w http.ResponseWriter, r *http.Request) {
	s.resolveApproval(w, r, false)
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, approve bool) {
	approvalID := chi.URLParam(r, "approvalID")
	var body domain.ApprovalResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Operator = strings.TrimSpace(body.Operator)
	if body.Operator == "" || len(body.Operator) > 128 {
		writeDetail(w, http.StatusBadRequest, "operator must be between 1 and 128 characters")
		return
	}

	approval, task, err := s.Postgres.ResolveApproval(r.Context(), approvalID, approve, body.Operator)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrApprovalNotFound):
			writeDetail(w, http.StatusNotFound, "approval not found")
			return
		case errors.Is(err, store.ErrApprovalAlreadyResolved):
			writeDetail(w, http.StatusConflict, "approval already resolved")
			return
		case strings.HasPrefix(err.Error(), "invalid_transition:"):
			if task != nil {
				targetState := domain.TaskStateCancelled
				if approve {
					targetState = domain.TaskStateQueued
				}
				writeTaskStateConflict(w, task.State, targetState, task.ID)
				return
			}
		}
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}

	if approve && task != nil && task.AssignedAgent != nil && task.ExecutionTarget != domain.ExecutionTargetLocal {
		if err := s.Redis.EnqueueTask(r.Context(), task.ID, *task.AssignedAgent, task.Priority); err != nil {
			writeDetail(w, http.StatusServiceUnavailable, "approval resolved but failed to enqueue task")
			return
		}
	}
	if task != nil {
		s.reconcileRootTask(r.Context(), task.ID)
	}
	writeJSON(w, http.StatusOK, approval)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.Redis.ListWorkers(r.Context())
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.WorkerListResponse{Workers: workers, Total: len(workers)})
}

type safeModeRequest struct {
	Enabled *bool `json:"enabled"`
}

type safeModeResponse struct {
	SafeModeEnabled bool   `json:"safe_mode_enabled"`
	Message         string `json:"message"`
}

func (s *Server) toggleSafeMode(w http.ResponseWriter, r *http.Request) {
	var body safeModeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Enabled == nil {
		writeDetail(w, http.StatusBadRequest, "enabled is required")
		return
	}
	previous := s.SafeMode.SetEnabled(*body.Enabled)
	action := "enabled"
	if !*body.Enabled {
		action = "disabled"
	}
	writeJSON(w, http.StatusOK, safeModeResponse{
		SafeModeEnabled: *body.Enabled,
		Message:         fmt.Sprintf("Safe mode %s (was %s)", action, ternaryBool(previous, "enabled", "disabled")),
	})
}

type workspaceCleanupResponse struct {
	CleanedCount   int      `json:"cleaned_count"`
	Errors         []string `json:"errors"`
	RetentionHours int      `json:"retention_hours"`
	Message        string   `json:"message"`
}

func (s *Server) cleanupWorkspaces(w http.ResponseWriter, r *http.Request) {
	cutoff := s.currentTime().UTC().Add(-time.Duration(s.Config.WorkspaceRetentionHours) * time.Hour)
	tasks, err := s.Postgres.ListWorkspaceCleanupCandidates(r.Context(), cutoff, 100)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}

	root := filepath.Clean(strings.TrimSpace(s.Config.WorkspaceRoot))
	cleanedCount := 0
	errorsList := []string{}

	for _, task := range tasks {
		if task.WorkspacePath == nil || strings.TrimSpace(*task.WorkspacePath) == "" {
			continue
		}
		workspacePath := filepath.Clean(strings.TrimSpace(*task.WorkspacePath))
		if root != "" && workspacePath != root && !strings.HasPrefix(workspacePath, root+string(os.PathSeparator)) {
			errorsList = append(errorsList, fmt.Sprintf("%s: outside workspace root", workspacePath))
			continue
		}
		if err := os.RemoveAll(workspacePath); err != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s: %v", workspacePath, err))
			continue
		}
		if err := s.Postgres.ClearWorkspacePath(r.Context(), task.ID); err != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s: failed to clear DB reference: %v", workspacePath, err))
			continue
		}
		cleanedCount++
	}

	writeJSON(w, http.StatusOK, workspaceCleanupResponse{
		CleanedCount:   cleanedCount,
		Errors:         errorsList,
		RetentionHours: s.Config.WorkspaceRetentionHours,
		Message:        fmt.Sprintf("Cleaned %d workspace(s) older than %dh", cleanedCount, s.Config.WorkspaceRetentionHours),
	})
}

func (s *Server) webSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	if body.Query == "" {
		writeDetail(w, http.StatusBadRequest, "query is required")
		return
	}
	results, err := s.Research.WebSearch(r.Context(), body.Query)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	sourceRefs := make([]domain.SourceRef, 0, len(results))
	for _, item := range results {
		sourceRefs = append(sourceRefs, domain.SourceRef{Title: item.Title, URI: item.URL, Kind: "search"})
	}
	invocationID, err := s.Postgres.CreateToolInvocation(r.Context(), store.CreateToolInvocationParams{
		Entrypoint:    "api",
		Capability:    "web.search",
		InputPayload:  map[string]any{"query": body.Query},
		OutputPayload: map[string]any{"results": results},
		Status:        "success",
		DurationMS:    0,
		SourceRefs:    sourceRefs,
		ArtifactIDs:   []string{},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "total": len(results), "tool_invocation_id": invocationID})
}

func (s *Server) webFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if body.URL == "" {
		writeDetail(w, http.StatusBadRequest, "url is required")
		return
	}
	source, content, err := s.Research.WebFetch(r.Context(), body.URL)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	sourceRef := domain.SourceRef{Title: source.Title, URI: source.URI, Kind: source.Kind}
	invocationID, err := s.Postgres.CreateToolInvocation(r.Context(), store.CreateToolInvocationParams{
		Entrypoint:    "api",
		Capability:    "web.fetch",
		InputPayload:  map[string]any{"url": body.URL},
		OutputPayload: map[string]any{"summary": truncateForAPI(content, 700), "content_text": truncateForAPI(content, 4000)},
		Status:        "success",
		DurationMS:    0,
		SourceRefs:    []domain.SourceRef{sourceRef},
		ArtifactIDs:   []string{},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	artifactID, err := s.Postgres.CreateArtifact(r.Context(), store.CreateArtifactParams{
		InvocationID: &invocationID,
		ArtifactType: "web_page",
		Title:        &source.Title,
		URI:          &source.URI,
		MediaType:    stringPtr("text/html"),
		ContentText:  stringPtr(truncateForAPI(content, 4000)),
		Metadata:     map[string]any{"kind": source.Kind},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Postgres.UpdateToolInvocationArtifactIDs(r.Context(), invocationID, []string{artifactID}); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source":             source,
		"summary":            truncateForAPI(content, 700),
		"content":            truncateForAPI(content, 4000),
		"tool_invocation_id": invocationID,
		"artifact_ids":       []string{artifactID},
	})
}

func (s *Server) documentRead(w http.ResponseWriter, r *http.Request) {
	if s.Capabilities == nil {
		writeDetail(w, http.StatusServiceUnavailable, "document sidecar is not configured")
		return
	}
	var body struct {
		Location string  `json:"location"`
		TaskID   *string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Location = strings.TrimSpace(body.Location)
	if body.Location == "" {
		writeDetail(w, http.StatusBadRequest, "location is required")
		return
	}
	result, err := s.Capabilities.ReadDocument(r.Context(), body.Location)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	response, err := s.persistCapabilityExecution(r.Context(), "api", body.TaskID, "document.read", map[string]any{"location": body.Location}, result)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) imageAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.Capabilities == nil {
		writeDetail(w, http.StatusServiceUnavailable, "image sidecar is not configured")
		return
	}
	var body struct {
		Location string  `json:"location"`
		TaskID   *string `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Location = strings.TrimSpace(body.Location)
	if body.Location == "" {
		writeDetail(w, http.StatusBadRequest, "location is required")
		return
	}
	result, err := s.Capabilities.AnalyzeImage(r.Context(), body.Location)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	response, err := s.persistCapabilityExecution(r.Context(), "api", body.TaskID, "image.analyze", map[string]any{"location": body.Location}, result)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getToolInvocation(w http.ResponseWriter, r *http.Request) {
	invocationID := chi.URLParam(r, "invocationID")
	record, err := s.Postgres.GetToolInvocation(r.Context(), invocationID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if record == nil {
		writeDetail(w, http.StatusNotFound, "invocation not found")
		return
	}
	artifacts, err := s.Postgres.ListArtifactsByInvocation(r.Context(), invocationID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"invocation": record,
		"artifacts":  artifacts,
	})
}

func (s *Server) getTaskSources(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	items, err := s.Postgres.ListArtifactsByTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (s *Server) researchQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query                    string   `json:"query"`
		TaskID                   *string  `json:"task_id"`
		AllowedCapabilities      []string `json:"allowed_capabilities"`
		EvaluateAgainstReference bool     `json:"evaluate_against_reference"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	if body.Query == "" {
		writeDetail(w, http.StatusBadRequest, "query is required")
		return
	}
	result, err := s.Research.Query(r.Context(), body.Query)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	sourceArtifactIDs, invocationIDs, err := s.persistResearchArtifacts(r.Context(), "api", body.TaskID, body.Query, result)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	researchRun, err := s.Postgres.CreateResearchRun(r.Context(), store.CreateResearchRunParams{
		TaskID:               body.TaskID,
		Entrypoint:           "api",
		Query:                body.Query,
		IntentType:           string(result.Intent),
		SelectedCapabilities: selectedCapabilitiesForIntent(result.Intent, body.AllowedCapabilities),
		FinalAnswer:          result.Answer,
		Confidence:           ptrFloat64(result.Confidence),
		SourceArtifactIDs:    sourceArtifactIDs,
		ToolInvocationIDs:    invocationIDs,
		Metadata: map[string]any{
			"source_count": len(result.Sources),
			"search_hits":  len(result.SearchResults),
		},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	response := map[string]any{
		"research_run":        researchRun,
		"answer":              result.Answer,
		"confidence":          result.Confidence,
		"sources":             result.Sources,
		"intent":              result.Intent,
		"search_hits":         len(result.SearchResults),
		"tool_invocation_ids": invocationIDs,
	}
	if body.EvaluateAgainstReference {
		evaluation, err := s.runInlineEvaluation(r.Context(), researchRun)
		if err != nil {
			writeDetail(w, http.StatusBadGateway, err.Error())
			return
		}
		response["evaluation"] = evaluation
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getResearchRun(w http.ResponseWriter, r *http.Request) {
	item, err := s.Postgres.GetResearchRun(r.Context(), chi.URLParam(r, "researchRunID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "research run not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createReferenceEvaluation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ResearchRunID      string             `json:"research_run_id"`
		Query              string             `json:"query"`
		OrchestratorAnswer string             `json:"orchestrator_answer"`
		Sources            []domain.SourceRef `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.ResearchRunID = strings.TrimSpace(body.ResearchRunID)
	if strings.TrimSpace(s.Config.OpenAIReferenceAPIKey) == "" {
		writeDetail(w, http.StatusBadRequest, "OpenAI reference API key is not configured")
		return
	}
	if body.ResearchRunID == "" {
		body.Query = strings.TrimSpace(body.Query)
		if body.Query == "" {
			writeDetail(w, http.StatusBadRequest, "research_run_id or query is required")
			return
		}
		referenceAnswer, err := s.callOpenAIChat(
			r.Context(),
			s.Config.OpenAIReferenceModel,
			"Eres un asistente de investigación riguroso. Responde a la pregunta del usuario con claridad. Si faltan datos, dilo.",
			body.Query,
		)
		if err != nil {
			writeDetail(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                "direct",
			"reference_provider":  "openai",
			"reference_model":     s.Config.OpenAIReferenceModel,
			"reference_answer":    referenceAnswer,
			"query":               body.Query,
			"orchestrator_answer": strings.TrimSpace(body.OrchestratorAnswer),
			"sources":             body.Sources,
		})
		return
	}
	researchRun, err := s.Postgres.GetResearchRun(r.Context(), body.ResearchRunID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if researchRun == nil {
		writeDetail(w, http.StatusNotFound, "research run not found")
		return
	}
	referenceAnswer, err := s.callOpenAIChat(
		r.Context(),
		s.Config.OpenAIReferenceModel,
		"Eres un asistente de investigación riguroso. Responde a la pregunta del usuario con claridad. Si faltan datos, dilo.",
		researchRun.Query,
	)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	record, err := s.Postgres.CreateEvaluationRun(r.Context(), store.CreateEvaluationRunParams{
		ResearchRunID:     researchRun.ID,
		ReferenceProvider: "openai",
		ReferenceModel:    stringPtr(s.Config.OpenAIReferenceModel),
		ReferenceAnswer:   &referenceAnswer,
		JudgeModel:        stringPtr(s.Config.OpenAIJudgeModel),
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) judgeEvaluation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EvaluationRunID    string             `json:"evaluation_run_id"`
		Query              string             `json:"query"`
		OrchestratorAnswer string             `json:"orchestrator_answer"`
		ReferenceAnswer    string             `json:"reference_answer"`
		Sources            []domain.SourceRef `json:"sources"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.EvaluationRunID = strings.TrimSpace(body.EvaluationRunID)
	if strings.TrimSpace(s.Config.OpenAIReferenceAPIKey) == "" {
		writeDetail(w, http.StatusBadRequest, "OpenAI reference API key is not configured")
		return
	}
	if body.EvaluationRunID == "" {
		body.Query = strings.TrimSpace(body.Query)
		body.OrchestratorAnswer = strings.TrimSpace(body.OrchestratorAnswer)
		body.ReferenceAnswer = strings.TrimSpace(body.ReferenceAnswer)
		if body.Query == "" || body.OrchestratorAnswer == "" || body.ReferenceAnswer == "" {
			writeDetail(w, http.StatusBadRequest, "evaluation_run_id or direct query/orchestrator_answer/reference_answer is required")
			return
		}
		verdict, err := s.callOpenAIJudge(r.Context(), body.Query, body.OrchestratorAnswer, body.ReferenceAnswer, body.Sources)
		if err != nil {
			writeDetail(w, http.StatusBadGateway, err.Error())
			return
		}
		scores := map[string]any{
			"accuracy_score":           verdict.AccuracyScore,
			"coverage_score":           verdict.CoverageScore,
			"source_use_score":         verdict.SourceUseScore,
			"usefulness_score":         verdict.UsefulnessScore,
			"hallucination_risk_score": verdict.HallucinationRiskScore,
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":                "direct",
			"judge_model":         s.Config.OpenAIJudgeModel,
			"judge_verdict":       verdict.Raw,
			"judge_scores":        scores,
			"winner":              verdict.Winner,
			"query":               body.Query,
			"orchestrator_answer": body.OrchestratorAnswer,
			"reference_answer":    body.ReferenceAnswer,
			"sources":             body.Sources,
		})
		return
	}
	evaluationRun, err := s.Postgres.GetEvaluationRun(r.Context(), body.EvaluationRunID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if evaluationRun == nil {
		writeDetail(w, http.StatusNotFound, "evaluation run not found")
		return
	}
	researchRun, err := s.Postgres.GetResearchRun(r.Context(), evaluationRun.ResearchRunID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if researchRun == nil {
		writeDetail(w, http.StatusNotFound, "research run not found")
		return
	}
	if evaluationRun.ReferenceAnswer == nil || strings.TrimSpace(*evaluationRun.ReferenceAnswer) == "" {
		writeDetail(w, http.StatusBadRequest, "evaluation run has no reference answer")
		return
	}
	sources, err := s.sourcesForResearchRun(r.Context(), researchRun)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	verdict, err := s.callOpenAIJudge(r.Context(), researchRun.Query, researchRun.FinalAnswer, *evaluationRun.ReferenceAnswer, sources)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	scores := map[string]any{
		"accuracy_score":           verdict.AccuracyScore,
		"coverage_score":           verdict.CoverageScore,
		"source_use_score":         verdict.SourceUseScore,
		"usefulness_score":         verdict.UsefulnessScore,
		"hallucination_risk_score": verdict.HallucinationRiskScore,
	}
	updatedRun, err := s.Postgres.UpdateEvaluationRunJudgement(
		r.Context(),
		evaluationRun.ID,
		stringPtr(s.Config.OpenAIJudgeModel),
		verdict.Raw,
		scores,
		stringPtr(verdict.Winner),
	)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, err = s.Postgres.CreateEvaluationDatasetItem(r.Context(), store.CreateEvaluationDatasetItemParams{
		ResearchRunID:      researchRun.ID,
		EvaluationRunID:    &updatedRun.ID,
		Query:              researchRun.Query,
		OrchestratorAnswer: researchRun.FinalAnswer,
		ReferenceAnswer:    *evaluationRun.ReferenceAnswer,
		Sources:            sources,
		Scores:             scores,
		Winner:             stringPtr(verdict.Winner),
		Metadata:           map[string]any{"judge_reasoning": verdict.Reasoning},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updatedRun)
}

func (s *Server) getEvaluationRun(w http.ResponseWriter, r *http.Request) {
	item, err := s.Postgres.GetEvaluationRun(r.Context(), chi.URLParam(r, "evaluationRunID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "evaluation run not found")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listEvaluationDatasetItems(w http.ResponseWriter, r *http.Request) {
	limit := parseIntDefault(r.URL.Query().Get("limit"), 100)
	items, err := s.Postgres.ListEvaluationDatasetItems(r.Context(), limit)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func (s *Server) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Model != s.OpenAIToolsID {
		writeDetail(w, http.StatusBadRequest, fmt.Sprintf("Unsupported model: %s", body.Model))
		return
	}
	prompt := extractPrompt(body.Messages)
	if prompt == "" {
		writeJSON(w, http.StatusOK, chatResponse(body.Model, "No se detectó una consulta utilizable."))
		return
	}
	if !research.ShouldUseResearch(prompt) {
		answer, err := s.callUtilityChat(r.Context(), prompt)
		if err == nil && strings.TrimSpace(answer) != "" {
			writeJSON(w, http.StatusOK, chatResponse(body.Model, strings.TrimSpace(answer)))
			return
		}
	}
	result, err := s.Research.Query(r.Context(), prompt)
	if err != nil {
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	sourceArtifactIDs, invocationIDs, err := s.persistResearchArtifacts(r.Context(), "open_webui", nil, prompt, result)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	researchRun, err := s.Postgres.CreateResearchRun(r.Context(), store.CreateResearchRunParams{
		Entrypoint:           "open_webui",
		Query:                prompt,
		IntentType:           string(result.Intent),
		SelectedCapabilities: selectedCapabilitiesForIntent(result.Intent, nil),
		FinalAnswer:          result.Answer,
		Confidence:           ptrFloat64(result.Confidence),
		SourceArtifactIDs:    sourceArtifactIDs,
		ToolInvocationIDs:    invocationIDs,
		Metadata: map[string]any{
			"source_count": len(result.Sources),
			"search_hits":  len(result.SearchResults),
		},
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	content := result.Answer
	if result.Confidence > 0 {
		content += fmt.Sprintf("\n\nConfidence: %.2f", result.Confidence)
	}
	if len(result.Sources) > 0 {
		content += "\n\nSources:\n"
		for _, src := range result.Sources[:minInt(5, len(result.Sources))] {
			content += fmt.Sprintf("- %s: %s\n", firstNonEmptyString(src.Title, src.URI), src.URI)
		}
	}
	content += fmt.Sprintf("\n\nResearch Run: %s", researchRun.ID)
	writeJSON(w, http.StatusOK, chatResponse(body.Model, strings.TrimSpace(content)))
}

func (s *Server) componentCheck(ctx context.Context, fn func(context.Context) error) (domain.ComponentHealth, *float64) {
	start := time.Now()
	err := fn(ctx)
	latency := roundFloat(float64(time.Since(start).Milliseconds()) / 1.0)
	if err != nil {
		return domain.ComponentHealth{Status: "unhealthy", Details: map[string]any{"error": err.Error()}}, &latency
	}
	return domain.ComponentHealth{Status: "healthy", Details: map[string]any{}}, &latency
}

func parseIntDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func ternaryBool(value bool, whenTrue, whenFalse string) string {
	if value {
		return whenTrue
	}
	return whenFalse
}

func roundFloat(value float64) float64 {
	return float64(int(value*100)) / 100
}

func truncateForAPI(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "..."
}

func extractPrompt(messages []struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != "user" {
			continue
		}
		switch content := message.Content.(type) {
		case string:
			return strings.TrimSpace(content)
		case []any:
			parts := make([]string, 0, len(content))
			for _, item := range content {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if entry["type"] == "text" {
					parts = append(parts, strings.TrimSpace(fmt.Sprint(entry["text"])))
				}
			}
			return strings.TrimSpace(strings.Join(parts, " "))
		}
	}
	return ""
}

func chatResponse(model, content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-" + fmt.Sprint(time.Now().UnixNano()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func selectedCapabilitiesForIntent(intent research.Intent, requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	switch intent {
	case research.IntentURLSummary:
		return []string{"web.fetch"}
	case research.IntentSearchAnswer:
		return []string{"web.search", "web.fetch"}
	case research.IntentDocumentQA:
		return []string{"document.read"}
	case research.IntentImageQA:
		return []string{"image.analyze"}
	default:
		return []string{}
	}
}

type judgeVerdict struct {
	AccuracyScore          float64        `json:"accuracy_score"`
	CoverageScore          float64        `json:"coverage_score"`
	SourceUseScore         float64        `json:"source_use_score"`
	UsefulnessScore        float64        `json:"usefulness_score"`
	HallucinationRiskScore float64        `json:"hallucination_risk_score"`
	Winner                 string         `json:"winner"`
	Reasoning              string         `json:"reasoning"`
	Raw                    map[string]any `json:"-"`
}

func (s *Server) sourcesForResearchRun(ctx context.Context, researchRun *domain.ResearchRunResponse) ([]domain.SourceRef, error) {
	artifacts, err := s.Postgres.ListArtifactsByIDs(ctx, researchRun.SourceArtifactIDs)
	if err != nil {
		return nil, err
	}
	sources := make([]domain.SourceRef, 0, len(artifacts))
	for _, artifact := range artifacts {
		sources = append(sources, domain.SourceRef{
			Title: firstNonEmptyString(stringValue(artifact.Title), stringValue(artifact.URI), artifact.ID),
			URI:   stringValue(artifact.URI),
			Kind:  artifact.ArtifactType,
		})
	}
	return sources, nil
}

func (s *Server) runInlineEvaluation(ctx context.Context, researchRun *domain.ResearchRunResponse) (*domain.EvaluationRunResponse, error) {
	if strings.TrimSpace(s.Config.OpenAIReferenceAPIKey) == "" {
		return nil, fmt.Errorf("OpenAI reference API key is not configured")
	}
	referenceAnswer, err := s.callOpenAIChat(
		ctx,
		s.Config.OpenAIReferenceModel,
		"Eres un asistente de investigación riguroso. Responde a la pregunta del usuario con claridad. Si faltan datos, dilo.",
		researchRun.Query,
	)
	if err != nil {
		return nil, err
	}
	evaluationRun, err := s.Postgres.CreateEvaluationRun(ctx, store.CreateEvaluationRunParams{
		ResearchRunID:     researchRun.ID,
		ReferenceProvider: "openai",
		ReferenceModel:    stringPtr(s.Config.OpenAIReferenceModel),
		ReferenceAnswer:   &referenceAnswer,
		JudgeModel:        stringPtr(s.Config.OpenAIJudgeModel),
	})
	if err != nil {
		return nil, err
	}
	sources, err := s.sourcesForResearchRun(ctx, researchRun)
	if err != nil {
		return nil, err
	}
	verdict, err := s.callOpenAIJudge(ctx, researchRun.Query, researchRun.FinalAnswer, referenceAnswer, sources)
	if err != nil {
		return nil, err
	}
	scores := map[string]any{
		"accuracy_score":           verdict.AccuracyScore,
		"coverage_score":           verdict.CoverageScore,
		"source_use_score":         verdict.SourceUseScore,
		"usefulness_score":         verdict.UsefulnessScore,
		"hallucination_risk_score": verdict.HallucinationRiskScore,
	}
	evaluationRun, err = s.Postgres.UpdateEvaluationRunJudgement(
		ctx,
		evaluationRun.ID,
		stringPtr(s.Config.OpenAIJudgeModel),
		verdict.Raw,
		scores,
		stringPtr(verdict.Winner),
	)
	if err != nil {
		return nil, err
	}
	_, err = s.Postgres.CreateEvaluationDatasetItem(ctx, store.CreateEvaluationDatasetItemParams{
		ResearchRunID:      researchRun.ID,
		EvaluationRunID:    &evaluationRun.ID,
		Query:              researchRun.Query,
		OrchestratorAnswer: researchRun.FinalAnswer,
		ReferenceAnswer:    referenceAnswer,
		Sources:            sources,
		Scores:             scores,
		Winner:             stringPtr(verdict.Winner),
		Metadata:           map[string]any{"judge_reasoning": verdict.Reasoning},
	})
	if err != nil {
		return nil, err
	}
	return evaluationRun, nil
}

func (s *Server) callUtilityChat(ctx context.Context, prompt string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(s.Config.LlamaUtilityBaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("utility base URL is not configured")
	}
	body := map[string]any{
		"model": "utility",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "Responde de forma directa y útil usando conocimiento general estable. No uses ni menciones búsqueda web salvo que el usuario pida explícitamente verificación o fuentes. Si el dato puede haber cambiado recientemente, dilo en una frase breve. No inventes términos ni detalles técnicos; usa terminología estándar y, si no estás seguro de un matiz, admítelo con claridad.",
			},
			{
				"role":    "user",
				"content": "[RESEARCH_MODE]\n" + prompt,
			},
		},
		"temperature": 0.2,
		"max_tokens":  900,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: time.Duration(s.Config.LlamaTimeoutSeconds) * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(s.Config.LlamaUtilityAPIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("utility request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &completion); err != nil {
		return "", err
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("utility returned no choices")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func (s *Server) callOpenAIChat(ctx context.Context, model, systemPrompt, userPrompt string) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	responseBody, err := s.callOpenAIAPI(ctx, body)
	if err != nil {
		return "", err
	}
	var payload struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return "", err
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("openai response returned no choices")
	}
	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}

func (s *Server) callOpenAIJudge(ctx context.Context, query, orchestratorAnswer, referenceAnswer string, sources []domain.SourceRef) (*judgeVerdict, error) {
	var sourceLines []string
	for _, src := range sources {
		sourceLines = append(sourceLines, fmt.Sprintf("- %s | %s | %s", firstNonEmptyString(src.Title, src.URI), src.URI, src.Kind))
	}
	userPrompt := fmt.Sprintf(
		"Pregunta original:\n%s\n\nRespuesta del orquestador:\n%s\n\nRespuesta de referencia:\n%s\n\nFuentes del orquestador:\n%s",
		query,
		orchestratorAnswer,
		referenceAnswer,
		strings.Join(sourceLines, "\n"),
	)
	content, err := s.callOpenAIChat(
		ctx,
		s.Config.OpenAIJudgeModel,
		`Eres un juez técnico y severo. Evalúa la RESPUESTA DEL ORQUESTADOR contra la pregunta original y el contrato de éxito. Usa la respuesta de referencia solo como punto de comparación, no como verdad absoluta.

Reglas obligatorias:
- Devuelve SOLO JSON válido.
- "winner" debe ser exactamente uno de: "orchestrator", "reference", "tie".
- Todas las puntuaciones van de 0.0 a 1.0.
- Reserva 0.0 para ausencia casi total de evidencia útil o incumplimiento claro.
- Si la respuesta del orquestador aporta evidencia concreta verificable de ejecución, aprobaciones, revisión o memoria relevante, no devuelvas todas las puntuaciones a 0.0.
- Penaliza respuestas vagas, genéricas o que no conecten con el contrato de éxito.
- "hallucination_risk_score" es 0.0 cuando el riesgo es bajo y 1.0 cuando el riesgo es alto.

Rubrica mínima:
- accuracy_score: ¿describe correctamente el resultado observado?
- coverage_score: ¿cubre los requisitos importantes del contrato?
- source_use_score: ¿usa bien la evidencia y memoria disponible?
- usefulness_score: ¿sirve para decidir si el caso pasó o falló?
- hallucination_risk_score: ¿hay riesgo de afirmaciones no sustentadas?

Forma exacta:
{"accuracy_score":0.0,"coverage_score":0.0,"source_use_score":0.0,"usefulness_score":0.0,"hallucination_risk_score":0.0,"winner":"tie","reasoning":"..."}`,
		userPrompt,
	)
	if err != nil {
		return nil, err
	}
	verdict, err := parseJudgeVerdict(content)
	if err != nil {
		return nil, err
	}
	return verdict, nil
}

func (s *Server) callOpenAIAPI(ctx context.Context, body map[string]any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: time.Duration(s.Config.OpenAITimeoutSeconds) * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.Config.OpenAIBaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Config.OpenAIReferenceAPIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai api failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func parseJudgeVerdict(content string) (*judgeVerdict, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var raw map[string]any
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil, err
	}
	verdict := &judgeVerdict{
		AccuracyScore:          floatValue(raw["accuracy_score"]),
		CoverageScore:          floatValue(raw["coverage_score"]),
		SourceUseScore:         floatValue(raw["source_use_score"]),
		UsefulnessScore:        floatValue(raw["usefulness_score"]),
		HallucinationRiskScore: floatValue(raw["hallucination_risk_score"]),
		Winner:                 strings.TrimSpace(asString(raw["winner"])),
		Reasoning:              strings.TrimSpace(asString(raw["reasoning"])),
		Raw:                    raw,
	}
	if verdict.Winner == "" {
		verdict.Winner = "tie"
	}
	if verdict.Winner != "orchestrator" && verdict.Winner != "reference" && verdict.Winner != "tie" {
		verdict.Winner = "tie"
	}
	return verdict, nil
}

func ptrFloat64(value float64) *float64 {
	return &value
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}

func (s *Server) persistResearchArtifacts(ctx context.Context, entrypoint string, taskID *string, query string, result research.Result) ([]string, []string, error) {
	sourceRefs := make([]domain.SourceRef, 0, len(result.Sources))
	for _, src := range result.Sources {
		sourceRefs = append(sourceRefs, domain.SourceRef{Title: src.Title, URI: src.URI, Kind: src.Kind})
	}
	invocationID, err := s.Postgres.CreateToolInvocation(ctx, store.CreateToolInvocationParams{
		TaskID:        taskID,
		Entrypoint:    entrypoint,
		Capability:    "research.query",
		InputPayload:  map[string]any{"query": query},
		OutputPayload: map[string]any{"answer": result.Answer, "confidence": result.Confidence, "intent": result.Intent},
		Status:        "success",
		DurationMS:    0,
		SourceRefs:    sourceRefs,
		ArtifactIDs:   []string{},
	})
	if err != nil {
		return nil, nil, err
	}
	artifactIDs := make([]string, 0, len(result.Sources))
	for _, src := range result.Sources {
		title := src.Title
		uri := src.URI
		artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
			TaskID:       taskID,
			InvocationID: &invocationID,
			ArtifactType: "research_source",
			Title:        &title,
			URI:          &uri,
			MediaType:    stringPtr("text/plain"),
			ContentText:  stringPtr(truncateForAPI(result.Answer, 4000)),
			Metadata:     map[string]any{"kind": src.Kind},
		})
		if err != nil {
			return nil, nil, err
		}
		artifactIDs = append(artifactIDs, artifactID)
	}
	if err := s.Postgres.UpdateToolInvocationArtifactIDs(ctx, invocationID, artifactIDs); err != nil {
		return nil, nil, err
	}
	return artifactIDs, []string{invocationID}, nil
}

func (s *Server) persistCapabilityExecution(ctx context.Context, entrypoint string, taskID *string, capability string, inputPayload map[string]any, result *capabilities.Result) (map[string]any, error) {
	sourceRefs := make([]domain.SourceRef, 0, len(result.SourceRefs))
	for _, item := range result.SourceRefs {
		sourceRefs = append(sourceRefs, domain.SourceRef{
			Title: item.Title,
			URI:   item.URI,
			Kind:  item.Kind,
		})
	}
	invocationID, err := s.Postgres.CreateToolInvocation(ctx, store.CreateToolInvocationParams{
		TaskID:        taskID,
		Entrypoint:    entrypoint,
		Capability:    capability,
		InputPayload:  inputPayload,
		OutputPayload: map[string]any{"summary": result.Summary, "status": result.Status, "output": result.Output},
		Status:        result.Status,
		DurationMS:    0,
		SourceRefs:    sourceRefs,
		ArtifactIDs:   []string{},
		ErrorMessage:  stringPtr(strings.TrimSpace(result.ErrorMessage)),
	})
	if err != nil {
		return nil, err
	}
	artifactIDs := make([]string, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
			TaskID:       taskID,
			InvocationID: &invocationID,
			ArtifactType: artifact.ArtifactType,
			Title:        stringPtr(strings.TrimSpace(artifact.Title)),
			URI:          stringPtr(strings.TrimSpace(artifact.URI)),
			MediaType:    stringPtr(strings.TrimSpace(artifact.MediaType)),
			ContentText:  stringPtr(truncateForAPI(strings.TrimSpace(artifact.ContentText), 4000)),
			Metadata:     artifact.Metadata,
		})
		if err != nil {
			return nil, err
		}
		artifactIDs = append(artifactIDs, artifactID)
	}
	if err := s.Postgres.UpdateToolInvocationArtifactIDs(ctx, invocationID, artifactIDs); err != nil {
		return nil, err
	}
	artifacts, err := s.Postgres.ListArtifactsByInvocation(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	record, err := s.Postgres.GetToolInvocation(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":             result.Status,
		"summary":            result.Summary,
		"output":             result.Output,
		"source_refs":        sourceRefs,
		"tool_invocation_id": invocationID,
		"artifact_ids":       artifactIDs,
		"invocation":         record,
		"artifacts":          artifacts,
	}, nil
}

func withLatency(c domain.ComponentHealth, latency *float64) domain.ComponentHealth {
	c.LatencyMS = latency
	return c
}

func (s *Server) currentTime() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) safeModeEnabled() bool {
	if s.SafeMode != nil {
		return s.SafeMode.Enabled()
	}
	return s.Config.SafeMode
}

func (s *Server) reconcileRootTask(ctx context.Context, taskID string) {
	task, err := s.Postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.InitiativeID != nil && strings.TrimSpace(*task.InitiativeID) != "" {
		_, _ = s.Postgres.ReconcileInitiativeExecution(ctx, strings.TrimSpace(*task.InitiativeID))
	}
	rootID := task.ID
	if task.RootTaskID != nil && strings.TrimSpace(*task.RootTaskID) != "" {
		rootID = strings.TrimSpace(*task.RootTaskID)
	}
	if rootID == task.ID && task.ParentTaskID == nil {
		return
	}
	tasks, err := s.Postgres.ListByRoot(ctx, rootID)
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
			if failed, err := s.Postgres.FailTask(ctx, root.ID, "api:aggregate", "One or more subtasks failed", "One or more subtasks failed", root.Results); err == nil && failed != nil {
				s.indexTask(ctx, failed.ID)
			}
		}
		return
	}
	if allCompleted && root.State != domain.TaskStateCompleted {
		if completed, err := s.Postgres.CompleteTask(ctx, root.ID, "api:aggregate", "All subtasks completed", root.Results); err == nil && completed != nil {
			s.indexTask(ctx, completed.ID)
		}
	}
}

func isSupportedAgent(agent domain.AgentType) bool {
	switch agent {
	case domain.AgentTypePlanner, domain.AgentTypeResearcher, domain.AgentTypeCoder, domain.AgentTypeReviewer:
		return true
	default:
		return false
	}
}

func isSupportedPriority(priority domain.Priority) bool {
	switch priority {
	case domain.PriorityCritical, domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow:
		return true
	default:
		return false
	}
}

func isRecognizedTaskState(state domain.TaskState) bool {
	switch state {
	case domain.TaskStateCreated, domain.TaskStateQueued, domain.TaskStateAssigned, domain.TaskStateInProgress, domain.TaskStateWaitingApproval, domain.TaskStateReview, domain.TaskStateRetrying, domain.TaskStateCompleted, domain.TaskStateFailed, domain.TaskStateCancelled:
		return true
	default:
		return false
	}
}

func isCancellableState(state domain.TaskState) bool {
	switch state {
	case domain.TaskStateCreated, domain.TaskStateQueued, domain.TaskStateAssigned, domain.TaskStateInProgress, domain.TaskStateWaitingApproval:
		return true
	default:
		return false
	}
}

func classifyAgent(description string, assigned *domain.AgentType) domain.AgentType {
	if assigned != nil {
		return *assigned
	}
	text := strings.ToLower(description)
	plannerHints := []string{"plan", "design", "architect", "roadmap", "spec", "requirements", "investigate", "research", "compare", "analyze"}
	for _, hint := range plannerHints {
		if strings.Contains(text, hint) {
			return domain.AgentTypePlanner
		}
	}
	return domain.AgentTypeCoder
}

func (s *Server) workspacePath(target domain.ExecutionTarget, metadata map[string]any) (*string, error) {
	if target == domain.ExecutionTargetLocal {
		raw := strings.TrimSpace(asString(metadata["workspace_root"]))
		if raw == "" {
			return nil, errors.New("execution_target=local requires metadata.workspace_root")
		}
		return nil, nil
	}
	base := strings.TrimSpace(s.Config.WorkspaceRoot)
	if base == "" {
		return nil, nil
	}
	value := filepath.Clean(base)
	return &value, nil
}

func asString(input any) string {
	switch v := input.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func floatValue(input any) float64 {
	switch v := input.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		out, _ := v.Float64()
		return out
	default:
		return 0
	}
}
