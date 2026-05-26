package com.stratecode.lab.jetbrains.bridge

import com.stratecode.lab.jetbrains.client.LocalBridgeResponse
import kotlin.test.Test
import kotlin.test.assertEquals

class BridgeResolverTest {
    @Test
    fun `matches exact workspace and bridge name`() {
        val bridge = LocalBridgeResponse(
            id = "1",
            name = "jetbrains-bridge",
            hostname = "devbox",
            workspaceRoot = "/workspace/repo",
            status = "active",
        )

        val resolution = BridgeResolver.resolve(listOf(bridge), "/workspace/repo", "jetbrains-bridge")

        assertEquals("active", resolution.status)
        assertEquals("Bridge matched by workspace and name.", resolution.detail)
        assertEquals("1", resolution.matched?.id)
    }

    @Test
    fun `returns missing when nothing matches`() {
        val resolution = BridgeResolver.resolve(emptyList(), "/workspace/repo", "jetbrains-bridge")
        assertEquals("missing", resolution.status)
        assertEquals("No registered bridge matches this project yet.", resolution.detail)
    }
}
