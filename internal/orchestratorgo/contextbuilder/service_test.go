package contextbuilder

import (
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestRankAndTrimPrioritizesSameInitiativeAndFailures(t *testing.T) {
	initiativeID := "11111111-1111-1111-1111-111111111111"
	otherID := "22222222-2222-2222-2222-222222222222"
	items := []domain.SemanticChunkResponse{
		{
			ID:           "other",
			SourceType:   string(domain.SemanticSourceArtifact),
			SourceID:     otherID,
			InitiativeID: &otherID,
			ContentText:  "other initiative",
			Score:        floatPtr(0.90),
			Metadata:     map[string]any{},
		},
		{
			ID:           "same-failure",
			SourceType:   string(domain.SemanticSourceTask),
			SourceID:     initiativeID,
			InitiativeID: &initiativeID,
			ContentText:  "same initiative failure",
			Score:        floatPtr(0.75),
			Metadata:     map[string]any{"failure_class": "test_failure"},
		},
	}
	chunks := rankAndTrim(items, domain.ContextBuildRequest{InitiativeID: &initiativeID}, 2, 1000)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ID != "same-failure" {
		t.Fatalf("expected same initiative failure first, got %s", chunks[0].ID)
	}
}

func floatPtr(value float64) *float64 {
	return &value
}
