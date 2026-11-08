package secretary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseReminderRequestUnderstandsNaturalPhrasing(t *testing.T) {
	cases := []struct {
		text   string
		d      time.Duration
		reason string
	}{
		{"Hola Donna! me puedes avisar en 10 minutos para que revise la comida que tengo en el horno?", 10 * time.Minute, "Revisa la comida que tienes en el horno"},
		{"avísame en 10 minutos", 10 * time.Minute, ""},
		{"Avísame en diez minutos para que llame a Gonzalo", 10 * time.Minute, "Llama a Gonzalo"},
		{"recuérdame revisar el horno en media hora", 30 * time.Minute, "Revisar el horno"},
		{"Recuérdame en un cuarto de hora que coma algo", 15 * time.Minute, "Come algo"},
		{"me avisas dentro de una hora y media para sacar la colada", 90 * time.Minute, "Sacar la colada"},
		{"ponme un temporizador de 5 minutos para la pasta", 5 * time.Minute, "La pasta"},
		{"avísame en 2h de que tenga la reunión lista", 2 * time.Hour, "Ten la reunión lista"},
		{"¿Puedes avisarme en 20 min para que escriba a mi equipo?", 20 * time.Minute, "Escribe a tu equipo"},
		{"avisame en 1 hora y 15 minutos", 75 * time.Minute, ""},
		{"avísame en tres cuartos de hora", 45 * time.Minute, ""},
		{"en 10 minutos avísame para que apague el horno", 10 * time.Minute, "Apaga el horno"},
		{"Hola, Donna. Me pones un recordatorio dentro de 5 minutos de que tengo que sacar el vino del congelado", 5 * time.Minute, "Tienes que sacar el vino del congelado"},
		{"¿Me puedes poner una alarma en 15 minutos para llamar a mi madre?", 15 * time.Minute, "Llamar a tu madre"},
	}
	for _, tc := range cases {
		got, status := parseReminderRequest(tc.text)
		if status != reminderOK || got.Duration != tc.d || got.Reason != tc.reason {
			t.Errorf("%q => status=%v duration=%v reason=%q (want %v / %q)", tc.text, status, got.Duration, got.Reason, tc.d, tc.reason)
		}
	}
}

func TestParseReminderRequestRejectsWhatItCannotSchedule(t *testing.T) {
	for _, text := range []string{
		"avísame a las 14:30 de la reunión",
		"avísame cuando venza PMI-7",
		"avísame mañana",
		"avísame en 25 horas",
		"avísame en 0 minutos",
		"recuérdame que compre pan",
	} {
		if _, status := parseReminderRequest(text); status != reminderUnclear {
			t.Errorf("%q must be unclear, got %v", text, status)
		}
	}
}

func TestParseReminderRequestLeavesOrdinaryTextAlone(t *testing.T) {
	for _, text := range []string{
		"¿Me avisó Gonzalo de la reunión?",
		"Dame mis tareas de Jira",
		"Hola Donna",
		"el horno tarda 10 minutos en calentarse",
		"Gonzalo me recordó el viernes la entrega",
		"/timer 10m",
	} {
		if _, status := parseReminderRequest(text); status != reminderNone && !strings.HasPrefix(text, "/timer") {
			t.Errorf("%q is not a reminder request, got %v", text, status)
		}
	}
}

func TestReminderIsADraftUntilConfirmedAndNeverFakesConfirmation(t *testing.T) {
	var modelCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls++
		io.WriteString(w, `{"choices":[{"message":{"content":"Entendido."}}]}`)
	}))
	defer upstream.Close()
	svc := New(upstream.URL, "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	answer, err := svc.ReplyInConversation(context.Background(), 7, "telegram", "7", "Hola Donna! me puedes avisar en 10 minutos para que revise la comida que tengo en el horno?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "¿Te aviso en 10 minutos: «Revisa la comida que tienes en el horno»?") || !strings.Contains(answer, "Pulsa Confirmar (o escribe /confirmar-aviso ") || !strings.Contains(answer, "no se programa nada") {
		t.Fatalf("answer=%q", answer)
	}
	if active, _ := svc.timersForUser(7); len(active) != 0 {
		t.Fatalf("an unconfirmed reminder must not be an active timer: %+v", active)
	}
	if due, _ := svc.dueTimers(time.Now().Add(48 * time.Hour)); len(due) != 0 {
		t.Fatalf("a draft never fires: %+v", due)
	}
	if modelCalls != 0 {
		t.Fatal("a reminder never needs the model, so it can never say «entendido» without a timer")
	}
	offer, ok := svc.takeConfirmation(7, "7")
	if !ok || offer.Kind != confirmTimerKind {
		t.Fatalf("a Telegram draft offers the confirm button: %+v %v", offer, ok)
	}
	all, _ := svc.readTimers()
	id := all[0].ID
	confirmed := svc.confirmTimerReply(7, id)
	if !strings.Contains(confirmed, "Hecho. Te aviso en 10 minutos: Revisa la comida que tienes en el horno") {
		t.Fatalf("confirmed=%q", confirmed)
	}
	timers, _ := svc.timersForUser(7)
	if len(timers) != 1 || timers[0].alertText() != "⏰ Revisa la comida que tienes en el horno." || time.Until(timers[0].DueAt) < 9*time.Minute {
		t.Fatalf("timers=%+v", timers)
	}
	if again := svc.confirmTimerReply(7, id); !strings.Contains(again, "ya estaba programado") {
		t.Fatalf("a second press must not reschedule: %q", again)
	}
	for _, text := range []string{"avísame a las 14:30", "avísame cuando venza PMI-7"} {
		got, _ := svc.ReplyInConversation(context.Background(), 7, "telegram", "7", text)
		if !strings.Contains(got, "No he programado nada") {
			t.Fatalf("%q => %q", text, got)
		}
	}
	// From Open WebUI there is no button: the text says what to type and that the alert goes to Telegram.
	web, _ := svc.ReplyInConversation(context.Background(), 7, "openwebui", "chat", "avísame en 5 minutos para sacar el pan")
	if !strings.Contains(web, "/confirmar-aviso") || !strings.Contains(web, "por Telegram") {
		t.Fatalf("web=%q", web)
	}
	if _, ok := svc.takeConfirmation(7, "chat"); ok {
		t.Fatal("only Telegram gets a button")
	}
	cmd, _ := svc.ReplyInConversation(context.Background(), 7, "openwebui", "chat", "/confirmar-aviso "+draftIDFromText(web))
	if !strings.Contains(cmd, "Hecho. Te aviso en 5 minutos: Sacar el pan") {
		t.Fatalf("cmd=%q", cmd)
	}
}

func draftIDFromText(text string) string {
	for _, field := range strings.Fields(text) {
		field = strings.Trim(field, ";.,")
		if len(field) == 16 && strings.Trim(field, "0123456789abcdef") == "" {
			return field
		}
	}
	return ""
}

func TestReminderDraftsExpireAndAreOperatorScoped(t *testing.T) {
	svc := New("http://127.0.0.1:1", "key", "i", t.TempDir(), map[int64]bool{7: true, 8: true})
	draft, err := svc.createTimerDraft(7, 10*time.Minute, "Algo")
	if err != nil {
		t.Fatal(err)
	}
	if got := svc.confirmTimerReply(8, draft.ID); !strings.Contains(got, "No encuentro un aviso tuyo") {
		t.Fatalf("another operator must not confirm: %q", got)
	}
	timers, _ := svc.readTimers()
	timers[0].CreatedAt = time.Now().Add(-timerDraftTTL - time.Minute)
	svc.writeTimers(timers)
	if got := svc.confirmTimerReply(7, draft.ID); !strings.Contains(got, "caducó") {
		t.Fatalf("an old draft must expire: %q", got)
	}
	if active, _ := svc.timersForUser(7); len(active) != 0 {
		t.Fatalf("active=%+v", active)
	}
	if got := svc.confirmTimerReply(7, "0123456789abcdef"); !strings.Contains(got, "No encuentro") {
		t.Fatalf("unknown id: %q", got)
	}
}

type telegramCall struct{ Method, Body string }

func fakeTelegram(t *testing.T) (*Bot, *[]telegramCall) {
	t.Helper()
	var calls []telegramCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls = append(calls, telegramCall{Method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], Body: string(body)})
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(server.Close)
	svc := New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	store, _ := OpenScheduledTaskStore(svc.StateDir)
	svc.ScheduledTasks = store
	return &Bot{Base: server.URL, Token: "t", Client: server.Client(), Service: svc}, &calls
}

func callsOf(calls []telegramCall, method string) []telegramCall {
	var out []telegramCall
	for _, c := range calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestReminderTelegramShowsOneConfirmButtonAndPressingItSchedules(t *testing.T) {
	bot, calls := fakeTelegram(t)
	msg := &Message{ID: 41, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "me puedes avisar en 10 minutos para que revise la comida que tengo en el horno?"}
	if err := bot.processUpdate(context.Background(), Update{ID: 1, Message: msg}); err != nil {
		t.Fatal(err)
	}
	sends := callsOf(*calls, "sendMessage")
	if len(sends) != 1 || strings.Count(sends[0].Body, "callback_data") != 1 || !strings.Contains(sends[0].Body, "Confirmar") || !strings.Contains(sends[0].Body, `"callback_data":"tmc:`) {
		t.Fatalf("one message with exactly one button expected: %+v", sends)
	}
	if !strings.Contains(sends[0].Body, "Revisa la comida") {
		t.Fatalf("the schedule must be spelled out before confirming: %s", sends[0].Body)
	}
	if active, _ := bot.Service.timersForUser(7); len(active) != 0 {
		t.Fatal("nothing is scheduled before the press")
	}
	timers, _ := bot.Service.readTimers()
	data := "tmc:" + timers[0].ID
	press := &CallbackQuery{ID: "cb1", From: User{ID: 7}, Data: data, Message: &Message{ID: 42, Chat: Chat{ID: 7, Type: "private"}}}
	if err := bot.processUpdate(context.Background(), Update{ID: 2, CallbackQuery: press}); err != nil {
		t.Fatal(err)
	}
	if len(callsOf(*calls, "answerCallbackQuery")) != 1 || len(callsOf(*calls, "editMessageReplyMarkup")) != 1 {
		t.Fatalf("the press must be acknowledged and the button removed: %+v", *calls)
	}
	last := callsOf(*calls, "sendMessage")
	if len(last) != 2 || !strings.Contains(last[1].Body, "Hecho. Te aviso en 10 minutos") {
		t.Fatalf("follow-up=%+v", last)
	}
	if active, _ := bot.Service.timersForUser(7); len(active) != 1 {
		t.Fatalf("pressing Confirmar schedules it: %+v", active)
	}
	// A second press (or a forged one from another user) schedules nothing more.
	press2 := &CallbackQuery{ID: "cb2", From: User{ID: 99}, Data: data, Message: &Message{ID: 42, Chat: Chat{ID: 99, Type: "private"}}}
	bot.processUpdate(context.Background(), Update{ID: 3, CallbackQuery: press2})
	if active, _ := bot.Service.timersForUser(7); len(active) != 1 {
		t.Fatalf("active=%+v", active)
	}
}

func TestReminderTelegramPlainRepliesHaveNoButton(t *testing.T) {
	bot, calls := fakeTelegram(t)
	msg := &Message{ID: 1, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "/skills"}
	if err := bot.processUpdate(context.Background(), Update{ID: 1, Message: msg}); err != nil {
		t.Fatal(err)
	}
	sends := callsOf(*calls, "sendMessage")
	if len(sends) != 1 || strings.Contains(sends[0].Body, "reply_markup") {
		t.Fatalf("sends=%+v", sends)
	}
}

func TestReminderTelegramScheduledTaskDraftGetsTheSameButton(t *testing.T) {
	bot, calls := fakeTelegram(t)
	msg := &Message{ID: 1, From: User{ID: 7}, Chat: Chat{ID: 7, Type: "private"}, Text: "/programar morning-brief diaria 08:30"}
	bot.processUpdate(context.Background(), Update{ID: 1, Message: msg})
	sends := callsOf(*calls, "sendMessage")
	if len(sends) != 1 || strings.Count(sends[0].Body, "callback_data") != 1 || !strings.Contains(sends[0].Body, `"callback_data":"stc:`) {
		t.Fatalf("sends=%+v", sends)
	}
	tasks, _ := bot.Service.ScheduledTasks.List(7)
	press := &CallbackQuery{ID: "cb", From: User{ID: 7}, Data: "stc:" + tasks[0].ID, Message: &Message{ID: 2, Chat: Chat{ID: 7, Type: "private"}}}
	bot.processUpdate(context.Background(), Update{ID: 2, CallbackQuery: press})
	got, _, _ := bot.Service.ScheduledTasks.Get(7, tasks[0].ID)
	if !got.Enabled || got.ConfirmedAt.IsZero() {
		t.Fatalf("the button confirms exactly that draft: %+v", got)
	}
}

func TestReminderBadCallbackDataIsRejected(t *testing.T) {
	bot, calls := fakeTelegram(t)
	for _, data := range []string{"tmc:../../x", "tmc:", "stc:UPPER", "tmc:a"} {
		press := &CallbackQuery{ID: "cb", From: User{ID: 7}, Data: data, Message: &Message{ID: 2, Chat: Chat{ID: 7, Type: "private"}}}
		if err := bot.processUpdate(context.Background(), Update{ID: 9, CallbackQuery: press}); err != nil {
			t.Fatal(err)
		}
	}
	if len(callsOf(*calls, "sendMessage")) != 0 {
		t.Fatal("an invalid press schedules and says nothing")
	}
}

func TestReminderKeepsExistingTimerFormsAndLimit(t *testing.T) {
	svc := New("http://127.0.0.1:1", "key", "i", t.TempDir(), map[int64]bool{7: true})
	if got, _ := svc.ReplyInConversation(context.Background(), 7, "telegram", "7", "/timer 10m"); !strings.Contains(got, "Temporizador") {
		t.Fatalf("got=%q", got)
	}
	if got, _ := svc.ReplyInConversation(context.Background(), 7, "telegram", "7", "temporizador"); !strings.Contains(got, "indica una duración") {
		t.Fatalf("a bare timer request keeps its guidance: %q", got)
	}
	for i := 0; i < 12; i++ {
		svc.ReplyInConversation(context.Background(), 7, "telegram", "7", "avísame en 5 minutos para sacar el pan")
	}
	if got, _ := svc.ReplyInConversation(context.Background(), 7, "telegram", "7", "avísame en 5 minutos para sacar el pan"); !strings.Contains(got, "No he programado nada") {
		t.Fatalf("over the limit must say nothing was scheduled: %q", got)
	}
}

func TestReminderAlertIsDeliveredWithItsReasonOverTelegram(t *testing.T) {
	var sent []string
	telegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID int64  `json:"chat_id"`
			Text   string `json:"text"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		sent = append(sent, body.Text)
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	defer telegram.Close()
	svc := New("http://127.0.0.1:1", "key", "i", t.TempDir(), map[int64]bool{7: true})
	timer, err := svc.createTimerWithReason(7, time.Second, "Revisa la comida que tienes en el horno")
	if err != nil {
		t.Fatal(err)
	}
	_ = timer
	bot := &Bot{Base: telegram.URL, Token: "t", Client: telegram.Client(), Service: svc}
	time.Sleep(1100 * time.Millisecond)
	bot.deliverDueTimers(context.Background())
	if len(sent) != 1 || !strings.Contains(sent[0], "Revisa la comida que tienes en el horno") {
		t.Fatalf("sent=%v", sent)
	}
}
