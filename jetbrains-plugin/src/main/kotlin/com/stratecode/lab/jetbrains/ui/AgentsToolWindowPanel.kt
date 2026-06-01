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
import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeSummary
import com.stratecode.lab.jetbrains.client.InitiativeTaskLinkRecord
import com.stratecode.lab.jetbrains.client.LocalBridgeResponse
import com.stratecode.lab.jetbrains.client.OrchestratorClient
import com.stratecode.lab.jetbrains.client.TaskDetailRecord
import com.stratecode.lab.jetbrains.project.ProjectContext
import com.stratecode.lab.jetbrains.project.ProjectContextResolver
import com.stratecode.lab.jetbrains.project.StrateCodeProjectStore
import com.stratecode.lab.jetbrains.settings.PluginSettingsService
import com.stratecode.lab.jetbrains.task.EvidenceExtractionResult
import com.stratecode.lab.jetbrains.task.EvidenceLocation
import com.stratecode.lab.jetbrains.task.TaskExecutionSupport
import com.stratecode.lab.jetbrains.task.TaskResultPatchView
import com.stratecode.lab.jetbrains.workbench.ApprovalSummaryViewState
import com.stratecode.lab.jetbrains.workbench.HeaderStatusViewState
import com.stratecode.lab.jetbrains.workbench.InitiativeWorkbenchItem
import com.stratecode.lab.jetbrains.workbench.StatusTone
import com.stratecode.lab.jetbrains.workbench.TaskActionAvailability
import com.stratecode.lab.jetbrains.workbench.TaskDetailViewState
import com.stratecode.lab.jetbrains.workbench.TaskWorkbenchItem
import com.stratecode.lab.jetbrains.workbench.WorkbenchStateMapper
import java.awt.BorderLayout
import java.awt.CardLayout
import java.awt.Color
import java.awt.Dimension
import java.awt.FlowLayout
import java.awt.Font
import java.awt.GridLayout
import javax.swing.BorderFactory
import javax.swing.Box
import javax.swing.BoxLayout
import javax.swing.DefaultComboBoxModel
import javax.swing.DefaultListCellRenderer
import javax.swing.DefaultListModel
import javax.swing.JButton
import javax.swing.JComboBox
import javax.swing.JComponent
import javax.swing.JLabel
import javax.swing.JList
import javax.swing.JOptionPane
import javax.swing.JPanel
import javax.swing.ListSelectionModel
import javax.swing.SwingConstants
import javax.swing.SwingUtilities

class AgentsToolWindowPanel(
    private val project: Project,
) : JPanel(BorderLayout()) {
    private enum class DrawerMode {
        NONE,
        APPROVALS,
        BRIDGE,
        CAPABILITIES,
        INITIATIVE,
    }

    private val titleLabel = JLabel("StrateCode Workbench")
    private val subtitleLabel = JLabel("<html>Task-first console for governed initiative execution.</html>")
    private val projectBadge = badge("Project", "unresolved", StatusTone.NEUTRAL)
    private val backendBadge = badge("Backend", "unknown", StatusTone.NEUTRAL)
    private val bridgeBadge = badge("Bridge", "unresolved", StatusTone.NEUTRAL)
    private val approvalsBadge = badge("Approvals", "0 pending", StatusTone.NEUTRAL)

    private val refreshButton = JButton("Refresh")
    private val createInitiativeButton = JButton("Create Initiative")
    private val resetWorkspaceButton = JButton("Reset Local State")
    private val approvalsDrawerButton = JButton("Approvals")
    private val bridgeDrawerButton = JButton("Bridge")
    private val capabilitiesDrawerButton = JButton("Capabilities")
    private val initiativeDrawerButton = JButton("Initiative Info")

    private val initiativeSelectorModel = DefaultComboBoxModel<InitiativeWorkbenchItem>()
    private val initiativeSelector = JComboBox(initiativeSelectorModel).apply {
        renderer = InitiativeWorkbenchItemRenderer()
    }
    private val statusFilterCombo = JComboBox(arrayOf("all", "pending", "running", "waiting_approval", "completed", "failed"))
    private val agentFilterCombo = JComboBox(arrayOf("all", "planner", "researcher", "coder", "reviewer"))

    private val taskModel = DefaultListModel<TaskWorkbenchItem>()
    private val taskList = JBList(taskModel).apply {
        selectionMode = ListSelectionModel.MULTIPLE_INTERVAL_SELECTION
        cellRenderer = TaskWorkbenchItemCellRenderer()
    }

    private val taskHeadlineLabel = JLabel("No task selected")
    private val taskMetaLabel = JLabel("Pick a task from the backlog to inspect diff, evidence, and patch actions.")
    private val taskBadgesLabel = JLabel("")

    private val approvalCalloutPanel = JPanel(BorderLayout())
    private val approvalCalloutLabel = JLabel("")
    private val approveInlineButton = JButton("Approve")
    private val rejectInlineButton = JButton("Reject")

    private val setModeButton = JButton("Set Mode")
    private val launchButton = JButton("Launch")
    private val previewDiffButton = JButton("Preview Diff")
    private val applyPatchButton = JButton("Apply Patch")
    private val openChangedFileButton = JButton("Open Changed File")
    private val openEvidenceButton = JButton("Open First Evidence")

    private val summaryArea = infoArea(16)
    private val diffArea = infoArea(18)
    private val evidenceArea = infoArea(14)
    private val artifactDetailArea = infoArea(14)

    private val evidenceModel = DefaultListModel<EvidenceLocation>()
    private val evidenceList = JBList(evidenceModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = EvidenceLocationCellRenderer()
    }

    private val taskArtifactModel = DefaultListModel<InitiativeArtifactRecord>()
    private val taskArtifactList = JBList(taskArtifactModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = InitiativeArtifactCellRenderer()
    }

    private val detailTabs = JBTabbedPane()

    private val drawerTitleLabel = JLabel("Support Panel")
    private val drawerWrapper = JPanel(BorderLayout())
    private val drawerCards = JPanel(CardLayout())
    private val closeDrawerButton = JButton("Hide")

    private val approvalsModel = DefaultListModel<ApprovalRecord>()
    private val approvalsList = JBList(approvalsModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = ApprovalCellRenderer()
    }
    private val approvalsArea = infoArea(12)
    private val refreshApprovalsButton = JButton("Refresh Approvals")
    private val approveApprovalButton = JButton("Approve")
    private val rejectApprovalButton = JButton("Reject")

    private val bridgeSummaryArea = infoArea(14)
    private val bridgeCandidatesArea = infoArea(12)
    private val refreshBridgeButton = JButton("Refresh Bridge")
    private val registerBridgeButton = JButton("Register Bridge")
    private val bridgeSmokeButton = JButton("Bridge Smoke")

    private val capabilitiesArea = infoArea(16)
    private val initiativeInfoArea = infoArea(16)
    private val refreshInitiativeButton = JButton("Refresh Initiative")
    private val advanceButton = JButton("Advance Draft")
    private val approvePhaseButton = JButton("Approve Phase")
    private val rejectPhaseButton = JButton("Reject Phase")
    private val generateTasksButton = JButton("Generate Tasks")

    private var drawerMode: DrawerMode = DrawerMode.NONE
    private var backendReady: Boolean? = null
    private var currentProjectContext: ProjectContext? = null
    private var currentBridgeResolution: BridgeResolution? = null
    private var currentBridges: List<LocalBridgeResponse> = emptyList()
    private var currentApprovals: List<ApprovalRecord> = emptyList()
    private var currentInitiatives: List<InitiativeSummary> = emptyList()
    private val initiativeDetailCache = linkedMapOf<String, InitiativeDetailResponseRecord>()
    private val initiativeTaskCache = linkedMapOf<String, List<InitiativeTaskLinkRecord>>()
    private val initiativeArtifactCache = linkedMapOf<String, List<InitiativeArtifactRecord>>()
    private val taskPatchCache = linkedMapOf<String, TaskResultPatchView?>()
    private val taskEvidenceCache = linkedMapOf<String, EvidenceExtractionResult?>()
    private val taskSourcesCache = linkedMapOf<String, List<InitiativeArtifactRecord>>()
    private var selectedInitiative: InitiativeSummary? = null
    private var selectedTaskDetail: TaskDetailRecord? = null
    private var selectedTaskPatchView: TaskResultPatchView? = null
    private var selectedTaskEvidence: EvidenceExtractionResult? = null
    private var selectedTaskSourceArtifacts: List<InitiativeArtifactRecord> = emptyList()
    private var selectedArtifact: InitiativeArtifactRecord? = null
    private var selectedApproval: ApprovalRecord? = null
    private var selectedEvidenceLocation: EvidenceLocation? = null

    init {
        border = JBUI.Borders.empty(12)
        buildDetailTabs()
        configureApprovalCallout()
        configureDrawer()
        add(buildHeader(), BorderLayout.NORTH)
        add(buildWorkbench(), BorderLayout.CENTER)
        bindActions()
        updateActionState()
        loadStatus()
        loadInitiatives()
        loadApprovals()
    }

    private fun buildHeader(): JComponent {
        titleLabel.font = titleLabel.font.deriveFont(Font.BOLD, 20f)
        subtitleLabel.foreground = Color(0x6B7280)
        val badges = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(projectBadge)
            add(backendBadge)
            add(bridgeBadge)
            add(approvalsBadge)
        }
        val actions = JPanel(FlowLayout(FlowLayout.RIGHT, 8, 0)).apply {
            isOpaque = false
            add(refreshButton)
            add(createInitiativeButton)
            add(resetWorkspaceButton)
            add(approvalsDrawerButton)
            add(bridgeDrawerButton)
            add(capabilitiesDrawerButton)
            add(initiativeDrawerButton)
        }
        val topRow = JPanel(BorderLayout()).apply {
            isOpaque = false
            add(titleLabel, BorderLayout.WEST)
            add(actions, BorderLayout.EAST)
        }
        val content = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            isOpaque = false
            add(topRow)
            add(Box.createVerticalStrut(8))
            add(subtitleLabel)
            add(Box.createVerticalStrut(8))
            add(badges)
        }
        return JPanel(BorderLayout()).apply {
            border = BorderFactory.createCompoundBorder(
                BorderFactory.createLineBorder(Color(0xD8DEE9)),
                JBUI.Borders.empty(14),
            )
            add(content, BorderLayout.CENTER)
        }
    }

    private fun buildWorkbench(): JComponent {
        val leftControls = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(JLabel("Active Initiative"))
            add(Box.createVerticalStrut(4))
            add(initiativeSelector)
            add(Box.createVerticalStrut(10))
            add(JLabel("Task Filters"))
            add(Box.createVerticalStrut(4))
            add(JPanel(GridLayout(1, 2, 8, 0)).apply {
                isOpaque = false
                add(statusFilterCombo)
                add(agentFilterCombo)
            })
        }

        val left = JPanel(BorderLayout()).apply {
            border = JBUI.Borders.emptyRight(8)
            add(section("Work", leftControls), BorderLayout.NORTH)
            add(section("Task Backlog", JBScrollPane(taskList)), BorderLayout.CENTER)
        }

        val detailHeader = JPanel(BorderLayout()).apply {
            border = JBUI.Borders.empty(0, 0, 10, 0)
            add(
                JPanel().apply {
                    layout = BoxLayout(this, BoxLayout.Y_AXIS)
                    isOpaque = false
                    add(taskHeadlineLabel)
                    add(Box.createVerticalStrut(4))
                    add(taskMetaLabel)
                    add(Box.createVerticalStrut(4))
                    add(taskBadgesLabel)
                },
                BorderLayout.CENTER,
            )
        }

        val actionBar = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(setModeButton)
            add(launchButton)
            add(previewDiffButton)
            add(applyPatchButton)
            add(openChangedFileButton)
            add(openEvidenceButton)
        }

        val detailTop = JPanel(BorderLayout()).apply {
            add(detailHeader, BorderLayout.NORTH)
            add(approvalCalloutPanel, BorderLayout.CENTER)
            add(actionBar, BorderLayout.SOUTH)
        }

        val right = JPanel(BorderLayout()).apply {
            add(detailTop, BorderLayout.NORTH)
            add(detailTabs, BorderLayout.CENTER)
            add(drawerWrapper, BorderLayout.SOUTH)
        }

        return JBSplitter(false, 0.30f).apply {
            border = JBUI.Borders.emptyTop(12)
            firstComponent = left
            secondComponent = right
        }
    }

    private fun buildDetailTabs() {
        detailTabs.addTab("Summary", section("Task / Initiative Summary", JBScrollPane(summaryArea)))
        detailTabs.addTab("Diff", section("Diff Preview", JBScrollPane(diffArea)))
        detailTabs.addTab(
            "Evidence",
            JBSplitter(false, 0.35f).apply {
                firstComponent = section("Findings", JBScrollPane(evidenceList))
                secondComponent = section("Evidence Detail", JBScrollPane(evidenceArea))
            },
        )
        detailTabs.addTab(
            "Artifacts",
            JBSplitter(false, 0.34f).apply {
                firstComponent = section("Task Artifacts", JBScrollPane(taskArtifactList))
                secondComponent = section("Artifact Detail", JBScrollPane(artifactDetailArea))
            },
        )
    }

    private fun configureApprovalCallout() {
        approvalCalloutPanel.isVisible = false
        approvalCalloutPanel.border = BorderFactory.createCompoundBorder(
            BorderFactory.createLineBorder(Color(0xE8B200)),
            JBUI.Borders.empty(8),
        )
        approvalCalloutPanel.background = Color(0xFFF7DB)
        approvalCalloutPanel.add(approvalCalloutLabel, BorderLayout.CENTER)
        approvalCalloutPanel.add(
            JPanel(FlowLayout(FlowLayout.RIGHT, 6, 0)).apply {
                isOpaque = false
                add(approveInlineButton)
                add(rejectInlineButton)
            },
            BorderLayout.EAST,
        )
    }

    private fun configureDrawer() {
        drawerTitleLabel.font = drawerTitleLabel.font.deriveFont(Font.BOLD, 13f)
        drawerCards.add(buildApprovalsDrawer(), DrawerMode.APPROVALS.name)
        drawerCards.add(buildBridgeDrawer(), DrawerMode.BRIDGE.name)
        drawerCards.add(buildCapabilitiesDrawer(), DrawerMode.CAPABILITIES.name)
        drawerCards.add(buildInitiativeDrawer(), DrawerMode.INITIATIVE.name)
        drawerWrapper.border = BorderFactory.createCompoundBorder(
            BorderFactory.createMatteBorder(1, 0, 0, 0, Color(0xD8DEE9)),
            JBUI.Borders.emptyTop(8),
        )
        drawerWrapper.isVisible = false
        drawerWrapper.add(
            JPanel(BorderLayout()).apply {
                add(drawerTitleLabel, BorderLayout.WEST)
                add(closeDrawerButton, BorderLayout.EAST)
            },
            BorderLayout.NORTH,
        )
        drawerWrapper.add(drawerCards, BorderLayout.CENTER)
        drawerWrapper.preferredSize = Dimension(100, 250)
    }

    private fun buildApprovalsDrawer(): JComponent {
        approvalsList.addListSelectionListener {
            selectedApproval = approvalsList.selectedValue
            approvalsArea.text = renderApprovalDetail(selectedApproval)
            updateActionState()
        }
        return JPanel(BorderLayout()).apply {
            add(
                JBSplitter(false, 0.36f).apply {
                    firstComponent = section("Pending Approvals", JBScrollPane(approvalsList))
                    secondComponent = section("Approval Detail", JBScrollPane(approvalsArea))
                },
                BorderLayout.CENTER,
            )
            add(
                JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
                    isOpaque = false
                    add(refreshApprovalsButton)
                    add(approveApprovalButton)
                    add(rejectApprovalButton)
                },
                BorderLayout.SOUTH,
            )
        }
    }

    private fun buildBridgeDrawer(): JComponent =
        JPanel(BorderLayout()).apply {
            add(
                JBSplitter(false, 0.54f).apply {
                    firstComponent = section("Bridge Summary", JBScrollPane(bridgeSummaryArea))
                    secondComponent = section("Known Bridges", JBScrollPane(bridgeCandidatesArea))
                },
                BorderLayout.CENTER,
            )
            add(
                JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
                    isOpaque = false
                    add(refreshBridgeButton)
                    add(registerBridgeButton)
                    add(bridgeSmokeButton)
                },
                BorderLayout.SOUTH,
            )
        }

    private fun buildCapabilitiesDrawer(): JComponent =
        section("Effective Capabilities", JBScrollPane(capabilitiesArea))

    private fun buildInitiativeDrawer(): JComponent =
        JPanel(BorderLayout()).apply {
            add(section("Initiative Snapshot", JBScrollPane(initiativeInfoArea)), BorderLayout.CENTER)
            add(
                JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
                    isOpaque = false
                    add(refreshInitiativeButton)
                    add(advanceButton)
                    add(approvePhaseButton)
                    add(rejectPhaseButton)
                    add(generateTasksButton)
                },
                BorderLayout.SOUTH,
            )
        }

    private fun bindActions() {
        refreshButton.addActionListener {
            loadStatus()
            loadInitiatives(selectInitiativeId = selectedInitiative?.id)
            loadApprovals()
        }
        createInitiativeButton.addActionListener { createInitiative() }
        resetWorkspaceButton.addActionListener { resetWorkspaceState() }
        approvalsDrawerButton.addActionListener { showDrawer(DrawerMode.APPROVALS) }
        bridgeDrawerButton.addActionListener { showDrawer(DrawerMode.BRIDGE) }
        capabilitiesDrawerButton.addActionListener { showDrawer(DrawerMode.CAPABILITIES) }
        initiativeDrawerButton.addActionListener { showDrawer(DrawerMode.INITIATIVE) }
        closeDrawerButton.addActionListener { showDrawer(DrawerMode.NONE) }
        initiativeSelector.addActionListener {
            val selected = initiativeSelector.selectedItem as? InitiativeWorkbenchItem ?: return@addActionListener
            if (selected.id != selectedInitiative?.id) {
                currentInitiatives.firstOrNull { it.id == selected.id }?.let {
                    selectedInitiative = it
                    loadInitiativeDetail(it.id)
                }
            }
        }
        statusFilterCombo.addActionListener { rebuildTaskList() }
        agentFilterCombo.addActionListener { rebuildTaskList() }
        taskList.addListSelectionListener {
            handleTaskSelectionChanged()
            updateActionState()
        }
        evidenceList.addListSelectionListener {
            selectedEvidenceLocation = evidenceList.selectedValue
            evidenceArea.text = renderEvidenceDetail(selectedEvidenceLocation, selectedTaskEvidence)
            updateActionState()
        }
        evidenceList.addMouseListener(object : java.awt.event.MouseAdapter() {
            override fun mouseClicked(event: java.awt.event.MouseEvent) {
                if (event.clickCount == 2) {
                    openSelectedTaskEvidence()
                }
            }
        })
        taskArtifactList.addListSelectionListener {
            selectedArtifact = taskArtifactList.selectedValue
            artifactDetailArea.text = renderArtifactDetail(selectedArtifact)
        }
        refreshApprovalsButton.addActionListener { loadApprovals() }
        approveApprovalButton.addActionListener { resolveSelectedApproval(true) }
        rejectApprovalButton.addActionListener { resolveSelectedApproval(false) }
        refreshBridgeButton.addActionListener { loadStatus() }
        registerBridgeButton.addActionListener { registerBridge() }
        bridgeSmokeButton.addActionListener { runBridgeSmoke() }
        refreshInitiativeButton.addActionListener { selectedInitiative?.let { loadInitiativeDetail(it.id) } }
        advanceButton.addActionListener { advanceSelectedInitiative() }
        approvePhaseButton.addActionListener { resolveSelectedInitiative(true) }
        rejectPhaseButton.addActionListener { resolveSelectedInitiative(false) }
        generateTasksButton.addActionListener { generateSelectedInitiativeTasks() }
        setModeButton.addActionListener { setSelectedTaskMode() }
        launchButton.addActionListener { launchSelectedTasks() }
        previewDiffButton.addActionListener { previewSelectedTaskDiff() }
        applyPatchButton.addActionListener { applySelectedTaskPatch() }
        openChangedFileButton.addActionListener { openSelectedTaskChangedFile() }
        openEvidenceButton.addActionListener { openSelectedTaskEvidence() }
        approveInlineButton.addActionListener { resolveBlockingApproval(true) }
        rejectInlineButton.addActionListener { resolveBlockingApproval(false) }
    }

    private fun showDrawer(mode: DrawerMode) {
        drawerMode = mode
        drawerWrapper.isVisible = mode != DrawerMode.NONE
        if (mode == DrawerMode.NONE) {
            revalidate()
            repaint()
            return
        }
        drawerTitleLabel.text = when (mode) {
            DrawerMode.APPROVALS -> "Approvals"
            DrawerMode.BRIDGE -> "Bridge"
            DrawerMode.CAPABILITIES -> "Capabilities"
            DrawerMode.INITIATIVE -> "Initiative Info"
            DrawerMode.NONE -> "Support Panel"
        }
        (drawerCards.layout as CardLayout).show(drawerCards, mode.name)
        revalidate()
        repaint()
    }

    private fun updateActionState() {
        val selectedItems = taskList.selectedValuesList
        val selectedTask = selectedItems.singleOrNull()
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedTask = selectedTask,
            patchView = selectedTaskPatchView,
            evidence = selectedTaskEvidence,
            resolution = currentBridgeResolution,
            degraded = currentProjectContext?.degraded == true,
        )
        setModeButton.isEnabled = availability.canSetMode && selectedTask != null
        launchButton.isEnabled = selectedItems.isNotEmpty() && currentBridgeResolution?.executable == true
        previewDiffButton.isEnabled = availability.canPreviewDiff
        applyPatchButton.isEnabled = availability.canApplyPatch
        openChangedFileButton.isEnabled = availability.canOpenChangedFile
        openEvidenceButton.isEnabled = availability.canOpenEvidence
        val pendingPhaseAction = selectedDetailOrNull()
        val phase = pendingPhaseAction?.initiative?.currentPhase
        val status = pendingPhaseAction?.initiative?.status
        advanceButton.isEnabled = phase in setOf("requirements", "design") && status?.endsWith("_draft") == true
        approvePhaseButton.isEnabled = phase in setOf("requirements", "design", "plan") && status?.endsWith("_review") == true
        rejectPhaseButton.isEnabled = approvePhaseButton.isEnabled
        generateTasksButton.isEnabled = phase == "plan" && status == "plan_draft"
        refreshInitiativeButton.isEnabled = pendingPhaseAction != null
        val hasApproval = selectedApproval != null
        approveApprovalButton.isEnabled = hasApproval
        rejectApprovalButton.isEnabled = hasApproval
        val inlineApproval = WorkbenchStateMapper.buildApprovalSummary(currentApprovals, selectedTask?.taskId).selectedTaskApproval
        approvalCalloutPanel.isVisible = inlineApproval != null
        approvalCalloutLabel.text = inlineApproval?.let {
            "Task ${it.taskId} is blocked by approval '${it.actionType}'. Resolve it here without leaving the work view."
        } ?: ""
        taskMetaLabel.toolTipText = availability.blockingReason
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        currentProjectContext = context
        if (apiKey.isBlank()) {
            backendReady = null
            currentBridgeResolution = null
            currentBridges = emptyList()
            capabilitiesArea.text = "Configure the API key in Settings > StrateCode Agents."
            bridgeSummaryArea.text = "Configure the API key first. No bridge checks can run without it."
            bridgeCandidatesArea.text = ""
            refreshHeader()
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
                StatusBundle(ready.ready, bridges.items, bridge, buildCapabilitiesText(projectCaps?.mode, capabilities.capabilities, projectCaps?.capabilities.orEmpty()))
            }.onSuccess { bundle ->
                StrateCodeProjectStore.write(context, bridgeName = settings.currentState.bridgeName)
                SwingUtilities.invokeLater {
                    backendReady = bundle.backendReady
                    currentBridges = bundle.bridges
                    currentBridgeResolution = bundle.bridgeResolution
                    capabilitiesArea.text = bundle.capabilitiesText
                    bridgeSummaryArea.text = renderBridgeSummary(context, bundle.bridgeResolution)
                    bridgeCandidatesArea.text = renderBridgeCandidates(context.workspaceRoot, bundle.bridges)
                    refreshHeader()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    backendReady = null
                    currentBridges = emptyList()
                    currentBridgeResolution = null
                    capabilitiesArea.text = error.message ?: error.toString()
                    bridgeSummaryArea.text = error.message ?: error.toString()
                    bridgeCandidatesArea.text = ""
                    refreshHeader()
                    updateActionState()
                    notify("Status refresh failed", error.message ?: error.toString(), NotificationType.ERROR)
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
        val knownInitiativeIds = buildKnownInitiativeIds(context)
        if (knownInitiativeIds.isEmpty()) {
            currentInitiatives = emptyList()
            refreshInitiativeSelector(selectInitiativeId ?: context.metadata?.lastInitiativeId)
            selectedInitiative = null
            clearInitiativeState("No local initiatives registered in .stratecode/project.json.\n\nCreate a new initiative from the plugin or from editor selection to seed this workspace.")
            refreshHeader()
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).listInitiatives(context.workspaceRoot)
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    currentInitiatives = response.items.filter { it.id in knownInitiativeIds }
                    refreshInitiativeSelector(selectInitiativeId ?: selectedInitiative?.id ?: context.metadata?.lastInitiativeId)
                    if (currentInitiatives.isEmpty()) {
                        selectedInitiative = null
                        clearInitiativeState("No locally tracked initiatives are currently available on the server.")
                    }
                    refreshHeader()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    currentInitiatives = emptyList()
                    clearInitiativeState(error.message ?: error.toString())
                }
            }
        }
    }

    private fun refreshInitiativeSelector(selectInitiativeId: String?) {
        val detailById = initiativeDetailCache.toMap()
        val artifactCountById = initiativeArtifactCache.mapValues { it.value.size }
        val items = WorkbenchStateMapper.buildInitiatives(currentInitiatives, detailById, artifactCountById)
        initiativeSelectorModel.removeAllElements()
        items.forEach(initiativeSelectorModel::addElement)
        val target = items.firstOrNull { it.id == selectInitiativeId } ?: items.firstOrNull()
        if (target != null) {
            initiativeSelector.selectedItem = target
            selectedInitiative = currentInitiatives.firstOrNull { it.id == target.id }
            if (selectedInitiative != null && selectedDetailOrNull()?.initiative?.id != selectedInitiative?.id) {
                loadInitiativeDetail(selectedInitiative!!.id)
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
                InitiativeDetailBundle(
                    detail = client.getInitiativeDetail(initiativeId),
                    tasks = client.listInitiativeTasks(initiativeId).items,
                    artifacts = client.listInitiativeArtifacts(initiativeId).items,
                )
            }.onSuccess { bundle ->
                val context = ProjectContextResolver.resolve(project)
                StrateCodeProjectStore.rememberInitiative(
                    context,
                    initiativeId = bundle.detail.initiative.id,
                    initiativeTitle = bundle.detail.initiative.title,
                    bridgeName = settings.currentState.bridgeName,
                )
                SwingUtilities.invokeLater {
                    initiativeDetailCache[initiativeId] = bundle.detail
                    initiativeTaskCache[initiativeId] = bundle.tasks
                    initiativeArtifactCache[initiativeId] = bundle.artifacts
                    selectedInitiative = currentInitiatives.firstOrNull { it.id == initiativeId }
                    refreshInitiativeSelector(initiativeId)
                    rebuildTaskList()
                    renderInitiativeSnapshot()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearInitiativeState("Failed to load initiative detail:\n${error.message ?: error}")
                }
            }
        }
    }

    private fun rebuildTaskList() {
        val detail = selectedDetailOrNull()
        val tasks = detail?.initiative?.id?.let { initiativeTaskCache[it] }.orEmpty()
        val items = WorkbenchStateMapper.buildTaskItems(
            tasks = tasks,
            approvals = currentApprovals,
            patchByTaskId = taskPatchCache,
            evidenceByTaskId = taskEvidenceCache,
            statusFilter = statusFilterCombo.selectedItem?.toString() ?: "all",
            agentFilter = agentFilterCombo.selectedItem?.toString() ?: "all",
        )
        val selectedIds = taskList.selectedValuesList.map { it.taskId }.toSet()
        taskModel.clear()
        items.forEach(taskModel::addElement)
        if (selectedIds.isNotEmpty()) {
            val indices = items.mapIndexedNotNull { index, item -> if (item.taskId in selectedIds) index else null }.toIntArray()
            if (indices.isNotEmpty()) {
                taskList.setSelectedIndices(indices)
            }
        }
        if (taskList.selectedValuesList.isEmpty()) {
            renderInitiativeSnapshot()
        }
    }

    private fun handleTaskSelectionChanged() {
        val selectedTasks = taskList.selectedValuesList
        if (selectedTasks.size == 1) {
            loadTaskExecutionDetail(selectedTasks.first().taskId)
            return
        }
        selectedTaskDetail = null
        selectedTaskPatchView = null
        selectedTaskEvidence = null
        selectedTaskSourceArtifacts = emptyList()
        selectedEvidenceLocation = null
        selectedArtifact = null
        evidenceModel.clear()
        taskArtifactModel.clear()
        if (selectedTasks.isEmpty()) {
            renderInitiativeSnapshot()
        } else {
            renderMultiTaskSelection(selectedTasks)
        }
    }

    private fun loadTaskExecutionDetail(taskId: String) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            return
        }
        summaryArea.text = "Loading task detail…"
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
                val task = client.getTask(taskId)
                val sources = client.getTaskSources(taskId).items
                val patch = TaskExecutionSupport.resolvePatch(task, sources)
                val evidence = TaskExecutionSupport.extractEvidence(task, sources)
                TaskExecutionBundle(task, sources, patch, evidence)
            }.onSuccess { bundle ->
                SwingUtilities.invokeLater {
                    selectedTaskDetail = bundle.task
                    selectedTaskPatchView = bundle.patchView
                    selectedTaskEvidence = bundle.evidence
                    selectedTaskSourceArtifacts = bundle.sources
                    taskPatchCache[taskId] = bundle.patchView
                    taskEvidenceCache[taskId] = bundle.evidence
                    taskSourcesCache[taskId] = bundle.sources
                    renderTaskDetail(bundle.task, bundle.sources, bundle.patchView, bundle.evidence)
                    rebuildTaskList()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    selectedTaskDetail = null
                    selectedTaskPatchView = null
                    selectedTaskEvidence = null
                    selectedTaskSourceArtifacts = emptyList()
                    taskHeadlineLabel.text = "Task detail failed"
                    taskMetaLabel.text = error.message ?: error.toString()
                    summaryArea.text = "Failed to load task detail:\n${error.message ?: error}"
                    diffArea.text = "No diff available."
                    evidenceArea.text = "No evidence available."
                    artifactDetailArea.text = "No artifacts available."
                    evidenceModel.clear()
                    taskArtifactModel.clear()
                    updateActionState()
                }
            }
        }
    }

    private fun renderTaskDetail(
        task: TaskDetailRecord,
        artifacts: List<InitiativeArtifactRecord>,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult,
    ) {
        val selectedTask = taskList.selectedValuesList.singleOrNull()
        val approvalSummary = WorkbenchStateMapper.buildApprovalSummary(currentApprovals, selectedTask?.taskId)
        val viewState = TaskDetailViewState(
            taskId = task.id,
            title = selectedTask?.title ?: task.description,
            agent = selectedTask?.agent ?: "unknown",
            state = task.state,
            executionMode = selectedTask?.executionMode ?: "-",
            updatedAt = task.updatedAt,
            summaryText = renderTaskSummaryText(task, patchView, evidence),
            diffText = patchView?.diff ?: "No diff available for this task.",
            patchView = patchView,
            evidenceLocations = evidence.locations,
            evidenceDetailText = renderEvidenceDetail(null, evidence),
            artifacts = artifacts,
            artifactDetailText = if (artifacts.isEmpty()) "No task-scoped artifacts." else renderArtifactDetail(artifacts.first()),
            approvalCallout = approvalSummary.selectedTaskApproval?.let {
                "Task ${it.taskId} is blocked by approval '${it.actionType}'. Resolve it directly here."
            },
        )
        renderTaskViewState(viewState)
    }

    private fun renderTaskViewState(viewState: TaskDetailViewState) {
        taskHeadlineLabel.text = viewState.title
        taskMetaLabel.text = "${viewState.agent} · ${viewState.state} · ${viewState.executionMode} · ${viewState.updatedAt}"
        taskBadgesLabel.text = buildBadgesText(viewState.patchView != null, viewState.evidenceLocations.isNotEmpty(), viewState.approvalCallout != null, viewState.artifacts.isNotEmpty())
        summaryArea.text = viewState.summaryText
        diffArea.text = viewState.diffText
        evidenceModel.clear()
        viewState.evidenceLocations.forEach(evidenceModel::addElement)
        evidenceArea.text = viewState.evidenceDetailText
        taskArtifactModel.clear()
        viewState.artifacts.forEach(taskArtifactModel::addElement)
        selectedArtifact = viewState.artifacts.firstOrNull()
        artifactDetailArea.text = if (selectedArtifact != null) renderArtifactDetail(selectedArtifact) else "No task-scoped artifacts."
        approvalCalloutPanel.isVisible = viewState.approvalCallout != null
        approvalCalloutLabel.text = viewState.approvalCallout ?: ""
    }

    private fun renderInitiativeSnapshot() {
        val detail = selectedDetailOrNull()
        val artifacts = detail?.initiative?.id?.let { initiativeArtifactCache[it] }.orEmpty()
        if (detail == null) {
            taskHeadlineLabel.text = "No initiative selected"
            taskMetaLabel.text = "Create one or pick an existing initiative for this project."
            taskBadgesLabel.text = ""
            summaryArea.text = "No initiative loaded for this workspace."
            diffArea.text = "Select a task to inspect its diff."
            evidenceArea.text = "Select a task to inspect its evidence."
            artifactDetailArea.text = "Select a task to inspect its artifacts."
            initiativeInfoArea.text = "No initiative loaded."
            return
        }
        taskHeadlineLabel.text = detail.initiative.title
        taskMetaLabel.text = "${detail.initiative.status} · ${detail.initiative.currentPhase} · ${detail.executionSummary.taskCount} tasks"
        taskBadgesLabel.text = buildBadgeLine(
            badgeText("phase", detail.initiative.currentPhase),
            badgeText("status", detail.initiative.status),
            badgeText("artifacts", artifacts.size.toString()),
        )
        summaryArea.text = buildString {
            appendLine("Goal")
            appendLine("====")
            appendLine(detail.initiative.goal)
            appendLine()
            appendLine("Snapshot")
            appendLine("========")
            appendLine("Workspace: ${detail.initiative.workspaceRoot}")
            appendLine("Execution mode: ${detail.initiative.executionMode}")
            appendLine("Aggregated status: ${detail.executionSummary.aggregatedStatus}")
            appendLine("Pending manual: ${detail.executionSummary.pendingManual}")
            appendLine("Last review: ${detail.reviews.maxByOrNull { it.createdAt }?.let { "${it.phase} / ${it.decision}" } ?: "-"}")
            currentBridgeResolution?.executionBlockReason()?.let {
                appendLine()
                appendLine("Bridge block")
                appendLine("============")
                appendLine(it)
            }
        }.trim()
        diffArea.text = "Select a coder task to preview its diff."
        evidenceArea.text = "Select a reviewer task to inspect findings."
        artifactDetailArea.text = if (artifacts.isEmpty()) "No initiative artifacts generated yet." else renderArtifactDetail(artifacts.first())
        initiativeInfoArea.text = renderInitiativeInfo(detail, artifacts)
        evidenceModel.clear()
        taskArtifactModel.clear()
        approvalCalloutPanel.isVisible = false
    }

    private fun renderMultiTaskSelection(selectedTasks: List<TaskWorkbenchItem>) {
        taskHeadlineLabel.text = "${selectedTasks.size} tasks selected"
        taskMetaLabel.text = "Bulk launch is enabled; diff, evidence and patch stay single-task."
        taskBadgesLabel.text = buildBadgeLine(badgeText("selection", selectedTasks.size.toString()))
        summaryArea.text = buildString {
            appendLine("${selectedTasks.size} tasks selected")
            appendLine("======================")
            selectedTasks.forEach { task ->
                appendLine("#${task.launchOrder} ${task.title}")
                appendLine("${task.agent} · ${task.state} · ${task.executionMode} · ${task.executionTarget}")
                appendLine()
            }
        }.trim()
        diffArea.text = "Preview Diff requires a single selected task."
        evidenceArea.text = "Evidence requires a single selected task."
        artifactDetailArea.text = "Artifacts require a single selected task."
        evidenceModel.clear()
        taskArtifactModel.clear()
        approvalCalloutPanel.isVisible = false
    }

    private fun clearInitiativeState(message: String) {
        taskModel.clear()
        evidenceModel.clear()
        taskArtifactModel.clear()
        selectedTaskDetail = null
        selectedTaskPatchView = null
        selectedTaskEvidence = null
        selectedTaskSourceArtifacts = emptyList()
        selectedArtifact = null
        taskHeadlineLabel.text = "No initiative selected"
        taskMetaLabel.text = message
        taskBadgesLabel.text = ""
        summaryArea.text = message
        diffArea.text = "Select a task to inspect its diff."
        evidenceArea.text = "Select a task to inspect its evidence."
        artifactDetailArea.text = "Select a task to inspect its artifacts."
        initiativeInfoArea.text = message
        approvalCalloutPanel.isVisible = false
        updateActionState()
    }

    private fun refreshHeader() {
        val context = currentProjectContext ?: return
        val state = WorkbenchStateMapper.buildHeaderState(context, backendReady, currentBridgeResolution, currentApprovals.size)
        applyHeaderState(state)
    }

    private fun applyHeaderState(state: HeaderStatusViewState) {
        projectBadge.text = "Project  ${state.projectName}"
        backendBadge.text = "Backend  ${state.backendLabel}"
        bridgeBadge.text = "Bridge  ${state.bridgeLabel}"
        approvalsBadge.text = "Approvals  ${state.approvalsLabel}"
        setBadgeTone(projectBadge, if (state.degraded) StatusTone.WARNING else StatusTone.NEUTRAL)
        setBadgeTone(backendBadge, state.backendTone)
        setBadgeTone(bridgeBadge, state.bridgeTone)
        setBadgeTone(approvalsBadge, state.approvalsTone)
        subtitleLabel.text = """
            <html>
            <div style='width:960px'>
            <b>Workspace:</b> ${escapeHtml(state.workspaceRoot)}<br/>
            <b>Repository:</b> ${escapeHtml(state.repositoryUrl ?: "degraded")}<br/>
            <b>Local state:</b> ${escapeHtml(state.metadataSummary)}
            </div>
            </html>
        """.trimIndent()
    }

    private fun createInitiative() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        if (apiKey.isBlank()) {
            notify("Initiative blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        val form = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(JLabel("Title"))
            val titleField = JBTextField("IDE initiative for ${context.projectName}")
            add(titleField)
            add(Box.createVerticalStrut(8))
            add(JLabel("Goal"))
            val goalArea = JBTextArea(6, 48).apply {
                lineWrap = true
                wrapStyleWord = true
                border = JBUI.Borders.empty(6)
            }
            add(JBScrollPane(goalArea))
            putClientProperty("titleField", titleField)
            putClientProperty("goalArea", goalArea)
        }
        val result = JOptionPane.showConfirmDialog(this, form, "Create Initiative", JOptionPane.OK_CANCEL_OPTION, JOptionPane.PLAIN_MESSAGE)
        if (result != JOptionPane.OK_OPTION) {
            return
        }
        val title = (form.getClientProperty("titleField") as JBTextField).text.trim().ifBlank { "IDE initiative for ${context.projectName}" }
        val goal = (form.getClientProperty("goalArea") as JBTextArea).text.trim()
        if (goal.isBlank()) {
            notify("Initiative blocked", "Goal is required.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).createInitiative(title, goal, context.workspaceRoot)
            }.onSuccess {
                StrateCodeProjectStore.rememberInitiative(
                    context,
                    initiativeId = it.id,
                    initiativeTitle = it.title,
                    bridgeName = settings.currentState.bridgeName,
                )
                SwingUtilities.invokeLater {
                    notify("Initiative created", "${it.title} (${it.id})", NotificationType.INFORMATION)
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

    private fun resetWorkspaceState() {
        val context = ProjectContextResolver.resolve(project)
        val confirmed = JOptionPane.showConfirmDialog(
            this,
            "Delete .stratecode/project.json and clear locally tracked initiatives for this workspace?",
            "Reset Local State",
            JOptionPane.OK_CANCEL_OPTION,
            JOptionPane.WARNING_MESSAGE,
        )
        if (confirmed != JOptionPane.OK_OPTION) {
            return
        }
        StrateCodeProjectStore.clear(context.workspaceRoot)
        initiativeDetailCache.clear()
        initiativeTaskCache.clear()
        initiativeArtifactCache.clear()
        taskPatchCache.clear()
        taskEvidenceCache.clear()
        taskSourcesCache.clear()
        currentInitiatives = emptyList()
        selectedInitiative = null
        selectedTaskDetail = null
        selectedTaskPatchView = null
        selectedTaskEvidence = null
        selectedTaskSourceArtifacts = emptyList()
        selectedArtifact = null
        selectedApproval = null
        selectedEvidenceLocation = null
        currentProjectContext = ProjectContextResolver.resolve(project)
        refreshHeader()
        clearInitiativeState("Local workspace state reset.\n\nCreate a new initiative to repopulate this workspace.")
        notify("Local state reset", "The workspace metadata has been cleared.", NotificationType.INFORMATION)
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
            notify("Bridge smoke passed", "Bridge ${resolution.matched?.name ?: "-"} is executable for ${context.workspaceRoot}.", NotificationType.INFORMATION)
        } else {
            notify("Bridge smoke failed", problem, NotificationType.WARNING)
        }
    }

    private fun buildKnownInitiativeIds(context: ProjectContext): Set<String> =
        buildSet {
            context.metadata?.knownInitiatives?.forEach { add(it.id) }
            context.metadata?.lastInitiativeId?.let { add(it) }
        }

    private fun loadApprovals() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            approvalsArea.text = "Configure an API key first."
            approvalsModel.clear()
            currentApprovals = emptyList()
            selectedApproval = null
            refreshHeader()
            updateActionState()
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                OrchestratorClient(settings.currentState.baseUrl, apiKey).listApprovals()
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    currentApprovals = response.items
                    approvalsModel.clear()
                    response.items.forEach(approvalsModel::addElement)
                    approvalsArea.text = if (response.items.isEmpty()) "No pending approvals." else renderApprovalDetail(response.items.first())
                    selectedApproval = response.items.firstOrNull()
                    refreshHeader()
                    rebuildTaskList()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    approvalsArea.text = error.message ?: error.toString()
                    approvalsModel.clear()
                    currentApprovals = emptyList()
                    selectedApproval = null
                    refreshHeader()
                    updateActionState()
                }
            }
        }
    }

    private fun resolveSelectedApproval(approve: Boolean) {
        val approval = selectedApproval ?: return
        resolveApproval(approval, approve)
    }

    private fun resolveBlockingApproval(approve: Boolean) {
        val selectedTaskId = taskList.selectedValuesList.singleOrNull()?.taskId ?: return
        val approval = currentApprovals.firstOrNull { it.taskId == selectedTaskId } ?: return
        resolveApproval(approval, approve)
    }

    private fun resolveApproval(approval: ApprovalRecord, approve: Boolean) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            notify("Approval blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = OrchestratorClient(settings.currentState.baseUrl, apiKey)
                if (approve) client.approveApproval(approval.id, "jetbrains-plugin") else client.rejectApproval(approval.id, "jetbrains-plugin")
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

    private fun advanceSelectedInitiative() {
        val detail = selectedDetailOrNull() ?: return
        val feedback = promptFeedback("Advance ${detail.initiative.currentPhase} draft") ?: return
        runInitiativeMutation("Draft advanced") { client -> client.advanceInitiative(detail.initiative.id, feedback) }
    }

    private fun resolveSelectedInitiative(approve: Boolean) {
        val detail = selectedDetailOrNull() ?: return
        val phase = detail.initiative.currentPhase
        val feedback = promptFeedback("${if (approve) "Approve" else "Reject"} $phase") ?: return
        runInitiativeMutation(if (approve) "Phase approved" else "Phase rejected") { client ->
            if (approve) {
                client.approveInitiativePhase(detail.initiative.id, phase, "jetbrains-plugin", feedback)
            } else {
                client.rejectInitiativePhase(detail.initiative.id, phase, "jetbrains-plugin", feedback)
            }
        }
    }

    private fun generateSelectedInitiativeTasks() {
        val detail = selectedDetailOrNull() ?: return
        val feedback = promptFeedback("Generate task backlog", allowEmpty = true) ?: return
        runInitiativeMutation("Task backlog generated") { client ->
            client.generateInitiativeTasks(detail.initiative.id, feedback).initiative
        }
    }

    private fun setSelectedTaskMode() {
        val selectedTask = taskList.selectedValuesList.singleOrNull() ?: return
        val choice = JOptionPane.showInputDialog(
            this,
            "Execution mode for selected task:",
            selectedTask.executionMode,
        )?.trim().orEmpty()
        if (choice !in setOf("manual", "agent_local", "agent_remote")) {
            if (choice.isNotBlank()) {
                notify("Invalid execution mode", "Use manual, agent_local, or agent_remote.", NotificationType.WARNING)
            }
            return
        }
        val detail = selectedDetailOrNull() ?: return
        runInitiativeMutation("Task mode updated") { client ->
            client.updateInitiativeTaskMode(detail.initiative.id, selectedTask.taskId, choice)
        }
    }

    private fun launchSelectedTasks() {
        val detail = selectedDetailOrNull() ?: return
        val selectedTasks = taskList.selectedValuesList
        if (selectedTasks.isEmpty()) {
            return
        }
        val resolution = currentBridgeResolution
        val blockReason = if (resolution == null) "No bridge state is loaded for this project." else resolution.executionBlockReason()
        if (blockReason != null) {
            notify("Launch blocked", blockReason, NotificationType.WARNING)
            loadStatus()
            return
        }
        runInitiativeMutation("Task launch queued") { client ->
            client.launchInitiativeTasks(detail.initiative.id, selectedTasks.map { it.taskId })
        }
    }

    private fun previewSelectedTaskDiff() {
        val patchView = selectedTaskPatchView ?: return
        val context = currentProjectContext ?: return
        TaskExecutionSupport.previewPatch(project, context.workspaceRoot, patchView)
    }

    private fun applySelectedTaskPatch() {
        val context = currentProjectContext ?: return
        val patchView = selectedTaskPatchView ?: return
        if (context.degraded || context.repositoryUrl.isNullOrBlank()) {
            notify("Patch blocked", "The project is in degraded mode or missing repository_url.", NotificationType.WARNING)
            return
        }
        val resolution = currentBridgeResolution
        val blockReason = if (resolution == null) "No bridge state is loaded for this project." else resolution.executionBlockReason()
        if (blockReason != null) {
            notify("Patch blocked", blockReason, NotificationType.WARNING)
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                TaskExecutionSupport.applyPatch(context.workspaceRoot, patchView)
            }.onSuccess { result ->
                SwingUtilities.invokeLater {
                    notify("Patch applied", patchView.summary ?: "Patch applied successfully.", NotificationType.INFORMATION)
                    TaskExecutionSupport.openChangedFile(project, context.workspaceRoot, result.changedFiles)
                    loadStatus()
                    selectedInitiative?.let { loadInitiativeDetail(it.id) }
                    taskList.selectedValuesList.singleOrNull()?.let { loadTaskExecutionDetail(it.taskId) }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Patch apply failed", error.message ?: error.toString(), NotificationType.ERROR)
                    summaryArea.text = buildString {
                        appendLine(summaryArea.text)
                        appendLine()
                        appendLine("Patch apply error")
                        appendLine("=================")
                        appendLine(error.message ?: error.toString())
                    }.trim()
                }
            }
        }
    }

    private fun openSelectedTaskChangedFile() {
        val context = currentProjectContext ?: return
        val patchView = selectedTaskPatchView ?: return
        if (!TaskExecutionSupport.openChangedFile(project, context.workspaceRoot, patchView.changedFiles)) {
            notify("Open changed file failed", "No changed file could be opened from this task.", NotificationType.WARNING)
        }
    }

    private fun openSelectedTaskEvidence() {
        val context = currentProjectContext ?: return
        val target = selectedEvidenceLocation ?: evidenceModel.getElementAtOrNull(0) ?: return
        if (!TaskExecutionSupport.openAtLocation(project, context.workspaceRoot, target)) {
            notify("Open evidence failed", "The referenced file or line could not be resolved in this workspace.", NotificationType.WARNING)
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
                    loadInitiatives(selectInitiativeId = selected.id)
                    loadInitiativeDetail(selected.id)
                    loadApprovals()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Initiative action failed", error.message ?: error.toString(), NotificationType.ERROR)
                }
            }
        }
    }

    private fun promptFeedback(title: String, allowEmpty: Boolean = false): String? {
        val area = JBTextArea(8, 48).apply {
            lineWrap = true
            wrapStyleWord = true
            border = JBUI.Borders.empty(8)
        }
        val result = JOptionPane.showConfirmDialog(this, JBScrollPane(area), title, JOptionPane.OK_CANCEL_OPTION, JOptionPane.PLAIN_MESSAGE)
        if (result != JOptionPane.OK_OPTION) {
            return null
        }
        val feedback = area.text.trim()
        if (!allowEmpty && feedback.isBlank()) {
            return ""
        }
        return feedback
    }

    private fun selectedDetailOrNull(): InitiativeDetailResponseRecord? =
        selectedInitiative?.id?.let { initiativeDetailCache[it] }

    private fun renderTaskSummaryText(
        task: TaskDetailRecord,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult,
    ): String = buildString {
        appendLine("Task")
        appendLine("====")
        appendLine("ID: ${task.id}")
        appendLine("State: ${task.state}")
        appendLine("Updated: ${task.updatedAt}")
        appendLine("Workspace path: ${task.workspacePath ?: "-"}")
        appendLine()
        appendLine("Patch")
        appendLine("=====")
        if (patchView == null) {
            appendLine("No applicable patch found.")
        } else {
            appendLine("Source: ${patchView.sourceType}")
            appendLine("Changed files: ${patchView.changedFiles.size}")
            patchView.summary?.let { appendLine("Summary: $it") }
            if (patchView.changedFiles.isNotEmpty()) {
                appendLine()
                patchView.changedFiles.forEach { appendLine("- $it") }
            }
        }
        appendLine()
        appendLine("Evidence")
        appendLine("========")
        appendLine("Navigable findings: ${evidence.locations.size}")
        appendLine("Raw artifacts without coordinates: ${evidence.rawArtifacts.size}")
        if (evidence.errors.isNotEmpty()) {
            appendLine("Parse errors: ${evidence.errors.size}")
            evidence.errors.forEach { appendLine("- $it") }
        }
        task.errorMessage?.takeIf { it.isNotBlank() }?.let {
            appendLine()
            appendLine("Task error")
            appendLine("==========")
            appendLine(it)
        }
    }.trim()

    private fun renderEvidenceDetail(
        selected: EvidenceLocation?,
        evidence: EvidenceExtractionResult?,
    ): String {
        if (selected != null) {
            return buildString {
                appendLine("File: ${selected.file}")
                appendLine("Line: ${selected.line}")
                selected.column?.let { appendLine("Column: $it") }
                appendLine("Source: ${selected.sourceType}")
                appendLine("Severity: ${selected.severity ?: "-"}")
                appendLine()
                appendLine(selected.message ?: "No message.")
            }.trim()
        }
        if (evidence == null) {
            return "Select a reviewer task to inspect its evidence."
        }
        return buildString {
            appendLine("Navigable findings: ${evidence.locations.size}")
            appendLine("Raw artifacts: ${evidence.rawArtifacts.size}")
            if (evidence.errors.isNotEmpty()) {
                appendLine()
                appendLine("Parse errors")
                appendLine("============")
                evidence.errors.forEach { appendLine("- $it") }
            }
            if (evidence.locations.isNotEmpty()) {
                appendLine()
                appendLine("Double click a finding or use 'Open First Evidence'.")
            }
            if (evidence.rawArtifacts.isNotEmpty()) {
                appendLine()
                appendLine("Raw artifact preview")
                appendLine("====================")
                evidence.rawArtifacts.take(2).forEach { artifact ->
                    appendLine("[${artifact.artifactType}] ${artifact.title ?: artifact.id}")
                    appendLine((artifact.contentText ?: "(no textual content)").take(800))
                    appendLine()
                }
            }
        }.trim()
    }

    private fun renderArtifactDetail(artifact: InitiativeArtifactRecord?): String {
        if (artifact == null) {
            return "No artifact selected."
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
        }.trim()
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
        }.trim()
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
            resolution.matched?.let {
                appendLine("Matched bridge")
                appendLine("==============")
                appendLine("ID: ${it.id}")
                appendLine("Name: ${it.name}")
                appendLine("Host: ${it.hostname}")
                appendLine("Workspace: ${it.workspaceRoot}")
                appendLine("Last heartbeat: ${it.lastHeartbeat ?: "-"}")
            }
            resolution.executionBlockReason()?.let {
                appendLine()
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
                val marker = if (bridge.workspaceRoot == workspaceRoot) "*" else "-"
                appendLine("$marker ${bridge.name} [${bridge.status}]")
                appendLine("  workspace=${bridge.workspaceRoot}")
                appendLine("  host=${bridge.hostname}")
                appendLine("  heartbeat=${bridge.lastHeartbeat ?: "<never>"}")
                appendLine()
            }
        }.trim()
    }

    private fun renderInitiativeInfo(
        detail: InitiativeDetailResponseRecord,
        artifacts: List<InitiativeArtifactRecord>,
    ): String = buildString {
        appendLine("Title: ${detail.initiative.title}")
        appendLine("Status: ${detail.initiative.status}")
        appendLine("Current phase: ${detail.initiative.currentPhase}")
        appendLine("Execution mode: ${detail.initiative.executionMode}")
        appendLine("Workspace: ${detail.initiative.workspaceRoot}")
        appendLine("Updated: ${detail.initiative.updatedAt}")
        appendLine()
        appendLine("Snapshot")
        appendLine("========")
        appendLine("Task count: ${detail.executionSummary.taskCount}")
        appendLine("Pending manual: ${detail.executionSummary.pendingManual}")
        appendLine("Artifacts: ${artifacts.size}")
        appendLine("Aggregated status: ${detail.executionSummary.aggregatedStatus}")
        appendLine("Last review: ${detail.reviews.maxByOrNull { it.createdAt }?.let { "${it.phase} / ${it.decision}" } ?: "-"}")
        appendLine()
        appendLine("Goal")
        appendLine("====")
        appendLine(detail.initiative.goal)
    }.trim()

    private fun buildCapabilitiesText(
        mode: String?,
        candidates: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
        projectCapabilities: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
    ): String = buildString {
        appendLine("Discovery candidates")
        appendLine("===================")
        if (candidates.isEmpty()) {
            appendLine("No capability candidates returned.")
        } else {
            candidates.forEach {
                appendLine("• ${it.name}  [${it.kind}]  score=${"%.1f".format(it.score)}")
                appendLine("  tags: ${it.capabilityTags.joinToString(", ")}")
            }
        }
        appendLine()
        appendLine("Project effective policy")
        appendLine("========================")
        appendLine("mode: ${mode ?: "n/a"}")
        if (projectCapabilities.isEmpty()) {
            appendLine("No project-scoped reviewer capabilities returned.")
        } else {
            projectCapabilities.forEach {
                appendLine("• ${it.name}  [${it.kind}]  score=${"%.1f".format(it.score)}")
                appendLine("  tags: ${it.capabilityTags.joinToString(", ")}")
            }
        }
    }.trim()

    private fun buildBadgesText(hasDiff: Boolean, hasEvidence: Boolean, hasApproval: Boolean, hasArtifacts: Boolean): String =
        buildBadgeLine(
            badgeText("diff", if (hasDiff) "ready" else "none"),
            badgeText("evidence", if (hasEvidence) "ready" else "none"),
            badgeText("approval", if (hasApproval) "required" else "clear"),
            badgeText("artifacts", if (hasArtifacts) "present" else "none"),
        )

    private fun buildBadgeLine(vararg badges: String): String =
        badges.joinToString("  ")

    private fun badgeText(label: String, value: String): String = "[$label: $value]"

    private fun badge(title: String, value: String, tone: StatusTone): JLabel =
        JLabel("$title  $value", SwingConstants.CENTER).apply {
            isOpaque = true
            foreground = Color.WHITE
            border = JBUI.Borders.empty(6, 10)
            font = font.deriveFont(Font.BOLD, 12f)
            setBadgeTone(this, tone)
        }

    private fun setBadgeTone(label: JLabel, tone: StatusTone) {
        label.background = when (tone) {
            StatusTone.HEALTHY -> Color(0x1F7A4C)
            StatusTone.WARNING -> Color(0x8B5E3C)
            StatusTone.DANGER -> Color(0x8B2E2E)
            StatusTone.NEUTRAL -> Color(0x5B6B7A)
        }
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

    private fun notify(title: String, message: String, type: NotificationType) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("StrateCode Agents")
            .createNotification(title, message, type)
            .notify(project)
    }

    private fun settings(): PluginSettingsService =
        ApplicationManager.getApplication().getService(PluginSettingsService::class.java)

    private fun escapeHtml(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

    private data class StatusBundle(
        val backendReady: Boolean,
        val bridges: List<LocalBridgeResponse>,
        val bridgeResolution: BridgeResolution,
        val capabilitiesText: String,
    )

    private data class InitiativeDetailBundle(
        val detail: InitiativeDetailResponseRecord,
        val tasks: List<InitiativeTaskLinkRecord>,
        val artifacts: List<InitiativeArtifactRecord>,
    )

    private data class TaskExecutionBundle(
        val task: TaskDetailRecord,
        val sources: List<InitiativeArtifactRecord>,
        val patchView: TaskResultPatchView?,
        val evidence: EvidenceExtractionResult,
    )
}

private class InitiativeWorkbenchItemRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is InitiativeWorkbenchItem && it is JLabel) {
            it.border = JBUI.Borders.empty(6)
            it.text = """
                <html>
                <b>${escape(value.title)}</b>
                <span style='color:#6B7280'> · ${escape(value.status)} / ${escape(value.currentPhase)} / ${value.taskCount} tasks</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}

private class TaskWorkbenchItemCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is TaskWorkbenchItem && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            val badges = buildString {
                if (value.diffAvailable) append(" diff")
                if (value.evidenceAvailable) append(" evidence")
                if (value.approvalRequired) append(" approval")
            }.trim()
            it.text = """
                <html>
                <b>#${value.launchOrder} ${escape(value.title)}</b><br/>
                <span style='color:#6B7280'>${escape(value.agent)} / ${escape(value.state)} / ${escape(value.executionMode)}${if (badges.isNotBlank()) " / ${escape(badges)}" else ""}</span>
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

private class EvidenceLocationCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is EvidenceLocation && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            it.text = """
                <html>
                <b>${escape(value.file)}:${value.line}</b><br/>
                <span style='color:#6B7280'>${escape(value.severity ?: value.sourceType)}${value.message?.let { msg -> " / ${escape(msg)}" } ?: ""}</span>
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

private fun <T> DefaultListModel<T>.getElementAtOrNull(index: Int): T? =
    if (index in 0 until size()) getElementAt(index) else null
