package com.stratecode.lab.jetbrains.workbench

import com.stratecode.lab.jetbrains.bridge.BridgeResolution
import com.stratecode.lab.jetbrains.bridge.executionBlockReason
import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.project.ProjectContext
import com.stratecode.lab.jetbrains.task.EvidenceExtractionResult
import com.stratecode.lab.jetbrains.task.TaskResultPatchView

object WorkbenchStateMapper {
    fun buildHeaderState(
        context: ProjectContext,
        backendReady: Boolean?,
        bridge: BridgeResolution?,
        approvalCount: Int,
    ): HeaderStatusViewState {
        val backendTone = when (backendReady) {
            true -> StatusTone.HEALTHY
            false -> StatusTone.WARNING
            null -> StatusTone.DANGER
        }
        val bridgeTone = when {
            bridge == null -> StatusTone.WARNING
            bridge.executable -> StatusTone.HEALTHY
            bridge.executionBlockReason() != null -> StatusTone.WARNING
            else -> StatusTone.NEUTRAL
        }
        val approvalsTone = when {
            approvalCount > 0 -> StatusTone.WARNING
            else -> StatusTone.NEUTRAL
        }
        val metadata = context.metadata
        val metadataSummary = if (metadata == null) {
            "`.stratecode/project.json` todavía no existe."
        } else {
            buildString {
                append(".stratecode/project.json")
                metadata.bridgeName?.let { append(" · bridge=$it") }
                metadata.lastInitiativeTitle?.let { append(" · last=$it") }
            }
        }
        return HeaderStatusViewState(
            projectName = context.projectName,
            workspaceRoot = context.workspaceRoot,
            repositoryUrl = context.repositoryUrl,
            degraded = context.degraded,
            backendLabel = when (backendReady) {
                true -> "ready"
                false -> "not ready"
                null -> "error"
            },
            backendTone = backendTone,
            bridgeLabel = bridge?.status ?: "unresolved",
            bridgeTone = bridgeTone,
            approvalsLabel = if (approvalCount > 0) "$approvalCount pending" else "0 pending",
            approvalsTone = approvalsTone,
            metadataSummary = metadataSummary,
        )
    }

    fun buildInitiatives(
        initiatives: List<InitiativeSummary>,
        detailById: Map<String, InitiativeDetailResponseRecord>,
        artifactCountByInitiative: Map<String, Int>,
    ): List<InitiativeWorkbenchItem> =
        initiatives.map { initiative ->
            val detail = detailById[initiative.id]
            InitiativeWorkbenchItem(
                id = initiative.id,
                title = initiative.title,
                status = initiative.status,
                currentPhase = initiative.currentPhase,
                taskCount = detail?.executionSummary?.taskCount ?: 0,
                artifactCount = artifactCountByInitiative[initiative.id] ?: 0,
                lastReviewSummary = detail?.reviews?.maxByOrNull { it.createdAt }?.let { "${it.phase} · ${it.decision}" },
            )
        }

    fun buildTaskItems(
        tasks: List<InitiativeTaskLinkRecord>,
        approvals: List<ApprovalRecord>,
        patchByTaskId: Map<String, TaskResultPatchView?>,
        evidenceByTaskId: Map<String, EvidenceExtractionResult?>,
        statusFilter: String,
        agentFilter: String,
    ): List<TaskWorkbenchItem> =
        tasks.map { link ->
            val agent = link.task.assignedAgent ?: link.task.plannedAgent ?: "unassigned"
            TaskWorkbenchItem(
                taskId = link.taskId,
                title = link.task.description,
                state = link.task.state,
                agent = agent,
                executionMode = link.executionMode,
                executionTarget = link.task.executionTarget,
                launchOrder = link.launchOrder,
                diffAvailable = patchByTaskId[link.taskId] != null,
                evidenceAvailable = evidenceByTaskId[link.taskId]?.locations?.isNotEmpty() == true,
                approvalRequired = approvals.any { it.taskId == link.taskId },
                link = link,
            )
        }.filter { matchesStatusFilter(it.state, statusFilter) && matchesAgentFilter(it.agent, agentFilter) }
            .sortedBy { it.launchOrder }

    fun buildTaskActionAvailability(
        selectedTask: TaskWorkbenchItem?,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult?,
        resolution: BridgeResolution?,
        degraded: Boolean,
    ): TaskActionAvailability {
        val blockReason = when {
            resolution == null -> "No bridge state is loaded for this project."
            else -> resolution.executionBlockReason()
        }
        val executable = blockReason == null
        val hasPatch = patchView != null
        val hasChangedFiles = patchView?.changedFiles?.isNotEmpty() == true
        val hasEvidence = evidence?.locations?.isNotEmpty() == true
        return TaskActionAvailability(
            canLaunch = selectedTask != null && executable,
            canSetMode = selectedTask != null,
            canPreviewDiff = selectedTask != null && hasPatch,
            canApplyPatch = selectedTask != null && hasPatch && executable && !degraded,
            canOpenChangedFile = selectedTask != null && hasChangedFiles,
            canOpenEvidence = selectedTask != null && hasEvidence,
            canResolveApproval = selectedTask?.approvalRequired == true,
            blockingReason = if (degraded && selectedTask != null && hasPatch) {
                "Project is in degraded mode or missing repository_url."
            } else {
                blockReason
            },
        )
    }

    fun buildApprovalSummary(
        approvals: List<ApprovalRecord>,
        selectedTaskId: String?,
    ): ApprovalSummaryViewState =
        ApprovalSummaryViewState(
            count = approvals.size,
            selectedTaskApproval = selectedTaskId?.let { taskId -> approvals.firstOrNull { it.taskId == taskId } },
        )

    private fun matchesStatusFilter(state: String, filter: String): Boolean {
        if (filter == "all") {
            return true
        }
        val normalized = state.lowercase()
        return when (filter) {
            "pending" -> normalized in setOf("pending", "queued", "created", "manual", "ready", "draft")
            "running" -> normalized in setOf("running", "executing", "in_progress", "working")
            "waiting_approval" -> "approval" in normalized
            "completed" -> normalized in setOf("completed", "success", "succeeded", "done")
            "failed" -> normalized in setOf("failed", "error", "cancelled")
            else -> true
        }
    }

    private fun matchesAgentFilter(agent: String, filter: String): Boolean =
        filter == "all" || agent.equals(filter, ignoreCase = true)
}
