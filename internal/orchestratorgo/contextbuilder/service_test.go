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

func TestHasRepoMemoryScope(t *testing.T) {
	if hasRepoMemoryScope(nil) {
		t.Fatal("expected nil metadata to have no repo memory scope")
	}
	if !hasRepoMemoryScope(map[string]any{"project_request": map[string]any{"repo_profile": "python_slugify_v1"}}) {
		t.Fatal("expected repo_profile in project_request to enable repo memory scope")
	}
}
