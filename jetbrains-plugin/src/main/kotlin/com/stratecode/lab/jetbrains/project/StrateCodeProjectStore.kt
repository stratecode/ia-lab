package com.stratecode.lab.jetbrains.project

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import java.io.File
import java.time.Instant

object StrateCodeProjectStore {
    private val json = Json {
        ignoreUnknownKeys = true
        prettyPrint = true
        explicitNulls = false
    }

    fun metadataFile(workspaceRoot: String): File =
        File(workspaceRoot, ".stratecode/project.json")

    fun read(workspaceRoot: String): StrateCodeProjectMetadata? {
        val file = metadataFile(workspaceRoot)
        if (!file.isFile) {
            return null
        }
        return runCatching {
            json.decodeFromString<StrateCodeProjectMetadata>(file.readText())
        }.getOrNull()
    }

    fun write(
        context: ProjectContext,
        bridgeName: String? = null,
        lastInitiativeId: String? = null,
        lastInitiativeTitle: String? = null,
    ) {
        if (context.workspaceRoot.isBlank()) {
            return
        }
        val existing = read(context.workspaceRoot)
        val file = metadataFile(context.workspaceRoot)
        val metadata = StrateCodeProjectMetadata(
            projectName = context.projectName,
            workspaceRoot = context.workspaceRoot,
            repositoryUrl = context.repositoryUrl ?: existing?.repositoryUrl,
            branch = context.branch ?: existing?.branch,
            stableBridgeId = context.stableBridgeId,
            bridgeName = bridgeName ?: existing?.bridgeName,
            lastInitiativeId = lastInitiativeId ?: existing?.lastInitiativeId,
            lastInitiativeTitle = lastInitiativeTitle ?: existing?.lastInitiativeTitle,
            updatedAt = Instant.now().toString(),
        )
        file.parentFile.mkdirs()
        file.writeText(json.encodeToString(metadata))
    }
}
