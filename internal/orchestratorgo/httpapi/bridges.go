package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

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
		ttl := 45
		if body.LeaseTTLSeconds != nil && *body.LeaseTTLSeconds > 0 {
			ttl = *body.LeaseTTLSeconds
		}
		if err := s.Postgres.TouchLocalBridgeTaskLease(r.Context(), strings.TrimSpace(*body.CurrentTaskID), chi.URLParam(r, "bridgeID"), ttl, "active"); err != nil {
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
	claim, err := s.Postgres.ClaimNextLocalBridgeTask(r.Context(), chi.URLParam(r, "bridgeID"))
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
		_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "waiting_approval")
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
		_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "completed")
		if completed, err := s.Postgres.CompleteTask(r.Context(), taskID, "bridge:"+bridgeID, firstNonEmptyString(derefStringPtr(body.Summary), "Local bridge execution completed"), results); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		} else if completed != nil {
			s.indexTask(r.Context(), completed.ID)
		}
		s.reconcileRootTask(r.Context(), taskID)
	default:
		_ = s.Postgres.FinalizeLocalBridgeTaskLease(r.Context(), taskID, bridgeID, "failed")
		errorMessage := firstNonEmptyString(derefStringPtr(body.ErrorMessage), derefStringPtr(body.Stderr), derefStringPtr(body.Stdout), "Local bridge execution failed")
		if failed, err := s.Postgres.FailTask(r.Context(), taskID, "bridge:"+bridgeID, errorMessage, errorMessage, results); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		} else if failed != nil {
			s.indexTask(r.Context(), failed.ID)
		}
		s.reconcileRootTask(r.Context(), taskID)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":             "accepted",
		"tool_invocation_id": invocationID,
		"artifact_ids":       artifactIDs,
	})
}

func (s *Server) persistLocalBridgeExecution(ctx context.Context, bridgeID, taskID string, task *domain.TaskResponse, result domain.LocalBridgeResultRequest) (map[string]any, string, []string, error) {
	taskIDPtr := &taskID
	agentType := string(domain.AgentTypeCoder)
	if task.AssignedAgent != nil {
		agentType = string(*task.AssignedAgent)
	}
	agentTypePtr := &agentType
	output := map[string]any{
		"summary":                      strings.TrimSpace(derefStringPtr(result.Summary)),
		"stdout":                       derefStringPtr(result.Stdout),
		"stderr":                       derefStringPtr(result.Stderr),
		"exit_code":                    result.ExitCode,
		"diff":                         derefStringPtr(result.Diff),
		"changed_files":                result.ChangedFiles,
		"test_results":                 result.TestResults,
		"semantic_context_sources":     result.SemanticContextSources,
		"semantic_context_chunk_count": result.SemanticContextChunkCount,
		"semantic_context_hits":        result.SemanticContextHits,
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
		artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
			TaskID:       taskIDPtr,
			InvocationID: &invocationID,
			ArtifactType: firstNonEmptyString(strings.TrimSpace(asString(artifact["type"])), "local_bridge_artifact"),
			Title:        stringPtr(strings.TrimSpace(asString(artifact["title"]))),
			URI:          stringPtr(strings.TrimSpace(asString(artifact["path"]))),
			MediaType:    stringPtr(strings.TrimSpace(asString(artifact["media_type"]))),
			ContentText:  stringPtr(truncateForAPI(strings.TrimSpace(asString(artifact["content_text"])), 4000)),
			Metadata:     artifact,
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
		"status":                       strings.ToLower(strings.TrimSpace(result.Status)),
		"summary":                      derefStringPtr(result.Summary),
		"stdout":                       derefStringPtr(result.Stdout),
		"stderr":                       derefStringPtr(result.Stderr),
		"exit_code":                    result.ExitCode,
		"diff":                         derefStringPtr(result.Diff),
		"changed_files":                result.ChangedFiles,
		"test_results":                 result.TestResults,
		"artifacts":                    artifactsToPersist,
		"error_message":                derefStringPtr(result.ErrorMessage),
		"tool_invocation_id":           invocationID,
		"artifact_ids":                 artifactIDs,
		"semantic_context_sources":     result.SemanticContextSources,
		"semantic_context_chunk_count": result.SemanticContextChunkCount,
		"semantic_context_hits":        result.SemanticContextHits,
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
		"memory_kind":               "repo_workflow_case",
		"workflow":                  firstNonEmptyString(strings.TrimSpace(asString(metadata["repo_workflow"])), "repo_workflow_v1"),
		"repo_profile":              repoProfile,
		"runtime_or_stack":          strings.TrimSpace(asString(projectRequest["runtime_or_stack"])),
		"language":                  strings.TrimSpace(asString(projectRequest["language"])),
		"framework":                 strings.TrimSpace(asString(projectRequest["framework"])),
		"problem_domain":            strings.TrimSpace(asString(projectRequest["problem_domain"])),
		"error_class":               strings.TrimSpace(asString(projectRequest["error_class"])),
		"fix_pattern":               strings.TrimSpace(asString(projectRequest["fix_pattern"])),
		"validation_pattern":        strings.TrimSpace(asString(projectRequest["validation_pattern"])),
		"repository_name":           repoName,
		"repository_url":            strings.TrimSpace(asString(projectRequest["repository_url"])),
		"benchmark_case_id":         strings.TrimSpace(asString(metadata["benchmark_case_id"])),
		"benchmark_case_type":       strings.TrimSpace(asString(metadata["benchmark_case_type"])),
		"benchmark_league":          strings.TrimSpace(asString(metadata["benchmark_league"])),
		"sequence_id":               strings.TrimSpace(asString(metadata["sequence_id"])),
		"sequence_position":         strings.TrimSpace(asString(metadata["sequence_position"])),
		"benchmark_memory_mode":     strings.TrimSpace(asString(metadata["benchmark_memory_mode"])),
		"benchmark_memory_strategy": strings.TrimSpace(asString(metadata["benchmark_memory_strategy"])),
		"default_branch":            strings.TrimSpace(asString(projectRequest["default_branch"])),
		"resolved_branch":           resolvedBranch,
		"resolved_commit":           resolvedCommit,
		"workspace_root":            workspaceRoot,
		"project_root":              firstNonEmptyString(projectRoot, "."),
		"initiative_id":             stringValue(task.InitiativeID),
		"task_id":                   task.ID,
		"assigned_agent":            agent,
		"execution_stage":           stage,
		"result_status":             status,
		"outcome":                   outcome,
		"summary":                   summary,
		"error_message":             errorMessage,
		"changed_files":             result.ChangedFiles,
		"diff_present":              strings.TrimSpace(derefStringPtr(result.Diff)) != "",
		"test_command":              anyStringSliceHTTPDefault(projectRequest["test_command"], []string{}),
		"expected_files":            anyStringSliceHTTPDefault(projectRequest["expected_files"], []string{}),
		"test_results":              nonNilMapHTTP(result.TestResults),
		"definition_of_done":        strings.TrimSpace(asString(metadata["definition_of_done"])),
	}
	caseRaw, _ := json.MarshalIndent(casePayload, "", "  ")
	lesson := renderRepoWorkflowLesson(casePayload)
	artifacts := []map[string]any{
		{
			"type":                      "repo_workflow_case",
			"title":                     fmt.Sprintf("Repo workflow case: %s %s", repoName, stage),
			"media_type":                "application/json",
			"content_text":              string(caseRaw),
			"repo_profile":              repoProfile,
			"runtime_or_stack":          casePayload["runtime_or_stack"],
			"language":                  casePayload["language"],
			"framework":                 casePayload["framework"],
			"problem_domain":            casePayload["problem_domain"],
			"error_class":               casePayload["error_class"],
			"fix_pattern":               casePayload["fix_pattern"],
			"validation_pattern":        casePayload["validation_pattern"],
			"repository_url":            casePayload["repository_url"],
			"benchmark_case_id":         casePayload["benchmark_case_id"],
			"benchmark_case_type":       casePayload["benchmark_case_type"],
			"benchmark_league":          casePayload["benchmark_league"],
			"sequence_id":               casePayload["sequence_id"],
			"sequence_position":         casePayload["sequence_position"],
			"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
			"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
			"workflow":                  casePayload["workflow"],
			"execution_stage":           casePayload["execution_stage"],
			"outcome":                   outcome,
			"memory_kind":               "case",
		},
		{
			"type":                      "repo_workflow_lesson",
			"title":                     fmt.Sprintf("Repo workflow lesson: %s %s", repoName, stage),
			"media_type":                "text/markdown",
			"content_text":              lesson,
			"repo_profile":              repoProfile,
			"runtime_or_stack":          casePayload["runtime_or_stack"],
			"language":                  casePayload["language"],
			"framework":                 casePayload["framework"],
			"problem_domain":            casePayload["problem_domain"],
			"error_class":               casePayload["error_class"],
			"fix_pattern":               casePayload["fix_pattern"],
			"validation_pattern":        casePayload["validation_pattern"],
			"repository_url":            casePayload["repository_url"],
			"benchmark_case_id":         casePayload["benchmark_case_id"],
			"benchmark_case_type":       casePayload["benchmark_case_type"],
			"benchmark_league":          casePayload["benchmark_league"],
			"sequence_id":               casePayload["sequence_id"],
			"sequence_position":         casePayload["sequence_position"],
			"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
			"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
			"workflow":                  casePayload["workflow"],
			"execution_stage":           casePayload["execution_stage"],
			"outcome":                   outcome,
			"memory_kind":               "lesson",
		},
	}
	if benchmarkCaseID := strings.TrimSpace(asString(casePayload["benchmark_case_id"])); benchmarkCaseID != "" {
		benchmarkCaseRaw, _ := json.MarshalIndent(map[string]any{
			"benchmark_case_id":         benchmarkCaseID,
			"benchmark_case_type":       casePayload["benchmark_case_type"],
			"benchmark_league":          casePayload["benchmark_league"],
			"sequence_id":               casePayload["sequence_id"],
			"sequence_position":         casePayload["sequence_position"],
			"repository_name":           repoName,
			"repository_url":            casePayload["repository_url"],
			"repo_profile":              repoProfile,
			"runtime_or_stack":          casePayload["runtime_or_stack"],
			"language":                  casePayload["language"],
			"framework":                 casePayload["framework"],
			"problem_domain":            casePayload["problem_domain"],
			"error_class":               casePayload["error_class"],
			"fix_pattern":               casePayload["fix_pattern"],
			"validation_pattern":        casePayload["validation_pattern"],
			"default_branch":            casePayload["default_branch"],
			"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
			"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
		}, "", "  ")
		benchmarkRunRaw, _ := json.MarshalIndent(map[string]any{
			"benchmark_case_id":         benchmarkCaseID,
			"benchmark_case_type":       casePayload["benchmark_case_type"],
			"workflow":                  casePayload["workflow"],
			"repository_name":           repoName,
			"repository_url":            casePayload["repository_url"],
			"repo_profile":              repoProfile,
			"resolved_branch":           resolvedBranch,
			"resolved_commit":           resolvedCommit,
			"task_id":                   task.ID,
			"initiative_id":             stringValue(task.InitiativeID),
			"assigned_agent":            agent,
			"execution_stage":           stage,
			"result_status":             status,
			"outcome":                   outcome,
			"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
			"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
			"benchmark_league":          casePayload["benchmark_league"],
			"sequence_id":               casePayload["sequence_id"],
			"sequence_position":         casePayload["sequence_position"],
			"changed_files":             result.ChangedFiles,
			"test_command":              anyStringSliceHTTPDefault(projectRequest["test_command"], []string{}),
			"test_results":              nonNilMapHTTP(result.TestResults),
			"diff_present":              strings.TrimSpace(derefStringPtr(result.Diff)) != "",
		}, "", "  ")
		artifacts = append(artifacts,
			map[string]any{
				"type":                      "benchmark_case",
				"title":                     fmt.Sprintf("Benchmark case: %s", benchmarkCaseID),
				"media_type":                "application/json",
				"content_text":              string(benchmarkCaseRaw),
				"repo_profile":              repoProfile,
				"runtime_or_stack":          casePayload["runtime_or_stack"],
				"language":                  casePayload["language"],
				"framework":                 casePayload["framework"],
				"problem_domain":            casePayload["problem_domain"],
				"error_class":               casePayload["error_class"],
				"fix_pattern":               casePayload["fix_pattern"],
				"validation_pattern":        casePayload["validation_pattern"],
				"repository_url":            casePayload["repository_url"],
				"benchmark_case_id":         benchmarkCaseID,
				"benchmark_case_type":       casePayload["benchmark_case_type"],
				"benchmark_league":          casePayload["benchmark_league"],
				"sequence_id":               casePayload["sequence_id"],
				"sequence_position":         casePayload["sequence_position"],
				"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
				"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
				"workflow":                  casePayload["workflow"],
				"outcome":                   outcome,
				"memory_kind":               "benchmark_case",
			},
			map[string]any{
				"type":                      "benchmark_run",
				"title":                     fmt.Sprintf("Benchmark run: %s %s", benchmarkCaseID, stage),
				"media_type":                "application/json",
				"content_text":              string(benchmarkRunRaw),
				"repo_profile":              repoProfile,
				"runtime_or_stack":          casePayload["runtime_or_stack"],
				"language":                  casePayload["language"],
				"framework":                 casePayload["framework"],
				"problem_domain":            casePayload["problem_domain"],
				"error_class":               casePayload["error_class"],
				"fix_pattern":               casePayload["fix_pattern"],
				"validation_pattern":        casePayload["validation_pattern"],
				"repository_url":            casePayload["repository_url"],
				"benchmark_case_id":         benchmarkCaseID,
				"benchmark_case_type":       casePayload["benchmark_case_type"],
				"benchmark_league":          casePayload["benchmark_league"],
				"sequence_id":               casePayload["sequence_id"],
				"sequence_position":         casePayload["sequence_position"],
				"benchmark_memory_mode":     casePayload["benchmark_memory_mode"],
				"benchmark_memory_strategy": casePayload["benchmark_memory_strategy"],
				"workflow":                  casePayload["workflow"],
				"execution_stage":           casePayload["execution_stage"],
				"outcome":                   outcome,
				"memory_kind":               "benchmark_run",
			},
		)
	}
	return artifacts
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
