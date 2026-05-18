package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
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
		if _, err := s.Postgres.CompleteTask(r.Context(), taskID, "bridge:"+bridgeID, firstNonEmptyString(derefStringPtr(body.Summary), "Local bridge execution completed"), results); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.reconcileRootTask(r.Context(), taskID)
	default:
		errorMessage := firstNonEmptyString(derefStringPtr(body.ErrorMessage), derefStringPtr(body.Stderr), derefStringPtr(body.Stdout), "Local bridge execution failed")
		if _, err := s.Postgres.FailTask(r.Context(), taskID, "bridge:"+bridgeID, errorMessage, errorMessage, results); err != nil {
			writeDetail(w, http.StatusInternalServerError, err.Error())
			return
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
		"summary":       strings.TrimSpace(derefStringPtr(result.Summary)),
		"stdout":        derefStringPtr(result.Stdout),
		"stderr":        derefStringPtr(result.Stderr),
		"exit_code":     result.ExitCode,
		"diff":          derefStringPtr(result.Diff),
		"changed_files": result.ChangedFiles,
		"test_results":  result.TestResults,
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

	artifactIDs := make([]string, 0, 4+len(result.Artifacts))
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
	for _, artifact := range result.Artifacts {
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

	results := map[string]any{
		"status":             strings.ToLower(strings.TrimSpace(result.Status)),
		"summary":            derefStringPtr(result.Summary),
		"stdout":             derefStringPtr(result.Stdout),
		"stderr":             derefStringPtr(result.Stderr),
		"exit_code":          result.ExitCode,
		"diff":               derefStringPtr(result.Diff),
		"changed_files":      result.ChangedFiles,
		"test_results":       result.TestResults,
		"artifacts":          result.Artifacts,
		"error_message":      derefStringPtr(result.ErrorMessage),
		"tool_invocation_id": invocationID,
		"artifact_ids":       artifactIDs,
	}
	return results, invocationID, artifactIDs, nil
}

func derefStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
