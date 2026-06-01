plugins {
    kotlin("jvm") version "2.0.21"
    kotlin("plugin.serialization") version "2.0.21"
    id("org.jetbrains.intellij") version "1.17.4"
}

group = "com.stratecode.lab"
version = "0.2.1"

repositories {
    mavenCentral()
}

dependencies {
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")
    testImplementation(kotlin("test"))
    testImplementation("org.junit.jupiter:junit-jupiter:5.11.4")
}

kotlin {
    jvmToolchain(17)
}

intellij {
    version.set("2023.3.8")
    type.set("IC")
    downloadSources.set(false)
    updateSinceUntilBuild.set(false)
}

tasks.patchPluginXml {
    sinceBuild.set("233")
    pluginDescription.set(
        """
        JetBrains operational console for governed StrateCode initiatives, bridge validation, approvals, and patch application.
        """.trimIndent()
    )
    changeNotes.set(
        """
        Improved startup status signaling with loading/repairing bridge badges, plus retryable auto-registration, HTTP tracing, and clearer diagnostics around goal creation and backend connectivity.
        """.trimIndent()
    )
}

tasks.test {
    useJUnitPlatform()
}
