package semantic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

var (
	semanticIndexTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_semantic_index_total",
		Help: "Total semantic indexing operations.",
	}, []string{"source_type", "status"})
	semanticSearchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_semantic_search_total",
		Help: "Total semantic search operations.",
	}, []string{"status"})
	semanticSearchDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "orchestrator_semantic_search_duration_seconds",
		Help:    "Semantic search latency in seconds.",
		Buckets: prometheus.DefBuckets,
	})
)

type Service struct {
	cfg        config.Config
	postgres   *store.PostgresStore
	embeddings *EmbeddingClient
}

func New(cfg config.Config, postgres *store.PostgresStore) *Service {
	return &Service{
		cfg:      cfg,
		postgres: postgres,
		embeddings: NewEmbeddingClient(
			cfg.SemanticEmbeddingBaseURL,
			cfg.SemanticEmbeddingAPIKey,
			cfg.SemanticEmbeddingModel,
			cfg.SemanticEmbeddingDimensions,
			time.Duration(cfg.LlamaTimeoutSeconds)*time.Second,
		),
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.SemanticEnabled
}

func (s *Service) Index(ctx context.Context, req domain.SemanticIndexRequest) (*domain.SemanticIndexResponse, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("semantic indexing is disabled")
	}
	sourceType := strings.TrimSpace(req.SourceType)
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceType == "" || sourceID == "" {
		return nil, fmt.Errorf("source_type and source_id are required")
	}
	var (
		text          string
		metadata      map[string]any
		initiativeID  *string
		taskID        *string
		artifactID    *string
		workspaceRoot *string
	)
	switch sourceType {
	case string(domain.SemanticSourceArtifact):
		source, err := s.postgres.GetArtifactIndexSource(ctx, sourceID)
		if err != nil {
			semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
			return nil, err
		}
		if source == nil {
			semanticIndexTotal.WithLabelValues(sourceType, "not_found").Inc()
			return nil, fmt.Errorf("artifact %s not found", sourceID)
		}
		text = artifactText(source.Artifact)
		metadata = source.Artifact.Metadata
		artifactID = &source.Artifact.ID
		taskID = source.TaskID
		initiativeID = stringPtrFromMap(metadata, "initiative_id")
		workspaceRoot = stringPtrFromMap(metadata, "workspace_root")
		metadata = mergeMetadata(SanitizeMap(metadata), classifyArtifactMetadata(source.Artifact))
		if workspaceRoot == nil && taskID != nil {
			if task, _ := s.postgres.GetTask(ctx, *taskID); task != nil {
				workspaceRoot = task.WorkspacePath
				if task.InitiativeID != nil {
					initiativeID = task.InitiativeID
				}
			}
		}
	case string(domain.SemanticSourceTask):
		task, err := s.postgres.GetTask(ctx, sourceID)
		if err != nil {
			semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
			return nil, err
		}
		if task == nil {
			semanticIndexTotal.WithLabelValues(sourceType, "not_found").Inc()
			return nil, fmt.Errorf("task %s not found", sourceID)
		}
		text = taskText(task)
		metadata = mergeMetadata(SanitizeMap(task.Metadata), classifyTaskMetadata(task))
		taskID = &task.ID
		initiativeID = task.InitiativeID
		workspaceRoot = task.WorkspacePath
		if workspaceRoot == nil {
			workspaceRoot = stringPtrFromMap(metadata, "workspace_root")
		}
	case string(domain.SemanticSourceReview):
		review, err := s.postgres.GetInitiativePhaseReview(ctx, sourceID)
		if err != nil {
			semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
			return nil, err
		}
		if review == nil {
			semanticIndexTotal.WithLabelValues(sourceType, "not_found").Inc()
			return nil, fmt.Errorf("initiative phase review %s not found", sourceID)
		}
		text = reviewText(review)
		metadata = map[string]any{
			"initiative_id":        review.InitiativeID,
			"phase":                string(review.Phase),
			"decision":             string(review.Decision),
			"generated_by":         review.GeneratedBy,
			"artifact_markdown_id": review.ArtifactMarkdownID,
			"artifact_json_id":     review.ArtifactJSONID,
		}
		metadata = mergeMetadata(metadata, classifyReviewMetadata(review))
		initiativeID = &review.InitiativeID
		if initiative, _ := s.postgres.GetInitiative(ctx, review.InitiativeID); initiative != nil {
			workspaceRoot = &initiative.WorkspaceRoot
		}
	default:
		semanticIndexTotal.WithLabelValues(sourceType, "unsupported").Inc()
		return nil, fmt.Errorf("unsupported semantic source_type %q", sourceType)
	}

	cleanText := SanitizeText(text)
	if cleanText == "" {
		semanticIndexTotal.WithLabelValues(sourceType, "empty").Inc()
		return nil, fmt.Errorf("source %s/%s has no indexable text", sourceType, sourceID)
	}
	sourceHash := HashText(cleanText)
	chunks := ChunkText(cleanText, s.cfg.SemanticChunkMaxChars, s.cfg.SemanticChunkOverlapChars, s.cfg.SemanticMaxChunksPerSource)
	if err := s.postgres.DeleteSemanticChunksForSource(ctx, sourceType, sourceID); err != nil {
		semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
		return nil, err
	}
	for _, chunk := range chunks {
		embedding, err := s.embeddings.Embed(ctx, chunk.ContentText)
		if err != nil {
			semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
			return nil, err
		}
		_, err = s.postgres.UpsertSemanticChunk(ctx, store.SemanticChunkParams{
			SourceType:    sourceType,
			SourceID:      sourceID,
			InitiativeID:  initiativeID,
			TaskID:        taskID,
			ArtifactID:    artifactID,
			WorkspaceRoot: workspaceRoot,
			ChunkIndex:    chunk.Index,
			ContentText:   chunk.ContentText,
			ContentHash:   chunk.ContentHash,
			Metadata: mergeMetadata(SanitizeMap(metadata), map[string]any{
				"semantic_content_hash": sourceHash,
				"semantic_indexed_at":   time.Now().UTC().Format(time.RFC3339),
			}),
			Embedding: embedding,
		})
		if err != nil {
			semanticIndexTotal.WithLabelValues(sourceType, "error").Inc()
			return nil, err
		}
	}
	semanticIndexTotal.WithLabelValues(sourceType, "success").Inc()
	return &domain.SemanticIndexResponse{
		SourceType:  sourceType,
		SourceID:    sourceID,
		ChunkCount:  len(chunks),
		ContentHash: sourceHash,
	}, nil
}

func (s *Service) Search(ctx context.Context, req domain.SemanticSearchRequest) (*domain.SemanticSearchResponse, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("semantic search is disabled")
	}
	start := time.Now()
	query := strings.TrimSpace(SanitizeText(req.Query))
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if req.InitiativeID == nil && req.TaskID == nil && req.ArtifactID == nil && req.WorkspaceRoot == nil &&
		req.RepositoryURL == nil && req.RepoProfile == nil && req.CaseType == nil {
		return nil, fmt.Errorf("semantic search requires initiative_id, task_id, artifact_id, workspace_root, repository_url, repo_profile, or benchmark_case_type filter")
	}
	embedding, err := s.embeddings.Embed(ctx, query)
	if err != nil {
		semanticSearchTotal.WithLabelValues("error").Inc()
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = s.cfg.SemanticContextMaxChunks
	}
	items, err := s.postgres.SearchSemanticChunks(ctx, store.SemanticSearchFilter{
		Embedding:     embedding,
		SourceTypes:   req.SourceTypes,
		InitiativeID:  req.InitiativeID,
		TaskID:        req.TaskID,
		ArtifactID:    req.ArtifactID,
		WorkspaceRoot: req.WorkspaceRoot,
		RepositoryURL: req.RepositoryURL,
		RepoProfile:   req.RepoProfile,
		CaseType:      req.CaseType,
		Limit:         limit,
	})
	if err != nil {
		semanticSearchTotal.WithLabelValues("error").Inc()
		return nil, err
	}
	items = trimSearchResults(items, req.MaxChars)
	items = filterSearchResults(items, req.Outcomes, req.MinConfidence)
	semanticSearchTotal.WithLabelValues("success").Inc()
	semanticSearchDuration.Observe(time.Since(start).Seconds())
	return &domain.SemanticSearchResponse{Items: items, Total: len(items)}, nil
}

func (s *Service) GetChunk(ctx context.Context, id string) (*domain.SemanticChunkResponse, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("semantic retrieval is disabled")
	}
	return s.postgres.GetSemanticChunk(ctx, id)
}

func artifactText(artifact domain.ArtifactResponse) string {
	var b strings.Builder
	b.WriteString("Artifact: ")
	if artifact.Title != nil {
		b.WriteString(*artifact.Title)
	}
	b.WriteString("\nType: ")
	b.WriteString(artifact.ArtifactType)
	if artifact.URI != nil {
		b.WriteString("\nURI: ")
		b.WriteString(*artifact.URI)
	}
	if artifact.ContentText != nil {
		b.WriteString("\n\n")
		b.WriteString(*artifact.ContentText)
	}
	if meta := CompactMetadata(artifact.Metadata); meta != "" {
		b.WriteString("\n\nMetadata: ")
		b.WriteString(meta)
	}
	return b.String()
}

func taskText(task *domain.TaskResponse) string {
	var b strings.Builder
	b.WriteString("Task: ")
	b.WriteString(task.ID)
	b.WriteString("\nState: ")
	b.WriteString(string(task.State))
	b.WriteString("\nDescription: ")
	b.WriteString(task.Description)
	if task.ErrorMessage != nil {
		b.WriteString("\nError: ")
		b.WriteString(*task.ErrorMessage)
	}
	if task.Results != nil {
		raw, _ := json.Marshal(task.Results)
		if len(raw) > 0 {
			b.WriteString("\nResults: ")
			b.Write(raw)
		}
	}
	if meta := CompactMetadata(task.Metadata); meta != "" {
		b.WriteString("\nMetadata: ")
		b.WriteString(meta)
	}
	return b.String()
}

func reviewText(review *domain.InitiativePhaseReviewResponse) string {
	var b strings.Builder
	b.WriteString("Initiative phase review: ")
	b.WriteString(review.ID)
	b.WriteString("\nInitiative: ")
	b.WriteString(review.InitiativeID)
	b.WriteString("\nPhase: ")
	b.WriteString(string(review.Phase))
	b.WriteString("\nDecision: ")
	b.WriteString(string(review.Decision))
	if review.GeneratedBy != nil {
		b.WriteString("\nGenerated by: ")
		b.WriteString(*review.GeneratedBy)
	}
	if review.Feedback != nil {
		b.WriteString("\nFeedback: ")
		b.WriteString(*review.Feedback)
	}
	return b.String()
}

func stringPtrFromMap(input map[string]any, key string) *string {
	if input == nil {
		return nil
	}
	if value, ok := input[key].(string); ok && strings.TrimSpace(value) != "" {
		trimmed := strings.TrimSpace(value)
		return &trimmed
	}
	return nil
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func trimSearchResults(items []domain.SemanticChunkResponse, maxChars int) []domain.SemanticChunkResponse {
	if maxChars <= 0 {
		return items
	}
	remaining := maxChars
	out := make([]domain.SemanticChunkResponse, 0, len(items))
	for _, item := range items {
		if remaining <= 0 {
			break
		}
		if len([]rune(item.ContentText)) > remaining {
			item.ContentText = TruncateChars(item.ContentText, remaining)
		}
		remaining -= len([]rune(item.ContentText))
		out = append(out, item)
	}
	return out
}

func classifyArtifactMetadata(artifact domain.ArtifactResponse) map[string]any {
	out := map[string]any{
		"outcome":    "unknown",
		"confidence": 0.5,
		"validated":  false,
	}
	artifactType := strings.ToLower(strings.TrimSpace(artifact.ArtifactType))
	if strings.Contains(artifactType, "requirements") || strings.Contains(artifactType, "design") || strings.Contains(artifactType, "plan") {
		out["outcome"] = "trusted"
		out["confidence"] = 0.85
		out["validated"] = true
	}
	if phase, ok := artifact.Metadata["phase"].(string); ok && strings.TrimSpace(phase) != "" {
		out["task_type"] = phase
	}
	if repo, ok := artifact.Metadata["repo"].(string); ok && strings.TrimSpace(repo) != "" {
		out["repo"] = repo
	}
	return out
}

func classifyTaskMetadata(task *domain.TaskResponse) map[string]any {
	out := map[string]any{
		"outcome":    "unknown",
		"confidence": 0.5,
		"validated":  false,
	}
	switch task.State {
	case domain.TaskStateCompleted:
		out["outcome"] = "trusted"
		out["confidence"] = 0.8
		out["validated"] = true
	case domain.TaskStateFailed:
		out["outcome"] = "failed"
		out["confidence"] = 1.0
		out["validated"] = false
		if task.ErrorMessage != nil {
			out["failure_reason"] = *task.ErrorMessage
		}
	case domain.TaskStateCancelled:
		out["outcome"] = "invalid"
		out["confidence"] = 0.7
		out["validated"] = false
	}
	if task.AssignedAgent != nil {
		out["agent_type"] = string(*task.AssignedAgent)
	}
	if task.PlannedAgent != nil {
		out["planned_agent"] = string(*task.PlannedAgent)
	}
	out["task_type"] = string(task.TaskKind)
	out["execution_target"] = string(task.ExecutionTarget)
	if task.WorkspacePath != nil {
		out["repo"] = *task.WorkspacePath
	}
	return out
}

func classifyReviewMetadata(review *domain.InitiativePhaseReviewResponse) map[string]any {
	out := map[string]any{
		"confidence": 1.0,
		"task_type":  string(review.Phase),
	}
	switch review.Decision {
	case domain.InitiativeReviewApproved:
		out["outcome"] = "trusted"
		out["validated"] = true
	case domain.InitiativeReviewRejected:
		out["outcome"] = "rejected"
		out["validated"] = false
		if review.Feedback != nil {
			out["failure_reason"] = *review.Feedback
		}
	default:
		out["outcome"] = "unknown"
		out["validated"] = false
		out["confidence"] = 0.6
	}
	return out
}

func filterSearchResults(items []domain.SemanticChunkResponse, outcomes []string, minConfidence *float64) []domain.SemanticChunkResponse {
	if len(outcomes) == 0 && minConfidence == nil {
		return items
	}
	allowed := map[string]bool{}
	for _, outcome := range outcomes {
		outcome = strings.TrimSpace(strings.ToLower(outcome))
		if outcome != "" {
			allowed[outcome] = true
		}
	}
	out := make([]domain.SemanticChunkResponse, 0, len(items))
	for _, item := range items {
		outcome, _ := item.Metadata["outcome"].(string)
		if len(allowed) > 0 && !allowed[strings.ToLower(strings.TrimSpace(outcome))] {
			continue
		}
		if minConfidence != nil {
			confidence, ok := numericMetadata(item.Metadata["confidence"])
			if !ok || confidence < *minConfidence {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}

func numericMetadata(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
