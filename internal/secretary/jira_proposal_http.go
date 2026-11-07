package secretary

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type startJiraProposalRequest struct {
	NotificationID string `json:"notification_id"`
	CandidateIndex int    `json:"candidate_index"`
}

func (s *Service) handleJiraProposalStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJiraProposalJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if s.JiraActions == nil || s.JiraActions.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.JiraActions.Token)) != 1 {
		writeJiraProposalJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorised"})
		return
	}
	operator, ok := s.openWebUIOperator(r)
	if !ok || strings.TrimSpace(r.Header.Get("X-Donna-Channel")) != "openwebui" || strings.TrimSpace(r.Header.Get("X-Donna-Operator-ID")) != strconv.FormatInt(operator, 10) {
		writeJiraProposalJSON(w, http.StatusForbidden, map[string]string{"error": "unauthorised operator"})
		return
	}
	conversation := strings.TrimSpace(r.Header.Get("X-Donna-Conversation-ID"))
	if conversation == "" || len(conversation) > 200 || strings.ContainsAny(conversation, "\r\n\x00") || s.GranolaNotifications == nil {
		writeJiraProposalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid proposal context"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request startJiraProposalRequest
	if decoder.Decode(&request) != nil || request.CandidateIndex < 0 || request.CandidateIndex >= 5 || !granolaNotificationID.MatchString(request.NotificationID) {
		writeJiraProposalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Granola candidate"})
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeJiraProposalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Granola candidate"})
		return
	}
	notification, found, err := s.GranolaNotifications.Get(operator, request.NotificationID)
	if err != nil || !found || request.CandidateIndex >= len(notification.TaskCandidates) {
		writeJiraProposalJSON(w, http.StatusNotFound, map[string]string{"error": "Granola candidate unavailable"})
		return
	}
	source := notification.TaskCandidates[request.CandidateIndex]
	candidate := TaskCandidate{GranolaNoteID: source.NoteID, NoteURL: source.NoteURL, MeetingTitle: source.MeetingTitle, SourceSummary: source.SourceSummary, SourceSpan: source.SourceSpan, Summary: source.SourceSpan}
	principal := JiraActionPrincipal{OperatorID: operator, Channel: "openwebui", ConversationID: conversation}
	result, err := s.StartJiraTaskProposal(r.Context(), principal, candidate)
	if err != nil {
		writeJiraProposalJSON(w, http.StatusBadGateway, map[string]string{"error": "proposal service unavailable"})
		return
	}
	writeJiraProposalJSON(w, http.StatusOK, map[string]string{"message": result.Message})
}

func writeJiraProposalJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
