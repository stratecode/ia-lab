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

data class TaskWorkbenchItem(
    val taskId: String,
    val title: String,
    val state: String,
    val agent: String,
    val executionMode: String,
    val executionTarget: String,
    val launchOrder: Int,
    val diffAvailable: Boolean,
    val evidenceAvailable: Boolean,
    val approvalRequired: Boolean,
    val link: InitiativeTaskLinkRecord,
)

data class TaskActionAvailability(
    val canLaunch: Boolean,
    val canSetMode: Boolean,
    val canPreviewDiff: Boolean,
    val canApplyPatch: Boolean,
    val canOpenChangedFile: Boolean,
    val canOpenEvidence: Boolean,
    val canResolveApproval: Boolean,
    val blockingReason: String? = null,
)

data class TaskDetailViewState(
    val taskId: String,
    val title: String,
    val agent: String,
    val state: String,
    val executionMode: String,
    val updatedAt: String,
    val summaryText: String,
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
    val tasks: List<TaskWorkbenchItem>,
    val selectedInitiativeId: String? = null,
    val selectedTaskId: String? = null,
    val selectedTaskDetail: TaskDetailViewState? = null,
    val approvalSummary: ApprovalSummaryViewState = ApprovalSummaryViewState(0),
)
