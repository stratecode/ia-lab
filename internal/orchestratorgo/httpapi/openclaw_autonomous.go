package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func (s *Server) startAutonomousInitiative(w http.ResponseWriter, r *http.Request) {
	if s.AutonomousRunner == nil {
		writeDetail(w, http.StatusNotImplemented, "autonomous runner is not configured")
		return
	}
	var req domain.AutonomousInitiativeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDetail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Surface = firstNonEmptyString(strings.TrimSpace(req.Surface), "openclaw.http")
	if !req.AutoApprovePhases {
		req.AutoApprovePhases = true
	}
	result, err := s.AutonomousRunner.StartFromChannel(r.Context(), req)
	if err != nil {
		writeDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}
