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
	enriched.MemoryMode, enriched.MemoryStrategy = resolveMemorySettings(enriched)
	query := semantic.SanitizeText(buildQuery(enriched))
	if query == "" {
		return emptyPackage(enriched, agentType), nil
	}
	if enriched.MemoryMode == "off" {
		pkg := emptyPackage(enriched, agentType)
		pkg.Query = query
		pkg.Policies = []string{"Semantic retrieval skipped because memory_mode=off."}
		return pkg, nil
	}
	if enriched.WorkspaceRoot == nil && enriched.InitiativeID == nil && !hasRepoMemoryScope(enriched.Metadata) {
		pkg := emptyPackage(enriched, agentType)
		pkg.Query = query
		pkg.Policies = []string{"Semantic retrieval skipped because no workspace_root, initiative_id, or repository memory scope was available."}
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
	searchLimit := initialSearchLimit(maxChunks, benchmarkLeague(enriched))
	searchMaxChars := initialSearchMaxChars(maxChars, benchmarkLeague(enriched))
	searchReq := domain.SemanticSearchRequest{
		Query:         query,
		SourceTypes:   allowedSources(enriched.AllowedSources),
		Outcomes:      requestedOutcomes(enriched),
		MinConfidence: enriched.MinConfidence,
		WorkspaceRoot: enriched.WorkspaceRoot,
		Limit:         searchLimit,
		MaxChars:      searchMaxChars,
	}
	applyRepoMemoryScope(&searchReq, enriched)
	if shouldScopeSearchToInitiative(enriched) {
		searchReq.InitiativeID = enriched.InitiativeID
	}
	search, err := s.searchContext(ctx, searchReq, enriched)
	if err != nil {
		contextBuildTotal.WithLabelValues(agentType, "error").Inc()
		return nil, err
	}
	chunks := rankAndTrim(search.Items, enriched, maxChunks, maxChars)
	pkg := &domain.ContextPackage{
		AgentType:      agentType,
		TaskID:         enriched.TaskID,
		InitiativeID:   enriched.InitiativeID,
		WorkspaceRoot:  enriched.WorkspaceRoot,
		Query:          query,
		MemoryMode:     enriched.MemoryMode,
		MemoryStrategy: enriched.MemoryStrategy,
		Chunks:         chunks,
		SourceRefs:     sourceRefs(chunks),
		Constraints:    []string{"Use retrieved context only as supporting evidence; orchestration policy remains authoritative."},
		Policies:       []string{"Do not expose secrets. Do not retrieve outside the filtered initiative/workspace scope."},
		GeneratedAt:    time.Now().UTC(),
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
		MemoryMode:      firstNonEmptyMetadata(task.Metadata, "context_memory_mode", "benchmark_memory_mode", "memory_mode"),
		MemoryStrategy:  firstNonEmptyMetadata(task.Metadata, "context_memory_strategy", "benchmark_memory_strategy", "memory_strategy"),
		OutputFormat:    "both",
		Outcomes:        outcomes,
		IncludeFailed:   includeFailed,
		IncludeRejected: includeRejected,
	})
}

func (s *Service) searchContext(ctx context.Context, req domain.SemanticSearchRequest, buildReq domain.ContextBuildRequest) (*domain.SemanticSearchResponse, error) {
	variants := searchVariants(req, buildReq)
	if len(variants) == 0 {
		variants = []domain.SemanticSearchRequest{req}
	}
	merged := []domain.SemanticChunkResponse{}
	seen := map[string]bool{}
	for _, variant := range variants {
		resp, err := s.searchSingleContext(ctx, variant, buildReq)
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
	if isTransferLeague(benchmarkLeague(buildReq)) {
		merged = diversifyTransferCandidates(merged, buildReq)
	}
	if len(merged) == 0 && len(variants) > 1 {
		return s.searchSingleContext(ctx, req, buildReq)
	}
	return &domain.SemanticSearchResponse{Items: merged, Total: len(merged)}, nil
}

func (s *Service) searchSingleContext(ctx context.Context, req domain.SemanticSearchRequest, buildReq domain.ContextBuildRequest) (*domain.SemanticSearchResponse, error) {
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

func searchVariants(req domain.SemanticSearchRequest, buildReq domain.ContextBuildRequest) []domain.SemanticSearchRequest {
	switch benchmarkLeague(buildReq) {
	case "technology_transfer":
		return uniqueSearchVariants(
			req,
			searchVariant(req, "runtime_or_stack", "language", "framework"),
			searchVariant(req, "runtime_or_stack", "language"),
			searchVariant(req, "framework", "problem_domain"),
			searchVariant(req, "language", "problem_domain", "error_class"),
		)
	case "pattern_transfer":
		return uniqueSearchVariants(
			req,
			searchVariant(req, "problem_domain", "error_class", "fix_pattern", "validation_pattern"),
			searchVariant(req, "problem_domain", "fix_pattern", "validation_pattern"),
			searchVariant(req, "problem_domain", "error_class"),
			searchVariant(req, "case_type", "problem_domain"),
			searchVariant(req, "framework", "problem_domain", "validation_pattern"),
		)
	default:
		return []domain.SemanticSearchRequest{req}
	}
}

func uniqueSearchVariants(variants ...domain.SemanticSearchRequest) []domain.SemanticSearchRequest {
	out := make([]domain.SemanticSearchRequest, 0, len(variants))
	seen := map[string]bool{}
	for _, variant := range variants {
		sig := searchVariantSignature(variant)
		if sig == "" || seen[sig] {
			continue
		}
		seen[sig] = true
		out = append(out, variant)
	}
	return out
}

func searchVariant(req domain.SemanticSearchRequest, fields ...string) domain.SemanticSearchRequest {
	variant := req
	variant.RepositoryURL = nil
	variant.RepoProfile = nil
	variant.CaseType = nil
	variant.RuntimeOrStack = nil
	variant.Language = nil
	variant.Framework = nil
	variant.ProblemDomain = nil
	variant.ErrorClass = nil
	variant.FixPattern = nil
	variant.ValidationPattern = nil
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[strings.TrimSpace(strings.ToLower(field))] = true
	}
	if allowed["repository_url"] {
		variant.RepositoryURL = req.RepositoryURL
	}
	if allowed["repo_profile"] {
		variant.RepoProfile = req.RepoProfile
	}
	if allowed["case_type"] {
		variant.CaseType = req.CaseType
	}
	if allowed["runtime_or_stack"] {
		variant.RuntimeOrStack = req.RuntimeOrStack
	}
	if allowed["language"] {
		variant.Language = req.Language
	}
	if allowed["framework"] {
		variant.Framework = req.Framework
	}
	if allowed["problem_domain"] {
		variant.ProblemDomain = req.ProblemDomain
	}
	if allowed["error_class"] {
		variant.ErrorClass = req.ErrorClass
	}
	if allowed["fix_pattern"] {
		variant.FixPattern = req.FixPattern
	}
	if allowed["validation_pattern"] {
		variant.ValidationPattern = req.ValidationPattern
	}
	return variant
}

func searchVariantSignature(req domain.SemanticSearchRequest) string {
	parts := []string{
		derefString(req.RepositoryURL),
		derefString(req.RepoProfile),
		derefString(req.CaseType),
		derefString(req.RuntimeOrStack),
		derefString(req.Language),
		derefString(req.Framework),
		derefString(req.ProblemDomain),
		derefString(req.ErrorClass),
		derefString(req.FixPattern),
		derefString(req.ValidationPattern),
	}
	if strings.TrimSpace(strings.Join(parts, "")) == "" {
		return ""
	}
	return strings.Join(parts, "|")
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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

func initialSearchLimit(maxChunks int, league string) int {
	base := maxInt(8, maxChunks*3)
	if isTransferLeague(league) {
		return maxInt(24, maxChunks*12)
	}
	return base
}

func initialSearchMaxChars(maxChars int, league string) int {
	if maxChars <= 0 {
		return 0
	}
	if isTransferLeague(league) {
		return maxChars * 8
	}
	return maxChars * 2
}

func isTransferLeague(league string) bool {
	switch strings.TrimSpace(strings.ToLower(league)) {
	case "technology_transfer", "pattern_transfer", "negative_transfer":
		return true
	default:
		return false
	}
}

func diversifyTransferCandidates(items []domain.SemanticChunkResponse, req domain.ContextBuildRequest) []domain.SemanticChunkResponse {
	if len(items) == 0 {
		return items
	}
	caseCap := 2
	sourceCap := 1
	league := benchmarkLeague(req)
	if league == "technology_transfer" {
		caseCap = 3
		sourceCap = 2
	}
	seenByCase := map[string]int{}
	seenBySource := map[string]int{}
	out := make([]domain.SemanticChunkResponse, 0, len(items))
	for _, item := range items {
		caseKey := strings.TrimSpace(metadataString(item.Metadata, "benchmark_case_id"))
		if caseKey == "" {
			caseKey = strings.TrimSpace(item.SourceID)
		}
		sourceKey := strings.TrimSpace(item.SourceType) + ":" + strings.TrimSpace(item.SourceID)
		if caseKey != "" && seenByCase[caseKey] >= caseCap {
			continue
		}
		if sourceKey != ":" && seenBySource[sourceKey] >= sourceCap {
			continue
		}
		out = append(out, item)
		if caseKey != "" {
			seenByCase[caseKey]++
		}
		if sourceKey != ":" {
			seenBySource[sourceKey]++
		}
	}
	if len(out) == 0 {
		return items
	}
	return out
}

func emptyPackage(req domain.ContextBuildRequest, agentType string) *domain.ContextPackage {
	return &domain.ContextPackage{
		AgentType:      agentType,
		TaskID:         req.TaskID,
		InitiativeID:   req.InitiativeID,
		WorkspaceRoot:  req.WorkspaceRoot,
		MemoryMode:     req.MemoryMode,
		MemoryStrategy: req.MemoryStrategy,
		Chunks:         []domain.ContextChunk{},
		SourceRefs:     []string{},
		Constraints:    []string{},
		Policies:       []string{},
		GeneratedAt:    time.Now().UTC(),
	}
}

func buildQuery(req domain.ContextBuildRequest) string {
	var b strings.Builder
	league := benchmarkLeague(req)
	transferLeague := league == "technology_transfer" || league == "pattern_transfer" || league == "negative_transfer"
	if transferLeague {
		switch league {
		case "technology_transfer":
			b.WriteString("Cross-repo technology transfer benchmark.")
		case "pattern_transfer":
			b.WriteString("Cross-repo pattern transfer benchmark.")
		case "negative_transfer":
			b.WriteString("Cross-repo negative transfer benchmark.")
		}
		if caseType, ok := firstStringFromMetadata(req.Metadata, "benchmark_case_type"); ok {
			b.WriteString("\nBenchmark case type: ")
			b.WriteString(caseType)
		}
	} else if goal, ok := firstStringFromMetadata(req.Metadata, "goal", "initiative_goal"); ok {
		b.WriteString(goal)
		if strings.TrimSpace(req.TaskDescription) != "" {
			b.WriteString("\nTask: ")
			b.WriteString(req.TaskDescription)
		}
	} else {
		b.WriteString(req.TaskDescription)
	}
	if len(req.Metadata) > 0 {
		if !transferLeague {
			if repoURL, ok := firstStringFromMetadata(req.Metadata, "repository_url", "repo_url"); ok {
				b.WriteString("\nRepository URL: ")
				b.WriteString(repoURL)
			}
			if repoProfile, ok := firstStringFromMetadata(req.Metadata, "repo_profile"); ok {
				b.WriteString("\nRepository profile: ")
				b.WriteString(repoProfile)
			}
			if caseID, ok := firstStringFromMetadata(req.Metadata, "benchmark_case_id"); ok {
				b.WriteString("\nBenchmark case: ")
				b.WriteString(caseID)
			}
			if caseType, ok := firstStringFromMetadata(req.Metadata, "benchmark_case_type"); ok {
				b.WriteString("\nBenchmark case type: ")
				b.WriteString(caseType)
			}
		}
		if league != "pattern_transfer" {
			appendMetadataLine(&b, req.Metadata, "Runtime", "runtime_or_stack")
			appendMetadataLine(&b, req.Metadata, "Language", "language")
			appendMetadataLine(&b, req.Metadata, "Framework", "framework")
		}
		appendMetadataLine(&b, req.Metadata, "Problem domain", "problem_domain")
		appendMetadataLine(&b, req.Metadata, "Error class", "error_class")
		appendMetadataLine(&b, req.Metadata, "Fix pattern", "fix_pattern")
		appendMetadataLine(&b, req.Metadata, "Validation pattern", "validation_pattern")
		if !transferLeague {
			if goal, ok := firstStringFromMetadata(req.Metadata, "goal"); ok {
				b.WriteString("\nGoal: ")
				b.WriteString(goal)
			}
			if title, ok := firstStringFromMetadata(req.Metadata, "title"); ok {
				b.WriteString("\nTitle: ")
				b.WriteString(title)
			}
			if dod, ok := firstStringFromMetadata(req.Metadata, "definition_of_done"); ok {
				b.WriteString("\nDefinition of done: ")
				b.WriteString(dod)
			}
			if request, ok := req.Metadata["project_request"].(map[string]any); ok {
				appendMetadataLine(&b, request, "Project type", "project_type")
				appendMetadataLine(&b, request, "Project root", "project_root")
				appendMetadataLine(&b, request, "Test focus", "test_focus")
				appendMetadataLine(&b, request, "Sequence", "sequence_id")
				appendMetadataLine(&b, request, "Sequence position", "sequence_position")
			}
		}
	}
	return semantic.TruncateChars(b.String(), 1200)
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

func applyRepoMemoryScope(searchReq *domain.SemanticSearchRequest, req domain.ContextBuildRequest) {
	if searchReq == nil {
		return
	}
	repoURL := strings.TrimSpace(metadataString(req.Metadata, "repository_url", "repo_url"))
	repoProfile := strings.TrimSpace(metadataString(req.Metadata, "repo_profile"))
	caseType := strings.TrimSpace(metadataString(req.Metadata, "benchmark_case_type"))
	runtimeOrStack := strings.TrimSpace(metadataString(req.Metadata, "runtime_or_stack"))
	language := strings.TrimSpace(metadataString(req.Metadata, "language"))
	framework := strings.TrimSpace(metadataString(req.Metadata, "framework"))
	problemDomain := strings.TrimSpace(metadataString(req.Metadata, "problem_domain"))
	errorClass := strings.TrimSpace(metadataString(req.Metadata, "error_class"))
	fixPattern := strings.TrimSpace(metadataString(req.Metadata, "fix_pattern"))
	validationPattern := strings.TrimSpace(metadataString(req.Metadata, "validation_pattern"))
	strategy := effectiveMemoryStrategy(req)
	if repoURL == "" && repoProfile == "" && caseType == "" &&
		runtimeOrStack == "" && language == "" && framework == "" &&
		problemDomain == "" && errorClass == "" && fixPattern == "" &&
		validationPattern == "" {
		return
	}
	switch strategy {
	case "repo_specific_first":
		if repoURL != "" {
			searchReq.RepositoryURL = &repoURL
		}
		if repoProfile != "" {
			searchReq.RepoProfile = &repoProfile
		}
		if caseType != "" {
			searchReq.CaseType = &caseType
		}
	case "pattern_first":
		if caseType != "" {
			searchReq.CaseType = &caseType
		}
		if problemDomain != "" {
			searchReq.ProblemDomain = &problemDomain
		}
		if errorClass != "" {
			searchReq.ErrorClass = &errorClass
		}
		if fixPattern != "" {
			searchReq.FixPattern = &fixPattern
		}
		if validationPattern != "" {
			searchReq.ValidationPattern = &validationPattern
		}
		if framework != "" {
			searchReq.Framework = &framework
		}
	case "technology_first":
		if runtimeOrStack != "" {
			searchReq.RuntimeOrStack = &runtimeOrStack
		}
		if language != "" {
			searchReq.Language = &language
		}
		if framework != "" {
			searchReq.Framework = &framework
		}
		if caseType != "" {
			searchReq.CaseType = &caseType
		}
		if errorClass != "" {
			searchReq.ErrorClass = &errorClass
		}
		if fixPattern != "" {
			searchReq.FixPattern = &fixPattern
		}
		if validationPattern != "" {
			searchReq.ValidationPattern = &validationPattern
		}
	default:
		if caseType != "" {
			searchReq.CaseType = &caseType
		}
		if runtimeOrStack != "" {
			searchReq.RuntimeOrStack = &runtimeOrStack
		}
		if language != "" {
			searchReq.Language = &language
		}
		if framework != "" {
			searchReq.Framework = &framework
		}
		if problemDomain != "" {
			searchReq.ProblemDomain = &problemDomain
		}
		if errorClass != "" {
			searchReq.ErrorClass = &errorClass
		}
		if fixPattern != "" {
			searchReq.FixPattern = &fixPattern
		}
		if validationPattern != "" {
			searchReq.ValidationPattern = &validationPattern
		}
		if repoProfile != "" {
			searchReq.RepoProfile = &repoProfile
		}
		if repoURL != "" {
			searchReq.RepositoryURL = &repoURL
		}
	}
	if strategy == "repo_specific_first" || strategy == "pattern_first" || strategy == "technology_first" {
		searchReq.WorkspaceRoot = nil
	}
}

func hasRepoMemoryScope(metadata map[string]any) bool {
	return strings.TrimSpace(metadataString(metadata, "repository_url", "repo_url")) != "" ||
		strings.TrimSpace(metadataString(metadata, "repo_profile")) != "" ||
		strings.TrimSpace(metadataString(metadata, "benchmark_case_type")) != "" ||
		strings.TrimSpace(metadataString(metadata, "runtime_or_stack")) != "" ||
		strings.TrimSpace(metadataString(metadata, "language")) != "" ||
		strings.TrimSpace(metadataString(metadata, "framework")) != "" ||
		strings.TrimSpace(metadataString(metadata, "problem_domain")) != "" ||
		strings.TrimSpace(metadataString(metadata, "error_class")) != "" ||
		strings.TrimSpace(metadataString(metadata, "fix_pattern")) != "" ||
		strings.TrimSpace(metadataString(metadata, "validation_pattern")) != ""
}

func shouldScopeSearchToInitiative(req domain.ContextBuildRequest) bool {
	return req.WorkspaceRoot == nil && !hasRepoMemoryScope(req.Metadata)
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
		if shouldExcludeCurrentInitiativeMemory(item, req) {
			continue
		}
		text := item.ContentText
		if len([]rune(text)) > remaining {
			text = semantic.TruncateChars(text, remaining)
		}
		metadata := semantic.SanitizeMap(item.Metadata)
		matchType := memoryMatchType(item, req)
		if matchType != "" {
			metadata["memory_match_type"] = matchType
		}
		chunk := domain.ContextChunk{
			ID:           item.ID,
			SourceType:   item.SourceType,
			SourceID:     item.SourceID,
			ChunkIndex:   item.ChunkIndex,
			ContentText:  text,
			Metadata:     metadata,
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

func shouldExcludeCurrentInitiativeMemory(item domain.SemanticChunkResponse, req domain.ContextBuildRequest) bool {
	if metadataString(req.Metadata, "benchmark_case_id") == "" {
		return false
	}
	if req.InitiativeID != nil && item.InitiativeID != nil && *req.InitiativeID == *item.InitiativeID {
		return true
	}
	league := benchmarkLeague(req)
	if league == "technology_transfer" || league == "pattern_transfer" || league == "negative_transfer" {
		caseID := metadataString(req.Metadata, "benchmark_case_id")
		itemCaseID := metadataString(item.Metadata, "benchmark_case_id")
		if caseID != "" && itemCaseID != "" && caseID == itemCaseID {
			return true
		}
		repoURL := metadataString(req.Metadata, "repository_url", "repo_url")
		itemRepoURL := metadataString(item.Metadata, "repository_url", "repo_url")
		if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
			return true
		}
	}
	return false
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
	repoURL := metadataString(req.Metadata, "repository_url", "repo_url")
	itemRepoURL := metadataString(item.Metadata, "repository_url", "repo_url")
	repoProfile := metadataString(req.Metadata, "repo_profile")
	itemRepoProfile := metadataString(item.Metadata, "repo_profile")
	caseType := metadataString(req.Metadata, "benchmark_case_type")
	itemCaseType := metadataString(item.Metadata, "benchmark_case_type")
	runtimeOrStack := metadataString(req.Metadata, "runtime_or_stack")
	itemRuntime := metadataString(item.Metadata, "runtime_or_stack")
	language := metadataString(req.Metadata, "language")
	itemLanguage := metadataString(item.Metadata, "language")
	framework := metadataString(req.Metadata, "framework")
	itemFramework := metadataString(item.Metadata, "framework")
	problemDomain := metadataString(req.Metadata, "problem_domain")
	itemProblemDomain := metadataString(item.Metadata, "problem_domain")
	errorClass := metadataString(req.Metadata, "error_class")
	itemErrorClass := metadataString(item.Metadata, "error_class")
	fixPattern := metadataString(req.Metadata, "fix_pattern")
	itemFixPattern := metadataString(item.Metadata, "fix_pattern")
	validationPattern := metadataString(req.Metadata, "validation_pattern")
	itemValidationPattern := metadataString(item.Metadata, "validation_pattern")
	switch benchmarkLeague(req) {
	case "repo_recall":
		if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
			score += 0.10
		}
		if language != "" && itemLanguage != "" && language == itemLanguage {
			score += 0.08
		}
		if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
			score += 0.16
		}
		if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
			score += 0.14
		}
		if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
			score += 0.14
		}
		if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
			score += 0.08
		}
		if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
			score += 0.22
		}
		if repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile {
			score += 0.14
		}
		if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
			score += 0.08
		}
	case "pattern_transfer":
		if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
			score += 0.16
		}
		if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
			score += 0.28
		}
		if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
			score += 0.18
		}
		if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
			score += 0.18
		}
		if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
			score += 0.14
		}
		if framework != "" && itemFramework != "" && framework == itemFramework {
			score += 0.10
		}
		if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
			score += 0.04
		}
		if language != "" && itemLanguage != "" && language == itemLanguage {
			score += 0.04
		}
	case "technology_transfer":
		if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
			score += 0.24
		}
		if language != "" && itemLanguage != "" && language == itemLanguage {
			score += 0.20
		}
		if framework != "" && itemFramework != "" && framework == itemFramework {
			score += 0.18
		}
		if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
			score += 0.10
		}
		if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
			score += 0.10
		}
		if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
			score += 0.10
		}
		if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
			score += 0.08
		}
		if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
			score += 0.06
		}
	case "negative_transfer":
		if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
			score -= 0.10
		}
		if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
			score -= 0.06
		}
		if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
			score -= 0.04
		}
	default:
		strategy := effectiveMemoryStrategy(req)
		switch strategy {
		case "repo_specific_first":
			if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
				score += 0.10
			}
			if language != "" && itemLanguage != "" && language == itemLanguage {
				score += 0.08
			}
			if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
				score += 0.16
			}
			if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
				score += 0.14
			}
			if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
				score += 0.14
			}
			if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
				score += 0.08
			}
			if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
				score += 0.22
			}
			if repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile {
				score += 0.14
			}
			if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
				score += 0.08
			}
		case "pattern_first":
			if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
				score += 0.16
			}
			if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
				score += 0.24
			}
			if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
				score += 0.18
			}
			if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
				score += 0.18
			}
			if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
				score += 0.12
			}
			if framework != "" && itemFramework != "" && framework == itemFramework {
				score += 0.12
			}
			if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
				score += 0.08
			}
			if language != "" && itemLanguage != "" && language == itemLanguage {
				score += 0.06
			}
			if repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile {
				score += 0.04
			}
			if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
				score += 0.02
			}
		case "technology_first":
			if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
				score += 0.22
			}
			if language != "" && itemLanguage != "" && language == itemLanguage {
				score += 0.18
			}
			if framework != "" && itemFramework != "" && framework == itemFramework {
				score += 0.18
			}
			if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
				score += 0.14
			}
			if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
				score += 0.10
			}
			if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
				score += 0.10
			}
			if validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern {
				score += 0.08
			}
			if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
				score += 0.06
			}
			if repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile {
				score += 0.03
			}
			if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
				score += 0.01
			}
		default:
			if caseType != "" && itemCaseType != "" && caseType == itemCaseType {
				score += 0.10
			}
			if problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain {
				score += 0.12
			}
			if errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass {
				score += 0.10
			}
			if fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern {
				score += 0.10
			}
			if runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime {
				score += 0.08
			}
			if language != "" && itemLanguage != "" && language == itemLanguage {
				score += 0.06
			}
			if framework != "" && itemFramework != "" && framework == itemFramework {
				score += 0.06
			}
			if repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile {
				score += 0.08
			}
			if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
				score += 0.06
			}
		}
	}
	return score
}

func resolveMemorySettings(req domain.ContextBuildRequest) (string, string) {
	mode := normalizeMemoryMode(req.MemoryMode)
	if mode == "" {
		mode = normalizeMemoryMode(metadataString(req.Metadata, "context_memory_mode", "benchmark_memory_mode", "memory_mode"))
	}
	if mode == "" {
		mode = "on"
	}
	strategy := effectiveMemoryStrategy(req)
	if strategy == "" {
		strategy = normalizeMemoryStrategy(metadataString(req.Metadata, "context_memory_strategy", "benchmark_memory_strategy", "memory_strategy"))
	}
	if strategy == "" {
		strategy = "repo_specific_first"
	}
	return mode, strategy
}

func effectiveMemoryStrategy(req domain.ContextBuildRequest) string {
	if league := benchmarkLeague(req); league != "" {
		switch league {
		case "repo_recall":
			return "repo_specific_first"
		case "technology_transfer":
			return "technology_first"
		case "pattern_transfer":
			return "pattern_first"
		case "negative_transfer":
			return "semantic_only"
		}
	}
	return normalizeMemoryStrategy(req.MemoryStrategy)
}

func benchmarkLeague(req domain.ContextBuildRequest) string {
	if req.Metadata == nil {
		return ""
	}
	return normalizeBenchmarkLeague(metadataString(req.Metadata, "benchmark_league"))
}

func normalizeMemoryMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off":
		return "off"
	case "on":
		return "on"
	default:
		return ""
	}
}

func normalizeMemoryStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "repo_specific_first":
		return "repo_specific_first"
	case "pattern_first":
		return "pattern_first"
	case "technology_first":
		return "technology_first"
	case "semantic_only":
		return "semantic_only"
	default:
		return ""
	}
}

func normalizeBenchmarkLeague(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "repo_recall":
		return "repo_recall"
	case "technology_transfer":
		return "technology_transfer"
	case "pattern_transfer":
		return "pattern_transfer"
	case "negative_transfer":
		return "negative_transfer"
	default:
		return ""
	}
}

func memoryMatchType(item domain.SemanticChunkResponse, req domain.ContextBuildRequest) string {
	repoURL := metadataString(req.Metadata, "repository_url", "repo_url")
	itemRepoURL := metadataString(item.Metadata, "repository_url", "repo_url")
	if repoURL != "" && itemRepoURL != "" && repoURL == itemRepoURL {
		return "repo_specific"
	}
	repoProfile := metadataString(req.Metadata, "repo_profile")
	itemRepoProfile := metadataString(item.Metadata, "repo_profile")
	caseType := metadataString(req.Metadata, "benchmark_case_type")
	itemCaseType := metadataString(item.Metadata, "benchmark_case_type")
	runtimeOrStack := metadataString(req.Metadata, "runtime_or_stack")
	itemRuntime := metadataString(item.Metadata, "runtime_or_stack")
	language := metadataString(req.Metadata, "language")
	itemLanguage := metadataString(item.Metadata, "language")
	framework := metadataString(req.Metadata, "framework")
	itemFramework := metadataString(item.Metadata, "framework")
	problemDomain := metadataString(req.Metadata, "problem_domain")
	itemProblemDomain := metadataString(item.Metadata, "problem_domain")
	errorClass := metadataString(req.Metadata, "error_class")
	itemErrorClass := metadataString(item.Metadata, "error_class")
	fixPattern := metadataString(req.Metadata, "fix_pattern")
	itemFixPattern := metadataString(item.Metadata, "fix_pattern")
	validationPattern := metadataString(req.Metadata, "validation_pattern")
	itemValidationPattern := metadataString(item.Metadata, "validation_pattern")
	patternMatch := (repoProfile != "" && itemRepoProfile != "" && repoProfile == itemRepoProfile) ||
		(caseType != "" && itemCaseType != "" && caseType == itemCaseType) ||
		(problemDomain != "" && itemProblemDomain != "" && problemDomain == itemProblemDomain) ||
		(errorClass != "" && itemErrorClass != "" && errorClass == itemErrorClass) ||
		(fixPattern != "" && itemFixPattern != "" && fixPattern == itemFixPattern) ||
		(validationPattern != "" && itemValidationPattern != "" && validationPattern == itemValidationPattern)
	technologyMatch := (runtimeOrStack != "" && itemRuntime != "" && runtimeOrStack == itemRuntime) ||
		(language != "" && itemLanguage != "" && language == itemLanguage) ||
		(framework != "" && itemFramework != "" && framework == itemFramework)
	if benchmarkLeague(req) == "pattern_transfer" && patternMatch {
		return "pattern_similar"
	}
	if technologyMatch {
		return "technology_similar"
	}
	if patternMatch {
		return "pattern_similar"
	}
	return "semantic_related"
}

func appendMetadataLine(b *strings.Builder, metadata map[string]any, label string, keys ...string) {
	if b == nil {
		return
	}
	if value, ok := firstStringFromMetadata(metadata, keys...); ok {
		b.WriteString("\n")
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(value)
	}
}

func metadataString(metadata map[string]any, keys ...string) string {
	if metadata == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if projectRequest, ok := metadata["project_request"].(map[string]any); ok {
		for _, key := range keys {
			if value, ok := projectRequest[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstStringFromMetadata(metadata map[string]any, keys ...string) (string, bool) {
	value := metadataString(metadata, keys...)
	return value, value != ""
}

func firstNonEmptyMetadata(metadata map[string]any, keys ...string) string {
	value, _ := firstStringFromMetadata(metadata, keys...)
	return value
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
