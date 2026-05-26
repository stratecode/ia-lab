package com.stratecode.lab.jetbrains.project

data class ProjectContext(
    val projectName: String,
    val workspaceRoot: String,
    val branch: String?,
    val repositoryUrl: String?,
    val stableBridgeId: String,
    val degraded: Boolean,
)
