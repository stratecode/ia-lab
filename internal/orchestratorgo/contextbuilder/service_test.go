package contextbuilder

import (
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestApplyRepoMemoryScopePrefersRepoFiltersOverWorkspaceForRepoStrategies(t *testing.T) {
	workspaceRoot := "/tmp/run-a/repo"
	searchReq := domain.SemanticSearchRequest{WorkspaceRoot: &workspaceRoot}
	req := domain.ContextBuildRequest{
		MemoryStrategy: "repo_specific_first",
		Metadata: map[string]any{
			"repository_url":      "https://github.com/un33k/python-slugify",
			"repo_profile":        "python_slugify_v1",
			"benchmark_case_type": "deterministic_bugfix",
		},
	}
	applyRepoMemoryScope(&searchReq, req)
	if searchReq.WorkspaceRoot != nil {
		t.Fatalf("expected workspace scope to be cleared for repo-specific retrieval, got %v", *searchReq.WorkspaceRoot)
	}
	if searchReq.RepositoryURL == nil || *searchReq.RepositoryURL != "https://github.com/un33k/python-slugify" {
		t.Fatalf("expected repository url filter, got %#v", searchReq.RepositoryURL)
	}
	if searchReq.RepoProfile == nil || *searchReq.RepoProfile != "python_slugify_v1" {
		t.Fatalf("expected repo profile filter, got %#v", searchReq.RepoProfile)
	}
	if searchReq.CaseType == nil || *searchReq.CaseType != "deterministic_bugfix" {
		t.Fatalf("expected case type filter, got %#v", searchReq.CaseType)
	}
}

func TestApplyRepoMemoryScopeUsesTechnologyFilters(t *testing.T) {
	workspaceRoot := "/tmp/run-b/repo"
	searchReq := domain.SemanticSearchRequest{WorkspaceRoot: &workspaceRoot}
	req := domain.ContextBuildRequest{
		Metadata: map[string]any{
			"benchmark_league":   "technology_transfer",
			"runtime_or_stack":   "php",
			"language":           "php",
			"framework":          "library",
			"problem_domain":     "logging",
			"error_class":        "repository_structure_validation",
			"fix_pattern":        "deterministic_marker_before_review",
			"validation_pattern": "containerized_manifest_smoke",
		},
	}
	applyRepoMemoryScope(&searchReq, req)
	if searchReq.WorkspaceRoot != nil {
		t.Fatalf("expected workspace scope cleared for technology-first retrieval, got %v", *searchReq.WorkspaceRoot)
	}
	if searchReq.RuntimeOrStack == nil || *searchReq.RuntimeOrStack != "php" {
		t.Fatalf("expected runtime_or_stack filter, got %#v", searchReq.RuntimeOrStack)
	}
	if searchReq.Language == nil || *searchReq.Language != "php" {
		t.Fatalf("expected language filter, got %#v", searchReq.Language)
	}
	if searchReq.Framework == nil || *searchReq.Framework != "library" {
		t.Fatalf("expected framework filter, got %#v", searchReq.Framework)
	}
	if searchReq.ProblemDomain != nil {
		t.Fatalf("expected technology-first retrieval to avoid hard problem_domain filtering, got %#v", searchReq.ProblemDomain)
	}
}

func TestHasRepoMemoryScope(t *testing.T) {
	if hasRepoMemoryScope(nil) {
		t.Fatal("expected nil metadata to have no repo memory scope")
	}
	if !hasRepoMemoryScope(map[string]any{"project_request": map[string]any{"repo_profile": "python_slugify_v1"}}) {
		t.Fatal("expected repo_profile in project_request to enable repo memory scope")
	}
	if !hasRepoMemoryScope(map[string]any{"language": "php"}) {
		t.Fatal("expected technology metadata to enable memory scope")
	}
}

func TestShouldScopeSearchToInitiative(t *testing.T) {
	initiativeID := "initiative-1"
	req := domain.ContextBuildRequest{InitiativeID: &initiativeID}
	if !shouldScopeSearchToInitiative(req) {
		t.Fatal("expected current initiative scope when workspace and repo scope are absent")
	}

	req.Metadata = map[string]any{"repository_url": "https://github.com/encode/httpx"}
	if shouldScopeSearchToInitiative(req) {
		t.Fatal("expected repo-scoped retrieval to search across initiatives")
	}

	workspaceRoot := "/tmp/run/repo"
	req = domain.ContextBuildRequest{
		InitiativeID:  &initiativeID,
		WorkspaceRoot: &workspaceRoot,
	}
	if shouldScopeSearchToInitiative(req) {
		t.Fatal("expected workspace-scoped retrieval to avoid initiative-only scope")
	}
}

func TestMemoryMatchTypeTechnologyAndPattern(t *testing.T) {
	req := domain.ContextBuildRequest{Metadata: map[string]any{
		"language":           "php",
		"framework":          "library",
		"problem_domain":     "logging",
		"error_class":        "repository_structure_validation",
		"fix_pattern":        "deterministic_marker_before_review",
		"validation_pattern": "containerized_manifest_smoke",
	}}
	item := domain.SemanticChunkResponse{Metadata: map[string]any{
		"language":       "php",
		"framework":      "library",
		"problem_domain": "logging",
	}}
	if got := memoryMatchType(item, req); got != "technology_similar" {
		t.Fatalf("expected technology_similar, got %q", got)
	}
	item = domain.SemanticChunkResponse{Metadata: map[string]any{
		"error_class": "repository_structure_validation",
		"fix_pattern": "deterministic_marker_before_review",
	}}
	if got := memoryMatchType(item, req); got != "pattern_similar" {
		t.Fatalf("expected pattern_similar, got %q", got)
	}
	req = domain.ContextBuildRequest{Metadata: map[string]any{
		"benchmark_league":   "pattern_transfer",
		"language":           "javascript",
		"framework":          "library",
		"problem_domain":     "http_client",
		"error_class":        "repository_structure_validation",
		"fix_pattern":        "deterministic_marker_before_review",
		"validation_pattern": "containerized_manifest_smoke",
	}}
	item = domain.SemanticChunkResponse{Metadata: map[string]any{
		"language":       "python",
		"framework":      "library",
		"problem_domain": "http_client",
	}}
	if got := memoryMatchType(item, req); got != "pattern_similar" {
		t.Fatalf("expected pattern_similar for pattern league, got %q", got)
	}
}

func TestShouldExcludeCurrentInitiativeMemoryForBenchmark(t *testing.T) {
	initiativeID := "initiative-1"
	req := domain.ContextBuildRequest{
		InitiativeID: &initiativeID,
		Metadata:     map[string]any{"benchmark_case_id": "bench-1"},
	}
	item := domain.SemanticChunkResponse{InitiativeID: &initiativeID}
	if !shouldExcludeCurrentInitiativeMemory(item, req) {
		t.Fatal("expected benchmark retrieval to exclude current initiative memory")
	}
	other := "initiative-2"
	item = domain.SemanticChunkResponse{InitiativeID: &other}
	if shouldExcludeCurrentInitiativeMemory(item, req) {
		t.Fatal("expected different initiative memory to remain eligible")
	}

	req = domain.ContextBuildRequest{
		Metadata: map[string]any{
			"benchmark_case_id": "bench-1",
			"benchmark_league":  "technology_transfer",
			"repository_url":    "https://github.com/example/repo-a",
		},
	}
	item = domain.SemanticChunkResponse{Metadata: map[string]any{"repository_url": "https://github.com/example/repo-a"}}
	if !shouldExcludeCurrentInitiativeMemory(item, req) {
		t.Fatal("expected same-repo memory to be excluded for cross-repo benchmark strategies")
	}
	item = domain.SemanticChunkResponse{Metadata: map[string]any{"benchmark_case_id": "bench-1"}}
	if !shouldExcludeCurrentInitiativeMemory(item, req) {
		t.Fatal("expected same benchmark_case_id memory to be excluded for transfer benchmarks")
	}
}

func TestEffectiveMemoryStrategyFromLeague(t *testing.T) {
	req := domain.ContextBuildRequest{Metadata: map[string]any{"benchmark_league": "pattern_transfer"}}
	if got := effectiveMemoryStrategy(req); got != "pattern_first" {
		t.Fatalf("expected pattern_first from pattern_transfer, got %q", got)
	}
	req = domain.ContextBuildRequest{Metadata: map[string]any{"benchmark_league": "negative_transfer"}}
	if got := effectiveMemoryStrategy(req); got != "semantic_only" {
		t.Fatalf("expected semantic_only from negative_transfer, got %q", got)
	}
}

func TestResolveMemorySettingsInfersOperationalStrategyByAgent(t *testing.T) {
	req := domain.ContextBuildRequest{
		AgentType: "planner",
		Metadata: map[string]any{
			"repository_url":   "https://github.com/example/repo",
			"runtime_or_stack": "python",
			"language":         "python",
		},
	}
	mode, strategy := resolveMemorySettings(req)
	if mode != "on" {
		t.Fatalf("expected default memory mode on, got %q", mode)
	}
	if strategy != "technology_first" {
		t.Fatalf("expected planner to infer technology_first, got %q", strategy)
	}

	req.AgentType = "reviewer"
	_, strategy = resolveMemorySettings(req)
	if strategy != "pattern_first" {
		t.Fatalf("expected reviewer to infer pattern_first, got %q", strategy)
	}

	req.AgentType = "coder"
	_, strategy = resolveMemorySettings(req)
	if strategy != "repo_specific_first" {
		t.Fatalf("expected coder to infer repo_specific_first, got %q", strategy)
	}
}

func TestSearchVariantsForTechnologyTransfer(t *testing.T) {
	runtime := "php"
	language := "php"
	framework := "library"
	problemDomain := "logging"
	errorClass := "repository_structure_validation"
	req := domain.SemanticSearchRequest{
		RuntimeOrStack: &runtime,
		Language:       &language,
		Framework:      &framework,
		ProblemDomain:  &problemDomain,
		ErrorClass:     &errorClass,
	}
	buildReq := domain.ContextBuildRequest{Metadata: map[string]any{"benchmark_league": "technology_transfer"}}
	variants := searchVariants(req, buildReq)
	if len(variants) < 3 {
		t.Fatalf("expected multiple technology transfer variants, got %d", len(variants))
	}
	if variants[1].RuntimeOrStack == nil || variants[1].Language == nil {
		t.Fatalf("expected technology variant to retain runtime and language filters")
	}
	if variants[1].ProblemDomain != nil {
		t.Fatalf("expected relaxed technology variant to drop problem_domain filter, got %#v", variants[1].ProblemDomain)
	}
}

func TestSearchVariantsForPatternTransfer(t *testing.T) {
	caseType := "review_only"
	problemDomain := "http_client"
	errorClass := "repository_structure_validation"
	fixPattern := "deterministic_marker_before_review"
	validationPattern := "containerized_manifest_smoke"
	framework := "library"
	req := domain.SemanticSearchRequest{
		CaseType:          &caseType,
		ProblemDomain:     &problemDomain,
		ErrorClass:        &errorClass,
		FixPattern:        &fixPattern,
		ValidationPattern: &validationPattern,
		Framework:         &framework,
	}
	buildReq := domain.ContextBuildRequest{Metadata: map[string]any{"benchmark_league": "pattern_transfer"}}
	variants := searchVariants(req, buildReq)
	if len(variants) < 4 {
		t.Fatalf("expected multiple pattern transfer variants, got %d", len(variants))
	}
	foundRelaxed := false
	for _, variant := range variants {
		if variant.ProblemDomain != nil && variant.FixPattern != nil && variant.Framework == nil && variant.CaseType == nil {
			foundRelaxed = true
			break
		}
	}
	if !foundRelaxed {
		t.Fatal("expected at least one relaxed pattern variant focused on problem/fix signal")
	}
}

func TestRankAndTrimKeepsCrossRepoPatternMatchesAfterExcludingSameCase(t *testing.T) {
	req := domain.ContextBuildRequest{
		Metadata: map[string]any{
			"benchmark_case_id":  "httpx-experience-review",
			"benchmark_league":   "pattern_transfer",
			"repository_url":     "https://github.com/encode/httpx",
			"repo_profile":       "python_http_client_library",
			"framework":          "library",
			"problem_domain":     "http_client",
			"error_class":        "repository_structure_validation",
			"fix_pattern":        "deterministic_marker_before_review",
			"validation_pattern": "containerized_manifest_smoke",
		},
	}
	scoreSelfHigh := 0.84
	scoreSelfLow := 0.79
	scoreAxios := 0.78
	items := []domain.SemanticChunkResponse{
		{ID: "self-1", SourceType: "task", SourceID: "self-1", ChunkIndex: 0, ContentText: "self 1", Metadata: map[string]any{"benchmark_case_id": "httpx-experience-review", "problem_domain": "http_client", "fix_pattern": "deterministic_marker_before_review"}, Score: &scoreSelfHigh},
		{ID: "self-2", SourceType: "task", SourceID: "self-2", ChunkIndex: 0, ContentText: "self 2", Metadata: map[string]any{"benchmark_case_id": "httpx-experience-review", "problem_domain": "http_client", "fix_pattern": "deterministic_marker_before_review"}, Score: &scoreSelfHigh},
		{ID: "self-3", SourceType: "task", SourceID: "self-3", ChunkIndex: 0, ContentText: "self 3", Metadata: map[string]any{"benchmark_case_id": "httpx-experience-review", "problem_domain": "http_client", "fix_pattern": "deterministic_marker_before_review"}, Score: &scoreSelfLow},
		{ID: "axios-1", SourceType: "task", SourceID: "axios-1", ChunkIndex: 0, ContentText: "axios 1", Metadata: map[string]any{"benchmark_case_id": "axios-experience-review", "problem_domain": "http_client", "fix_pattern": "deterministic_marker_before_review"}, Score: &scoreAxios},
		{ID: "axios-2", SourceType: "task", SourceID: "axios-2", ChunkIndex: 0, ContentText: "axios 2", Metadata: map[string]any{"benchmark_case_id": "axios-experience-review", "problem_domain": "http_client", "fix_pattern": "deterministic_marker_before_review"}, Score: &scoreAxios},
	}
	chunks := rankAndTrim(items, req, 8, 2000)
	if len(chunks) == 0 {
		t.Fatal("expected cross-repo chunks to survive exclusion of same-case memory")
	}
	for _, chunk := range chunks {
		if got := chunk.Metadata["memory_match_type"]; got != "pattern_similar" {
			t.Fatalf("expected pattern_similar match type, got %#v", got)
		}
		if chunk.Metadata["benchmark_case_id"] == "httpx-experience-review" {
			t.Fatalf("expected self-case chunks to be excluded, got %#v", chunk.Metadata)
		}
	}
}

func TestDiversifyTransferCandidatesCapsSameCaseDominance(t *testing.T) {
	req := domain.ContextBuildRequest{Metadata: map[string]any{"benchmark_league": "pattern_transfer"}}
	score := 0.8
	items := []domain.SemanticChunkResponse{
		{ID: "self-1", SourceType: "task", SourceID: "self-1", Metadata: map[string]any{"benchmark_case_id": "httpx"}, Score: &score},
		{ID: "self-2", SourceType: "task", SourceID: "self-2", Metadata: map[string]any{"benchmark_case_id": "httpx"}, Score: &score},
		{ID: "self-3", SourceType: "task", SourceID: "self-3", Metadata: map[string]any{"benchmark_case_id": "httpx"}, Score: &score},
		{ID: "cross-1", SourceType: "task", SourceID: "cross-1", Metadata: map[string]any{"benchmark_case_id": "axios"}, Score: &score},
		{ID: "cross-2", SourceType: "task", SourceID: "cross-2", Metadata: map[string]any{"benchmark_case_id": "axios"}, Score: &score},
	}
	got := diversifyTransferCandidates(items, req)
	if len(got) != 4 {
		t.Fatalf("expected transfer diversification to trim same-case dominance to 4 items, got %d", len(got))
	}
	selfCount := 0
	for _, item := range got {
		if metadataString(item.Metadata, "benchmark_case_id") == "httpx" {
			selfCount++
		}
	}
	if selfCount != 2 {
		t.Fatalf("expected at most 2 items from same benchmark case, got %d", selfCount)
	}
}

func TestInitialSearchSettingsOverfetchTransferLeagues(t *testing.T) {
	if got := initialSearchLimit(2, "pattern_transfer"); got < 24 {
		t.Fatalf("expected pattern transfer limit overfetch, got %d", got)
	}
	if got := initialSearchMaxChars(400, "pattern_transfer"); got != 3200 {
		t.Fatalf("expected expanded search max chars for transfer, got %d", got)
	}
	if got := initialSearchLimit(2, "repo_recall"); got != 8 {
		t.Fatalf("expected default repo recall overfetch of 8, got %d", got)
	}
}

func TestBuildQueryOmitsRepoSpecificMarkersForPatternTransfer(t *testing.T) {
	req := domain.ContextBuildRequest{
		TaskDescription: "Run a deterministic experience benchmark workflow for httpx and validate the repository smoke review command.",
		Metadata: map[string]any{
			"benchmark_case_id":   "httpx-experience-review",
			"benchmark_case_type": "review_only",
			"benchmark_league":    "pattern_transfer",
			"repository_url":      "https://github.com/encode/httpx",
			"repo_profile":        "python_http_client_library",
			"runtime_or_stack":    "python",
			"language":            "python",
			"framework":           "library",
			"problem_domain":      "http_client",
			"error_class":         "repository_structure_validation",
			"fix_pattern":         "deterministic_marker_before_review",
			"validation_pattern":  "containerized_manifest_smoke",
			"initiative_goal":     "Run a deterministic experience benchmark workflow for httpx and validate the repository smoke review command.",
		},
	}
	query := buildQuery(req)
	if !strings.Contains(query, "Cross-repo pattern transfer benchmark.") {
		t.Fatalf("expected generic transfer preamble, got %q", query)
	}
	for _, forbidden := range []string{
		"https://github.com/encode/httpx",
		"python_http_client_library",
		"httpx-experience-review",
		"Runtime: python",
		"Language: python",
		"Framework: library",
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("expected transfer query to omit repo-specific token %q, got %q", forbidden, query)
		}
	}
	if !strings.Contains(query, "Problem domain: http_client") {
		t.Fatalf("expected pattern metadata to remain in query, got %q", query)
	}
}

func TestMemoryMatchTypeTechnologyTransferRequiresCoherentSignal(t *testing.T) {
	req := domain.ContextBuildRequest{
		Metadata: map[string]any{
			"benchmark_league":    "technology_transfer",
			"runtime_or_stack":    "php",
			"language":            "php",
			"framework":           "library",
			"problem_domain":      "logging",
			"error_class":         "repository_structure_validation",
			"fix_pattern":         "deterministic_marker_before_review",
			"validation_pattern":  "containerized_manifest_smoke",
			"benchmark_case_type": "review_only",
		},
	}

	weak := domain.SemanticChunkResponse{
		Metadata: map[string]any{
			"language": "php",
		},
	}
	if got := memoryMatchType(weak, req); got != "semantic_related" {
		t.Fatalf("expected weak single-dimension hit to stay semantic_related, got %q", got)
	}

	strong := domain.SemanticChunkResponse{
		Metadata: map[string]any{
			"runtime_or_stack":   "php",
			"language":           "php",
			"problem_domain":     "logging",
			"validation_pattern": "containerized_manifest_smoke",
		},
	}
	if got := memoryMatchType(strong, req); got != "technology_similar" {
		t.Fatalf("expected coherent technology hit to be technology_similar, got %q", got)
	}
}

func TestBuildForTaskUsesWorkspaceRootMetadataWhenWorkspacePathIsNil(t *testing.T) {
	workspaceRoot := "/tmp/objective-workspace"
	task := &domain.TaskResponse{
		ID:          "task-1",
		Description: "Review the objective execution results",
		Metadata: map[string]any{
			"workspace_root":  workspaceRoot,
			"initiative_goal": "Fix the repository and validate the changes.",
		},
	}
	svc := &Service{}
	pkg, err := svc.BuildForTask(nil, task)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.WorkspaceRoot == nil || *pkg.WorkspaceRoot != workspaceRoot {
		t.Fatalf("expected workspace_root metadata to be preserved, got %#v", pkg.WorkspaceRoot)
	}
}
