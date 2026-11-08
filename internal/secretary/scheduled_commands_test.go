package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type commandFixture struct {
	svc      *Service
	upstream *int32
}

func newCommandFixture(t *testing.T, enabled bool) commandFixture {
	t.Helper()
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.WriteString(w, `{"choices":[{"message":{"content":"respuesta del modelo"}}]}`)
	}))
	t.Cleanup(upstream.Close)
	svc := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true, 8: true})
	if enabled {
		store, err := OpenScheduledTaskStore(svc.StateDir)
		if err != nil {
			t.Fatal(err)
		}
		svc.ScheduledTasks = store
	}
	return commandFixture{svc: svc, upstream: &calls}
}

func (f commandFixture) say(t *testing.T, user int64, channel, text string) string {
	t.Helper()
	answer, err := f.svc.ReplyInConversation(context.Background(), user, channel, "conv-"+channel, text)
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return answer
}

var draftID = regexp.MustCompile(`t[0-9a-f]{12}`)

func TestScheduledCommandSkillsListsBuiltIns(t *testing.T) {
	f := newCommandFixture(t, true)
	answer := f.say(t, 7, "telegram", "/skills")
	for _, want := range []string{"morning-brief", "deadline-watch", "solo lectura", "Jira"} {
		if !strings.Contains(answer, want) {
			t.Fatalf("missing %q in %q", want, answer)
		}
	}
	if atomic.LoadInt32(f.upstream) != 0 {
		t.Fatal("deterministic commands never call the model")
	}
}

func TestScheduledCommandDraftThenConfirmFlow(t *testing.T) {
	f := newCommandFixture(t, true)
	preview := f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30")
	id := draftID.FindString(preview)
	if id == "" {
		t.Fatalf("no draft id in %q", preview)
	}
	for _, want := range []string{"aún no activo", "morning-brief", "cada día a las 08:30 (Europe/Madrid)", "Telegram", "Jira, Granola", "solo lectura", "08:00", "21:00", "3 mensajes", "/confirmar-tarea " + id} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview lacks %q: %q", want, preview)
		}
	}
	task, ok, _ := f.svc.ScheduledTasks.Get(7, id)
	if !ok || task.Enabled || !task.ConfirmedAt.IsZero() {
		t.Fatalf("a draft must be created disabled: %+v", task)
	}
	if got := f.say(t, 7, "telegram", "/tareas"); !strings.Contains(got, "borrador sin confirmar") {
		t.Fatalf("list=%q", got)
	}
	if got := f.say(t, 7, "telegram", "/confirmar-tarea "+id); !strings.Contains(got, "Confirmada") {
		t.Fatalf("confirm=%q", got)
	}
	task, _, _ = f.svc.ScheduledTasks.Get(7, id)
	if !task.Enabled || task.ConfirmedAt.IsZero() || task.OperatorID != 7 || task.Channel != "telegram" || task.ConversationID != "conv-telegram" {
		t.Fatalf("binding must come from the channel context: %+v", task)
	}
	if got := f.say(t, 7, "telegram", "/confirmar-tarea "+id); !strings.Contains(got, "ya estaba confirmada") {
		t.Fatalf("second confirm=%q", got)
	}
	if got := f.say(t, 7, "telegram", "/tareas"); !strings.Contains(got, "activa") || !strings.Contains(got, "próxima:") {
		t.Fatalf("list=%q", got)
	}
}

func TestScheduledCommandPauseResumeCancel(t *testing.T) {
	f := newCommandFixture(t, true)
	id := draftID.FindString(f.say(t, 7, "telegram", "/programar deadline-watch laborables 09:00"))
	if got := f.say(t, 7, "telegram", "/reanudar-tarea "+id); !strings.Contains(got, "borrador sin confirmar") {
		t.Fatalf("resume must not bypass confirmation: %q", got)
	}
	f.say(t, 7, "telegram", "/confirmar-tarea "+id)
	if got := f.say(t, 7, "telegram", "/pausar-tarea "+id); !strings.Contains(got, "Pausada") {
		t.Fatalf("pause=%q", got)
	}
	if task, _, _ := f.svc.ScheduledTasks.Get(7, id); task.Enabled {
		t.Fatal("paused task is still enabled")
	}
	if got := f.say(t, 7, "telegram", "/tareas"); !strings.Contains(got, "pausada") {
		t.Fatalf("list=%q", got)
	}
	if got := f.say(t, 7, "telegram", "/reanudar-tarea "+id); !strings.Contains(got, "Reanudada") {
		t.Fatalf("resume=%q", got)
	}
	if got := f.say(t, 7, "telegram", "/cancelar-tarea "+id); !strings.Contains(got, "Cancelada") {
		t.Fatalf("cancel=%q", got)
	}
	if got := f.say(t, 7, "telegram", "/tareas"); !strings.Contains(got, "No tienes tareas programadas") {
		t.Fatalf("list=%q", got)
	}
}

func TestScheduledCommandsAreOperatorScoped(t *testing.T) {
	f := newCommandFixture(t, true)
	id := draftID.FindString(f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30"))
	for _, command := range []string{"/confirmar-tarea ", "/pausar-tarea ", "/reanudar-tarea ", "/cancelar-tarea "} {
		if got := f.say(t, 8, "telegram", command+id); !strings.Contains(got, "No encuentro una tarea tuya") {
			t.Fatalf("%s leaked across operators: %q", command, got)
		}
	}
	if got := f.say(t, 8, "telegram", "/tareas"); !strings.Contains(got, "No tienes tareas programadas") {
		t.Fatalf("operator 8 sees %q", got)
	}
	task, ok, _ := f.svc.ScheduledTasks.Get(7, id)
	if !ok || task.Enabled || task.Canceled {
		t.Fatalf("the other operator's attempts must not change the task: %+v", task)
	}
}

func TestScheduledCommandRecipientCannotBeChosenFromText(t *testing.T) {
	f := newCommandFixture(t, true)
	id := draftID.FindString(f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30 operador=8 chat=999 canal=openwebui"))
	task, ok, _ := f.svc.ScheduledTasks.Get(7, id)
	if !ok || task.OperatorID != 7 || task.Channel != "telegram" || task.ConversationID != "conv-telegram" {
		t.Fatalf("recipient came from text: %+v", task)
	}
	if list, _ := f.svc.ScheduledTasks.List(8); len(list) != 0 {
		t.Fatal("nothing may be created for another operator")
	}
}

func TestScheduledCommandMissingFieldsAskOneClarificationAndCreateNothing(t *testing.T) {
	f := newCommandFixture(t, true)
	cases := map[string]string{
		"/programar":                             "la skill",
		"/programar morning-brief":               "la recurrencia",
		"/programar morning-brief diaria":        "la hora",
		"/programar diaria 08:30":                "la skill",
		"/programar morning-brief 08:30":         "la recurrencia",
		"/programar morning-brief una vez 09:00": "la fecha",
	}
	for command, want := range cases {
		got := f.say(t, 7, "telegram", command)
		if !strings.Contains(got, "me falta") || !strings.Contains(got, want) || !strings.Contains(got, "No he creado nada") {
			t.Fatalf("%q => %q", command, got)
		}
	}
	if list, _ := f.svc.ScheduledTasks.List(7); len(list) != 0 {
		t.Fatalf("a clarification must create no task: %+v", list)
	}
}

func TestScheduledCommandRejectsUnknownSkillCronAndBadZone(t *testing.T) {
	f := newCommandFixture(t, true)
	if got := f.say(t, 7, "telegram", "/programar borrar-jira diaria 08:30"); !strings.Contains(got, "la skill") || strings.Contains(got, "Borrador") {
		t.Fatalf("unknown skill => %q", got)
	}
	if got := f.say(t, 7, "telegram", "/programar morning-brief */5 * * * *"); !strings.Contains(got, "No admito expresiones cron") {
		t.Fatalf("cron => %q", got)
	}
	if got := f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30 Mars/Olympus"); !strings.Contains(got, "zona horaria") || strings.Contains(got, "Borrador") {
		// An unknown zone token is rejected by the store, not silently replaced.
		t.Fatalf("bad zone => %q", got)
	}
	if list, _ := f.svc.ScheduledTasks.List(7); len(list) != 0 {
		t.Fatalf("nothing should be stored: %+v", list)
	}
}

func TestScheduledCommandNaturalLanguage(t *testing.T) {
	f := newCommandFixture(t, true)
	got := f.say(t, 7, "telegram", "Prográmame el morning-brief todos los días laborables a las 8:30")
	if !strings.Contains(got, "de lunes a viernes a las 08:30 (Europe/Madrid)") || !strings.Contains(got, "/confirmar-tarea") {
		t.Fatalf("natural request => %q", got)
	}
	if got := f.say(t, 7, "telegram", "prográmame el deadline watch"); !strings.Contains(got, "me falta la recurrencia (diaria, laborables o una vez) y la hora") || !strings.Contains(got, "No he creado nada") {
		t.Fatalf("incomplete natural request => %q", got)
	}
	if list, _ := f.svc.ScheduledTasks.List(7); len(list) != 1 {
		t.Fatalf("only the complete request creates a draft: %+v", list)
	}
	// Ordinary sentences containing "programa" are not schedule requests.
	before := atomic.LoadInt32(f.upstream)
	f.say(t, 7, "telegram", "Dime cómo funciona el programa de Jira")
	if atomic.LoadInt32(f.upstream) != before+1 {
		t.Fatal("a normal question must still reach the model")
	}
}

func TestScheduledCommandOneOffNeedsDateAndRejectsPast(t *testing.T) {
	f := newCommandFixture(t, true)
	got := f.say(t, 7, "telegram", "/programar morning-brief una vez 2099-01-05 09:30")
	id := draftID.FindString(got)
	if id == "" || !strings.Contains(got, "una vez el 2099-01-05 09:30 (Europe/Madrid)") {
		t.Fatalf("one-off => %q", got)
	}
	past := f.say(t, 7, "telegram", "/programar morning-brief una vez 2020-01-05 09:30")
	pastID := draftID.FindString(past)
	if pastID == "" {
		t.Fatalf("a past draft is stored but cannot be confirmed: %q", past)
	}
	if got := f.say(t, 7, "telegram", "/confirmar-tarea "+pastID); !strings.Contains(got, "ya ha pasado") {
		t.Fatalf("confirming a past one-off => %q", got)
	}
}

func TestScheduledCommandInvalidIDs(t *testing.T) {
	f := newCommandFixture(t, true)
	for _, command := range []string{"/pausar-tarea ../../etc", "/cancelar-tarea", "/confirmar-tarea t1 t2"} {
		got := f.say(t, 7, "telegram", command)
		if !strings.Contains(got, "no es válido") && !strings.Contains(got, "Uso:") {
			t.Fatalf("%q => %q", command, got)
		}
	}
}

func TestScheduledCommandsAreInertWhenFeatureDisabled(t *testing.T) {
	f := newCommandFixture(t, false)
	if got := f.say(t, 7, "telegram", "/skills"); !strings.Contains(got, "morning-brief") {
		t.Fatalf("listing the built-ins is informational and allowed: %q", got)
	}
	for _, command := range []string{"/programar morning-brief diaria 08:30", "/tareas", "/confirmar-tarea t0123456789ab", "prográmame el morning-brief cada día a las 8:30"} {
		if got := f.say(t, 7, "telegram", command); !strings.Contains(got, "no están activadas") {
			t.Fatalf("%q => %q", command, got)
		}
	}
}

func TestScheduledCommandsDoNotCollideWithTimers(t *testing.T) {
	f := newCommandFixture(t, true)
	if got := f.say(t, 7, "telegram", "/timer 10m"); !strings.Contains(got, "Temporizador") {
		t.Fatalf("timer => %q", got)
	}
	timers, _ := f.svc.timersForUser(7)
	if len(timers) != 1 {
		t.Fatalf("timers=%v", timers)
	}
	if got := f.say(t, 7, "telegram", "/timers"); !strings.Contains(got, timers[0].ID) {
		t.Fatalf("timers => %q", got)
	}
	// A one-shot timer ID is not a scheduled-task ID and vice versa.
	if got := f.say(t, 7, "telegram", "/cancelar-tarea "+timers[0].ID); !strings.Contains(got, "no es válido") {
		t.Fatalf("cancelar-tarea on a timer id => %q", got)
	}
	taskID := draftID.FindString(f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30"))
	if got := f.say(t, 7, "telegram", "/cancel "+taskID); !strings.Contains(got, "No encuentro un temporizador") {
		t.Fatalf("/cancel on a task id => %q", got)
	}
	if task, ok, _ := f.svc.ScheduledTasks.Get(7, taskID); !ok || task.Canceled {
		t.Fatal("/cancel must not touch scheduled tasks")
	}
	if got := f.say(t, 7, "telegram", "/cancel "+timers[0].ID); !strings.Contains(got, "Temporizador cancelado") {
		t.Fatalf("timer cancel => %q", got)
	}
	if got := f.say(t, 7, "telegram", "Pon un temporizador de 5 minutos"); !strings.Contains(got, "Temporizador") {
		t.Fatalf("natural timer => %q", got)
	}
	if _, err := f.svc.ScheduledTasks.List(7); err != nil {
		t.Fatal(err)
	}
}

func TestScheduledCommandsBehaveTheSameOnTelegramAndOpenWebUI(t *testing.T) {
	f := newCommandFixture(t, true)
	tg := f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30")
	ow := f.say(t, 7, "openwebui", "/programar morning-brief diaria 08:30")
	strip := func(s string) string { return draftID.ReplaceAllString(s, "ID") }
	tgLines, owLines := strings.Split(strip(tg), "\n"), strings.Split(strip(ow), "\n")
	if len(tgLines) != len(owLines) {
		t.Fatalf("telegram=%q openwebui=%q", tg, ow)
	}
	if !strings.Contains(ow, "Open WebUI") || !strings.Contains(ow, "próxima conversación") {
		t.Fatalf("openwebui preview must explain delivery on the next conversation: %q", ow)
	}
	list := f.say(t, 7, "openwebui", "/tareas")
	if strings.Count(list, "morning-brief") != 2 {
		t.Fatalf("the operator sees tasks from both channels: %q", list)
	}
}

func TestScheduledCommandsThroughOpenWebUIHandler(t *testing.T) {
	f := newCommandFixture(t, true)
	body := `{"model":"main","messages":[{"role":"user","content":"/programar deadline-watch diaria 09:15"}]}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer key")
	request.Header.Set("X-OpenWebUI-User-Id", "owner")
	request.Header.Set("X-OpenWebUI-User-Role", "admin")
	request.Header.Set("X-OpenWebUI-Chat-Id", "chat-sched")
	response := httptest.NewRecorder()
	// Allowed has two operators, so Open WebUI cannot map the admin: use a single-operator service.
	single := New("http://127.0.0.1:1", "key", "instructions", f.svc.StateDir, map[int64]bool{7: true})
	single.ScheduledTasks = f.svc.ScheduledTasks
	single.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var parsed struct {
		Choices []struct {
			Message Turn `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil || len(parsed.Choices) == 0 {
		t.Fatalf("body=%s err=%v", response.Body.String(), err)
	}
	if !strings.Contains(parsed.Choices[0].Message.Content, "Borrador") {
		t.Fatalf("content=%q", parsed.Choices[0].Message.Content)
	}
	list, _ := single.ScheduledTasks.List(7)
	if len(list) != 1 || list[0].Channel != "openwebui" || list[0].ConversationID == "" || list[0].Enabled {
		t.Fatalf("task=%+v", list)
	}
}

func TestScheduledCommandsCreateDraftsThatClaimDueNeverReturns(t *testing.T) {
	f := newCommandFixture(t, true)
	f.say(t, 7, "telegram", "/programar morning-brief diaria 08:30")
	claims, err := f.svc.ScheduledTasks.ClaimDue(time.Now().Add(72*time.Hour), 10)
	if err != nil || len(claims) != 0 {
		t.Fatalf("unconfirmed drafts must never be claimed: %+v %v", claims, err)
	}
}
