package com.stratecode.lab.jetbrains.project

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import java.io.File
import kotlin.io.path.createTempDirectory

class ProjectContextResolverTest {
    @Test
    fun `normalizes ssh github remote`() {
        val normalized = ProjectContextResolver.normalizeRepositoryUrl("git@github.com:stratecode/ia-lab.git")
        assertEquals("https://github.com/stratecode/ia-lab", normalized)
    }

    @Test
    fun `normalizes https remote by stripping dot git`() {
        val normalized = ProjectContextResolver.normalizeRepositoryUrl("https://github.com/stratecode/ia-lab.git")
        assertEquals("https://github.com/stratecode/ia-lab", normalized)
    }

    @Test
    fun `returns null for blank remote`() {
        assertNull(ProjectContextResolver.normalizeRepositoryUrl("   "))
    }

    @Test
    fun `stable bridge id is deterministic`() {
        val first = ProjectContextResolver.stableBridgeId("/tmp/example")
        val second = ProjectContextResolver.stableBridgeId("/tmp/example")
        assertEquals(first, second)
    }

    @Test
    fun `stable bridge id uses normalized workspace root`() {
        val root = createTempDirectory().toFile()
        val canonical = root.canonicalPath
        val alternate = File(canonical, ".").absolutePath

        val first = ProjectContextResolver.stableBridgeId(canonical)
        val second = ProjectContextResolver.stableBridgeId(alternate)

        assertEquals(first, second)
    }
}
