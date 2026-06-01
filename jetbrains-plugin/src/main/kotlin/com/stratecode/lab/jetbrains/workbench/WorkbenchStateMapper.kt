package com.stratecode.lab.jetbrains.workbench

import com.stratecode.lab.jetbrains.bridge.BridgeResolution
import com.stratecode.lab.jetbrains.bridge.executionBlockReason
import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.project.ProjectContext
import com.stratecode.lab.jetbrains.task.EvidenceExtractionResult
import com.stratecode.lab.jetbrains.task.TaskResultPatchView

object WorkbenchStateMapper {
    private val phaseOrder = listOf("requirements", "design", "plan", "execution")

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
        val approvalsTone = if (approvalCount > 0) StatusTone.WARNING else StatusTone.NEUTRAL
        val metadata = context.metadata
        val metadataSummary = if (metadata == null) {
            "`.stratecode/project.json` todavía no existe."
        } else {
            buildString {
                append(".stratecode/project.json")
                metadata.bridgeName?.let { append(" · bridge=$it") }
                metadata.lastInitiativeTitle?.let { append(" · last=$it") }
                append(" · known=${metadata.knownInitiatives.size}")
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

    fun buildBackendBadgeState(
        state: HeaderStatusViewState,
        backendReady: Boolean?,
        operationStatus: String?,
    ): BadgeViewState =
        if (operationStatus?.startsWith("Refreshing workspace status") == true && backendReady == null) {
            BadgeViewState("loading", StatusTone.NEUTRAL)
        } else {
            BadgeViewState(state.backendLabel, state.backendTone)
        }

    fun buildBridgeBadgeState(
        state: HeaderStatusViewState,
        bridge: BridgeResolution?,
        operationStatus: String?,
    ): BadgeViewState =
        when {
            operationStatus?.startsWith("Auto-registering bridge") == true ->
                BadgeViewState("repairing", StatusTone.NEUTRAL)
            operationStatus?.startsWith("Refreshing workspace status") == true && bridge == null ->
                BadgeViewState("loading", StatusTone.NEUTRAL)
            else ->
                BadgeViewState(state.bridgeLabel, state.bridgeTone)
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

    fun buildPlanSteps(
        detail: InitiativeDetailResponseRecord,
        tasks: List<InitiativeTaskLinkRecord>,
        approvals: List<ApprovalRecord>,
        patchByTaskId: Map<String, TaskResultPatchView?>,
        evidenceByTaskId: Map<String, EvidenceExtractionResult?>,
        statusFilter: String,
        agentFilter: String,
    ): List<PlanStepWorkbenchItem> {
        val phaseSteps = phaseOrder.map { phase ->
            PlanStepWorkbenchItem(
                stepId = "phase:$phase",
                initiativeId = detail.initiative.id,
                kind = PlanStepKind.PHASE,
                title = phaseTitle(phase),
                subtitle = phaseSubtitle(phase, detail),
                status = phaseStatus(phase, detail, approvals),
                phase = phase,
                approvalRequired = phase == detail.initiative.currentPhase && detail.initiative.status.endsWith("_review"),
            )
        }
        val taskSteps = tasks.map { link ->
            val agent = link.task.assignedAgent ?: link.task.plannedAgent ?: "unassigned"
            val approvalRequired = approvals.any { it.taskId == link.taskId }
            PlanStepWorkbenchItem(
                stepId = "task:${link.taskId}",
                initiativeId = link.initiativeId,
                kind = PlanStepKind.TASK,
                title = link.task.description,
                subtitle = "$agent · ${link.task.state} · ${link.executionMode}",
                status = taskStatus(link.task.state, approvalRequired),
                phase = link.phaseOrigin,
                agent = agent,
                taskId = link.taskId,
                executionMode = link.executionMode,
                diffAvailable = patchByTaskId[link.taskId] != null,
                evidenceAvailable = evidenceByTaskId[link.taskId]?.locations?.isNotEmpty() == true,
                approvalRequired = approvalRequired,
                link = link,
            )
        }.filter { matchesStatusFilter(it.status, statusFilter) && matchesAgentFilter(it.agent, agentFilter) }
            .sortedBy { it.link?.launchOrder ?: Int.MAX_VALUE }
        return phaseSteps + taskSteps
    }

    fun buildTaskActionAvailability(
        selectedStep: PlanStepWorkbenchItem?,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult?,
        resolution: BridgeResolution?,
        degraded: Boolean,
        initiativeStatus: String?,
    ): TaskActionAvailability {
        val blockReason = when {
            resolution == null -> "No bridge state is loaded for this project."
            else -> resolution.executionBlockReason()
        }
        val executable = blockReason == null
        val isTask = selectedStep?.kind == PlanStepKind.TASK
        val isPhase = selectedStep?.kind == PlanStepKind.PHASE
        val phase = selectedStep?.phase
        val hasPatch = patchView != null
        val hasChangedFiles = patchView?.changedFiles?.isNotEmpty() == true
        val hasEvidence = evidence?.locations?.isNotEmpty() == true
        return TaskActionAvailability(
            canLaunch = isTask && executable,
            canSetMode = isTask,
            canPreviewDiff = isTask && hasPatch,
            canApplyPatch = isTask && hasPatch && executable && !degraded,
            canOpenChangedFile = isTask && hasChangedFiles,
            canOpenEvidence = isTask && hasEvidence,
            canAdvancePhase = isPhase && phase in setOf("requirements", "design") && initiativeStatus?.endsWith("_draft") == true,
            canApprovePhase = isPhase && phase in setOf("requirements", "design", "plan") && initiativeStatus?.endsWith("_review") == true,
            canRejectPhase = isPhase && phase in setOf("requirements", "design", "plan") && initiativeStatus?.endsWith("_review") == true,
            canGenerateTasks = isPhase && phase == "plan" && initiativeStatus == "plan_draft",
            canResolveApproval = selectedStep?.approvalRequired == true,
            blockingReason = if (degraded && isTask && hasPatch) {
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

    private fun phaseStatus(
        phase: String,
        detail: InitiativeDetailResponseRecord,
        approvals: List<ApprovalRecord>,
    ): PlanStepStatus {
        val currentIndex = phaseOrder.indexOf(detail.initiative.currentPhase).coerceAtLeast(0)
        val phaseIndex = phaseOrder.indexOf(phase).coerceAtLeast(0)
        if (phase == "execution") {
            if (detail.executionSummary.taskCount == 0 && currentIndex < phaseIndex) {
                return PlanStepStatus.PENDING
            }
            val status = detail.executionSummary.aggregatedStatus.lowercase()
            return when {
                currentIndex < phaseIndex -> PlanStepStatus.PENDING
                "completed" in status || "succeeded" in status || "done" in status -> PlanStepStatus.DONE
                approvals.isNotEmpty() || "approval" in status -> PlanStepStatus.BLOCKED
                else -> PlanStepStatus.ACTIVE
            }
        }
        if (phaseIndex < currentIndex) {
            return PlanStepStatus.DONE
        }
        if (phaseIndex > currentIndex) {
            return PlanStepStatus.PENDING
        }
        return if (detail.initiative.status.endsWith("_review")) PlanStepStatus.BLOCKED else PlanStepStatus.ACTIVE
    }

    private fun taskStatus(state: String, approvalRequired: Boolean): PlanStepStatus {
        val normalized = state.lowercase()
        return when {
            approvalRequired || "approval" in normalized -> PlanStepStatus.BLOCKED
            normalized in setOf("completed", "success", "succeeded", "done") -> PlanStepStatus.DONE
            normalized in setOf("running", "executing", "in_progress", "working") -> PlanStepStatus.ACTIVE
            else -> PlanStepStatus.PENDING
        }
    }

    private fun matchesStatusFilter(status: PlanStepStatus, filter: String): Boolean {
        if (filter == "all") {
            return true
        }
        return when (filter) {
            "pending" -> status == PlanStepStatus.PENDING
            "running" -> status == PlanStepStatus.ACTIVE
            "waiting_approval" -> status == PlanStepStatus.BLOCKED
            "completed" -> status == PlanStepStatus.DONE
            "failed" -> false
            else -> true
        }
    }

    private fun matchesAgentFilter(agent: String?, filter: String): Boolean =
        filter == "all" || agent.equals(filter, ignoreCase = true)

    private fun phaseTitle(phase: String): String =
        when (phase) {
            "requirements" -> "Define requirements"
            "design" -> "Shape design"
            "plan" -> "Lock execution plan"
            "execution" -> "Run and review tasks"
            else -> phase.replaceFirstChar { it.uppercase() }
        }

    private fun phaseSubtitle(phase: String, detail: InitiativeDetailResponseRecord): String =
        when (phase) {
            "requirements" -> detail.histories.firstOrNull { it.phase == phase }?.items?.size?.let { "$it versions tracked" } ?: "Requirements draft and review"
            "design" -> detail.histories.firstOrNull { it.phase == phase }?.items?.size?.let { "$it versions tracked" } ?: "Design draft and review"
            "plan" -> detail.histories.firstOrNull { it.phase == phase }?.items?.size?.let { "$it versions tracked" } ?: "Plan draft and task generation"
            "execution" -> "${detail.executionSummary.taskCount} task steps · ${detail.executionSummary.aggregatedStatus}"
            else -> detail.initiative.status
        }
}
