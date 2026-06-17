package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/memory"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeDetail(w http.ResponseWriter, statusCode int, detail string) {
	writeJSON(w, statusCode, map[string]string{"detail": detail})
}

func (s *Server) indexTask(ctx context.Context, taskID string) {
	_ = s
	_ = ctx
	_ = taskID
}

func (s *Server) indexArtifact(ctx context.Context, artifactID string) {
	_ = s
	_ = ctx
	_ = artifactID
}

func (s *Server) indexReview(ctx context.Context, reviewID string) {
	_ = s
	_ = ctx
	_ = reviewID
}

func (s *Server) localBridgeLeaseTTLSeconds() int {
	if s != nil && s.LocalBridgeLeaseTTLSeconds > 0 {
		return s.LocalBridgeLeaseTTLSeconds
	}
	return 45
}

func writeTaskStateConflict(w http.ResponseWriter, currentState, requestedState domain.TaskState, taskID string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"detail": domain.TaskStateConflictResponse{
			Error:            "invalid_state_transition",
			CurrentState:     currentState,
			RequestedState:   requestedState,
			ValidTransitions: domain.ValidTransitionStrings(currentState),
			TaskID:           taskID,
		},
	})
}

func writeNotImplemented(w http.ResponseWriter, feature string) {
	writeDetail(w, http.StatusNotImplemented, feature+" is not implemented in orchestrator-go yet")
}

func (s *Server) attachTaskContextPackage(ctx context.Context, task *domain.TaskResponse, contextPackage *domain.ContextPackage) error {
	_ = s
	_ = ctx
	_ = task
	_ = contextPackage
	return nil
}

func (s *Server) persistRetrievalPacketArtifact(ctx context.Context, task *domain.TaskResponse, contextPackage *domain.ContextPackage) (string, error) {
	packet := buildRetrievalPacket(task, contextPackage)
	raw, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", err
	}
	artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		TaskID:       &task.ID,
		ArtifactType: "retrieval_packet",
		Title:        stringPtr(fmt.Sprintf("Retrieval packet %s", task.ID)),
		MediaType:    stringPtr("application/json"),
		ContentText:  stringPtr(string(raw)),
		Metadata: map[string]any{
			"initiative_id":   derefStringPtr(task.InitiativeID),
			"task_id":         task.ID,
			"agent_type":      derefAgentType(task.AssignedAgent),
			"memory_kind":     "retrieval_packet",
			"memory_mode":     contextPackage.MemoryMode,
			"memory_strategy": contextPackage.MemoryStrategy,
			"query":           contextPackage.Query,
			"chunk_count":     len(contextPackage.Chunks),
			"precedent_count": len(packet.Precedents),
			"total_chars":     packet.TotalChars,
			"source_refs":     contextPackage.SourceRefs,
		},
	})
	if err != nil {
		return "", err
	}
	s.indexArtifact(ctx, artifactID)
	return artifactID, nil
}

func (s *Server) persistInitiativeRetrievalPacketArtifact(ctx context.Context, initiative *domain.InitiativeResponse, agentType string, contextPackage *domain.ContextPackage) (string, error) {
	if s == nil || s.Postgres == nil || initiative == nil || contextPackage == nil || len(contextPackage.Chunks) == 0 {
		return "", nil
	}
	packet := domain.RetrievalPacket{
		AgentType:          strings.TrimSpace(agentType),
		InitiativeID:       &initiative.ID,
		WorkspaceRoot:      contextPackage.WorkspaceRoot,
		Query:              contextPackage.Query,
		MemoryMode:         contextPackage.MemoryMode,
		MemoryStrategy:     contextPackage.MemoryStrategy,
		ChunkCount:         len(contextPackage.Chunks),
		SourceRefs:         append([]string{}, contextPackage.SourceRefs...),
		Constraints:        append([]string{}, contextPackage.Constraints...),
		Policies:           append([]string{}, contextPackage.Policies...),
		CompactSummaries:   cloneStringMap(contextPackage.CompactSummaries),
		WhyThesePrecedents: append([]string{}, contextPackage.WhyThesePrecedents...),
		Precedents:         append([]domain.RetrievalHit{}, contextPackage.Precedents...),
		OperationalIR:      contextPackage.OperationalIR,
		TotalChars:         contextPackage.TotalChars,
		GeneratedAt:        contextPackage.GeneratedAt,
	}
	if len(packet.Precedents) == 0 {
		selected, compactSummaries, why := memory.SelectRetrievalHits(contextPackage.Chunks, memory.DefaultPrecedentLimit, memory.DefaultPerClassLimit)
		packet.Precedents = selected
		if len(packet.CompactSummaries) == 0 {
			packet.CompactSummaries = compactSummaries
		}
		if len(packet.WhyThesePrecedents) == 0 {
			packet.WhyThesePrecedents = why
		}
	}
	raw, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return "", err
	}
	artifactID, err := s.Postgres.CreateArtifact(ctx, store.CreateArtifactParams{
		ArtifactType: "planning_retrieval_packet",
		Title:        stringPtr("Planning retrieval packet"),
		MediaType:    stringPtr("application/json"),
		ContentText:  stringPtr(string(raw)),
		Metadata: map[string]any{
			"initiative_id":   initiative.ID,
			"agent_type":      strings.TrimSpace(agentType),
			"memory_kind":     "planning_retrieval_packet",
			"memory_mode":     contextPackage.MemoryMode,
			"memory_strategy": contextPackage.MemoryStrategy,
			"query":           contextPackage.Query,
			"chunk_count":     len(contextPackage.Chunks),
			"precedent_count": len(packet.Precedents),
			"total_chars":     packet.TotalChars,
			"source_refs":     contextPackage.SourceRefs,
			"workspace_root":  strings.TrimSpace(initiative.WorkspaceRoot),
		},
	})
	if err != nil {
		return "", err
	}
	s.indexArtifact(ctx, artifactID)
	return artifactID, nil
}

func buildRetrievalPacket(task *domain.TaskResponse, contextPackage *domain.ContextPackage) domain.RetrievalPacket {
	packet := domain.RetrievalPacket{
		AgentType:          derefAgentType(task.AssignedAgent),
		TaskID:             stringPtr(task.ID),
		InitiativeID:       task.InitiativeID,
		WorkspaceRoot:      contextPackage.WorkspaceRoot,
		Query:              contextPackage.Query,
		MemoryMode:         contextPackage.MemoryMode,
		MemoryStrategy:     contextPackage.MemoryStrategy,
		ChunkCount:         len(contextPackage.Chunks),
		SourceRefs:         append([]string{}, contextPackage.SourceRefs...),
		Constraints:        append([]string{}, contextPackage.Constraints...),
		Policies:           append([]string{}, contextPackage.Policies...),
		CompactSummaries:   cloneStringMap(contextPackage.CompactSummaries),
		WhyThesePrecedents: append([]string{}, contextPackage.WhyThesePrecedents...),
		Precedents:         append([]domain.RetrievalHit{}, contextPackage.Precedents...),
		OperationalIR:      contextPackage.OperationalIR,
		TotalChars:         contextPackage.TotalChars,
		GeneratedAt:        contextPackage.GeneratedAt,
	}
	if len(packet.Precedents) == 0 {
		selected, compactSummaries, why := memory.SelectRetrievalHits(contextPackage.Chunks, memory.DefaultPrecedentLimit, memory.DefaultPerClassLimit)
		packet.Precedents = selected
		if len(packet.CompactSummaries) == 0 {
			packet.CompactSummaries = compactSummaries
		}
		if len(packet.WhyThesePrecedents) == 0 {
			packet.WhyThesePrecedents = why
		}
	}
	return packet
}

func summarizeRetrievalChunk(text string) string {
	return memory.CompactText(text, memory.DefaultChunkSummaryChars)
}

func derefAgentType(value *domain.AgentType) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(string(*value))
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
