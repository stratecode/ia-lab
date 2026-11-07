package secretary

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

func (b *Bot) processJiraCallback(ctx context.Context, query *CallbackQuery) error {
	if query == nil || strings.TrimSpace(query.ID) == "" || len(query.ID) > 200 {
		return nil
	}
	if query.Message == nil || b.Service == nil || !b.Service.Permitted(Message{From: query.From, Chat: query.Message.Chat}) {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Esta acción no está autorizada."}, nil)
		b.event("telegram_rejected")
		return nil
	}
	parts := strings.Split(query.Data, ":")
	if len(parts) == 3 && parts[0] == "djp" {
		return b.startGranolaJiraProposal(ctx, query, parts[1], parts[2])
	}
	if len(parts) != 2 || (parts[0] != "dja" && parts[0] != "djn" && parts[0] != "djc") || !safeActionID(parts[1]) || b.Service.JiraActions == nil {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "La acción no es válida o ha caducado."}, nil)
		return nil
	}
	principal := JiraActionPrincipal{OperatorID: query.From.ID, Channel: "telegram", ConversationID: fmt.Sprint(query.Message.Chat.ID)}
	preview, err := b.Service.JiraActions.Preview(ctx, principal, parts[1])
	if err != nil || (preview.Kind != "create" && preview.Kind != "comment") {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "No se pudo verificar la propuesta."}, nil)
		return nil
	}
	if parts[0] == "djn" || parts[0] == "djc" {
		if parts[0] == "djn" && preview.Kind != "comment" {
			_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Esta propuesta no es una coincidencia."}, nil)
			return nil
		}
		if err := b.Service.JiraActions.Cancel(ctx, principal, preview.ActionID, preview.PayloadDigest); err != nil {
			_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "No se pudo cancelar la propuesta."}, nil)
			return nil
		}
		if parts[0] == "djn" {
			_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Coincidencia descartada."}, nil)
			return b.send(ctx, query.Message.Chat.ID, "De acuerdo: no añadiré el comentario. Si quieres crear una tarea nueva, pídemelo y revisaré la propuesta con ese criterio.")
		}
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Propuesta descartada."}, nil)
		return b.send(ctx, query.Message.Chat.ID, "Propuesta descartada. No he escrito nada en Jira.")
	}
	_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Aplicando la propuesta confirmada…"}, nil)
	receipt, err := b.Service.JiraActions.Apply(ctx, principal, preview.ActionID, preview.PayloadDigest)
	if err != nil {
		b.event("jira_action_unverified")
		return b.send(ctx, query.Message.Chat.ID, "No he podido verificar el resultado en Jira. No vuelvas a pulsar; revisaré el estado antes de reintentar.")
	}
	b.event("jira_action_verified")
	message := "Confirmado en Jira: " + receipt.IssueKey
	if receipt.IssueURL != "" {
		message += "\n" + receipt.IssueURL
	}
	return b.send(ctx, query.Message.Chat.ID, message)
}

func (b *Bot) startGranolaJiraProposal(ctx context.Context, query *CallbackQuery, notificationID, rawIndex string) error {
	if b.Service == nil || b.Service.GranolaNotifications == nil || b.Service.JiraActions == nil || !granolaNotificationID.MatchString(notificationID) || len(notificationID)+len(rawIndex)+5 > 64 {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "No se pudo recuperar la propuesta de Granola."}, nil)
		return nil
	}
	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 || index >= 5 {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "La propuesta no es válida."}, nil)
		return nil
	}
	stored, found, err := b.Service.GranolaNotifications.Get(query.From.ID, notificationID)
	if err != nil || !found || index >= len(stored.TaskCandidates) {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "La propuesta ya no está disponible."}, nil)
		return nil
	}
	source := stored.TaskCandidates[index]
	candidate := TaskCandidate{GranolaNoteID: source.NoteID, NoteURL: source.NoteURL, MeetingTitle: source.MeetingTitle, SourceSummary: source.SourceSummary, SourceSpan: source.SourceSpan, Summary: source.SourceSpan}
	principal := JiraActionPrincipal{OperatorID: query.From.ID, Channel: "telegram", ConversationID: strconv.FormatInt(query.Message.Chat.ID, 10)}
	result, err := b.Service.StartJiraTaskProposal(ctx, principal, candidate)
	if err != nil {
		_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "No pude preparar la propuesta."}, nil)
		return b.send(ctx, query.Message.Chat.ID, "No he podido preparar la propuesta de Jira. No se ha escrito nada.")
	}
	_ = b.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": query.ID, "text": "Propuesta preparada."}, nil)
	if result.Preview != nil {
		return b.sendJiraActionPreview(ctx, query.Message.Chat.ID, *result.Preview)
	}
	return b.send(ctx, query.Message.Chat.ID, result.Message)
}

func (b *Bot) sendGranolaNotification(ctx context.Context, chatID int64, notification GranolaNotification) error {
	markup := map[string]any{}
	var rows [][]map[string]string
	if len(notification.TaskCandidates) > 0 && len(notification.ID) <= 55 {
		for index, candidate := range notification.TaskCandidates {
			if strings.TrimSpace(candidate.SourceSpan) == "" {
				continue
			}
			text := "Proponer en Jira: " + candidate.SourceSpan
			if len([]rune(text)) > 60 {
				text = string([]rune(text)[:57]) + "…"
			}
			rows = append(rows, []map[string]string{{"text": text, "callback_data": "djp:" + notification.ID + ":" + strconv.Itoa(index)}})
		}
	}
	payload := map[string]any{"chat_id": chatID, "text": notification.Message}
	if len(rows) > 0 {
		markup["inline_keyboard"] = rows
		payload["reply_markup"] = markup
	}
	return b.call(ctx, "sendMessage", payload, nil)
}

func (b *Bot) sendJiraActionPreview(ctx context.Context, chatID int64, preview JiraActionPreview) error {
	if chatID <= 0 || !safeActionID(preview.ActionID) || preview.PayloadDigest == "" || (preview.Kind != "create" && preview.Kind != "comment") {
		return ErrJiraActionsUnavailable
	}
	var lines []string
	if preview.Kind == "create" {
		lines = append(lines, "Propuesta de tarea Jira · "+preview.ProjectKey+" / "+preview.IssueType, preview.Summary)
		if strings.TrimSpace(preview.Description) != "" {
			lines = append(lines, "\n"+preview.Description)
		}
	} else {
		lines = append(lines, "Coincidencia Jira · "+preview.IssueKey+" · "+preview.IssueSummary, "Comentario exacto que se añadirá:", preview.Comment)
	}
	if strings.TrimSpace(preview.SourceSpan) != "" {
		lines = append(lines, "\nEvidencia de Granola: "+preview.SourceSpan)
	}
	if strings.TrimSpace(preview.NoteURL) != "" {
		lines = append(lines, "Fuente: "+preview.NoteURL)
	}
	label := "Crear en Jira"
	if preview.Kind == "comment" {
		label = "Actualizar tarea"
	}
	buttons := []map[string]string{{"text": label, "callback_data": "dja:" + preview.ActionID}}
	if preview.Kind == "comment" {
		buttons = append(buttons, map[string]string{"text": "No es la misma tarea", "callback_data": "djn:" + preview.ActionID})
	}
	buttons = append(buttons, map[string]string{"text": "Descartar propuesta", "callback_data": "djc:" + preview.ActionID})
	markup := map[string]any{"inline_keyboard": [][]map[string]string{buttons}}
	return b.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": strings.Join(lines, "\n"), "reply_markup": markup}, nil)
}
