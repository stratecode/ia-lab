package donnajira

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeJiraWriter struct {
	created      MatchCandidate
	issue        MatchCandidate
	commentID    string
	commentText  string
	createCalls  int
	commentCalls int
}

func (f *fakeJiraWriter) AllowedOptions() JiraOptions {
	return JiraOptions{ProjectKeys: []string{"PMI"}, IssueTypes: []string{"Task"}}
}

func (f *fakeJiraWriter) SearchTaskCandidates(context.Context, string, string, int) ([]MatchCandidate, error) {
	return nil, nil
}

func (f *fakeJiraWriter) CreateIssue(context.Context, TaskDraft) (MatchCandidate, error) {
	f.createCalls++
	return f.created, nil
}

func (f *fakeJiraWriter) AddComment(_ context.Context, _, text string) (string, error) {
	f.commentCalls++
	f.commentText = text
	return f.commentID, nil
}

func (f *fakeJiraWriter) GetIssue(context.Context, string) (MatchCandidate, error) {
	return f.issue, nil
}

func (f *fakeJiraWriter) GetComment(context.Context, string, string) (string, error) {
	return f.commentText, nil
}

func TestExecutorAppliesConfirmedCreateOnceAndVerifiesIssue(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{
		created: MatchCandidate{IssueKey: "PMI-43", Summary: draft.Summary},
		issue:   MatchCandidate{IssueKey: "PMI-43", Summary: draft.Summary},
	}
	executor := NewExecutor(store, writer, func() time.Time { return now.Add(time.Minute) })

	receipt, err := executor.Apply(context.Background(), draft.Principal, draft.ID, draft.PayloadDigest)
	if err != nil || !receipt.Verified || receipt.IssueKey != "PMI-43" || writer.createCalls != 1 {
		t.Fatalf("receipt=%+v createCalls=%d err=%v", receipt, writer.createCalls, err)
	}
	if _, err := executor.Apply(context.Background(), draft.Principal, draft.ID, draft.PayloadDigest); !errors.Is(err, ErrActionAlreadyConsumed) || writer.createCalls != 1 {
		t.Fatalf("replay err=%v createCalls=%d", err, writer.createCalls)
	}
}

func TestExecutorAddsOnlyConfirmedSourceCommentAndVerifiesIt(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	draft := testDraft(now)
	draft.ID = ""
	draft.Kind = ActionComment
	draft.ProjectKey = ""
	draft.IssueType = ""
	draft.IssueKey = "PMI-42"
	draft.ExpectedIssueSummary = draft.Summary
	draft.Summary = ""
	draft.Comment = "Contexto nuevo. Fuente: " + draft.NoteURL
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{
		issue:       MatchCandidate{IssueKey: draft.IssueKey, Summary: draft.ExpectedIssueSummary},
		commentID:   "comment-9",
		commentText: draft.Comment,
	}
	executor := NewExecutor(store, writer, func() time.Time { return now.Add(time.Minute) })

	receipt, err := executor.Apply(context.Background(), draft.Principal, draft.ID, draft.PayloadDigest)
	if err != nil || !receipt.Verified || receipt.IssueKey != "PMI-42" || receipt.CommentID != "comment-9" || writer.commentCalls != 1 || writer.commentText != draft.Comment {
		t.Fatalf("receipt=%+v writer=%+v err=%v", receipt, writer, err)
	}
}

func TestExecutorRejectsCommentWhenIssueChangedSincePreview(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	draft := testDraft(now)
	draft.Kind = ActionComment
	draft.ProjectKey = ""
	draft.IssueType = ""
	draft.IssueKey = "PMI-42"
	draft.ExpectedIssueSummary = draft.Summary
	draft.Summary = ""
	draft.Comment = "Fuente: " + draft.NoteURL
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err = store.Create(context.Background(), draft)
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{issue: MatchCandidate{IssueKey: draft.IssueKey, Summary: "Another task"}}
	executor := NewExecutor(store, writer, func() time.Time { return now.Add(time.Minute) })

	if _, err := executor.Apply(context.Background(), draft.Principal, draft.ID, draft.PayloadDigest); !errors.Is(err, ErrActionTargetChanged) {
		t.Fatalf("apply error=%v, want target changed", err)
	}
	if writer.commentCalls != 0 {
		t.Fatalf("commentCalls=%d, want no comment", writer.commentCalls)
	}
}

func TestExecutorDoesNotReportCreateSuccessWithoutReadBackMatch(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJiraWriter{
		created: MatchCandidate{IssueKey: "PMI-43", Summary: draft.Summary},
		issue:   MatchCandidate{IssueKey: "PMI-43", Summary: "Different task"},
	}
	executor := NewExecutor(store, writer, func() time.Time { return now.Add(time.Minute) })

	receipt, err := executor.Apply(context.Background(), draft.Principal, draft.ID, draft.PayloadDigest)
	if err == nil || receipt.Verified || receipt.Status != ActionUncertain {
		t.Fatalf("receipt=%+v err=%v, want unverified uncertain result", receipt, err)
	}
}
