package com.stratecode.lab.jetbrains.project

import com.intellij.openapi.project.Project
import java.io.File
import java.nio.charset.StandardCharsets
import java.util.UUID

object ProjectContextResolver {
    fun resolve(project: Project): ProjectContext {
        val basePath = project.basePath.orEmpty()
        val metadata = StrateCodeProjectStore.read(basePath)
        val branch = readGitValue(basePath, "rev-parse", "--abbrev-ref", "HEAD")
        val remote = readGitValue(basePath, "config", "--get", "remote.origin.url")
        val normalized = normalizeRepositoryUrl(remote) ?: metadata?.repositoryUrl
        return ProjectContext(
            projectName = project.name,
            workspaceRoot = basePath,
            branch = branch ?: metadata?.branch,
            repositoryUrl = normalized,
            stableBridgeId = stableBridgeId(basePath),
            degraded = normalized == null,
            metadata = metadata,
        )
    }

    fun normalizeRepositoryUrl(raw: String?): String? {
        val trimmed = raw?.trim().orEmpty()
        if (trimmed.isBlank()) {
            return null
        }
        return when {
            trimmed.startsWith("git@") -> {
                val withoutPrefix = trimmed.removePrefix("git@")
                val hostAndPath = withoutPrefix.split(":", limit = 2)
                if (hostAndPath.size != 2) return trimmed
                "https://${hostAndPath[0]}/${hostAndPath[1].removeSuffix(".git")}"
            }
            trimmed.startsWith("ssh://git@") -> {
                "https://" + trimmed.removePrefix("ssh://git@").replace(":", "/").removeSuffix(".git")
            }
            trimmed.startsWith("http://") || trimmed.startsWith("https://") -> {
                trimmed.removeSuffix(".git")
            }
            else -> trimmed
        }
    }

    fun stableBridgeId(workspaceRoot: String): String =
        UUID.nameUUIDFromBytes("stratecode:$workspaceRoot".toByteArray(StandardCharsets.UTF_8)).toString()

    private fun readGitValue(basePath: String, vararg args: String): String? {
        if (basePath.isBlank() || !File(basePath).exists()) {
            return null
        }
        return runCatching {
            val process = ProcessBuilder(listOf("git", *args))
                .directory(File(basePath))
                .redirectErrorStream(true)
                .start()
            val output = process.inputStream.bufferedReader().readText().trim()
            val code = process.waitFor()
            if (code == 0 && output.isNotBlank()) output else null
        }.getOrNull()
    }
}
