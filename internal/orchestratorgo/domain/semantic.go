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
	AllowedSources  []string       `json:"allowed_source_types,omitempty"`
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
	AgentType     string         `json:"agent_type"`
	TaskID        *string        `json:"task_id,omitempty"`
	InitiativeID  *string        `json:"initiative_id,omitempty"`
	WorkspaceRoot *string        `json:"workspace_root,omitempty"`
	Query         string         `json:"query"`
	Chunks        []ContextChunk `json:"chunks"`
	SourceRefs    []string       `json:"source_refs"`
	Constraints   []string       `json:"constraints"`
	Policies      []string       `json:"policies"`
	PromptSection string         `json:"prompt_section"`
	TotalChars    int            `json:"total_chars"`
	GeneratedAt   time.Time      `json:"generated_at"`
}
