package domain

import "time"

type CapabilityDefinition struct {
	Name                   string         `json:"name"`
	Kind                   string         `json:"kind"`
	BackendRef             string         `json:"backend_ref"`
	InputSchema            map[string]any `json:"input_schema"`
	OutputSchema           map[string]any `json:"output_schema"`
	AuthorizedAgents       []string       `json:"authorized_agents"`
	CapabilityTags         []string       `json:"capability_tags"`
	RiskClass              string         `json:"risk_class"`
	CostClass              string         `json:"cost_class"`
	LatencyClass           string         `json:"latency_class"`
	WorkspaceScopeRequired bool           `json:"workspace_scope_required"`
	DefaultPolicy          map[string]any `json:"default_policy"`
	Enabled                bool           `json:"enabled"`
	Version                int            `json:"version"`
	CreatedAt              time.Time      `json:"created_at,omitempty"`
	UpdatedAt              time.Time      `json:"updated_at,omitempty"`
}

type CapabilityPolicyOverride struct {
	Enabled               *bool    `json:"enabled,omitempty"`
	AutoUse               *bool    `json:"auto_use,omitempty"`
	ApprovalRequired      *bool    `json:"approval_required,omitempty"`
	MaxInvocationsPerTask *int     `json:"max_invocations_per_task,omitempty"`
	TimeoutSeconds        *int     `json:"timeout_seconds,omitempty"`
	PreferredAgents       []string `json:"preferred_agents,omitempty"`
	DeniedAgents          []string `json:"denied_agents,omitempty"`
}

type ProjectCapabilityPolicy struct {
	RepositoryURL        string                              `json:"repository_url"`
	Mode                 string                              `json:"mode"`
	EnabledCapabilities  []string                            `json:"enabled_capabilities"`
	DisabledCapabilities []string                            `json:"disabled_capabilities"`
	PolicyOverrides      map[string]CapabilityPolicyOverride `json:"policy_overrides"`
	UpdatedBy            *string                             `json:"updated_by,omitempty"`
	UpdatedAt            time.Time                           `json:"updated_at"`
}

type CapabilityCandidate struct {
	Name           string                    `json:"name"`
	Kind           string                    `json:"kind"`
	BackendRef     string                    `json:"backend_ref"`
	CapabilityTags []string                  `json:"capability_tags"`
	RiskClass      string                    `json:"risk_class"`
	CostClass      string                    `json:"cost_class"`
	LatencyClass   string                    `json:"latency_class"`
	Score          float64                   `json:"score"`
	Definition     CapabilityDefinition      `json:"definition"`
	Override       *CapabilityPolicyOverride `json:"override,omitempty"`
}

type EffectiveProjectCapabilitiesResponse struct {
	RepositoryURL string                `json:"repository_url"`
	Mode          string                `json:"mode"`
	Version       string                `json:"version"`
	Capabilities  []CapabilityCandidate `json:"capabilities"`
}
