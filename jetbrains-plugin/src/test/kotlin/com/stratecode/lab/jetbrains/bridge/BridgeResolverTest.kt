package com.stratecode.lab.jetbrains.bridge

import com.stratecode.lab.jetbrains.client.LocalBridgeResponse
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

class BridgeResolverTest {
    @Test
    fun `exact matching fresh bridge is executable`() {
        val bridge = LocalBridgeResponse(
            id = "bridge-1",
            name = "jetbrains-bridge",
            hostname = "devbox",
            workspaceRoot = "/repo",
            status = "active",
            lastHeartbeat = Instant.now().minusSeconds(10).toString(),
        )

        val resolution = BridgeResolver.resolve(listOf(bridge), "/repo", "jetbrains-bridge")

        assertEquals(BridgeConsistency.MATCHED, resolution.consistency)
        assertTrue(resolution.executable)
        assertFalse(resolution.stale)
        assertNotNull(resolution.heartbeatAgeSeconds)
    }

    @Test
    fun `name match with different workspace blocks execution`() {
        val bridge = LocalBridgeResponse(
            id = "bridge-1",
            name = "jetbrains-bridge",
            hostname = "devbox",
            workspaceRoot = "/other-repo",
            status = "active",
            lastHeartbeat = Instant.now().minusSeconds(5).toString(),
        )

        val resolution = BridgeResolver.resolve(listOf(bridge), "/repo", "jetbrains-bridge")

        assertEquals(BridgeConsistency.MISMATCH, resolution.consistency)
        assertFalse(resolution.executable)
        assertEquals("The configured bridge points to another workspace.", resolution.executionBlockReason())
    }

    @Test
    fun `stale heartbeat blocks execution even with matching workspace`() {
        val bridge = LocalBridgeResponse(
            id = "bridge-1",
            name = "jetbrains-bridge",
            hostname = "devbox",
            workspaceRoot = "/repo",
            status = "idle",
            lastHeartbeat = Instant.now().minusSeconds(600).toString(),
        )

        val resolution = BridgeResolver.resolve(listOf(bridge), "/repo", "jetbrains-bridge")

        assertEquals(BridgeConsistency.MATCHED, resolution.consistency)
        assertTrue(resolution.stale)
        assertFalse(resolution.executable)
        assertEquals("The matched bridge heartbeat is stale or missing.", resolution.executionBlockReason())
    }
}
