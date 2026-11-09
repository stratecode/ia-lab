package secretary

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CheckinSender sends one check-in to the operator's private chat and returns the
// channel's message id, which is how a later reply is tied to the question.
type CheckinSender interface {
	SendCheckin(ctx context.Context, chatID int64, text string) (int64, ProactiveSendOutcome)
}

// CheckinScheduler asks the operator, once an hour on weekdays, what they are
// doing and what they are putting off. Questions are fixed text: there is no
// model call, so a check-in can never invent anything. It has its own budget,
// separate from scheduled-skill notifications.
type CheckinScheduler struct {
	Journal  *WorkJournal
	Sender   CheckinSender
	ChatID   int64
	Now      func() time.Time
	Interval time.Duration
	Event    func(string)
}

var checkinQuestions = []string{
	"¿Qué estás haciendo ahora mismo y qué se te está quedando sin tocar?",
	"Pausa de una hora: ¿en qué has estado y qué has ido dejando para después?",
	"¿En qué andas ahora? ¿Algo que debías hacer y no estás haciendo?",
}

func checkinText(slot string) string {
	n := 0
	for _, r := range slot {
		n += int(r)
	}
	return "🗒 " + checkinQuestions[n%len(checkinQuestions)] + "\n\nResponde a este mensaje (con texto). «/diario pausa 2h» o «/diario parar» si ahora no."
}

func (c *CheckinScheduler) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CheckinScheduler) event(name string) {
	if c.Event != nil {
		c.Event(name)
	}
}

func (c *CheckinScheduler) Run(ctx context.Context) {
	if c.Journal == nil || c.Sender == nil || c.ChatID == 0 {
		return
	}
	interval := c.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	c.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.RunOnce(ctx)
		}
	}
}

func (c *CheckinScheduler) RunOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := c.now()
	c.Journal.Expire(now)
	if answered, due := c.Journal.DoneNotice(now); due {
		text := fmt.Sprintf("Ya han pasado las dos semanas de check-ins: tengo %d apuntes tuyos. No te pregunto más. Si quieres que los revise y te diga qué he aprendido de cómo trabajas, pídemelo; «/diario ver» los muestra y «/diario olvidar confirmar» los borra.", answered)
		if _, outcome := c.Sender.SendCheckin(ctx, c.ChatID, text); outcome != SendFailed {
			_ = c.Journal.MarkDoneNotified()
			c.event("checkin_window_done")
		}
		return
	}
	loc, err := time.LoadLocation(proactiveTimezone)
	if err != nil {
		c.event("checkin_state_error")
		return
	}
	slot, ok := c.Journal.SlotDue(now, loc)
	if !ok {
		return
	}
	id, outcome := c.Sender.SendCheckin(ctx, c.ChatID, checkinText(slot))
	switch outcome {
	case SendDelivered:
		if c.Journal.Asked(slot, id, now) != nil {
			c.event("checkin_state_error")
			return
		}
		c.event("checkin_sent")
	case SendUncertain:
		// May have arrived: never repeat the slot, but no reply can be tied to it.
		_ = c.Journal.Asked(slot, 0, now)
		c.event("checkin_uncertain")
	default:
		// Not accepted: the next minute tries again until the slot's grace ends.
		c.event("checkin_failed")
	}
}

// journalCommand handles /diario. It works on every channel and is the operator's
// control surface: status, notes, pause, stop, resume, view and wipe.
func (s *Service) journalCommand(text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 || strings.ToLower(fields[0]) != "/diario" {
		return "", false
	}
	if s.Journal == nil {
		return "Los check-ins de trabajo no están activados.", true
	}
	now := time.Now()
	loc, _ := time.LoadLocation(proactiveTimezone)
	if loc == nil {
		loc = time.UTC
	}
	if len(fields) == 1 || strings.ToLower(fields[1]) == "estado" {
		return journalStatusText(s.Journal.Status(now, 0), loc), true
	}
	switch strings.ToLower(fields[1]) {
	case "pausa":
		d := time.Hour
		if len(fields) > 2 {
			parsed, ok := parseJournalPause(fields[2])
			if !ok {
				return "Indica la pausa así: «/diario pausa 2h» o «/diario pausa 30m» (hasta 24 h).", true
			}
			d = parsed
		}
		until, err := s.Journal.Pause(d, now)
		if err != nil {
			return "No he podido pausar los check-ins.", true
		}
		return "Pausados hasta las " + until.In(loc).Format("15:04") + ".", true
	case "parar":
		if s.Journal.Stop() != nil {
			return "No he podido parar los check-ins.", true
		}
		return "Check-ins parados. Lo ya apuntado se queda; «/diario reanudar» los reactiva.", true
	case "reanudar":
		if s.Journal.Resume() != nil {
			return "No he podido reanudar los check-ins.", true
		}
		return "Check-ins reanudados.", true
	case "ver":
		return journalViewText(s.Journal.Status(now, 10), loc), true
	case "olvidar":
		if len(fields) < 3 || strings.ToLower(fields[2]) != "confirmar" {
			return "Esto borra todo lo que he apuntado de cómo trabajas y no se puede deshacer. Si quieres, escribe «/diario olvidar confirmar».", true
		}
		if s.Journal.Forget() != nil {
			return "No he podido borrar el diario.", true
		}
		return "Diario borrado. No queda nada apuntado.", true
	}
	note := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), fields[0]))
	if err := s.Journal.Note(note, now); err != nil {
		return "No he podido apuntar eso.", true
	}
	return "Anotado.", true
}

func parseJournalPause(word string) (time.Duration, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	if len(word) < 2 {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(word[:len(word)-1], "%d", &n); err != nil || n <= 0 {
		return 0, false
	}
	switch word[len(word)-1] {
	case 'm':
		return time.Duration(n) * time.Minute, n <= 24*60
	case 'h':
		return time.Duration(n) * time.Hour, n <= 24
	}
	return 0, false
}

func journalStatusText(st JournalStatus, loc *time.Location) string {
	switch {
	case st.Stopped:
		return fmt.Sprintf("Check-ins parados. Llevo %d apuntes (%d sin respuesta). «/diario reanudar» los reactiva.", st.Answered, st.Skipped)
	case st.Finished:
		return fmt.Sprintf("La ventana de dos semanas terminó: %d apuntes. «/diario ver» los muestra.", st.Answered)
	case !st.Started:
		return "Check-ins activados: te preguntaré cada hora, de lunes a viernes de 9:00 a 19:00, durante dos semanas desde el primero."
	}
	text := fmt.Sprintf("Check-ins activos, quedan %d días. Llevo %d apuntes (%d sin respuesta).", st.DaysLeft, st.Answered, st.Skipped)
	if !st.PausedUntil.IsZero() {
		text += " En pausa hasta las " + st.PausedUntil.In(loc).Format("15:04") + "."
	}
	return text
}

func journalViewText(st JournalStatus, loc *time.Location) string {
	if len(st.LatestAnswers) == 0 {
		return "Todavía no hay apuntes."
	}
	lines := []string{"Últimos apuntes:"}
	for _, e := range st.LatestAnswers {
		lines = append(lines, "• "+e.At.In(loc).Format("02/01 15:04")+" — "+e.Text)
	}
	return strings.Join(lines, "\n")
}
