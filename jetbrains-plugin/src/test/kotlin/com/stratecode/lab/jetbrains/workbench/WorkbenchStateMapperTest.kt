package com.stratecode.lab.jetbrains.workbench

import com.stratecode.lab.jetbrains.bridge.BridgeConsistency
import com.stratecode.lab.jetbrains.bridge.BridgeResolution
import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeExecutionPolicyRecord
import com.stratecode.lab.jetbrains.client.InitiativeExecutionSummaryRecord
import com.stratecode.lab.jetbrains.client.InitiativeRecord
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.client.InitiativeTaskRecord
import com.stratecode.lab.jetbrains.project.ProjectContext
import com.stratecode.lab.jetbrains.task.EvidenceExtractionResult
import com.stratecode.lab.jetbrains.task.EvidenceLocation
import com.stratecode.lab.jetbrains.task.TaskResultPatchView
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class WorkbenchStateMapperTest {
    @Test
    fun `build plan steps keeps phases and filters task steps`() {
        val detail = initiativeDetail(status = "design_review", currentPhase = "design", taskCount = 2)
        val taskA = taskLink("task-a", "coder", "pending", "agent_local", 1)
        val taskB = taskLink("task-b", "reviewer", "waiting_approval", "manual", 2)
        val approvals = listOf(approval("task-b"))

        val steps = WorkbenchStateMapper.buildPlanSteps(
            detail = detail,
            tasks = listOf(taskA, taskB),
            approvals = approvals,
            patchByTaskId = mapOf("task-a" to TaskResultPatchView("diff --git a/a b/a\n", listOf("a"))),
            evidenceByTaskId = mapOf(
                "task-b" to EvidenceExtractionResult(
                    locations = listOf(EvidenceLocation("foo.kt", 12, sourceType = "code_analysis_report")),
                    rawArtifacts = emptyList(),
                    errors = emptyList(),
                ),
            ),
            statusFilter = "waiting_approval",
            agentFilter = "reviewer",
        )

        assertEquals(5, steps.size)
        assertEquals(PlanStepKind.PHASE, steps.first().kind)
        assertEquals(PlanStepStatus.BLOCKED, steps[1].status)
        val taskStep = steps.last()
        assertEquals("task:task-b", taskStep.stepId)
        assertEquals(PlanStepKind.TASK, taskStep.kind)
        assertTrue(taskStep.approvalRequired)
        assertTrue(taskStep.evidenceAvailable)
        assertFalse(taskStep.diffAvailable)
    }

    @Test
    fun `task action availability blocks patch on stale bridge`() {
        val selected = PlanStepWorkbenchItem(
            stepId = "task:task-a",
            initiativeId = "initiative-1",
            kind = PlanStepKind.TASK,
            title = "Task A",
            subtitle = "coder",
            status = PlanStepStatus.PENDING,
            taskId = "task-a",
            executionMode = "agent_local",
        )
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedStep = selected,
            patchView = TaskResultPatchView("diff --git a/a b/a\n", listOf("a")),
            evidence = null,
            resolution = BridgeResolution(
                matched = null,
                status = "idle",
                detail = "stale",
                consistency = BridgeConsistency.MISSING,
                stale = true,
                executable = false,
            ),
            degraded = false,
            initiativeStatus = "execution",
        )

        assertFalse(availability.canApplyPatch)
        assertNotNull(availability.blockingReason)
    }

    @Test
    fun `approval summary returns blocking approval for selected task`() {
        val summary = WorkbenchStateMapper.buildApprovalSummary(
            approvals = listOf(approval("task-a"), approval("task-b")),
            selectedTaskId = "task-b",
        )

        assertEquals(2, summary.count)
        assertEquals("task-b", summary.selectedTaskApproval?.taskId)
    }

    @Test
    fun `header state reflects approvals and degraded project`() {
        val header = WorkbenchStateMapper.buildHeaderState(
            context = ProjectContext(
                projectName = "lab",
                workspaceRoot = "/repo",
                branch = "main",
                repositoryUrl = null,
                stableBridgeId = "bridge",
                degraded = true,
            ),
            backendReady = true,
            bridge = null,
            approvalCount = 3,
        )

        assertEquals("ready", header.backendLabel)
        assertEquals(StatusTone.WARNING, header.approvalsTone)
        assertTrue(header.degraded)
    }

    @Test
    fun `backend badge shows loading while status refresh is in progress`() {
        val header = WorkbenchStateMapper.buildHeaderState(
            context = ProjectContext(
                projectName = "lab",
                workspaceRoot = "/repo",
                branch = "main",
                repositoryUrl = null,
                stableBridgeId = "bridge",
                degraded = false,
            ),
            backendReady = null,
            bridge = null,
            approvalCount = 0,
        )

        val badge = WorkbenchStateMapper.buildBackendBadgeState(header, backendReady = null, operationStatus = "Refreshing workspace status…")

        assertEquals("loading", badge.label)
        assertEquals(StatusTone.NEUTRAL, badge.tone)
    }

    @Test
    fun `bridge badge shows repairing during auto register`() {
        val header = WorkbenchStateMapper.buildHeaderState(
            context = ProjectContext(
                projectName = "lab",
                workspaceRoot = "/repo",
                branch = "main",
                repositoryUrl = null,
                stableBridgeId = "bridge",
                degraded = false,
            ),
            backendReady = true,
            bridge = BridgeResolution(
                matched = null,
                status = "missing",
                detail = "missing",
                consistency = BridgeConsistency.MISSING,
                stale = false,
                executable = false,
            ),
            approvalCount = 0,
        )

        val badge = WorkbenchStateMapper.buildBridgeBadgeState(header, bridge = null, operationStatus = "Auto-registering bridge…")

        assertEquals("repairing", badge.label)
        assertEquals(StatusTone.NEUTRAL, badge.tone)
    }

    private fun initiativeDetail(
        status: String,
        currentPhase: String,
        taskCount: Int,
    ) = InitiativeDetailResponseRecord(
        initiative = InitiativeRecord(
            id = "initiative-1",
            title = "Test initiative",
            workspaceRoot = "/repo",
            goal = "Ship the feature",
            status = status,
            currentPhase = currentPhase,
            createdBy = "tester",
            executionMode = "selective",
            createdAt = "2026-06-01T00:00:00Z",
            updatedAt = "2026-06-01T00:00:00Z",
        ),
        executionSummary = InitiativeExecutionSummaryRecord(
            backlogMaterialized = taskCount > 0,
            aggregatedStatus = currentPhase,
            taskCount = taskCount,
            pendingManual = 0,
        ),
        executionPolicy = InitiativeExecutionPolicyRecord(
            workspaceRoot = "/repo",
            scope = "local_bridge",
        ),
    )

    private fun taskLink(
        taskId: String,
        agent: String,
        state: String,
        executionMode: String,
        launchOrder: Int,
    ) = InitiativeTaskLinkRecord(
        initiativeId = "initiative-1",
        taskId = taskId,
        phaseOrigin = "plan",
        executionMode = executionMode,
        launchOrder = launchOrder,
        task = InitiativeTaskRecord(
            id = taskId,
            state = state,
            description = "Task $taskId",
            assignedAgent = agent,
            priority = "medium",
            executionTarget = "local_bridge",
        ),
    )

    private fun approval(taskId: String) = ApprovalRecord(
        id = "approval-$taskId",
        taskId = taskId,
        actionType = "apply_patch",
        targetResource = taskId,
        status = "pending",
        requestedAt = "2026-06-01T00:00:00Z",
        timeoutAt = "2026-06-01T01:00:00Z",
    )
}
