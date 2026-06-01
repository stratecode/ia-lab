package com.stratecode.lab.jetbrains.project

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull

class StrateCodeProjectStoreTest {
    @Test
    fun `writes and reads project metadata`() {
        val workspace = Files.createTempDirectory("stratecode-plugin-test").toFile()
        val context = ProjectContext(
            projectName = "ia-lab",
            workspaceRoot = workspace.absolutePath,
            branch = "main",
            repositoryUrl = "https://github.com/stratecode/ia-lab",
            stableBridgeId = "bridge-123",
            degraded = false,
        )

        StrateCodeProjectStore.write(
            context = context,
            bridgeName = "jetbrains-bridge",
            lastInitiativeId = "init-1",
            lastInitiativeTitle = "First initiative",
        )

        val metadata = StrateCodeProjectStore.read(workspace.absolutePath)
        assertNotNull(metadata)
        assertEquals("ia-lab", metadata.projectName)
        assertEquals("https://github.com/stratecode/ia-lab", metadata.repositoryUrl)
        assertEquals("bridge-123", metadata.stableBridgeId)
        assertEquals("jetbrains-bridge", metadata.bridgeName)
        assertEquals("init-1", metadata.lastInitiativeId)
        assertEquals("First initiative", metadata.lastInitiativeTitle)
        assertEquals(listOf("init-1"), metadata.knownInitiatives.map { it.id })
    }

    @Test
    fun `remember initiative appends local registry and clear removes metadata`() {
        val workspace = Files.createTempDirectory("stratecode-plugin-test-clear").toFile()
        val context = ProjectContext(
            projectName = "ia-lab",
            workspaceRoot = workspace.absolutePath,
            branch = "main",
            repositoryUrl = "https://github.com/stratecode/ia-lab",
            stableBridgeId = "bridge-123",
            degraded = false,
        )

        StrateCodeProjectStore.rememberInitiative(context, "init-1", "First")
        StrateCodeProjectStore.rememberInitiative(context, "init-2", "Second")

        val metadata = StrateCodeProjectStore.read(workspace.absolutePath)
        assertNotNull(metadata)
        assertEquals(listOf("init-1", "init-2"), metadata.knownInitiatives.map { it.id })

        StrateCodeProjectStore.clear(workspace.absolutePath)
        assertFalse(StrateCodeProjectStore.metadataFile(workspace.absolutePath).exists())
    }
}
