package com.stratecode.lab.jetbrains.client

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.decodeFromJsonElement
import kotlinx.serialization.json.jsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

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

    @Test
    fun `advance response wrapper contains initiative record`() {
        val raw = """
            {
              "initiative": {
                "id": "init-1",
                "title": "Test",
                "workspace_root": "/repo",
                "goal": "goal",
                "status": "requirements_review",
                "current_phase": "requirements",
                "created_by": "tester",
                "execution_mode": "selective",
                "created_at": "2026-06-01T10:00:00Z",
                "updated_at": "2026-06-01T10:00:05Z"
              },
              "artifacts": {
                "markdown_id": "artifact-md",
                "json_id": "artifact-json"
              }
            }
        """.trimIndent()

        val element = json.parseToJsonElement(raw)
        val nested = element.jsonObject["initiative"]

        assertTrue(nested != null)
        val record: InitiativeRecord = json.decodeFromJsonElement(nested)
        assertEquals("init-1", record.id)
        assertEquals("requirements_review", record.status)
    }
}
