package semantic

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestOutcomeFromTaskIndexesOnlyOperationalTerminalStates(t *testing.T) {
	completed := &domain.TaskResponse{State: domain.TaskStateCompleted}
	failed := &domain.TaskResponse{State: domain.TaskStateFailed}
	cancelled := &domain.TaskResponse{State: domain.TaskStateCancelled}
	queued := &domain.TaskResponse{State: domain.TaskStateQueued}
	if outcomeFromTask(completed) != "trusted" {
		t.Fatal("completed task should be trusted")
	}
	if outcomeFromTask(failed) != "failed" {
		t.Fatal("failed task should be failed")
	}
	if outcomeFromTask(cancelled) != "invalid" {
		t.Fatal("cancelled task should be invalid")
	}
	if outcomeFromTask(queued) != "unknown" {
		t.Fatal("queued task should not be auto-indexed")
	}
}

func TestOutcomeFromReviewIndexesOnlyApprovedRejected(t *testing.T) {
	approved := &domain.InitiativePhaseReviewResponse{Decision: domain.InitiativeReviewApproved}
	rejected := &domain.InitiativePhaseReviewResponse{Decision: domain.InitiativeReviewRejected}
	generated := &domain.InitiativePhaseReviewResponse{Decision: domain.InitiativeReviewGenerated}
	if outcomeFromReview(approved) != "trusted" {
		t.Fatal("approved review should be trusted")
	}
	if outcomeFromReview(rejected) != "rejected" {
		t.Fatal("rejected review should be rejected")
	}
	if outcomeFromReview(generated) != "unknown" {
		t.Fatal("generated review should not be auto-indexed")
	}
}

func TestIsUsefulArtifactRequiresContentOrURI(t *testing.T) {
	content := "hello"
	uri := "file:///tmp/demo"
	if !isUsefulArtifact(domain.ArtifactResponse{ContentText: &content}) {
		t.Fatal("content artifact should be useful")
	}
	if !isUsefulArtifact(domain.ArtifactResponse{URI: &uri}) {
		t.Fatal("uri artifact should be useful")
	}
	if isUsefulArtifact(domain.ArtifactResponse{}) {
		t.Fatal("empty artifact should not be useful")
	}
}

func TestClassifyArtifactMetadataPreservesExplicitOutcome(t *testing.T) {
	artifact := domain.ArtifactResponse{
		ArtifactType: "repo_workflow_case",
		Metadata: map[string]any{
			"outcome":    "trusted",
			"confidence": 0.91,
			"validated":  true,
		},
	}
	meta := classifyArtifactMetadata(artifact)
	if got := meta["outcome"]; got != "trusted" {
		t.Fatalf("expected explicit trusted outcome to be preserved, got %#v", got)
	}
	if got := meta["validated"]; got != true {
		t.Fatalf("expected explicit validated flag to be preserved, got %#v", got)
	}
	if got := meta["confidence"]; got != 0.91 {
		t.Fatalf("expected explicit confidence to be preserved, got %#v", got)
	}
}
