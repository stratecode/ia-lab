package com.stratecode.lab.jetbrains.ui

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.project.Project
import com.intellij.ui.JBSplitter
import com.intellij.ui.components.JBList
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTabbedPane
import com.intellij.ui.components.JBTextArea
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.JBUI
import com.stratecode.lab.jetbrains.bridge.BridgeResolver
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.client.OrchestratorClient
import com.stratecode.lab.jetbrains.project.ProjectContext
import com.stratecode.lab.jetbrains.project.ProjectContextResolver
import com.stratecode.lab.jetbrains.project.StrateCodeProjectStore
import com.stratecode.lab.jetbrains.settings.PluginSettingsService
import java.awt.BorderLayout
import java.awt.Color
import java.awt.Dimension
import java.awt.FlowLayout
import java.awt.Font
import java.awt.GridLayout
import javax.swing.BorderFactory
import javax.swing.Box
import javax.swing.BoxLayout
import javax.swing.DefaultListCellRenderer
import javax.swing.DefaultListModel
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JList
import javax.swing.JPanel
import javax.swing.ListSelectionModel
import javax.swing.SwingConstants
import javax.swing.SwingUtilities

class AgentsToolWindowPanel(
    private val project: Project,
) : JPanel(BorderLayout()) {
    private val headerTitle = JLabel("StrateCode Agents")
    private val headerSubtitle = JLabel("Governed initiatives, scoped to the current repository.")

    private val backendBadge = badge("Backend", "unknown", Color(0x5B6B7A))
    private val bridgeBadge = badge("Bridge", "unresolved", Color(0x7A5B5B))
    private val projectBadge = badge("Project", "unresolved", Color(0x5B6B7A))

    private val projectSummaryLabel = htmlLabel("Project context not loaded yet.")
    private val metadataSummaryLabel = htmlLabel("`.stratecode/project.json` not written yet.")
    private val scopeSummaryLabel = htmlLabel("Initiatives will be scoped to the current workspace.")

    private val capabilitiesArea = infoArea(12)
    private val initiativeSummaryArea = infoArea(15)
    private val initiativeReviewsArea = infoArea(8)
    private val tasksArea = infoArea(14)
    private val feedbackArea = JBTextArea(4, 60).apply {
        lineWrap = true
        wrapStyleWord = true
        border = JBUI.Borders.empty(8)
    }

    private val initiativesModel = DefaultListModel<InitiativeSummary>()
    private val initiativesList = JBList(initiativesModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = InitiativeCellRenderer()
    }

    private val initiativeTitleField = JBTextField()
    private val initiativeGoalArea = JBTextArea(6, 60).apply {
        lineWrap = true
        wrapStyleWord = true
        border = JBUI.Borders.empty(8)
    }

    private val advanceButton = JButton("Advance Draft")
    private val approveButton = JButton("Approve Phase")
    private val rejectButton = JButton("Reject Phase")
    private val generateTasksButton = JButton("Generate Task Backlog")
    private val refreshDetailButton = JButton("Refresh Detail")

    private var selectedInitiative: InitiativeSummary? = null
    private var selectedDetail: InitiativeDetailResponseRecord? = null

    init {
        border = JBUI.Borders.empty(12)
        add(buildHeader(), BorderLayout.NORTH)
        add(buildTabs(), BorderLayout.CENTER)
        bindActions()
        updateActionState()
        loadStatus()
        loadInitiatives()
    }

    private fun buildHeader(): JComponent {
        headerTitle.font = headerTitle.font.deriveFont(Font.BOLD, 20f)
        headerSubtitle.foreground = Color(0x6B7280)

        val titleBlock = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            isOpaque = false
            add(headerTitle)
            add(Box.createVerticalStrut(4))
            add(headerSubtitle)
        }

        val badges = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(projectBadge)
            add(backendBadge)
            add(bridgeBadge)
        }

        return JPanel(BorderLayout()).apply {
            border = BorderFactory.createCompoundBorder(
                BorderFactory.createLineBorder(Color(0xD8DEE9)),
                JBUI.Borders.empty(14),
            )
            add(titleBlock, BorderLayout.NORTH)
            add(Box.createVerticalStrut(10), BorderLayout.CENTER)
            add(badges, BorderLayout.SOUTH)
        }
    }

    private fun buildTabs(): JComponent =
        JBTabbedPane().apply {
            addTab("Overview", buildOverviewTab())
            addTab("Initiatives", buildInitiativesTab())
        }

    private fun buildOverviewTab(): JComponent {
        val statusGrid = JPanel(GridLayout(3, 1, 0, 10)).apply {
            add(section("Project Context", projectSummaryLabel))
            add(section("Local Metadata", metadataSummaryLabel))
            add(section("Scope", scopeSummaryLabel))
        }

        val actions = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(JButton("Refresh").apply { addActionListener { loadStatus() } })
            add(JButton("Register Bridge").apply { addActionListener { registerBridge() } })
        }

        val left = JPanel(BorderLayout()).apply {
            add(statusGrid, BorderLayout.CENTER)
            add(actions, BorderLayout.SOUTH)
        }

        val right = section("Effective Capabilities", JBScrollPane(capabilitiesArea))
        right.preferredSize = Dimension(480, 420)

        return JBSplitter(false, 0.42f).apply {
            firstComponent = left
            secondComponent = right
            border = JBUI.Borders.emptyTop(12)
        }
    }

    private fun buildInitiativesTab(): JComponent {
        val form = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(JLabel("Title"))
            add(Box.createVerticalStrut(4))
            add(initiativeTitleField)
            add(Box.createVerticalStrut(8))
            add(JLabel("Goal"))
            add(Box.createVerticalStrut(4))
            add(JBScrollPane(initiativeGoalArea))
        }

        val createControls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(JButton("Create Initiative").apply { addActionListener { createInitiative() } })
            add(JButton("Refresh Project Initiatives").apply { addActionListener { loadInitiatives() } })
        }

        val leftTop = JPanel(BorderLayout()).apply {
            add(section("New Initiative", form), BorderLayout.CENTER)
            add(createControls, BorderLayout.SOUTH)
        }

        initiativesList.addListSelectionListener {
            val selected = initiativesList.selectedValue ?: return@addListSelectionListener
            selectedInitiative = selected
            loadInitiativeDetail(selected.id)
        }

        val detailTabs = JBTabbedPane().apply {
            addTab("Summary", section("Initiative Summary", JBScrollPane(initiativeSummaryArea)))
            addTab("Reviews", section("Phase Reviews", JBScrollPane(initiativeReviewsArea)))
            addTab("Tasks", section("Task Backlog", JBScrollPane(tasksArea)))
        }

        val feedbackSection = section("Action Feedback", JBScrollPane(feedbackArea))
        val actionControls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(advanceButton)
            add(approveButton)
            add(rejectButton)
            add(generateTasksButton)
            add(refreshDetailButton)
        }

        val rightTop = JPanel(BorderLayout()).apply {
            add(detailTabs, BorderLayout.CENTER)
            add(actionControls, BorderLayout.SOUTH)
        }

        val right = JBSplitter(true, 0.78f).apply {
            firstComponent = rightTop
            secondComponent = feedbackSection
        }

        val bottom = JBSplitter(false, 0.32f).apply {
            firstComponent = section("Project Initiatives", JBScrollPane(initiativesList))
            secondComponent = right
        }

        return JBSplitter(true, 0.30f).apply {
            border = JBUI.Borders.emptyTop(12)
            firstComponent = leftTop
            secondComponent = bottom
        }
    }

    private fun bindActions() {
        advanceButton.addActionListener { advanceSelectedInitiative() }
        approveButton.addActionListener { resolveSelectedInitiative(true) }
        rejectButton.addActionListener { resolveSelectedInitiative(false) }
        generateTasksButton.addActionListener { generateSelectedInitiativeTasks() }
        refreshDetailButton.addActionListener {
            selectedInitiative?.let { loadInitiativeDetail(it.id) }
        }
    }

    private fun updateActionState() {
        val detail = selectedDetail
        if (detail == null) {
            advanceButton.isEnabled = false
            approveButton.isEnabled = false
            rejectButton.isEnabled = false
            generateTasksButton.isEnabled = false
            refreshDetailButton.isEnabled = false
            return
        }
        refreshDetailButton.isEnabled = true
        val phase = detail.initiative.currentPhase
        val status = detail.initiative.status
        advanceButton.isEnabled = phase in setOf("requirements", "design") && status.endsWith("_draft")
        approveButton.isEnabled = phase in setOf("requirements", "design", "plan") && status.endsWith("_review")
        rejectButton.isEnabled = phase in setOf("requirements", "design", "plan") && status.endsWith("_review")
        generateTasksButton.isEnabled = phase == "plan" && status == "plan_draft"
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        updateProjectSummary(context)

        if (apiKey.isBlank()) {
            backendBadge.text = "Backend  missing key"
            setBadgeColor(backendBadge, Color(0x8B5E3C))
            capabilitiesArea.text = "Configure the API key in Settings > StrateCode Agents."
            return
        }

        val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val ready = client.checkReady()
                val capabilities = client.listCapabilities(context.repositoryUrl, "needs_external_evidence", "researcher", listOf("evidence", "docs"))
                val bridges = client.listBridges()
                val bridge = BridgeResolver.resolve(bridges.items, context.workspaceRoot, settings.currentState.bridgeName)
                val projectCaps = context.repositoryUrl?.let { client.getProjectCapabilities(it, "needs_repo_static_analysis", "reviewer") }
                StrateCodeProjectStore.write(context, bridgeName = settings.currentState.bridgeName)
                SwingUtilities.invokeLater {
                    backendBadge.text = "Backend  ${if (ready.ready) "ready" else "not ready"}"
                    setBadgeColor(backendBadge, if (ready.ready) Color(0x1F7A4C) else Color(0x8B5E3C))
                    bridgeBadge.text = "Bridge  ${bridge.status}"
                    setBadgeColor(bridgeBadge, when (bridge.status) {
                        "online", "idle", "busy", "ready" -> Color(0x1F7A4C)
                        "missing" -> Color(0x8B5E3C)
                        else -> Color(0x5B6B7A)
                    })
                    metadataSummaryLabel.text = metadataSummaryHtml(ProjectContextResolver.resolve(project))
                    capabilitiesArea.text = buildCapabilitiesText(projectCaps?.mode, capabilities.capabilities, projectCaps?.capabilities.orEmpty())
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    backendBadge.text = "Backend  error"
                    setBadgeColor(backendBadge, Color(0x8B2E2E))
                    bridgeBadge.text = "Bridge  unresolved"
                    setBadgeColor(bridgeBadge, Color(0x8B5E3C))
                    capabilitiesArea.text = error.message ?: error.toString()
                    notify("Status refresh failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun registerBridge() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        if (apiKey.isBlank()) {
            notify("Bridge registration blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).registerBridge(context, settings.currentState.bridgeName)
            }.onSuccess {
                StrateCodeProjectStore.write(context, bridgeName = settings.currentState.bridgeName)
                SwingUtilities.invokeLater {
                    notify("Bridge registered", "Bridge ${it.name} is now bound to ${it.workspaceRoot}.", NotificationType.INFORMATION)
                    loadStatus()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Bridge registration failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun createInitiative() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        val title = initiativeTitleField.text.trim().ifBlank { "IDE initiative for ${context.projectName}" }
        val goal = initiativeGoalArea.text.trim()
        if (apiKey.isBlank()) {
            notify("Initiative blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        if (goal.isBlank()) {
            notify("Initiative blocked", "Goal is required.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).createInitiative(title, goal, context.workspaceRoot)
            }.onSuccess {
                StrateCodeProjectStore.write(
                    context,
                    bridgeName = settings.currentState.bridgeName,
                    lastInitiativeId = it.id,
                    lastInitiativeTitle = it.title,
                )
                SwingUtilities.invokeLater {
                    notify("Initiative created", "${it.title} (${it.id})", NotificationType.INFORMATION)
                    initiativeGoalArea.text = ""
                    initiativeTitleField.text = it.title
                    loadStatus()
                    loadInitiatives(selectInitiativeId = it.id)
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Initiative creation failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun loadInitiatives(selectInitiativeId: String? = null) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        if (apiKey.isBlank()) {
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).listInitiatives(context.workspaceRoot)
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    initiativesModel.clear()
                    response.items.forEach(initiativesModel::addElement)
                    val targetId = selectInitiativeId ?: context.metadata?.lastInitiativeId
                    if (targetId != null) {
                        val target = response.items.firstOrNull { it.id == targetId }
                        if (target != null) {
                            initiativesList.setSelectedValue(target, true)
                        }
                    }
                    if (response.items.isEmpty()) {
                        initiativeSummaryArea.text = "No initiatives found for:\n${context.workspaceRoot}\n\nThis panel is scoped to the current project only."
                        initiativeReviewsArea.text = ""
                        tasksArea.text = ""
                        selectedDetail = null
                        updateActionState()
                    }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    initiativeSummaryArea.text = error.message ?: error.toString()
                    selectedDetail = null
                    updateActionState()
                }
            }
        }
    }

    private fun loadInitiativeDetail(initiativeId: String) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
                val detail = client.getInitiativeDetail(initiativeId)
                val tasks = client.listInitiativeTasks(initiativeId)
                detail to tasks.items
            }.onSuccess { (detail, tasks) ->
                val context = ProjectContextResolver.resolve(project)
                StrateCodeProjectStore.write(
                    context,
                    bridgeName = settings.currentState.bridgeName,
                    lastInitiativeId = detail.initiative.id,
                    lastInitiativeTitle = detail.initiative.title,
                )
                SwingUtilities.invokeLater {
                    selectedDetail = detail
                    initiativeSummaryArea.text = renderInitiativeSummary(detail)
                    initiativeReviewsArea.text = renderReviews(detail)
                    tasksArea.text = renderTasks(tasks)
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    initiativeSummaryArea.text = error.message ?: error.toString()
                    initiativeReviewsArea.text = ""
                    tasksArea.text = ""
                    selectedDetail = null
                    updateActionState()
                }
            }
        }
    }

    private fun advanceSelectedInitiative() {
        val detail = selectedDetail ?: return
        runInitiativeMutation("Draft advanced") { client ->
            client.advanceInitiative(detail.initiative.id, feedbackArea.text.trim())
        }
    }

    private fun resolveSelectedInitiative(approve: Boolean) {
        val detail = selectedDetail ?: return
        val phase = detail.initiative.currentPhase
        val action = if (approve) "Phase approved" else "Phase rejected"
        runInitiativeMutation(action) { client ->
            if (approve) {
                client.approveInitiativePhase(detail.initiative.id, phase, "jetbrains-plugin", feedbackArea.text.trim())
            } else {
                client.rejectInitiativePhase(detail.initiative.id, phase, "jetbrains-plugin", feedbackArea.text.trim())
            }
        }
    }

    private fun generateSelectedInitiativeTasks() {
        val detail = selectedDetail ?: return
        runInitiativeMutation("Task backlog generated") { client ->
            client.generateInitiativeTasks(detail.initiative.id, feedbackArea.text.trim()).initiative
        }
    }

    private fun runInitiativeMutation(successTitle: String, action: (OrchestratorClient) -> Any) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val selected = selectedInitiative ?: return
        if (apiKey.isBlank()) {
            notify("Action blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                action(OrchestratorClient(settings.currentState.baseUrl, apiKey))
            }.onSuccess {
                SwingUtilities.invokeLater {
                    notify(successTitle, selected.title, NotificationType.INFORMATION)
                    feedbackArea.text = ""
                    loadInitiatives(selectInitiativeId = selected.id)
                    loadInitiativeDetail(selected.id)
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Initiative action failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun updateProjectSummary(context: ProjectContext) {
        projectBadge.text = "Project  ${context.projectName}"
        setBadgeColor(projectBadge, if (context.degraded) Color(0x8B5E3C) else Color(0x365A7C))
        projectSummaryLabel.text = """
            <html>
            <b>${escape(context.projectName)}</b><br/>
            Workspace: <code>${escape(context.workspaceRoot)}</code><br/>
            Branch: <code>${escape(context.branch ?: "-")}</code><br/>
            Repository: <code>${escape(context.repositoryUrl ?: "degraded")}</code>
            </html>
        """.trimIndent()
        metadataSummaryLabel.text = metadataSummaryHtml(context)
        scopeSummaryLabel.text = """
            <html>
            This plugin scopes initiative visibility to the current workspace root.<br/>
            Only initiatives with <code>workspace_root=${escape(context.workspaceRoot)}</code> are listed.
            </html>
        """.trimIndent()
    }

    private fun metadataSummaryHtml(context: ProjectContext): String {
        val metadata = context.metadata ?: return "<html>No local metadata yet. The plugin will write <code>.stratecode/project.json</code>.</html>"
        return """
            <html>
            File: <code>.stratecode/project.json</code><br/>
            Bridge: <code>${escape(metadata.bridgeName ?: "-")}</code><br/>
            Last initiative: <code>${escape(metadata.lastInitiativeTitle ?: metadata.lastInitiativeId ?: "-")}</code><br/>
            Updated: <code>${escape(metadata.updatedAt)}</code>
            </html>
        """.trimIndent()
    }

    private fun buildCapabilitiesText(
        mode: String?,
        candidates: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
        projectCapabilities: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
    ): String {
        val builder = StringBuilder()
        builder.appendLine("Discovery candidates")
        builder.appendLine("===================")
        if (candidates.isEmpty()) {
            builder.appendLine("No capability candidates returned.")
        } else {
            candidates.forEach {
                builder.appendLine("• ${it.name}  [${it.kind}]  score=${"%.1f".format(it.score)}")
                builder.appendLine("  tags: ${it.capabilityTags.joinToString(", ")}")
            }
        }
        builder.appendLine()
        builder.appendLine("Project effective policy")
        builder.appendLine("========================")
        builder.appendLine("mode: ${mode ?: "n/a"}")
        if (projectCapabilities.isEmpty()) {
            builder.appendLine("No project-scoped reviewer capabilities returned.")
        } else {
            projectCapabilities.forEach {
                builder.appendLine("• ${it.name}  [${it.kind}]  score=${"%.1f".format(it.score)}")
                builder.appendLine("  tags: ${it.capabilityTags.joinToString(", ")}")
            }
        }
        return builder.toString()
    }

    private fun renderInitiativeSummary(detail: InitiativeDetailResponseRecord): String {
        val initiative = detail.initiative
        val builder = StringBuilder()
        builder.appendLine("Title: ${initiative.title}")
        builder.appendLine("Status: ${initiative.status}")
        builder.appendLine("Current phase: ${initiative.currentPhase}")
        builder.appendLine("Execution mode: ${initiative.executionMode}")
        builder.appendLine("Workspace: ${initiative.workspaceRoot}")
        builder.appendLine("Created by: ${initiative.createdBy}")
        builder.appendLine("Updated: ${initiative.updatedAt}")
        builder.appendLine()
        builder.appendLine("Goal")
        builder.appendLine("====")
        builder.appendLine(initiative.goal)
        builder.appendLine()
        builder.appendLine("Execution summary")
        builder.appendLine("=================")
        builder.appendLine("Backlog materialized: ${detail.executionSummary.backlogMaterialized}")
        builder.appendLine("Aggregated status: ${detail.executionSummary.aggregatedStatus}")
        builder.appendLine("Task count: ${detail.executionSummary.taskCount}")
        builder.appendLine("Pending manual: ${detail.executionSummary.pendingManual}")
        builder.appendLine()
        builder.appendLine("Execution policy")
        builder.appendLine("================")
        builder.appendLine("Scope: ${detail.executionPolicy.scope}")
        builder.appendLine("Allowed modes: ${detail.executionPolicy.allowedModes.joinToString(", ")}")
        builder.appendLine("Approval modes: ${detail.executionPolicy.approvalRequiredModes.joinToString(", ")}")
        builder.appendLine()
        builder.appendLine("Phase history")
        builder.appendLine("=============")
        detail.histories.forEach { history ->
            builder.appendLine("• ${history.phase} v${history.activeVersion} (${history.items.size} entries)")
        }
        return builder.toString()
    }

    private fun renderReviews(detail: InitiativeDetailResponseRecord): String {
        if (detail.reviews.isEmpty()) {
            return "No reviews recorded yet."
        }
        return buildString {
            detail.reviews.forEach { review ->
                appendLine("${review.phase} / ${review.decision} / ${review.createdAt}")
                appendLine("by: ${review.generatedBy ?: "-"}")
                if (!review.feedback.isNullOrBlank()) {
                    appendLine("feedback: ${review.feedback}")
                }
                appendLine()
            }
        }.trim()
    }

    private fun renderTasks(tasks: List<InitiativeTaskLinkRecord>): String {
        if (tasks.isEmpty()) {
            return "No tasks materialized yet."
        }
        return buildString {
            tasks.sortedBy { it.launchOrder }.forEach { link ->
                appendLine("#${link.launchOrder} ${link.task.description}")
                appendLine("agent=${link.task.assignedAgent ?: link.task.plannedAgent ?: "-"} mode=${link.executionMode} target=${link.task.executionTarget} state=${link.task.state}")
                if (!link.epic.isNullOrBlank()) {
                    appendLine("epic=${link.epic}")
                }
                if (!link.launchGroup.isNullOrBlank()) {
                    appendLine("group=${link.launchGroup}")
                }
                appendLine()
            }
        }.trim()
    }

    private fun badge(title: String, value: String, color: Color): JLabel =
        JLabel("$title  $value", SwingConstants.CENTER).apply {
            isOpaque = true
            background = color
            foreground = Color.WHITE
            border = JBUI.Borders.empty(6, 10)
            font = font.deriveFont(Font.BOLD, 12f)
        }

    private fun setBadgeColor(label: JLabel, color: Color) {
        label.background = color
    }

    private fun section(title: String, component: JComponent): JPanel =
        JPanel(BorderLayout()).apply {
            border = BorderFactory.createCompoundBorder(
                BorderFactory.createTitledBorder(title),
                JBUI.Borders.empty(8),
            )
            add(component, BorderLayout.CENTER)
        }

    private fun infoArea(rows: Int): JBTextArea =
        JBTextArea(rows, 60).apply {
            isEditable = false
            lineWrap = true
            wrapStyleWord = true
            border = JBUI.Borders.empty(8)
        }

    private fun htmlLabel(text: String): JLabel =
        JLabel("<html>$text</html>").apply {
            verticalAlignment = SwingConstants.TOP
        }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

    private fun notify(title: String, message: String, type: NotificationType) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("StrateCode Agents")
            .createNotification(title, message, type)
            .notify(project)
    }

    private fun settings(): PluginSettingsService =
        ApplicationManager.getApplication().getService(PluginSettingsService::class.java)
}

private class InitiativeCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is InitiativeSummary && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            it.text = """
                <html>
                <b>${escape(value.title)}</b><br/>
                <span style='color:#6B7280'>${escape(value.status)} / ${escape(value.currentPhase)}<br/>${escape(value.workspaceRoot)}</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}
