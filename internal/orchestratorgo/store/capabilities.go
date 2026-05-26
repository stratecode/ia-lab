package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type UpsertCapabilityDefinitionParams struct {
	Name                   string
	Kind                   string
	BackendRef             string
	InputSchema            map[string]any
	OutputSchema           map[string]any
	AuthorizedAgents       []string
	CapabilityTags         []string
	RiskClass              string
	CostClass              string
	LatencyClass           string
	WorkspaceScopeRequired bool
	DefaultPolicy          map[string]any
	Enabled                bool
	Version                int
}

type UpsertProjectCapabilityPolicyParams struct {
	RepositoryURL        string
	Mode                 string
	EnabledCapabilities  []string
	DisabledCapabilities []string
	PolicyOverrides      map[string]domain.CapabilityPolicyOverride
	UpdatedBy            *string
}

func (s *PostgresStore) EnsureDefaultCapabilityDefinitions(ctx context.Context, definitions []domain.CapabilityDefinition) error {
	for _, item := range definitions {
		if _, err := s.UpsertCapabilityDefinition(ctx, UpsertCapabilityDefinitionParams{
			Name:                   item.Name,
			Kind:                   item.Kind,
			BackendRef:             item.BackendRef,
			InputSchema:            item.InputSchema,
			OutputSchema:           item.OutputSchema,
			AuthorizedAgents:       item.AuthorizedAgents,
			CapabilityTags:         item.CapabilityTags,
			RiskClass:              item.RiskClass,
			CostClass:              item.CostClass,
			LatencyClass:           item.LatencyClass,
			WorkspaceScopeRequired: item.WorkspaceScopeRequired,
			DefaultPolicy:          item.DefaultPolicy,
			Enabled:                item.Enabled,
			Version:                item.Version,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) UpsertCapabilityDefinition(ctx context.Context, params UpsertCapabilityDefinitionParams) (*domain.CapabilityDefinition, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" {
		return nil, fmt.Errorf("capability name is required")
	}
	if params.Version <= 0 {
		params.Version = 1
	}
	inputRaw, err := json.Marshal(nonNilMap(params.InputSchema))
	if err != nil {
		return nil, err
	}
	outputRaw, err := json.Marshal(nonNilMap(params.OutputSchema))
	if err != nil {
		return nil, err
	}
	defaultPolicyRaw, err := json.Marshal(nonNilMap(params.DefaultPolicy))
	if err != nil {
		return nil, err
	}
	authRaw, err := json.Marshal(params.AuthorizedAgents)
	if err != nil {
		return nil, err
	}
	tagsRaw, err := json.Marshal(params.CapabilityTags)
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO capability_definitions (
			name, kind, backend_ref, input_schema, output_schema, authorized_agents,
			capability_tags, risk_class, cost_class, latency_class, workspace_scope_required,
			default_policy, enabled, version
		) VALUES (
			$1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9, $10, $11, $12::jsonb, $13, $14
		)
		ON CONFLICT (name) DO UPDATE SET
			kind = EXCLUDED.kind,
			backend_ref = EXCLUDED.backend_ref,
			input_schema = EXCLUDED.input_schema,
			output_schema = EXCLUDED.output_schema,
			authorized_agents = EXCLUDED.authorized_agents,
			capability_tags = EXCLUDED.capability_tags,
			risk_class = EXCLUDED.risk_class,
			cost_class = EXCLUDED.cost_class,
			latency_class = EXCLUDED.latency_class,
			workspace_scope_required = EXCLUDED.workspace_scope_required,
			default_policy = EXCLUDED.default_policy,
			enabled = EXCLUDED.enabled,
			version = EXCLUDED.version,
			updated_at = NOW()
	`, name, strings.TrimSpace(params.Kind), strings.TrimSpace(params.BackendRef), string(inputRaw), string(outputRaw), string(authRaw), string(tagsRaw), strings.TrimSpace(params.RiskClass), strings.TrimSpace(params.CostClass), strings.TrimSpace(params.LatencyClass), params.WorkspaceScopeRequired, string(defaultPolicyRaw), params.Enabled, params.Version); err != nil {
		return nil, err
	}
	return s.GetCapabilityDefinition(ctx, name)
}

func (s *PostgresStore) GetCapabilityDefinition(ctx context.Context, name string) (*domain.CapabilityDefinition, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT name, kind, backend_ref, input_schema, output_schema, authorized_agents, capability_tags,
		       risk_class, cost_class, latency_class, workspace_scope_required, default_policy,
		       enabled, version, created_at, updated_at
		FROM capability_definitions
		WHERE name = $1
	`, strings.TrimSpace(name))
	item, err := scanCapabilityDefinition(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func (s *PostgresStore) ListCapabilityDefinitions(ctx context.Context) ([]domain.CapabilityDefinition, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT name, kind, backend_ref, input_schema, output_schema, authorized_agents, capability_tags,
		       risk_class, cost_class, latency_class, workspace_scope_required, default_policy,
		       enabled, version, created_at, updated_at
		FROM capability_definitions
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.CapabilityDefinition, 0)
	for rows.Next() {
		item, err := scanCapabilityDefinition(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpsertProjectCapabilityPolicy(ctx context.Context, params UpsertProjectCapabilityPolicyParams) (*domain.ProjectCapabilityPolicy, error) {
	repositoryURL := strings.TrimSpace(params.RepositoryURL)
	if repositoryURL == "" {
		return nil, fmt.Errorf("repository_url is required")
	}
	mode := strings.TrimSpace(params.Mode)
	if mode == "" {
		mode = "discover_filter"
	}
	enabledRaw, err := json.Marshal(params.EnabledCapabilities)
	if err != nil {
		return nil, err
	}
	disabledRaw, err := json.Marshal(params.DisabledCapabilities)
	if err != nil {
		return nil, err
	}
	overrideRaw, err := json.Marshal(nonNilPolicyOverrides(params.PolicyOverrides))
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO project_capability_policies (
			repository_url, mode, enabled_capabilities, disabled_capabilities, policy_overrides, updated_by, updated_at
		) VALUES (
			$1, $2, $3::jsonb, $4::jsonb, $5::jsonb, $6, NOW()
		)
		ON CONFLICT (repository_url) DO UPDATE SET
			mode = EXCLUDED.mode,
			enabled_capabilities = EXCLUDED.enabled_capabilities,
			disabled_capabilities = EXCLUDED.disabled_capabilities,
			policy_overrides = EXCLUDED.policy_overrides,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, repositoryURL, mode, string(enabledRaw), string(disabledRaw), string(overrideRaw), params.UpdatedBy); err != nil {
		return nil, err
	}
	return s.GetProjectCapabilityPolicy(ctx, repositoryURL)
}

func (s *PostgresStore) GetProjectCapabilityPolicy(ctx context.Context, repositoryURL string) (*domain.ProjectCapabilityPolicy, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT repository_url, mode, enabled_capabilities, disabled_capabilities, policy_overrides, updated_by, updated_at
		FROM project_capability_policies
		WHERE repository_url = $1
	`, strings.TrimSpace(repositoryURL))
	item, err := scanProjectCapabilityPolicy(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return item, nil
}

func scanCapabilityDefinition(row pgxScanner) (*domain.CapabilityDefinition, error) {
	var (
		item             domain.CapabilityDefinition
		inputRaw         []byte
		outputRaw        []byte
		authRaw          []byte
		tagsRaw          []byte
		defaultPolicyRaw []byte
	)
	if err := row.Scan(
		&item.Name,
		&item.Kind,
		&item.BackendRef,
		&inputRaw,
		&outputRaw,
		&authRaw,
		&tagsRaw,
		&item.RiskClass,
		&item.CostClass,
		&item.LatencyClass,
		&item.WorkspaceScopeRequired,
		&defaultPolicyRaw,
		&item.Enabled,
		&item.Version,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(inputRaw, &item.InputSchema)
	_ = json.Unmarshal(outputRaw, &item.OutputSchema)
	_ = json.Unmarshal(authRaw, &item.AuthorizedAgents)
	_ = json.Unmarshal(tagsRaw, &item.CapabilityTags)
	_ = json.Unmarshal(defaultPolicyRaw, &item.DefaultPolicy)
	if item.InputSchema == nil {
		item.InputSchema = map[string]any{}
	}
	if item.OutputSchema == nil {
		item.OutputSchema = map[string]any{}
	}
	if item.DefaultPolicy == nil {
		item.DefaultPolicy = map[string]any{}
	}
	return &item, nil
}

func scanProjectCapabilityPolicy(row pgxScanner) (*domain.ProjectCapabilityPolicy, error) {
	var (
		item        domain.ProjectCapabilityPolicy
		enabledRaw  []byte
		disabledRaw []byte
		overrideRaw []byte
	)
	if err := row.Scan(
		&item.RepositoryURL,
		&item.Mode,
		&enabledRaw,
		&disabledRaw,
		&overrideRaw,
		&item.UpdatedBy,
		&item.UpdatedAt,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(enabledRaw, &item.EnabledCapabilities)
	_ = json.Unmarshal(disabledRaw, &item.DisabledCapabilities)
	_ = json.Unmarshal(overrideRaw, &item.PolicyOverrides)
	if item.PolicyOverrides == nil {
		item.PolicyOverrides = map[string]domain.CapabilityPolicyOverride{}
	}
	return &item, nil
}

func nonNilPolicyOverrides(input map[string]domain.CapabilityPolicyOverride) map[string]domain.CapabilityPolicyOverride {
	if input == nil {
		return map[string]domain.CapabilityPolicyOverride{}
	}
	return input
}

type pgxScanner interface {
	Scan(dest ...any) error
}
