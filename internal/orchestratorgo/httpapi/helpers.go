package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
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
	if s != nil && s.SemanticIndexer != nil {
		go s.runSemanticIndex(func(runCtx context.Context) {
			s.SemanticIndexer.IndexTask(runCtx, taskID)
		})
	}
}

func (s *Server) indexArtifact(ctx context.Context, artifactID string) {
	if s != nil && s.SemanticIndexer != nil {
		go s.runSemanticIndex(func(runCtx context.Context) {
			s.SemanticIndexer.IndexArtifact(runCtx, artifactID)
		})
	}
}

func (s *Server) indexReview(ctx context.Context, reviewID string) {
	if s != nil && s.SemanticIndexer != nil {
		go s.runSemanticIndex(func(runCtx context.Context) {
			s.SemanticIndexer.IndexReview(runCtx, reviewID)
		})
	}
}

func (s *Server) runSemanticIndex(fn func(context.Context)) {
	if s == nil || s.SemanticIndexer == nil || fn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fn(ctx)
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
