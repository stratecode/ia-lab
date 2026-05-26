package capabilitybroker

import (
	"context"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type memoryStore struct {
	definitions []domain.CapabilityDefinition
	policies    map[string]*domain.ProjectCapabilityPolicy
}

func (m *memoryStore) EnsureDefaultCapabilityDefinitions(_ context.Context, definitions []domain.CapabilityDefinition) error {
	m.definitions = append([]domain.CapabilityDefinition{}, definitions...)
	return nil
}

func (m *memoryStore) ListCapabilityDefinitions(_ context.Context) ([]domain.CapabilityDefinition, error) {
	return append([]domain.CapabilityDefinition{}, m.definitions...), nil
}

func (m *memoryStore) GetProjectCapabilityPolicy(_ context.Context, repositoryURL string) (*domain.ProjectCapabilityPolicy, error) {
	if m.policies == nil {
		return nil, nil
	}
	return m.policies[repositoryURL], nil
}

func TestResolveDefaultsCoderToFilesystemWrite(t *testing.T) {
	store := &memoryStore{definitions: DefaultDefinitions()}
	broker := New(Options{Store: store})
	candidate, _, err := broker.Resolve(context.Background(), "https://github.com/example/repo", "coder", "needs_workspace_write", []string{"filesystem", "write"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Name != "filesystem.write" {
		t.Fatalf("expected filesystem.write, got %s", candidate.Name)
	}
}

func TestResolveReviewerPrefersCodeAnalysis(t *testing.T) {
	store := &memoryStore{definitions: DefaultDefinitions()}
	broker := New(Options{Store: store})
	candidate, _, err := broker.Resolve(context.Background(), "https://github.com/example/repo", "reviewer", "needs_repo_static_analysis", []string{"review", "static-analysis"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Name != "code.analysis" {
		t.Fatalf("expected code.analysis, got %s", candidate.Name)
	}
}

func TestProjectPolicyDisablesCapability(t *testing.T) {
	store := &memoryStore{
		definitions: DefaultDefinitions(),
		policies: map[string]*domain.ProjectCapabilityPolicy{
			"https://github.com/example/repo": {
				RepositoryURL:        "https://github.com/example/repo",
				Mode:                 "discover_filter",
				DisabledCapabilities: []string{"code.analysis"},
			},
		},
	}
	broker := New(Options{Store: store})
	_, _, err := broker.Resolve(context.Background(), "https://github.com/example/repo", "reviewer", "needs_repo_static_analysis", []string{"review"}, "code.analysis")
	if err == nil {
		t.Fatal("expected code.analysis to be denied by project policy")
	}
}

func TestLegacyAliasResolvesToFilesystemWrite(t *testing.T) {
	store := &memoryStore{definitions: DefaultDefinitions()}
	broker := New(Options{Store: store})
	candidate, _, err := broker.Resolve(context.Background(), "https://github.com/example/repo", "coder", "needs_workspace_write", []string{"filesystem"}, "write_file")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Name != "filesystem.write" {
		t.Fatalf("expected filesystem.write, got %s", candidate.Name)
	}
}
