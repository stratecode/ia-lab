package com.stratecode.lab.jetbrains.project

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class KnownInitiativeMetadata(
    val id: String,
    val title: String? = null,
)

@Serializable
data class StrateCodeProjectMetadata(
    @SerialName("project_name") val projectName: String,
    @SerialName("workspace_root") val workspaceRoot: String,
    @SerialName("repository_url") val repositoryUrl: String? = null,
    val branch: String? = null,
    @SerialName("stable_bridge_id") val stableBridgeId: String,
    @SerialName("bridge_name") val bridgeName: String? = null,
    @SerialName("last_initiative_id") val lastInitiativeId: String? = null,
    @SerialName("last_initiative_title") val lastInitiativeTitle: String? = null,
    @SerialName("known_initiatives") val knownInitiatives: List<KnownInitiativeMetadata> = emptyList(),
    @SerialName("updated_at") val updatedAt: String,
)
