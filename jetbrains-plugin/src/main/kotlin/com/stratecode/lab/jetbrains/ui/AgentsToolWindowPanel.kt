package com.stratecode.lab.jetbrains.ui

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.project.Project
import com.intellij.ui.components.JBList
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTabbedPane
import com.intellij.ui.components.JBTextArea
import com.intellij.ui.components.JBTextField
import com.stratecode.lab.jetbrains.bridge.BridgeResolver
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.OrchestratorClient
import com.stratecode.lab.jetbrains.project.ProjectContextResolver
import com.stratecode.lab.jetbrains.settings.PluginSettingsService
import java.awt.BorderLayout
import java.awt.FlowLayout
import java.awt.GridLayout
import javax.swing.BoxLayout
import javax.swing.DefaultListModel
import javax.swing.JButton
import javax.swing.JLabel
import javax.swing.JPanel
import javax.swing.ListSelectionModel
import javax.swing.SwingUtilities

class AgentsToolWindowPanel(
    private val project: Project,
) : JPanel(BorderLayout()) {
    private val connectionLabel = JLabel("Backend: unknown")
    private val projectLabel = JLabel("Project: unresolved")
    private val bridgeLabel = JLabel("Bridge: unresolved")
    private val capabilitiesArea = JBTextArea(8, 60).apply {
        isEditable = false
        lineWrap = true
        wrapStyleWord = true
    }

    private val initiativesModel = DefaultListModel<InitiativeSummary>()
    private val initiativesList = JBList(initiativesModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
    }
    private val initiativeDetailArea = JBTextArea(16, 60).apply {
        isEditable = false
        lineWrap = true
        wrapStyleWord = true
    }
    private val initiativeTitleField = JBTextField()
    private val initiativeGoalArea = JBTextArea(5, 60).apply {
        lineWrap = true
        wrapStyleWord = true
    }

    init {
        val tabs = JBTabbedPane()
        tabs.addTab("Status", buildStatusTab())
        tabs.addTab("Initiatives", buildInitiativesTab())
        add(tabs, BorderLayout.CENTER)
        loadStatus()
        loadInitiatives()
    }

    private fun buildStatusTab(): JPanel {
        val panel = JPanel(BorderLayout())
        val top = JPanel(GridLayout(3, 1))
        top.add(connectionLabel)
        top.add(projectLabel)
        top.add(bridgeLabel)

        val buttons = JPanel(FlowLayout(FlowLayout.LEFT))
        val refresh = JButton("Refresh").apply { addActionListener { loadStatus() } }
        val registerBridge = JButton("Register Bridge").apply { addActionListener { registerBridge() } }
        buttons.add(refresh)
        buttons.add(registerBridge)

        panel.add(top, BorderLayout.NORTH)
        panel.add(JBScrollPane(capabilitiesArea), BorderLayout.CENTER)
        panel.add(buttons, BorderLayout.SOUTH)
        return panel
    }

    private fun buildInitiativesTab(): JPanel {
        val panel = JPanel(BorderLayout())
        val createPanel = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(JLabel("Title"))
            add(initiativeTitleField)
            add(JLabel("Goal"))
            add(JBScrollPane(initiativeGoalArea))
        }
        val buttonRow = JPanel(FlowLayout(FlowLayout.LEFT))
        val create = JButton("Create Initiative").apply { addActionListener { createInitiative() } }
        val refresh = JButton("Refresh Initiatives").apply { addActionListener { loadInitiatives() } }
        buttonRow.add(create)
        buttonRow.add(refresh)

        initiativesList.addListSelectionListener {
            val selected = initiativesList.selectedValue ?: return@addListSelectionListener
            loadInitiativeDetail(selected.id)
        }
        val split = JPanel(GridLayout(1, 2))
        split.add(JBScrollPane(initiativesList))
        split.add(JBScrollPane(initiativeDetailArea))

        val north = JPanel(BorderLayout())
        north.add(createPanel, BorderLayout.CENTER)
        north.add(buttonRow, BorderLayout.SOUTH)

        panel.add(north, BorderLayout.NORTH)
        panel.add(split, BorderLayout.CENTER)
        return panel
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            connectionLabel.text = "Backend: missing API key"
            capabilitiesArea.text = "Configure the API key in Settings > StrateCode Agents."
            return
        }
        val context = ProjectContextResolver.resolve(project)
        projectLabel.text = "Project: ${context.projectName} | branch=${context.branch ?: "-"} | repo=${context.repositoryUrl ?: "degraded"}"
        val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val ready = client.checkReady()
                val capabilities = client.listCapabilities(context.repositoryUrl, "needs_external_evidence", "researcher", listOf("evidence", "docs"))
                val bridges = client.listBridges()
                val bridge = BridgeResolver.resolve(bridges.items, context.workspaceRoot, settings.currentState.bridgeName)
                val projectCaps = context.repositoryUrl?.let { client.getProjectCapabilities(it, "needs_repo_static_analysis", "reviewer") }
                SwingUtilities.invokeLater {
                    connectionLabel.text = "Backend: ${if (ready.ready) "ready" else "not ready"} | checks=${ready.checks}"
                    bridgeLabel.text = "Bridge: ${bridge.status} | ${bridge.detail}"
                    val builder = StringBuilder()
                    builder.appendLine("Effective capability candidates:")
                    capabilities.capabilities.forEach {
                        builder.appendLine("- ${it.name} [${it.kind}] tags=${it.capabilityTags.joinToString(",")} score=${"%.1f".format(it.score)}")
                    }
                    if (projectCaps != null) {
                        builder.appendLine()
                        builder.appendLine("Project policy mode: ${projectCaps.mode}")
                        builder.appendLine("Reviewer-scoped project capabilities:")
                        projectCaps.capabilities.forEach {
                            builder.appendLine("- ${it.name} [${it.kind}] tags=${it.capabilityTags.joinToString(",")} score=${"%.1f".format(it.score)}")
                        }
                    }
                    capabilitiesArea.text = builder.toString()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    connectionLabel.text = "Backend: error"
                    bridgeLabel.text = "Bridge: unresolved"
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
                SwingUtilities.invokeLater {
                    notify("Initiative created", "${it.title} (${it.id})", NotificationType.INFORMATION)
                    initiativeGoalArea.text = ""
                    initiativeTitleField.text = it.title
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
        if (apiKey.isBlank()) {
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).listInitiatives()
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    initiativesModel.clear()
                    response.items.forEach(initiativesModel::addElement)
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

    private fun settings(): PluginSettingsService =
        ApplicationManager.getApplication().getService(PluginSettingsService::class.java)

    private fun notify(title: String, message: String, type: NotificationType) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("StrateCode Agents")
            .createNotification(title, message, type)
            .notify(project)
    }
}
