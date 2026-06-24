package domain

import "time"

type InitiativeStatus string
type InitiativePhase string
type InitiativeReviewDecision string
type InitiativeExecutionMode string

const (
	InitiativeStatusIdeaSubmitted      InitiativeStatus = "idea_submitted"
	InitiativeStatusRequirementsDraft  InitiativeStatus = "requirements_draft"
	InitiativeStatusRequirementsReview InitiativeStatus = "requirements_review"
	InitiativeStatusDesignDraft        InitiativeStatus = "design_draft"
	InitiativeStatusDesignReview       InitiativeStatus = "design_review"
	InitiativeStatusPlanDraft          InitiativeStatus = "plan_draft"
	InitiativeStatusPlanReview         InitiativeStatus = "plan_review"
	InitiativeStatusExecutionReady     InitiativeStatus = "execution_ready"
	InitiativeStatusExecuting          InitiativeStatus = "executing"
	InitiativeStatusBlocked            InitiativeStatus = "blocked"
	InitiativeStatusCompleted          InitiativeStatus = "completed"
	InitiativeStatusCancelled          InitiativeStatus = "cancelled"

	InitiativePhaseRequirements InitiativePhase = "requirements"
	InitiativePhaseDesign       InitiativePhase = "design"
	InitiativePhasePlan         InitiativePhase = "plan"
	InitiativePhaseExecution    InitiativePhase = "execution"

	InitiativeReviewGenerated InitiativeReviewDecision = "generated"
	InitiativeReviewApproved  InitiativeReviewDecision = "approved"
	InitiativeReviewRejected  InitiativeReviewDecision = "rejected"

	InitiativeExecutionModeSelective InitiativeExecutionMode = "selective"

	TaskLaunchModeManual      = "manual"
	TaskLaunchModeAgentLocal  = "agent_local"
	TaskLaunchModeAgentRemote = "agent_remote"
)

type InitiativeResponse struct {
	ID                           string                  `json:"id"`
	Title                        string                  `json:"title"`
	WorkspaceRoot                string                  `json:"workspace_root"`
	Goal                         string                  `json:"goal"`
	Status                       InitiativeStatus        `json:"status"`
	CurrentPhase                 InitiativePhase         `json:"current_phase"`
	ActiveRequirementsArtifactID *string                 `json:"active_requirements_artifact_id"`
	ActiveDesignArtifactID       *string                 `json:"active_design_artifact_id"`
	ActivePlanArtifactID         *string                 `json:"active_plan_artifact_id"`
	CreatedBy                    string                  `json:"created_by"`
	ExecutionMode                InitiativeExecutionMode `json:"execution_mode"`
	ArchivedAt                   *time.Time              `json:"archived_at"`
	CreatedAt                    time.Time               `json:"created_at"`
	UpdatedAt                    time.Time               `json:"updated_at"`
}

type InitiativeListResponse struct {
	Items []InitiativeResponse `json:"items"`
	Total int                  `json:"total"`
}

type InitiativeCreateRequest struct {
	Title         string                  `json:"title"`
	WorkspaceRoot string                  `json:"workspace_root"`
	Goal          string                  `json:"goal"`
	CreatedBy     string                  `json:"created_by"`
	ExecutionMode InitiativeExecutionMode `json:"execution_mode"`
}

type InitiativeAdvanceRequest struct {
	Feedback string `json:"feedback"`
}

type InitiativeReviewRequest struct {
	Operator string `json:"operator"`
	Feedback string `json:"feedback"`
}

type InitiativePhaseReviewResponse struct {
	ID                 string                   `json:"id"`
	InitiativeID       string                   `json:"initiative_id"`
	Phase              InitiativePhase          `json:"phase"`
	Decision           InitiativeReviewDecision `json:"decision"`
	Feedback           *string                  `json:"feedback"`
	GeneratedBy        *string                  `json:"generated_by"`
	ArtifactMarkdownID *string                  `json:"artifact_markdown_id"`
	ArtifactJSONID     *string                  `json:"artifact_json_id"`
	CreatedAt          time.Time                `json:"created_at"`
}

type InitiativePhaseHistoryEntry struct {
	Review       InitiativePhaseReviewResponse `json:"review"`
	Version      int                           `json:"version"`
	DiffSummary  *string                       `json:"diff_summary"`
	ArtifactType *string                       `json:"artifact_type"`
	CreatedAt    time.Time                     `json:"created_at"`
}

type InitiativePhaseHistoryResponse struct {
	Phase         InitiativePhase               `json:"phase"`
	ActiveVersion int                           `json:"active_version"`
	Items         []InitiativePhaseHistoryEntry `json:"items"`
}

type InitiativeExecutionSummaryResponse struct {
	BacklogMaterialized bool             `json:"backlog_materialized"`
	AggregatedStatus    InitiativeStatus `json:"aggregated_status"`
	TaskCount           int              `json:"task_count"`
	ModeCounts          map[string]int   `json:"mode_counts"`
	StateCounts         map[string]int   `json:"state_counts"`
	PendingManual       int              `json:"pending_manual"`
}

type InitiativeExecutionPolicyResponse struct {
	WorkspaceRoot         string   `json:"workspace_root"`
	Scope                 string   `json:"scope"`
	AllowedModes          []string `json:"allowed_modes"`
	ApprovalRequiredModes []string `json:"approval_required_modes"`
}

type InitiativeObjectiveRuntimeViewResponse struct {
	LatestStatusSnapshot          *ObjectiveStatusSnapshot `json:"latest_status_snapshot,omitempty"`
	LatestCoderPacket             *ArtifactResponse        `json:"latest_coder_packet,omitempty"`
	LatestReviewPacket            *ArtifactResponse        `json:"latest_review_packet,omitempty"`
	LatestRepairSignal            *ArtifactResponse        `json:"latest_repair_signal,omitempty"`
	LatestRetrievalPacket         *ArtifactResponse        `json:"latest_retrieval_packet,omitempty"`
	LatestPlanningRetrievalPacket *ArtifactResponse        `json:"latest_planning_retrieval_packet,omitempty"`
}

type InitiativeDetailResponse struct {
	Initiative       *InitiativeResponse                     `json:"initiative"`
	Reviews          []InitiativePhaseReviewResponse         `json:"reviews"`
	Histories        []InitiativePhaseHistoryResponse        `json:"histories"`
	ExecutionSummary InitiativeExecutionSummaryResponse      `json:"execution_summary"`
	ExecutionPolicy  InitiativeExecutionPolicyResponse       `json:"execution_policy"`
	ObjectiveRuntime *InitiativeObjectiveRuntimeViewResponse `json:"objective_runtime,omitempty"`
}

type InitiativeArtifactsResponse struct {
	Items []ArtifactResponse `json:"items"`
	Total int                `json:"total"`
}

type InitiativeTaskLinkResponse struct {
	InitiativeID  string       `json:"initiative_id"`
	TaskID        string       `json:"task_id"`
	PhaseOrigin   string       `json:"phase_origin"`
	Epic          *string      `json:"epic"`
	LaunchGroup   *string      `json:"launch_group"`
	ExecutionMode string       `json:"execution_mode"`
	LaunchOrder   int          `json:"launch_order"`
	CreatedAt     time.Time    `json:"created_at"`
	Task          TaskResponse `json:"task"`
}

type InitiativeTaskListResponse struct {
	Items []InitiativeTaskLinkResponse `json:"items"`
	Total int                          `json:"total"`
}

type InitiativeLaunchTasksRequest struct {
	TaskIDs       []string          `json:"task_ids"`
	Groups        []string          `json:"groups"`
	ModeOverrides map[string]string `json:"mode_overrides"`
}

type InitiativeTaskModeRequest struct {
	ExecutionMode string `json:"execution_mode"`
}

type AutonomousInitiativeRequest struct {
	Surface           string `json:"surface"`
	WorkspaceAlias    string `json:"workspace_alias"`
	WorkspaceRoot     string `json:"workspace_root"`
	Goal              string `json:"goal"`
	OperatorID        string `json:"operator_id"`
	AutoApprovePhases bool   `json:"auto_approve_phases"`
}

type AutonomousRunResult struct {
	InitiativeID  string           `json:"initiative_id"`
	WorkspaceRoot string           `json:"workspace_root"`
	Status        InitiativeStatus `json:"status"`
	CurrentPhase  InitiativePhase  `json:"current_phase"`
	TasksCreated  int              `json:"tasks_created"`
	TasksLaunched int              `json:"tasks_launched"`
	PendingAction string           `json:"pending_action"`
	Summary       string           `json:"summary"`
}

func IsRecognizedInitiativePhase(phase InitiativePhase) bool {
	switch phase {
	case InitiativePhaseRequirements, InitiativePhaseDesign, InitiativePhasePlan, InitiativePhaseExecution:
		return true
	default:
		return false
	}
}

func IsRecognizedInitiativeStatus(status InitiativeStatus) bool {
	switch status {
	case InitiativeStatusIdeaSubmitted, InitiativeStatusRequirementsDraft, InitiativeStatusRequirementsReview,
		InitiativeStatusDesignDraft, InitiativeStatusDesignReview, InitiativeStatusPlanDraft,
		InitiativeStatusPlanReview, InitiativeStatusExecutionReady, InitiativeStatusExecuting,
		InitiativeStatusBlocked, InitiativeStatusCompleted, InitiativeStatusCancelled:
		return true
	default:
		return false
	}
}

func IsRecognizedTaskLaunchMode(mode string) bool {
	switch mode {
	case TaskLaunchModeManual, TaskLaunchModeAgentLocal, TaskLaunchModeAgentRemote:
		return true
	default:
		return false
	}
}
