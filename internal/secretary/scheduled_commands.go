package secretary

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const scheduledDisabledMessage = "Las tareas programadas no están activadas en este despliegue."

var scheduledTaskIDPattern = regexp.MustCompile(`^t[0-9a-f]{12}$`)

// scheduledCommand handles the deterministic schedule controls shared by every
// channel. It reports handled=false for anything that is not a schedule request,
// so existing commands and timers keep their behaviour. Operator, channel and
// conversation always come from the trusted adapter arguments, never from text.
func (s *Service) scheduledCommand(user int64, channel, conversation, text string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", false
	}
	switch strings.ToLower(fields[0]) {
	case "/skills":
		return scheduledSkillsMessage(), true
	case "/programar":
		return s.scheduleDraft(user, channel, conversation, fields[1:]), true
	case "/tareas":
		return s.scheduledTaskList(user), true
	case "/confirmar-tarea":
		return s.scheduledTaskAction(user, fields, "confirmar"), true
	case "/pausar-tarea":
		return s.scheduledTaskAction(user, fields, "pausar"), true
	case "/reanudar-tarea":
		return s.scheduledTaskAction(user, fields, "reanudar"), true
	case "/cancelar-tarea":
		return s.scheduledTaskAction(user, fields, "cancelar"), true
	}
	if request, ok := parseNaturalScheduleRequest(text); ok {
		return s.scheduleFromRequest(user, channel, conversation, request), true
	}
	return "", false
}

func scheduledSkillsMessage() string {
	lines := []string{"Skills que puedo programar (solo lectura):"}
	for _, skill := range ScheduledSkills() {
		lines = append(lines, fmt.Sprintf("• %s: %s Fuentes: %s.", skill.ID, skill.Description, strings.Join(skill.RequiredCapabilities, ", ")))
	}
	lines = append(lines, "Ejemplo: /programar morning-brief diaria 08:30")
	return strings.Join(lines, "\n")
}

// scheduleRequest is a parsed, not yet validated, request. Missing lists what
// the operator still has to say; Cron marks an unsupported cron expression.
type scheduleRequest struct {
	SkillID   string
	SkillSeen bool
	Cadence   ScheduleCadence
	LocalTime string
	RunAt     string // "2006-01-02 15:04" for one-off requests
	Timezone  string
	Cron      bool
}

var (
	clockPattern    = regexp.MustCompile(`\b([01]?\d|2[0-3]):([0-5]\d)\b`)
	datePattern     = regexp.MustCompile(`\b(\d{4}-\d{2}-\d{2})\b`)
	spokenClock     = regexp.MustCompile(`\ba las? (\d{1,2})(?:[:.h]([0-5]\d))?(?:\b|$)`)
	cronLikePattern = regexp.MustCompile(`(^|\s)[\d*/,-]+\s+[\d*/,-]+\s+[\d*/,-]+\s+[\d*/,-]+\s+[\d*/,-]+(\s|$)`)
	programVerb     = regexp.MustCompile(`\bprogram(?:a|ame|ar|alo|e)\b`)
	zonePattern     = regexp.MustCompile(`\b[A-Z][A-Za-z_]+/[A-Za-z_]+(?:/[A-Za-z_]+)?\b`)
)

func skillMention(normalized string) (string, bool) {
	for _, skill := range ScheduledSkills() {
		spaced := strings.ReplaceAll(skill.ID, "-", " ")
		if strings.Contains(normalized, skill.ID) || strings.Contains(normalized, spaced) {
			return skill.ID, true
		}
	}
	return "", false
}

// parseScheduleWords reads skill, cadence, time and zone from free words. It
// understands only the supported built-in forms; anything else stays empty.
func parseScheduleWords(text string) scheduleRequest {
	normalized := normalizedIntentText(text)
	request := scheduleRequest{}
	request.SkillID, request.SkillSeen = skillMention(normalized)
	if cronLikePattern.MatchString(text) || strings.Contains(normalized, "cron") {
		request.Cron = true
	}
	switch {
	case containsAny(normalized, "laborable", "laborables", "weekdays", "entre semana", "lunes a viernes"):
		request.Cadence = CadenceWeekdays
	case containsAny(normalized, "una vez", "once", "solo una vez"):
		request.Cadence = CadenceOnce
	case containsAny(normalized, "diaria", "diario", "daily", "cada dia", "todos los dias", "cada manana", "todas las mananas"):
		request.Cadence = CadenceDaily
	}
	if m := clockPattern.FindStringSubmatch(text); m != nil {
		hour, _ := strconv.Atoi(m[1])
		request.LocalTime = fmt.Sprintf("%02d:%s", hour, m[2])
	} else if m := spokenClock.FindStringSubmatch(normalized); m != nil {
		if hour, _ := strconv.Atoi(m[1]); hour <= 23 {
			minute := m[2]
			if minute == "" {
				minute = "00"
			}
			request.LocalTime = fmt.Sprintf("%02d:%s", hour, minute)
		}
	}
	if request.Cadence == CadenceOnce {
		if d := datePattern.FindStringSubmatch(text); d != nil && request.LocalTime != "" {
			request.RunAt = d[1] + " " + request.LocalTime
		}
	}
	if z := zonePattern.FindString(text); z != "" {
		request.Timezone = z
	}
	return request
}

// parseNaturalScheduleRequest recognises "prográmame el morning-brief cada día a
// las 8:30" style requests. It only fires when a built-in skill (or the word
// "skill") is named, so ordinary sentences with "programa" are left alone.
func parseNaturalScheduleRequest(text string) (scheduleRequest, bool) {
	normalized := normalizedIntentText(text)
	if !programVerb.MatchString(normalized) {
		return scheduleRequest{}, false
	}
	request := parseScheduleWords(text)
	if !request.SkillSeen && !containsAny(normalized, "skill", "tarea programada") {
		return scheduleRequest{}, false
	}
	return request, true
}

func (s *Service) scheduleDraft(user int64, channel, conversation string, args []string) string {
	if s.ScheduledTasks == nil {
		return scheduledDisabledMessage
	}
	return s.scheduleFromRequest(user, channel, conversation, parseScheduleWords(strings.Join(args, " ")))
}

const scheduleUsage = "/programar morning-brief diaria 08:30 · cadencias: diaria, laborables, o «una vez 2026-11-10 09:00»"

func (s *Service) scheduleFromRequest(user int64, channel, conversation string, request scheduleRequest) string {
	if s.ScheduledTasks == nil {
		return scheduledDisabledMessage
	}
	if request.Cron {
		return "No admito expresiones cron. Solo programo cadencias diaria, laborables o una vez, con hora local. Ejemplo: " + scheduleUsage
	}
	var missing []string
	if !request.SkillSeen {
		missing = append(missing, "la skill ("+skillIDsText()+")")
	}
	if request.Cadence == "" {
		missing = append(missing, "la recurrencia (diaria, laborables o una vez)")
	}
	if request.LocalTime == "" {
		missing = append(missing, "la hora (por ejemplo 08:30)")
	} else if request.Cadence == CadenceOnce && request.RunAt == "" {
		missing = append(missing, "la fecha (AAAA-MM-DD) de la ejecución única")
	}
	if len(missing) > 0 {
		return "Para programarlo me falta " + joinSpanish(missing) + ". No he creado nada. Ejemplo: " + scheduleUsage
	}
	zone, defaulted := request.Timezone, false
	if zone == "" {
		zone, defaulted = DefaultScheduleTimezone, true
	}
	schedule := TaskSchedule{Cadence: request.Cadence, LocalTime: request.LocalTime, Timezone: zone}
	if request.Cadence == CadenceOnce {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return "No conozco la zona horaria «" + boundedLabel(zone) + "». Usa un nombre IANA, como Europe/Madrid. No he creado nada."
		}
		runAt, err := time.ParseInLocation("2006-01-02 15:04", request.RunAt, loc)
		if err != nil {
			return "No entiendo esa fecha y hora. Usa AAAA-MM-DD HH:MM. No he creado nada."
		}
		schedule.LocalTime, schedule.RunAt = "", runAt.UTC()
	}
	task, err := s.ScheduledTasks.Create(ScheduledTask{OperatorID: user, Channel: channel, ConversationID: conversation, SkillID: request.SkillID, Schedule: schedule})
	if err != nil {
		return scheduledCreateError(err)
	}
	return scheduleDraftPreview(task, defaulted)
}

func scheduledCreateError(err error) string {
	switch {
	case strings.Contains(err.Error(), "limit"):
		return fmt.Sprintf("Ya tienes %d tareas programadas; cancela alguna con /cancelar-tarea ID antes de crear otra.", maxScheduledTasksPerOperator)
	case strings.Contains(err.Error(), "timezone"):
		return "No conozco esa zona horaria. Usa un nombre IANA, como Europe/Madrid. No he creado nada."
	case strings.Contains(err.Error(), "invalid"), strings.Contains(err.Error(), "needs"), strings.Contains(err.Error(), "unsupported"):
		return "Esa programación no es válida. No he creado nada. Ejemplo: " + scheduleUsage
	}
	return "No he podido guardar la tarea; no he creado nada."
}

func skillIDsText() string {
	ids := make([]string, 0, 2)
	for _, skill := range ScheduledSkills() {
		ids = append(ids, skill.ID)
	}
	return strings.Join(ids, ", ")
}

func scheduleWhenText(schedule TaskSchedule) string {
	loc, _ := time.LoadLocation(schedule.Timezone)
	switch schedule.Cadence {
	case CadenceDaily:
		return fmt.Sprintf("cada día a las %s (%s)", schedule.LocalTime, schedule.Timezone)
	case CadenceWeekdays:
		return fmt.Sprintf("de lunes a viernes a las %s (%s)", schedule.LocalTime, schedule.Timezone)
	default:
		if loc == nil {
			loc = time.UTC
		}
		return fmt.Sprintf("una vez el %s (%s)", schedule.RunAt.In(loc).Format("2006-01-02 15:04"), schedule.Timezone)
	}
}

func channelLabel(channel string) string {
	if channel == "openwebui" {
		return "Open WebUI (el aviso aparece en tu próxima conversación)"
	}
	return "Telegram (este chat privado)"
}

func scheduleDraftPreview(task ScheduledTask, zoneDefaulted bool) string {
	skill, _ := LookupScheduledSkill(task.SkillID)
	zoneNote := ""
	if zoneDefaulted {
		zoneNote = " Zona por defecto; indica otra con su nombre IANA si quieres."
	}
	return strings.Join([]string{
		"Borrador " + task.ID + " (aún no activo):",
		"• Skill: " + skill.ID + " — " + skill.Description,
		"• Cuándo: " + scheduleWhenText(task.Schedule) + "." + zoneNote,
		"• Canal: " + channelLabel(task.Channel),
		"• Fuentes: " + strings.Join(skill.RequiredCapabilities, ", ") + " (solo lectura)",
		fmt.Sprintf("• Límites: solo entre %02d:00 y %02d:00 (%s) y como mucho %d mensajes proactivos al día.", proactiveWindowStartHour, proactiveWindowEndHour, proactiveTimezone, proactiveDailyLimit),
		"No enviaré nada hasta que lo confirmes: /confirmar-tarea " + task.ID,
	}, "\n")
}

func (s *Service) scheduledTaskList(user int64) string {
	if s.ScheduledTasks == nil {
		return scheduledDisabledMessage
	}
	tasks, err := s.ScheduledTasks.List(user)
	if err != nil {
		return "No he podido leer tus tareas programadas."
	}
	if len(tasks) == 0 {
		return "No tienes tareas programadas. Mira las skills con /skills."
	}
	lines := make([]string, 0, len(tasks))
	for _, task := range tasks {
		state := "pausada"
		switch {
		case task.ConfirmedAt.IsZero():
			state = "borrador sin confirmar"
		case task.Enabled:
			state = "activa"
		}
		line := fmt.Sprintf("%s · %s · %s · %s", task.ID, task.SkillID, scheduleWhenText(task.Schedule), state)
		if task.Enabled && !task.NextRunAt.IsZero() {
			loc, _ := time.LoadLocation(task.Schedule.Timezone)
			if loc == nil {
				loc = time.UTC
			}
			line += " · próxima: " + task.NextRunAt.In(loc).Format("2006-01-02 15:04")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) scheduledTaskAction(user int64, fields []string, action string) string {
	if s.ScheduledTasks == nil {
		return scheduledDisabledMessage
	}
	if len(fields) != 2 {
		return "Uso: " + strings.ToLower(fields[0]) + " ID"
	}
	id := fields[1]
	if !scheduledTaskIDPattern.MatchString(id) {
		return "Ese ID no es válido. Mira los IDs con /tareas."
	}
	task, ok, err := s.ScheduledTasks.Get(user, id)
	if err != nil {
		return "No he podido leer tus tareas programadas."
	}
	if !ok {
		return "No encuentro una tarea tuya con ese ID."
	}
	confirmed := !task.ConfirmedAt.IsZero()
	switch action {
	case "confirmar":
		if confirmed {
			return "Esa tarea ya estaba confirmada. Mira su estado con /tareas."
		}
		return scheduledEnableResult(s.ScheduledTasks.SetEnabled(user, id, true), "Confirmada. "+scheduleWhenText(task.Schedule)+": "+task.SkillID+".")
	case "reanudar":
		if !confirmed {
			return "Esa tarea es un borrador sin confirmar. Actívala con /confirmar-tarea " + id + "."
		}
		return scheduledEnableResult(s.ScheduledTasks.SetEnabled(user, id, true), "Reanudada. No repetiré lo que quedó pendiente mientras estaba pausada.")
	case "pausar":
		if err := s.ScheduledTasks.SetEnabled(user, id, false); err != nil {
			return "No he podido pausar la tarea."
		}
		return "Pausada. Puedes reanudarla con /reanudar-tarea " + id + "."
	default:
		if err := s.ScheduledTasks.Cancel(user, id); err != nil {
			return "No he podido cancelar la tarea."
		}
		return "Cancelada. No volverá a ejecutarse."
	}
}

func scheduledEnableResult(err error, ok string) string {
	if err == nil {
		return ok
	}
	if strings.Contains(err.Error(), "already passed") {
		return "Esa hora ya ha pasado; programa una nueva con /programar."
	}
	return "No he podido activar la tarea."
}
