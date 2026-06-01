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
        knownInitiatives: List<KnownInitiativeMetadata>? = null,
    ) {
        if (context.workspaceRoot.isBlank()) {
            return
        }
        val existing = read(context.workspaceRoot)
        val file = metadataFile(context.workspaceRoot)
        val mergedKnownInitiatives = when {
            knownInitiatives != null -> knownInitiatives.distinctBy { it.id }
            !lastInitiativeId.isNullOrBlank() -> mergeKnownInitiatives(
                existing?.knownInitiatives.orEmpty(),
                KnownInitiativeMetadata(lastInitiativeId, lastInitiativeTitle),
            )
            else -> existing?.knownInitiatives.orEmpty()
        }
        val metadata = StrateCodeProjectMetadata(
            projectName = context.projectName,
            workspaceRoot = context.workspaceRoot,
            repositoryUrl = context.repositoryUrl ?: existing?.repositoryUrl,
            branch = context.branch ?: existing?.branch,
            stableBridgeId = context.stableBridgeId,
            bridgeName = bridgeName ?: existing?.bridgeName,
            lastInitiativeId = lastInitiativeId ?: existing?.lastInitiativeId,
            lastInitiativeTitle = lastInitiativeTitle ?: existing?.lastInitiativeTitle,
            knownInitiatives = mergedKnownInitiatives,
            updatedAt = Instant.now().toString(),
        )
        file.parentFile.mkdirs()
        file.writeText(json.encodeToString(metadata))
    }

    fun rememberInitiative(
        context: ProjectContext,
        initiativeId: String,
        initiativeTitle: String? = null,
        bridgeName: String? = null,
    ) {
        val existing = read(context.workspaceRoot)
        write(
            context = context,
            bridgeName = bridgeName ?: existing?.bridgeName,
            lastInitiativeId = initiativeId,
            lastInitiativeTitle = initiativeTitle ?: existing?.lastInitiativeTitle,
            knownInitiatives = mergeKnownInitiatives(
                existing?.knownInitiatives.orEmpty(),
                KnownInitiativeMetadata(initiativeId, initiativeTitle),
            ),
        )
    }

    fun clear(workspaceRoot: String) {
        val file = metadataFile(workspaceRoot)
        if (file.exists()) {
            file.delete()
        }
    }

    private fun mergeKnownInitiatives(
        existing: List<KnownInitiativeMetadata>,
        newItem: KnownInitiativeMetadata,
    ): List<KnownInitiativeMetadata> {
        val merged = linkedMapOf<String, KnownInitiativeMetadata>()
        existing.forEach { merged[it.id] = it }
        merged[newItem.id] = newItem
        return merged.values.toList()
    }
}
