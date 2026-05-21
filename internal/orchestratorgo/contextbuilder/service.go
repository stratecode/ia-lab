package contextbuilder

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/semantic"
	"github.com/stratecode/lab/internal/orchestratorgo/semanticir"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

var (
	contextBuildTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_context_build_total",
		Help: "Total context builder operations.",
	}, []string{"agent_type", "status"})
	contextBuildDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "orchestrator_context_build_duration_seconds",
		Help:    "Context builder latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"agent_type"})
	semanticIRCompileTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "orchestrator_semantic_ir_compile_total",
		Help: "Total semantic operational IR compile operations.",
	}, []string{"agent_type", "status"})
)

type Service struct {
	cfg      config.Config
	semantic *semantic.Service
	postgres *store.PostgresStore
}

func New(cfg config.Config, semanticService *semantic.Service, postgres *store.PostgresStore) *Service {
	return &Service{cfg: cfg, semantic: semanticService, postgres: postgres}
}

func (s *Service) Build(ctx context.Context, req domain.ContextBuildRequest) (*domain.ContextPackage, error) {
	start := time.Now()
	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		agentType = "unknown"
	}
	if s == nil || s.semantic == nil || !s.cfg.SemanticEnabled {
		return emptyPackage(req, agentType), nil
	}
	enriched := req
	if enriched.TaskID != nil && s.postgres != nil {
		if task, err := s.postgres.GetTask(ctx, *enriched.TaskID); err == nil && task != nil {
			if enriched.TaskDescription == "" {
				enriched.TaskDescription = task.Description
			}
			if enriched.Metadata == nil {
				enriched.Metadata = task.Metadata
			}
			if enriched.InitiativeID == nil {
				enriched.InitiativeID = task.InitiativeID
			}
			if enriched.WorkspaceRoot == nil {
				enriched.WorkspaceRoot = task.WorkspacePath
			}
		}
	}
	query := semantic.SanitizeText(buildQuery(enriched))
	if query == "" {
		return emptyPackage(enriched, agentType), nil
	}
	if enriched.WorkspaceRoot == nil && enriched.InitiativeID == nil {
		pkg := emptyPackage(enriched, agentType)
		pkg.Query = query
		pkg.Policies = []string{"Semantic retrieval skipped because no workspace_root or initiative_id scope was available."}
		return pkg, nil
	}
	maxChunks := enriched.MaxChunks
	if maxChunks <= 0 {
		maxChunks = s.cfg.SemanticContextMaxChunks
	}
	maxChars := enriched.MaxChars
	if maxChars <= 0 {
		maxChars = s.cfg.SemanticContextMaxChars
	}
	searchReq := domain.SemanticSearchRequest{
		Query:         query,
		SourceTypes:   allowedSources(enriched.AllowedSources),
		Outcomes:      requestedOutcomes(enriched),
		MinConfidence: enriched.MinConfidence,
		WorkspaceRoot: enriched.WorkspaceRoot,
		Limit:         maxChunks * 3,
		MaxChars:      maxChars,
	}
	if enriched.WorkspaceRoot == nil {
		searchReq.InitiativeID = enriched.InitiativeID
	}
	search, err := s.searchContext(ctx, searchReq, enriched)
	if err != nil {
		contextBuildTotal.WithLabelValues(agentType, "error").Inc()
		return nil, err
	}
	chunks := rankAndTrim(search.Items, enriched, maxChunks, maxChars)
	pkg := &domain.ContextPackage{
		AgentType:     agentType,
		TaskID:        enriched.TaskID,
		InitiativeID:  enriched.InitiativeID,
		WorkspaceRoot: enriched.WorkspaceRoot,
		Query:         query,
		Chunks:        chunks,
		SourceRefs:    sourceRefs(chunks),
		Constraints:   []string{"Use retrieved context only as supporting evidence; orchestration policy remains authoritative."},
		Policies:      []string{"Do not expose secrets. Do not retrieve outside the filtered initiative/workspace scope."},
		GeneratedAt:   time.Now().UTC(),
	}
	format := strings.TrimSpace(enriched.OutputFormat)
	if format == "" {
		format = "prompt_section"
	}
	if format == "prompt_section" || format == "both" {
		pkg.PromptSection = promptSection(chunks)
	}
	if format == "operational_ir" || format == "both" {
		pkg.OperationalIR = semanticir.Compile(semanticir.CompileRequest{
			Mode:       modeFromRequest(enriched, agentType),
			AgentType:  agentType,
			Chunks:     chunks,
			Policies:   pkg.Policies,
			SourceRefs: pkg.SourceRefs,
			MaxChars:   maxChars,
		})
		semanticIRCompileTotal.WithLabelValues(agentType, "success").Inc()
	}
	for _, chunk := range chunks {
		pkg.TotalChars += len([]rune(chunk.ContentText))
	}
	contextBuildTotal.WithLabelValues(agentType, "success").Inc()
	contextBuildDuration.WithLabelValues(agentType).Observe(time.Since(start).Seconds())
	return pkg, nil
}

func (s *Service) BuildForTask(ctx context.Context, task *domain.TaskResponse) (*domain.ContextPackage, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}
	agentType := ""
	if task.AssignedAgent != nil {
		agentType = string(*task.AssignedAgent)
	}
	includeFailed := false
	includeRejected := false
	outcomes := []string(nil)
	if task.AssignedAgent != nil && (*task.AssignedAgent == domain.AgentTypeReviewer || *task.AssignedAgent == domain.AgentTypePlanner) {
		includeFailed = true
		includeRejected = true
		outcomes = []string{"trusted", "failed", "rejected", "invalid"}
	}
	return s.Build(ctx, domain.ContextBuildRequest{
		AgentType:       agentType,
		TaskID:          &task.ID,
		InitiativeID:    task.InitiativeID,
		WorkspaceRoot:   task.WorkspacePath,
		TaskDescription: task.Description,
		Metadata:        task.Metadata,
		OutputFormat:    "both",
		Outcomes:        outcomes,
		IncludeFailed:   includeFailed,
		IncludeRejected: includeRejected,
	})
}

func (s *Service) searchContext(ctx context.Context, req domain.SemanticSearchRequest, buildReq domain.ContextBuildRequest) (*domain.SemanticSearchResponse, error) {
	if !wantsBalancedOutcomes(buildReq) {
		return s.semantic.Search(ctx, req)
	}
	groups := [][]string{{"trusted"}, {"failed", "rejected", "invalid"}}
	merged := []domain.SemanticChunkResponse{}
	seen := map[string]bool{}
	for _, outcomes := range groups {
		groupReq := req
		groupReq.Outcomes = outcomes
		groupReq.Limit = maxInt(1, req.Limit/len(groups))
		resp, err := s.semantic.Search(ctx, groupReq)
		if err != nil {
			return nil, err
		}
		for _, item := range resp.Items {
			if !seen[item.ID] {
				merged = append(merged, item)
				seen[item.ID] = true
			}
		}
	}
	if len(merged) == 0 {
		return s.semantic.Search(ctx, req)
	}
	return &domain.SemanticSearchResponse{Items: merged, Total: len(merged)}, nil
}

func wantsBalancedOutcomes(req domain.ContextBuildRequest) bool {
	hasTrusted := false
	hasInvalid := req.IncludeFailed || req.IncludeRejected
	for _, outcome := range req.Outcomes {
		switch strings.ToLower(strings.TrimSpace(outcome)) {
		case "trusted":
			hasTrusted = true
		case "failed", "rejected", "invalid":
			hasInvalid = true
		}
	}
	return hasTrusted && hasInvalid
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func emptyPackage(req domain.ContextBuildRequest, agentType string) *domain.ContextPackage {
	return &domain.ContextPackage{
		AgentType:     agentType,
		TaskID:        req.TaskID,
		InitiativeID:  req.InitiativeID,
		WorkspaceRoot: req.WorkspaceRoot,
		Chunks:        []domain.ContextChunk{},
		SourceRefs:    []string{},
		Constraints:   []string{},
		Policies:      []string{},
		GeneratedAt:   time.Now().UTC(),
	}
}

func buildQuery(req domain.ContextBuildRequest) string {
	var b strings.Builder
	b.WriteString(req.TaskDescription)
	if len(req.Metadata) > 0 {
		if goal, ok := req.Metadata["goal"].(string); ok {
			b.WriteString("\nGoal: ")
			b.WriteString(goal)
		}
		if title, ok := req.Metadata["title"].(string); ok {
			b.WriteString("\nTitle: ")
			b.WriteString(title)
		}
		if dod, ok := req.Metadata["definition_of_done"].(string); ok {
			b.WriteString("\nDefinition of done: ")
			b.WriteString(dod)
		}
		if request, ok := req.Metadata["project_request"].(map[string]any); ok {
			b.WriteString("\nProject request: ")
			b.WriteString(semantic.CompactMetadata(request))
		}
	}
	return b.String()
}

func allowedSources(input []string) []string {
	if len(input) > 0 {
		return input
	}
	return []string{
		string(domain.SemanticSourceArtifact),
		string(domain.SemanticSourceTask),
		string(domain.SemanticSourceReview),
		string(domain.SemanticSourceRepoDoc),
	}
}

func requestedOutcomes(req domain.ContextBuildRequest) []string {
	out := make([]string, 0, len(req.Outcomes)+2)
	seen := map[string]bool{}
	for _, outcome := range req.Outcomes {
		outcome = strings.TrimSpace(strings.ToLower(outcome))
		if outcome != "" && !seen[outcome] {
			out = append(out, outcome)
			seen[outcome] = true
		}
	}
	if req.IncludeFailed && !seen["failed"] {
		out = append(out, "failed")
		seen["failed"] = true
	}
	if req.IncludeRejected && !seen["rejected"] {
		out = append(out, "rejected")
	}
	return out
}

func modeFromRequest(req domain.ContextBuildRequest, agentType string) string {
	if req.Metadata != nil {
		if mode, ok := req.Metadata["mode"].(string); ok && strings.TrimSpace(mode) != "" {
			return strings.TrimSpace(mode)
		}
	}
	switch strings.ToLower(strings.TrimSpace(agentType)) {
	case "planner":
		return "PLAN_MODE"
	case "coder":
		return "CODE_MODE"
	case "reviewer":
		return "REVIEW_MODE"
	case "researcher":
		return "RESEARCH_MODE"
	default:
		return strings.ToUpper(strings.TrimSpace(agentType))
	}
}

func rankAndTrim(items []domain.SemanticChunkResponse, req domain.ContextBuildRequest, maxChunks, maxChars int) []domain.ContextChunk {
	sort.SliceStable(items, func(i, j int) bool {
		return adjustedScore(items[i], req) > adjustedScore(items[j], req)
	})
	out := make([]domain.ContextChunk, 0, maxChunks)
	remaining := maxChars
	for _, item := range items {
		if len(out) >= maxChunks || remaining <= 0 {
			break
		}
		text := item.ContentText
		if len([]rune(text)) > remaining {
			text = semantic.TruncateChars(text, remaining)
		}
		chunk := domain.ContextChunk{
			ID:           item.ID,
			SourceType:   item.SourceType,
			SourceID:     item.SourceID,
			ChunkIndex:   item.ChunkIndex,
			ContentText:  text,
			Metadata:     semantic.SanitizeMap(item.Metadata),
			Score:        item.Score,
			SourceRef:    fmt.Sprintf("%s:%s#%d", item.SourceType, item.SourceID, item.ChunkIndex),
			InitiativeID: item.InitiativeID,
			TaskID:       item.TaskID,
			ArtifactID:   item.ArtifactID,
		}
		out = append(out, chunk)
		remaining -= len([]rune(text))
	}
	return out
}

func adjustedScore(item domain.SemanticChunkResponse, req domain.ContextBuildRequest) float64 {
	score := 0.0
	if item.Score != nil {
		score = *item.Score
	}
	if req.InitiativeID != nil && item.InitiativeID != nil && *req.InitiativeID == *item.InitiativeID {
		score += 0.12
	}
	if req.TaskID != nil && item.TaskID != nil && *req.TaskID == *item.TaskID {
		score += 0.08
	}
	if item.SourceType == string(domain.SemanticSourceArtifact) {
		score += 0.04
	}
	if item.Metadata != nil {
		if failureClass, ok := item.Metadata["failure_class"].(string); ok && strings.TrimSpace(failureClass) != "" {
			score += 0.08
		}
	}
	return score
}

func sourceRefs(chunks []domain.ContextChunk) []string {
	refs := make([]string, 0, len(chunks))
	seen := map[string]bool{}
	for _, chunk := range chunks {
		if !seen[chunk.SourceRef] {
			refs = append(refs, chunk.SourceRef)
			seen[chunk.SourceRef] = true
		}
	}
	return refs
}

func promptSection(chunks []domain.ContextChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Retrieved semantic context:\n")
	for i, chunk := range chunks {
		b.WriteString(fmt.Sprintf("\n[%d] %s\n", i+1, chunk.SourceRef))
		b.WriteString(chunk.ContentText)
		b.WriteByte('\n')
	}
	return b.String()
}
