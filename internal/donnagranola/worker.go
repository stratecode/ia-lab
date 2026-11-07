package donnagranola

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxEventAttempts = 10

var errEventParked = errors.New("Granola event was retried or moved to failed storage")

type OperatorContext struct {
	OperatorID     int64 `json:"operator_id"`
	TelegramChatID int64 `json:"telegram_chat_id,omitempty"`
}

type Notification struct {
	ID             string          `json:"id"`
	Message        string          `json:"message"`
	Title          string          `json:"title"`
	Date           string          `json:"date,omitempty"`
	Owner          string          `json:"owner,omitempty"`
	Summary        string          `json:"summary"`
	Actions        []string        `json:"actions,omitempty"`
	TaskCandidates []GranolaAction `json:"task_candidates,omitempty"`
}

type NotificationSink interface {
	Deliver(context.Context, OperatorContext, Notification) error
}

type workerQueue interface {
	Next(context.Context) (GranolaEvent, error)
	Complete(string) error
	Retry(string, time.Duration) error
	Fail(string, error) error
}

type Worker struct {
	Queue    workerQueue
	Notes    GranolaNoteReader
	Sink     NotificationSink
	Operator OperatorContext
	OnRetry  func(eventID, category string, attempt int)
	OnFail   func(eventID, category string)
}

func (w *Worker) Process(ctx context.Context, event GranolaEvent) error {
	if w == nil || w.Queue == nil || w.Notes == nil || w.Sink == nil || event.EventID == "" {
		return errors.New("Granola worker is not configured")
	}
	note, err := w.Notes.GetNote(ctx, event.NoteID)
	if err == nil && strings.TrimSpace(note.Summary) == "" {
		err = ErrNoteNotReady
	}
	if err == nil {
		notification := Notification{ID: event.EventID, Title: note.Title, Date: note.Date, Owner: note.Owner, Summary: note.Summary, Actions: extractActionLines(note.Summary), TaskCandidates: ExtractGranolaActions(note)}
		notification.Message = FormatGranolaNotification(note)
		err = w.Sink.Deliver(ctx, w.Operator, notification)
	}
	if err != nil {
		if isRetryable(err) {
			if event.Attempts+1 >= maxEventAttempts {
				if failErr := w.Queue.Fail(event.EventID, err); failErr != nil {
					return failErr
				}
				if w.OnFail != nil {
					w.OnFail(event.EventID, failureCategory(err))
				}
				return errEventParked
			}
			if retryErr := w.Queue.Retry(event.EventID, retryDelay(event.Attempts)); retryErr != nil {
				return retryErr
			}
			if w.OnRetry != nil {
				w.OnRetry(event.EventID, failureCategory(err), event.Attempts+1)
			}
			return errEventParked
		}
		if failErr := w.Queue.Fail(event.EventID, err); failErr != nil {
			return failErr
		}
		if w.OnFail != nil {
			w.OnFail(event.EventID, failureCategory(err))
		}
		return errEventParked
	}
	if err := w.Queue.Complete(event.EventID); err != nil {
		return errors.New("cannot complete Granola event")
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	if w == nil || w.Queue == nil {
		return errors.New("Granola worker is not configured")
	}
	for {
		event, err := w.Queue.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("cannot read Granola event queue")
		}
		if err := w.Process(ctx, event); err != nil && !errors.Is(err, errEventParked) {
			return errors.New("cannot persist Granola event processing state")
		}
	}
}

func isRetryable(err error) bool {
	return errors.Is(err, ErrNoteNotReady) || errors.Is(err, ErrTemporary)
}

func failureCategory(err error) string {
	switch {
	case errors.Is(err, ErrNoteNotReady):
		return "note_not_ready"
	case errors.Is(err, ErrTemporary):
		return "temporary_dependency"
	case errors.Is(err, ErrUnauthorized):
		return "granola_authorization"
	case errors.Is(err, ErrNotificationRejected):
		return "secretary_rejected"
	default:
		return "permanent_processing_failure"
	}
}

func retryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	d := time.Minute * time.Duration(1<<min(attempt, 6))
	if d > time.Hour {
		return time.Hour
	}
	return d
}

// FormatGranolaNotification renders a short follow-up message: title, date and
// owner (explicitly marked when missing), the topics covered, and candidate
// actions. The full summary stays in Granola behind the link.
func FormatGranolaNotification(note GranolaNote) string {
	title := strings.TrimSpace(note.Title)
	if title == "" {
		title = "Reunión de Granola"
	}
	lines := []string{"**Granola · " + title + "**"}
	date := strings.TrimSpace(note.Date)
	if len(date) > 10 && date[4] == '-' && date[7] == '-' {
		date = date[:10]
	}
	if date == "" {
		date = "no indicada"
	}
	owner := strings.TrimSpace(note.Owner)
	if owner == "" {
		owner = "no indicado"
	}
	lines = append(lines, "Fecha: "+date+" · Responsable de la nota: "+owner)
	if topics := summaryTopics(note.Summary, 4); topics != "" {
		lines = append(lines, "Temas: "+topics)
	}
	actions := extractActionLines(note.Summary)
	if len(actions) > 0 {
		lines = append(lines, "Acciones candidatas a Jira (sin crear tareas):")
		shown := actions
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, action := range shown {
			lines = append(lines, "• "+truncateRunes(action, 160))
		}
		if extra := len(actions) - len(shown); extra > 0 {
			lines = append(lines, fmt.Sprintf("… y %d más en la nota.", extra))
		}
	}
	if noteURL := strings.TrimSpace(note.URL); noteURL != "" {
		lines = append(lines, "Fuente: [Abrir nota de Granola]("+noteURL+")")
	}
	return strings.Join(lines, "\n")
}

// summaryTopics lists the section headings of a summary, skipping action
// sections, and falls back to the opening line when there are none.
func summaryTopics(summary string, max int) string {
	var topics []string
	first := ""
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.Trim(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), ":* ")
			switch strings.ToLower(heading) {
			case "", "acciones", "action items", "actions", "next steps", "siguientes pasos", "próximos pasos", "proximos pasos", "siguientes acciones", "próximas acciones", "proximas acciones", "pasos siguientes":
				continue
			}
			if len(topics) < max {
				topics = append(topics, truncateRunes(heading, 70))
			}
			continue
		}
		if first == "" {
			first = strings.TrimSpace(strings.TrimLeft(trimmed, "-*• "))
		}
	}
	if len(topics) > 0 {
		return strings.Join(topics, "; ") + "."
	}
	if first != "" {
		return truncateRunes(first, 200)
	}
	return ""
}

func extractActionLines(summary string) []string {
	spans := extractActionSpans(summary)
	actions := make([]string, 0, len(spans))
	for _, span := range spans {
		actions = append(actions, truncateRunes(span, 240))
	}
	return actions
}

func ExtractGranolaActions(note GranolaNote) []GranolaAction {
	spans := extractActionSpans(note.Summary)
	actions := make([]GranolaAction, 0, len(spans))
	for _, span := range spans {
		actions = append(actions, GranolaAction{
			NoteID:        strings.TrimSpace(note.ID),
			NoteURL:       strings.TrimSpace(note.URL),
			MeetingTitle:  strings.TrimSpace(note.Title),
			SourceSpan:    span,
			SourceSummary: strings.TrimSpace(note.Summary),
		})
	}
	return actions
}

func extractActionSpans(summary string) []string {
	var actions []string
	inActions := false
	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.ToLower(strings.Trim(strings.TrimSpace(strings.TrimLeft(trimmed, "#")), ":* "))
			switch heading {
			case "acciones", "action items", "actions", "next steps", "siguientes pasos", "próximos pasos", "proximos pasos", "siguientes acciones", "próximas acciones", "proximas acciones", "pasos siguientes":
				inActions = true
			default:
				inActions = false
			}
			continue
		}
		if inActions && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "•")) {
			item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*• "))
			if item != "" {
				actions = append(actions, item)
				if len(actions) == 5 {
					break
				}
			}
		} else if inActions && trimmed == "" {
			continue
		} else if inActions {
			inActions = false
		}
	}
	return actions
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
