package com.stratecode.lab.jetbrains.task

import com.intellij.diff.DiffManager
import com.intellij.diff.DiffContentFactory
import com.intellij.openapi.application.ApplicationManager
import com.intellij.diff.requests.SimpleDiffRequest
import com.intellij.openapi.fileEditor.FileEditorManager
import com.intellij.openapi.fileEditor.OpenFileDescriptor
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.LocalFileSystem
import com.intellij.openapi.vfs.VfsUtil
import com.intellij.testFramework.LightVirtualFile
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.TaskDetailRecord
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import java.io.File

data class TaskResultPatchView(
    val diff: String,
    val changedFiles: List<String>,
    val summary: String? = null,
    val sourceType: String = "task.results.diff",
)

data class EvidenceLocation(
    val file: String,
    val line: Int,
    val column: Int? = null,
    val message: String? = null,
    val severity: String? = null,
    val sourceType: String,
) {
    override fun toString(): String = buildString {
        append(file)
        append(':')
        append(line)
        column?.let {
            append(':')
            append(it)
        }
        if (!severity.isNullOrBlank()) {
            append(" [")
            append(severity)
            append(']')
        }
        if (!message.isNullOrBlank()) {
            append("  ")
            append(message)
        }
    }
}

data class EvidenceExtractionResult(
    val locations: List<EvidenceLocation>,
    val rawArtifacts: List<InitiativeArtifactRecord>,
    val errors: List<String>,
)

data class PatchApplyResult(
    val changedFiles: List<String>,
    val stdout: String,
    val stderr: String,
)

object TaskExecutionSupport {
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    fun resolvePatch(task: TaskDetailRecord, artifacts: List<InitiativeArtifactRecord>): TaskResultPatchView? {
        val results = task.results.asObject()
        val resultDiff = results?.get("diff").asString()?.trim().orEmpty()
        val resultChangedFiles = results?.get("changed_files").asStringList()
        val summary = results?.get("summary").asString()
        if (resultDiff.isNotBlank()) {
            return TaskResultPatchView(
                diff = resultDiff,
                changedFiles = if (resultChangedFiles.isNotEmpty()) resultChangedFiles else parseChangedFilesFromDiff(resultDiff),
                summary = summary,
                sourceType = "task.results.diff",
            )
        }
        val fallback = artifacts.firstOrNull(::looksLikePatchArtifact) ?: return null
        val diff = fallback.contentText?.trim().orEmpty()
        if (diff.isBlank()) {
            return null
        }
        return TaskResultPatchView(
            diff = diff,
            changedFiles = parseChangedFilesFromDiff(diff),
            summary = fallback.title ?: summary,
            sourceType = "artifact:${fallback.artifactType}",
        )
    }

    fun extractEvidence(task: TaskDetailRecord, artifacts: List<InitiativeArtifactRecord>): EvidenceExtractionResult {
        val locations = mutableListOf<EvidenceLocation>()
        val errors = mutableListOf<String>()
        val raw = mutableListOf<InitiativeArtifactRecord>()

        val combined = buildList {
            addAll(artifacts)
            addAll(taskResultsArtifacts(task))
        }

        combined.forEach { artifact ->
            val content = artifact.contentText?.trim().orEmpty()
            if (content.isBlank()) {
                return@forEach
            }
            if (artifact.artifactType == "code_analysis_report") {
                runCatching {
                    val payload = json.parseToJsonElement(content).asObject() ?: error("invalid JSON object")
                    payload["findings"].asArray().forEach { finding ->
                        val obj = finding.asObject() ?: return@forEach
                        val location = obj["location"].asObject() ?: return@forEach
                        val file = location["file"].asString()?.trim().orEmpty()
                        val line = location["line"].asInt()
                        if (file.isBlank() || line == null) {
                            return@forEach
                        }
                        locations += EvidenceLocation(
                            file = file,
                            line = line,
                            column = location?.get("column")?.asInt(),
                            message = obj["message"].asString(),
                            severity = obj["severity"].asString(),
                            sourceType = "code_analysis_report",
                        )
                    }
                }.onFailure {
                    errors += "Failed to parse code_analysis_report ${artifact.id}: ${it.message}"
                    raw += artifact
                }
                return@forEach
            }

            val parsed = runCatching { json.parseToJsonElement(content) }.getOrNull()
            if (parsed == null) {
                raw += artifact
                return@forEach
            }

            val found = mutableListOf<EvidenceLocation>()
            collectLocations(parsed, artifact.artifactType, found)
            if (found.isEmpty()) {
                raw += artifact
            } else {
                locations += found
            }
        }

        return EvidenceExtractionResult(
            locations = locations.distinctBy { "${it.sourceType}|${it.file}|${it.line}|${it.column}|${it.message}" },
            rawArtifacts = raw.distinctBy { it.id },
            errors = errors,
        )
    }

    fun previewPatch(project: Project, workspaceRoot: String, patchView: TaskResultPatchView) {
        val changed = patchView.changedFiles
        if (changed.size == 1) {
            val file = File(workspaceRoot, changed.first())
            if (file.exists()) {
                val current = file.readText()
                val patched = applyUnifiedDiffToSingleFile(current, patchView.diff)
                if (patched != null) {
                    val factory = DiffContentFactory.getInstance()
                    val request = SimpleDiffRequest(
                        patchView.summary ?: "Patch Preview",
                        factory.create(project, current),
                        factory.create(project, patched),
                        "Current",
                        "Patched",
                    )
                    DiffManager.getInstance().showDiff(project, request)
                    return
                }
            }
        }
        openTextPreview(project, patchView.summary ?: "Patch Preview", patchView.diff, "diff")
    }

    fun applyPatch(workspaceRoot: String, patchView: TaskResultPatchView): PatchApplyResult {
        require(patchView.diff.isNotBlank()) { "patch is empty" }
        val root = File(workspaceRoot)
        require(root.isDirectory) { "workspace root is invalid" }
        val check = runGitApply(root, listOf("apply", "--check", "--whitespace=nowarn", "-"), patchView.diff)
        if (check.exitCode != 0) {
            error(check.errorText())
        }
        val apply = runGitApply(root, listOf("apply", "--whitespace=nowarn", "-"), patchView.diff)
        if (apply.exitCode != 0) {
            error(apply.errorText())
        }
        refreshWorkspace(workspaceRoot)
        return PatchApplyResult(
            changedFiles = patchView.changedFiles,
            stdout = apply.stdout,
            stderr = apply.stderr,
        )
    }

    fun openChangedFile(project: Project, workspaceRoot: String, changedFiles: List<String>): Boolean {
        val first = changedFiles.firstOrNull()?.trim().orEmpty()
        if (first.isBlank()) {
            return false
        }
        return openAtLocation(project, workspaceRoot, EvidenceLocation(file = first, line = 1, sourceType = "changed_file"))
    }

    fun openAtLocation(project: Project, workspaceRoot: String, location: EvidenceLocation): Boolean {
        val file = resolveWorkspaceFile(workspaceRoot, location.file) ?: return false
        if (!file.exists()) {
            return false
        }
        val virtualFile = LocalFileSystem.getInstance().refreshAndFindFileByIoFile(file) ?: return false
        OpenFileDescriptor(project, virtualFile, (location.line - 1).coerceAtLeast(0), (location.column?.minus(1) ?: 0).coerceAtLeast(0)).navigate(true)
        return true
    }

    fun resolveWorkspaceFile(workspaceRoot: String, relativePath: String): File? {
        val root = File(workspaceRoot).canonicalFile
        val file = File(root, relativePath).canonicalFile
        if (!file.path.startsWith(root.path)) {
            return null
        }
        return file
    }

    fun openTextPreview(project: Project, title: String, content: String, extension: String = "txt") {
        val virtualFile = LightVirtualFile("$title.$extension", content)
        FileEditorManager.getInstance(project).openFile(virtualFile, true)
    }

    fun refreshWorkspace(workspaceRoot: String) {
        if (ApplicationManager.getApplication() == null) {
            return
        }
        val ioFile = File(workspaceRoot)
        val virtualFile = LocalFileSystem.getInstance().refreshAndFindFileByIoFile(ioFile) ?: return
        VfsUtil.markDirtyAndRefresh(true, true, true, virtualFile)
    }

    private fun taskResultsArtifacts(task: TaskDetailRecord): List<InitiativeArtifactRecord> {
        val artifacts = task.results.asObject()?.get("artifacts").asArray()
        return artifacts.mapIndexedNotNull { index, item ->
            val obj = item.asObject() ?: return@mapIndexedNotNull null
            val type = obj["type"].asString()?.trim().orEmpty()
            val content = obj["content_text"].asString()
            if (type.isBlank() || content.isNullOrBlank()) {
                return@mapIndexedNotNull null
            }
            InitiativeArtifactRecord(
                id = "${task.id}:results:$index",
                artifactType = type,
                title = obj["title"].asString(),
                uri = obj["path"].asString(),
                mediaType = obj["media_type"].asString(),
                contentText = content,
                metadata = emptyMap(),
                createdAt = task.updatedAt,
            )
        }
    }

    private fun looksLikePatchArtifact(artifact: InitiativeArtifactRecord): Boolean {
        val type = artifact.artifactType.lowercase()
        val media = artifact.mediaType?.lowercase().orEmpty()
        val content = artifact.contentText?.trim().orEmpty()
        return "patch" in type ||
            "diff" in type ||
            media.contains("x-diff") ||
            media.contains("x-patch") ||
            content.startsWith("diff --git ") ||
            (content.startsWith("--- ") && content.contains("\n+++ "))
    }

    private fun parseChangedFilesFromDiff(diff: String): List<String> {
        val out = linkedSetOf<String>()
        diff.lineSequence().forEach { line ->
            when {
                line.startsWith("diff --git ") -> {
                    val parts = line.split(" ")
                    if (parts.size >= 4) {
                        normalizePatchedPath(parts[3])?.let(out::add)
                    }
                }
                line.startsWith("+++ ") -> {
                    normalizePatchedPath(line.removePrefix("+++ ").trim())?.let(out::add)
                }
            }
        }
        return out.toList()
    }

    private fun normalizePatchedPath(raw: String): String? {
        val cleaned = raw.removePrefix("b/").removePrefix("a/").trim()
        if (cleaned.isBlank() || cleaned == "/dev/null") {
            return null
        }
        return cleaned
    }

    private fun applyUnifiedDiffToSingleFile(current: String, diff: String): String? {
        val lines = current.split("\n").toMutableList()
        val output = mutableListOf<String>()
        var currentIndex = 0
        val allLines = diff.lines()
        var i = 0
        while (i < allLines.size) {
            val line = allLines[i]
            if (!line.startsWith("@@")) {
                i++
                continue
            }
            val header = parseHunkHeader(line) ?: return null
            val targetIndex = (header.oldStart - 1).coerceAtLeast(0)
            while (currentIndex < targetIndex && currentIndex < lines.size) {
                output += lines[currentIndex++]
            }
            i++
            while (i < allLines.size && !allLines[i].startsWith("@@")) {
                val patchLine = allLines[i]
                if (patchLine == "\\ No newline at end of file") {
                    i++
                    continue
                }
                if (patchLine.isEmpty()) {
                    if (currentIndex >= lines.size || lines[currentIndex] != "") {
                        return null
                    }
                    output += lines[currentIndex++]
                    i++
                    continue
                }
                when (patchLine.first()) {
                    ' ' -> {
                        val expected = patchLine.drop(1)
                        if (currentIndex >= lines.size || lines[currentIndex] != expected) {
                            return null
                        }
                        output += lines[currentIndex++]
                    }
                    '-' -> {
                        val expected = patchLine.drop(1)
                        if (currentIndex >= lines.size || lines[currentIndex] != expected) {
                            return null
                        }
                        currentIndex++
                    }
                    '+' -> output += patchLine.drop(1)
                    else -> return null
                }
                i++
            }
        }
        while (currentIndex < lines.size) {
            output += lines[currentIndex++]
        }
        return output.joinToString("\n")
    }

    private fun parseHunkHeader(line: String): HunkHeader? {
        val parts = line.substringAfter("@@ ").substringBefore(" @@").trim().split(" ")
        if (parts.size != 2) {
            return null
        }
        val oldStart = parts[0].substringAfter("-").substringBefore(",").toIntOrNull() ?: return null
        val newStart = parts[1].substringAfter("+").substringBefore(",").toIntOrNull() ?: return null
        return HunkHeader(oldStart, newStart)
    }

    private fun collectLocations(element: JsonElement, sourceType: String, out: MutableList<EvidenceLocation>) {
        when (element) {
            is JsonArray -> element.forEach { collectLocations(it, sourceType, out) }
            is JsonObject -> {
                val location = element["location"].asObject()
                val file = location?.get("file").asString()?.trim().orEmpty()
                val line = location?.get("line").asInt()
                if (file.isNotBlank() && line != null) {
                    out += EvidenceLocation(
                        file = file,
                        line = line,
                        column = location?.get("column")?.asInt(),
                        message = element["message"].asString(),
                        severity = element["severity"].asString(),
                        sourceType = sourceType,
                    )
                }
                element.values.forEach { collectLocations(it, sourceType, out) }
            }
            else -> Unit
        }
    }

    private fun JsonElement?.asObject(): JsonObject? = this as? JsonObject
    private fun JsonElement?.asArray(): List<JsonElement> = (this as? JsonArray)?.toList().orEmpty()
    private fun JsonElement?.asString(): String? = (this as? JsonPrimitive)?.content
    private fun JsonElement?.asInt(): Int? = (this as? JsonPrimitive)?.content?.toIntOrNull()
    private fun JsonElement?.asStringList(): List<String> = asArray().mapNotNull { it.asString()?.trim() }.filter { it.isNotBlank() }

    private data class HunkHeader(val oldStart: Int, val newStart: Int)

    private data class CommandResult(val exitCode: Int, val stdout: String, val stderr: String) {
        fun errorText(): String = sequenceOf(stderr.trim(), stdout.trim()).firstOrNull { it.isNotBlank() } ?: "git apply failed"
    }

    private fun runGitApply(root: File, args: List<String>, patch: String): CommandResult {
        val process = ProcessBuilder(listOf("git", *args.toTypedArray()))
            .directory(root)
            .redirectErrorStream(false)
            .start()
        process.outputStream.bufferedWriter().use { it.write(patch) }
        val stdout = process.inputStream.bufferedReader().readText()
        val stderr = process.errorStream.bufferedReader().readText()
        val exit = process.waitFor()
        return CommandResult(exit, stdout, stderr)
    }
}
