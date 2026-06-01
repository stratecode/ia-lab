package com.stratecode.lab.jetbrains.ui

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.application.PathManager
import com.intellij.openapi.ide.CopyPasteManager
import com.intellij.openapi.diagnostic.Logger
import com.intellij.openapi.fileEditor.FileEditorManager
import com.intellij.openapi.fileEditor.OpenFileDescriptor
import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.LocalFileSystem
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
import com.stratecode.lab.jetbrains.bridge.shouldAttemptAutoRegister
import com.stratecode.lab.jetbrains.client.ApprovalRecord
import com.stratecode.lab.jetbrains.client.InitiativeArtifactRecord
import com.stratecode.lab.jetbrains.client.InitiativeDetailResponseRecord
import com.stratecode.lab.jetbrains.client.InitiativeRecord
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
import com.stratecode.lab.jetbrains.workbench.PlanStepKind
import com.stratecode.lab.jetbrains.workbench.PlanStepStatus
import com.stratecode.lab.jetbrains.workbench.PlanStepWorkbenchItem
import com.stratecode.lab.jetbrains.workbench.StatusTone
import com.stratecode.lab.jetbrains.workbench.TaskActionAvailability
import com.stratecode.lab.jetbrains.workbench.TaskDetailViewState
import com.stratecode.lab.jetbrains.workbench.WorkbenchStateMapper
import java.awt.BorderLayout
import java.awt.CardLayout
import java.awt.Color
import java.awt.Dimension
import java.awt.FlowLayout
import java.awt.Font
import java.awt.datatransfer.StringSelection
import java.io.File
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.ArrayDeque
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
        LOGS,
    }

    companion object {
        private val LOG = Logger.getInstance(AgentsToolWindowPanel::class.java)
        private val logTimeFormatter: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss").withZone(ZoneId.systemDefault())
        private const val autoRegisterCooldownMillis: Long = 30_000
    }

    private val titleLabel = JLabel("StrateCode Plan")
    private val goalLabel = JLabel("No active goal")
    private val contextLabel = JLabel("Open or create a local initiative for this workspace.")
    private val operationLabel = JLabel("Idle")
    private val projectBadge = badge("Project", "local", StatusTone.NEUTRAL)
    private val backendBadge = badge("Backend", "unknown", StatusTone.NEUTRAL)
    private val bridgeBadge = badge("Bridge", "unresolved", StatusTone.NEUTRAL)
    private val approvalsBadge = badge("Approvals", "0 pending", StatusTone.NEUTRAL)

    private val refreshButton = JButton("Refresh")
    private val createInitiativeButton = JButton("Create Goal")
    private val resetWorkspaceButton = JButton("Reset Local State")
    private val approvalsDrawerButton = JButton("Approvals")
    private val bridgeDrawerButton = JButton("Bridge")
    private val capabilitiesDrawerButton = JButton("Capabilities")
    private val initiativeDrawerButton = JButton("Raw Initiative")
    private val logsDrawerButton = JButton("Logs")

    private val initiativeSelectorModel = DefaultComboBoxModel<InitiativeWorkbenchItem>()
    private val initiativeSelector = JComboBox(initiativeSelectorModel).apply {
        renderer = InitiativeWorkbenchItemRenderer()
    }
    private val statusFilterCombo = JComboBox(arrayOf("all", "pending", "running", "waiting_approval", "completed"))
    private val agentFilterCombo = JComboBox(arrayOf("all", "planner", "researcher", "coder", "reviewer"))

    private val planModel = DefaultListModel<PlanStepWorkbenchItem>()
    private val planList = JBList(planModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = PlanStepWorkbenchItemCellRenderer()
    }

    private val stepTitleLabel = JLabel("No plan step selected")
    private val stepMetaLabel = JLabel("Pick a goal or create one. The current step will appear here.")
    private val stepBadgesLabel = JLabel("")

    private val approvalCalloutPanel = JPanel(BorderLayout())
    private val approvalCalloutLabel = JLabel("")
    private val approveInlineButton = JButton("Approve")
    private val rejectInlineButton = JButton("Reject")

    private val advanceButton = JButton("Advance Draft")
    private val approvePhaseButton = JButton("Approve Phase")
    private val rejectPhaseButton = JButton("Reject Phase")
    private val generateTasksButton = JButton("Generate Tasks")
    private val setModeButton = JButton("Set Mode")
    private val launchButton = JButton("Run")
    private val previewDiffButton = JButton("Preview Diff")
    private val applyPatchButton = JButton("Apply Patch")
    private val openChangedFileButton = JButton("Open Changed File")
    private val openEvidenceButton = JButton("Open First Evidence")

    private val overviewArea = infoArea(16)
    private val outputArea = infoArea(14)
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
    private val diagnosticsArea = infoArea(14)
    private val refreshInitiativeButton = JButton("Refresh Goal")
    private val openIdeLogButton = JButton("Open idea.log")
    private val openWorkspaceLogButton = JButton("Open workspace log")
    private val refreshLogsButton = JButton("Refresh Logs")
    private val copyLogsButton = JButton("Copy Logs")

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
    private var currentPlanSteps: List<PlanStepWorkbenchItem> = emptyList()
    private var selectedInitiative: InitiativeSummary? = null
    private var selectedStep: PlanStepWorkbenchItem? = null
    private var selectedTaskDetail: TaskDetailRecord? = null
    private var selectedTaskPatchView: TaskResultPatchView? = null
    private var selectedTaskEvidence: EvidenceExtractionResult? = null
    private var selectedTaskSourceArtifacts: List<InitiativeArtifactRecord> = emptyList()
    private var selectedArtifact: InitiativeArtifactRecord? = null
    private var selectedApproval: ApprovalRecord? = null
    private var selectedEvidenceLocation: EvidenceLocation? = null
    private val recentDiagnostics: ArrayDeque<String> = ArrayDeque()
    private val autoRegisterAttempts: MutableMap<String, Long> = linkedMapOf()
    private var suppressInitiativeSelectionEvents: Boolean = false
    private var suppressPlanSelectionEvents: Boolean = false
    private var loadingInitiativeDetailId: String? = null
    private var loadingTaskId: String? = null
    private var currentOperationStatus: String? = null

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
        goalLabel.font = goalLabel.font.deriveFont(Font.BOLD, 16f)
        contextLabel.foreground = Color(0x6B7280)
        operationLabel.foreground = Color(0xA16207)
        operationLabel.font = operationLabel.font.deriveFont(Font.BOLD, 12f)

        val primaryActions = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(refreshButton)
            add(createInitiativeButton)
            add(resetWorkspaceButton)
        }
        val supportActions = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(approvalsDrawerButton)
            add(bridgeDrawerButton)
            add(capabilitiesDrawerButton)
            add(initiativeDrawerButton)
            add(logsDrawerButton)
        }
        val badges = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(projectBadge)
            add(backendBadge)
            add(bridgeBadge)
            add(approvalsBadge)
        }
        val content = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            isOpaque = false
            add(titleLabel)
            add(Box.createVerticalStrut(8))
            add(goalLabel)
            add(Box.createVerticalStrut(6))
            add(contextLabel)
            add(Box.createVerticalStrut(4))
            add(operationLabel)
            add(Box.createVerticalStrut(8))
            add(primaryActions)
            add(Box.createVerticalStrut(6))
            add(supportActions)
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
            minimumSize = Dimension(220, 120)
            add(JLabel("Goal"))
            add(Box.createVerticalStrut(4))
            initiativeSelector.maximumSize = Dimension(Int.MAX_VALUE, initiativeSelector.preferredSize.height)
            add(initiativeSelector)
            add(Box.createVerticalStrut(10))
            add(JLabel("Plan filters"))
            add(Box.createVerticalStrut(4))
            add(JPanel().apply {
                layout = BoxLayout(this, BoxLayout.X_AXIS)
                isOpaque = false
                statusFilterCombo.maximumSize = Dimension(160, statusFilterCombo.preferredSize.height)
                agentFilterCombo.maximumSize = Dimension(160, agentFilterCombo.preferredSize.height)
                add(statusFilterCombo)
                add(Box.createHorizontalStrut(8))
                add(agentFilterCombo)
            })
        }

        val left = section(
            "Plan",
            JPanel(BorderLayout()).apply {
                add(leftControls, BorderLayout.NORTH)
                add(JBScrollPane(planList), BorderLayout.CENTER)
            },
        ).apply {
            minimumSize = Dimension(250, 240)
        }

        val detailHeader = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            isOpaque = false
            add(stepTitleLabel)
            add(Box.createVerticalStrut(4))
            add(stepMetaLabel)
            add(Box.createVerticalStrut(4))
            add(stepBadgesLabel)
        }

        val phaseActions = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(advanceButton)
            add(approvePhaseButton)
            add(rejectPhaseButton)
            add(generateTasksButton)
        }
        val taskActions = JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
            isOpaque = false
            add(setModeButton)
            add(launchButton)
            add(previewDiffButton)
            add(applyPatchButton)
            add(openChangedFileButton)
            add(openEvidenceButton)
        }
        val detailTop = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            isOpaque = false
            border = JBUI.Borders.emptyBottom(8)
            add(detailHeader)
            add(Box.createVerticalStrut(8))
            add(approvalCalloutPanel)
            add(Box.createVerticalStrut(8))
            add(phaseActions)
            add(Box.createVerticalStrut(6))
            add(taskActions)
        }

        val right = JPanel(BorderLayout()).apply {
            add(detailTop, BorderLayout.NORTH)
            add(detailTabs, BorderLayout.CENTER)
            add(drawerWrapper, BorderLayout.SOUTH)
        }

        return JBSplitter(false, 0.28f).apply {
            border = JBUI.Borders.emptyTop(12)
            dividerWidth = 10
            firstComponent = left
            secondComponent = right
        }
    }

    private fun buildDetailTabs() {
        detailTabs.addTab("Overview", section("Plan Step", JBScrollPane(overviewArea)))
        detailTabs.addTab("Output", section("Execution Output", JBScrollPane(outputArea)))
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
                firstComponent = section("Relevant Artifacts", JBScrollPane(taskArtifactList))
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
        drawerCards.add(buildLogsDrawer(), DrawerMode.LOGS.name)
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
        drawerWrapper.preferredSize = Dimension(100, 220)
    }

    private fun buildApprovalsDrawer(): JComponent {
        approvalsList.addListSelectionListener {
            selectedApproval = approvalsList.selectedValue
            approvalsArea.text = renderApprovalDetail(selectedApproval)
            updateActionState()
        }
        return JPanel(BorderLayout()).apply {
            add(
                JBSplitter(false, 0.38f).apply {
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
                },
                BorderLayout.SOUTH,
            )
        }

    private fun buildLogsDrawer(): JComponent =
        JPanel(BorderLayout()).apply {
            add(section("Plugin Diagnostics", JBScrollPane(diagnosticsArea)), BorderLayout.CENTER)
            add(
                JPanel(FlowLayout(FlowLayout.LEFT, 8, 0)).apply {
                    isOpaque = false
                    add(refreshLogsButton)
                    add(copyLogsButton)
                    add(openWorkspaceLogButton)
                    add(openIdeLogButton)
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
        logsDrawerButton.addActionListener { showDrawer(DrawerMode.LOGS) }
        closeDrawerButton.addActionListener { showDrawer(DrawerMode.NONE) }
        initiativeSelector.addActionListener {
            if (suppressInitiativeSelectionEvents) {
                return@addActionListener
            }
            val selected = initiativeSelector.selectedItem as? InitiativeWorkbenchItem ?: return@addActionListener
            if (selected.id != selectedInitiative?.id) {
                currentInitiatives.firstOrNull { it.id == selected.id }?.let {
                    selectedInitiative = it
                    loadInitiativeDetail(it.id)
                }
            }
        }
        statusFilterCombo.addActionListener { rebuildPlanList() }
        agentFilterCombo.addActionListener { rebuildPlanList() }
        planList.addListSelectionListener {
            if (suppressPlanSelectionEvents) {
                return@addListSelectionListener
            }
            handlePlanSelectionChanged()
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
        refreshLogsButton.addActionListener { diagnosticsArea.text = renderDiagnostics() }
        copyLogsButton.addActionListener { copyDiagnostics() }
        openWorkspaceLogButton.addActionListener { openWorkspaceDiagnostics() }
        openIdeLogButton.addActionListener { openIdeLog() }
        advanceButton.addActionListener { advanceSelectedInitiative() }
        approvePhaseButton.addActionListener { resolveSelectedInitiative(true) }
        rejectPhaseButton.addActionListener { resolveSelectedInitiative(false) }
        generateTasksButton.addActionListener { generateSelectedInitiativeTasks() }
        setModeButton.addActionListener { setSelectedTaskMode() }
        launchButton.addActionListener { launchSelectedTask() }
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
            DrawerMode.INITIATIVE -> "Raw Initiative"
            DrawerMode.LOGS -> "Logs"
            DrawerMode.NONE -> "Support Panel"
        }
        (drawerCards.layout as CardLayout).show(drawerCards, mode.name)
        revalidate()
        repaint()
    }

    private fun updateActionState() {
        val detail = selectedDetailOrNull()
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedStep = selectedStep,
            patchView = selectedTaskPatchView,
            evidence = selectedTaskEvidence,
            resolution = currentBridgeResolution,
            degraded = currentProjectContext?.degraded == true,
            initiativeStatus = detail?.initiative?.status,
        )
        val isPhase = selectedStep?.kind == PlanStepKind.PHASE
        val isTask = selectedStep?.kind == PlanStepKind.TASK

        advanceButton.isVisible = isPhase
        approvePhaseButton.isVisible = isPhase
        rejectPhaseButton.isVisible = isPhase
        generateTasksButton.isVisible = isPhase
        setModeButton.isVisible = isTask
        launchButton.isVisible = isTask
        previewDiffButton.isVisible = isTask
        applyPatchButton.isVisible = isTask
        openChangedFileButton.isVisible = isTask
        openEvidenceButton.isVisible = isTask

        advanceButton.isEnabled = availability.canAdvancePhase
        approvePhaseButton.isEnabled = availability.canApprovePhase
        rejectPhaseButton.isEnabled = availability.canRejectPhase
        generateTasksButton.isEnabled = availability.canGenerateTasks
        setModeButton.isEnabled = availability.canSetMode
        launchButton.isEnabled = availability.canLaunch
        previewDiffButton.isEnabled = availability.canPreviewDiff
        applyPatchButton.isEnabled = availability.canApplyPatch
        openChangedFileButton.isEnabled = availability.canOpenChangedFile
        openEvidenceButton.isEnabled = availability.canOpenEvidence

        val approvalSummary = WorkbenchStateMapper.buildApprovalSummary(currentApprovals, selectedStep?.taskId)
        val selectedApprovalForTask = approvalSummary.selectedTaskApproval
        val taskCallout = selectedApprovalForTask?.let {
            "This step is waiting for approval '${it.actionType}'. Resolve it here or from the approvals panel."
        }
        val phaseCallout = if (isPhase && detail?.initiative?.status?.endsWith("_review") == true) {
            "This phase is waiting for review. Approve it or send it back with feedback."
        } else {
            null
        }
        approvalCalloutPanel.isVisible = taskCallout != null || phaseCallout != null
        approvalCalloutLabel.text = taskCallout ?: phaseCallout.orEmpty()
        approveInlineButton.isVisible = taskCallout != null
        rejectInlineButton.isVisible = taskCallout != null
        stepMetaLabel.toolTipText = availability.blockingReason
    }

    private fun loadStatus() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        currentProjectContext = context
        setOperationStatus("Refreshing workspace status…")
        if (apiKey.isBlank()) {
            backendReady = null
            currentBridgeResolution = null
            currentBridges = emptyList()
            capabilitiesArea.text = "Configure the API key in Settings > StrateCode Agents."
            bridgeSummaryArea.text = "Configure the API key first. No bridge checks can run without it."
            bridgeCandidatesArea.text = ""
            recordDiagnostic("warning", "Missing API key", "Configure StrateCode Agents settings first.")
            clearOperationStatus()
            refreshHeader()
            updateActionState()
            return
        }
        val client = buildClient(settings.currentState.baseUrl, apiKey)
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val warnings = mutableListOf<Pair<String, String>>()
                val ready = runCatching { client.checkReady() }
                    .onFailure { warnings += "Ready check degraded" to (it.message ?: it.toString()) }
                    .getOrNull()
                val bridges = runCatching { client.listBridges() }
                    .onFailure { warnings += "Bridge list degraded" to (it.message ?: it.toString()) }
                    .getOrNull()
                val capabilities = runCatching {
                    client.listCapabilities(context.repositoryUrl, "needs_external_evidence", "researcher", listOf("evidence", "docs"))
                }.onFailure {
                    warnings += "Capabilities degraded" to (it.message ?: it.toString())
                }.getOrNull()
                val projectCaps = context.repositoryUrl?.let {
                    runCatching { client.getProjectCapabilities(it, "needs_repo_static_analysis", "reviewer") }
                        .onFailure { error -> warnings += "Project capabilities degraded" to (error.message ?: error.toString()) }
                        .getOrNull()
                }
                val bridge = bridges?.let { BridgeResolver.resolve(it.items, context.workspaceRoot, settings.currentState.bridgeName) }
                StatusBundle(
                    backendReady = ready?.ready,
                    bridges = bridges?.items.orEmpty(),
                    bridgeResolution = bridge,
                    capabilitiesText = buildCapabilitiesText(
                        projectMode = projectCaps?.mode,
                        candidates = capabilities?.capabilities.orEmpty(),
                        projectCapabilities = projectCaps?.capabilities.orEmpty(),
                        warnings = warnings,
                    ),
                    warnings = warnings,
                )
            }.onSuccess { bundle ->
                StrateCodeProjectStore.write(context, bridgeName = settings.currentState.bridgeName)
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    backendReady = bundle.backendReady
                    currentBridges = bundle.bridges
                    currentBridgeResolution = bundle.bridgeResolution
                    capabilitiesArea.text = bundle.capabilitiesText
                    bridgeSummaryArea.text = renderBridgeSummary(context, bundle.bridgeResolution, bundle.warnings)
                    bridgeCandidatesArea.text = renderBridgeCandidates(context.workspaceRoot, bundle.bridges)
                    bundle.warnings.forEach { (title, message) ->
                        recordDiagnostic("warning", title, message)
                    }
                    bundle.bridgeResolution?.let { maybeAutoRegisterBridge(context, it) }
                    refreshHeader()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    backendReady = null
                    currentBridges = emptyList()
                    currentBridgeResolution = null
                    capabilitiesArea.text = error.message ?: error.toString()
                    bridgeSummaryArea.text = error.message ?: error.toString()
                    bridgeCandidatesArea.text = ""
                    handleFailure("Status refresh failed", error.message ?: error.toString())
                    refreshHeader()
                    updateActionState()
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
            clearPlanState("No local goals registered in .stratecode/project.json.\n\nCreate a new goal from the plugin or from editor selection to seed this workspace.")
            refreshHeader()
            return
        }
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                fetchInitiativesForWorkspace(
                    client = buildClient(settings.currentState.baseUrl, apiKey),
                    workspaceRoot = context.workspaceRoot,
                    expectedInitiativeId = selectInitiativeId,
                )
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    val fetched = response.items.filter { it.id in knownInitiativeIds }
                    val preserved = if (!selectInitiativeId.isNullOrBlank() && fetched.none { it.id == selectInitiativeId }) {
                        currentInitiatives.filter { it.id == selectInitiativeId }
                    } else {
                        emptyList()
                    }
                    if (preserved.isNotEmpty()) {
                        recordDiagnostic(
                            "warning",
                            "Initiative list lagging",
                            "Server list did not include $selectInitiativeId yet; preserving local goal entry temporarily.",
                        )
                    }
                    currentInitiatives = (preserved + fetched).distinctBy { it.id }
                    val target = refreshInitiativeSelector(selectInitiativeId ?: selectedInitiative?.id ?: context.metadata?.lastInitiativeId)
                    if (currentInitiatives.isEmpty()) {
                        selectedInitiative = null
                        clearPlanState("No locally tracked initiatives are currently available on the server.")
                    } else if (target != null && selectedDetailOrNull()?.initiative?.id != target.id) {
                        loadInitiativeDetail(target.id)
                    }
                    refreshHeader()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    currentInitiatives = emptyList()
                    handleFailure("Initiative list failed", error.message ?: error.toString(), notifyUser = false)
                    clearPlanState(error.message ?: error.toString())
                }
            }
        }
    }

    private fun refreshInitiativeSelector(selectInitiativeId: String?): InitiativeWorkbenchItem? {
        val detailById = initiativeDetailCache.toMap()
        val artifactCountById = initiativeArtifactCache.mapValues { it.value.size }
        val items = WorkbenchStateMapper.buildInitiatives(currentInitiatives, detailById, artifactCountById)
        suppressInitiativeSelectionEvents = true
        initiativeSelectorModel.removeAllElements()
        items.forEach(initiativeSelectorModel::addElement)
        val target = items.firstOrNull { it.id == selectInitiativeId } ?: items.firstOrNull()
        if (target != null) {
            initiativeSelector.selectedItem = target
            selectedInitiative = currentInitiatives.firstOrNull { it.id == target.id }
                ?: selectedInitiative?.takeIf { it.id == target.id }
                ?: detailById[target.id]?.initiative?.toSummary()
        } else {
            selectedInitiative = null
        }
        suppressInitiativeSelectionEvents = false
        return target
    }

    private fun loadInitiativeDetail(initiativeId: String) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            return
        }
        if (loadingInitiativeDetailId == initiativeId) {
            return
        }
        loadingInitiativeDetailId = initiativeId
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = buildClient(settings.currentState.baseUrl, apiKey)
                fetchInitiativeDetailBundle(client, initiativeId)
            }.onSuccess { bundle ->
                val context = ProjectContextResolver.resolve(project)
                StrateCodeProjectStore.rememberInitiative(
                    context,
                    initiativeId = bundle.detail.initiative.id,
                    initiativeTitle = bundle.detail.initiative.title,
                    bridgeName = settings.currentState.bridgeName,
                )
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    initiativeDetailCache[initiativeId] = bundle.detail
                    initiativeTaskCache[initiativeId] = bundle.tasks
                    initiativeArtifactCache[initiativeId] = bundle.artifacts
                    val summary = bundle.detail.initiative.toSummary()
                    currentInitiatives = (listOf(summary) + currentInitiatives.filterNot { it.id == initiativeId }).distinctBy { it.id }
                    selectedInitiative = summary
                    refreshInitiativeSelector(initiativeId)
                    rebuildPlanList()
                    renderInitiativeSnapshot()
                    refreshHeader()
                    updateActionState()
                    loadingInitiativeDetailId = null
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    loadingInitiativeDetailId = null
                    handleFailure("Initiative detail failed", error.message ?: error.toString(), notifyUser = false)
                    clearPlanState("Failed to load initiative detail:\n${error.message ?: error}")
                }
            }
        }
    }

    private fun rebuildPlanList(selectStepId: String? = selectedStep?.stepId) {
        val detail = selectedDetailOrNull()
        if (detail == null) {
            planModel.clear()
            currentPlanSteps = emptyList()
            renderInitiativeSnapshot()
            return
        }
        val tasks = initiativeTaskCache[detail.initiative.id].orEmpty()
        currentPlanSteps = WorkbenchStateMapper.buildPlanSteps(
            detail = detail,
            tasks = tasks,
            approvals = currentApprovals,
            patchByTaskId = taskPatchCache,
            evidenceByTaskId = taskEvidenceCache,
            statusFilter = statusFilterCombo.selectedItem?.toString() ?: "all",
            agentFilter = agentFilterCombo.selectedItem?.toString() ?: "all",
        )
        suppressPlanSelectionEvents = true
        planModel.clear()
        currentPlanSteps.forEach(planModel::addElement)
        val target = currentPlanSteps.firstOrNull { it.stepId == selectStepId }
            ?: currentPlanSteps.firstOrNull { it.status == PlanStepStatus.ACTIVE || it.status == PlanStepStatus.BLOCKED }
            ?: currentPlanSteps.firstOrNull()
        if (target != null) {
            planList.setSelectedValue(target, true)
            selectedStep = target
        } else {
            selectedStep = null
        }
        suppressPlanSelectionEvents = false
        handlePlanSelectionChanged()
    }

    private fun handlePlanSelectionChanged() {
        val step = planList.selectedValue
        selectedStep = step
        if (step == null) {
            renderInitiativeSnapshot()
            updateActionState()
            return
        }
        if (step.kind == PlanStepKind.TASK && step.taskId != null) {
            loadTaskExecutionDetail(step.taskId)
        } else {
            renderPhaseStep(step)
            updateActionState()
        }
    }

    private fun loadTaskExecutionDetail(taskId: String) {
        if (loadingTaskId == taskId) {
            return
        }
        val cachedTask = selectedTaskDetail?.takeIf { it.id == taskId }
        val cachedSources = taskSourcesCache[taskId]
        val cachedPatch = taskPatchCache[taskId]
        val cachedEvidence = taskEvidenceCache[taskId]
        if (cachedTask != null && cachedSources != null && cachedEvidence != null) {
            renderTaskDetail(cachedTask, cachedSources, cachedPatch, cachedEvidence)
            updateActionState()
            return
        }
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            return
        }
        loadingTaskId = taskId
        setOperationStatus("Loading task detail…")
        overviewArea.text = "Loading step detail…"
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = buildClient(settings.currentState.baseUrl, apiKey)
                val task = client.getTask(taskId)
                val sources = client.getTaskSources(taskId).items
                val patch = TaskExecutionSupport.resolvePatch(task, sources)
                val evidence = TaskExecutionSupport.extractEvidence(task, sources)
                TaskExecutionBundle(task, sources, patch, evidence)
            }.onSuccess { bundle ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    loadingTaskId = null
                    selectedTaskDetail = bundle.task
                    selectedTaskPatchView = bundle.patchView
                    selectedTaskEvidence = bundle.evidence
                    selectedTaskSourceArtifacts = bundle.sources
                    taskPatchCache[taskId] = bundle.patchView
                    taskEvidenceCache[taskId] = bundle.evidence
                    taskSourcesCache[taskId] = bundle.sources
                    renderTaskDetail(bundle.task, bundle.sources, bundle.patchView, bundle.evidence)
                    rebuildPlanList(selectStepId = selectedStep?.stepId)
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    loadingTaskId = null
                    selectedTaskDetail = null
                    selectedTaskPatchView = null
                    selectedTaskEvidence = null
                    selectedTaskSourceArtifacts = emptyList()
                    handleFailure("Task detail failed", error.message ?: error.toString(), notifyUser = false)
                    renderErrorDetail("Step detail failed", error.message ?: error.toString())
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
        val step = selectedStep ?: return
        val approvalSummary = WorkbenchStateMapper.buildApprovalSummary(currentApprovals, step.taskId)
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedStep = step,
            patchView = patchView,
            evidence = evidence,
            resolution = currentBridgeResolution,
            degraded = currentProjectContext?.degraded == true,
            initiativeStatus = selectedDetailOrNull()?.initiative?.status,
        )
        val viewState = TaskDetailViewState(
            title = step.title,
            subtitle = "${step.agent ?: "unknown"} · ${task.state} · ${step.executionMode ?: "-"} · ${task.updatedAt}",
            badgesText = buildBadgesText(patchView != null, evidence.locations.isNotEmpty(), approvalSummary.selectedTaskApproval != null, artifacts.isNotEmpty()),
            overviewText = renderTaskOverviewText(task, patchView, evidence, availability),
            outputText = renderTaskOutputText(task, patchView, evidence),
            diffText = patchView?.diff ?: "No diff available for this step.",
            patchView = patchView,
            evidenceLocations = evidence.locations,
            evidenceDetailText = renderEvidenceDetail(null, evidence),
            artifacts = artifacts,
            artifactDetailText = if (artifacts.isEmpty()) "No task-scoped artifacts." else renderArtifactDetail(artifacts.first()),
            approvalCallout = approvalSummary.selectedTaskApproval?.let {
                "Task ${it.taskId} is blocked by approval '${it.actionType}'. Resolve it inline or from the approvals panel."
            },
        )
        renderDetailViewState(viewState)
    }

    private fun renderPhaseStep(step: PlanStepWorkbenchItem) {
        val detail = selectedDetailOrNull()
        if (detail == null) {
            renderInitiativeSnapshot()
            return
        }
        val artifacts = initiativeArtifactCache[detail.initiative.id].orEmpty()
        val phase = step.phase ?: detail.initiative.currentPhase
        val phaseHistory = detail.histories.firstOrNull { it.phase == phase }
        val reviews = detail.reviews.filter { it.phase == phase }
        val availability = WorkbenchStateMapper.buildTaskActionAvailability(
            selectedStep = step,
            patchView = null,
            evidence = null,
            resolution = currentBridgeResolution,
            degraded = currentProjectContext?.degraded == true,
            initiativeStatus = detail.initiative.status,
        )
        val overview = buildString {
            appendLine("Goal")
            appendLine("====")
            appendLine(detail.initiative.goal)
            appendLine()
            appendLine("Plan step")
            appendLine("=========")
            appendLine("Phase: ${phase.replaceFirstChar { it.uppercase() }}")
            appendLine("Status: ${detail.initiative.status}")
            appendLine("Current phase: ${detail.initiative.currentPhase}")
            appendLine("Execution mode: ${detail.initiative.executionMode}")
            appendLine("Task count: ${detail.executionSummary.taskCount}")
            appendLine()
            appendLine("Next action")
            appendLine("===========")
            appendLine(nextActionText(step, availability, detail))
            currentBridgeResolution?.executionBlockReason()?.let {
                appendLine()
                appendLine("Bridge block")
                appendLine("============")
                appendLine(it)
            }
        }.trim()
        val output = buildString {
            appendLine("Phase history")
            appendLine("=============")
            val items = phaseHistory?.items.orEmpty()
            if (items.isEmpty()) {
                appendLine("No stored versions for this phase yet.")
            } else {
                items.forEach { entry ->
                    appendLine("v${entry.version} · ${entry.createdAt}")
                    entry.diffSummary?.let { appendLine(it) }
                    entry.artifactType?.let { appendLine("artifact: $it") }
                    appendLine()
                }
            }
            appendLine()
            appendLine("Reviews")
            appendLine("=======")
            if (reviews.isEmpty()) {
                appendLine("No review decisions recorded yet.")
            } else {
                reviews.forEach { review ->
                    appendLine("${review.phase} · ${review.decision} · ${review.createdAt}")
                    review.feedback?.takeIf { it.isNotBlank() }?.let { appendLine(it) }
                    appendLine()
                }
            }
        }.trim()
        renderDetailViewState(
            TaskDetailViewState(
                title = step.title,
                subtitle = step.subtitle,
                badgesText = buildBadgeLine(
                    badgeText("status", step.status.name.lowercase()),
                    badgeText("phase", phase),
                    badgeText("artifacts", artifacts.size.toString()),
                ),
                overviewText = overview,
                outputText = output,
                diffText = "Diff preview is only available on executable coder steps.",
                patchView = null,
                evidenceLocations = emptyList(),
                evidenceDetailText = "Evidence navigation is available on reviewer steps with concrete findings.",
                artifacts = artifacts,
                artifactDetailText = if (artifacts.isEmpty()) "No initiative artifacts generated yet." else renderArtifactDetail(artifacts.first()),
                approvalCallout = if (detail.initiative.status.endsWith("_review") && detail.initiative.currentPhase == phase) {
                    "This phase is waiting for review. Use the phase actions above."
                } else {
                    null
                },
            ),
        )
    }

    private fun renderDetailViewState(viewState: TaskDetailViewState) {
        stepTitleLabel.text = viewState.title
        stepMetaLabel.text = viewState.subtitle
        stepBadgesLabel.text = viewState.badgesText
        overviewArea.text = viewState.overviewText
        outputArea.text = viewState.outputText
        diffArea.text = viewState.diffText
        evidenceModel.clear()
        viewState.evidenceLocations.forEach(evidenceModel::addElement)
        evidenceArea.text = viewState.evidenceDetailText
        taskArtifactModel.clear()
        viewState.artifacts.forEach(taskArtifactModel::addElement)
        selectedArtifact = viewState.artifacts.firstOrNull()
        artifactDetailArea.text = if (selectedArtifact != null) renderArtifactDetail(selectedArtifact) else "No relevant artifacts."
        approvalCalloutPanel.isVisible = viewState.approvalCallout != null
        approvalCalloutLabel.text = viewState.approvalCallout ?: ""
        approveInlineButton.isVisible = selectedStep?.kind == PlanStepKind.TASK && selectedStep?.approvalRequired == true
        rejectInlineButton.isVisible = approveInlineButton.isVisible
        initiativeInfoArea.text = selectedDetailOrNull()?.let { renderInitiativeInfo(it, initiativeArtifactCache[it.initiative.id].orEmpty()) } ?: "No initiative loaded."
    }

    private fun renderInitiativeSnapshot() {
        val detail = selectedDetailOrNull()
        val artifacts = detail?.initiative?.id?.let { initiativeArtifactCache[it] }.orEmpty()
        if (detail == null) {
            goalLabel.text = "No active goal"
            contextLabel.text = "Create a local initiative to start a plan for this workspace."
            stepTitleLabel.text = "No goal selected"
            stepMetaLabel.text = "Create one or pick an existing local goal for this project."
            stepBadgesLabel.text = ""
            overviewArea.text = "No initiative loaded for this workspace."
            outputArea.text = "When a goal exists, this panel will show the current step and its output."
            diffArea.text = "Select a coder step to inspect its diff."
            evidenceArea.text = "Select a reviewer step to inspect its evidence."
            artifactDetailArea.text = "Select a step to inspect its artifacts."
            initiativeInfoArea.text = "No initiative loaded."
            evidenceModel.clear()
            taskArtifactModel.clear()
            approvalCalloutPanel.isVisible = false
            return
        }
        goalLabel.text = detail.initiative.title
        contextLabel.text = """
            <html>
            <b>Goal:</b> ${escapeHtml(detail.initiative.goal.take(180))}<br/>
            <b>Phase:</b> ${escapeHtml(detail.initiative.currentPhase)} · <b>Status:</b> ${escapeHtml(detail.initiative.status)} · <b>Tasks:</b> ${detail.executionSummary.taskCount}<br/>
            <b>Next:</b> ${escapeHtml(nextActionText(selectedStep, WorkbenchStateMapper.buildTaskActionAvailability(selectedStep, selectedTaskPatchView, selectedTaskEvidence, currentBridgeResolution, currentProjectContext?.degraded == true, detail.initiative.status), detail))}
            </html>
        """.trimIndent()
        renderPhaseStep(
            selectedStep ?: currentPlanSteps.firstOrNull { it.kind == PlanStepKind.PHASE && it.phase == detail.initiative.currentPhase }
                ?: PlanStepWorkbenchItem(
                    stepId = "phase:${detail.initiative.currentPhase}",
                    initiativeId = detail.initiative.id,
                    kind = PlanStepKind.PHASE,
                    title = detail.initiative.title,
                    subtitle = detail.initiative.status,
                    status = PlanStepStatus.ACTIVE,
                    phase = detail.initiative.currentPhase,
                ),
        )
        initiativeInfoArea.text = renderInitiativeInfo(detail, artifacts)
    }

    private fun renderErrorDetail(title: String, message: String) {
        stepTitleLabel.text = title
        stepMetaLabel.text = message
        stepBadgesLabel.text = ""
        overviewArea.text = message
        outputArea.text = message
        diffArea.text = "No diff available."
        evidenceArea.text = "No evidence available."
        artifactDetailArea.text = "No artifacts available."
        evidenceModel.clear()
        taskArtifactModel.clear()
        approvalCalloutPanel.isVisible = false
    }

    private fun clearPlanState(message: String) {
        planModel.clear()
        currentPlanSteps = emptyList()
        selectedStep = null
        selectedTaskDetail = null
        selectedTaskPatchView = null
        selectedTaskEvidence = null
        selectedTaskSourceArtifacts = emptyList()
        selectedArtifact = null
        renderErrorDetail("No goal selected", message)
        goalLabel.text = "No active goal"
        contextLabel.text = message
        initiativeInfoArea.text = message
        updateActionState()
    }

    private fun refreshHeader() {
        val context = currentProjectContext ?: return
        val state = WorkbenchStateMapper.buildHeaderState(context, backendReady, currentBridgeResolution, currentApprovals.size)
        applyHeaderState(state)
    }

    private fun applyHeaderState(state: HeaderStatusViewState) {
        projectBadge.text = "Project  ${state.projectName}"
        approvalsBadge.text = "Approvals  ${state.approvalsLabel}"
        setBadgeTone(projectBadge, if (state.degraded) StatusTone.WARNING else StatusTone.NEUTRAL)
        setBadgeTone(approvalsBadge, state.approvalsTone)
        val backendBadgeState = WorkbenchStateMapper.buildBackendBadgeState(state, backendReady, currentOperationStatus)
        backendBadge.text = "Backend  ${backendBadgeState.label}"
        setBadgeTone(backendBadge, backendBadgeState.tone)
        val bridgeBadgeState = WorkbenchStateMapper.buildBridgeBadgeState(state, currentBridgeResolution, currentOperationStatus)
        bridgeBadge.text = "Bridge  ${bridgeBadgeState.label}"
        setBadgeTone(bridgeBadge, bridgeBadgeState.tone)
        val currentGoal = selectedDetailOrNull()?.initiative?.goal?.take(180)
        if (!currentGoal.isNullOrBlank()) {
            goalLabel.text = selectedDetailOrNull()?.initiative?.title ?: state.projectName
        }
        contextLabel.text = """
            <html>
            <b>Workspace:</b> ${escapeHtml(shortPath(state.workspaceRoot))}<br/>
            <b>Repository:</b> ${escapeHtml(shortRepo(state.repositoryUrl))}<br/>
            <b>Local state:</b> ${escapeHtml(state.metadataSummary)}
            </html>
        """.trimIndent()
        operationLabel.text = currentOperationStatus ?: derivePassiveOperationStatus()
    }

    private fun derivePassiveOperationStatus(): String {
        if (loadingInitiativeDetailId != null) {
            return "Loading initiative detail…"
        }
        if (loadingTaskId != null) {
            return "Loading task detail…"
        }
        val selectedTaskApproval = selectedStep?.taskId?.let { taskId ->
            currentApprovals.firstOrNull { it.taskId == taskId }
        }
        if (selectedTaskApproval != null) {
            return "Waiting for approval: ${selectedTaskApproval.actionType}"
        }
        val detail = selectedDetailOrNull()
        if (detail != null && detail.initiative.status.endsWith("_review")) {
            return "Waiting for phase review approval."
        }
        val taskState = selectedTaskDetail?.state?.lowercase().orEmpty()
        if (taskState in setOf("running", "executing", "in_progress", "working")) {
            return "Task running on the current bridge."
        }
        return "Idle"
    }

    private fun createInitiative() {
        val settings = settings()
        val apiKey = settings.getApiKey()
        val context = ProjectContextResolver.resolve(project)
        if (apiKey.isBlank()) {
            notify("Goal blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        val form = JPanel().apply {
            layout = BoxLayout(this, BoxLayout.Y_AXIS)
            add(JLabel("Title"))
            val titleField = JBTextField("Goal for ${context.projectName}")
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
        val result = JOptionPane.showConfirmDialog(this, form, "Create Goal", JOptionPane.OK_CANCEL_OPTION, JOptionPane.PLAIN_MESSAGE)
        if (result != JOptionPane.OK_OPTION) {
            return
        }
        val title = (form.getClientProperty("titleField") as JBTextField).text.trim().ifBlank { "Goal for ${context.projectName}" }
        val goal = (form.getClientProperty("goalArea") as JBTextArea).text.trim()
        if (goal.isBlank()) {
            notify("Goal blocked", "Goal is required.", NotificationType.WARNING)
            return
        }
        setOperationStatus("Creating goal…")
        createInitiativeButton.isEnabled = false
        recordDiagnostic("info", "Creating goal", "Submitting '$title' for workspace ${context.workspaceRoot}.")
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                buildClient(settings.currentState.baseUrl, apiKey).createInitiative(title, goal, context.workspaceRoot)
            }.onSuccess {
                StrateCodeProjectStore.rememberInitiative(
                    context,
                    initiativeId = it.id,
                    initiativeTitle = it.title,
                    bridgeName = settings.currentState.bridgeName,
                )
                SwingUtilities.invokeLater {
                    createInitiativeButton.isEnabled = true
                    recordDiagnostic("info", "Goal created", "${it.title} (${it.id})")
                    notify("Goal created", "${it.title} (${it.id})", NotificationType.INFORMATION)
                    currentInitiatives = listOf(it) + currentInitiatives.filterNot { existing -> existing.id == it.id }
                    selectedInitiative = it
                    refreshInitiativeSelector(it.id)
                    clearPlanState("Goal created.\n\nLoading initiative detail from the server…")
                    setOperationStatus("Goal created. Waiting for initiative detail…")
                    loadInitiativeDetail(it.id)
                    loadInitiatives(selectInitiativeId = it.id)
                    loadApprovals()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    createInitiativeButton.isEnabled = true
                    handleFailure("Goal creation failed", error.message ?: error.toString())
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
        setOperationStatus("Registering bridge…")
        registerBridgeButton.isEnabled = false
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                buildClient(settings.currentState.baseUrl, apiKey).registerBridge(context, settings.currentState.bridgeName)
            }.onSuccess {
                StrateCodeProjectStore.write(context, bridgeName = settings.currentState.bridgeName)
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    registerBridgeButton.isEnabled = true
                    recordDiagnostic("info", "Bridge registered", "Bridge ${it.name} bound to ${it.workspaceRoot}.")
                    notify("Bridge registered", "Bridge ${it.name} is now bound to ${it.workspaceRoot}.", NotificationType.INFORMATION)
                    loadStatus()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    registerBridgeButton.isEnabled = true
                    handleFailure("Bridge registration failed", error.message ?: error.toString())
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
        currentPlanSteps = emptyList()
        selectedInitiative = null
        selectedStep = null
        selectedTaskDetail = null
        selectedTaskPatchView = null
        selectedTaskEvidence = null
        selectedTaskSourceArtifacts = emptyList()
        selectedArtifact = null
        selectedApproval = null
        selectedEvidenceLocation = null
        currentProjectContext = ProjectContextResolver.resolve(project)
        autoRegisterAttempts.keys.removeIf { it.startsWith("${context.workspaceRoot}|") }
        recordDiagnostic("info", "Local state reset", "Workspace metadata cleared for ${context.workspaceRoot}.")
        refreshHeader()
        clearPlanState("Local workspace state reset.\n\nCreate a new goal to repopulate this workspace.")
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
            recordDiagnostic("info", "Bridge smoke passed", "Bridge ${resolution.matched?.name ?: "-"} is executable.")
            notify("Bridge smoke passed", "Bridge ${resolution.matched?.name ?: "-"} is executable for ${context.workspaceRoot}.", NotificationType.INFORMATION)
        } else {
            recordDiagnostic("warning", "Bridge smoke failed", problem)
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
                buildClient(settings.currentState.baseUrl, apiKey).listApprovals()
            }.onSuccess { response ->
                SwingUtilities.invokeLater {
                    currentApprovals = response.items
                    approvalsModel.clear()
                    response.items.forEach(approvalsModel::addElement)
                    approvalsArea.text = if (response.items.isEmpty()) "No pending approvals." else renderApprovalDetail(response.items.first())
                    selectedApproval = response.items.firstOrNull()
                    refreshHeader()
                    rebuildPlanList()
                    updateActionState()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    approvalsArea.text = error.message ?: error.toString()
                    approvalsModel.clear()
                    currentApprovals = emptyList()
                    selectedApproval = null
                    handleFailure("Approvals refresh failed", error.message ?: error.toString(), notifyUser = false)
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
        val taskId = selectedStep?.taskId ?: return
        val approval = currentApprovals.firstOrNull { it.taskId == taskId } ?: return
        resolveApproval(approval, approve)
    }

    private fun resolveApproval(approval: ApprovalRecord, approve: Boolean) {
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            notify("Approval blocked", "Configure an API key first.", NotificationType.WARNING)
            return
        }
        setOperationStatus(if (approve) "Approving pending action…" else "Rejecting pending action…")
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                val client = buildClient(settings.currentState.baseUrl, apiKey)
                if (approve) client.approveApproval(approval.id, "jetbrains-plugin") else client.rejectApproval(approval.id, "jetbrains-plugin")
            }.onSuccess {
                SwingUtilities.invokeLater {
                    setOperationStatus("Approval resolved. Refreshing goal state…")
                    recordDiagnostic("info", if (approve) "Approval granted" else "Approval rejected", approval.id)
                    notify(if (approve) "Approval granted" else "Approval rejected", approval.id, NotificationType.INFORMATION)
                    loadApprovals()
                    selectedInitiative?.let { loadInitiativeDetail(it.id) }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    handleFailure("Approval resolution failed", error.message ?: error.toString())
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
        val selectedTask = selectedStep?.takeIf { it.kind == PlanStepKind.TASK } ?: return
        val choice = JOptionPane.showInputDialog(
            this,
            "Execution mode for selected step:",
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
            client.updateInitiativeTaskMode(detail.initiative.id, selectedTask.taskId!!, choice)
        }
    }

    private fun launchSelectedTask() {
        val detail = selectedDetailOrNull() ?: return
        val selectedTask = selectedStep?.takeIf { it.kind == PlanStepKind.TASK && it.taskId != null } ?: return
        val resolution = currentBridgeResolution
        val blockReason = if (resolution == null) "No bridge state is loaded for this project." else resolution.executionBlockReason()
        if (blockReason != null) {
            notify("Launch blocked", blockReason, NotificationType.WARNING)
            loadStatus()
            return
        }
        runInitiativeMutation("Task launch queued") { client ->
            client.launchInitiativeTasks(detail.initiative.id, listOf(selectedTask.taskId!!))
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
                    selectedStep?.taskId?.let { loadTaskExecutionDetail(it) }
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    notify("Patch apply failed", error.message ?: error.toString(), NotificationType.ERROR)
                    outputArea.text = buildString {
                        appendLine(outputArea.text)
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
            notify("Open changed file failed", "No changed file could be opened from this step.", NotificationType.WARNING)
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
        setOperationStatus("$successTitle…")
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                action(buildClient(settings.currentState.baseUrl, apiKey))
            }.onSuccess {
                SwingUtilities.invokeLater {
                    setOperationStatus("$successTitle. Refreshing goal state…")
                    recordDiagnostic("info", successTitle, selected.title)
                    notify(successTitle, selected.title, NotificationType.INFORMATION)
                    loadInitiatives(selectInitiativeId = selected.id)
                    loadInitiativeDetail(selected.id)
                    loadApprovals()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    handleFailure("Initiative action failed", error.message ?: error.toString())
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

    private fun InitiativeRecord.toSummary(): InitiativeSummary =
        InitiativeSummary(
            id = id,
            title = title,
            status = status,
            currentPhase = currentPhase,
            workspaceRoot = workspaceRoot,
            createdAt = createdAt,
        )

    private fun renderTaskOverviewText(
        task: TaskDetailRecord,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult,
        availability: TaskActionAvailability,
    ): String = buildString {
        appendLine("Task")
        appendLine("====")
        appendLine("ID: ${task.id}")
        appendLine("State: ${task.state}")
        appendLine("Updated: ${task.updatedAt}")
        appendLine("Workspace path: ${task.workspacePath ?: "-"}")
        task.errorMessage?.takeIf { it.isNotBlank() }?.let {
            appendLine()
            appendLine("Task error")
            appendLine("==========")
            appendLine(it)
        }
        appendLine()
        appendLine("Signal")
        appendLine("======")
        appendLine("Diff: ${if (patchView != null) "available" else "none"}")
        appendLine("Evidence findings: ${evidence.locations.size}")
        appendLine("Raw artifacts: ${evidence.rawArtifacts.size}")
        appendLine()
        appendLine("Next action")
        appendLine("===========")
        appendLine(nextActionText(selectedStep, availability, selectedDetailOrNull()))
        availability.blockingReason?.let {
            appendLine()
            appendLine("Blocking reason")
            appendLine("===============")
            appendLine(it)
        }
    }.trim()

    private fun nextActionText(
        step: PlanStepWorkbenchItem?,
        availability: TaskActionAvailability,
        detail: InitiativeDetailResponseRecord?,
    ): String {
        if (step == null || detail == null) {
            return "Create or select a goal."
        }
        return when (step.kind) {
            PlanStepKind.PHASE -> when {
                availability.canAdvancePhase -> "Click 'Advance Draft' to generate the next draft for this phase."
                availability.canApprovePhase -> "Review the phase output and click 'Approve Phase' or 'Reject Phase'."
                availability.canGenerateTasks -> "Click 'Generate Tasks' to materialize the execution backlog."
                detail.initiative.currentPhase != step.phase -> "Inspect this phase for context or switch to the active phase."
                else -> "Select the execution step or refresh if the phase should already have moved."
            }
            PlanStepKind.TASK -> when {
                step.approvalRequired -> "Resolve the pending approval before trying to continue."
                availability.canLaunch -> "Click 'Run' to launch this task on the current bridge."
                availability.canApplyPatch -> "Inspect the diff and apply the patch if it looks correct."
                availability.blockingReason != null -> availability.blockingReason
                else -> "Inspect output, diff or evidence for this step."
            }
        }
    }

    private fun renderTaskOutputText(
        task: TaskDetailRecord,
        patchView: TaskResultPatchView?,
        evidence: EvidenceExtractionResult,
    ): String = buildString {
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
        task.results?.let {
            appendLine()
            appendLine("Raw results")
            appendLine("===========")
            appendLine(it.toString().take(1600))
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
            return "Select a reviewer step to inspect its evidence."
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

    private fun renderBridgeSummary(
        context: ProjectContext,
        resolution: BridgeResolution?,
        warnings: List<Pair<String, String>> = emptyList(),
    ): String =
        buildString {
            appendLine("Configured bridge name: ${settings().currentState.bridgeName}")
            appendLine("Workspace root: ${context.workspaceRoot}")
            appendLine("Repository: ${context.repositoryUrl ?: "degraded"}")
            appendLine()
            appendLine("Resolution")
            appendLine("==========")
            if (resolution == null) {
                appendLine("Bridge state unavailable because bridge discovery failed.")
            } else {
                appendLine("Status: ${resolution.status}")
                appendLine("Consistency: ${resolution.consistency}")
                appendLine("Executable: ${resolution.executable}")
                appendLine("Detail: ${resolution.detail}")
                appendLine("Heartbeat age: ${resolution.heartbeatAgeSeconds?.let { "${it}s" } ?: "<never>"}")
                appendLine("Stale: ${resolution.stale}")
                appendLine()
            }
            resolution?.matched?.let {
                appendLine("Matched bridge")
                appendLine("==============")
                appendLine("ID: ${it.id}")
                appendLine("Name: ${it.name}")
                appendLine("Host: ${it.hostname}")
                appendLine("Workspace: ${it.workspaceRoot}")
                appendLine("Last heartbeat: ${it.lastHeartbeat ?: "-"}")
            }
            resolution?.executionBlockReason()?.let {
                appendLine()
                appendLine("Execution block")
                appendLine("===============")
                appendLine(it)
            }
            if (warnings.isNotEmpty()) {
                appendLine()
                appendLine("Degraded checks")
                appendLine("===============")
                warnings.forEach { (title, message) ->
                    appendLine("$title: $message")
                }
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
        projectMode: String?,
        candidates: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
        projectCapabilities: List<com.stratecode.lab.jetbrains.client.CapabilityCandidate>,
        warnings: List<Pair<String, String>> = emptyList(),
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
        appendLine("mode: ${projectMode ?: "n/a"}")
        if (projectCapabilities.isEmpty()) {
            appendLine("No project-scoped reviewer capabilities returned.")
        } else {
            projectCapabilities.forEach {
                appendLine("• ${it.name}  [${it.kind}]  score=${"%.1f".format(it.score)}")
                appendLine("  tags: ${it.capabilityTags.joinToString(", ")}")
            }
        }
        if (warnings.isNotEmpty()) {
            appendLine()
            appendLine("Degraded checks")
            appendLine("===============")
            warnings.forEach { (title, message) ->
                appendLine("• $title")
                appendLine("  $message")
            }
        }
    }.trim()

    private fun fetchInitiativeDetailBundle(
        client: OrchestratorClient,
        initiativeId: String,
        attempts: Int = 4,
        delayMillis: Long = 500,
    ): InitiativeDetailBundle {
        var lastError: Throwable? = null
        repeat(attempts) { index ->
            try {
                return InitiativeDetailBundle(
                    detail = client.getInitiativeDetail(initiativeId),
                    tasks = client.listInitiativeTasks(initiativeId).items,
                    artifacts = client.listInitiativeArtifacts(initiativeId).items,
                )
            } catch (error: Throwable) {
                lastError = error
                if (index < attempts - 1) {
                    Thread.sleep(delayMillis)
                }
            }
        }
        throw (lastError ?: IllegalStateException("Initiative detail remained unavailable for $initiativeId"))
    }

    private fun fetchInitiativesForWorkspace(
        client: OrchestratorClient,
        workspaceRoot: String,
        expectedInitiativeId: String? = null,
        attempts: Int = 4,
        delayMillis: Long = 500,
    ): com.stratecode.lab.jetbrains.client.InitiativeListResponse {
        var lastResponse: com.stratecode.lab.jetbrains.client.InitiativeListResponse? = null
        var lastError: Throwable? = null
        repeat(attempts) { index ->
            try {
                val response = client.listInitiatives(workspaceRoot)
                lastResponse = response
                if (expectedInitiativeId.isNullOrBlank() || response.items.any { it.id == expectedInitiativeId }) {
                    return response
                }
                recordDiagnostic(
                    "warning",
                    "Initiative list pending consistency",
                    "Goal $expectedInitiativeId not visible in workspace list yet. Retry ${index + 1}/$attempts.",
                )
            } catch (error: Throwable) {
                lastError = error
            }
            if (index < attempts - 1) {
                Thread.sleep(delayMillis)
            }
        }
        lastResponse?.let { return it }
        throw (lastError ?: IllegalStateException("Initiative list remained unavailable for workspace $workspaceRoot"))
    }

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

    private fun maybeAutoRegisterBridge(context: ProjectContext, resolution: BridgeResolution) {
        if (context.workspaceRoot.isBlank() || !resolution.shouldAttemptAutoRegister()) {
            return
        }
        val attemptKey = "${context.workspaceRoot}|${settings().currentState.bridgeName}"
        val now = System.currentTimeMillis()
        val lastAttemptAt = autoRegisterAttempts[attemptKey]
        if (lastAttemptAt != null && now - lastAttemptAt < autoRegisterCooldownMillis) {
            return
        }
        autoRegisterAttempts[attemptKey] = now
        val reason = resolution.executionBlockReason() ?: "No bridge was registered for this workspace."
        recordDiagnostic("info", "Auto-registering bridge", "$reason Attempting automatic registration.")
        val settings = settings()
        val apiKey = settings.getApiKey()
        if (apiKey.isBlank()) {
            return
        }
        setOperationStatus("Auto-registering bridge…")
        ApplicationManager.getApplication().executeOnPooledThread {
            runCatching {
                buildClient(settings.currentState.baseUrl, apiKey).registerBridge(context, settings.currentState.bridgeName)
            }.onSuccess {
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    recordDiagnostic("info", "Auto-register succeeded", "Bridge ${it.name} bound to ${it.workspaceRoot}.")
                    loadStatus()
                }
            }.onFailure { error ->
                SwingUtilities.invokeLater {
                    clearOperationStatus()
                    handleFailure("Auto-register failed", error.message ?: error.toString(), notifyUser = false)
                }
            }
        }
    }

    private fun setOperationStatus(message: String) {
        val update = {
            currentOperationStatus = message
            refreshHeader()
            updateActionState()
        }
        if (SwingUtilities.isEventDispatchThread()) {
            update()
        } else {
            SwingUtilities.invokeLater(update)
        }
    }

    private fun buildClient(baseUrl: String, apiKey: String): OrchestratorClient =
        OrchestratorClient(baseUrl, apiKey) { traceMessage ->
            recordDiagnostic("info", "HTTP", traceMessage)
        }

    private fun clearOperationStatus() {
        val update = {
            currentOperationStatus = null
            refreshHeader()
            updateActionState()
        }
        if (SwingUtilities.isEventDispatchThread()) {
            update()
        } else {
            SwingUtilities.invokeLater(update)
        }
    }

    private fun handleFailure(title: String, message: String, notifyUser: Boolean = true) {
        recordDiagnostic("error", title, message)
        showDrawer(DrawerMode.LOGS)
        if (notifyUser) {
            notify(title, message, NotificationType.ERROR)
        }
    }

    private fun recordDiagnostic(level: String, title: String, message: String) {
        val line = "[${logTimeFormatter.format(Instant.now())}] ${level.uppercase()}  $title\n$message"
        updateDiagnosticsBuffer(line)
        updateDiagnosticsUi()
        currentProjectContext?.let { StrateCodeProjectStore.appendDiagnostic(it.workspaceRoot, line) }
        when (level.lowercase()) {
            "error" -> LOG.warn("$title: $message")
            "warning" -> LOG.warn("$title: $message")
            else -> LOG.info("$title: $message")
        }
    }

    private fun updateDiagnosticsBuffer(line: String) {
        synchronized(recentDiagnostics) {
            while (recentDiagnostics.size >= 50) {
                recentDiagnostics.removeFirst()
            }
            recentDiagnostics.addLast(line)
        }
    }

    private fun updateDiagnosticsUi() {
        val render = {
            diagnosticsArea.text = renderDiagnostics()
        }
        if (SwingUtilities.isEventDispatchThread()) {
            render()
        } else {
            SwingUtilities.invokeLater(render)
        }
    }

    private fun renderDiagnostics(): String =
        buildString {
            appendLine("StrateCode Agents diagnostics")
            appendLine("============================")
            appendLine("Generated at ${logTimeFormatter.format(Instant.now())}")
            appendLine("Current operation: ${currentOperationStatus ?: "Idle"}")
            appendLine()
            appendLine("IDE log")
            appendLine("=======")
            appendLine(File(PathManager.getLogPath(), "idea.log").absolutePath)
            appendLine()
            appendLine("Recent plugin events")
            appendLine("====================")
            synchronized(recentDiagnostics) {
                if (recentDiagnostics.isEmpty()) {
                    appendLine("No diagnostics recorded yet.")
                } else {
                    recentDiagnostics.forEach {
                        appendLine(it)
                        appendLine()
                    }
                }
            }
        }.trim()

    private fun copyDiagnostics() {
        val payload = renderDiagnostics()
        CopyPasteManager.getInstance().setContents(StringSelection(payload))
        recordDiagnostic("info", "Logs copied", "Diagnostics copied to clipboard.")
        notify("Logs copied", "Plugin diagnostics copied to clipboard.", NotificationType.INFORMATION)
    }

    private fun openIdeLog() {
        val logFile = File(PathManager.getLogPath(), "idea.log")
        if (!logFile.isFile) {
            recordDiagnostic("warning", "IDE log missing", logFile.absolutePath)
            notify("IDE log missing", logFile.absolutePath, NotificationType.WARNING)
            return
        }
        val virtualFile = LocalFileSystem.getInstance().refreshAndFindFileByIoFile(logFile)
        if (virtualFile == null) {
            recordDiagnostic("warning", "IDE log unresolved", logFile.absolutePath)
            notify("IDE log unresolved", logFile.absolutePath, NotificationType.WARNING)
            return
        }
        FileEditorManager.getInstance(project).openEditor(OpenFileDescriptor(project, virtualFile), true)
    }

    private fun openWorkspaceDiagnostics() {
        val context = currentProjectContext ?: return
        val logFile = StrateCodeProjectStore.diagnosticsFile(context.workspaceRoot)
        if (!logFile.isFile) {
            recordDiagnostic("warning", "Workspace log missing", logFile.absolutePath)
            notify("Workspace log missing", logFile.absolutePath, NotificationType.WARNING)
            return
        }
        val virtualFile = LocalFileSystem.getInstance().refreshAndFindFileByIoFile(logFile)
        if (virtualFile == null) {
            recordDiagnostic("warning", "Workspace log unresolved", logFile.absolutePath)
            notify("Workspace log unresolved", logFile.absolutePath, NotificationType.WARNING)
            return
        }
        FileEditorManager.getInstance(project).openEditor(OpenFileDescriptor(project, virtualFile), true)
    }

    private fun settings(): PluginSettingsService =
        ApplicationManager.getApplication().getService(PluginSettingsService::class.java)

    private fun escapeHtml(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

    private fun shortPath(value: String): String {
        val normalized = value.trim()
        if (normalized.length <= 72) {
            return normalized
        }
        return "…" + normalized.takeLast(69)
    }

    private fun shortRepo(value: String?): String {
        val normalized = value?.trim().orEmpty()
        if (normalized.isBlank()) {
            return "degraded"
        }
        if (normalized.length <= 72) {
            return normalized
        }
        return normalized.take(32) + "…" + normalized.takeLast(32)
    }

    private data class StatusBundle(
        val backendReady: Boolean?,
        val bridges: List<LocalBridgeResponse>,
        val bridgeResolution: BridgeResolution?,
        val capabilitiesText: String,
        val warnings: List<Pair<String, String>> = emptyList(),
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
                <b>${escape(value.title)}</b><br/>
                <span style='color:#6B7280'>${escape(value.currentPhase)} / ${escape(value.status)} / ${value.taskCount} tasks</span>
                </html>
            """.trimIndent()
        }
    }

    private fun escape(value: String): String =
        value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
}

private class PlanStepWorkbenchItemCellRenderer : DefaultListCellRenderer() {
    override fun getListCellRendererComponent(
        list: JList<*>,
        value: Any?,
        index: Int,
        isSelected: Boolean,
        cellHasFocus: Boolean,
    ) = super.getListCellRendererComponent(list, value, index, isSelected, cellHasFocus).also {
        if (value is PlanStepWorkbenchItem && it is JLabel) {
            it.border = JBUI.Borders.empty(8)
            val dot = when (value.status) {
                PlanStepStatus.DONE -> "done"
                PlanStepStatus.ACTIVE -> "active"
                PlanStepStatus.BLOCKED -> "blocked"
                PlanStepStatus.PENDING -> "pending"
            }
            val suffix = buildString {
                if (value.diffAvailable) append(" · diff")
                if (value.evidenceAvailable) append(" · evidence")
                if (value.approvalRequired) append(" · approval")
            }
            it.text = """
                <html>
                <b>${escape(value.title)}</b><br/>
                <span style='color:#6B7280'>${escape(dot)} · ${escape(value.subtitle)}${escape(suffix)}</span>
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
