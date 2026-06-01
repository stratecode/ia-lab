package com.stratecode.lab.jetbrains.bridge

import com.stratecode.lab.jetbrains.client.LocalBridgeResponse
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime

data class BridgeResolution(
    val matched: LocalBridgeResponse?,
    val status: String,
    val detail: String,
    val consistency: BridgeConsistency,
    val heartbeatAgeSeconds: Long? = null,
    val stale: Boolean = false,
    val executable: Boolean = false,
)

enum class BridgeConsistency {
    MATCHED,
    WARNING,
    MISMATCH,
    MISSING,
}

object BridgeResolver {
    private const val staleThresholdSeconds = 90L

    fun resolve(bridges: List<LocalBridgeResponse>, workspaceRoot: String, bridgeName: String): BridgeResolution {
        val normalizedWorkspace = normalizePath(workspaceRoot)
        val exact = bridges.firstOrNull { normalizePath(it.workspaceRoot) == normalizedWorkspace && it.name == bridgeName }
        if (exact != null) {
            return classify(exact, "Bridge matched by workspace and name.", BridgeConsistency.MATCHED)
        }
        val workspaceMatch = bridges.firstOrNull { normalizePath(it.workspaceRoot) == normalizedWorkspace }
        if (workspaceMatch != null) {
            return classify(workspaceMatch, "Workspace matches but configured bridge name differs.", BridgeConsistency.WARNING)
        }
        val nameMatch = bridges.firstOrNull { it.name == bridgeName }
        if (nameMatch != null) {
            return classify(nameMatch, "Bridge name matches but workspace root differs. Execution must stay blocked.", BridgeConsistency.MISMATCH)
        }
        return BridgeResolution(
            matched = null,
            status = "missing",
            detail = "No registered bridge matches this project yet.",
            consistency = BridgeConsistency.MISSING,
            executable = false,
        )
    }

    private fun normalizePath(path: String): String =
        runCatching { java.io.File(path.trim()).canonicalPath }.getOrElse { java.io.File(path.trim()).absolutePath }

    private fun classify(bridge: LocalBridgeResponse, detail: String, consistency: BridgeConsistency): BridgeResolution {
        val age = heartbeatAgeSeconds(bridge.lastHeartbeat, bridge.updatedAt, bridge.createdAt)
        val stale = age == null || age > staleThresholdSeconds
        val healthyStatus = bridge.status in setOf("online", "idle", "busy", "ready", "active")
        val executable = consistency != BridgeConsistency.MISMATCH && !stale && healthyStatus
        return BridgeResolution(
            matched = bridge,
            status = bridge.status,
            detail = detail,
            consistency = consistency,
            heartbeatAgeSeconds = age,
            stale = stale,
            executable = executable,
        )
    }

    private fun heartbeatAgeSeconds(vararg rawValues: String?): Long? {
        for (raw in rawValues) {
            val value = raw?.trim().orEmpty()
            if (value.isBlank()) {
                continue
            }
            val instant = parseInstant(value) ?: continue
            return Duration.between(instant, Instant.now()).seconds.coerceAtLeast(0)
        }
        return null
    }

    private fun parseInstant(value: String): Instant? =
        runCatching { Instant.parse(value) }.getOrNull()
            ?: runCatching { OffsetDateTime.parse(value).toInstant() }.getOrNull()
            ?: runCatching { Instant.ofEpochMilli(value.toLong()) }.getOrNull()
            ?: runCatching { Instant.ofEpochSecond(value.toLong()) }.getOrNull()
}

fun BridgeResolution.executionBlockReason(): String? {
    if (executable) {
        return null
    }
    return when {
        matched == null -> "No matching bridge is registered for this project."
        consistency == BridgeConsistency.MISMATCH -> "The configured bridge points to another workspace."
        stale -> "The matched bridge heartbeat is stale or missing."
        else -> "The matched bridge status is '${matched.status}', not executable."
    }
}
