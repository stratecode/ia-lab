package httpapi

import (
	"encoding/json"
	"net/http"

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
