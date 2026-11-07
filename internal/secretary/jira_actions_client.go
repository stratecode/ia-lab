package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrJiraActionsUnavailable = errors.New("Jira task actions are unavailable")

type JiraActionPrincipal struct {
	OperatorID     int64
	Channel        string
	ConversationID string
}

type JiraTaskDraft struct {
	Kind                 string `json:"kind"`
	ProjectKey           string `json:"project_key,omitempty"`
	IssueType            string `json:"issue_type,omitempty"`
	IssueKey             string `json:"issue_key,omitempty"`
	ExpectedIssueSummary string `json:"expected_issue_summary,omitempty"`
	Summary              string `json:"summary,omitempty"`
	Description          string `json:"description,omitempty"`
	Comment              string `json:"comment,omitempty"`
	AssigneeAccountID    string `json:"assignee_account_id,omitempty"`
	DueDate              string `json:"due_date,omitempty"`
	NoteID               string `json:"note_id"`
	NoteURL              string `json:"note_url"`
	SourceSpan           string `json:"source_span"`
	DedupeKey            string `json:"dedupe_key"`
}

type JiraActionPreview struct {
	ActionID      string    `json:"action_id"`
	Kind          string    `json:"kind"`
	ProjectKey    string    `json:"project_key,omitempty"`
	IssueKey      string    `json:"issue_key,omitempty"`
	IssueSummary  string    `json:"issue_summary,omitempty"`
	IssueType     string    `json:"issue_type,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Description   string    `json:"description,omitempty"`
	Comment       string    `json:"comment,omitempty"`
	NoteURL       string    `json:"note_url"`
	SourceSpan    string    `json:"source_span"`
	PayloadDigest string    `json:"payload_digest"`
	ExpiresAt     time.Time `json:"expires_at"`
}

type JiraActionReceipt struct {
	ActionID  string `json:"action_id"`
	Kind      string `json:"kind"`
	IssueKey  string `json:"issue_key"`
	IssueURL  string `json:"issue_url,omitempty"`
	CommentID string `json:"comment_id,omitempty"`
	Verified  bool   `json:"verified"`
	Status    string `json:"status"`
}

type JiraTaskMatchCandidate struct {
	IssueKey   string  `json:"issue_key"`
	ProjectKey string  `json:"project_key"`
	Summary    string  `json:"summary"`
	Status     string  `json:"status"`
	Similarity float64 `json:"similarity"`
	Confidence float64 `json:"confidence"`
}

type JiraTaskOptions struct {
	ProjectKeys []string `json:"project_keys"`
	IssueTypes  []string `json:"issue_types"`
}

type JiraActionsClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func (c *JiraActionsClient) Draft(ctx context.Context, principal JiraActionPrincipal, draft JiraTaskDraft) (JiraActionPreview, error) {
	var preview JiraActionPreview
	if err := c.request(ctx, principal, http.MethodPost, "/v1/actions/draft", draft, http.StatusCreated, &preview); err != nil {
		return JiraActionPreview{}, err
	}
	if strings.TrimSpace(preview.ActionID) == "" || strings.TrimSpace(preview.PayloadDigest) == "" || preview.Kind != draft.Kind {
		return JiraActionPreview{}, ErrJiraActionsUnavailable
	}
	return preview, nil
}

func (c *JiraActionsClient) Preview(ctx context.Context, principal JiraActionPrincipal, actionID string) (JiraActionPreview, error) {
	var preview JiraActionPreview
	if !safeActionID(actionID) {
		return JiraActionPreview{}, ErrJiraActionsUnavailable
	}
	if err := c.request(ctx, principal, http.MethodGet, "/v1/actions/"+url.PathEscape(actionID), nil, http.StatusOK, &preview); err != nil {
		return JiraActionPreview{}, err
	}
	return preview, nil
}

func (c *JiraActionsClient) Apply(ctx context.Context, principal JiraActionPrincipal, actionID, digest string) (JiraActionReceipt, error) {
	if !safeActionID(actionID) || strings.TrimSpace(digest) == "" {
		return JiraActionReceipt{}, ErrJiraActionsUnavailable
	}
	var receipt JiraActionReceipt
	if err := c.request(ctx, principal, http.MethodPost, "/v1/actions/"+url.PathEscape(actionID)+"/apply", map[string]string{"payload_digest": digest}, http.StatusOK, &receipt); err != nil {
		return JiraActionReceipt{}, err
	}
	if !receipt.Verified || receipt.IssueKey == "" || receipt.ActionID != actionID {
		return JiraActionReceipt{}, ErrJiraActionsUnavailable
	}
	return receipt, nil
}

func (c *JiraActionsClient) Cancel(ctx context.Context, principal JiraActionPrincipal, actionID, digest string) error {
	if !safeActionID(actionID) || strings.TrimSpace(digest) == "" {
		return ErrJiraActionsUnavailable
	}
	return c.request(ctx, principal, http.MethodPost, "/v1/actions/"+url.PathEscape(actionID)+"/cancel", map[string]string{"payload_digest": digest}, http.StatusOK, nil)
}

func (c *JiraActionsClient) Match(ctx context.Context, principal JiraActionPrincipal, projectKey, query string, limit int) ([]JiraTaskMatchCandidate, error) {
	var response struct {
		Candidates []JiraTaskMatchCandidate `json:"candidates"`
	}
	request := map[string]any{"project_key": projectKey, "query": query, "limit": limit}
	if err := c.request(ctx, principal, http.MethodPost, "/v1/tasks/matches", request, http.StatusOK, &response); err != nil {
		return nil, err
	}
	return response.Candidates, nil
}

func (c *JiraActionsClient) Options(ctx context.Context, principal JiraActionPrincipal) (JiraTaskOptions, error) {
	var options JiraTaskOptions
	if err := c.request(ctx, principal, http.MethodGet, "/v1/config/options", nil, http.StatusOK, &options); err != nil {
		return JiraTaskOptions{}, err
	}
	return options, nil
}

func (c *JiraActionsClient) request(ctx context.Context, principal JiraActionPrincipal, method, path string, payload any, expectedStatus int, target any) error {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"))
	if err != nil || base == nil {
		return ErrJiraActionsUnavailable
	}
	hostIP := net.ParseIP(base.Hostname())
	loopbackHost := hostIP != nil && hostIP.IsLoopback() || strings.EqualFold(base.Hostname(), "localhost")
	if (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || !loopbackHost || base.User != nil || base.RawQuery != "" || base.Fragment != "" || len(strings.TrimSpace(c.Token)) < 32 || principal.OperatorID <= 0 || (principal.Channel != "telegram" && principal.Channel != "openwebui") || strings.TrimSpace(principal.ConversationID) == "" {
		return ErrJiraActionsUnavailable
	}
	if strings.ContainsAny(principal.ConversationID, "\r\n\x00") {
		return ErrJiraActionsUnavailable
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return ErrJiraActionsUnavailable
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.String()+path, body)
	if err != nil {
		return ErrJiraActionsUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("X-Donna-Operator-ID", fmt.Sprint(principal.OperatorID))
	request.Header.Set("X-Donna-Channel", principal.Channel)
	request.Header.Set("X-Donna-Conversation-ID", principal.ConversationID)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(request)
	if err != nil {
		return ErrJiraActionsUnavailable
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != expectedStatus {
		return ErrJiraActionsUnavailable
	}
	if target != nil && json.Unmarshal(data, target) != nil {
		return ErrJiraActionsUnavailable
	}
	return nil
}

func safeActionID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}
	return true
}
