package donnajira

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Authenticator interface {
	Authenticate(*http.Request) (ActionPrincipal, error)
}

type HeaderAuthenticator struct {
	ServiceToken string
}

func (a HeaderAuthenticator) Authenticate(request *http.Request) (ActionPrincipal, error) {
	if strings.TrimSpace(a.ServiceToken) == "" {
		return ActionPrincipal{}, ErrActionPrincipalMismatch
	}
	token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	if len(token) != len(a.ServiceToken) || subtle.ConstantTimeCompare([]byte(token), []byte(a.ServiceToken)) != 1 {
		return ActionPrincipal{}, ErrActionPrincipalMismatch
	}
	operatorID, err := strconv.ParseInt(strings.TrimSpace(request.Header.Get("X-Donna-Operator-ID")), 10, 64)
	if err != nil || operatorID <= 0 {
		return ActionPrincipal{}, ErrActionPrincipalMismatch
	}
	principal := ActionPrincipal{
		OperatorID:     operatorID,
		Channel:        strings.ToLower(strings.TrimSpace(request.Header.Get("X-Donna-Channel"))),
		ConversationID: strings.TrimSpace(request.Header.Get("X-Donna-Conversation-ID")),
	}
	if (principal.Channel != "telegram" && principal.Channel != "openwebui") || principal.ConversationID == "" || strings.ContainsAny(principal.ConversationID, "\r\n\x00") {
		return ActionPrincipal{}, ErrActionPrincipalMismatch
	}
	return principal, nil
}

type Handler struct {
	store    ApprovalStore
	writer   JiraWriter
	auth     Authenticator
	executor *Executor
	now      func() time.Time
}

func NewHandler(store ApprovalStore, writer JiraWriter, auth Authenticator, now func() time.Time) http.Handler {
	if now == nil {
		now = time.Now
	}
	handler := &Handler{store: store, writer: writer, auth: auth, now: now}
	handler.executor = NewExecutor(store, writer, now)
	return handler
}

type draftRequest struct {
	Kind                 ActionKind `json:"kind"`
	ProjectKey           string     `json:"project_key,omitempty"`
	IssueType            string     `json:"issue_type,omitempty"`
	IssueKey             string     `json:"issue_key,omitempty"`
	ExpectedIssueSummary string     `json:"expected_issue_summary,omitempty"`
	Summary              string     `json:"summary,omitempty"`
	Description          string     `json:"description,omitempty"`
	Comment              string     `json:"comment,omitempty"`
	AssigneeAccountID    string     `json:"assignee_account_id,omitempty"`
	DueDate              string     `json:"due_date,omitempty"`
	NoteID               string     `json:"note_id"`
	NoteURL              string     `json:"note_url"`
	SourceSpan           string     `json:"source_span"`
	DedupeKey            string     `json:"dedupe_key"`
}

type matchRequest struct {
	ProjectKey string `json:"project_key"`
	Query      string `json:"query"`
	Limit      int    `json:"limit,omitempty"`
}

type applyRequest struct {
	PayloadDigest string `json:"payload_digest"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if h.store == nil || h.writer == nil || h.auth == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Jira action service unavailable")
		return
	}
	principal, err := h.auth.Authenticate(r)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "authenticated Donna operator required")
		return
	}
	switch {
	case r.URL.Path == "/v1/config/options" && r.Method == http.MethodGet:
		provider, ok := h.writer.(JiraOptionsProvider)
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "Jira options unavailable")
			return
		}
		writeJSON(w, http.StatusOK, provider.AllowedOptions())
	case r.URL.Path == "/v1/tasks/matches" && r.Method == http.MethodPost:
		h.match(w, r)
	case r.URL.Path == "/v1/actions/draft" && r.Method == http.MethodPost:
		h.draft(w, r, principal)
	case strings.HasPrefix(r.URL.Path, "/v1/actions/"):
		h.action(w, r, principal)
	default:
		writeJSONError(w, http.StatusNotFound, "route not found")
	}
}

func (h *Handler) match(w http.ResponseWriter, r *http.Request) {
	var request matchRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid match request")
		return
	}
	if request.Limit == 0 {
		request.Limit = 5
	}
	issues, err := h.writer.SearchTaskCandidates(r.Context(), request.ProjectKey, request.Query, request.Limit)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "Jira candidate search failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": issues})
}

func (h *Handler) draft(w http.ResponseWriter, r *http.Request, principal ActionPrincipal) {
	var request draftRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid task draft")
		return
	}
	now := h.now()
	draft := TaskDraft{
		Principal:            principal,
		Kind:                 request.Kind,
		ProjectKey:           strings.TrimSpace(request.ProjectKey),
		IssueType:            strings.TrimSpace(request.IssueType),
		IssueKey:             strings.TrimSpace(request.IssueKey),
		ExpectedIssueSummary: strings.TrimSpace(request.ExpectedIssueSummary),
		Summary:              strings.TrimSpace(request.Summary),
		Description:          strings.TrimSpace(request.Description),
		Comment:              strings.TrimSpace(request.Comment),
		AssigneeAccountID:    strings.TrimSpace(request.AssigneeAccountID),
		DueDate:              strings.TrimSpace(request.DueDate),
		NoteID:               strings.TrimSpace(request.NoteID),
		NoteURL:              strings.TrimSpace(request.NoteURL),
		SourceSpan:           strings.TrimSpace(request.SourceSpan),
		DedupeKey:            strings.TrimSpace(request.DedupeKey),
		CreatedAt:            now,
		ExpiresAt:            now.Add(10 * time.Minute),
	}
	if err := validateNoteURL(draft.NoteURL); err != nil {
		writeJSONError(w, http.StatusBadRequest, "task source link is invalid")
		return
	}
	if draft.Kind == ActionComment {
		issue, err := h.writer.GetIssue(r.Context(), draft.IssueKey)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "Jira task could not be verified")
			return
		}
		if issue.IssueKey != draft.IssueKey || issue.Summary != draft.ExpectedIssueSummary {
			writeJSONError(w, http.StatusConflict, "Jira task changed; review the match again")
			return
		}
	}
	created, err := h.store.Create(r.Context(), draft)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrActionDuplicateSource) {
			status = http.StatusConflict
		}
		writeJSONError(w, status, "task draft could not be stored")
		return
	}
	writeJSON(w, http.StatusCreated, previewFor(created))
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request, principal ActionPrincipal) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/actions/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		draft, err := h.store.Get(r.Context(), parts[0], principal)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, previewFor(draft))
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		var request applyRequest
		if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.PayloadDigest) == "" {
			writeJSONError(w, http.StatusBadRequest, "invalid action cancellation")
			return
		}
		if err := h.store.Cancel(r.Context(), parts[0], principal, request.PayloadDigest, h.now()); err != nil {
			status := http.StatusConflict
			if errors.Is(err, ErrActionNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, ErrActionPrincipalMismatch) {
				status = http.StatusUnauthorized
			} else if errors.Is(err, ErrActionExpired) || errors.Is(err, ErrActionAlreadyConsumed) || errors.Is(err, ErrActionDigestMismatch) {
				status = http.StatusConflict
			}
			writeJSONError(w, status, "Jira action could not be canceled")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"action_id": parts[0], "status": string(ActionCanceled)})
		return
	}
	if len(parts) != 2 || parts[1] != "apply" || r.Method != http.MethodPost {
		writeJSONError(w, http.StatusNotFound, "route not found")
		return
	}
	var request applyRequest
	if err := decodeJSON(w, r, &request); err != nil || strings.TrimSpace(request.PayloadDigest) == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid action confirmation")
		return
	}
	receipt, err := h.executor.Apply(r.Context(), principal, parts[0], request.PayloadDigest)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrActionNotFound) {
			status = http.StatusNotFound
		} else if errors.Is(err, ErrActionPrincipalMismatch) {
			status = http.StatusUnauthorized
		} else if errors.Is(err, ErrActionDigestMismatch) || errors.Is(err, ErrActionExpired) || errors.Is(err, ErrActionAlreadyConsumed) || errors.Is(err, ErrActionTargetChanged) {
			status = http.StatusConflict
		}
		if receipt.ActionID != "" {
			writeJSON(w, status, receipt)
			return
		}
		writeJSONError(w, status, "Jira action was not verified")
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func previewFor(draft TaskDraft) ActionPreview {
	return ActionPreview{
		ActionID: draft.ID, Kind: draft.Kind, Principal: draft.Principal,
		ProjectKey: draft.ProjectKey, IssueKey: draft.IssueKey, IssueSummary: draft.ExpectedIssueSummary,
		IssueType: draft.IssueType, Summary: draft.Summary, Description: draft.Description,
		Comment: draft.Comment, NoteURL: draft.NoteURL, SourceSpan: draft.SourceSpan,
		PayloadDigest: draft.PayloadDigest, ExpiresAt: draft.ExpiresAt,
	}
}

func validateNoteURL(value string) error {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
		return ErrActionInvalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return ErrActionInvalid
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrActionNotFound):
		writeJSONError(w, http.StatusNotFound, "Jira action not found")
	case errors.Is(err, ErrActionPrincipalMismatch):
		writeJSONError(w, http.StatusUnauthorized, "authenticated Donna operator required")
	default:
		writeJSONError(w, http.StatusConflict, "Jira action unavailable")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
