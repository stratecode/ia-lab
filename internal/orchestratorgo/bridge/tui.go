package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

var (
	tuiAccentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	tuiMutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tuiErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	tuiSuccessStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Bold(true)
	tuiTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
	tuiPanelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	tuiSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("62")).Bold(true)
)

type tuiMode string

const (
	tuiModeNormal  tuiMode = "normal"
	tuiModeFilter  tuiMode = "filter"
	tuiModeChat    tuiMode = "chat"
	tuiModeProject tuiMode = "project"
)

var tuiViews = []string{"Overview", "Tasks", "Approvals", "Bridge", "Projects", "Chat"}

type tuiLoadedMsg struct {
	health    *domain.HealthResponse
	bridges   *domain.LocalBridgeListResponse
	tasks     *domain.TaskListResponse
	approvals *domain.ApprovalListResponse
	models    []string
	err       error
}

type tuiTaskDetailMsg struct {
	task    *domain.TaskResponse
	tree    *domain.TaskTreeResponse
	sources []domain.ArtifactResponse
	err     error
}

type tuiActionMsg struct {
	text string
	err  error
}

type tuiChatMsg struct {
	prompt  string
	content string
	model   string
	err     error
}

type tuiCreateTaskMsg struct {
	task      *domain.TaskResponse
	recent    *RecentProject
	presets   map[string]string
	mode      tuiMode
	err       error
}

type tuiDaemonStartedMsg struct {
	cancel context.CancelFunc
	err    error
}

type chatExchange struct {
	Prompt      string
	Response    string
	Model       string
	UsedResearch bool
}

type projectForm struct {
	ParentDirectory textinput.Model
	ProjectName     textinput.Model
	Goal            textinput.Model
	TestFocus       textinput.Model
	ProjectTypeIdx  int
	StackIdx        int
	InitGit         bool
	NeedsApproval   bool
	Focus           int
}

func newProjectForm(opts CLIOptions, state TUIState) projectForm {
	parent := textinput.New()
	parent.Placeholder = "/absolute/path/to/projects"
	parent.SetValue(firstNonEmptyString(state.WizardPresets["parent_directory"], normalizedWorkspaceRoot(opts.WorkspaceRoot)))
	parent.CharLimit = 500
	parent.Width = 48

	name := textinput.New()
	name.Placeholder = "demo-lab-project"
	name.SetValue(firstNonEmptyString(state.WizardPresets["project_name"], "demo-lab-project"))
	name.CharLimit = 120
	name.Width = 32

	goal := textinput.New()
	goal.Placeholder = "What should this project prove?"
	goal.SetValue(firstNonEmptyString(state.WizardPresets["goal"], "Validate local execution, artifacts, and approvals"))
	goal.CharLimit = 240
	goal.Width = 60

	testFocus := textinput.New()
	testFocus.Placeholder = "e.g. bridge execution, planner, approvals"
	testFocus.SetValue(firstNonEmptyString(state.WizardPresets["test_focus"], "bridge execution"))
	testFocus.CharLimit = 180
	testFocus.Width = 48

	parent.Focus()
	return projectForm{
		ParentDirectory: parent,
		ProjectName:     name,
		Goal:            goal,
		TestFocus:       testFocus,
		ProjectTypeIdx:  indexOfString(projectTypeOptions(), state.WizardPresets["project_type"]),
		StackIdx:        indexOfString(stackOptions(), state.WizardPresets["runtime_or_stack"]),
		InitGit:         parseBoolString(state.WizardPresets["initialize_git"], true),
		NeedsApproval:   parseBoolString(state.WizardPresets["requires_approval"], false),
	}
}

func projectTypeOptions() []string {
	return []string{"cli_simple", "api_http", "web_small", "worker_background", "debug_regression", "toy_repo"}
}

func stackOptions() []string {
	return []string{"python", "go", "node", "static"}
}

func (f *projectForm) focusedInput() *textinput.Model {
	switch f.Focus {
	case 0:
		return &f.ParentDirectory
	case 1:
		return &f.ProjectName
	case 4:
		return &f.Goal
	case 5:
		return &f.TestFocus
	default:
		return nil
	}
}

func (f *projectForm) blurAll() {
	f.ParentDirectory.Blur()
	f.ProjectName.Blur()
	f.Goal.Blur()
	f.TestFocus.Blur()
}

func (f *projectForm) setFocus(index int) {
	if index < 0 {
		index = 0
	}
	if index > 7 {
		index = 7
	}
	f.Focus = index
	f.blurAll()
	if input := f.focusedInput(); input != nil {
		input.Focus()
	}
}

func (f *projectForm) move(delta int) {
	f.setFocus((f.Focus + delta + 8) % 8)
}

func (f *projectForm) handleKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "up":
		f.move(-1)
		return
	case "down":
		f.move(1)
		return
	case "left":
		f.shiftChoice(-1)
		return
	case "right":
		f.shiftChoice(1)
		return
	case " ":
		f.toggleBool()
		return
	}

	if input := f.focusedInput(); input != nil {
		updated, _ := input.Update(msg)
		*input = updated
	}
}

func (f *projectForm) shiftChoice(delta int) {
	switch f.Focus {
	case 2:
		items := projectTypeOptions()
		f.ProjectTypeIdx = (f.ProjectTypeIdx + delta + len(items)) % len(items)
	case 3:
		items := stackOptions()
		f.StackIdx = (f.StackIdx + delta + len(items)) % len(items)
	}
}

func (f *projectForm) toggleBool() {
	switch f.Focus {
	case 6:
		f.InitGit = !f.InitGit
	case 7:
		f.NeedsApproval = !f.NeedsApproval
	}
}

func (f *projectForm) toRequest(workspaceRoot string) (domain.TaskCreateRequest, RecentProject) {
	parentDir := strings.TrimSpace(f.ParentDirectory.Value())
	projectName := sanitizeProjectName(f.ProjectName.Value())
	projectType := projectTypeOptions()[f.ProjectTypeIdx]
	stack := stackOptions()[f.StackIdx]
	goal := strings.TrimSpace(f.Goal.Value())
	testFocus := strings.TrimSpace(f.TestFocus.Value())
	description := fmt.Sprintf("Plan and build mini test project %s (%s, %s)", projectName, projectType, stack)
	projectRequest := map[string]any{
		"project_name":     projectName,
		"parent_directory": parentDir,
		"project_type":     projectType,
		"runtime_or_stack": stack,
		"goal":             goal,
		"test_focus":       testFocus,
		"initialize_git":   f.InitGit,
		"requires_approval": f.NeedsApproval,
		"test_command":     strings.Join(defaultBridgeTestCommand(projectType, stack), " "),
		"expected_files":   expectedProjectFiles(projectType),
	}
	metadata := map[string]any{
		"workspace_root":    normalizedWorkspaceRoot(workspaceRoot),
		"requires_approval": f.NeedsApproval,
		"project_flow":      "boilerplate_v1",
		"requested_agents":  []string{"researcher", "coder", "reviewer"},
		"project_request":   projectRequest,
	}
	return domain.TaskCreateRequest{
			Description:     description,
			Metadata:        metadata,
			Priority:        domain.PriorityNormal,
			ExecutionTarget: domain.ExecutionTargetRemote,
			AssignedAgent:   ptrAgentType(domain.AgentTypePlanner),
		}, RecentProject{
			Name:            projectName,
			ParentDirectory: parentDir,
			ProjectType:     projectType,
			RuntimeOrStack:  stack,
		}
}

type TUIModel struct {
	opts         CLIOptions
	client       *Client
	stateStore   *TUIStateStore
	state        TUIState
	width        int
	height       int
	viewIdx      int
	mode         tuiMode
	status       string
	errText      string
	health       *domain.HealthResponse
	bridges      []domain.LocalBridgeResponse
	tasks        []domain.TaskResponse
	taskDetail   *domain.TaskResponse
	taskTree     *domain.TaskTreeResponse
	taskSources  []domain.ArtifactResponse
	approvals    []domain.ApprovalResponse
	models       []string
	selectedTask int
	selectedApproval int
	filterInput  textinput.Model
	filterText   string
	filterFocus  string
	chatInput    textinput.Model
	chatHistory  []chatExchange
	projectForm  projectForm
	daemonCancel  context.CancelFunc
	daemonRunning bool
}

func RunTUI(ctx context.Context, opts CLIOptions) error {
	store, err := NewTUIStateStore()
	if err != nil {
		return err
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	filter := textinput.New()
	filter.Placeholder = "filter tasks or approvals"
	filter.CharLimit = 100
	filter.Width = 32

	chat := textinput.New()
	chat.Placeholder = "Ask the agent something useful"
	chat.CharLimit = 800
	chat.Width = 72

	model := &TUIModel{
		opts:       opts,
		client:     NewClient(opts.BaseURL, opts.APIKey, 20*time.Second),
		stateStore: store,
		state:      state,
		mode:       tuiModeNormal,
		status:     "Ready",
		viewIdx:    maxInt(0, indexOfString(tuiViews, state.LastView)),
		filterInput: filter,
		chatInput:   chat,
		chatHistory: []chatExchange{},
		projectForm: newProjectForm(opts, state),
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err = p.Run()
	if model.daemonCancel != nil {
		model.daemonCancel()
	}
	return err
}

func (m *TUIModel) Init() tea.Cmd {
	return m.refreshAllCmd()
}

func (m *TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tuiLoadedMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = "Refresh failed"
			return m, nil
		}
		m.errText = ""
		m.status = "Data refreshed"
		m.health = msg.health
		if msg.bridges != nil {
			m.bridges = msg.bridges.Items
		}
		if msg.tasks != nil {
			m.tasks = msg.tasks.Items
			if m.selectedTask >= len(m.tasks) && len(m.tasks) > 0 {
				m.selectedTask = len(m.tasks) - 1
			}
		}
		if msg.approvals != nil {
			m.approvals = msg.approvals.Items
			if m.selectedApproval >= len(m.approvals) && len(m.approvals) > 0 {
				m.selectedApproval = len(m.approvals) - 1
			}
		}
		if len(msg.models) > 0 {
			m.models = msg.models
		}
		return m, nil
	case tuiTaskDetailMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			return m, nil
		}
		m.taskDetail = msg.task
		m.taskTree = msg.tree
		m.taskSources = msg.sources
		m.status = "Loaded task detail"
		return m, nil
	case tuiActionMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = "Action failed"
			return m, nil
		}
		m.errText = ""
		m.status = msg.text
		return m, m.refreshAllCmd()
	case tuiChatMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = "Chat failed"
			return m, nil
		}
		m.chatHistory = append([]chatExchange{{
			Prompt:      msg.prompt,
			Response:    msg.content,
			Model:       msg.model,
			UsedResearch: strings.Contains(msg.content, "Sources:") || strings.Contains(msg.content, "Research Run:"),
		}}, m.chatHistory...)
		if len(m.chatHistory) > 8 {
			m.chatHistory = m.chatHistory[:8]
		}
		m.chatInput.SetValue("")
		m.status = "Chat response received"
		m.errText = ""
		return m, nil
	case tuiCreateTaskMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = "Task creation failed"
			return m, nil
		}
		if msg.recent != nil {
			m.state.LastWorkspace = normalizedWorkspaceRoot(m.opts.WorkspaceRoot)
			m.state.LastView = "Projects"
			m.state.WizardPresets = msg.presets
			m.state.RecentProjects = append([]RecentProject{*msg.recent}, m.state.RecentProjects...)
			if len(m.state.RecentProjects) > 8 {
				m.state.RecentProjects = m.state.RecentProjects[:8]
			}
			m.mode = msg.mode
			_ = m.stateStore.Save(m.state)
		}
		m.status = fmt.Sprintf("Created task %s", shortID(msg.task.ID))
		m.errText = ""
		return m, m.refreshAllCmd()
	case tuiDaemonStartedMsg:
		if msg.err != nil {
			m.daemonRunning = false
			m.errText = msg.err.Error()
			m.status = "Bridge daemon failed to start"
			return m, nil
		}
		m.daemonCancel = msg.cancel
		m.daemonRunning = true
		m.errText = ""
		m.status = "Bridge daemon started in-process"
		return m, nil
	case tea.KeyMsg:
		if m.mode == tuiModeChat {
			return m.handleChatMode(msg)
		}
		if m.mode == tuiModeProject {
			return m.handleProjectMode(msg)
		}
		if m.mode == tuiModeFilter {
			return m.handleFilterMode(msg)
		}
		return m.handleNormalMode(msg)
	}
	return m, nil
}

func (m *TUIModel) handleNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.persistState()
		if m.daemonCancel != nil {
			m.daemonCancel()
		}
		return m, tea.Quit
	case "tab":
		m.viewIdx = (m.viewIdx + 1) % len(tuiViews)
		m.status = fmt.Sprintf("View: %s", m.currentView())
		return m, nil
	case "shift+tab":
		m.viewIdx = (m.viewIdx - 1 + len(tuiViews)) % len(tuiViews)
		m.status = fmt.Sprintf("View: %s", m.currentView())
		return m, nil
	case "r":
		return m, m.refreshAllCmd()
	case "/":
		m.mode = tuiModeFilter
		m.filterInput.SetValue(m.filterText)
		m.filterInput.Focus()
		if m.currentView() == "Approvals" {
			m.filterFocus = "approvals"
		} else {
			m.filterFocus = "tasks"
		}
		return m, nil
	case "c":
		m.viewIdx = indexOfString(tuiViews, "Chat")
		m.mode = tuiModeChat
		m.chatInput.Focus()
		return m, nil
	case "p":
		m.viewIdx = indexOfString(tuiViews, "Projects")
		m.mode = tuiModeProject
		m.projectForm = newProjectForm(m.opts, m.state)
		m.projectForm.setFocus(0)
		return m, nil
	}

	switch m.currentView() {
	case "Tasks":
		return m.handleTasksKeys(msg)
	case "Approvals":
		return m.handleApprovalKeys(msg)
	case "Bridge":
		return m.handleBridgeKeys(msg)
	case "Projects":
		if msg.String() == "n" {
			m.mode = tuiModeProject
			m.projectForm = newProjectForm(m.opts, m.state)
			m.projectForm.setFocus(0)
		}
		return m, nil
	case "Chat":
		m.mode = tuiModeChat
		m.chatInput.Focus()
		return m, nil
	default:
		return m, nil
	}
}

func (m *TUIModel) handleTasksKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.filteredTasks()
	switch msg.String() {
	case "up":
		if m.selectedTask > 0 {
			m.selectedTask--
		}
	case "down":
		if m.selectedTask < len(items)-1 {
			m.selectedTask++
		}
	case "enter", "l", "d":
		task := m.selectedTaskItem()
		if task == nil {
			return m, nil
		}
		return m, m.loadTaskDetailCmd(task.ID)
	case "x":
		task := m.selectedTaskItem()
		if task == nil {
			return m, nil
		}
		return m, m.cancelTaskCmd(task.ID)
	case "h":
		task := m.selectedTaskItem()
		if task == nil {
			return m, nil
		}
		return m, m.archiveTaskCmd(task.ID)
	case "v":
		m.state.ShowCompleted = !m.state.ShowCompleted
		_ = m.stateStore.Save(m.state)
		m.status = fmt.Sprintf("Show completed: %t", m.state.ShowCompleted)
		return m, nil
	case "A":
		m.state.ShowArchived = !m.state.ShowArchived
		_ = m.stateStore.Save(m.state)
		m.status = fmt.Sprintf("Show archived: %t", m.state.ShowArchived)
		return m, m.refreshAllCmd()
	}
	return m, nil
}

func (m *TUIModel) handleApprovalKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.filteredApprovals()
	switch msg.String() {
	case "up":
		if m.selectedApproval > 0 {
			m.selectedApproval--
		}
	case "down":
		if m.selectedApproval < len(items)-1 {
			m.selectedApproval++
		}
	case "a":
		approval := m.selectedApprovalItem()
		if approval == nil {
			return m, nil
		}
		return m, m.resolveApprovalCmd(approval.ID, true)
	case "x":
		approval := m.selectedApprovalItem()
		if approval == nil {
			return m, nil
		}
		return m, m.resolveApprovalCmd(approval.ID, false)
	case "enter":
		approval := m.selectedApprovalItem()
		if approval == nil {
			return m, nil
		}
		return m, m.loadTaskDetailCmd(approval.TaskID)
	}
	return m, nil
}

func (m *TUIModel) handleBridgeKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "g":
		return m, m.registerBridgeCmd()
	case "h":
		return m, m.heartbeatBridgeCmd()
	case "s":
		return m, m.smokeBridgeCmd()
	case "enter":
		if m.daemonRunning {
			if m.daemonCancel != nil {
				m.daemonCancel()
			}
			m.daemonCancel = nil
			m.daemonRunning = false
			m.status = "Bridge daemon stopped"
			return m, nil
		}
		return m, m.startDaemonCmd()
	}
	return m, nil
}

func (m *TUIModel) handleChatMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = tuiModeNormal
		m.chatInput.Blur()
		return m, nil
	case "enter":
		prompt := strings.TrimSpace(m.chatInput.Value())
		if prompt == "" {
			return m, nil
		}
		return m, m.chatCmd(prompt)
	case "t":
		prompt := strings.TrimSpace(m.chatInput.Value())
		if prompt == "" && len(m.chatHistory) > 0 {
			prompt = m.chatHistory[0].Prompt
		}
		if prompt == "" {
			return m, nil
		}
		return m, m.createPromptTaskCmd(prompt)
	}
	updated, cmd := m.chatInput.Update(msg)
	m.chatInput = updated
	return m, cmd
}

func (m *TUIModel) handleProjectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = tuiModeNormal
		m.projectForm.blurAll()
		return m, nil
	case "enter":
		if m.projectForm.Focus == 7 {
			request, recent := m.projectForm.toRequest(m.opts.WorkspaceRoot)
			return m, m.createProjectTaskCmd(request, recent)
		}
		m.projectForm.move(1)
		return m, nil
	}
	m.projectForm.handleKey(msg)
	return m, nil
}

func (m *TUIModel) handleFilterMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = tuiModeNormal
		m.filterInput.Blur()
		return m, nil
	case "enter":
		m.filterText = strings.TrimSpace(m.filterInput.Value())
		m.mode = tuiModeNormal
		m.filterInput.Blur()
		m.status = fmt.Sprintf("Applied %s filter", m.filterFocus)
		return m, nil
	}
	updated, cmd := m.filterInput.Update(msg)
	m.filterInput = updated
	return m, cmd
}

func (m *TUIModel) View() string {
	header := m.renderHeader()
	body := m.renderBody()
	status := m.renderStatus()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

func (m *TUIModel) renderHeader() string {
	tabs := make([]string, 0, len(tuiViews))
	for idx, view := range tuiViews {
		label := " " + view + " "
		if idx == m.viewIdx {
			tabs = append(tabs, tuiSelectedStyle.Render(label))
		} else {
			tabs = append(tabs, tuiMutedStyle.Render(label))
		}
	}
	top := lipgloss.JoinHorizontal(lipgloss.Left, tabs...)
	workspace := fmt.Sprintf("workspace: %s", normalizedWorkspaceRoot(m.opts.WorkspaceRoot))
	bridgeID := firstNonEmptyString(strings.TrimSpace(m.opts.BridgeID), "<auto>")
	meta := lipgloss.JoinHorizontal(lipgloss.Left, tuiMutedStyle.Render(workspace), "   ", tuiMutedStyle.Render("bridge: "+bridgeID))
	return lipgloss.JoinVertical(lipgloss.Left, top, meta)
}

func (m *TUIModel) renderBody() string {
	switch m.currentView() {
	case "Overview":
		return m.renderOverview()
	case "Tasks":
		return m.renderTasks()
	case "Approvals":
		return m.renderApprovals()
	case "Bridge":
		return m.renderBridge()
	case "Projects":
		return m.renderProjects()
	case "Chat":
		return m.renderChat()
	default:
		return "Unknown view"
	}
}

func (m *TUIModel) renderOverview() string {
	status := "<unknown>"
	version := ""
	if m.health != nil {
		status = m.health.Status
		version = m.health.Version
	}
	taskCounts := map[domain.TaskState]int{}
	for _, task := range m.tasks {
		taskCounts[task.State]++
	}
	lines := []string{
		tuiTitleStyle.Render("Overview"),
		fmt.Sprintf("Health: %s", status),
		fmt.Sprintf("Version: %s", firstNonEmptyString(version, "<unknown>")),
		fmt.Sprintf("Bridges: %d", len(m.bridges)),
		fmt.Sprintf("Local tasks: %d", len(m.filteredTasks())),
		fmt.Sprintf("Pending approvals: %d", len(m.filteredApprovals())),
		fmt.Sprintf("Queued: %d | In progress: %d | Failed: %d | Completed visible: %d", taskCounts[domain.TaskStateQueued], taskCounts[domain.TaskStateInProgress], taskCounts[domain.TaskStateFailed], taskCounts[domain.TaskStateCompleted]),
		fmt.Sprintf("Chat model: %s", firstNonEmptyString(firstModel(m.models), "orchestrator-tools")),
		"",
		"Use Tab to move between views, / to filter, c for chat, p for project wizard.",
	}
	return tuiPanelStyle.Width(maxInt(60, m.width-2)).Render(strings.Join(lines, "\n"))
}

func (m *TUIModel) renderTasks() string {
	items := m.filteredTasks()
	listLines := []string{tuiTitleStyle.Render("Tasks")}
	listLines = append(listLines, tuiMutedStyle.Render(fmt.Sprintf("v completed=%t   A archived=%t   h archive selected completed", m.state.ShowCompleted, m.state.ShowArchived)))
	for idx, task := range items {
		line := fmt.Sprintf("%s  %-18s  %-9s  %s", shortID(task.ID), task.State, task.ExecutionTarget, truncateInline(task.Description, 44))
		if idx == m.selectedTask {
			listLines = append(listLines, tuiSelectedStyle.Render(line))
		} else {
			listLines = append(listLines, line)
		}
	}
	if len(items) == 0 {
		listLines = append(listLines, tuiMutedStyle.Render("No tasks match the current filter."))
	}
	left := tuiPanelStyle.Width(maxInt(42, m.width/2-2)).Render(strings.Join(listLines, "\n"))

	detail := []string{tuiTitleStyle.Render("Detail")}
	task := m.selectedTaskItem()
	if task != nil {
		detail = append(detail,
			fmt.Sprintf("ID: %s", task.ID),
			fmt.Sprintf("State: %s", task.State),
			fmt.Sprintf("Agent: %s", derefAgent(task.AssignedAgent)),
			fmt.Sprintf("Target: %s", task.ExecutionTarget),
			fmt.Sprintf("Priority: %s", task.Priority),
			fmt.Sprintf("Archived: %t", task.ArchivedAt != nil),
			fmt.Sprintf("Updated: %s", task.UpdatedAt.Format(time.RFC3339)),
			"",
			task.Description,
		)
		if m.taskDetail != nil && m.taskDetail.ID == task.ID {
			detail = append(detail, "", renderTaskResults(m.taskDetail.Results))
			workspacePath := ""
			if task.WorkspacePath != nil && strings.TrimSpace(*task.WorkspacePath) != "" {
				workspacePath = *task.WorkspacePath
			} else if task.Metadata != nil {
				workspacePath = firstNonEmptyString(asString(task.Metadata["workspace_root"]), workspacePath)
			}
			if strings.TrimSpace(workspacePath) != "" {
				detail = append(detail, "", fmt.Sprintf("Workspace path: %s", workspacePath))
			}
			if m.taskTree != nil && m.taskTree.ID == task.ID {
				detail = append(detail, "", "Task tree:", renderTaskTree(*m.taskTree, 0))
			}
			if len(m.taskSources) > 0 {
				detail = append(detail, "", "Artifacts:")
				for _, item := range m.taskSources[:minInt(5, len(m.taskSources))] {
					detail = append(detail, fmt.Sprintf("- %s [%s]", firstNonEmptyString(derefString(item.Title), derefString(item.URI), item.ID), item.ArtifactType))
				}
			}
		}
	} else {
		detail = append(detail, tuiMutedStyle.Render("Select a task to inspect its output."))
	}
	right := tuiPanelStyle.Width(maxInt(42, m.width/2-2)).Render(strings.Join(detail, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *TUIModel) renderApprovals() string {
	items := m.filteredApprovals()
	listLines := []string{tuiTitleStyle.Render("Approvals")}
	for idx, approval := range items {
		line := fmt.Sprintf("%s  %-10s  %s", shortID(approval.ID), approval.Status, truncateInline(approval.TargetResource, 44))
		if idx == m.selectedApproval {
			listLines = append(listLines, tuiSelectedStyle.Render(line))
		} else {
			listLines = append(listLines, line)
		}
	}
	if len(items) == 0 {
		listLines = append(listLines, tuiMutedStyle.Render("No approvals pending."))
	}
	left := tuiPanelStyle.Width(maxInt(42, m.width/2-2)).Render(strings.Join(listLines, "\n"))

	detail := []string{tuiTitleStyle.Render("Approval detail")}
	item := m.selectedApprovalItem()
	if item != nil {
		detail = append(detail,
			fmt.Sprintf("ID: %s", item.ID),
			fmt.Sprintf("Task: %s", item.TaskID),
			fmt.Sprintf("Action: %s", item.ActionType),
			fmt.Sprintf("Target: %s", item.TargetResource),
			fmt.Sprintf("Status: %s", item.Status),
			fmt.Sprintf("Timeout: %s", item.TimeoutAt.Format(time.RFC3339)),
			"",
			"`a` approve  `x` reject  `enter` inspect task",
		)
	}
	right := tuiPanelStyle.Width(maxInt(42, m.width/2-2)).Render(strings.Join(detail, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *TUIModel) renderBridge() string {
	lines := []string{
		tuiTitleStyle.Render("Bridge"),
		fmt.Sprintf("Configured bridge ID: %s", firstNonEmptyString(strings.TrimSpace(m.opts.BridgeID), "<auto>")),
		fmt.Sprintf("Workspace root: %s", normalizedWorkspaceRoot(m.opts.WorkspaceRoot)),
		fmt.Sprintf("Daemon managed by TUI: %t", m.daemonRunning),
		"",
		"Keys:",
		"g register   h heartbeat   s smoke   enter start/stop daemon",
		"",
	}
	if len(m.bridges) == 0 {
		lines = append(lines, tuiMutedStyle.Render("No bridge registered yet."))
	} else {
		for _, item := range m.bridges {
			heartbeat := "<never>"
			if item.LastHeartbeat != nil {
				heartbeat = item.LastHeartbeat.Format(time.RFC3339)
			}
			lines = append(lines, fmt.Sprintf("- %s  %-8s  %s", item.Name, item.Status, heartbeat))
		}
	}
	return tuiPanelStyle.Width(maxInt(60, m.width-2)).Render(strings.Join(lines, "\n"))
}

func (m *TUIModel) renderProjects() string {
	lines := []string{tuiTitleStyle.Render("Projects")}
	if m.mode == tuiModeProject {
		lines = append(lines,
			"Wizard: use up/down to move, left/right for options, space for booleans, enter to continue/submit, esc to cancel.",
			renderProjectField("Parent directory", m.projectForm.ParentDirectory.Value(), m.projectForm.Focus == 0),
			renderProjectField("Project name", m.projectForm.ProjectName.Value(), m.projectForm.Focus == 1),
			renderProjectField("Project type", projectTypeOptions()[m.projectForm.ProjectTypeIdx], m.projectForm.Focus == 2),
			renderProjectField("Runtime / stack", stackOptions()[m.projectForm.StackIdx], m.projectForm.Focus == 3),
			renderProjectField("Goal", m.projectForm.Goal.Value(), m.projectForm.Focus == 4),
			renderProjectField("Test focus", m.projectForm.TestFocus.Value(), m.projectForm.Focus == 5),
			renderProjectField("Initialize git", boolLabel(m.projectForm.InitGit), m.projectForm.Focus == 6),
			renderProjectField("Requires approval", boolLabel(m.projectForm.NeedsApproval), m.projectForm.Focus == 7),
		)
	} else {
		lines = append(lines, "Press `n` or `p` to open the project wizard.")
		lines = append(lines, "The wizard launches a planner root task that fans out to researcher, coder, and reviewer.")
		if len(m.state.RecentProjects) == 0 {
			lines = append(lines, tuiMutedStyle.Render("No recent projects created from the TUI yet."))
		} else {
			lines = append(lines, "")
			for _, item := range m.state.RecentProjects {
				lines = append(lines, fmt.Sprintf("- %s (%s / %s) task=%s", item.Name, item.ProjectType, item.RuntimeOrStack, shortID(item.TaskID)))
			}
		}
	}
	return tuiPanelStyle.Width(maxInt(60, m.width-2)).Render(strings.Join(lines, "\n"))
}

func (m *TUIModel) renderChat() string {
	lines := []string{
		tuiTitleStyle.Render("Chat"),
		"Type a prompt and press enter. Press `t` to convert the prompt into a task, `esc` to leave chat mode.",
		"",
		"> " + m.chatInput.View(),
	}
	if len(m.chatHistory) == 0 {
		lines = append(lines, "", tuiMutedStyle.Render("No chat exchanges yet."))
	} else {
		for _, item := range m.chatHistory[:minInt(4, len(m.chatHistory))] {
			mode := "direct"
			if item.UsedResearch {
				mode = "research"
			}
			lines = append(lines, "", tuiAccentStyle.Render("Prompt:")+" "+item.Prompt)
			lines = append(lines, tuiMutedStyle.Render(fmt.Sprintf("Model: %s | Mode: %s", item.Model, mode)))
			lines = append(lines, truncateMultiline(item.Response, 10))
		}
	}
	return tuiPanelStyle.Width(maxInt(60, m.width-2)).Render(strings.Join(lines, "\n"))
}

func (m *TUIModel) renderStatus() string {
	parts := []string{tuiMutedStyle.Render("q quit"), tuiMutedStyle.Render("tab switch"), tuiMutedStyle.Render("r refresh")}
	if m.errText != "" {
		parts = append(parts, tuiErrorStyle.Render(m.errText))
	} else {
		parts = append(parts, tuiSuccessStyle.Render(m.status))
	}
	return lipgloss.NewStyle().Padding(1, 0, 0, 0).Render(strings.Join(parts, "   "))
}

func (m *TUIModel) currentView() string {
	if m.viewIdx < 0 || m.viewIdx >= len(tuiViews) {
		return "Overview"
	}
	return tuiViews[m.viewIdx]
}

func (m *TUIModel) filteredTasks() []domain.TaskResponse {
	out := make([]domain.TaskResponse, 0, len(m.tasks))
	needle := strings.ToLower(strings.TrimSpace(m.filterText))
	for _, item := range m.tasks {
		if item.ArchivedAt != nil && !m.state.ShowArchived {
			continue
		}
		if item.State == domain.TaskStateCompleted && !m.state.ShowCompleted {
			continue
		}
		hay := strings.ToLower(item.ID + " " + item.Description + " " + string(item.State))
		if needle == "" || m.filterFocus != "tasks" || strings.Contains(hay, needle) {
			out = append(out, item)
		}
	}
	return out
}

func (m *TUIModel) filteredApprovals() []domain.ApprovalResponse {
	if strings.TrimSpace(m.filterText) == "" || m.filterFocus != "approvals" {
		return m.approvals
	}
	out := make([]domain.ApprovalResponse, 0, len(m.approvals))
	needle := strings.ToLower(strings.TrimSpace(m.filterText))
	for _, item := range m.approvals {
		hay := strings.ToLower(item.ID + " " + item.TargetResource + " " + item.TaskID + " " + item.ActionType)
		if strings.Contains(hay, needle) {
			out = append(out, item)
		}
	}
	return out
}

func (m *TUIModel) selectedTaskItem() *domain.TaskResponse {
	items := m.filteredTasks()
	if len(items) == 0 || m.selectedTask < 0 || m.selectedTask >= len(items) {
		return nil
	}
	item := items[m.selectedTask]
	return &item
}

func (m *TUIModel) selectedApprovalItem() *domain.ApprovalResponse {
	items := m.filteredApprovals()
	if len(items) == 0 || m.selectedApproval < 0 || m.selectedApproval >= len(items) {
		return nil
	}
	item := items[m.selectedApproval]
	return &item
}

func (m *TUIModel) refreshAllCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		health, err := m.client.Health(ctx)
		if err != nil {
			return tuiLoadedMsg{err: err}
		}
		bridges, err := m.client.ListBridges(ctx)
		if err != nil {
			return tuiLoadedMsg{err: err}
		}
		params := map[string]string{"limit": "100"}
		if m.state.ShowArchived {
			params["archived"] = "include"
		}
		tasks, err := m.client.ListTasksFiltered(ctx, params)
		if err != nil {
			return tuiLoadedMsg{err: err}
		}
		approvals, err := m.client.ListApprovals(ctx)
		if err != nil {
			return tuiLoadedMsg{err: err}
		}
		models, _ := m.client.ListModels(ctx)
		return tuiLoadedMsg{health: health, bridges: bridges, tasks: tasks, approvals: approvals, models: models}
	}
}

func (m *TUIModel) loadTaskDetailCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		task, err := m.client.GetTask(ctx, taskID)
		if err != nil {
			return tuiTaskDetailMsg{err: err}
		}
		tree, err := m.client.GetTaskTree(ctx, taskID)
		if err != nil {
			return tuiTaskDetailMsg{err: err}
		}
		sources, err := m.client.GetTaskSources(ctx, taskID)
		if err != nil {
			return tuiTaskDetailMsg{err: err}
		}
		return tuiTaskDetailMsg{task: task, tree: tree, sources: sources}
	}
}

func (m *TUIModel) cancelTaskCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := m.client.CancelTask(ctx, taskID)
		return tuiActionMsg{text: "Task cancelled", err: err}
	}
}

func (m *TUIModel) archiveTaskCmd(taskID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := m.client.ArchiveTask(ctx, taskID)
		return tuiActionMsg{text: "Task archived", err: err}
	}
}

func (m *TUIModel) resolveApprovalCmd(approvalID string, approve bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := m.client.ResolveApproval(ctx, approvalID, firstNonEmptyString(m.opts.Name, defaultHostname()), approve)
		text := "Approval rejected"
		if approve {
			text = "Approval approved"
		}
		return tuiActionMsg{text: text, err: err}
	}
}

func (m *TUIModel) registerBridgeCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := m.client.Register(ctx, registerPayload(m.opts))
		return tuiActionMsg{text: "Bridge registered", err: err}
	}
}

func (m *TUIModel) heartbeatBridgeCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := m.client.Heartbeat(ctx, normalizedBridgeID(m.opts.BridgeID), "active")
		return tuiActionMsg{text: "Heartbeat sent", err: err}
	}
}

func (m *TUIModel) smokeBridgeCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		claim, err := m.client.ClaimNext(ctx, normalizedBridgeID(m.opts.BridgeID))
		if err != nil {
			return tuiActionMsg{err: err}
		}
		if claim == nil {
			return tuiActionMsg{text: "Smoke OK: no pending local task"}
		}
		return tuiActionMsg{text: "Smoke OK: pending task " + shortID(claim.TaskID)}
	}
}

func (m *TUIModel) startDaemonCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		daemon, err := NewDaemon(DaemonOptions{
			BaseURL:           m.opts.BaseURL,
			APIKey:            m.opts.APIKey,
			BridgeID:          m.opts.BridgeID,
			WorkspaceRoot:     normalizedWorkspaceRoot(m.opts.WorkspaceRoot),
			Name:              firstNonEmptyString(m.opts.Name, "lab-agent-tui"),
			Hostname:          m.opts.Hostname,
			PollInterval:      m.opts.PollInterval,
			HeartbeatInterval: m.opts.HeartbeatInterval,
		})
		if err != nil {
			cancel()
			return tuiDaemonStartedMsg{err: err}
		}
		go func() {
			_ = daemon.Run(ctx)
		}()
		return tuiDaemonStartedMsg{cancel: cancel}
	}
}

func (m *TUIModel) chatCmd(prompt string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		modelID := firstNonEmptyString(firstModel(m.models), "orchestrator-tools")
		text, err := m.client.ChatText(ctx, modelID, prompt)
		return tuiChatMsg{prompt: prompt, content: text, model: modelID, err: err}
	}
}

func (m *TUIModel) createPromptTaskCmd(prompt string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req := domain.TaskCreateRequest{
			Description: fmt.Sprintf("Operator prompt: %s", prompt),
			Metadata: map[string]any{
				"entrypoint":      "tui_chat",
				"workspace_root":  normalizedWorkspaceRoot(m.opts.WorkspaceRoot),
				"original_prompt": prompt,
			},
			Priority:        domain.PriorityNormal,
			ExecutionTarget: domain.ExecutionTargetRemote,
			AssignedAgent:   ptrAgentType(domain.AgentTypePlanner),
		}
		task, err := m.client.CreateTask(ctx, req)
		return tuiCreateTaskMsg{task: task, err: err}
	}
}

func (m *TUIModel) createProjectTaskCmd(req domain.TaskCreateRequest, recent RecentProject) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		task, err := m.client.CreateTask(ctx, req)
		if err != nil {
			return tuiCreateTaskMsg{err: err}
		}
		recent.TaskID = task.ID
		recent.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		projectRequest := req.Metadata["project_request"].(map[string]any)
		return tuiCreateTaskMsg{
			task:   task,
			recent: &recent,
			presets: map[string]string{
				"parent_directory":  asString(projectRequest["parent_directory"]),
				"project_name":      asString(projectRequest["project_name"]),
				"project_type":      asString(projectRequest["project_type"]),
				"runtime_or_stack":  asString(projectRequest["runtime_or_stack"]),
				"goal":              asString(projectRequest["goal"]),
				"test_focus":        asString(projectRequest["test_focus"]),
				"initialize_git":    fmt.Sprintf("%t", projectRequest["initialize_git"]),
				"requires_approval": fmt.Sprintf("%t", req.Metadata["requires_approval"]),
			},
			mode: tuiModeNormal,
		}
	}
}

func (m *TUIModel) persistState() {
	m.state.LastView = m.currentView()
	m.state.LastWorkspace = normalizedWorkspaceRoot(m.opts.WorkspaceRoot)
	_ = m.stateStore.Save(m.state)
}

func renderTaskResults(results any) string {
	if results == nil {
		return tuiMutedStyle.Render("No result payload loaded yet.")
	}
	payload, ok := results.(map[string]any)
	if !ok {
		return fmt.Sprintf("Results: %#v", results)
	}
	lines := []string{}
	for _, key := range []string{"summary", "status", "stdout", "stderr", "error_message", "diff"} {
		value := strings.TrimSpace(fmt.Sprintf("%v", payload[key]))
		if value == "" || value == "<nil>" {
			continue
		}
		if key == "diff" || key == "stdout" || key == "stderr" {
			value = truncateMultiline(value, 8)
		}
		lines = append(lines, fmt.Sprintf("%s:\n%s", key, value))
	}
	if len(lines) == 0 {
		return tuiMutedStyle.Render("Result payload exists but has no short printable fields.")
	}
	return strings.Join(lines, "\n\n")
}

func renderTaskTree(node domain.TaskTreeResponse, depth int) string {
	prefix := strings.Repeat("  ", depth)
	lines := []string{fmt.Sprintf("%s- %s [%s / %s]", prefix, shortID(node.ID), node.State, derefAgent(node.AssignedAgent))}
	for _, child := range node.Children {
		lines = append(lines, renderTaskTree(child, depth+1))
	}
	return strings.Join(lines, "\n")
}

func renderProjectField(label, value string, selected bool) string {
	line := fmt.Sprintf("%-18s %s", label+":", firstNonEmptyString(value, "<empty>"))
	if selected {
		return tuiSelectedStyle.Render(line)
	}
	return line
}

func firstModel(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

func shortID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func truncateInline(value string, max int) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
	if len(value) <= max {
		return value
	}
	return value[:max-1] + "…"
}

func truncateMultiline(value string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxLines], "\n") + "\n..."
}

func boolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func indexOfString(items []string, value string) int {
	value = strings.TrimSpace(value)
	for idx, item := range items {
		if item == value {
			return idx
		}
	}
	return 0
}

func parseBoolString(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

func derefAgent(value *domain.AgentType) string {
	if value == nil {
		return "<none>"
	}
	return string(*value)
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func ptrAgentType(value domain.AgentType) *domain.AgentType {
	return &value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
