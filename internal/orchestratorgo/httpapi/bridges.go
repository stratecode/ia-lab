package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

func (s *Server) registerBridge(w http.ResponseWriter, r *http.Request) {
	var body domain.LocalBridgeRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.BridgeID = strings.TrimSpace(body.BridgeID)
	body.Name = strings.TrimSpace(body.Name)
	body.Hostname = strings.TrimSpace(body.Hostname)
	body.WorkspaceRoot = strings.TrimSpace(body.WorkspaceRoot)
	if body.BridgeID == "" || body.Name == "" || body.Hostname == "" || body.WorkspaceRoot == "" {
		writeDetail(w, http.StatusBadRequest, "bridge_id, name, hostname and workspace_root are required")
		return
	}
	record, err := s.Postgres.UpsertLocalBridge(r.Context(), store.UpsertLocalBridgeParams{
		ID:            body.BridgeID,
		Name:          body.Name,
		Hostname:      body.Hostname,
		WorkspaceRoot: body.WorkspaceRoot,
		Capabilities:  body.Capabilities,
		APIKeyName:    body.APIKeyName,
		Status:        "active",
	})
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) heartbeatBridge(w http.ResponseWriter, r *http.Request) {
	var body domain.LocalBridgeHeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	record, err := s.Postgres.HeartbeatLocalBridge(r.Context(), chi.URLParam(r, "bridgeID"), strings.TrimSpace(body.Status))
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if record == nil {
		writeDetail(w, http.StatusNotFound, "local bridge not found")
		return
	}
	if body.CurrentTaskID != nil && strings.TrimSpace(*body.CurrentTaskID) != "" {
		ttl := s.localBridgeLeaseTTLSeconds()
		if body.LeaseTTLSeconds != nil && *body.LeaseTTLSeconds > 0 {
			ttl = *body.LeaseTTLSeconds
		}
		if err := s.Postgres.TouchLocalBridgeTaskLease(r.Context(), strings.TrimSpace(*body.CurrentTaskID), chi.URLParam(r, "bridgeID"), ttl, "active", body.CurrentStage, body.CurrentTool, body.CurrentSummary); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, record)
}

func (s *Server) listBridges(w http.ResponseWriter, r *http.Request) {
	items, err := s.Postgres.ListLocalBridges(r.Context())
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, domain.LocalBridgeListResponse{Items: items, Total: len(items)})
}

func (s *Server) claimNextBridgeTask(w http.ResponseWriter, r *http.Request) {
	claim, err := s.Postgres.ClaimNextLocalBridgeTaskWithTTL(r.Context(), chi.URLParam(r, "bridgeID"), s.localBridgeLeaseTTLSeconds())
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if claim == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
		return
	}
	writeJSON(w, http.StatusOK, claim)
}

func (s *Server) submitBridgeTaskResult(w http.ResponseWriter, r *http.Request) {
	var body domain.LocalBridgeResultRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	taskID := chi.URLParam(r, "taskID")
	bridgeID := chi.URLParam(r, "bridgeID")
	task, err := s.Postgres.GetTask(r.Context(), taskID)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeDetail(w, http.StatusNotFound, "task not found")
		return
	}

	results, invocationID, artifactIDs, err := s.persistLocalBridgeExecution(r.Context(), bridgeID, taskID, task, body)
	if err != nil {
		writeDetail(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := strings.ToLower(strings.TrimSpace(body.Status))
	switch status {
	case "waiting_approval":
		_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "waiting_approval", stringPtr("waiting_approval"), nil, body.Summary)
		timeout := 300
		if body.TimeoutSeconds != nil && *body.TimeoutSeconds > 0 {
			timeout = *body.TimeoutSeconds
		}
		actionType := firstNonEmptyString(derefStringPtr(body.ActionType), "local_bridge_tool")
		targetResource := firstNonEmptyString(derefStringPtr(body.TargetResource), derefStringPtr(body.Summary), task.Description)
		if _, err := s.Postgres.RequestApproval(r.Context(), taskID, actionType, targetResource, timeout, "bridge:"+bridgeID, firstNonEmptyString(derefStringPtr(body.Summary), "Local bridge requested approval"), map[string]any{"approval_requested": true}); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "success":
		_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "completed", stringPtr("completed"), nil, body.Summary)
		if completed, err := s.Postgres.CompleteTask(r.Context(), taskID, "bridge:"+bridgeID, firstNonEmptyString(derefStringPtr(body.Summary), "Local bridge execution completed"), results); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		} else if completed != nil {
			s.indexTask(r.Context(), completed.ID)
		}
		if stopReason, blockerReason, shouldStop := shouldStopObjectiveAfterSuccess(task, body); shouldStop {
			if err := s.stopObjectiveAfterTaskCompletion(r.Context(), task, stopReason, blockerReason); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			}
			s.reconcileRootTask(r.Context(), taskID)
			break
		}
		if task.InitiativeID != nil {
			if err := s.queueReadyObjectiveTasks(r.Context(), *task.InitiativeID); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		s.reconcileRootTask(r.Context(), taskID)
	default:
		errorMessage := firstNonEmptyString(derefStringPtr(body.ErrorMessage), derefStringPtr(body.Stderr), derefStringPtr(body.Stdout), "Local bridge execution failed")
		if objectiveAsBool(task.Metadata["objective_baseline_validation"]) {
			_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "completed", stringPtr("completed"), nil, body.Summary)
			reason := firstNonEmptyString(derefStringPtr(body.Summary), "Objective baseline validation recorded failing evidence before the first edit")
			if completed, err := s.Postgres.CompleteTask(r.Context(), taskID, "bridge:"+bridgeID, reason, results); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			} else if completed != nil {
				s.indexTask(r.Context(), completed.ID)
			}
			if task.InitiativeID != nil {
				if err := s.queueReadyObjectiveTasks(r.Context(), *task.InitiativeID); err != nil {
					writeDetail(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			s.reconcileRootTask(r.Context(), taskID)
			break
		}
		if shouldStartObjectiveRepairCycle(task, body) {
			_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "completed", stringPtr("completed"), nil, body.Summary)
			reason := firstNonEmptyString(derefStringPtr(body.Summary), "Objective iteration completed and requested another repair cycle")
			if completed, err := s.Postgres.CompleteTask(r.Context(), taskID, "bridge:"+bridgeID, reason, results); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			} else if completed != nil {
				s.indexTask(r.Context(), completed.ID)
			}
			if err := s.startObjectiveRepairCycle(r.Context(), task, body); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			}
			if task.InitiativeID != nil {
				if _, err := s.Postgres.ReconcileInitiativeExecution(r.Context(), *task.InitiativeID); err != nil {
					writeDetail(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		} else {
			_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "failed", stringPtr("failed"), nil, body.Summary)
			if failed, err := s.Postgres.FailTask(r.Context(), taskID, "bridge:"+bridgeID, errorMessage, errorMessage, results); err != nil {
				writeDetail(w, http.StatusInternalServerError, err.Error())
				return
			} else if failed != nil {
				s.indexTask(r.Context(), failed.ID)
			}
		}
		s.reconcileRootTask(r.Context(), taskID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":             "accepted",
		"tool_invocation_id": invocationID,
		"artifact_ids":       artifactIDs,
	})
}

func shouldStopObjectiveAfterSuccess(task *domain.TaskResponse, result domain.LocalBridgeResultRequest) (string, string, bool) {
	if task == nil || task.InitiativeID == nil {
		return "", "", false
	}
	if !objectiveAsBool(task.Metadata["objective_entrypoint"]) {
		return "", "", false
	}
	if !objectiveTimeBudgetExceeded(task.Metadata, time.Now().UTC()) {
		return "", "", false
	}
	workItemKind := strings.TrimSpace(asString(task.Metadata["work_item_kind"]))
	if workItemKind == string(domain.WorkItemKindReview) && strings.EqualFold(derefStringPtr(result.ReviewDecision), "approved") {
		return "", "", false
	}
	budgetSeconds, deadline, ok := objectiveTimeBudgetDetails(task.Metadata)
	if !ok {
		return "", "", false
	}
	return "time_budget_exhausted", objectiveTimeBudgetExceededReason(budgetSeconds, deadline), true
}

func (s *Server) stopObjectiveAfterTaskCompletion(ctx context.Context, task *domain.TaskResponse, stopReason, blockerReason string) error {
	if task == nil || task.InitiativeID == nil {
		return nil
	}
	budgetSeconds, deadline, _ := objectiveTimeBudgetDetails(task.Metadata)
	patch := map[string]any{
		"objective_stop_reason":              stopReason,
		"objective_time_budget_exhausted":    stopReason == "time_budget_exhausted",
		"objective_time_budget_exhausted_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.Postgres.PatchTaskMetadata(ctx, task.ID, patch); err != nil {
		return err
	}
	if err := s.persistObjectiveStopSnapshot(ctx, task, stopReason, blockerReason, deadline, budgetSeconds); err != nil {
		return err
	}
	blocked := domain.InitiativeStatusBlocked
	currentPhase := domain.InitiativePhaseExecution
	if _, err := s.Postgres.UpdateInitiative(ctx, *task.InitiativeID, store.UpdateInitiativeParams{
		Status:       &blocked,
		CurrentPhase: &currentPhase,
	}); err != nil {
		return err
	}
	return nil
}

func (s *Server) persistLocalBridgeExecution(ctx context.Context, bridgeID, taskID string, task *domain.TaskResponse, result domain.LocalBridgeResultRequest) (map[string]any, string, []string, error) {
	taskIDPtr := &taskID
	agentType := string(domain.AgentTypeCoder)
	if task.AssignedAgent != nil {
		agentType = string(*task.AssignedAgent)
	}
	agentTypePtr := &agentType
	output := map[string]any{
		"summary":           strings.TrimSpace(derefStringPtr(result.Summary)),
		"stdout":            derefStringPtr(result.Stdout),
		"stderr":            derefStringPtr(result.Stderr),
		"exit_code":         result.ExitCode,
		"diff":              derefStringPtr(result.Diff),
		"changed_files":     result.ChangedFiles,
		"test_results":      result.TestResults,
		"review_decision":   derefStringPtr(result.ReviewDecision),
		"review_comments":   result.ReviewComments,
		"capability_usage":  result.CapabilityUsage,
		"capability_helped": result.CapabilityHelped,
		"capability_noise":  result.CapabilityNoise,
		"capability_denied": result.CapabilityDenied,
	}
	invocationID, err := s.Postgres.CreateToolInvocation(ctx, store.CreateToolInvocationParams{
		TaskID:        taskIDPtr,
		AgentType:     agentTypePtr,
		Entrypoint:    "local_bridge",
		Capability:    "local_bridge.run",
		InputPayload:  map[string]any{"bridge_id": bridgeID, "workspace_root": asString(task.Metadata["workspace_root"]), "tool_request": task.Metadata["tool_request"]},
		OutputPayload: output,
		Status:        strings.ToLower(strings.TrimSpace(result.Status)),
		DurationMS:    0,
		SourceRefs:    []domain.SourceRef{},
		ArtifactIDs:   []string{},
		ErrorMessage:  stringPtr(strings.TrimSpace(derefStringPtr(result.ErrorMessage))),
	})
	if err != nil {
		return nil, "", nil, err
	}

	artifactsToPersist := append([]map[string]any{}, result.Artifacts...)
	artifactsToPersist = append(artifactsToPersist, buildObjectiveIterationArtifacts(task, result)...)
	artifactsToPersist = append(artifactsToPersist, buildRepoWorkflowMemoryArtifacts(task, result)...)
	artifactIDs := make([]string, 0, 4+len(artifactsToPersist))
	appendTextArtifact := func(kind, title, content string) error {
		if strings.TrimSpace(content) == "" {
			return nil
		}
		text := truncateForAPI(content, 16000)
		id, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
			TaskID:       taskIDPtr,
			InvocationID: &invocationID,
			ArtifactType: kind,
			Title:        stringPtr(title),
			MediaType:    stringPtr("text/plain"),
			ContentText:  &text,
			Metadata:     map[string]any{"bridge_id": bridgeID},
		})
		if err != nil {
			return err
		}
		artifactIDs = append(artifactIDs, id)
		return nil
	}

	if err := appendTextArtifact("local_bridge_stdout", "Local bridge stdout", derefStringPtr(result.Stdout)); err != nil {
		return nil, "", nil, err
	}
	if err := appendTextArtifact("local_bridge_stderr", "Local bridge stderr", derefStringPtr(result.Stderr)); err != nil {
		return nil, "", nil, err
	}
	if err := appendTextArtifact("local_bridge_diff", "Local bridge diff", derefStringPtr(result.Diff)); err != nil {
		return nil, "", nil, err
	}
	if len(result.TestResults) > 0 {
		raw, err := json.MarshalIndent(result.TestResults, "", "  ")
		if err != nil {
			return nil, "", nil, err
		}
		if err := appendTextArtifact("local_bridge_test_results", "Local bridge test results", string(raw)); err != nil {
			return nil, "", nil, err
		}
	}
	for _, artifact := range artifactsToPersist {
		artifactMetadata := cloneMap(artifact)
		if artifactMetadata == nil {
			artifactMetadata = map[string]any{}
		}
		if task != nil {
			if task.InitiativeID != nil && strings.TrimSpace(*task.InitiativeID) != "" {
				artifactMetadata["initiative_id"] = strings.TrimSpace(*task.InitiativeID)
			}
			artifactMetadata["task_id"] = task.ID
			if workItemID := strings.TrimSpace(asString(task.Metadata["work_item_id"])); workItemID != "" {
				artifactMetadata["work_item_id"] = workItemID
			}
			if workItemKind := strings.TrimSpace(asString(task.Metadata["work_item_kind"])); workItemKind != "" {
				artifactMetadata["work_item_kind"] = workItemKind
			}
			if iteration := objectiveAsInt(task.Metadata["objective_iteration"], 0); iteration > 0 {
				artifactMetadata["iteration"] = iteration
			}
		}
		artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
			TaskID:       taskIDPtr,
			InvocationID: &invocationID,
			ArtifactType: firstNonEmptyString(strings.TrimSpace(asString(artifact["type"])), "local_bridge_artifact"),
			Title:        stringPtr(strings.TrimSpace(asString(artifact["title"]))),
			URI:          stringPtr(strings.TrimSpace(asString(artifact["path"]))),
			MediaType:    stringPtr(strings.TrimSpace(asString(artifact["media_type"]))),
			ContentText:  stringPtr(truncateForAPI(strings.TrimSpace(asString(artifact["content_text"])), 4000)),
			Metadata:     artifactMetadata,
		})
		if err != nil {
			return nil, "", nil, err
		}
		artifactIDs = append(artifactIDs, artifactID)
	}
	if err := s.Postgres.UpdateToolInvocationArtifactIDs(ctx, invocationID, artifactIDs); err != nil {
		return nil, "", nil, err
	}
	for _, artifactID := range artifactIDs {
		s.indexArtifact(ctx, artifactID)
	}

	results := map[string]any{
		"status":             strings.ToLower(strings.TrimSpace(result.Status)),
		"summary":            derefStringPtr(result.Summary),
		"stdout":             derefStringPtr(result.Stdout),
		"stderr":             derefStringPtr(result.Stderr),
		"exit_code":          result.ExitCode,
		"diff":               derefStringPtr(result.Diff),
		"changed_files":      result.ChangedFiles,
		"test_results":       result.TestResults,
		"review_decision":    derefStringPtr(result.ReviewDecision),
		"review_comments":    result.ReviewComments,
		"findings":           result.Findings,
		"artifacts":          artifactsToPersist,
		"error_message":      derefStringPtr(result.ErrorMessage),
		"tool_invocation_id": invocationID,
		"artifact_ids":       artifactIDs,
		"capability_usage":   result.CapabilityUsage,
		"capability_helped":  result.CapabilityHelped,
		"capability_noise":   result.CapabilityNoise,
		"capability_denied":  result.CapabilityDenied,
	}
	return results, invocationID, artifactIDs, nil
}

func buildRepoWorkflowMemoryArtifacts(task *domain.TaskResponse, result domain.LocalBridgeResultRequest) []map[string]any {
	if task == nil {
		return nil
	}
	metadata := task.Metadata
	projectRequest, _ := metadata["project_request"].(map[string]any)
	repoProfile := firstNonEmptyString(
		strings.TrimSpace(asString(projectRequest["repo_profile"])),
		strings.TrimSpace(asString(metadata["repo_profile"])),
		strings.TrimSpace(asString(metadata["repo_workflow"])),
	)
	if repoProfile == "" {
		return nil
	}
	agent := ""
	if task.AssignedAgent != nil {
		agent = strings.TrimSpace(string(*task.AssignedAgent))
	}
	if agent != string(domain.AgentTypeCoder) && agent != string(domain.AgentTypeReviewer) {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(result.Status))
	if status == "" || status == "waiting_approval" {
		return nil
	}
	workspaceRoot := strings.TrimSpace(asString(metadata["workspace_root"]))
	if workspaceRoot == "" && task.WorkspacePath != nil {
		workspaceRoot = strings.TrimSpace(*task.WorkspacePath)
	}
	projectRoot := strings.TrimSpace(asString(projectRequest["project_root"]))
	repoPath := workspaceRoot
	if projectRoot != "" && projectRoot != "." {
		if filepath.IsAbs(projectRoot) {
			repoPath = projectRoot
		} else if workspaceRoot != "" {
			repoPath = filepath.Join(workspaceRoot, projectRoot)
		}
	}
	repoName := firstNonEmptyString(strings.TrimSpace(asString(projectRequest["project_name"])), filepath.Base(repoPath), "repo")
	stage := "patch_apply"
	if agent == string(domain.AgentTypeReviewer) {
		stage = "review"
	}
	outcome := "failed"
	if status == "success" {
		outcome = "trusted"
	}
	resolvedCommit := gitOutput(repoPath, "rev-parse", "HEAD")
	resolvedBranch := gitOutput(repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	summary := strings.TrimSpace(derefStringPtr(result.Summary))
	if summary == "" {
		summary = strings.TrimSpace(task.Description)
	}
	errorMessage := strings.TrimSpace(derefStringPtr(result.ErrorMessage))
	casePayload := map[string]any{
		"memory_kind":         "repo_workflow_case",
		"workflow":            firstNonEmptyString(strings.TrimSpace(asString(metadata["repo_workflow"])), "repo_workflow_v1"),
		"repo_profile":        repoProfile,
		"runtime_or_stack":    strings.TrimSpace(asString(projectRequest["runtime_or_stack"])),
		"language":            strings.TrimSpace(asString(projectRequest["language"])),
		"framework":           strings.TrimSpace(asString(projectRequest["framework"])),
		"problem_domain":      strings.TrimSpace(asString(projectRequest["problem_domain"])),
		"error_class":         strings.TrimSpace(asString(projectRequest["error_class"])),
		"fix_pattern":         strings.TrimSpace(asString(projectRequest["fix_pattern"])),
		"validation_pattern":  strings.TrimSpace(asString(projectRequest["validation_pattern"])),
		"repository_name":     repoName,
		"repository_url":      strings.TrimSpace(asString(projectRequest["repository_url"])),
		"benchmark_case_id":   strings.TrimSpace(asString(metadata["benchmark_case_id"])),
		"benchmark_case_type": strings.TrimSpace(asString(metadata["benchmark_case_type"])),
		"benchmark_league":    strings.TrimSpace(asString(metadata["benchmark_league"])),
		"sequence_id":         strings.TrimSpace(asString(metadata["sequence_id"])),
		"sequence_position":   strings.TrimSpace(asString(metadata["sequence_position"])),
		"default_branch":      strings.TrimSpace(asString(projectRequest["default_branch"])),
		"resolved_branch":     resolvedBranch,
		"resolved_commit":     resolvedCommit,
		"workspace_root":      workspaceRoot,
		"project_root":        firstNonEmptyString(projectRoot, "."),
		"initiative_id":       stringValue(task.InitiativeID),
		"task_id":             task.ID,
		"assigned_agent":      agent,
		"execution_stage":     stage,
		"result_status":       status,
		"outcome":             outcome,
		"summary":             summary,
		"error_message":       errorMessage,
		"changed_files":       result.ChangedFiles,
		"diff_present":        strings.TrimSpace(derefStringPtr(result.Diff)) != "",
		"test_command":        anyStringSliceHTTPDefault(projectRequest["test_command"], []string{}),
		"expected_files":      anyStringSliceHTTPDefault(projectRequest["expected_files"], []string{}),
		"test_results":        nonNilMapHTTP(result.TestResults),
		"definition_of_done":  strings.TrimSpace(asString(metadata["definition_of_done"])),
	}
	caseRaw, _ := json.MarshalIndent(casePayload, "", "  ")
	lesson := renderRepoWorkflowLesson(casePayload)
	artifacts := []map[string]any{
		{
			"type":                "repo_workflow_case",
			"title":               fmt.Sprintf("Repo workflow case: %s %s", repoName, stage),
			"media_type":          "application/json",
			"content_text":        string(caseRaw),
			"repo_profile":        repoProfile,
			"runtime_or_stack":    casePayload["runtime_or_stack"],
			"language":            casePayload["language"],
			"framework":           casePayload["framework"],
			"problem_domain":      casePayload["problem_domain"],
			"error_class":         casePayload["error_class"],
			"fix_pattern":         casePayload["fix_pattern"],
			"validation_pattern":  casePayload["validation_pattern"],
			"repository_url":      casePayload["repository_url"],
			"benchmark_case_id":   casePayload["benchmark_case_id"],
			"benchmark_case_type": casePayload["benchmark_case_type"],
			"benchmark_league":    casePayload["benchmark_league"],
			"sequence_id":         casePayload["sequence_id"],
			"sequence_position":   casePayload["sequence_position"],
			"workflow":            casePayload["workflow"],
			"execution_stage":     casePayload["execution_stage"],
			"outcome":             outcome,
			"memory_kind":         "case",
		},
		{
			"type":                "repo_workflow_lesson",
			"title":               fmt.Sprintf("Repo workflow lesson: %s %s", repoName, stage),
			"media_type":          "text/markdown",
			"content_text":        lesson,
			"repo_profile":        repoProfile,
			"runtime_or_stack":    casePayload["runtime_or_stack"],
			"language":            casePayload["language"],
			"framework":           casePayload["framework"],
			"problem_domain":      casePayload["problem_domain"],
			"error_class":         casePayload["error_class"],
			"fix_pattern":         casePayload["fix_pattern"],
			"validation_pattern":  casePayload["validation_pattern"],
			"repository_url":      casePayload["repository_url"],
			"benchmark_case_id":   casePayload["benchmark_case_id"],
			"benchmark_case_type": casePayload["benchmark_case_type"],
			"benchmark_league":    casePayload["benchmark_league"],
			"sequence_id":         casePayload["sequence_id"],
			"sequence_position":   casePayload["sequence_position"],
			"workflow":            casePayload["workflow"],
			"execution_stage":     casePayload["execution_stage"],
			"outcome":             outcome,
			"memory_kind":         "lesson",
		},
	}
	if benchmarkCaseID := strings.TrimSpace(asString(casePayload["benchmark_case_id"])); benchmarkCaseID != "" {
		benchmarkCaseRaw, _ := json.MarshalIndent(map[string]any{
			"benchmark_case_id":   benchmarkCaseID,
			"benchmark_case_type": casePayload["benchmark_case_type"],
			"benchmark_league":    casePayload["benchmark_league"],
			"sequence_id":         casePayload["sequence_id"],
			"sequence_position":   casePayload["sequence_position"],
			"repository_name":     repoName,
			"repository_url":      casePayload["repository_url"],
			"repo_profile":        repoProfile,
			"runtime_or_stack":    casePayload["runtime_or_stack"],
			"language":            casePayload["language"],
			"framework":           casePayload["framework"],
			"problem_domain":      casePayload["problem_domain"],
			"error_class":         casePayload["error_class"],
			"fix_pattern":         casePayload["fix_pattern"],
			"validation_pattern":  casePayload["validation_pattern"],
			"default_branch":      casePayload["default_branch"],
		}, "", "  ")
		benchmarkRunRaw, _ := json.MarshalIndent(map[string]any{
			"benchmark_case_id":   benchmarkCaseID,
			"benchmark_case_type": casePayload["benchmark_case_type"],
			"workflow":            casePayload["workflow"],
			"repository_name":     repoName,
			"repository_url":      casePayload["repository_url"],
			"repo_profile":        repoProfile,
			"resolved_branch":     resolvedBranch,
			"resolved_commit":     resolvedCommit,
			"task_id":             task.ID,
			"initiative_id":       stringValue(task.InitiativeID),
			"assigned_agent":      agent,
			"execution_stage":     stage,
			"result_status":       status,
			"outcome":             outcome,
			"benchmark_league":    casePayload["benchmark_league"],
			"sequence_id":         casePayload["sequence_id"],
			"sequence_position":   casePayload["sequence_position"],
			"changed_files":       result.ChangedFiles,
			"test_command":        anyStringSliceHTTPDefault(projectRequest["test_command"], []string{}),
			"test_results":        nonNilMapHTTP(result.TestResults),
			"diff_present":        strings.TrimSpace(derefStringPtr(result.Diff)) != "",
		}, "", "  ")
		artifacts = append(artifacts,
			map[string]any{
				"type":                "benchmark_case",
				"title":               fmt.Sprintf("Benchmark case: %s", benchmarkCaseID),
				"media_type":          "application/json",
				"content_text":        string(benchmarkCaseRaw),
				"repo_profile":        repoProfile,
				"runtime_or_stack":    casePayload["runtime_or_stack"],
				"language":            casePayload["language"],
				"framework":           casePayload["framework"],
				"problem_domain":      casePayload["problem_domain"],
				"error_class":         casePayload["error_class"],
				"fix_pattern":         casePayload["fix_pattern"],
				"validation_pattern":  casePayload["validation_pattern"],
				"repository_url":      casePayload["repository_url"],
				"benchmark_case_id":   benchmarkCaseID,
				"benchmark_case_type": casePayload["benchmark_case_type"],
				"benchmark_league":    casePayload["benchmark_league"],
				"sequence_id":         casePayload["sequence_id"],
				"sequence_position":   casePayload["sequence_position"],
				"workflow":            casePayload["workflow"],
				"outcome":             outcome,
				"memory_kind":         "benchmark_case",
			},
			map[string]any{
				"type":                "benchmark_run",
				"title":               fmt.Sprintf("Benchmark run: %s %s", benchmarkCaseID, stage),
				"media_type":          "application/json",
				"content_text":        string(benchmarkRunRaw),
				"repo_profile":        repoProfile,
				"runtime_or_stack":    casePayload["runtime_or_stack"],
				"language":            casePayload["language"],
				"framework":           casePayload["framework"],
				"problem_domain":      casePayload["problem_domain"],
				"error_class":         casePayload["error_class"],
				"fix_pattern":         casePayload["fix_pattern"],
				"validation_pattern":  casePayload["validation_pattern"],
				"repository_url":      casePayload["repository_url"],
				"benchmark_case_id":   benchmarkCaseID,
				"benchmark_case_type": casePayload["benchmark_case_type"],
				"benchmark_league":    casePayload["benchmark_league"],
				"sequence_id":         casePayload["sequence_id"],
				"sequence_position":   casePayload["sequence_position"],
				"workflow":            casePayload["workflow"],
				"execution_stage":     casePayload["execution_stage"],
				"outcome":             outcome,
				"memory_kind":         "benchmark_run",
			},
		)
	}
	return artifacts
}

func buildObjectiveIterationArtifacts(task *domain.TaskResponse, result domain.LocalBridgeResultRequest) []map[string]any {
	if task == nil || task.InitiativeID == nil || !objectiveAsBool(task.Metadata["objective_entrypoint"]) {
		return nil
	}
	status := objectiveEffectiveResultStatus(task, result)
	if status == "" || status == "waiting_approval" {
		return nil
	}
	metadata := task.Metadata
	iteration := objectiveIteration(metadata)
	maxIterations := objectiveMaxIterations(metadata)
	workItemKind := strings.TrimSpace(asString(metadata["work_item_kind"]))
	contract, _ := decodeObjectiveExecutionContract(metadata["execution_contract"])
	objectiveTitle := firstNonEmptyString(strings.TrimSpace(contract.Title), strings.TrimSpace(asString(metadata["objective_title"])))
	normalizedObjective := firstNonEmptyString(strings.TrimSpace(contract.NormalizedObjective), strings.TrimSpace(asString(metadata["initiative_goal"])))
	workspaceRoot := firstNonEmptyString(strings.TrimSpace(contract.WorkspaceRoot), strings.TrimSpace(asString(metadata["workspace_root"])))
	summary := strings.TrimSpace(derefStringPtr(result.Summary))
	if summary == "" {
		summary = strings.TrimSpace(task.Description)
	}
	errorMessage := strings.TrimSpace(derefStringPtr(result.ErrorMessage))
	reviewDecision := strings.TrimSpace(derefStringPtr(result.ReviewDecision))
	needsRepair := shouldStartObjectiveRepairCycle(task, result)
	repairFeedback := firstNonEmptyString(
		strings.TrimSpace(asString(metadata["objective_repair_feedback"])),
		objectiveRepairFeedback(result),
	)
	payload := map[string]any{
		"memory_kind":            "objective_iteration",
		"initiative_id":          stringValue(task.InitiativeID),
		"task_id":                task.ID,
		"objective_title":        objectiveTitle,
		"normalized_objective":   normalizedObjective,
		"workspace_root":         workspaceRoot,
		"iteration":              iteration,
		"max_iterations":         maxIterations,
		"work_item_kind":         workItemKind,
		"result_status":          status,
		"raw_result_status":      strings.ToLower(strings.TrimSpace(result.Status)),
		"review_decision":        reviewDecision,
		"summary":                summary,
		"error_message":          errorMessage,
		"changed_files":          result.ChangedFiles,
		"diff_present":           strings.TrimSpace(derefStringPtr(result.Diff)) != "",
		"test_results":           nonNilMapHTTP(result.TestResults),
		"review_comments":        result.ReviewComments,
		"findings":               result.Findings,
		"validation_commands":    anyStringSliceHTTPDefault(metadata["validation_commands"], []string{}),
		"definition_of_done":     strings.TrimSpace(asString(metadata["definition_of_done"])),
		"needs_repair_cycle":     needsRepair,
		"repair_feedback":        repairFeedback,
		"objective_iteration_id": fmt.Sprintf("%s:%d:%s", stringValue(task.InitiativeID), iteration, workItemKind),
	}
	raw, _ := json.MarshalIndent(payload, "", "  ")
	statusSnapshot := buildObjectiveStatusSnapshot(task, result, payload, needsRepair, repairFeedback)
	statusSnapshotRaw, _ := json.MarshalIndent(statusSnapshot, "", "  ")
	artifacts := []map[string]any{
		{
			"type":                 "objective_iteration_summary",
			"title":                fmt.Sprintf("Objective iteration %d %s", iteration, workItemKind),
			"media_type":           "application/json",
			"content_text":         string(raw),
			"initiative_id":        payload["initiative_id"],
			"objective_title":      objectiveTitle,
			"normalized_objective": normalizedObjective,
			"iteration":            iteration,
			"max_iterations":       maxIterations,
			"work_item_kind":       workItemKind,
			"result_status":        status,
			"review_decision":      reviewDecision,
			"needs_repair_cycle":   needsRepair,
			"memory_kind":          "objective_iteration",
			"findings":             result.Findings,
		},
		{
			"type":                     "objective_status_snapshot",
			"title":                    fmt.Sprintf("Objective status snapshot %d %s", iteration, workItemKind),
			"media_type":               "application/json",
			"content_text":             string(statusSnapshotRaw),
			"initiative_id":            payload["initiative_id"],
			"objective_title":          objectiveTitle,
			"normalized_objective":     normalizedObjective,
			"iteration":                iteration,
			"max_iterations":           maxIterations,
			"work_item_kind":           workItemKind,
			"result_status":            status,
			"review_decision":          reviewDecision,
			"needs_repair_cycle":       needsRepair,
			"next_expected_action":     statusSnapshot.NextExpectedAction,
			"remaining_retries":        statusSnapshot.RemainingRetries,
			"stop_reason":              statusSnapshot.StopReason,
			"blocker_reason":           statusSnapshot.BlockerReason,
			"time_budget_seconds":      statusSnapshot.TimeBudgetSeconds,
			"remaining_budget_seconds": statusSnapshot.RemainingBudgetSeconds,
			"deadline_at":              statusSnapshot.DeadlineAt,
			"memory_kind":              "objective_status_snapshot",
			"findings":                 result.Findings,
		},
	}
	if needsRepair {
		signal := map[string]any{
			"initiative_id":        payload["initiative_id"],
			"task_id":              task.ID,
			"iteration":            iteration,
			"max_iterations":       maxIterations,
			"work_item_kind":       workItemKind,
			"repair_feedback":      repairFeedback,
			"review_decision":      reviewDecision,
			"next_expected_action": "replan",
		}
		signalRaw, _ := json.MarshalIndent(signal, "", "  ")
		artifacts = append(artifacts, map[string]any{
			"type":               "objective_repair_signal",
			"title":              fmt.Sprintf("Objective repair signal %d", iteration),
			"media_type":         "application/json",
			"content_text":       string(signalRaw),
			"initiative_id":      payload["initiative_id"],
			"iteration":          iteration,
			"work_item_kind":     workItemKind,
			"needs_repair_cycle": true,
			"memory_kind":        "objective_repair_signal",
		})
	}
	return artifacts
}

func objectiveEffectiveResultStatus(task *domain.TaskResponse, result domain.LocalBridgeResultRequest) string {
	status := strings.ToLower(strings.TrimSpace(result.Status))
	if task != nil && objectiveAsBool(task.Metadata["objective_baseline_validation"]) && status != "" && status != "waiting_approval" {
		return "success"
	}
	return status
}

func buildObjectiveStatusSnapshot(task *domain.TaskResponse, result domain.LocalBridgeResultRequest, payload map[string]any, needsRepair bool, repairFeedback string) domain.ObjectiveStatusSnapshot {
	iteration := objectiveAsInt(payload["iteration"], 1)
	maxIterations := objectiveAsInt(payload["max_iterations"], iteration)
	if maxIterations < iteration {
		maxIterations = iteration
	}
	remainingRetries := maxIterations - iteration
	if remainingRetries < 0 {
		remainingRetries = 0
	}
	workItemKind := strings.TrimSpace(asString(payload["work_item_kind"]))
	resultStatus := strings.TrimSpace(asString(payload["result_status"]))
	reviewDecision := strings.TrimSpace(asString(payload["review_decision"]))
	nextExpectedAction := objectiveNextExpectedAction(task.Metadata, workItemKind, resultStatus, reviewDecision, needsRepair)
	blockerReason := ""
	stopReason := ""
	budgetSeconds, remainingBudgetSeconds, deadline, hasBudget := objectiveRemainingBudgetSeconds(task.Metadata, time.Now().UTC())
	if nextExpectedAction == "block_initiative" {
		stopReason = objectiveStopReason(task.Metadata, result, iteration, maxIterations)
		blockerReason = objectiveBlockerReason(task.Metadata, result, repairFeedback, iteration, maxIterations, stopReason, budgetSeconds, deadline)
	}
	snapshot := domain.ObjectiveStatusSnapshot{
		InitiativeID:        strings.TrimSpace(asString(payload["initiative_id"])),
		ObjectiveTitle:      strings.TrimSpace(asString(payload["objective_title"])),
		NormalizedObjective: strings.TrimSpace(asString(payload["normalized_objective"])),
		CurrentPhase:        string(domain.InitiativePhaseExecution),
		Iteration:           iteration,
		MaxIterations:       maxIterations,
		RemainingRetries:    remainingRetries,
		WorkItemKind:        workItemKind,
		ResultStatus:        resultStatus,
		ReviewDecision:      reviewDecision,
		NeedsRepairCycle:    needsRepair,
		NextExpectedAction:  nextExpectedAction,
		StopReason:          stopReason,
		BlockerReason:       blockerReason,
		ChangedFiles:        result.ChangedFiles,
		ValidationCommands:  anyStringSliceHTTPDefault(task.Metadata["validation_commands"], []string{}),
		TestResults:         nonNilMapHTTP(result.TestResults),
		Findings:            result.Findings,
	}
	if hasBudget {
		snapshot.TimeBudgetSeconds = budgetSeconds
		snapshot.RemainingBudgetSeconds = remainingBudgetSeconds
		snapshot.DeadlineAt = deadline.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func objectiveNextExpectedAction(metadata map[string]any, workItemKind, resultStatus, reviewDecision string, needsRepair bool) string {
	if needsRepair {
		return "replan"
	}
	if strings.EqualFold(resultStatus, "success") {
		if explicit := strings.TrimSpace(asString(metadata["objective_next_action_after_success"])); explicit != "" {
			return explicit
		}
		switch workItemKind {
		case string(domain.WorkItemKindResearch), string(domain.WorkItemKindReplan):
			return "edit"
		case string(domain.WorkItemKindAnalyze):
			return "edit"
		case string(domain.WorkItemKindEdit):
			return "validate"
		case string(domain.WorkItemKindValidate):
			return "review"
		case string(domain.WorkItemKindReview):
			if strings.EqualFold(reviewDecision, "approved") {
				return "close_initiative"
			}
		}
		return "await_orchestrator"
	}
	return "block_initiative"
}

func objectiveStopReason(metadata map[string]any, result domain.LocalBridgeResultRequest, iteration, maxIterations int) string {
	if objectiveTimeBudgetExceeded(metadata, time.Now().UTC()) {
		return "time_budget_exhausted"
	}
	if iteration >= maxIterations {
		return "retry_budget_exhausted"
	}
	return "execution_failed"
}

func objectiveBlockerReason(metadata map[string]any, result domain.LocalBridgeResultRequest, repairFeedback string, iteration, maxIterations int, stopReason string, budgetSeconds int, deadline time.Time) string {
	switch stopReason {
	case "time_budget_exhausted":
		return objectiveTimeBudgetExceededReason(budgetSeconds, deadline)
	case "retry_budget_exhausted":
		return fmt.Sprintf("Maximum repair iterations exhausted after %s.", objectiveFailureLabel(result))
	}
	if strings.TrimSpace(repairFeedback) != "" {
		return strings.TrimSpace(repairFeedback)
	}
	return "Objective execution failed without a remaining autonomous repair path."
}

func objectiveFailureLabel(result domain.LocalBridgeResultRequest) string {
	switch {
	case strings.EqualFold(derefStringPtr(result.ReviewDecision), "changes_requested"):
		return "review requested changes"
	case strings.TrimSpace(derefStringPtr(result.ErrorMessage)) != "":
		return strings.TrimSpace(derefStringPtr(result.ErrorMessage))
	case strings.TrimSpace(derefStringPtr(result.Summary)) != "":
		return strings.TrimSpace(derefStringPtr(result.Summary))
	default:
		return "an unresolved failure"
	}
}

func renderRepoWorkflowLesson(casePayload map[string]any) string {
	lines := []string{
		"# Repo Workflow Lesson",
		"",
		fmt.Sprintf("- Repository: %s", asString(casePayload["repository_name"])),
		fmt.Sprintf("- Workflow: %s", asString(casePayload["workflow"])),
		fmt.Sprintf("- Profile: %s", asString(casePayload["repo_profile"])),
		fmt.Sprintf("- Stage: %s", asString(casePayload["execution_stage"])),
		fmt.Sprintf("- Outcome: %s", asString(casePayload["outcome"])),
		fmt.Sprintf("- Summary: %s", asString(casePayload["summary"])),
	}
	if branch := strings.TrimSpace(asString(casePayload["resolved_branch"])); branch != "" {
		lines = append(lines, fmt.Sprintf("- Branch: %s", branch))
	}
	if commit := strings.TrimSpace(asString(casePayload["resolved_commit"])); commit != "" {
		lines = append(lines, fmt.Sprintf("- Commit: %s", commit))
	}
	if changed := anyStringSliceHTTPDefault(casePayload["changed_files"], []string{}); len(changed) > 0 {
		lines = append(lines, fmt.Sprintf("- Changed files: %s", strings.Join(changed, ", ")))
	}
	if testCommand := anyStringSliceHTTPDefault(casePayload["test_command"], []string{}); len(testCommand) > 0 {
		lines = append(lines, fmt.Sprintf("- Validation command: `%s`", strings.Join(testCommand, " ")))
	}
	if errorMessage := strings.TrimSpace(asString(casePayload["error_message"])); errorMessage != "" {
		lines = append(lines, fmt.Sprintf("- Failure signal: %s", errorMessage))
	}
	lines = append(lines, "", "## Reuse")
	if asString(casePayload["outcome"]) == "trusted" {
		lines = append(lines, "- Reuse this case as a trusted precedent for the same repo profile and stage.")
	} else {
		lines = append(lines, "- Reuse this case as a warning: inspect the failure signal before retrying the same stage.")
	}
	return strings.Join(lines, "\n")
}

func gitOutput(dir string, args ...string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || len(args) == 0 {
		return ""
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	raw, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func anyStringSliceHTTP(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text := strings.TrimSpace(asString(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out, len(out) > 0
	default:
		return nil, false
	}
}

func anyStringSliceHTTPDefault(value any, fallback []string) []string {
	if items, ok := anyStringSliceHTTP(value); ok {
		return items
	}
	return fallback
}

func nonNilMapHTTP(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
