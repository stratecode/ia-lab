package contextbuilder

import (
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
