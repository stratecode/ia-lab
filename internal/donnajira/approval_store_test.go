package donnajira

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func testDraft(now time.Time) TaskDraft {
	return TaskDraft{
		Principal:  ActionPrincipal{OperatorID: 7, Channel: "telegram", ConversationID: "chat-7"},
		Kind:       ActionCreate,
		ProjectKey: "PMI",
		IssueType:  "Task",
		Summary:    "Preparar el informe de conciliación",
		NoteID:     "note-123",
		NoteURL:    "https://notes.example.test/note-123",
		SourceSpan: "Preparar el informe de conciliación para el viernes.",
		DedupeKey:  "note-123:preparar-el-informe-de-conciliacion",
		CreatedAt:  now,
		ExpiresAt:  now.Add(10 * time.Minute),
	}
}

func TestApprovalStoreBindsSingleUseActionToPrincipalAndDigest(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID == "" || draft.PayloadDigest == "" {
		t.Fatalf("draft must have a generated ID and digest: %+v", draft)
	}

	wrongPrincipal := ActionPrincipal{OperatorID: 8, Channel: "telegram", ConversationID: "chat-7"}
	if _, err := store.Consume(context.Background(), draft.ID, wrongPrincipal, draft.PayloadDigest, now.Add(time.Minute)); !errors.Is(err, ErrActionPrincipalMismatch) {
		t.Fatalf("wrong principal consume error=%v", err)
	}
	if _, err := store.Consume(context.Background(), draft.ID, draft.Principal, "changed-digest", now.Add(time.Minute)); !errors.Is(err, ErrActionDigestMismatch) {
		t.Fatalf("changed digest consume error=%v", err)
	}
	if _, err := store.Consume(context.Background(), draft.ID, draft.Principal, draft.PayloadDigest, now.Add(time.Minute)); err != nil {
		t.Fatalf("valid consume: %v", err)
	}
	if _, err := store.Consume(context.Background(), draft.ID, draft.Principal, draft.PayloadDigest, now.Add(2*time.Minute)); !errors.Is(err, ErrActionAlreadyConsumed) {
		t.Fatalf("replay consume error=%v", err)
	}
}

func TestApprovalStoreRejectsExpiredDraft(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Consume(context.Background(), draft.ID, draft.Principal, draft.PayloadDigest, draft.ExpiresAt); !errors.Is(err, ErrActionExpired) {
		t.Fatalf("expired consume error=%v", err)
	}
}

func TestApprovalStoreSurvivesRestartAndDeduplicatesSourceAction(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "actions.json")
	first, err := OpenApprovalStore(path)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := first.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenApprovalStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := second.Get(context.Background(), draft.ID, draft.Principal)
	if err != nil || loaded.PayloadDigest != draft.PayloadDigest {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	bySource, err := second.FindByDedupeKey(context.Background(), draft.DedupeKey)
	if err != nil || bySource.ID != draft.ID {
		t.Fatalf("dedupe lookup=%+v err=%v", bySource, err)
	}
	duplicate, err := second.Create(context.Background(), testDraft(now))
	if err != nil || duplicate.ID != draft.ID {
		t.Fatalf("duplicate=%+v err=%v, want the existing pending draft", duplicate, err)
	}
}

func TestApprovalStoreCanCancelExactPendingDraftAndAllowReplacement(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenApprovalStore(filepath.Join(t.TempDir(), "actions.json"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.Create(context.Background(), testDraft(now))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(context.Background(), draft.ID, draft.Principal, "wrong", now.Add(time.Minute)); !errors.Is(err, ErrActionDigestMismatch) {
		t.Fatalf("wrong-digest cancellation error=%v", err)
	}
	if err := store.Cancel(context.Background(), draft.ID, draft.Principal, draft.PayloadDigest, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.Create(context.Background(), testDraft(now.Add(2*time.Minute)))
	if err != nil || replacement.ID == draft.ID {
		t.Fatalf("replacement=%+v err=%v; canceled source action must get a fresh proposal", replacement, err)
	}
}
