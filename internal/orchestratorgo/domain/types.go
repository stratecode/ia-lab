package domain

import (
	"encoding/base64"
	"encoding/json"
	"time"
)

type TaskState string
type AgentType string
type Priority string
type TaskKind string
type ExecutionTarget string
type ApprovalStatus string
type ApiKeyScope string

const (
	TaskStateCreated         TaskState = "created"
	TaskStateQueued          TaskState = "queued"
	TaskStateAssigned        TaskState = "assigned"
	TaskStateInProgress      TaskState = "in_progress"
	TaskStateWaitingApproval TaskState = "waiting_approval"
	TaskStateReview          TaskState = "review"
	TaskStateRetrying        TaskState = "retrying"
	TaskStateCompleted       TaskState = "completed"
	TaskStateFailed          TaskState = "failed"
	TaskStateCancelled       TaskState = "cancelled"

	AgentTypePlanner    AgentType = "planner"
	AgentTypeCoder      AgentType = "coder"
	AgentTypeReviewer   AgentType = "reviewer"
	AgentTypeInfra      AgentType = "infra"
	AgentTypeResearcher AgentType = "researcher"

	PriorityCritical Priority = "critical"
	PriorityHigh     Priority = "high"
	PriorityNormal   Priority = "normal"
	PriorityLow      Priority = "low"

	TaskKindRoot     TaskKind = "root"
	TaskKindPlanStep TaskKind = "plan_step"

	ExecutionTargetRemote ExecutionTarget = "remote"
	ExecutionTargetLocal  ExecutionTarget = "local"

	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalTimeout  ApprovalStatus = "timeout"

	ScopeAdmin    ApiKeyScope = "admin"
	ScopeOperator ApiKeyScope = "operator"
	ScopeBot      ApiKeyScope = "bot"
	ScopeReadonly ApiKeyScope = "readonly"
)

type ComponentHealth struct {
	Status    string         `json:"status"`
	LatencyMS *float64       `json:"latency_ms"`
	Details   map[string]any `json:"details"`
}

type HealthResponse struct {
	Status     string                     `json:"status"`
	Timestamp  time.Time                  `json:"timestamp"`
	Version    string                     `json:"version"`
	SafeMode   bool                       `json:"safe_mode"`
	Components map[string]ComponentHealth `json:"components"`
}

type ReadyResponse struct {
	Ready     bool            `json:"ready"`
	Timestamp time.Time       `json:"timestamp"`
	Checks    map[string]bool `json:"checks"`
}

type TaskResponse struct {
	ID                  string          `json:"id"`
	State               TaskState       `json:"state"`
	Description         string          `json:"description"`
	Metadata            map[string]any  `json:"metadata"`
	AllowedCapabilities []string        `json:"allowed_capabilities"`
	ParentTaskID        *string         `json:"parent_task_id"`
	RootTaskID          *string         `json:"root_task_id"`
	TaskKind            TaskKind        `json:"task_kind"`
	AssignedAgent       *AgentType      `json:"assigned_agent"`
	Priority            Priority        `json:"priority"`
	ExecutionTarget     ExecutionTarget `json:"execution_target"`
	WorkspacePath       *string         `json:"workspace_path"`
	RetryCount          int             `json:"retry_count"`
	CorrelationID       string          `json:"correlation_id"`
	Results             any             `json:"results"`
	ErrorMessage        *string         `json:"error_message"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	StartedAt           *time.Time      `json:"started_at"`
	CompletedAt         *time.Time      `json:"completed_at"`
	ArchivedAt          *time.Time      `json:"archived_at"`
}

type TaskCreateRequest struct {
	Description         string          `json:"description"`
	Metadata            map[string]any  `json:"metadata"`
	AllowedCapabilities []string        `json:"allowed_capabilities"`
	Priority            Priority        `json:"priority"`
	AssignedAgent       *AgentType      `json:"assigned_agent"`
	ExecutionTarget     ExecutionTarget `json:"execution_target"`
}

type TaskUpdateRequest struct {
	State  TaskState `json:"state"`
	Reason *string   `json:"reason"`
}

type TaskListResponse struct {
	Items    []TaskResponse `json:"items"`
	Cursor   *string        `json:"cursor"`
	Total    int            `json:"total"`
	PageSize int            `json:"page_size"`
}

type TaskTreeResponse struct {
	TaskResponse
	Children []TaskTreeResponse `json:"children"`
}

type ApprovalResponse struct {
	ID              string         `json:"id"`
	TaskID          string         `json:"task_id"`
	ActionType      string         `json:"action_type"`
	TargetResource  string         `json:"target_resource"`
	Status          ApprovalStatus `json:"status"`
	Operator        *string        `json:"operator"`
	TimeoutSeconds  int            `json:"timeout_seconds"`
	EscalationLevel int            `json:"escalation_level"`
	RequestedAt     time.Time      `json:"requested_at"`
	ResolvedAt      *time.Time     `json:"resolved_at"`
	TimeoutAt       time.Time      `json:"timeout_at"`
}

type ApprovalResolveRequest struct {
	Operator string `json:"operator"`
}

type ApprovalListResponse struct {
	Items []ApprovalResponse `json:"items"`
	Total int                `json:"total"`
}

type ResearchRunResponse struct {
	ID                   string         `json:"id"`
	TaskID               *string        `json:"task_id"`
	Entrypoint           string         `json:"entrypoint"`
	Query                string         `json:"query"`
	IntentType           string         `json:"intent_type"`
	SelectedCapabilities []string       `json:"selected_capabilities"`
	FinalAnswer          string         `json:"final_answer"`
	Confidence           *float64       `json:"confidence"`
	SourceArtifactIDs    []string       `json:"source_artifact_ids"`
	ToolInvocationIDs    []string       `json:"tool_invocation_ids"`
	Metadata             map[string]any `json:"metadata"`
	CreatedAt            time.Time      `json:"created_at"`
}

type EvaluationRunResponse struct {
	ID                string         `json:"id"`
	ResearchRunID     string         `json:"research_run_id"`
	ReferenceProvider string         `json:"reference_provider"`
	ReferenceModel    *string        `json:"reference_model"`
	ReferenceAnswer   *string        `json:"reference_answer"`
	JudgeModel        *string        `json:"judge_model"`
	JudgeVerdict      map[string]any `json:"judge_verdict"`
	JudgeScores       map[string]any `json:"judge_scores"`
	Winner            *string        `json:"winner"`
	CreatedAt         time.Time      `json:"created_at"`
}

type EvaluationDatasetItemResponse struct {
	ID                 string         `json:"id"`
	ResearchRunID      string         `json:"research_run_id"`
	EvaluationRunID    *string        `json:"evaluation_run_id"`
	Query              string         `json:"query"`
	OrchestratorAnswer string         `json:"orchestrator_answer"`
	ReferenceAnswer    string         `json:"reference_answer"`
	Sources            []SourceRef    `json:"sources"`
	Scores             map[string]any `json:"scores"`
	Winner             *string        `json:"winner"`
	Metadata           map[string]any `json:"metadata"`
	CreatedAt          time.Time      `json:"created_at"`
}

type SourceRef struct {
	Title string `json:"title"`
	URI   string `json:"uri"`
	Kind  string `json:"kind"`
}

type ToolInvocationResponse struct {
	ID           string         `json:"id"`
	TaskID       *string        `json:"task_id"`
	EntryPoint   string         `json:"entrypoint"`
	Capability   string         `json:"capability"`
	Status       string         `json:"status"`
	DurationMS   int            `json:"duration_ms"`
	Summary      *string        `json:"summary"`
	Output       map[string]any `json:"output"`
	SourceRefs   []SourceRef    `json:"source_refs"`
	ArtifactIDs  []string       `json:"artifact_ids"`
	ErrorMessage *string        `json:"error_message"`
	CreatedAt    time.Time      `json:"created_at"`
}

type ArtifactResponse struct {
	ID          string         `json:"id"`
	ArtifactType string        `json:"artifact_type"`
	Title       *string        `json:"title"`
	URI         *string        `json:"uri"`
	MediaType   *string        `json:"media_type"`
	ContentText *string        `json:"content_text"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
}

type WorkerInfo struct {
	WorkerID      string   `json:"worker_id"`
	Status        string   `json:"status"`
	AgentTypes    string   `json:"agent_types"`
	CurrentTask   *string  `json:"current_task"`
	RegisteredAt  *string  `json:"registered_at"`
	LastHeartbeat *string  `json:"last_heartbeat"`
	MemoryMB      *float64 `json:"memory_mb"`
	UptimeS       *float64 `json:"uptime_s"`
}

type WorkerListResponse struct {
	Workers []WorkerInfo `json:"workers"`
	Total   int          `json:"total"`
}

type APIKeyRecord struct {
	ID    string
	Name  string
	Scope ApiKeyScope
}

type TaskStateConflictResponse struct {
	Error            string    `json:"error"`
	CurrentState     TaskState `json:"current_state"`
	RequestedState   TaskState `json:"requested_state"`
	ValidTransitions []string  `json:"valid_transitions"`
	TaskID           string    `json:"task_id"`
}

type CursorPayload struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func EncodeCursor(createdAt time.Time, id string) (string, error) {
	raw, err := json.Marshal(CursorPayload{CreatedAt: createdAt, ID: id})
	if err != nil {
		return "", err
	}
	encoded := base64.URLEncoding.EncodeToString(raw)
	return encoded, nil
}

func DecodeCursor(cursor string) (CursorPayload, error) {
	var payload CursorPayload
	raw, err := base64.URLEncoding.DecodeString(cursor)
	if err != nil {
		return payload, err
	}
	err = json.Unmarshal(raw, &payload)
	return payload, err
}

var terminalStates = map[TaskState]struct{}{
	TaskStateCompleted: {},
	TaskStateFailed:    {},
	TaskStateCancelled: {},
}

var validTransitions = map[TaskState][]TaskState{
	TaskStateCreated:         {TaskStateQueued, TaskStateWaitingApproval, TaskStateCancelled},
	TaskStateQueued:          {TaskStateAssigned, TaskStateCancelled},
	TaskStateAssigned:        {TaskStateInProgress, TaskStateFailed, TaskStateCancelled},
	TaskStateInProgress:      {TaskStateWaitingApproval, TaskStateReview, TaskStateCompleted, TaskStateFailed, TaskStateCancelled, TaskStateRetrying},
	TaskStateWaitingApproval: {TaskStateInProgress, TaskStateQueued, TaskStateCancelled, TaskStateFailed},
	TaskStateRetrying:        {TaskStateInProgress, TaskStateFailed, TaskStateCancelled},
	TaskStateReview:          {TaskStateCompleted, TaskStateInProgress, TaskStateFailed},
}

func IsTerminalState(state TaskState) bool {
	_, ok := terminalStates[state]
	return ok
}

func GetValidTransitions(state TaskState) []TaskState {
	transitions := validTransitions[state]
	if len(transitions) == 0 {
		return []TaskState{}
	}
	result := make([]TaskState, len(transitions))
	copy(result, transitions)
	return result
}

func IsValidTransition(current, target TaskState) bool {
	for _, candidate := range validTransitions[current] {
		if candidate == target {
			return true
		}
	}
	return false
}

func ValidTransitionStrings(state TaskState) []string {
	transitions := GetValidTransitions(state)
	result := make([]string, 0, len(transitions))
	for _, transition := range transitions {
		result = append(result, string(transition))
	}
	return result
}
