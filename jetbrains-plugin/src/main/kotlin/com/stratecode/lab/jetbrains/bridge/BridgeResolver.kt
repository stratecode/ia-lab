package com.stratecode.lab.jetbrains.bridge

import com.stratecode.lab.jetbrains.client.LocalBridgeResponse

data class BridgeResolution(
    val matched: LocalBridgeResponse?,
    val status: String,
    val detail: String,
)

object BridgeResolver {
    fun resolve(bridges: List<LocalBridgeResponse>, workspaceRoot: String, bridgeName: String): BridgeResolution {
        val exact = bridges.firstOrNull { it.workspaceRoot == workspaceRoot && it.name == bridgeName }
        if (exact != null) {
            return BridgeResolution(exact, exact.status, "Bridge matched by workspace and name.")
        }
        val workspaceMatch = bridges.firstOrNull { it.workspaceRoot == workspaceRoot }
        if (workspaceMatch != null) {
            return BridgeResolution(workspaceMatch, workspaceMatch.status, "Workspace matches but bridge name differs.")
        }
        val nameMatch = bridges.firstOrNull { it.name == bridgeName }
        if (nameMatch != null) {
            return BridgeResolution(nameMatch, nameMatch.status, "Bridge name matches but workspace root differs.")
        }
        return BridgeResolution(null, "missing", "No registered bridge matches this project yet.")
    }
}
