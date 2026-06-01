package com.stratecode.lab.jetbrains.client

import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class OrchestratorClientParsingTest {
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Test
    fun `initiative detail tolerates null history items`() {
        val raw = """
            {
              "initiative": {
                "id": "init-1",
                "title": "Test",
                "workspace_root": "/repo",
                "goal": "goal",
                "status": "requirements_draft",
                "current_phase": "requirements",
                "created_by": "tester",
                "execution_mode": "selective",
                "created_at": "2026-06-01T10:00:00Z",
                "updated_at": "2026-06-01T10:00:00Z"
              },
              "reviews": [],
              "histories": [
                {
                  "phase": "requirements",
                  "active_version": 0,
                  "items": null
                }
              ],
              "execution_summary": {
                "backlog_materialized": false,
                "aggregated_status": "requirements_draft",
                "task_count": 0,
                "pending_manual": 0
              },
              "execution_policy": {
                "workspace_root": "/repo",
                "scope": "local_bridge",
                "allowed_modes": ["manual", "agent_local"],
                "approval_required_modes": []
              }
            }
        """.trimIndent()

        val detail = json.decodeFromString<InitiativeDetailResponseRecord>(raw)

        assertEquals(1, detail.histories.size)
        assertNull(detail.histories.first().items)
    }
}
