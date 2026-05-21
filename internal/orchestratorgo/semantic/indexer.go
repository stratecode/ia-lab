package semantic

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

var (
	semanticAutoIndexTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_semantic_autoindex_total",
		Help: "Total best-effort semantic auto-index operations.",
	}, []string{"source_type", "outcome", "status"})
	semanticChunksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_semantic_chunks_total",
		Help: "Total semantic chunks indexed by source type and outcome.",
	}, []string{"source_type", "outcome"})
)

type Indexer struct {
	semantic *Service
	postgres *store.PostgresStore
}

func NewIndexer(semanticService *Service, postgres *store.PostgresStore) *Indexer {
	return &Indexer{semantic: semanticService, postgres: postgres}
}

func (i *Indexer) IndexTask(ctx context.Context, taskID string) {
	if i == nil || i.semantic == nil || i.postgres == nil || !i.semantic.Enabled() {
		return
	}
	task, err := i.postgres.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil || task == nil {
		i.record(string(domain.SemanticSourceTask), "unknown", "error", 0, err)
		return
	}
	outcome := outcomeFromTask(task)
	if outcome == "unknown" {
		return
	}
	i.index(ctx, string(domain.SemanticSourceTask), task.ID, outcome)
	i.IndexTaskArtifacts(ctx, task.ID)
}

func (i *Indexer) IndexArtifact(ctx context.Context, artifactID string) {
	if i == nil || i.semantic == nil || i.postgres == nil || !i.semantic.Enabled() {
		return
	}
	source, err := i.postgres.GetArtifactIndexSource(ctx, strings.TrimSpace(artifactID))
	if err != nil || source == nil {
		i.record(string(domain.SemanticSourceArtifact), "unknown", "error", 0, err)
		return
	}
	if !isUsefulArtifact(source.Artifact) {
		return
	}
	i.index(ctx, string(domain.SemanticSourceArtifact), source.Artifact.ID, outcomeFromArtifact(source.Artifact))
}

func (i *Indexer) IndexReview(ctx context.Context, reviewID string) {
	if i == nil || i.semantic == nil || i.postgres == nil || !i.semantic.Enabled() {
		return
	}
	review, err := i.postgres.GetInitiativePhaseReview(ctx, strings.TrimSpace(reviewID))
	if err != nil || review == nil {
		i.record(string(domain.SemanticSourceReview), "unknown", "error", 0, err)
		return
	}
	outcome := outcomeFromReview(review)
	if outcome == "unknown" {
		return
	}
	i.index(ctx, string(domain.SemanticSourceReview), review.ID, outcome)
}

func (i *Indexer) IndexTaskArtifacts(ctx context.Context, taskID string) {
	if i == nil || i.postgres == nil {
		return
	}
	artifacts, err := i.postgres.ListArtifactsByTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		i.record(string(domain.SemanticSourceArtifact), "unknown", "error", 0, err)
		return
	}
	for _, artifact := range artifacts {
		if isUsefulArtifact(artifact) {
			i.index(ctx, string(domain.SemanticSourceArtifact), artifact.ID, outcomeFromArtifact(artifact))
		}
	}
}

func (i *Indexer) index(ctx context.Context, sourceType, sourceID, outcome string) {
	resp, err := i.semantic.Index(ctx, domain.SemanticIndexRequest{SourceType: sourceType, SourceID: sourceID})
	if err != nil {
		i.record(sourceType, outcome, "error", 0, err)
		return
	}
	i.record(sourceType, outcome, "success", resp.ChunkCount, nil)
}

func (i *Indexer) record(sourceType, outcome, status string, chunkCount int, err error) {
	sourceType = strings.TrimSpace(sourceType)
	if sourceType == "" {
		sourceType = "unknown"
	}
	outcome = strings.TrimSpace(outcome)
	if outcome == "" {
		outcome = "unknown"
	}
	semanticAutoIndexTotal.WithLabelValues(sourceType, outcome, status).Inc()
	if status == "success" && chunkCount > 0 {
		semanticChunksTotal.WithLabelValues(sourceType, outcome).Add(float64(chunkCount))
	}
	if err != nil {
		log.Warn().Err(err).Str("source_type", sourceType).Str("outcome", outcome).Msg("semantic auto-index failed")
	}
}

func outcomeFromTask(task *domain.TaskResponse) string {
	switch task.State {
	case domain.TaskStateCompleted:
		return "trusted"
	case domain.TaskStateFailed:
		return "failed"
	case domain.TaskStateCancelled:
		return "invalid"
	default:
		return "unknown"
	}
}

func outcomeFromReview(review *domain.InitiativePhaseReviewResponse) string {
	switch review.Decision {
	case domain.InitiativeReviewApproved:
		return "trusted"
	case domain.InitiativeReviewRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func outcomeFromArtifact(artifact domain.ArtifactResponse) string {
	if outcome, ok := artifact.Metadata["outcome"].(string); ok && strings.TrimSpace(outcome) != "" {
		return strings.ToLower(strings.TrimSpace(outcome))
	}
	return "trusted"
}

func isUsefulArtifact(artifact domain.ArtifactResponse) bool {
	if artifact.ContentText != nil && strings.TrimSpace(*artifact.ContentText) != "" {
		return true
	}
	if artifact.URI != nil && strings.TrimSpace(*artifact.URI) != "" {
		return true
	}
	return false
}
