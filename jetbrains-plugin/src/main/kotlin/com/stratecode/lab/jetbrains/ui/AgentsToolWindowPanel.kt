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
import com.stratecode.lab.jetbrains.client.InitiativeSummary
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
    private val initiativeDetailArea = infoArea(18)

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

    init {
        border = JBUI.Borders.empty(12)
        add(buildHeader(), BorderLayout.NORTH)
        add(buildTabs(), BorderLayout.CENTER)
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

    private fun buildTabs(): JComponent {
        return JBTabbedPane().apply {
            addTab("Overview", buildOverviewTab())
            addTab("Initiatives", buildInitiativesTab())
        }
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

        val controls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(JButton("Create Initiative").apply { addActionListener { createInitiative() } })
            add(JButton("Refresh Project Initiatives").apply { addActionListener { loadInitiatives() } })
        }

        val formSection = JPanel(BorderLayout()).apply {
            add(section("New Initiative", form), BorderLayout.CENTER)
            add(controls, BorderLayout.SOUTH)
        }

        initiativesList.addListSelectionListener {
            val selected = initiativesList.selectedValue ?: return@addListSelectionListener
            loadInitiativeDetail(selected.id)
        }

        val listSection = section("Project Initiatives", JBScrollPane(initiativesList))
        val detailSection = section("Initiative Detail", JBScrollPane(initiativeDetailArea))

        val bottom = JBSplitter(false, 0.34f).apply {
            firstComponent = listSection
            secondComponent = detailSection
        }

        return JBSplitter(true, 0.34f).apply {
            border = JBUI.Borders.emptyTop(12)
            firstComponent = formSection
            secondComponent = bottom
        }
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        updateProjectSummary(context)

        if (apiKey.isBlank()) {
            backendBadge.setText("Backend  missing key")
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
                    backendBadge.setText("Backend  ${if (ready.ready) "ready" else "not ready"}")
                    setBadgeColor(backendBadge, if (ready.ready) Color(0x1F7A4C) else Color(0x8B5E3C))
                    bridgeBadge.setText("Bridge  ${bridge.status}")
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
                    backendBadge.setText("Backend  error")
                    setBadgeColor(backendBadge, Color(0x8B2E2E))
                    bridgeBadge.setText("Bridge  unresolved")
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
                    loadInitiatives()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Initiative creation failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun loadInitiatives() {
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
                    if (response.items.isEmpty()) {
                        initiativeDetailArea.text = "No initiatives found for:\n${context.workspaceRoot}\n\nThis panel is scoped to the current project only."
                    }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    initiativeDetailArea.text = error.message ?: error.toString()
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
                OrchestratorClient(settings.currentState.baseUrl, apiKey).getInitiativeDetailRaw(initiativeId)
            }.onSuccess { detail ->
                SwingUtilities.invokeLater {
                    initiativeDetailArea.text = detail
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    initiativeDetailArea.text = error.message ?: error.toString()
                }
            }
        }
    }

    private fun updateProjectSummary(context: ProjectContext) {
        projectBadge.setText("Project  ${context.projectName}")
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
            This plugin now scopes initiative visibility to the current workspace root.<br/>
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
