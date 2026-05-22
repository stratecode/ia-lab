package domain

import "time"

type SemanticSourceType string

const (
	SemanticSourceArtifact SemanticSourceType = "artifact"
	SemanticSourceTask     SemanticSourceType = "task"
	SemanticSourceReview   SemanticSourceType = "review"
	SemanticSourceRepoDoc  SemanticSourceType = "repo_doc"
)

type SemanticChunkResponse struct {
	ID            string         `json:"id"`
	SourceType    string         `json:"source_type"`
	SourceID      string         `json:"source_id"`
	InitiativeID  *string        `json:"initiative_id,omitempty"`
	TaskID        *string        `json:"task_id,omitempty"`
	ArtifactID    *string        `json:"artifact_id,omitempty"`
	WorkspaceRoot *string        `json:"workspace_root,omitempty"`
	ChunkIndex    int            `json:"chunk_index"`
	ContentText   string         `json:"content_text"`
	ContentHash   string         `json:"content_hash"`
	Metadata      map[string]any `json:"metadata"`
	Score         *float64       `json:"score,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type SemanticIndexRequest struct {
	SourceType string `json:"source_type"`
	SourceID   string `json:"source_id"`
}

type SemanticIndexResponse struct {
	SourceType  string `json:"source_type"`
	SourceID    string `json:"source_id"`
	ChunkCount  int    `json:"chunk_count"`
	ContentHash string `json:"content_hash"`
}

type SemanticSearchRequest struct {
	Query         string   `json:"query"`
	SourceTypes   []string `json:"source_types,omitempty"`
	InitiativeID  *string  `json:"initiative_id,omitempty"`
	TaskID        *string  `json:"task_id,omitempty"`
	ArtifactID    *string  `json:"artifact_id,omitempty"`
	WorkspaceRoot *string  `json:"workspace_root,omitempty"`
	RepositoryURL *string  `json:"repository_url,omitempty"`
	RepoProfile   *string  `json:"repo_profile,omitempty"`
	RuntimeOrStack *string `json:"runtime_or_stack,omitempty"`
	Language       *string `json:"language,omitempty"`
	Framework      *string `json:"framework,omitempty"`
	ProblemDomain  *string `json:"problem_domain,omitempty"`
	ErrorClass     *string `json:"error_class,omitempty"`
	FixPattern     *string `json:"fix_pattern,omitempty"`
	ValidationPattern *string `json:"validation_pattern,omitempty"`
	CaseType      *string  `json:"benchmark_case_type,omitempty"`
	Outcomes      []string `json:"outcomes,omitempty"`
	MinConfidence *float64 `json:"min_confidence,omitempty"`
	Limit         int      `json:"limit,omitempty"`
	MaxChars      int      `json:"max_chars,omitempty"`
}

type SemanticSearchResponse struct {
	Items []SemanticChunkResponse `json:"items"`
	Total int                     `json:"total"`
}

type ContextBuildRequest struct {
	AgentType       string         `json:"agent_type"`
	TaskID          *string        `json:"task_id,omitempty"`
	InitiativeID    *string        `json:"initiative_id,omitempty"`
	WorkspaceRoot   *string        `json:"workspace_root,omitempty"`
	TaskDescription string         `json:"task_description,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	MemoryMode      string         `json:"memory_mode,omitempty"`
	MemoryStrategy  string         `json:"memory_strategy,omitempty"`
	AllowedSources  []string       `json:"allowed_source_types,omitempty"`
	OutputFormat    string         `json:"output_format,omitempty"`
	Outcomes        []string       `json:"outcomes,omitempty"`
	MinConfidence   *float64       `json:"min_confidence,omitempty"`
	IncludeFailed   bool           `json:"include_failed,omitempty"`
	IncludeRejected bool           `json:"include_rejected,omitempty"`
	MaxChunks       int            `json:"max_chunks,omitempty"`
	MaxChars        int            `json:"max_chars,omitempty"`
}

type ContextChunk struct {
	ID           string         `json:"id"`
	SourceType   string         `json:"source_type"`
	SourceID     string         `json:"source_id"`
	ChunkIndex   int            `json:"chunk_index"`
	ContentText  string         `json:"content_text"`
	Metadata     map[string]any `json:"metadata"`
	Score        *float64       `json:"score,omitempty"`
	SourceRef    string         `json:"source_ref"`
	InitiativeID *string        `json:"initiative_id,omitempty"`
	TaskID       *string        `json:"task_id,omitempty"`
	ArtifactID   *string        `json:"artifact_id,omitempty"`
}

type ContextPackage struct {
	AgentType      string         `json:"agent_type"`
	TaskID         *string        `json:"task_id,omitempty"`
	InitiativeID   *string        `json:"initiative_id,omitempty"`
	WorkspaceRoot  *string        `json:"workspace_root,omitempty"`
	Query          string         `json:"query"`
	MemoryMode     string         `json:"memory_mode,omitempty"`
	MemoryStrategy string         `json:"memory_strategy,omitempty"`
	Chunks         []ContextChunk `json:"chunks"`
	SourceRefs     []string       `json:"source_refs"`
	Constraints    []string       `json:"constraints"`
	Policies       []string       `json:"policies"`
	PromptSection  string         `json:"prompt_section"`
	OperationalIR  *OperationalIR `json:"operational_ir,omitempty"`
	TotalChars     int            `json:"total_chars"`
	GeneratedAt    time.Time      `json:"generated_at"`
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
