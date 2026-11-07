package donnajira

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Executor struct {
	Store ApprovalStore
	Jira  JiraWriter
	Now   func() time.Time
}

func NewExecutor(store ApprovalStore, jira JiraWriter, now func() time.Time) *Executor {
	if now == nil {
		now = time.Now
	}
	return &Executor{Store: store, Jira: jira, Now: now}
}

func (e *Executor) Apply(ctx context.Context, principal ActionPrincipal, actionID, digest string) (ActionReceipt, error) {
	if e == nil || e.Store == nil || e.Jira == nil || strings.TrimSpace(actionID) == "" {
		return ActionReceipt{}, ErrActionInvalid
	}
	draft, err := e.Store.Get(ctx, actionID, principal)
	if err != nil {
		return ActionReceipt{}, err
	}
	if draft.Status != ActionPending {
		return ActionReceipt{}, ErrActionAlreadyConsumed
	}
	if draft.Kind == ActionComment {
		issue, err := e.Jira.GetIssue(ctx, draft.IssueKey)
		if err != nil {
			return ActionReceipt{}, err
		}
		if issue.IssueKey != draft.IssueKey || issue.Summary != draft.ExpectedIssueSummary {
			return ActionReceipt{}, ErrActionTargetChanged
		}
	}
	consumed, err := e.Store.Consume(ctx, actionID, principal, digest, e.Now())
	if err != nil {
		return ActionReceipt{}, err
	}
	switch consumed.Kind {
	case ActionCreate:
		return e.applyCreate(ctx, consumed)
	case ActionComment:
		return e.applyComment(ctx, consumed)
	default:
		return ActionReceipt{}, ErrActionInvalid
	}
}

func (e *Executor) applyCreate(ctx context.Context, draft TaskDraft) (ActionReceipt, error) {
	created, err := e.Jira.CreateIssue(ctx, draft)
	if err != nil {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionCreate}, err)
	}
	if !jiraIssueKeyPattern.MatchString(created.IssueKey) {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionCreate}, errors.New("Jira create returned no valid issue key"))
	}
	issue, err := e.Jira.GetIssue(ctx, created.IssueKey)
	if err != nil {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionCreate, IssueKey: created.IssueKey}, err)
	}
	if issue.IssueKey != created.IssueKey || issue.Summary != draft.Summary {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionCreate, IssueKey: created.IssueKey}, errors.New("Jira create read-back did not match the preview"))
	}
	receipt := ActionReceipt{ActionID: draft.ID, Kind: ActionCreate, IssueKey: issue.IssueKey, Verified: true, Status: ActionCommitted, CompletedAt: e.Now()}
	if err := e.Store.Complete(ctx, draft.ID, draft.Principal, receipt); err != nil {
		return ActionReceipt{}, err
	}
	return receipt, nil
}

func (e *Executor) applyComment(ctx context.Context, draft TaskDraft) (ActionReceipt, error) {
	commentID, err := e.Jira.AddComment(ctx, draft.IssueKey, draft.Comment)
	if err != nil {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionComment, IssueKey: draft.IssueKey}, err)
	}
	comment, err := e.Jira.GetComment(ctx, draft.IssueKey, commentID)
	if err != nil {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionComment, IssueKey: draft.IssueKey, CommentID: commentID}, err)
	}
	if comment != draft.Comment {
		return e.recordUncertain(ctx, draft, ActionReceipt{Kind: ActionComment, IssueKey: draft.IssueKey, CommentID: commentID}, errors.New("Jira comment read-back did not match the preview"))
	}
	receipt := ActionReceipt{ActionID: draft.ID, Kind: ActionComment, IssueKey: draft.IssueKey, CommentID: commentID, Verified: true, Status: ActionCommitted, CompletedAt: e.Now()}
	if err := e.Store.Complete(ctx, draft.ID, draft.Principal, receipt); err != nil {
		return ActionReceipt{}, err
	}
	return receipt, nil
}

func (e *Executor) recordUncertain(ctx context.Context, draft TaskDraft, receipt ActionReceipt, cause error) (ActionReceipt, error) {
	receipt.ActionID = draft.ID
	receipt.Status = ActionUncertain
	receipt.Verified = false
	receipt.CompletedAt = e.Now()
	if err := e.Store.Complete(ctx, draft.ID, draft.Principal, receipt); err != nil {
		return ActionReceipt{}, err
	}
	return receipt, cause
}
