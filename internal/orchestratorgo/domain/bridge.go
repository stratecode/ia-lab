package domain

import "time"

type LocalBridgeRegisterRequest struct {
	BridgeID      string         `json:"bridge_id"`
	Name          string         `json:"name"`
	Hostname      string         `json:"hostname"`
	WorkspaceRoot string         `json:"workspace_root"`
	Capabilities  map[string]any `json:"capabilities"`
	APIKeyName    *string        `json:"api_key_name"`
}

type LocalBridgeHeartbeatRequest struct {
	Status          string  `json:"status"`
	CurrentTaskID   *string `json:"current_task_id,omitempty"`
	LeaseTTLSeconds *int    `json:"lease_ttl_seconds,omitempty"`
}

type LocalBridgeResponse struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Hostname      string         `json:"hostname"`
	WorkspaceRoot string         `json:"workspace_root"`
	Status        string         `json:"status"`
	Capabilities  map[string]any `json:"capabilities"`
	APIKeyName    *string        `json:"api_key_name"`
	LastHeartbeat *time.Time     `json:"last_heartbeat"`
	CreatedAt     *time.Time     `json:"created_at"`
	UpdatedAt     *time.Time     `json:"updated_at"`
}

type LocalBridgeListResponse struct {
	Items []LocalBridgeResponse `json:"items"`
	Total int                   `json:"total"`
}

type LocalBridgeTaskClaimResponse struct {
	TaskID          string         `json:"task_id"`
	RootTaskID      *string        `json:"root_task_id"`
	ParentTaskID    *string        `json:"parent_task_id"`
	Description     string         `json:"description"`
	AssignedAgent   string         `json:"assigned_agent"`
	ExecutionTarget string         `json:"execution_target"`
	WorkspacePath   string         `json:"workspace_path"`
	WorkspaceRoot   string         `json:"workspace_root"`
	Metadata        map[string]any `json:"metadata"`
	RepoPath        string         `json:"repo_path"`
	Branch          string         `json:"branch"`
}

type LocalBridgeResultRequest struct {
	Status                    string           `json:"status"`
	Summary                   *string          `json:"summary"`
	Stdout                    *string          `json:"stdout"`
	Stderr                    *string          `json:"stderr"`
	ExitCode                  *int             `json:"exit_code"`
	Diff                      *string          `json:"diff"`
	ChangedFiles              []string         `json:"changed_files"`
	TestResults               map[string]any   `json:"test_results"`
	Artifacts                 []map[string]any `json:"artifacts"`
	ErrorMessage              *string          `json:"error_message"`
	ActionType                *string          `json:"action_type"`
	TargetResource            *string          `json:"target_resource"`
	TimeoutSeconds            *int             `json:"timeout_seconds"`
	SemanticContextSources    []string         `json:"semantic_context_sources"`
	SemanticContextChunkCount int              `json:"semantic_context_chunk_count"`
	SemanticContextHits       []map[string]any `json:"semantic_context_hits"`
}
