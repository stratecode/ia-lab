package donnaidentity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCandidate() MappingCandidate {
	return MappingCandidate{
		OperatorID:     42,
		Channel:        "telegram",
		ConversationID: "chat-7",
		Kind:           EntityPerson,
		CanonicalID:    "jira-account-17",
		CanonicalLabel: "Valentina Ruiz",
		Provider:       "jira",
		ExternalID:     "jira-account-17",
		Aliases:        []string{"Valentina"},
		Evidence:       []string{"Jira assignee account jira-account-17"},
		Status:         MappingPending,
	}
}

func TestStoreIsolatesMappingsByOperator(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateCandidate(context.Background(), testCandidate())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Get(context.Background(), 99, candidate.ID); !errors.Is(err, ErrMappingNotFound) {
		t.Fatalf("Get by another operator error = %v, want ErrMappingNotFound", err)
	}
	items, err := store.List(context.Background(), 99, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("operator 99 received %d mappings, want none", len(items))
	}
}

func TestStoreConfirmsMappingOnlyForMatchingContextAndDigest(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateCandidate(context.Background(), testCandidate())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.SetStatus(context.Background(), candidate.OperatorID, candidate.ID, candidate.Channel, "other-chat", candidate.PayloadDigest, MappingApproved, candidate.OperatorID, time.Now()); !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("confirmation from another conversation error = %v, want ErrMappingConflict", err)
	}
	if _, err := store.SetStatus(context.Background(), candidate.OperatorID, candidate.ID, candidate.Channel, candidate.ConversationID, "stale-digest", MappingApproved, candidate.OperatorID, time.Now()); !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("confirmation with stale digest error = %v, want ErrMappingConflict", err)
	}

	confirmedAt := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	approved, err := store.SetStatus(context.Background(), candidate.OperatorID, candidate.ID, candidate.Channel, candidate.ConversationID, candidate.PayloadDigest, MappingApproved, candidate.OperatorID, confirmedAt)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != MappingApproved || approved.ConfirmedBy != candidate.OperatorID || !approved.ConfirmedAt.Equal(confirmedAt) {
		t.Fatalf("approved mapping = %#v, confirmation metadata missing", approved)
	}

	if _, err := store.SetStatus(context.Background(), candidate.OperatorID, candidate.ID, candidate.Channel, candidate.ConversationID, candidate.PayloadDigest, MappingApproved, candidate.OperatorID, confirmedAt.Add(time.Minute)); err != nil {
		t.Fatalf("repeated approval should be idempotent, got %v", err)
	}
}

func TestStoreRejectsConfirmationAfterCanonicalPayloadChanges(t *testing.T) {
	store, err := NewJSONStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.CreateCandidate(context.Background(), testCandidate())
	if err != nil {
		t.Fatal(err)
	}
	changed := store.data[candidate.ID]
	changed.CanonicalLabel = "A different identity"
	store.data[candidate.ID] = changed
	if _, err := store.SetStatus(context.Background(), candidate.OperatorID, candidate.ID, candidate.Channel, candidate.ConversationID, candidate.PayloadDigest, MappingApproved, candidate.OperatorID, time.Now()); !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("confirmation after payload mutation error = %v, want ErrMappingConflict", err)
	}
	if store.data[candidate.ID].Status != MappingPending {
		t.Fatal("changed mapping was approved")
	}
}

func TestStorePersistsMappingsAcrossRestartAndKeepsPendingUnapproved(t *testing.T) {
	root := filepath.Join(t.TempDir(), "identity-state")
	store, err := NewJSONStore(root)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.CreateCandidate(context.Background(), testCandidate())
	if err != nil {
		t.Fatal(err)
	}
	pendingInput := testCandidate()
	pendingInput.CanonicalID = "granola-person-18"
	pendingInput.ExternalID = "granola-person-18"
	pendingInput.Provider = "granola"
	pendingInput.CanonicalLabel = "Valentina R."
	pending, err := store.CreateCandidate(context.Background(), pendingInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetStatus(context.Background(), approved.OperatorID, approved.ID, approved.Channel, approved.ConversationID, approved.PayloadDigest, MappingApproved, approved.OperatorID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewJSONStore(root)
	if err != nil {
		t.Fatal(err)
	}
	approvedAfterRestart, err := reopened.Get(context.Background(), approved.OperatorID, approved.ID)
	if err != nil {
		t.Fatal(err)
	}
	pendingAfterRestart, err := reopened.Get(context.Background(), pending.OperatorID, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approvedAfterRestart.Status != MappingApproved {
		t.Fatalf("approved status after restart = %q", approvedAfterRestart.Status)
	}
	if pendingAfterRestart.Status != MappingPending {
		t.Fatalf("pending status after restart = %q, want pending", pendingAfterRestart.Status)
	}
}

func TestStoreUsesPrivateStatePermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "identity-state")
	store, err := NewJSONStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCandidate(context.Background(), testCandidate()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("state directory mode = %04o, want 0700", info.Mode().Perm())
	}
}
