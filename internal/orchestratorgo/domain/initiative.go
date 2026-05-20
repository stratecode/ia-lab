package domain

import "time"

type InitiativeStatus string
type InitiativePhase string
type InitiativeReviewDecision string
type InitiativeExecutionMode string

const (
	InitiativeStatusIdeaSubmitted     InitiativeStatus = "idea_submitted"
	InitiativeStatusRequirementsDraft InitiativeStatus = "requirements_draft"
	InitiativeStatusRequirementsReview InitiativeStatus = "requirements_review"
	InitiativeStatusDesignDraft       InitiativeStatus = "design_draft"
	InitiativeStatusDesignReview      InitiativeStatus = "design_review"
	InitiativeStatusPlanDraft         InitiativeStatus = "plan_draft"
	InitiativeStatusPlanReview        InitiativeStatus = "plan_review"
	InitiativeStatusExecutionReady    InitiativeStatus = "execution_ready"
	InitiativeStatusExecuting         InitiativeStatus = "executing"
	InitiativeStatusBlocked           InitiativeStatus = "blocked"
	InitiativeStatusCompleted         InitiativeStatus = "completed"
	InitiativeStatusCancelled         InitiativeStatus = "cancelled"

	InitiativePhaseRequirements InitiativePhase = "requirements"
	InitiativePhaseDesign       InitiativePhase = "design"
	InitiativePhasePlan         InitiativePhase = "plan"
	InitiativePhaseExecution    InitiativePhase = "execution"

	InitiativeReviewGenerated InitiativeReviewDecision = "generated"
	InitiativeReviewApproved  InitiativeReviewDecision = "approved"
	InitiativeReviewRejected  InitiativeReviewDecision = "rejected"

	InitiativeExecutionModeSelective InitiativeExecutionMode = "selective"

	TaskLaunchModeManual     = "manual"
	TaskLaunchModeAgentLocal = "agent_local"
	TaskLaunchModeAgentRemote = "agent_remote"
)

type InitiativeResponse struct {
	ID                         string                    `json:"id"`
	Title                      string                    `json:"title"`
	WorkspaceRoot              string                    `json:"workspace_root"`
	Goal                       string                    `json:"goal"`
	Status                     InitiativeStatus          `json:"status"`
	CurrentPhase               InitiativePhase           `json:"current_phase"`
	ActiveRequirementsArtifactID *string                 `json:"active_requirements_artifact_id"`
	ActiveDesignArtifactID     *string                   `json:"active_design_artifact_id"`
	ActivePlanArtifactID       *string                   `json:"active_plan_artifact_id"`
	CreatedBy                  string                    `json:"created_by"`
	ExecutionMode              InitiativeExecutionMode   `json:"execution_mode"`
	ArchivedAt                 *time.Time                `json:"archived_at"`
	CreatedAt                  time.Time                 `json:"created_at"`
	UpdatedAt                  time.Time                 `json:"updated_at"`
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
	ID               string                   `json:"id"`
	InitiativeID     string                   `json:"initiative_id"`
	Phase            InitiativePhase          `json:"phase"`
	Decision         InitiativeReviewDecision `json:"decision"`
	Feedback         *string                  `json:"feedback"`
	GeneratedBy      *string                  `json:"generated_by"`
	ArtifactMarkdownID *string                `json:"artifact_markdown_id"`
	ArtifactJSONID   *string                  `json:"artifact_json_id"`
	CreatedAt        time.Time                `json:"created_at"`
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
	TaskIDs        []string          `json:"task_ids"`
	Groups         []string          `json:"groups"`
	ModeOverrides  map[string]string `json:"mode_overrides"`
}

type InitiativeTaskModeRequest struct {
	ExecutionMode string `json:"execution_mode"`
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
