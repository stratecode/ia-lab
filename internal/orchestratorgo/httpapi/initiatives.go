package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	initiativesvc "github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

var (
	initiativePhaseDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "orchestrator",
		Subsystem: "initiative",
		Name:      "phase_duration_seconds",
		Help:      "Duration of initiative phase generation, review, and launch operations.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"action", "phase"})
	initiativeActionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "orchestrator",
		Subsystem: "initiative",
		Name:      "actions_total",
		Help:      "Total initiative actions grouped by action, phase and outcome.",
	}, []string{"action", "phase", "outcome"})
)

func (s *Server) listInitiatives(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListInitiatives(r.Context(), store.InitiativeListFilter{
		IncludeArchived: strings.EqualFold(r.URL.Query().Get("archived"), "include"),
		Limit:           parseIntDefault(r.URL.Query().Get("limit"), 100),
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.InitiativeListResponse{Items: items, Total: len(items)})
}

func (s *Server) createInitiative(w http.ResponseWriter, r *http.Request) {
	if s.Initiatives == nil {
		writeDetail(w, http.StatusServiceUnavailable, "initiative service is not configured")
		return
	}
	var body domain.InitiativeCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	body.Goal = strings.TrimSpace(body.Goal)
	body.WorkspaceRoot = strings.TrimSpace(body.WorkspaceRoot)
	body.CreatedBy = strings.TrimSpace(body.CreatedBy)
	if body.Title == "" {
		body.Title = truncateTitle(body.Goal, 80)
	}
	if body.Title == "" || body.Goal == "" || body.WorkspaceRoot == "" {
		writeDetail(w, http.StatusBadRequest, "title, goal and workspace_root are required")
		return
	}
	if body.ExecutionMode == "" {
		body.ExecutionMode = domain.InitiativeExecutionModeSelective
	}
	item, err := s.Postgres.CreateInitiative(r.Context(), store.CreateInitiativeParams{
		Title:         body.Title,
		WorkspaceRoot: body.WorkspaceRoot,
		Goal:          body.Goal,
		CreatedBy:     firstNonEmptyString(body.CreatedBy, "api"),
		ExecutionMode: body.ExecutionMode,
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getInitiative(w http.ResponseWriter, r *http.Request) {
	item, err := s.Postgres.GetInitiative(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "initiative not found")
		return
	}
	reviews, err := s.Postgres.ListInitiativePhaseReviews(r.Context(), item.ID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"initiative": item,
		"reviews":    reviews,
	})
}

func (s *Server) getInitiativeArtifacts(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListInitiativeArtifacts(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.InitiativeArtifactsResponse{Items: items, Total: len(items)})
}

func (s *Server) listInitiativeTasks(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListInitiativeTasks(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.InitiativeTaskListResponse{Items: items, Total: len(items)})
}

func (s *Server) advanceInitiative(w http.ResponseWriter, r *http.Request) {
	if s.Initiatives == nil {
		writeDetail(w, http.StatusServiceUnavailable, "initiative service is not configured")
		return
	}
	started := time.Now()
	var body domain.InitiativeAdvanceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.Postgres.GetInitiative(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "initiative not found")
		return
	}
	var (
		phase        domain.InitiativePhase
		status       domain.InitiativeStatus
		markdownType string
		jsonType     string
		generatedBy  = "initiative-service"
		artifacts    phaseArtifactIDs
	)
	switch item.CurrentPhase {
	case domain.InitiativePhaseRequirements:
		payload, err := s.Initiatives.GenerateRequirements(r.Context(), item, body.Feedback)
		if err != nil {
			recordInitiativeAction("generate", domain.InitiativePhaseRequirements, "error", started)
			writeDetail(w, http.StatusBadGateway, err.Error())
			return
		}
		artifacts, err = s.persistInitiativePhaseArtifacts(r.Context(), item, domain.InitiativePhaseRequirements, payload)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		phase = domain.InitiativePhaseRequirements
		status = domain.InitiativeStatusRequirementsReview
		markdownType = "initiative_requirements.md"
		jsonType = "initiative_requirements.json"
		_, _ = markdownType, jsonType
	case domain.InitiativePhaseDesign:
		requirements, err := s.getActiveInitiativeArtifactJSON(r.Context(), item.ActiveRequirementsArtifactID)
		if err != nil {
			writeDetail(w, http.StatusBadRequest, err.Error())
			return
		}
		payload, err := s.Initiatives.GenerateDesign(r.Context(), item, requirements, body.Feedback)
		if err != nil {
			recordInitiativeAction("generate", domain.InitiativePhaseDesign, "error", started)
			writeDetail(w, http.StatusBadGateway, err.Error())
			return
		}
		artifacts, err = s.persistInitiativePhaseArtifacts(r.Context(), item, domain.InitiativePhaseDesign, payload)
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		phase = domain.InitiativePhaseDesign
		status = domain.InitiativeStatusDesignReview
	case domain.InitiativePhasePlan:
		writeDetail(w, http.StatusBadRequest, "use /initiatives/{id}/tasks/generate to build the execution plan and task backlog")
		return
	default:
		writeDetail(w, http.StatusBadRequest, "initiative cannot be advanced from the current phase")
		return
	}
	if _, err := s.Postgres.CreateInitiativePhaseReview(r.Context(), store.CreateInitiativePhaseReviewParams{
		InitiativeID:       item.ID,
		Phase:              phase,
		Decision:           domain.InitiativeReviewGenerated,
		Feedback:           cleanStringPtr(body.Feedback),
		GeneratedBy:        &generatedBy,
		ArtifactMarkdownID: artifacts.MarkdownID,
		ArtifactJSONID:     artifacts.JSONID,
	}); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	params := store.UpdateInitiativeParams{Status: &status}
	switch phase {
	case domain.InitiativePhaseRequirements:
		params.ActiveRequirementsArtifactID = artifacts.MarkdownID
	case domain.InitiativePhaseDesign:
		params.ActiveDesignArtifactID = artifacts.MarkdownID
	}
	if phase == domain.InitiativePhaseRequirements {
		params.ActiveRequirementsArtifactID = artifacts.JSONID
	}
	if phase == domain.InitiativePhaseDesign {
		params.ActiveDesignArtifactID = artifacts.JSONID
	}
	updated, err := s.updateInitiativeActiveArtifacts(r.Context(), item, phase, status, artifacts)
	if err != nil {
		recordInitiativeAction("generate", phase, "error", started)
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Info().Str("initiative_id", item.ID).Str("phase", string(phase)).Str("artifact_id", derefString(artifacts.JSONID)).Msg("initiative phase generated")
	recordInitiativeAction("generate", phase, "success", started)
	writeJSON(w, http.StatusOK, map[string]any{
		"initiative": updated,
		"artifacts":  artifacts,
	})
}

func (s *Server) approveInitiativePhase(w http.ResponseWriter, r *http.Request) {
	s.resolveInitiativePhase(w, r, true)
}

func (s *Server) rejectInitiativePhase(w http.ResponseWriter, r *http.Request) {
	s.resolveInitiativePhase(w, r, false)
}

func (s *Server) resolveInitiativePhase(w http.ResponseWriter, r *http.Request, approve bool) {
	started := time.Now()
	var body domain.InitiativeReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.Postgres.GetInitiative(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "initiative not found")
		return
	}
	phase := domain.InitiativePhase(strings.TrimSpace(chi.URLParam(r, "phase")))
	if !domain.IsRecognizedInitiativePhase(phase) {
		writeDetail(w, http.StatusBadRequest, "invalid phase")
		return
	}
	if item.CurrentPhase != phase && !(phase == domain.InitiativePhasePlan && item.Status == domain.InitiativeStatusPlanReview) {
		recordInitiativeAction(reviewActionName(approve), phase, "conflict", started)
		writeDetail(w, http.StatusConflict, "phase does not match initiative current phase")
		return
	}
	decision := domain.InitiativeReviewRejected
	if approve {
		decision = domain.InitiativeReviewApproved
	}
	activeMarkdownID, activeJSONID := s.activeArtifactIDsForPhase(item, phase)
	operator := firstNonEmptyString(strings.TrimSpace(body.Operator), "operator")
	if _, err := s.Postgres.CreateInitiativePhaseReview(r.Context(), store.CreateInitiativePhaseReviewParams{
		InitiativeID:       item.ID,
		Phase:              phase,
		Decision:           decision,
		Feedback:           cleanStringPtr(body.Feedback),
		GeneratedBy:        &operator,
		ArtifactMarkdownID: activeMarkdownID,
		ArtifactJSONID:     activeJSONID,
	}); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	var nextStatus domain.InitiativeStatus
	var nextPhase domain.InitiativePhase
	if approve {
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
			writeDetail(w, http.StatusBadRequest, "approval is not supported for this phase")
			return
		}
	} else {
		switch phase {
		case domain.InitiativePhaseRequirements:
			nextStatus = domain.InitiativeStatusRequirementsDraft
			nextPhase = domain.InitiativePhaseRequirements
		case domain.InitiativePhaseDesign:
			nextStatus = domain.InitiativeStatusDesignDraft
			nextPhase = domain.InitiativePhaseDesign
		case domain.InitiativePhasePlan:
			nextStatus = domain.InitiativeStatusPlanDraft
			nextPhase = domain.InitiativePhasePlan
		default:
			writeDetail(w, http.StatusBadRequest, "rejection is not supported for this phase")
			return
		}
	}
	updated, err := s.Postgres.UpdateInitiative(r.Context(), item.ID, store.UpdateInitiativeParams{
		Status:       &nextStatus,
		CurrentPhase: &nextPhase,
	})
	if err != nil {
		recordInitiativeAction(reviewActionName(approve), phase, "error", started)
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Info().
		Str("initiative_id", item.ID).
		Str("phase", string(phase)).
		Str("operator", operator).
		Str("decision", string(decision)).
		Msg("initiative phase reviewed")
	recordInitiativeAction(reviewActionName(approve), phase, "success", started)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) generateInitiativeTasks(w http.ResponseWriter, r *http.Request) {
	if s.Initiatives == nil {
		writeDetail(w, http.StatusServiceUnavailable, "initiative service is not configured")
		return
	}
	started := time.Now()
	var body domain.InitiativeAdvanceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.Postgres.GetInitiative(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "initiative not found")
		return
	}
	if item.Status != domain.InitiativeStatusPlanDraft || item.CurrentPhase != domain.InitiativePhasePlan {
		writeDetail(w, http.StatusConflict, "initiative must have approved design before generating the execution plan")
		return
	}
	designJSON, err := s.getActiveInitiativeArtifactJSON(r.Context(), item.ActiveDesignArtifactID)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := s.Initiatives.GenerateExecutionPlan(r.Context(), item, designJSON, body.Feedback)
	if err != nil {
		recordInitiativeAction("plan_generate", domain.InitiativePhasePlan, "error", started)
		writeDetail(w, http.StatusBadGateway, err.Error())
		return
	}
	artifacts, err := s.persistInitiativePhaseArtifacts(r.Context(), item, domain.InitiativePhasePlan, payload)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	generatedBy := "initiative-service"
	if _, err := s.Postgres.CreateInitiativePhaseReview(r.Context(), store.CreateInitiativePhaseReviewParams{
		InitiativeID:       item.ID,
		Phase:              domain.InitiativePhasePlan,
		Decision:           domain.InitiativeReviewGenerated,
		Feedback:           cleanStringPtr(body.Feedback),
		GeneratedBy:        &generatedBy,
		ArtifactMarkdownID: artifacts.MarkdownID,
		ArtifactJSONID:     artifacts.JSONID,
	}); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.updateInitiativeActiveArtifacts(r.Context(), item, domain.InitiativePhasePlan, domain.InitiativeStatusPlanReview, artifacts)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	createdTasks, err := s.materializeInitiativePlanTasks(r.Context(), updated, payload.JSON)
	if err != nil {
		recordInitiativeAction("plan_generate", domain.InitiativePhasePlan, "error", started)
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Info().
		Str("initiative_id", item.ID).
		Str("phase", string(domain.InitiativePhasePlan)).
		Str("artifact_id", derefString(artifacts.JSONID)).
		Int("task_count", len(createdTasks)).
		Msg("initiative execution plan generated")
	recordInitiativeAction("plan_generate", domain.InitiativePhasePlan, "success", started)
	writeJSON(w, http.StatusOK, map[string]any{
		"initiative": updated,
		"artifacts":  artifacts,
		"tasks":      createdTasks,
		"total":      len(createdTasks),
	})
}

func (s *Server) launchInitiativeTasks(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var body domain.InitiativeLaunchTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := s.Postgres.GetInitiative(r.Context(), chi.URLParam(r, "initiativeID"))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if item == nil {
		writeDetail(w, http.StatusNotFound, "initiative not found")
		return
	}
	if item.Status != domain.InitiativeStatusExecutionReady && item.Status != domain.InitiativeStatusExecuting && item.Status != domain.InitiativeStatusBlocked {
		writeDetail(w, http.StatusConflict, "initiative is not ready for execution")
		return
	}
	allItems, err := s.Postgres.ListInitiativeTasks(r.Context(), item.ID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	selected := selectInitiativeTasks(allItems, body.TaskIDs, body.Groups)
	if len(selected) == 0 {
		recordInitiativeAction("launch", domain.InitiativePhaseExecution, "empty", started)
		writeDetail(w, http.StatusBadRequest, "no launchable initiative tasks matched the request")
		return
	}
	launched := make([]domain.TaskResponse, 0, len(selected))
	for _, link := range selected {
		mode := firstNonEmptyString(strings.TrimSpace(body.ModeOverrides[link.TaskID]), strings.TrimSpace(link.ExecutionMode))
		if !domain.IsRecognizedTaskLaunchMode(mode) {
			recordInitiativeAction("launch", domain.InitiativePhaseExecution, "error", started)
			writeDetail(w, http.StatusBadRequest, "invalid execution mode override")
			return
		}
		if _, err := s.Postgres.UpdateInitiativeTaskMode(r.Context(), item.ID, link.TaskID, mode); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.Postgres.UpdateTaskLaunchMode(r.Context(), link.TaskID, mode); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if mode == domain.TaskLaunchModeManual {
			continue
		}
		queuedTask, err := s.Postgres.QueueTaskForLaunch(r.Context(), link.TaskID, "api:initiative_launch", "Queued from initiative launch")
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if queuedTask.ExecutionTarget != domain.ExecutionTargetLocal && queuedTask.AssignedAgent != nil {
			if err := s.Redis.EnqueueTask(r.Context(), queuedTask.ID, *queuedTask.AssignedAgent, queuedTask.Priority); err != nil {
				writeDetail(w, http.StatusServiceUnavailable, "failed to enqueue remote task")
				return
			}
		}
		launched = append(launched, *queuedTask)
	}
	updated, err := s.Postgres.ReconcileInitiativeExecution(r.Context(), item.ID)
	if err != nil {
		recordInitiativeAction("launch", domain.InitiativePhaseExecution, "error", started)
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Info().
		Str("initiative_id", item.ID).
		Str("phase", string(domain.InitiativePhaseExecution)).
		Int("selected_count", len(selected)).
		Int("launched_count", len(launched)).
		Msg("initiative tasks launch processed")
	recordInitiativeAction("launch", domain.InitiativePhaseExecution, "success", started)
	writeJSON(w, http.StatusOK, map[string]any{
		"initiative": updated,
		"tasks":      launched,
		"total":      len(launched),
	})
}

func (s *Server) updateInitiativeTaskMode(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var body domain.InitiativeTaskModeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.ExecutionMode = strings.TrimSpace(body.ExecutionMode)
	if !domain.IsRecognizedTaskLaunchMode(body.ExecutionMode) {
		writeDetail(w, http.StatusBadRequest, "invalid execution_mode")
		return
	}
	item, err := s.Postgres.UpdateInitiativeTaskMode(r.Context(), chi.URLParam(r, "initiativeID"), chi.URLParam(r, "taskID"), body.ExecutionMode)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Postgres.UpdateTaskLaunchMode(r.Context(), item.TaskID, body.ExecutionMode); err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, err := s.Postgres.GetInitiativeTaskLink(r.Context(), item.InitiativeID, item.TaskID)
	if err != nil {
		recordInitiativeAction("mode_update", domain.InitiativePhaseExecution, "error", started)
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.Postgres.ReconcileInitiativeExecution(r.Context(), item.InitiativeID)
	log.Info().
		Str("initiative_id", item.InitiativeID).
		Str("task_id", item.TaskID).
		Str("execution_mode", body.ExecutionMode).
		Msg("initiative task mode updated")
	recordInitiativeAction("mode_update", domain.InitiativePhaseExecution, "success", started)
	writeJSON(w, http.StatusOK, updated)
}

type phaseArtifactIDs struct {
	MarkdownID *string `json:"markdown_id"`
	JSONID     *string `json:"json_id"`
}

func (s *Server) persistInitiativePhaseArtifacts(ctx context.Context, item *domain.InitiativeResponse, phase domain.InitiativePhase, payload initiativesvc.PhaseArtifacts) (phaseArtifactIDs, error) {
	reviews, err := s.Postgres.ListInitiativePhaseReviews(ctx, item.ID)
	if err != nil {
		return phaseArtifactIDs{}, err
	}
	version := 1
	for _, review := range reviews {
		if review.Phase == phase && review.Decision == domain.InitiativeReviewGenerated {
			version++
		}
	}
	metaBase := map[string]any{
		"initiative_id":    item.ID,
		"initiative_phase": string(phase),
		"version":          version,
	}
	titleBase := fmt.Sprintf("%s %s v%d", item.Title, strings.Title(string(phase)), version)
	mdType := "initiative_" + string(phase) + ".md"
	jsonType := "initiative_" + string(phase) + ".json"
	mdID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: mdType,
		Title:        cleanStringPtr(titleBase + " markdown"),
		MediaType:    cleanStringPtr("text/markdown"),
		ContentText:  cleanStringPtr(payload.Markdown),
		Metadata: mergeMaps(metaBase, map[string]any{
			"format": "markdown",
		}),
	})
	if err != nil {
		return phaseArtifactIDs{}, err
	}
	jsonRaw, _ := json.Marshal(payload.JSON)
	jsonID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: jsonType,
		Title:        cleanStringPtr(titleBase + " json"),
		MediaType:    cleanStringPtr("application/json"),
		ContentText:  cleanStringPtr(string(jsonRaw)),
		Metadata: mergeMaps(metaBase, map[string]any{
			"format":  "json",
			"payload": payload.JSON,
		}),
	})
	if err != nil {
		return phaseArtifactIDs{}, err
	}
	return phaseArtifactIDs{MarkdownID: &mdID, JSONID: &jsonID}, nil
}

func (s *Server) getActiveInitiativeArtifactJSON(ctx context.Context, artifactID *string) (map[string]any, error) {
	if artifactID == nil || strings.TrimSpace(*artifactID) == "" {
		return nil, fmt.Errorf("required initiative artifact is missing")
	}
	items, err := s.Postgres.ListArtifactsByIDs(ctx, []string{strings.TrimSpace(*artifactID)})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("initiative artifact not found")
	}
	artifact := items[0]
	var payload map[string]any
	if raw := strings.TrimSpace(stringValue(artifact.ContentText)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err == nil && len(payload) > 0 {
			return payload, nil
		}
	}
	if metaPayload, ok := artifact.Metadata["payload"].(map[string]any); ok {
		return metaPayload, nil
	}
	return nil, fmt.Errorf("initiative artifact JSON payload is missing")
}

func (s *Server) updateInitiativeActiveArtifacts(ctx context.Context, item *domain.InitiativeResponse, phase domain.InitiativePhase, status domain.InitiativeStatus, ids phaseArtifactIDs) (*domain.InitiativeResponse, error) {
	params := store.UpdateInitiativeParams{
		Status:       &status,
		CurrentPhase: &phase,
	}
	switch phase {
	case domain.InitiativePhaseRequirements:
		params.ActiveRequirementsArtifactID = ids.JSONID
	case domain.InitiativePhaseDesign:
		params.ActiveDesignArtifactID = ids.JSONID
	case domain.InitiativePhasePlan:
		params.ActivePlanArtifactID = ids.JSONID
	}
	return s.Postgres.UpdateInitiative(ctx, item.ID, params)
}

func (s *Server) activeArtifactIDsForPhase(item *domain.InitiativeResponse, phase domain.InitiativePhase) (*string, *string) {
	switch phase {
	case domain.InitiativePhaseRequirements:
		return item.ActiveRequirementsArtifactID, item.ActiveRequirementsArtifactID
	case domain.InitiativePhaseDesign:
		return item.ActiveDesignArtifactID, item.ActiveDesignArtifactID
	case domain.InitiativePhasePlan:
		return item.ActivePlanArtifactID, item.ActivePlanArtifactID
	default:
		return nil, nil
	}
}

func (s *Server) materializeInitiativePlanTasks(ctx context.Context, item *domain.InitiativeResponse, payload map[string]any) ([]domain.InitiativeTaskLinkResponse, error) {
	epics := normalizeMapSlice(payload["epics"])
	created := make([]domain.InitiativeTaskLinkResponse, 0)
	launchOrder := 10
	for _, epic := range epics {
		epicName := strings.TrimSpace(asString(epic["name"]))
		group := strings.TrimSpace(asString(epic["group"]))
		tasks := normalizeMapSlice(epic["tasks"])
		for _, taskSpec := range tasks {
			description := firstNonEmptyString(strings.TrimSpace(asString(taskSpec["description"])), strings.TrimSpace(asString(taskSpec["title"])))
			assigned := domain.AgentType(strings.TrimSpace(asString(taskSpec["suggested_agent"])))
			if assigned == "" {
				assigned = domain.AgentTypeCoder
			}
			planned := assigned
			priority := domain.Priority(strings.TrimSpace(asString(taskSpec["priority"])))
			if !isSupportedPriority(priority) {
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
				"approval_required":       asBoolHTTP(taskSpec["approval_required"]),
				"planned_execution_mode":  strings.TrimSpace(asString(taskSpec["execution_mode"])),
				"launch_group":            group,
				"epic":                    epicName,
				"title":                   asString(taskSpec["title"]),
			}
			if specMeta, ok := taskSpec["metadata"].(map[string]any); ok {
				for key, value := range specMeta {
					metadata[key] = value
				}
			}
			normalizeInitiativeToolRequest(metadata, assigned)
			workspacePath, err := s.workspacePath(executionTarget, metadata)
			if err != nil {
				return nil, err
			}
			task, err := s.Postgres.CreateTask(ctx, store.CreateTaskParams{
				Description:     description,
				Metadata:        metadata,
				Priority:        priority,
				AssignedAgent:   assigned,
				PlannedAgent:    &planned,
				ExecutionTarget: executionTarget,
				Entrypoint:      "initiative:generate",
				WorkspacePath:   workspacePath,
				InitiativeID:    &item.ID,
				TaskKind:        domain.TaskKindPlanStep,
				QueueOnCreate:   false,
			})
			if err != nil {
				return nil, err
			}
			link, err := s.Postgres.LinkInitiativeTask(ctx, store.LinkInitiativeTaskParams{
				InitiativeID:  item.ID,
				TaskID:        task.ID,
				PhaseOrigin:   "plan",
				Epic:          cleanStringPtr(epicName),
				LaunchGroup:   cleanStringPtr(group),
				ExecutionMode: firstNonEmptyString(strings.TrimSpace(asString(taskSpec["execution_mode"])), domain.TaskLaunchModeAgentLocal),
				LaunchOrder:   launchOrder,
			})
			if err != nil {
				return nil, err
			}
			created = append(created, *link)
			launchOrder += 10
		}
	}
	sort.SliceStable(created, func(i, j int) bool { return created[i].LaunchOrder < created[j].LaunchOrder })
	return created, nil
}

func normalizeMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			typed, ok := item.(map[string]any)
			if ok {
				out = append(out, typed)
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeInitiativeToolRequest(metadata map[string]any, assigned domain.AgentType) {
	projectRequest, _ := metadata["project_request"].(map[string]any)
	toolRequest, _ := metadata["tool_request"].(map[string]any)
	if toolRequest == nil {
		toolRequest = map[string]any{}
	}
	if strings.TrimSpace(asString(toolRequest["tool"])) == "" {
		switch assigned {
		case domain.AgentTypeResearcher:
			toolRequest["tool"] = "research_project"
		case domain.AgentTypeCoder:
			toolRequest["tool"] = "scaffold_project"
		case domain.AgentTypeReviewer:
			toolRequest["tool"] = "review_project"
		}
	}
	if len(projectRequest) > 0 {
		toolRequest["project_request"] = projectRequest
		for key, value := range projectRequest {
			if _, exists := toolRequest[key]; !exists {
				toolRequest[key] = value
			}
		}
		parentDirectory := strings.TrimSpace(asString(projectRequest["parent_directory"]))
		projectName := strings.TrimSpace(asString(projectRequest["project_name"]))
		if parentDirectory != "" && projectName != "" && strings.TrimSpace(asString(toolRequest["project_root"])) == "" {
			toolRequest["project_root"] = filepath.ToSlash(filepath.Join(parentDirectory, projectName))
		}
	}
	if goal := strings.TrimSpace(asString(metadata["initiative_goal"])); goal != "" && strings.TrimSpace(asString(toolRequest["goal"])) == "" {
		toolRequest["goal"] = goal
	}
	metadata["tool_request"] = toolRequest
}

func selectInitiativeTasks(items []domain.InitiativeTaskLinkResponse, taskIDs, groups []string) []domain.InitiativeTaskLinkResponse {
	taskSet := make(map[string]struct{}, len(taskIDs))
	for _, id := range taskIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			taskSet[id] = struct{}{}
		}
	}
	groupSet := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group != "" {
			groupSet[group] = struct{}{}
		}
	}
	if len(taskSet) == 0 && len(groupSet) == 0 {
		return items
	}
	out := make([]domain.InitiativeTaskLinkResponse, 0, len(items))
	for _, item := range items {
		_, taskMatch := taskSet[item.TaskID]
		_, groupMatch := groupSet[firstNonEmptyString(stringValue(item.LaunchGroup), stringValue(item.Epic))]
		if taskMatch || groupMatch {
			out = append(out, item)
		}
	}
	return out
}

func mergeMaps(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func cleanStringPtr(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func truncateTitle(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max]) + "..."
}

func asBoolHTTP(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		value := strings.TrimSpace(strings.ToLower(v))
		return value == "true" || value == "1" || value == "yes" || value == "on"
	default:
		return false
	}
}

func recordInitiativeAction(action string, phase domain.InitiativePhase, outcome string, started time.Time) {
	initiativeActionsTotal.WithLabelValues(action, string(phase), outcome).Inc()
	if !started.IsZero() {
		initiativePhaseDuration.WithLabelValues(action, string(phase)).Observe(time.Since(started).Seconds())
	}
}

func reviewActionName(approve bool) string {
	if approve {
		return "approve"
	}
	return "reject"
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
