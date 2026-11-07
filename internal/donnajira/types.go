package donnajira

import (
	"context"
	"errors"
	"time"
)

type ActionKind string

const (
	ActionCreate  ActionKind = "create"
	ActionComment ActionKind = "comment"
)

type ActionStatus string

const (
	ActionPending   ActionStatus = "pending"
	ActionConsumed  ActionStatus = "consumed"
	ActionCommitted ActionStatus = "committed"
	ActionUncertain ActionStatus = "uncertain"
	ActionCanceled  ActionStatus = "canceled"
)

var (
	ErrActionNotFound          = errors.New("Jira action not found")
	ErrActionPrincipalMismatch = errors.New("Jira action principal mismatch")
	ErrActionDigestMismatch    = errors.New("Jira action payload changed")
	ErrActionExpired           = errors.New("Jira action expired")
	ErrActionAlreadyConsumed   = errors.New("Jira action already consumed")
	ErrActionDuplicateSource   = errors.New("Jira source action already has a draft")
	ErrActionInvalid           = errors.New("invalid Jira action")
	ErrActionStoreUnavailable  = errors.New("Jira action store unavailable")
	ErrActionTargetChanged     = errors.New("Jira action target changed after preview")
)

type ActionPrincipal struct {
	OperatorID     int64  `json:"operator_id"`
	Channel        string `json:"channel"`
	ConversationID string `json:"conversation_id"`
}

type TaskDraft struct {
	ID                   string          `json:"id"`
	Principal            ActionPrincipal `json:"principal"`
	Kind                 ActionKind      `json:"kind"`
	ProjectKey           string          `json:"project_key,omitempty"`
	IssueType            string          `json:"issue_type,omitempty"`
	IssueKey             string          `json:"issue_key,omitempty"`
	ExpectedIssueSummary string          `json:"expected_issue_summary,omitempty"`
	Summary              string          `json:"summary,omitempty"`
	Description          string          `json:"description,omitempty"`
	Comment              string          `json:"comment,omitempty"`
	AssigneeAccountID    string          `json:"assignee_account_id,omitempty"`
	DueDate              string          `json:"due_date,omitempty"`
	NoteID               string          `json:"note_id"`
	NoteURL              string          `json:"note_url"`
	SourceSpan           string          `json:"source_span"`
	DedupeKey            string          `json:"dedupe_key"`
	PayloadDigest        string          `json:"payload_digest"`
	CreatedAt            time.Time       `json:"created_at"`
	ExpiresAt            time.Time       `json:"expires_at"`
	Status               ActionStatus    `json:"status"`
	Receipt              *ActionReceipt  `json:"receipt,omitempty"`
}

type MatchCandidate struct {
	IssueKey   string  `json:"issue_key"`
	Summary    string  `json:"summary"`
	Status     string  `json:"status,omitempty"`
	Similarity float64 `json:"similarity,omitempty"`
	Confidence float64 `json:"confidence"`
}

type JiraOptions struct {
	ProjectKeys []string `json:"project_keys"`
	IssueTypes  []string `json:"issue_types"`
}

type JiraOptionsProvider interface {
	AllowedOptions() JiraOptions
}

type ActionPreview struct {
	ActionID      string          `json:"action_id"`
	Kind          ActionKind      `json:"kind"`
	Principal     ActionPrincipal `json:"principal"`
	ProjectKey    string          `json:"project_key,omitempty"`
	IssueKey      string          `json:"issue_key,omitempty"`
	IssueSummary  string          `json:"issue_summary,omitempty"`
	IssueType     string          `json:"issue_type,omitempty"`
	Summary       string          `json:"summary,omitempty"`
	Description   string          `json:"description,omitempty"`
	Comment       string          `json:"comment,omitempty"`
	NoteURL       string          `json:"note_url"`
	SourceSpan    string          `json:"source_span"`
	PayloadDigest string          `json:"payload_digest"`
	ExpiresAt     time.Time       `json:"expires_at"`
}

type ActionReceipt struct {
	ActionID    string       `json:"action_id"`
	Kind        ActionKind   `json:"kind"`
	IssueKey    string       `json:"issue_key"`
	IssueURL    string       `json:"issue_url,omitempty"`
	CommentID   string       `json:"comment_id,omitempty"`
	Verified    bool         `json:"verified"`
	Status      ActionStatus `json:"status"`
	CompletedAt time.Time    `json:"completed_at"`
}

type JiraWriter interface {
	SearchTaskCandidates(context.Context, string, string, int) ([]MatchCandidate, error)
	CreateIssue(context.Context, TaskDraft) (MatchCandidate, error)
	AddComment(context.Context, string, string) (string, error)
	GetIssue(context.Context, string) (MatchCandidate, error)
	GetComment(context.Context, string, string) (string, error)
}
