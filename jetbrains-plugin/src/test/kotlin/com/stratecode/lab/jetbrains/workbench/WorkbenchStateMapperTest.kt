package com.stratecode.lab.jetbrains.workbench

import com.stratecode.lab.jetbrains.bridge.BridgeConsistency
import com.stratecode.lab.jetbrains.bridge.BridgeResolution
import com.stratecode.lab.jetbrains.client.ApprovalRecord
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
    fun `build task items applies filters and badges`() {
        val taskA = taskLink("task-a", "coder", "pending", "agent_local", 1)
        val taskB = taskLink("task-b", "reviewer", "waiting_approval", "manual", 2)
        val approvals = listOf(approval("task-b"))
        val tasks = WorkbenchStateMapper.buildTaskItems(
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

        assertEquals(1, tasks.size)
        assertEquals("task-b", tasks.first().taskId)
        assertTrue(tasks.first().approvalRequired)
        assertTrue(tasks.first().evidenceAvailable)
        assertFalse(tasks.first().diffAvailable)
    }

    @Test
    fun `task action availability blocks patch on stale bridge`() {
        val selected = taskLink("task-a", "coder", "pending", "agent_local", 1)
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedTask = WorkbenchStateMapper.buildTaskItems(
                listOf(selected),
                approvals = emptyList(),
                patchByTaskId = mapOf("task-a" to TaskResultPatchView("diff --git a/a b/a\n", listOf("a"))),
                evidenceByTaskId = emptyMap(),
                statusFilter = "all",
                agentFilter = "all",
            ).first(),
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
