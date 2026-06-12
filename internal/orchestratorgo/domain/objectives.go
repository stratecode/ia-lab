package domain

type WorkItemKind string

const (
	WorkItemKindResearch WorkItemKind = "research"
	WorkItemKindAnalyze  WorkItemKind = "analyze"
	WorkItemKindEdit     WorkItemKind = "edit"
	WorkItemKindValidate WorkItemKind = "validate"
	WorkItemKindReview   WorkItemKind = "review"
	WorkItemKindReplan   WorkItemKind = "replan"
)

type ObjectiveRequest struct {
	Title             string                  `json:"title"`
	Objective         string                  `json:"objective"`
	WorkspaceRoot     string                  `json:"workspace_root"`
	CreatedBy         string                  `json:"created_by"`
	TimeBudgetSeconds int                     `json:"time_budget_seconds,omitempty"`
	ExecutionMode     InitiativeExecutionMode `json:"execution_mode"`
}

type WorkItem struct {
	ID                 string          `json:"id"`
	Kind               WorkItemKind    `json:"kind"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	AssignedAgent      AgentType       `json:"assigned_agent"`
	ExecutionMode      string          `json:"execution_mode"`
	ExecutionTarget    ExecutionTarget `json:"execution_target"`
	Backend            string          `json:"backend"`
	DependsOn          []string        `json:"depends_on,omitempty"`
	ApprovalRequired   bool            `json:"approval_required"`
	DefinitionOfDone   string          `json:"definition_of_done"`
	ValidationCommands []string        `json:"validation_commands,omitempty"`
	SuspectedPaths     []string        `json:"suspected_paths,omitempty"`
	ToolRequest        map[string]any  `json:"tool_request,omitempty"`
	Metadata           map[string]any  `json:"metadata,omitempty"`
}

type ExecutionContract struct {
	Title                   string            `json:"title"`
	NormalizedObjective     string            `json:"normalized_objective"`
	WorkspaceRoot           string            `json:"workspace_root"`
	ExecutionBackend        string            `json:"execution_backend"`
	ChangeStrategy          string            `json:"change_strategy"`
	ValidationCommands      []string          `json:"validation_commands"`
	CompletionCriteria      []string          `json:"completion_criteria"`
	TimeBudgetSeconds       int               `json:"time_budget_seconds,omitempty"`
	RiskLevel               string            `json:"risk_level"`
	ApprovalRequired        bool              `json:"approval_required"`
	SuspectedPaths          []string          `json:"suspected_paths,omitempty"`
	InitialScopeHypotheses  []ScopeHypothesis `json:"initial_scope_hypotheses,omitempty"`
	RejectedScopeHypotheses []ScopeHypothesis `json:"rejected_scope_hypotheses,omitempty"`
	RepositoryURL           string            `json:"repository_url,omitempty"`
	RepoProfile             string            `json:"repo_profile,omitempty"`
	WorkItems               []WorkItem        `json:"work_items"`
}

type ScopeHypothesis struct {
	Path            string   `json:"path"`
	Rationale       string   `json:"rationale"`
	Evidence        string   `json:"evidence,omitempty"`
	Confidence      string   `json:"confidence,omitempty"`
	CitationRefs    []string `json:"citation_refs,omitempty"`
	Rank            int      `json:"rank,omitempty"`
	Score           int      `json:"score,omitempty"`
	Disposition     string   `json:"disposition,omitempty"`
	RejectedBecause string   `json:"rejected_because,omitempty"`
}

type ReviewPacket struct {
	ExecutionContract ExecutionContract `json:"execution_contract"`
	Diff              string            `json:"diff"`
	ChangedFiles      []string          `json:"changed_files"`
	TestResults       map[string]any    `json:"test_results"`
	PriorFindings     []string          `json:"prior_findings,omitempty"`
}

type InitiativeResolution struct {
	InitiativeID string           `json:"initiative_id"`
	Status       InitiativeStatus `json:"status"`
	Summary      string           `json:"summary"`
	Decision     string           `json:"decision"`
	Artifacts    []map[string]any `json:"artifacts,omitempty"`
	Metadata     map[string]any   `json:"metadata,omitempty"`
}

type ObjectiveResponse struct {
	Initiative *InitiativeResponse `json:"initiative"`
	Contract   ExecutionContract   `json:"execution_contract"`
	Tasks      []TaskResponse      `json:"tasks"`
	Total      int                 `json:"total"`
}

type ObjectiveStatusSnapshot struct {
	InitiativeID           string           `json:"initiative_id"`
	ObjectiveTitle         string           `json:"objective_title,omitempty"`
	NormalizedObjective    string           `json:"normalized_objective,omitempty"`
	CurrentPhase           string           `json:"current_phase"`
	Iteration              int              `json:"iteration"`
	MaxIterations          int              `json:"max_iterations"`
	RemainingRetries       int              `json:"remaining_retries"`
	WorkItemKind           string           `json:"work_item_kind"`
	ResultStatus           string           `json:"result_status"`
	ReviewDecision         string           `json:"review_decision,omitempty"`
	NeedsRepairCycle       bool             `json:"needs_repair_cycle"`
	NextExpectedAction     string           `json:"next_expected_action"`
	StopReason             string           `json:"stop_reason,omitempty"`
	BlockerReason          string           `json:"blocker_reason,omitempty"`
	TimeBudgetSeconds      int              `json:"time_budget_seconds,omitempty"`
	RemainingBudgetSeconds int              `json:"remaining_budget_seconds,omitempty"`
	DeadlineAt             string           `json:"deadline_at,omitempty"`
	ChangedFiles           []string         `json:"changed_files,omitempty"`
	ValidationCommands     []string         `json:"validation_commands,omitempty"`
	TestResults            map[string]any   `json:"test_results,omitempty"`
	Findings               []map[string]any `json:"findings,omitempty"`
}
