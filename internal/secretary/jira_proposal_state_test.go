package secretary

import (
	"testing"
	"time"
)

func TestJiraProposalStorePersistsConversationScopedClarification(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	path := t.TempDir()
	first, err := OpenJiraProposalStore(path)
	if err != nil {
		t.Fatal(err)
	}
	principal := JiraActionPrincipal{OperatorID: 17, Channel: "telegram", ConversationID: "17"}
	proposal := PendingJiraProposal{ID: "0123456789abcdef0123456789abcdef", Principal: principal, Candidate: readyTaskCandidate(), QuestionDimension: TaskDimensionProject, Question: "¿Proyecto?", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := first.Save(proposal); err != nil {
		t.Fatal(err)
	}
	second, err := OpenJiraProposalStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, found, err := second.Get(principal, now.Add(time.Minute))
	if err != nil || !found || loaded.ID != proposal.ID || loaded.QuestionDimension != TaskDimensionProject || loaded.Candidate.SourceSpan != proposal.Candidate.SourceSpan {
		t.Fatalf("loaded=%+v found=%v err=%v", loaded, found, err)
	}
	other := principal
	other.ConversationID = "other-chat"
	if _, found, err := second.Get(other, now.Add(time.Minute)); err != nil || found {
		t.Fatalf("proposal leaked across conversations: found=%v err=%v", found, err)
	}
}

func TestJiraProposalStoreExpiresAndDeletesPendingQuestion(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	store, err := OpenJiraProposalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	principal := JiraActionPrincipal{OperatorID: 17, Channel: "openwebui", ConversationID: "chat-1"}
	proposal := PendingJiraProposal{ID: "0123456789abcdef0123456789abcdef", Principal: principal, Candidate: readyTaskCandidate(), QuestionDimension: TaskDimensionAction, Question: "¿Crear?", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(principal, proposal.ExpiresAt); err != nil || found {
		t.Fatalf("expired proposal still available: found=%v err=%v", found, err)
	}
	proposal.ExpiresAt = now.Add(10 * time.Minute)
	if err := store.Save(proposal); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(principal); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(principal, now.Add(time.Minute)); err != nil || found {
		t.Fatalf("deleted proposal still available: found=%v err=%v", found, err)
	}
}
