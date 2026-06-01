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
import com.stratecode.lab.jetbrains.bridge.BridgeResolution
import com.stratecode.lab.jetbrains.bridge.BridgeResolver
import com.stratecode.lab.jetbrains.bridge.executionBlockReason
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.client.LocalBridgeResponse
import com.stratecode.lab.jetbrains.client.OrchestratorClient
import com.stratecode.lab.jetbrains.client.ApprovalRecord
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
import javax.swing.JOptionPane
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
    private val bridgeSummaryArea = infoArea(14)
    private val bridgeCandidatesArea = infoArea(12)
    private val initiativeSummaryArea = infoArea(15)
    private val initiativeReviewsArea = infoArea(8)
    private val tasksArea = infoArea(14)
    private val artifactsArea = infoArea(16)
    private val approvalsArea = infoArea(14)
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
    private val taskLinksModel = DefaultListModel<InitiativeTaskLinkRecord>()
    private val taskLinksList = JBList(taskLinksModel).apply {
        selectionMode = ListSelectionModel.MULTIPLE_INTERVAL_SELECTION
        cellRenderer = InitiativeTaskCellRenderer()
    }
    private val artifactsModel = DefaultListModel<InitiativeArtifactRecord>()
    private val artifactsList = JBList(artifactsModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = InitiativeArtifactCellRenderer()
    }
    private val approvalsModel = DefaultListModel<ApprovalRecord>()
    private val approvalsList = JBList(approvalsModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = ApprovalCellRenderer()
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
    private val setTaskModeButton = JButton("Set Task Mode")
    private val launchTaskButton = JButton("Launch Selected Tasks")
    private val refreshApprovalsButton = JButton("Refresh Approvals")
    private val approveApprovalButton = JButton("Approve Approval")
    private val rejectApprovalButton = JButton("Reject Approval")
    private val refreshBridgeButton = JButton("Refresh Bridge")
    private val registerBridgeButton = JButton("Register Bridge")
    private val bridgeSmokeButton = JButton("Bridge Smoke")

    private var selectedInitiative: InitiativeSummary? = null
    private var selectedDetail: InitiativeDetailResponseRecord? = null
    private var selectedTaskLink: InitiativeTaskLinkRecord? = null
    private var selectedArtifact: InitiativeArtifactRecord? = null
    private var selectedApproval: ApprovalRecord? = null
    private var currentProjectContext: ProjectContext? = null
    private var currentBridgeResolution: BridgeResolution? = null
    private var currentBridges: List<LocalBridgeResponse> = emptyList()

    init {
        border = JBUI.Borders.empty(12)
        add(buildHeader(), BorderLayout.NORTH)
        add(buildTabs(), BorderLayout.CENTER)
        bindActions()
        updateActionState()
        loadStatus()
        loadInitiatives()
        loadApprovals()
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
            addTab("Approvals", buildApprovalsTab())
            addTab("Bridge", buildBridgeTab())
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
        taskLinksList.addListSelectionListener {
            selectedTaskLink = taskLinksList.selectedValue
            tasksArea.text = renderTaskSelection(taskLinksList.selectedValuesList)
            updateActionState()
        }
        artifactsList.addListSelectionListener {
            selectedArtifact = artifactsList.selectedValue
            artifactsArea.text = renderArtifactDetail(selectedArtifact)
            updateActionState()
        }

        val detailTabs = JBTabbedPane().apply {
            addTab("Summary", section("Initiative Summary", JBScrollPane(initiativeSummaryArea)))
            addTab("Reviews", section("Phase Reviews", JBScrollPane(initiativeReviewsArea)))
            addTab(
                "Tasks",
                JBSplitter(false, 0.42f).apply {
                    firstComponent = section("Task Backlog", JBScrollPane(taskLinksList))
                    secondComponent = section("Task Detail", JBScrollPane(tasksArea))
                },
            )
            addTab(
                "Artifacts",
                JBSplitter(false, 0.34f).apply {
                    firstComponent = section("Initiative Artifacts", JBScrollPane(artifactsList))
                    secondComponent = section("Artifact Detail", JBScrollPane(artifactsArea))
                },
            )
        }

        val feedbackSection = section("Action Feedback", JBScrollPane(feedbackArea))
        val actionControls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(advanceButton)
            add(approveButton)
            add(rejectButton)
            add(generateTasksButton)
            add(refreshDetailButton)
            add(setTaskModeButton)
            add(launchTaskButton)
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

    private fun buildApprovalsTab(): JComponent {
        approvalsList.addListSelectionListener {
            selectedApproval = approvalsList.selectedValue
            approvalsArea.text = renderApprovalDetail(selectedApproval)
            updateActionState()
        }

        val controls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(refreshApprovalsButton)
            add(approveApprovalButton)
            add(rejectApprovalButton)
        }

        val splitter = JBSplitter(false, 0.40f).apply {
            firstComponent = section("Pending Approvals", JBScrollPane(approvalsList))
            secondComponent = section("Approval Detail", JBScrollPane(approvalsArea))
        }

        return JPanel(BorderLayout()).apply {
            border = JBUI.Borders.emptyTop(12)
            add(splitter, BorderLayout.CENTER)
            add(controls, BorderLayout.SOUTH)
        }
    }

    private fun buildBridgeTab(): JComponent {
        val controls = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(refreshBridgeButton)
            add(registerBridgeButton)
            add(bridgeSmokeButton)
        }
        val splitter = JBSplitter(false, 0.55f).apply {
            firstComponent = section("Bridge Summary", JBScrollPane(bridgeSummaryArea))
            secondComponent = section("Known Bridges", JBScrollPane(bridgeCandidatesArea))
        }
        return JPanel(BorderLayout()).apply {
            border = JBUI.Borders.emptyTop(12)
            add(splitter, BorderLayout.CENTER)
            add(controls, BorderLayout.SOUTH)
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
        setTaskModeButton.addActionListener { setSelectedTaskMode() }
        launchTaskButton.addActionListener { launchSelectedTask() }
        refreshApprovalsButton.addActionListener { loadApprovals() }
        approveApprovalButton.addActionListener { resolveSelectedApproval(true) }
        rejectApprovalButton.addActionListener { resolveSelectedApproval(false) }
        refreshBridgeButton.addActionListener { loadStatus() }
        registerBridgeButton.addActionListener { registerBridge() }
        bridgeSmokeButton.addActionListener { runBridgeSmoke() }
    }

    private fun updateActionState() {
        val detail = selectedDetail
        if (detail == null) {
            advanceButton.isEnabled = false
            approveButton.isEnabled = false
            rejectButton.isEnabled = false
            generateTasksButton.isEnabled = false
            refreshDetailButton.isEnabled = false
            setTaskModeButton.isEnabled = false
            launchTaskButton.isEnabled = false
            refreshApprovalsButton.isEnabled = true
            approveApprovalButton.isEnabled = selectedApproval != null
            rejectApprovalButton.isEnabled = selectedApproval != null
            return
        }
        refreshDetailButton.isEnabled = true
        val phase = detail.initiative.currentPhase
        val status = detail.initiative.status
        advanceButton.isEnabled = phase in setOf("requirements", "design") && status.endsWith("_draft")
        approveButton.isEnabled = phase in setOf("requirements", "design", "plan") && status.endsWith("_review")
        rejectButton.isEnabled = phase in setOf("requirements", "design", "plan") && status.endsWith("_review")
        generateTasksButton.isEnabled = phase == "plan" && status == "plan_draft"
        val selectedTasks = taskLinksList.selectedValuesList
        setTaskModeButton.isEnabled = selectedTasks.size == 1
        launchTaskButton.isEnabled = selectedTasks.isNotEmpty() &&
            detail.executionSummary.taskCount > 0 &&
            currentBridgeResolution?.executable == true
        val hasApproval = selectedApproval != null
        refreshApprovalsButton.isEnabled = true
        approveApprovalButton.isEnabled = hasApproval
        rejectApprovalButton.isEnabled = hasApproval
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        currentProjectContext = context
        updateProjectSummary(context)

        if (apiKey.isBlank()) {
            backendBadge.text = "Backend  missing key"
            setBadgeColor(backendBadge, Color(0x8B5E3C))
            capabilitiesArea.text = "Configure the API key in Settings > StrateCode Agents."
            bridgeSummaryArea.text = "Configure the API key first. No bridge checks can run without it."
            bridgeCandidatesArea.text = ""
            currentBridgeResolution = null
            currentBridges = emptyList()
            updateActionState()
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
                    currentBridgeResolution = bridge
                    currentBridges = bridges.items
                    backendBadge.text = "Backend  ${if (ready.ready) "ready" else "not ready"}"
                    setBadgeColor(backendBadge, if (ready.ready) Color(0x1F7A4C) else Color(0x8B5E3C))
                    bridgeBadge.text = "Bridge  ${bridge.status}"
                    setBadgeColor(bridgeBadge, when (bridge.status) {
                        "online", "idle", "busy", "ready", "active" -> if (bridge.executable) Color(0x1F7A4C) else Color(0x8B5E3C)
                        "missing" -> Color(0x8B5E3C)
                        else -> Color(0x5B6B7A)
                    })
                    metadataSummaryLabel.text = metadataSummaryHtml(ProjectContextResolver.resolve(project))
                    capabilitiesArea.text = buildCapabilitiesText(projectCaps?.mode, capabilities.capabilities, projectCaps?.capabilities.orEmpty())
                    bridgeSummaryArea.text = renderBridgeSummary(context, bridge)
                    bridgeCandidatesArea.text = renderBridgeCandidates(context.workspaceRoot, bridges.items)
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    currentBridgeResolution = null
                    currentBridges = emptyList()
                    backendBadge.text = "Backend  error"
                    setBadgeColor(backendBadge, Color(0x8B2E2E))
                    bridgeBadge.text = "Bridge  unresolved"
                    setBadgeColor(bridgeBadge, Color(0x8B5E3C))
                    capabilitiesArea.text = error.message ?: error.toString()
                    bridgeSummaryArea.text = error.message ?: error.toString()
                    bridgeCandidatesArea.text = ""
                    updateActionState()
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
        loadApprovals()
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
                        taskLinksModel.clear()
                        selectedDetail = null
                        selectedTaskLink = null
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
                val artifacts = client.listInitiativeArtifacts(initiativeId)
                Triple(detail, tasks.items, artifacts.items)
            }.onSuccess { (detail, tasks, artifacts) ->
                val context = ProjectContextResolver.resolve(project)
                StrateCodeProjectStore.write(
                    context,
                    bridgeName = settings.currentState.bridgeName,
                    lastInitiativeId = detail.initiative.id,
                    lastInitiativeTitle = detail.initiative.title,
                )
                SwingUtilities.invokeLater {
                    selectedDetail = detail
                    taskLinksModel.clear()
                    tasks.forEach(taskLinksModel::addElement)
                    artifactsModel.clear()
                    artifacts.forEach(artifactsModel::addElement)
                    if (selectedTaskLink != null) {
                        val target = tasks.firstOrNull { it.taskId == selectedTaskLink?.taskId }
                        if (target != null) {
                            taskLinksList.setSelectedValue(target, true)
                        }
                    }
                    if (selectedArtifact != null) {
                        val targetArtifact = artifacts.firstOrNull { it.id == selectedArtifact?.id }
                        if (targetArtifact != null) {
                            artifactsList.setSelectedValue(targetArtifact, true)
                        }
                    }
                    initiativeSummaryArea.text = renderInitiativeSummary(detail)
                    initiativeReviewsArea.text = renderReviews(detail)
                    tasksArea.text = renderTaskSelection(taskLinksList.selectedValuesList)
                    if (artifacts.isEmpty()) {
                        artifactsArea.text = "No artifacts generated yet."
                        selectedArtifact = null
                    }
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    initiativeSummaryArea.text = error.message ?: error.toString()
                    initiativeReviewsArea.text = ""
                    tasksArea.text = ""
                    artifactsArea.text = ""
                    taskLinksModel.clear()
                    artifactsModel.clear()
                    selectedDetail = null
                    selectedTaskLink = null
                    selectedArtifact = null
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

    private fun setSelectedTaskMode() {
        val detail = selectedDetail ?: return
        val link = selectedTaskLink ?: return
        val choice = JOptionPane.showInputDialog(
            this,
            "Execution mode for selected task:",
            link.executionMode,
        )?.trim().orEmpty()
        if (choice !in setOf("manual", "agent_local", "agent_remote")) {
            if (choice.isNotBlank()) {
                notify("Invalid execution mode", "Use manual, agent_local, or agent_remote.", NotificationType.WARNING)
            }
            return
        }
        runInitiativeMutation("Task mode updated") { client ->
            client.updateInitiativeTaskMode(detail.initiative.id, link.taskId, choice)
        }
    }

    private fun launchSelectedTask() {
        val detail = selectedDetail ?: return
        val selectedTasks = taskLinksList.selectedValuesList
        if (selectedTasks.isEmpty()) {
            return
        }
        val resolution = currentBridgeResolution
        val blockReason = if (resolution == null) {
            "No bridge state is loaded for this project."
        } else {
            resolution.executionBlockReason()
        }
        if (blockReason != null) {
            notify("Launch blocked", blockReason, NotificationType.WARNING)
            loadStatus()
            return
        }
        runInitiativeMutation("Task launch queued") { client ->
            client.launchInitiativeTasks(detail.initiative.id, selectedTasks.map { it.taskId })
        }
    }

    private fun runBridgeSmoke() {
        val context = ProjectContextResolver.resolve(project)
        val resolution = currentBridgeResolution
        if (resolution == null) {
            loadStatus()
            notify("Bridge smoke inconclusive", "Bridge state was not loaded yet. Refreshed instead.", NotificationType.WARNING)
            return
        }
        val problem = resolution.executionBlockReason()
        if (problem == null) {
            notify(
                "Bridge smoke passed",
                "Bridge ${resolution.matched?.name ?: "-"} is executable for ${context.workspaceRoot}.",
                NotificationType.INFORMATION,
            )
        } else {
            notify("Bridge smoke failed", problem, NotificationType.WARNING)
        }
    }

    private fun loadApprovals() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            approvalsArea.text = "Configure an API key first."
            approvalsModel.clear()
            selectedApproval = null
            updateActionState()
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).listApprovals()
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    approvalsModel.clear()
                    response.items.forEach(approvalsModel::addElement)
                    if (response.items.isEmpty()) {
                        approvalsArea.text = "No pending approvals."
                        selectedApproval = null
                    }
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    approvalsArea.text = error.message ?: error.toString()
                    approvalsModel.clear()
                    selectedApproval = null
                    updateActionState()
                }
            }
        }
    }

    private fun resolveSelectedApproval(approve: Boolean) {
        val approval = selectedApproval ?: return
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            notify("Approval blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
                if (approve) {
                    client.approveApproval(approval.id, "jetbrains-plugin")
                } else {
                    client.rejectApproval(approval.id, "jetbrains-plugin")
                }
            }.onSuccess {
                SwingUtilities.invokeLater {
                    notify(if (approve) "Approval granted" else "Approval rejected", approval.id, NotificationType.INFORMATION)
                    loadApprovals()
                    selectedInitiative?.let { loadInitiativeDetail(it.id) }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Approval resolution failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
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
            Only initiatives with <code>workspace_root=${escape(context.workspaceRoot)}</code> are listed.<br/>
            Launch stays blocked if the matched bridge is stale or points elsewhere.
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

    private fun renderBridgeSummary(context: ProjectContext, resolution: BridgeResolution): String =
        buildString {
            appendLine("Configured bridge name: ${settings().currentState.bridgeName}")
            appendLine("Workspace root: ${context.workspaceRoot}")
            appendLine("Repository: ${context.repositoryUrl ?: "degraded"}")
            appendLine()
            appendLine("Resolution")
            appendLine("==========")
            appendLine("Status: ${resolution.status}")
            appendLine("Consistency: ${resolution.consistency}")
            appendLine("Executable: ${resolution.executable}")
            appendLine("Detail: ${resolution.detail}")
            appendLine("Heartbeat age: ${resolution.heartbeatAgeSeconds?.let { "${it}s" } ?: "<never>"}")
            appendLine("Stale: ${resolution.stale}")
            appendLine()
            if (resolution.matched != null) {
                appendLine("Matched bridge")
                appendLine("==============")
                appendLine("ID: ${resolution.matched.id}")
                appendLine("Name: ${resolution.matched.name}")
                appendLine("Host: ${resolution.matched.hostname}")
                appendLine("Workspace: ${resolution.matched.workspaceRoot}")
                appendLine("Last heartbeat: ${resolution.matched.lastHeartbeat ?: "-"}")
                appendLine()
            }
            resolution.executionBlockReason()?.let {
                appendLine("Execution block")
                appendLine("===============")
                appendLine(it)
            }
        }.trim()

    private fun renderBridgeCandidates(workspaceRoot: String, bridges: List<LocalBridgeResponse>): String {
        if (bridges.isEmpty()) {
            return "No bridges are registered."
        }
        return buildString {
            bridges.sortedWith(compareBy<LocalBridgeResponse>({ it.workspaceRoot != workspaceRoot }, { it.name })).forEach { bridge ->
                val marker = when {
                    bridge.workspaceRoot == workspaceRoot -> "*"
                    else -> "-"
                }
                appendLine("$marker ${bridge.name} [${bridge.status}]")
                appendLine("  workspace=${bridge.workspaceRoot}")
                appendLine("  host=${bridge.hostname}")
                appendLine("  heartbeat=${bridge.lastHeartbeat ?: "<never>"}")
                appendLine()
            }
        }.trim()
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

    private fun renderTaskSelection(selectedTasks: List<InitiativeTaskLinkRecord>): String {
        if (selectedTasks.isEmpty()) {
            return if (taskLinksModel.isEmpty) {
                "No tasks materialized yet."
            } else {
                "Select one or more tasks to inspect or launch."
            }
        }
        if (selectedTasks.size == 1) {
            return renderTasks(selectedTasks)
        }
        return buildString {
            appendLine("${selectedTasks.size} tasks selected")
            appendLine("====================")
            selectedTasks.sortedBy { it.launchOrder }.forEach { link ->
                appendLine("#${link.launchOrder} ${link.task.description}")
                appendLine("mode=${link.executionMode} state=${link.task.state} target=${link.task.executionTarget}")
                appendLine()
            }
        }.trim()
    }

    private fun renderArtifactDetail(artifact: InitiativeArtifactRecord?): String {
        if (artifact == null) {
            return if (artifactsModel.isEmpty) {
                "No artifacts generated yet."
            } else {
                "Select an artifact to inspect its payload."
            }
        }
        return buildString {
            appendLine("Artifact: ${artifact.id}")
            appendLine("Type: ${artifact.artifactType}")
            appendLine("Title: ${artifact.title ?: "-"}")
            appendLine("Media type: ${artifact.mediaType ?: "-"}")
            appendLine("URI: ${artifact.uri ?: "-"}")
            appendLine("Created: ${artifact.createdAt}")
            appendLine()
            if (artifact.metadata.isNotEmpty()) {
                appendLine("Metadata")
                appendLine("========")
                artifact.metadata.toSortedMap().forEach { (key, value) ->
                    appendLine("$key: $value")
                }
                appendLine()
            }
            appendLine("Content")
            appendLine("=======")
            appendLine(artifact.contentText?.take(12000) ?: "(no textual content)")
        }
    }

    private fun renderApprovalDetail(approval: ApprovalRecord?): String {
        if (approval == null) {
            return "Select a pending approval."
        }
        return buildString {
            appendLine("Approval: ${approval.id}")
            appendLine("Task: ${approval.taskId}")
            appendLine("Action: ${approval.actionType}")
            appendLine("Target: ${approval.targetResource}")
            appendLine("Status: ${approval.status}")
            appendLine("Requested: ${approval.requestedAt}")
            appendLine("Timeout at: ${approval.timeoutAt}")
            appendLine("Timeout seconds: ${approval.timeoutSeconds}")
            appendLine("Escalation: ${approval.escalationLevel}")
            appendLine("Operator: ${approval.operator ?: "-"}")
        }
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

private class InitiativeTaskCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is InitiativeTaskLinkRecord && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            it.text = """
                <html>
                <b>${escape(value.task.description)}</b><br/>
                <span style='color:#6B7280'>${escape(value.task.state)} / ${escape(value.executionMode)} / ${escape(value.task.executionTarget)}</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}

private class ApprovalCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is ApprovalRecord && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            it.text = """
                <html>
                <b>${escape(value.actionType)}</b><br/>
                <span style='color:#6B7280'>${escape(value.taskId)} / ${escape(value.targetResource)} / ${escape(value.status)}</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}

private class InitiativeArtifactCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is InitiativeArtifactRecord && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            it.text = """
                <html>
                <b>${escape(value.title ?: value.artifactType)}</b><br/>
                <span style='color:#6B7280'>${escape(value.artifactType)} / ${escape(value.createdAt)}</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}
