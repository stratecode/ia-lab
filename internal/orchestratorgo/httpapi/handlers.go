package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type Server struct {
	Config        config.Config
	Postgres      *store.PostgresStore
	Redis         *store.RedisStore
	Research      *research.Service
	Now           func() time.Time
	Version       string
	OpenAIToolsID string
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
	})
	r.Route("/approvals", func(r chi.Router) {
		r.Get("/", s.listApprovals)
		r.Get("/{approvalID}", s.getApproval)
		r.Post("/{approvalID}/approve", s.approveApproval)
		r.Post("/{approvalID}/reject", s.rejectApproval)
	})
	r.Get("/workers", s.listWorkers)
	r.Post("/config/safe-mode", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /config/safe-mode") })
	r.Post("/workspaces/cleanup", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /workspaces/cleanup") })
	r.Get("/capabilities", s.listCapabilities)
	r.Post("/tools/web/search", s.webSearch)
	r.Post("/tools/web/fetch", s.webFetch)
	r.Post("/tools/documents/read", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /tools/documents/read") })
	r.Post("/tools/images/analyze", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /tools/images/analyze") })
	r.Post("/research/query", s.researchQuery)
	r.Get("/research/runs/{researchRunID}", s.getResearchRun)
	r.Post("/evaluations/reference", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /evaluations/reference") })
	r.Post("/evaluations/judge", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "POST /evaluations/judge") })
	r.Get("/datasets/evaluation-items", func(w http.ResponseWriter, r *http.Request) { writeNotImplemented(w, "GET /datasets/evaluation-items") })
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
		SafeMode:   s.Config.SafeMode,
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
		"capabilities": []string{"web.search", "web.fetch", "research.query"},
		"total":        3,
	})
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	filter := store.TaskListFilter{
		State:           domain.TaskState(r.URL.Query().Get("state")),
		Agent:           domain.AgentType(r.URL.Query().Get("agent")),
		Priority:        domain.Priority(r.URL.Query().Get("priority")),
		ExecutionTarget: domain.ExecutionTarget(r.URL.Query().Get("execution_target")),
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
	if agentType != domain.AgentTypePlanner && agentType != domain.AgentTypeCoder {
		writeDetail(w, http.StatusBadRequest, "Phase 5 shadow core only supports planner and coder")
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
		ExecutionTarget:     body.ExecutionTarget,
		IdempotencyKey:      idempotencyPtr,
		Entrypoint:          "api",
		WorkspacePath:       workspacePath,
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

	if err := s.Redis.EnqueueTask(r.Context(), task.ID, agentType, body.Priority); err != nil {
		_ = s.Postgres.CancelTask(r.Context(), task.ID, "api:create", "queue enqueue failed: "+err.Error())
		writeDetail(w, http.StatusServiceUnavailable, "failed to enqueue task")
		return
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

	if approve && task != nil && task.AssignedAgent != nil {
		if err := s.Redis.EnqueueTask(r.Context(), task.ID, *task.AssignedAgent, task.Priority); err != nil {
			writeDetail(w, http.StatusServiceUnavailable, "approval resolved but failed to enqueue task")
			return
		}
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

func (s *Server) researchQuery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query               string   `json:"query"`
		TaskID              *string  `json:"task_id"`
		AllowedCapabilities []string `json:"allowed_capabilities"`
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
	writeJSON(w, http.StatusOK, map[string]any{
		"research_run":        researchRun,
		"answer":              result.Answer,
		"confidence":          result.Confidence,
		"sources":             result.Sources,
		"intent":              result.Intent,
		"search_hits":         len(result.SearchResults),
		"tool_invocation_ids": invocationIDs,
	})
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
	default:
		return []string{}
	}
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
		TaskID:         taskID,
		Entrypoint:     entrypoint,
		Capability:     "research.query",
		InputPayload:   map[string]any{"query": query},
		OutputPayload:  map[string]any{"answer": result.Answer, "confidence": result.Confidence, "intent": result.Intent},
		Status:         "success",
		DurationMS:     0,
		SourceRefs:     sourceRefs,
		ArtifactIDs:    []string{},
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
		value := filepath.Clean(raw)
		return &value, nil
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
