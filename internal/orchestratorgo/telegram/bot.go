package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/httpapi"
	"github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type Bot struct {
	cfg          config.Config
	postgres     *store.PostgresStore
	redis        *store.RedisStore
	research     *research.Service
	initiatives  *initiative.Service
	autonomous   autonomousStarter
	capabilities *capabilities.Client
	safeMode     *httpapi.SafeModeState
	client       *http.Client
	cancel       context.CancelFunc
	done         chan struct{}
	offset       int64
	mu           sync.Mutex
}

type autonomousStarter interface {
	StartFromChannel(ctx context.Context, req domain.AutonomousInitiativeRequest) (*domain.AutonomousRunResult, error)
}

func New(cfg config.Config, postgres *store.PostgresStore, redis *store.RedisStore, researchService *research.Service, initiativeService *initiative.Service, autonomousRunner autonomousStarter, capabilityClient *capabilities.Client, safeMode *httpapi.SafeModeState) *Bot {
	return &Bot{
		cfg:          cfg,
		postgres:     postgres,
		redis:        redis,
		research:     researchService,
		initiatives:  initiativeService,
		autonomous:   autonomousRunner,
		capabilities: capabilityClient,
		safeMode:     safeMode,
		client:       &http.Client{Timeout: 35 * time.Second},
		done:         make(chan struct{}),
	}
}

func (b *Bot) Enabled() bool {
	return strings.TrimSpace(b.cfg.TelegramBotToken) != "" && len(b.cfg.TelegramAllowedUsers) > 0
}

func (b *Bot) Start(parent context.Context) error {
	if !b.Enabled() {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	b.cancel = cancel
	go b.run(ctx)
	return nil
}

func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
		<-b.done
	}
}

func (b *Bot) run(ctx context.Context) {
	defer close(b.done)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := b.getUpdates(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("telegram polling failed")
			time.Sleep(5 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= b.offset {
				b.offset = update.UpdateID + 1
			}
			if update.CallbackQuery != nil {
				if !b.isAllowed(update.CallbackQuery.From.ID) {
					continue
				}
				if err := b.handleCallback(ctx, update.CallbackQuery); err != nil {
					log.Warn().Err(err).Msg("telegram callback handling failed")
				}
				continue
			}
			if update.Message == nil {
				continue
			}
			if !b.isAllowed(update.Message.From.ID) {
				continue
			}
			command := parseCommand(update.Message.Text)
			switch command {
			case "tasks":
				if err := b.sendTasksList(ctx, update.Message.Chat.ID); err != nil {
					log.Warn().Err(err).Int64("chat_id", update.Message.Chat.ID).Msg("telegram send tasks failed")
				}
				continue
			case "approvals":
				if err := b.sendApprovalsList(ctx, update.Message.Chat.ID); err != nil {
					log.Warn().Err(err).Int64("chat_id", update.Message.Chat.ID).Msg("telegram send approvals failed")
				}
				continue
			case "initiatives":
				if err := b.sendInitiativesList(ctx, update.Message.Chat.ID); err != nil {
					log.Warn().Err(err).Int64("chat_id", update.Message.Chat.ID).Msg("telegram send initiatives failed")
				}
				continue
			}
			reply := b.handleCommand(ctx, update.Message)
			if strings.TrimSpace(reply) == "" {
				continue
			}
			if err := b.sendMessage(ctx, update.Message.Chat.ID, reply); err != nil {
				log.Warn().Err(err).Int64("chat_id", update.Message.Chat.ID).Msg("telegram sendMessage failed")
			}
		}
	}
}

func (b *Bot) isAllowed(userID int64) bool {
	for _, allowed := range b.cfg.TelegramAllowedUsers {
		if allowed == userID {
			return true
		}
	}
	return false
}

func (b *Bot) getUpdates(ctx context.Context) ([]update, error) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=20&offset=%d", b.cfg.TelegramBotToken, b.offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram getUpdates failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		OK     bool     `json:"ok"`
		Result []update `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload.Result, nil
}

func (b *Bot) sendMessage(ctx context.Context, chatID int64, text string) error {
	return b.sendMessageWithMarkup(ctx, chatID, text, nil)
}

func (b *Bot) sendMessageWithMarkup(ctx context.Context, chatID int64, text string, replyMarkup map[string]any) error {
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyMarkup != nil {
		body["reply_markup"] = replyMarkup
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.cfg.TelegramBotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("telegram sendMessage failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (b *Bot) answerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{"callback_query_id": callbackID}
	if strings.TrimSpace(text) != "" {
		body["text"] = text
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", b.cfg.TelegramBotToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("telegram answerCallbackQuery failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (b *Bot) handleCommand(ctx context.Context, message *message) string {
	text := strings.TrimSpace(message.Text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return "Usa /status, /tasks, /task, /safe, /run, /plan, /idea, /autonomous, /research, /fetch, /doc, /image, /approvals, /approve, /reject, /capabilities o /sources."
	}
	fields := strings.Fields(text)
	command := strings.TrimPrefix(fields[0], "/")
	arg := ""
	if len(fields) > 1 {
		arg = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	switch command {
	case "status":
		return b.cmdStatus(ctx)
	case "tasks":
		return b.cmdTasks(ctx)
	case "task":
		return b.cmdTask(ctx, arg)
	case "safe":
		return b.cmdSafe()
	case "run":
		return b.cmdCreateTask(ctx, arg, domain.AgentTypePlanner, false)
	case "plan":
		return b.cmdCreateTask(ctx, arg, domain.AgentTypePlanner, true)
	case "initiatives":
		return b.cmdInitiatives(ctx)
	case "initiative":
		return b.cmdInitiative(ctx, arg)
	case "idea":
		return b.cmdIdea(ctx, arg)
	case "autonomous":
		return b.cmdAutonomous(ctx, arg, message.From.Username)
	case "approve_phase":
		return b.cmdResolveInitiativePhase(ctx, arg, true, message.From.Username)
	case "reject_phase":
		return b.cmdResolveInitiativePhase(ctx, arg, false, message.From.Username)
	case "launch_tasks":
		return b.cmdLaunchInitiativeTasks(ctx, arg)
	case "initiative_tasks":
		return b.cmdInitiativeTasks(ctx, arg)
	case "approvals":
		return b.cmdApprovals(ctx)
	case "approve":
		return b.cmdResolveApproval(ctx, arg, true, message.From.Username)
	case "reject":
		return b.cmdResolveApproval(ctx, arg, false, message.From.Username)
	case "cancel":
		return b.cmdCancelTask(ctx, arg)
	case "research", "web":
		return b.cmdResearch(ctx, arg)
	case "fetch":
		return b.cmdFetch(ctx, arg)
	case "doc":
		return b.cmdDocument(ctx, arg)
	case "image":
		return b.cmdImage(ctx, arg)
	case "sources":
		return b.cmdSources(ctx, arg)
	case "capabilities":
		return "Capabilities\n- web.search\n- web.fetch\n- document.read\n- image.analyze\n- research.query"
	default:
		return "Comando no soportado todavía en el bot Go. De momento usa /status, /safe, /run, /idea, /autonomous, /research, /doc, /image o /capabilities."
	}
}

func (b *Bot) cmdStatus(ctx context.Context) string {
	tasks, _, err := b.postgres.ListTasks(ctx, store.TaskListFilter{Limit: 10})
	if err != nil {
		return "Error leyendo tareas: " + err.Error()
	}
	workers, err := b.redis.ListWorkers(ctx)
	if err != nil {
		return "Error leyendo workers: " + err.Error()
	}
	approvals, err := b.postgres.ListApprovals(ctx, store.ApprovalListFilter{Status: domain.ApprovalPending, Limit: 20})
	if err != nil {
		return "Error leyendo approvals: " + err.Error()
	}
	active := 0
	for _, item := range tasks {
		if item.State != domain.TaskStateCompleted && item.State != domain.TaskStateFailed && item.State != domain.TaskStateCancelled {
			active++
		}
	}
	return fmt.Sprintf("Status\n- Active tasks: %d\n- Workers: %d\n- Pending approvals: %d", active, len(workers), len(approvals))
}

func (b *Bot) cmdTasks(ctx context.Context) string {
	items, _, err := b.postgres.ListTasks(ctx, store.TaskListFilter{Limit: 10})
	if err != nil {
		return "Error leyendo tareas: " + err.Error()
	}
	if len(items) == 0 {
		return "No hay tareas."
	}
	lines := []string{"Tasks"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s", item.ID, item.State, item.Description))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) sendTasksList(ctx context.Context, chatID int64) error {
	items, _, err := b.postgres.ListTasks(ctx, store.TaskListFilter{Limit: 10})
	if err != nil {
		return b.sendMessage(ctx, chatID, "Error leyendo tareas: "+err.Error())
	}
	if len(items) == 0 {
		return b.sendMessage(ctx, chatID, "No hay tareas.")
	}
	lines := []string{"Tasks"}
	rows := make([][]map[string]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s", item.State, item.ID, item.Description))
		rows = append(rows, []map[string]string{{
			"text":          fmt.Sprintf("%s | %s", item.State, trimForButton(item.Description, 28)),
			"callback_data": "task:" + item.ID,
		}})
	}
	return b.sendMessageWithMarkup(ctx, chatID, strings.Join(lines, "\n"), map[string]any{"inline_keyboard": rows})
}

func (b *Bot) cmdTask(ctx context.Context, taskID string) string {
	if strings.TrimSpace(taskID) == "" {
		return "Uso: /task <task_id>"
	}
	task, err := b.postgres.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return "Error leyendo tarea: " + err.Error()
	}
	if task == nil {
		return "Tarea no encontrada."
	}
	return fmt.Sprintf("Task %s\n- State: %s\n- Agent: %s\n- Priority: %s\n- Description: %s", task.ID, task.State, derefAgent(task.AssignedAgent), task.Priority, task.Description)
}

func (b *Bot) cmdSafe() string {
	if b.safeMode == nil {
		return "Safe mode no disponible."
	}
	previous := b.safeMode.Enabled()
	b.safeMode.SetEnabled(!previous)
	if previous {
		return "Safe mode -> OFF"
	}
	return "Safe mode -> ON"
}

func (b *Bot) cmdCreateTask(ctx context.Context, description string, agent domain.AgentType, planOnly bool) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "La descripción es obligatoria."
	}
	totalDepth, err := b.redis.TotalQueueDepth(ctx)
	if err != nil {
		return "Error leyendo cola: " + err.Error()
	}
	if totalDepth >= int64(b.cfg.QueueMaxGlobal) {
		return "La cola global está al límite."
	}
	agentDepth, err := b.redis.QueueDepth(ctx, agent)
	if err != nil {
		return "Error leyendo cola del agente: " + err.Error()
	}
	if agentDepth >= int64(b.cfg.QueueMaxPerAgent) {
		return "La cola del agente está al límite."
	}
	workspace := strings.TrimSpace(b.cfg.WorkspaceRoot)
	var workspacePtr *string
	if workspace != "" {
		workspacePtr = &workspace
	}
	metadata := map[string]any{"entrypoint": "telegram"}
	if planOnly {
		metadata["plan_only"] = true
	}
	task, err := b.postgres.CreateTask(ctx, store.CreateTaskParams{
		Description:     description,
		Metadata:        metadata,
		Priority:        domain.PriorityNormal,
		AssignedAgent:   agent,
		ExecutionTarget: domain.ExecutionTargetRemote,
		Entrypoint:      "telegram",
		WorkspacePath:   workspacePtr,
		QueueOnCreate:   true,
	})
	if err != nil {
		return "Error creando tarea: " + err.Error()
	}
	if err := b.redis.EnqueueTask(ctx, task.ID, agent, domain.PriorityNormal); err != nil {
		_ = b.postgres.CancelTask(ctx, task.ID, "telegram:create", "queue enqueue failed: "+err.Error())
		return "Error encolando tarea: " + err.Error()
	}
	return fmt.Sprintf("Task creada\n- ID: %s\n- Agent: %s\n- State: queued", task.ID, agent)
}

func (b *Bot) cmdInitiatives(ctx context.Context) string {
	items, err := b.postgres.ListInitiatives(ctx, store.InitiativeListFilter{Limit: 10})
	if err != nil {
		return "Error leyendo iniciativas: " + err.Error()
	}
	if len(items) == 0 {
		return "No hay iniciativas."
	}
	lines := []string{"Initiatives"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | %s", item.ID, item.Status, item.CurrentPhase, item.Title))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) sendInitiativesList(ctx context.Context, chatID int64) error {
	items, err := b.postgres.ListInitiatives(ctx, store.InitiativeListFilter{Limit: 10})
	if err != nil {
		return b.sendMessage(ctx, chatID, "Error leyendo iniciativas: "+err.Error())
	}
	if len(items) == 0 {
		return b.sendMessage(ctx, chatID, "No hay iniciativas.")
	}
	lines := []string{"Initiatives"}
	rows := make([][]map[string]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | %s", item.ID, item.Status, item.CurrentPhase, item.Title))
		rows = append(rows, []map[string]string{{
			"text":          fmt.Sprintf("%s | %s", item.Status, trimForButton(item.Title, 24)),
			"callback_data": "initiative:" + item.ID,
		}})
	}
	return b.sendMessageWithMarkup(ctx, chatID, strings.Join(lines, "\n"), map[string]any{"inline_keyboard": rows})
}

func (b *Bot) cmdInitiative(ctx context.Context, initiativeID string) string {
	initiativeID = strings.TrimSpace(initiativeID)
	if initiativeID == "" {
		return "Uso: /initiative <initiative_id>"
	}
	item, err := b.postgres.GetInitiative(ctx, initiativeID)
	if err != nil {
		return "Error leyendo iniciativa: " + err.Error()
	}
	if item == nil {
		return "Iniciativa no encontrada."
	}
	links, err := b.postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return "Error leyendo tareas de iniciativa: " + err.Error()
	}
	return formatInitiativeSummary(item, links)
}

func (b *Bot) sendInitiativeDetail(ctx context.Context, chatID int64, initiativeID string) error {
	item, err := b.postgres.GetInitiative(ctx, initiativeID)
	if err != nil {
		return b.sendMessage(ctx, chatID, "Error leyendo iniciativa: "+err.Error())
	}
	if item == nil {
		return b.sendMessage(ctx, chatID, "Iniciativa no encontrada.")
	}
	links, err := b.postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return b.sendMessage(ctx, chatID, "Error leyendo tareas de iniciativa: "+err.Error())
	}
	text := formatInitiativeSummary(item, links)
	rows := [][]map[string]string{}
	if isPhaseReviewStatus(item.Status) {
		rows = append(rows, []map[string]string{
			{"text": "Approve phase", "callback_data": fmt.Sprintf("phaseapprove:%s:%s", item.ID, item.CurrentPhase)},
			{"text": "Reject phase", "callback_data": fmt.Sprintf("phasereject:%s:%s", item.ID, item.CurrentPhase)},
		})
	}
	if item.Status == domain.InitiativeStatusExecutionReady || item.Status == domain.InitiativeStatusExecuting || item.Status == domain.InitiativeStatusBlocked {
		rows = append(rows, []map[string]string{
			{"text": "Launch tasks", "callback_data": "launchinitiative:" + item.ID},
			{"text": "List tasks", "callback_data": "initiative_tasks:" + item.ID},
		})
	}
	var markup map[string]any
	if len(rows) > 0 {
		markup = map[string]any{"inline_keyboard": rows}
	}
	return b.sendMessageWithMarkup(ctx, chatID, text, markup)
}

func (b *Bot) cmdIdea(ctx context.Context, arg string) string {
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		return "Uso: /idea <workspace_alias> <texto>"
	}
	workspaceRoot, err := b.resolveWorkspaceAlias(ctx, fields[0])
	if err != nil {
		return "Error resolviendo workspace: " + err.Error()
	}
	goal := strings.TrimSpace(strings.Join(fields[1:], " "))
	title := goal
	if len(title) > 72 {
		title = strings.TrimSpace(title[:72]) + "..."
	}
	item, err := b.postgres.CreateInitiative(ctx, store.CreateInitiativeParams{
		Title:         title,
		WorkspaceRoot: workspaceRoot,
		Goal:          goal,
		CreatedBy:     "telegram",
		ExecutionMode: domain.InitiativeExecutionModeSelective,
	})
	if err != nil {
		return "Error creando iniciativa: " + err.Error()
	}
	return fmt.Sprintf("Iniciativa creada\n- ID: %s\n- Workspace: %s\n- Status: %s", item.ID, item.WorkspaceRoot, item.Status)
}

func (b *Bot) cmdAutonomous(ctx context.Context, arg string, operator string) string {
	if b.autonomous == nil {
		return "El runner autónomo no está configurado."
	}
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		return "Uso: /autonomous <workspace_alias> <objetivo>"
	}
	workspaceAlias := strings.TrimSpace(fields[0])
	workspaceRoot, err := b.resolveWorkspaceAlias(ctx, workspaceAlias)
	if err != nil {
		return "Error resolviendo workspace: " + err.Error()
	}
	result, err := b.autonomous.StartFromChannel(ctx, domain.AutonomousInitiativeRequest{
		Surface:           "openclaw.telegram",
		WorkspaceAlias:    workspaceAlias,
		WorkspaceRoot:     workspaceRoot,
		Goal:              strings.TrimSpace(strings.Join(fields[1:], " ")),
		OperatorID:        strings.TrimSpace(operator),
		AutoApprovePhases: true,
	})
	if err != nil {
		return "Error lanzando iniciativa autónoma: " + err.Error()
	}
	return result.Summary
}

func (b *Bot) cmdResolveInitiativePhase(ctx context.Context, arg string, approve bool, operator string) string {
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) < 2 {
		if approve {
			return "Uso: /approve_phase <initiative_id> <requirements|design|plan>"
		}
		return "Uso: /reject_phase <initiative_id> <requirements|design|plan>"
	}
	initiativeID := strings.TrimSpace(fields[0])
	phase := domain.InitiativePhase(strings.TrimSpace(fields[1]))
	item, err := b.postgres.GetInitiative(ctx, initiativeID)
	if err != nil {
		return "Error leyendo iniciativa: " + err.Error()
	}
	if item == nil {
		return "Iniciativa no encontrada."
	}
	if !domain.IsRecognizedInitiativePhase(phase) {
		return "Fase inválida."
	}
	if item.CurrentPhase != phase && !(phase == domain.InitiativePhasePlan && item.Status == domain.InitiativeStatusPlanReview) {
		return fmt.Sprintf("La iniciativa está en %s / %s, no en la fase pedida.", item.Status, item.CurrentPhase)
	}
	activeJSONID := activeArtifactJSONIDForPhaseTelegram(item, phase)
	if activeJSONID == nil || strings.TrimSpace(*activeJSONID) == "" {
		return "La fase no tiene artifact activo para revisar."
	}
	payload, err := b.activeInitiativeArtifactPayload(ctx, *activeJSONID)
	if err != nil {
		return "Error leyendo artifact activo: " + err.Error()
	}
	if err := validateInitiativePhasePayloadTelegram(phase, payload); err != nil {
		return "El artifact activo no cumple el contrato mínimo: " + err.Error()
	}
	if approve && phase == domain.InitiativePhasePlan {
		links, err := b.postgres.ListInitiativeTasks(ctx, initiativeID)
		if err != nil {
			return "Error leyendo backlog: " + err.Error()
		}
		if len(links) == 0 {
			return "No se puede aprobar el plan sin backlog materializado."
		}
	}
	activeMarkdownID, activeJSONID := activeArtifactIDsForPhase(item, phase)
	decision := domain.InitiativeReviewRejected
	if approve {
		decision = domain.InitiativeReviewApproved
	}
	if strings.TrimSpace(operator) == "" {
		operator = "telegram"
	}
	if _, err := b.postgres.CreateInitiativePhaseReview(ctx, store.CreateInitiativePhaseReviewParams{
		InitiativeID:       item.ID,
		Phase:              phase,
		Decision:           decision,
		GeneratedBy:        &operator,
		ArtifactMarkdownID: activeMarkdownID,
		ArtifactJSONID:     activeJSONID,
	}); err != nil {
		return "Error registrando review de fase: " + err.Error()
	}
	var nextStatus domain.InitiativeStatus
	var nextPhase domain.InitiativePhase
	if approve {
		switch phase {
		case domain.InitiativePhaseRequirements:
			nextStatus = domain.InitiativeStatusDesignDraft
			nextPhase = domain.InitiativePhaseDesign
		case domain.InitiativePhaseDesign:
			nextStatus = domain.InitiativeStatusPlanDraft
			nextPhase = domain.InitiativePhasePlan
		case domain.InitiativePhasePlan:
			nextStatus = domain.InitiativeStatusExecutionReady
			nextPhase = domain.InitiativePhaseExecution
		default:
			return "Fase no aprobable."
		}
	} else {
		switch phase {
		case domain.InitiativePhaseRequirements:
			nextStatus = domain.InitiativeStatusRequirementsDraft
			nextPhase = domain.InitiativePhaseRequirements
		case domain.InitiativePhaseDesign:
			nextStatus = domain.InitiativeStatusDesignDraft
			nextPhase = domain.InitiativePhaseDesign
		case domain.InitiativePhasePlan:
			nextStatus = domain.InitiativeStatusPlanDraft
			nextPhase = domain.InitiativePhasePlan
		default:
			return "Fase no rechazable."
		}
	}
	if _, err := b.postgres.UpdateInitiative(ctx, item.ID, store.UpdateInitiativeParams{
		Status:       &nextStatus,
		CurrentPhase: &nextPhase,
	}); err != nil {
		return "Error actualizando iniciativa: " + err.Error()
	}
	if approve {
		return fmt.Sprintf("Phase approved\n- Initiative: %s\n- Next: %s", item.ID, nextStatus)
	}
	return fmt.Sprintf("Phase rejected\n- Initiative: %s\n- Back to: %s", item.ID, nextStatus)
}

func (b *Bot) cmdInitiativeTasks(ctx context.Context, initiativeID string) string {
	initiativeID = strings.TrimSpace(initiativeID)
	if initiativeID == "" {
		return "Uso: /initiative_tasks <initiative_id>"
	}
	items, err := b.postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return "Error leyendo tareas de iniciativa: " + err.Error()
	}
	if len(items) == 0 {
		return "La iniciativa no tiene tareas generadas."
	}
	lines := []string{"Initiative tasks"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s | %s", item.TaskID, item.Task.State, item.ExecutionMode, item.Task.Description))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdLaunchInitiativeTasks(ctx context.Context, initiativeID string) string {
	initiativeID = strings.TrimSpace(initiativeID)
	if initiativeID == "" {
		return "Uso: /launch_tasks <initiative_id>"
	}
	item, err := b.postgres.GetInitiative(ctx, initiativeID)
	if err != nil {
		return "Error leyendo iniciativa: " + err.Error()
	}
	if item == nil {
		return "Iniciativa no encontrada."
	}
	if item.Status != domain.InitiativeStatusExecutionReady && item.Status != domain.InitiativeStatusExecuting && item.Status != domain.InitiativeStatusBlocked {
		return fmt.Sprintf("La iniciativa no está lista para lanzar tareas. Estado actual: %s", item.Status)
	}
	links, err := b.postgres.ListInitiativeTasks(ctx, initiativeID)
	if err != nil {
		return "Error leyendo tareas de iniciativa: " + err.Error()
	}
	policy := initiative.ResolveExecutionPolicy(b.cfg.WorkspaceRoot, item.WorkspaceRoot)
	launched := 0
	for _, link := range links {
		if strings.TrimSpace(link.ExecutionMode) == domain.TaskLaunchModeManual {
			continue
		}
		if err := initiative.ValidateTaskLaunchAgainstPolicy(policy, link, link.ExecutionMode); err != nil {
			return "Política de ejecución: " + err.Error()
		}
		if err := b.postgres.UpdateTaskLaunchMode(ctx, link.TaskID, link.ExecutionMode); err != nil {
			return "Error preparando tarea: " + err.Error()
		}
		task, err := b.postgres.QueueTaskForLaunch(ctx, link.TaskID, "telegram:initiative_launch", "Queued from Telegram")
		if err != nil {
			return "Error lanzando tarea: " + err.Error()
		}
		if task.ExecutionTarget != domain.ExecutionTargetLocal && task.AssignedAgent != nil {
			if err := b.redis.EnqueueTask(ctx, task.ID, *task.AssignedAgent, task.Priority); err != nil {
				return "Error encolando tarea remota: " + err.Error()
			}
		}
		launched++
	}
	if _, err := b.postgres.ReconcileInitiativeExecution(ctx, initiativeID); err != nil {
		return "Error actualizando iniciativa: " + err.Error()
	}
	return fmt.Sprintf("Initiative launch queued\n- Initiative: %s\n- Tasks launched: %d", initiativeID, launched)
}

func (b *Bot) resolveWorkspaceAlias(ctx context.Context, alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	switch alias {
	case "remote", "server":
		return strings.TrimSpace(b.cfg.WorkspaceRoot), nil
	}
	if strings.HasPrefix(alias, "/") {
		return alias, nil
	}
	bridges, err := b.postgres.ListLocalBridges(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range bridges {
		if alias == item.Name || alias == item.ID || alias == "local" {
			return item.WorkspaceRoot, nil
		}
	}
	return "", fmt.Errorf("workspace alias %q not found", alias)
}

func (b *Bot) cmdApprovals(ctx context.Context) string {
	items, err := b.postgres.ListApprovals(ctx, store.ApprovalListFilter{Status: domain.ApprovalPending, Limit: 20})
	if err != nil {
		return "Error leyendo approvals: " + err.Error()
	}
	if len(items) == 0 {
		return "No hay approvals pendientes."
	}
	lines := []string{"Pending approvals"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | task=%s | %s | %s", item.ID, item.TaskID, item.ActionType, item.TargetResource))
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) sendApprovalsList(ctx context.Context, chatID int64) error {
	items, err := b.postgres.ListApprovals(ctx, store.ApprovalListFilter{Status: domain.ApprovalPending, Limit: 20})
	if err != nil {
		return b.sendMessage(ctx, chatID, "Error leyendo approvals: "+err.Error())
	}
	if len(items) == 0 {
		return b.sendMessage(ctx, chatID, "No hay approvals pendientes.")
	}
	lines := []string{"Pending approvals"}
	rows := make([][]map[string]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | task=%s | %s | %s", item.ID, item.TaskID, item.ActionType, item.TargetResource))
		rows = append(rows, []map[string]string{
			{"text": "Approve " + trimForButton(item.TargetResource, 18), "callback_data": "approve:" + item.ID},
			{"text": "Reject " + trimForButton(item.TargetResource, 18), "callback_data": "reject:" + item.ID},
		})
	}
	return b.sendMessageWithMarkup(ctx, chatID, strings.Join(lines, "\n"), map[string]any{"inline_keyboard": rows})
}

func (b *Bot) cmdResolveApproval(ctx context.Context, approvalID string, approve bool, operator string) string {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		if approve {
			return "Uso: /approve <approval_id>"
		}
		return "Uso: /reject <approval_id>"
	}
	if strings.TrimSpace(operator) == "" {
		operator = "telegram"
	}
	approval, task, err := b.postgres.ResolveApproval(ctx, approvalID, approve, operator)
	if err != nil {
		return "Error resolviendo approval: " + err.Error()
	}
	if approve && task != nil && task.AssignedAgent != nil && task.ExecutionTarget != domain.ExecutionTargetLocal {
		if err := b.redis.EnqueueTask(ctx, task.ID, *task.AssignedAgent, task.Priority); err != nil {
			return "Approval resuelta, pero falló el requeue: " + err.Error()
		}
	}
	if task != nil {
		b.reconcileRootTask(ctx, task.ID)
	}
	return fmt.Sprintf("Approval %s -> %s", approval.ID, approval.Status)
}

func (b *Bot) cmdCancelTask(ctx context.Context, taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "Uso: /cancel <task_id>"
	}
	task, err := b.postgres.GetTask(ctx, taskID)
	if err != nil {
		return "Error leyendo tarea: " + err.Error()
	}
	if task == nil {
		return "Tarea no encontrada."
	}
	if task.State != domain.TaskStateCreated && task.State != domain.TaskStateQueued && task.State != domain.TaskStateAssigned && task.State != domain.TaskStateInProgress && task.State != domain.TaskStateWaitingApproval {
		return "La tarea no está en un estado cancelable."
	}
	if err := b.postgres.CancelTask(ctx, taskID, "telegram:cancel", "cancelled by telegram"); err != nil {
		return "Error cancelando tarea: " + err.Error()
	}
	b.reconcileRootTask(ctx, taskID)
	return fmt.Sprintf("Task %s cancelada.", taskID)
}

func (b *Bot) cmdResearch(ctx context.Context, query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Uso: /research <consulta>"
	}
	result, err := b.research.Query(ctx, query)
	if err != nil {
		return "Error en research: " + err.Error()
	}
	lines := []string{result.Answer}
	if result.Confidence > 0 {
		lines = append(lines, fmt.Sprintf("\nConfidence: %.2f", result.Confidence))
	}
	if len(result.Sources) > 0 {
		lines = append(lines, "\nSources:")
		for _, src := range result.Sources[:minInt(5, len(result.Sources))] {
			lines = append(lines, fmt.Sprintf("- %s: %s", firstNonEmpty(src.Title, src.URI), src.URI))
		}
	}
	return strings.Join(lines, "\n")
}

func (b *Bot) cmdFetch(ctx context.Context, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "Uso: /fetch <url>"
	}
	source, content, err := b.research.WebFetch(ctx, rawURL)
	if err != nil {
		return "Error leyendo URL: " + err.Error()
	}
	return fmt.Sprintf("%s\n\n%s", source.Title, truncate(strings.TrimSpace(content), 1200))
}

func (b *Bot) cmdDocument(ctx context.Context, location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return "Uso: /doc <ruta_o_url>"
	}
	if b.capabilities == nil {
		return "El sidecar de documentos no está configurado."
	}
	result, err := b.capabilities.ReadDocument(ctx, location)
	if err != nil {
		return "Error leyendo documento: " + err.Error()
	}
	return result.Summary
}

func (b *Bot) cmdImage(ctx context.Context, location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return "Uso: /image <ruta_o_url>"
	}
	if b.capabilities == nil {
		return "El sidecar de imágenes no está configurado."
	}
	result, err := b.capabilities.AnalyzeImage(ctx, location)
	if err != nil {
		return "Error analizando imagen: " + err.Error()
	}
	return result.Summary
}

func (b *Bot) cmdSources(ctx context.Context, taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "Uso: /sources <task_id>"
	}
	items, err := b.postgres.ListArtifactsByTask(ctx, taskID)
	if err != nil {
		return "Error leyendo fuentes: " + err.Error()
	}
	if len(items) == 0 {
		return "La tarea no tiene artifacts."
	}
	lines := []string{"Sources"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s | %s | %s", item.ID, item.ArtifactType, firstNonEmpty(stringValue(item.Title), stringValue(item.URI))))
	}
	return strings.Join(lines, "\n")
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
	From    struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
}

func derefAgent(agent *domain.AgentType) string {
	if agent == nil {
		return ""
	}
	return string(*agent)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func trimForButton(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return text[:max-3] + "..."
}

func activeArtifactIDsForPhase(item *domain.InitiativeResponse, phase domain.InitiativePhase) (*string, *string) {
	switch phase {
	case domain.InitiativePhaseRequirements:
		return item.ActiveRequirementsArtifactID, item.ActiveRequirementsArtifactID
	case domain.InitiativePhaseDesign:
		return item.ActiveDesignArtifactID, item.ActiveDesignArtifactID
	case domain.InitiativePhasePlan:
		return item.ActivePlanArtifactID, item.ActivePlanArtifactID
	default:
		return nil, nil
	}
}

func activeArtifactJSONIDForPhaseTelegram(item *domain.InitiativeResponse, phase domain.InitiativePhase) *string {
	switch phase {
	case domain.InitiativePhaseRequirements:
		return item.ActiveRequirementsArtifactID
	case domain.InitiativePhaseDesign:
		return item.ActiveDesignArtifactID
	case domain.InitiativePhasePlan:
		return item.ActivePlanArtifactID
	default:
		return nil
	}
}

func (b *Bot) activeInitiativeArtifactPayload(ctx context.Context, artifactID string) (map[string]any, error) {
	items, err := b.postgres.ListArtifactsByIDs(ctx, []string{strings.TrimSpace(artifactID)})
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("artifact not found")
	}
	artifact := items[0]
	var payload map[string]any
	if raw := strings.TrimSpace(stringValue(artifact.ContentText)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &payload); err == nil && len(payload) > 0 {
			return payload, nil
		}
	}
	if metaPayload, ok := artifact.Metadata["payload"].(map[string]any); ok {
		return metaPayload, nil
	}
	return nil, fmt.Errorf("artifact JSON payload is missing")
}

func validateInitiativePhasePayloadTelegram(phase domain.InitiativePhase, payload map[string]any) error {
	switch phase {
	case domain.InitiativePhaseRequirements:
		return initiative.ValidateRequirementsPayload(payload)
	case domain.InitiativePhaseDesign:
		return initiative.ValidateDesignPayload(payload)
	case domain.InitiativePhasePlan:
		return initiative.ValidateExecutionPlanPayload(payload)
	default:
		return fmt.Errorf("phase %s is not reviewable", phase)
	}
}

func formatInitiativeSummary(item *domain.InitiativeResponse, links []domain.InitiativeTaskLinkResponse) string {
	backlog := "no"
	if len(links) > 0 {
		backlog = "sí"
	}
	modeCounts := map[string]int{}
	for _, link := range links {
		modeCounts[strings.TrimSpace(link.ExecutionMode)]++
	}
	activeArtifacts := "none"
	switch {
	case item.ActivePlanArtifactID != nil:
		activeArtifacts = "plan"
	case item.ActiveDesignArtifactID != nil:
		activeArtifacts = "design"
	case item.ActiveRequirementsArtifactID != nil:
		activeArtifacts = "requirements"
	}
	return fmt.Sprintf(
		"Initiative %s\n- Status: %s\n- Phase: %s\n- Workspace: %s\n- Goal: %s\n- Active artifact: %s\n- Backlog: %s\n- Tasks: %d\n- Modes: manual=%d local=%d remote=%d",
		item.ID,
		item.Status,
		item.CurrentPhase,
		item.WorkspaceRoot,
		item.Goal,
		activeArtifacts,
		backlog,
		len(links),
		modeCounts[domain.TaskLaunchModeManual],
		modeCounts[domain.TaskLaunchModeAgentLocal],
		modeCounts[domain.TaskLaunchModeAgentRemote],
	)
}

func isPhaseReviewStatus(status domain.InitiativeStatus) bool {
	switch status {
	case domain.InitiativeStatusRequirementsReview, domain.InitiativeStatusDesignReview, domain.InitiativeStatusPlanReview:
		return true
	default:
		return false
	}
}

func parseCommand(text string) string {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[0], "/")
}

func (b *Bot) handleCallback(ctx context.Context, query *callbackQuery) error {
	if query == nil || query.Message == nil {
		return nil
	}
	data := strings.TrimSpace(query.Data)
	if data == "" {
		return b.answerCallbackQuery(ctx, query.ID, "")
	}
	var reply string
	switch {
	case strings.HasPrefix(data, "initiative:"):
		if err := b.sendInitiativeDetail(ctx, query.Message.Chat.ID, strings.TrimSpace(strings.TrimPrefix(data, "initiative:"))); err != nil {
			return err
		}
		return b.answerCallbackQuery(ctx, query.ID, "Cargada")
	case strings.HasPrefix(data, "phaseapprove:"):
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(data, "phaseapprove:")), ":")
		if len(parts) == 2 {
			reply = b.cmdResolveInitiativePhase(ctx, parts[0]+" "+parts[1], true, query.From.Username)
		}
	case strings.HasPrefix(data, "phasereject:"):
		parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(data, "phasereject:")), ":")
		if len(parts) == 2 {
			reply = b.cmdResolveInitiativePhase(ctx, parts[0]+" "+parts[1], false, query.From.Username)
		}
	case strings.HasPrefix(data, "launchinitiative:"):
		reply = b.cmdLaunchInitiativeTasks(ctx, strings.TrimSpace(strings.TrimPrefix(data, "launchinitiative:")))
	case strings.HasPrefix(data, "initiative_tasks:"):
		reply = b.cmdInitiativeTasks(ctx, strings.TrimSpace(strings.TrimPrefix(data, "initiative_tasks:")))
	case strings.HasPrefix(data, "task:"):
		reply = b.cmdTask(ctx, strings.TrimSpace(strings.TrimPrefix(data, "task:")))
	case strings.HasPrefix(data, "approve:"):
		reply = b.cmdResolveApproval(ctx, strings.TrimSpace(strings.TrimPrefix(data, "approve:")), true, query.From.Username)
	case strings.HasPrefix(data, "reject:"):
		reply = b.cmdResolveApproval(ctx, strings.TrimSpace(strings.TrimPrefix(data, "reject:")), false, query.From.Username)
	default:
		reply = "Acción no soportada."
	}
	if strings.TrimSpace(reply) != "" {
		if err := b.sendMessage(ctx, query.Message.Chat.ID, reply); err != nil {
			return err
		}
	}
	return b.answerCallbackQuery(ctx, query.ID, "Hecho")
}

func (b *Bot) reconcileRootTask(ctx context.Context, taskID string) {
	task, err := b.postgres.GetTask(ctx, taskID)
	if err != nil || task == nil {
		return
	}
	if task.InitiativeID != nil && strings.TrimSpace(*task.InitiativeID) != "" {
		_, _ = b.postgres.ReconcileInitiativeExecution(ctx, strings.TrimSpace(*task.InitiativeID))
	}
	rootID := task.ID
	if task.RootTaskID != nil && strings.TrimSpace(*task.RootTaskID) != "" {
		rootID = strings.TrimSpace(*task.RootTaskID)
	}
	if rootID == task.ID && task.ParentTaskID == nil {
		return
	}
	tasks, err := b.postgres.ListByRoot(ctx, rootID)
	if err != nil || len(tasks) == 0 {
		return
	}
	var root *domain.TaskResponse
	children := make([]domain.TaskResponse, 0, len(tasks))
	for _, item := range tasks {
		item := item
		if item.ID == rootID {
			root = &item
			continue
		}
		children = append(children, item)
	}
	if root == nil || len(children) == 0 {
		return
	}
	hasFailed := false
	allCompleted := true
	for _, child := range children {
		switch child.State {
		case domain.TaskStateFailed, domain.TaskStateCancelled:
			hasFailed = true
		case domain.TaskStateCompleted:
		default:
			allCompleted = false
		}
	}
	if hasFailed {
		if !domain.IsTerminalState(root.State) {
			_, _ = b.postgres.FailTask(ctx, root.ID, "telegram:aggregate", "One or more subtasks failed", "One or more subtasks failed", root.Results)
		}
		return
	}
	if allCompleted && root.State != domain.TaskStateCompleted {
		_, _ = b.postgres.CompleteTask(ctx, root.ID, "telegram:aggregate", "All subtasks completed", root.Results)
	}
}

func parseChatID(input string) int64 {
	value, _ := strconv.ParseInt(strings.TrimSpace(input), 10, 64)
	return value
}
