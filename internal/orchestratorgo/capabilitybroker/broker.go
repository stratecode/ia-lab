package capabilitybroker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type RegistryStore interface {
	EnsureDefaultCapabilityDefinitions(ctx context.Context, definitions []domain.CapabilityDefinition) error
	ListCapabilityDefinitions(ctx context.Context) ([]domain.CapabilityDefinition, error)
	GetProjectCapabilityPolicy(ctx context.Context, repositoryURL string) (*domain.ProjectCapabilityPolicy, error)
}

type Backend func(ctx context.Context, req ExecuteRequest) (*capabilities.Result, error)

type ExecuteRequest struct {
	TaskID        *string
	AgentType     string
	RepositoryURL string
	Intent        string
	Capability    string
	Input         map[string]any
}

type ExecutionResult struct {
	Candidate domain.CapabilityCandidate
	Result    *capabilities.Result
}

type Options struct {
	Store    RegistryStore
	Backends map[string]Backend
	Now      func() time.Time
}

type Broker struct {
	store    RegistryStore
	backends map[string]Backend
	now      func() time.Time
}

var ErrCapabilityNotAllowed = errors.New("capability not allowed")

func New(options Options) *Broker {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	backends := map[string]Backend{}
	for key, value := range options.Backends {
		backends[strings.TrimSpace(key)] = value
	}
	return &Broker{
		store:    options.Store,
		backends: backends,
		now:      now,
	}
}

func (b *Broker) EnsureDefaults(ctx context.Context) error {
	if b == nil || b.store == nil {
		return nil
	}
	return b.store.EnsureDefaultCapabilityDefinitions(ctx, DefaultDefinitions())
}

func (b *Broker) RegisterBackend(name string, backend Backend) {
	if b == nil || backend == nil {
		return
	}
	if b.backends == nil {
		b.backends = map[string]Backend{}
	}
	b.backends[strings.TrimSpace(name)] = backend
}

func (b *Broker) ListDefinitions(ctx context.Context) ([]domain.CapabilityDefinition, error) {
	if b == nil || b.store == nil {
		return DefaultDefinitions(), nil
	}
	items, err := b.store.ListCapabilityDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return DefaultDefinitions(), nil
	}
	return items, nil
}

func (b *Broker) ListAvailable(ctx context.Context, projectURL, agentType, intent string, preferredTags []string) ([]domain.CapabilityCandidate, *domain.ProjectCapabilityPolicy, error) {
	definitions, err := b.ListDefinitions(ctx)
	if err != nil {
		return nil, nil, err
	}
	policy, err := b.projectPolicy(ctx, projectURL)
	if err != nil {
		return nil, nil, err
	}
	items := make([]domain.CapabilityCandidate, 0)
	for _, def := range definitions {
		candidate, ok := b.evaluateCandidate(def, policy, agentType, intent, preferredTags)
		if !ok {
			continue
		}
		items = append(items, candidate)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Name < items[j].Name
		}
		return items[i].Score > items[j].Score
	})
	return items, policy, nil
}

func (b *Broker) Resolve(ctx context.Context, projectURL, agentType, intent string, preferredTags []string, explicitCapability string) (*domain.CapabilityCandidate, *domain.ProjectCapabilityPolicy, error) {
	items, policy, err := b.ListAvailable(ctx, projectURL, agentType, intent, preferredTags)
	if err != nil {
		return nil, nil, err
	}
	explicitCapability = normalizeCapabilityAlias(explicitCapability)
	if explicitCapability != "" {
		for _, item := range items {
			if item.Name == explicitCapability {
				return &item, policy, nil
			}
		}
		return nil, policy, fmt.Errorf("%w: %s", ErrCapabilityNotAllowed, explicitCapability)
	}
	if len(items) == 0 {
		return nil, policy, fmt.Errorf("%w: no capability matched", ErrCapabilityNotAllowed)
	}
	return &items[0], policy, nil
}

func (b *Broker) Execute(ctx context.Context, req ExecuteRequest, preferredTags []string) (*ExecutionResult, *domain.ProjectCapabilityPolicy, error) {
	candidate, policy, err := b.Resolve(ctx, req.RepositoryURL, req.AgentType, req.Intent, preferredTags, req.Capability)
	if err != nil {
		return nil, policy, err
	}
	backend := b.backends[candidate.Name]
	if backend == nil {
		backend = b.backends[candidate.Definition.BackendRef]
	}
	if backend == nil {
		return nil, policy, fmt.Errorf("no backend registered for capability %s", candidate.Name)
	}
	result, err := backend(ctx, ExecuteRequest{
		TaskID:        req.TaskID,
		AgentType:     req.AgentType,
		RepositoryURL: req.RepositoryURL,
		Intent:        req.Intent,
		Capability:    candidate.Name,
		Input:         req.Input,
	})
	if err != nil {
		return nil, policy, err
	}
	return &ExecutionResult{Candidate: *candidate, Result: result}, policy, nil
}

func (b *Broker) projectPolicy(ctx context.Context, projectURL string) (*domain.ProjectCapabilityPolicy, error) {
	projectURL = strings.TrimSpace(projectURL)
	if projectURL == "" || b == nil || b.store == nil {
		return nil, nil
	}
	return b.store.GetProjectCapabilityPolicy(ctx, projectURL)
}

func (b *Broker) evaluateCandidate(def domain.CapabilityDefinition, policy *domain.ProjectCapabilityPolicy, agentType, intent string, preferredTags []string) (domain.CapabilityCandidate, bool) {
	def.Name = normalizeCapabilityAlias(def.Name)
	if !def.Enabled {
		return domain.CapabilityCandidate{}, false
	}
	if strings.TrimSpace(agentType) != "" && !containsString(def.AuthorizedAgents, agentType) {
		return domain.CapabilityCandidate{}, false
	}
	if strings.TrimSpace(agentType) != "" && !defaultPolicyAllows(def, agentType) {
		return domain.CapabilityCandidate{}, false
	}
	override := policyOverride(policy, def.Name)
	if isDeniedByProjectPolicy(policy, def.Name, agentType, override) {
		return domain.CapabilityCandidate{}, false
	}
	score := scoreCandidate(def, agentType, intent, preferredTags, override)
	return domain.CapabilityCandidate{
		Name:           def.Name,
		Kind:           def.Kind,
		BackendRef:     def.BackendRef,
		CapabilityTags: append([]string{}, def.CapabilityTags...),
		RiskClass:      def.RiskClass,
		CostClass:      def.CostClass,
		LatencyClass:   def.LatencyClass,
		Score:          score,
		Definition:     def,
		Override:       override,
	}, true
}

func DefaultDefinitions() []domain.CapabilityDefinition {
	return []domain.CapabilityDefinition{
		{
			Name:             "web.search",
			Kind:             "internal",
			BackendRef:       "web.search",
			AuthorizedAgents: []string{"planner", "researcher"},
			CapabilityTags:   []string{"evidence", "search", "docs"},
			RiskClass:        "read_only",
			CostClass:        "low",
			LatencyClass:     "medium",
			DefaultPolicy:    map[string]any{"auto_use": false},
			Enabled:          true,
			Version:          1,
		},
		{
			Name:             "web.fetch",
			Kind:             "internal",
			BackendRef:       "web.fetch",
			AuthorizedAgents: []string{"planner", "researcher", "reviewer"},
			CapabilityTags:   []string{"evidence", "fetch", "docs"},
			RiskClass:        "read_only",
			CostClass:        "low",
			LatencyClass:     "medium",
			DefaultPolicy:    map[string]any{"auto_use": false},
			Enabled:          true,
			Version:          1,
		},
		{
			Name:             "document.read",
			Kind:             "sidecar",
			BackendRef:       "document.read",
			AuthorizedAgents: []string{"researcher", "reviewer"},
			CapabilityTags:   []string{"evidence", "docs", "documents"},
			RiskClass:        "read_only",
			CostClass:        "medium",
			LatencyClass:     "medium",
			Enabled:          true,
			Version:          1,
		},
		{
			Name:             "image.analyze",
			Kind:             "sidecar",
			BackendRef:       "image.analyze",
			AuthorizedAgents: []string{"researcher", "reviewer"},
			CapabilityTags:   []string{"evidence", "images"},
			RiskClass:        "read_only",
			CostClass:        "medium",
			LatencyClass:     "medium",
			Enabled:          true,
			Version:          1,
		},
		{
			Name:             "code.analysis",
			Kind:             "internal",
			BackendRef:       "code.analysis",
			AuthorizedAgents: []string{"reviewer"},
			CapabilityTags:   []string{"review", "static-analysis", "code-quality", "validation"},
			RiskClass:        "read_only",
			CostClass:        "medium",
			LatencyClass:     "medium",
			Enabled:          true,
			Version:          1,
		},
		{
			Name:                   "filesystem.read",
			Kind:                   "internal",
			BackendRef:             "filesystem.read",
			AuthorizedAgents:       []string{"coder", "reviewer", "infra"},
			CapabilityTags:         []string{"filesystem", "read", "workspace"},
			RiskClass:              "read_only",
			CostClass:              "low",
			LatencyClass:           "low",
			WorkspaceScopeRequired: true,
			Enabled:                true,
			Version:                1,
		},
		{
			Name:                   "filesystem.list",
			Kind:                   "internal",
			BackendRef:             "filesystem.list",
			AuthorizedAgents:       []string{"coder", "reviewer", "infra"},
			CapabilityTags:         []string{"filesystem", "read", "workspace"},
			RiskClass:              "read_only",
			CostClass:              "low",
			LatencyClass:           "low",
			WorkspaceScopeRequired: true,
			Enabled:                true,
			Version:                1,
		},
		{
			Name:                   "filesystem.write",
			Kind:                   "internal",
			BackendRef:             "filesystem.write",
			AuthorizedAgents:       []string{"coder", "infra"},
			CapabilityTags:         []string{"filesystem", "write", "workspace"},
			RiskClass:              "workspace_write",
			CostClass:              "low",
			LatencyClass:           "low",
			WorkspaceScopeRequired: true,
			DefaultPolicy:          map[string]any{"approval_required": true},
			Enabled:                true,
			Version:                1,
		},
		{
			Name:             "research.query",
			Kind:             "internal",
			BackendRef:       "research.query",
			AuthorizedAgents: []string{"planner", "researcher"},
			CapabilityTags:   []string{"research", "evidence", "synthesis"},
			RiskClass:        "read_only",
			CostClass:        "medium",
			LatencyClass:     "high",
			Enabled:          true,
			Version:          1,
		},
	}
}

func normalizeCapabilityAlias(value string) string {
	switch strings.TrimSpace(value) {
	case "write_file":
		return "filesystem.write"
	case "read_file":
		return "filesystem.read"
	case "list_files":
		return "filesystem.list"
	default:
		return strings.TrimSpace(value)
	}
}

func defaultPolicyAllows(def domain.CapabilityDefinition, agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case "coder":
		return def.Name == "filesystem.write"
	case "researcher":
		return def.RiskClass == "read_only" && (containsAnyTag(def.CapabilityTags, []string{"evidence", "docs", "search", "research"}) || strings.HasPrefix(def.Name, "web."))
	case "reviewer":
		return def.RiskClass == "read_only" && containsAnyTag(def.CapabilityTags, []string{"review", "validation", "docs", "evidence", "static-analysis", "code-quality"})
	case "planner":
		return def.RiskClass == "read_only" && containsAnyTag(def.CapabilityTags, []string{"evidence", "docs", "research", "search"})
	default:
		return true
	}
}

func policyOverride(policy *domain.ProjectCapabilityPolicy, name string) *domain.CapabilityPolicyOverride {
	if policy == nil || policy.PolicyOverrides == nil {
		return nil
	}
	override, ok := policy.PolicyOverrides[name]
	if !ok {
		return nil
	}
	return &override
}

func isDeniedByProjectPolicy(policy *domain.ProjectCapabilityPolicy, name, agentType string, override *domain.CapabilityPolicyOverride) bool {
	if override != nil {
		if override.Enabled != nil && !*override.Enabled {
			return true
		}
		if containsString(override.DeniedAgents, agentType) {
			return true
		}
		if len(override.PreferredAgents) > 0 && !containsString(override.PreferredAgents, agentType) {
			return true
		}
	}
	if policy == nil {
		return false
	}
	if containsString(policy.DisabledCapabilities, name) {
		return true
	}
	if len(policy.EnabledCapabilities) > 0 && !containsString(policy.EnabledCapabilities, name) {
		return true
	}
	return false
}

func scoreCandidate(def domain.CapabilityDefinition, agentType, intent string, preferredTags []string, override *domain.CapabilityPolicyOverride) float64 {
	score := 10.0
	if containsAnyTag(def.CapabilityTags, preferredTags) {
		score += 25
	}
	switch strings.TrimSpace(intent) {
	case "needs_external_evidence":
		if containsAnyTag(def.CapabilityTags, []string{"evidence", "docs", "search", "research"}) {
			score += 20
		}
	case "needs_repo_static_analysis":
		if containsAnyTag(def.CapabilityTags, []string{"review", "static-analysis", "code-quality", "validation"}) {
			score += 20
		}
	case "needs_workspace_write":
		if containsAnyTag(def.CapabilityTags, []string{"filesystem", "write"}) {
			score += 20
		}
	case "needs_docs_lookup":
		if containsAnyTag(def.CapabilityTags, []string{"docs", "fetch", "search"}) {
			score += 20
		}
	case "needs_ci_signal", "needs_issue_tracker_context":
		if containsAnyTag(def.CapabilityTags, []string{"evidence", "review"}) {
			score += 10
		}
	}
	if def.RiskClass == "read_only" {
		score += 5
	}
	score -= classPenalty(def.CostClass)
	score -= classPenalty(def.LatencyClass)
	if override != nil && override.AutoUse != nil && *override.AutoUse {
		score += 5
	}
	if agentType == "reviewer" && def.Name == "code.analysis" {
		score += 15
	}
	if agentType == "coder" && def.Name == "filesystem.write" {
		score += 15
	}
	return score
}

func classPenalty(value string) float64 {
	switch strings.TrimSpace(value) {
	case "high":
		return 4
	case "medium":
		return 2
	default:
		return 0
	}
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func containsAnyTag(items, targets []string) bool {
	for _, target := range targets {
		if containsString(items, target) {
			return true
		}
	}
	return false
}
