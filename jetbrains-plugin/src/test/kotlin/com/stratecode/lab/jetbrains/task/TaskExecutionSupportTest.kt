package com.stratecode.lab.jetbrains.task

import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.TaskDetailRecord
import kotlinx.serialization.json.Json
import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

class TaskExecutionSupportTest {
    private val json = Json { ignoreUnknownKeys = true; explicitNulls = false }

    @Test
    fun `resolve patch prefers task results diff`() {
        val task = taskDetail(
            """
            {
              "summary": "Coder run",
              "diff": "diff --git a/foo.txt b/foo.txt\n--- a/foo.txt\n+++ b/foo.txt\n@@ -1 +1 @@\n-old\n+new\n",
              "changed_files": ["foo.txt"]
            }
            """.trimIndent(),
        )
        val fallback = artifact("patch_artifact", "diff --git a/bar.txt b/bar.txt")

        val resolved = TaskExecutionSupport.resolvePatch(task, listOf(fallback))

        assertNotNull(resolved)
        assertEquals("task.results.diff", resolved.sourceType)
        assertEquals(listOf("foo.txt"), resolved.changedFiles)
    }

    @Test
    fun `resolve patch falls back to patch artifact`() {
        val task = taskDetail("""{"summary":"No direct diff"}""")
        val fallback = artifact("local_bridge_diff", "diff --git a/bar.txt b/bar.txt\n--- a/bar.txt\n+++ b/bar.txt\n@@ -0,0 +1 @@\n+hello\n")

        val resolved = TaskExecutionSupport.resolvePatch(task, listOf(fallback))

        assertNotNull(resolved)
        assertEquals("artifact:local_bridge_diff", resolved.sourceType)
        assertEquals(listOf("bar.txt"), resolved.changedFiles)
    }

    @Test
    fun `resolve patch returns null when no patch exists`() {
        val task = taskDetail("""{"summary":"No patch"}""")

        val resolved = TaskExecutionSupport.resolvePatch(task, emptyList())

        assertNull(resolved)
    }

    @Test
    fun `extract evidence reads code analysis findings`() {
        val payload = """
          {
            "findings": [
              {
                "severity": "warning",
                "message": "composer.json present without composer.lock",
                "location": {"file": "composer.json", "line": 12}
              }
            ]
          }
        """.trimIndent()
        val task = taskDetail("""{"summary":"Reviewer"}""")

        val result = TaskExecutionSupport.extractEvidence(task, listOf(artifact("code_analysis_report", payload)))

        assertEquals(1, result.locations.size)
        assertEquals("composer.json", result.locations.first().file)
        assertEquals(12, result.locations.first().line)
    }

    @Test
    fun `extract evidence keeps raw artifact when no coordinates exist`() {
        val task = taskDetail("""{"summary":"Reviewer"}""")
        val artifact = artifact("review_report", """{"message":"looks fine"}""")

        val result = TaskExecutionSupport.extractEvidence(task, listOf(artifact))

        assertTrue(result.locations.isEmpty())
        assertEquals(1, result.rawArtifacts.size)
    }

    @Test
    fun `resolve workspace file rejects escapes outside root`() {
        val workspace = Files.createTempDirectory("stratecode-task-support").toFile()
        val inside = TaskExecutionSupport.resolveWorkspaceFile(workspace.absolutePath, "dir/file.txt")
        val outside = TaskExecutionSupport.resolveWorkspaceFile(workspace.absolutePath, "../escape.txt")

        assertNotNull(inside)
        assertNull(outside)
    }

    @Test
    fun `apply patch changes files in git workspace`() {
        val workspace = Files.createTempDirectory("stratecode-apply-patch").toFile()
        run(workspace, "git", "init")
        run(workspace, "git", "config", "user.email", "test@example.com")
        run(workspace, "git", "config", "user.name", "StrateCode Test")
        File(workspace, "foo.txt").writeText("old\n")
        run(workspace, "git", "add", "foo.txt")
        run(workspace, "git", "commit", "-m", "init")
        val patch = TaskResultPatchView(
            diff = """
                diff --git a/foo.txt b/foo.txt
                index 3367afd..3e75765 100644
                --- a/foo.txt
                +++ b/foo.txt
                @@ -1 +1 @@
                -old
                +new
            """.trimIndent() + "\n",
            changedFiles = listOf("foo.txt"),
        )

        val result = TaskExecutionSupport.applyPatch(workspace.absolutePath, patch)

        assertEquals(listOf("foo.txt"), result.changedFiles)
        assertEquals("new\n", File(workspace, "foo.txt").readText())
    }

    @Test
    fun `apply patch fails cleanly on invalid patch`() {
        val workspace = Files.createTempDirectory("stratecode-apply-patch-fail").toFile()
        run(workspace, "git", "init")
        run(workspace, "git", "config", "user.email", "test@example.com")
        run(workspace, "git", "config", "user.name", "StrateCode Test")
        File(workspace, "foo.txt").writeText("old\n")
        run(workspace, "git", "add", "foo.txt")
        run(workspace, "git", "commit", "-m", "init")
        val patch = TaskResultPatchView(
            diff = """
                diff --git a/foo.txt b/foo.txt
                --- a/foo.txt
                +++ b/foo.txt
                @@ -1 +1 @@
                -missing
                +new
            """.trimIndent() + "\n",
            changedFiles = listOf("foo.txt"),
        )

        val error = runCatching { TaskExecutionSupport.applyPatch(workspace.absolutePath, patch) }.exceptionOrNull()

        assertNotNull(error)
        assertEquals("old\n", File(workspace, "foo.txt").readText())
    }

    private fun taskDetail(resultsJson: String): TaskDetailRecord =
        TaskDetailRecord(
            id = "task-1",
            state = "completed",
            description = "Task",
            workspacePath = "/tmp/workspace",
            results = json.parseToJsonElement(resultsJson),
            errorMessage = null,
            updatedAt = "2026-06-01T00:00:00Z",
        )

    private fun artifact(type: String, content: String): InitiativeArtifactRecord =
        InitiativeArtifactRecord(
            id = "artifact-$type",
            artifactType = type,
            title = type,
            uri = null,
            mediaType = "application/json",
            contentText = content,
            metadata = emptyMap(),
            createdAt = "2026-06-01T00:00:00Z",
        )

    private fun run(workdir: File, vararg command: String) {
        val process = ProcessBuilder(command.toList())
            .directory(workdir)
            .redirectErrorStream(true)
            .start()
        val output = process.inputStream.bufferedReader().readText()
        val code = process.waitFor()
        assertEquals(0, code, "Command failed: ${command.joinToString(" ")}\n$output")
    }
}
