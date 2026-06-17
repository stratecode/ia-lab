package domain

import "time"

const (
	MemoryClassRepoSpecific      = "repo_specific"
	MemoryClassTechnologySimilar = "technology_similar"
	MemoryClassPatternSimilar    = "pattern_similar"
	MemoryClassNegativeGuardrail = "negative_guardrail"
	MemoryClassRecentExecution   = "recent_execution"
	MemoryClassPlanningContext   = "planning_context"
	MemoryClassLegacy            = "legacy_unclassified"
)

type ContextChunk struct {
	ID           string         `json:"id"`
	SourceType   string         `json:"source_type"`
	SourceID     string         `json:"source_id"`
	ChunkIndex   int            `json:"chunk_index"`
	ContentText  string         `json:"content_text"`
	Metadata     map[string]any `json:"metadata"`
	Score        *float64       `json:"score,omitempty"`
	SourceRef    string         `json:"source_ref"`
	MemoryClass  string         `json:"memory_class,omitempty"`
	InitiativeID *string        `json:"initiative_id,omitempty"`
	TaskID       *string        `json:"task_id,omitempty"`
	ArtifactID   *string        `json:"artifact_id,omitempty"`
}

type ContextPackage struct {
	AgentType          string            `json:"agent_type"`
	TaskID             *string           `json:"task_id,omitempty"`
	InitiativeID       *string           `json:"initiative_id,omitempty"`
	WorkspaceRoot      *string           `json:"workspace_root,omitempty"`
	Query              string            `json:"query"`
	MemoryMode         string            `json:"memory_mode,omitempty"`
	MemoryStrategy     string            `json:"memory_strategy,omitempty"`
	Chunks             []ContextChunk    `json:"chunks"`
	Precedents         []RetrievalHit    `json:"precedents,omitempty"`
	SourceRefs         []string          `json:"source_refs"`
	Constraints        []string          `json:"constraints"`
	Policies           []string          `json:"policies"`
	PromptSection      string            `json:"prompt_section"`
	CompactSummaries   map[string]string `json:"compact_summaries,omitempty"`
	WhyThesePrecedents []string          `json:"why_these_precedents,omitempty"`
	OperationalIR      *OperationalIR    `json:"operational_ir,omitempty"`
	TotalChars         int               `json:"total_chars"`
	GeneratedAt        time.Time         `json:"generated_at"`
}

type RetrievalPacket struct {
	AgentType          string            `json:"agent_type"`
	TaskID             *string           `json:"task_id,omitempty"`
	InitiativeID       *string           `json:"initiative_id,omitempty"`
	WorkspaceRoot      *string           `json:"workspace_root,omitempty"`
	Query              string            `json:"query"`
	MemoryMode         string            `json:"memory_mode,omitempty"`
	MemoryStrategy     string            `json:"memory_strategy,omitempty"`
	ChunkCount         int               `json:"chunk_count"`
	SourceRefs         []string          `json:"source_refs"`
	Constraints        []string          `json:"constraints,omitempty"`
	Policies           []string          `json:"policies,omitempty"`
	CompactSummaries   map[string]string `json:"compact_summaries,omitempty"`
	WhyThesePrecedents []string          `json:"why_these_precedents,omitempty"`
	Precedents         []RetrievalHit    `json:"precedents"`
	OperationalIR      *OperationalIR    `json:"operational_ir,omitempty"`
	TotalChars         int               `json:"total_chars,omitempty"`
	GeneratedAt        time.Time         `json:"generated_at"`
}

type RetrievalHit struct {
	SourceRef       string   `json:"source_ref"`
	SourceType      string   `json:"source_type"`
	SourceID        string   `json:"source_id"`
	Score           *float64 `json:"score,omitempty"`
	MemoryClass     string   `json:"memory_class,omitempty"`
	InitiativeID    *string  `json:"initiative_id,omitempty"`
	TaskID          *string  `json:"task_id,omitempty"`
	ArtifactID      *string  `json:"artifact_id,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	SelectionReason string   `json:"selection_reason,omitempty"`
}

type OperationalIR struct {
	Version         string            `json:"version"`
	Mode            string            `json:"mode"`
	Confidence      string            `json:"confidence"`
	Trusted         []OperationalItem `json:"trusted"`
	Invalid         []OperationalItem `json:"invalid"`
	Constraints     []string          `json:"constraints"`
	Dependencies    []string          `json:"dependencies"`
	Policies        []string          `json:"policies"`
	Flows           []string          `json:"flows"`
	Approvals       []string          `json:"approvals"`
	Risks           []string          `json:"risks"`
	ValidationRules []string          `json:"validation_rules"`
	JSONata         JSONataIR         `json:"jsonata"`
	SourceRefs      []string          `json:"source_refs"`
}

type OperationalItem struct {
	SourceRef     string         `json:"source_ref"`
	SourceType    string         `json:"source_type"`
	SourceID      string         `json:"source_id"`
	Outcome       string         `json:"outcome"`
	Confidence    *float64       `json:"confidence,omitempty"`
	Summary       string         `json:"summary,omitempty"`
	AgentType     string         `json:"agent_type,omitempty"`
	TaskType      string         `json:"task_type,omitempty"`
	FailureReason string         `json:"failure_reason,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type JSONataIR struct {
	Selectors   map[string]string `json:"selectors"`
	Conditions  map[string]string `json:"conditions"`
	Projections map[string]string `json:"projections"`
}
