package com.stratecode.lab.jetbrains.workbench

import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.task.EvidenceLocation
import com.stratecode.lab.jetbrains.task.TaskResultPatchView

enum class StatusTone {
    HEALTHY,
    WARNING,
    DANGER,
    NEUTRAL,
}

enum class PlanStepKind {
    PHASE,
    TASK,
}

enum class PlanStepStatus {
    PENDING,
    ACTIVE,
    BLOCKED,
    DONE,
}

data class HeaderStatusViewState(
    val projectName: String,
    val workspaceRoot: String,
    val repositoryUrl: String?,
    val degraded: Boolean,
    val backendLabel: String,
    val backendTone: StatusTone,
    val bridgeLabel: String,
    val bridgeTone: StatusTone,
    val approvalsLabel: String,
    val approvalsTone: StatusTone,
    val metadataSummary: String,
)

data class InitiativeWorkbenchItem(
    val id: String,
    val title: String,
    val status: String,
    val currentPhase: String,
    val taskCount: Int,
    val artifactCount: Int,
    val lastReviewSummary: String? = null,
)

data class PlanStepWorkbenchItem(
    val stepId: String,
    val initiativeId: String,
    val kind: PlanStepKind,
    val title: String,
    val subtitle: String,
    val status: PlanStepStatus,
    val phase: String? = null,
    val agent: String? = null,
    val taskId: String? = null,
    val executionMode: String? = null,
    val diffAvailable: Boolean = false,
    val evidenceAvailable: Boolean = false,
    val approvalRequired: Boolean = false,
    val link: InitiativeTaskLinkRecord? = null,
)

data class TaskActionAvailability(
    val canLaunch: Boolean,
    val canSetMode: Boolean,
    val canPreviewDiff: Boolean,
    val canApplyPatch: Boolean,
    val canOpenChangedFile: Boolean,
    val canOpenEvidence: Boolean,
    val canAdvancePhase: Boolean,
    val canApprovePhase: Boolean,
    val canRejectPhase: Boolean,
    val canGenerateTasks: Boolean,
    val canResolveApproval: Boolean,
    val blockingReason: String? = null,
)

data class TaskDetailViewState(
    val title: String,
    val subtitle: String,
    val badgesText: String,
    val overviewText: String,
    val outputText: String,
    val diffText: String,
    val patchView: TaskResultPatchView?,
    val evidenceLocations: List<EvidenceLocation>,
    val evidenceDetailText: String,
    val artifacts: List<InitiativeArtifactRecord>,
    val artifactDetailText: String,
    val approvalCallout: String? = null,
)

data class ApprovalSummaryViewState(
    val count: Int,
    val selectedTaskApproval: ApprovalRecord? = null,
)

data class WorkbenchViewState(
    val header: HeaderStatusViewState,
    val initiatives: List<InitiativeWorkbenchItem>,
    val steps: List<PlanStepWorkbenchItem>,
    val selectedInitiativeId: String? = null,
    val selectedStepId: String? = null,
    val selectedDetail: TaskDetailViewState? = null,
    val approvalSummary: ApprovalSummaryViewState = ApprovalSummaryViewState(0),
)
