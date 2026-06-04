package domain

type WorkItemKind string

const (
	WorkItemKindResearch WorkItemKind = "research"
	WorkItemKindEdit     WorkItemKind = "edit"
	WorkItemKindValidate WorkItemKind = "validate"
	WorkItemKindReview   WorkItemKind = "review"
	WorkItemKindReplan   WorkItemKind = "replan"
)

type ObjectiveRequest struct {
	Title         string                  `json:"title"`
	Objective     string                  `json:"objective"`
	WorkspaceRoot string                  `json:"workspace_root"`
	CreatedBy     string                  `json:"created_by"`
	ExecutionMode InitiativeExecutionMode `json:"execution_mode"`
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
	Title               string     `json:"title"`
	NormalizedObjective string     `json:"normalized_objective"`
	WorkspaceRoot       string     `json:"workspace_root"`
	ExecutionBackend    string     `json:"execution_backend"`
	ChangeStrategy      string     `json:"change_strategy"`
	ValidationCommands  []string   `json:"validation_commands"`
	CompletionCriteria  []string   `json:"completion_criteria"`
	RiskLevel           string     `json:"risk_level"`
	ApprovalRequired    bool       `json:"approval_required"`
	SuspectedPaths      []string   `json:"suspected_paths,omitempty"`
	RepositoryURL       string     `json:"repository_url,omitempty"`
	RepoProfile         string     `json:"repo_profile,omitempty"`
	WorkItems           []WorkItem `json:"work_items"`
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
