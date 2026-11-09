package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/secretary"
)

func TestParseTelegramMode(t *testing.T) {
	for input, want := range map[string]string{
		"":         "webhook",
		"webhook":  "webhook",
		"disabled": "disabled",
	} {
		got, err := parseTelegramMode(input)
		if err != nil || string(got) != want {
			t.Errorf("parseTelegramMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := parseTelegramMode("polling"); err == nil {
		t.Fatal("retired transport was accepted")
	}
	if _, err := parseTelegramMode("both"); err == nil {
		t.Fatal("invalid Telegram mode was accepted")
	}
}

func TestParseTranscriptionEnabledIsIndependentFromLegacyVoiceFlag(t *testing.T) {
	cases := []struct {
		name, transcription, legacy string
		want                        bool
	}{
		{name: "input remains enabled when spoken replies are disabled", transcription: "true", legacy: "false", want: true},
		{name: "explicit input disable wins", transcription: "false", legacy: "true", want: false},
		{name: "legacy setting remains a fallback", legacy: "true", want: true},
		{name: "unset settings stay disabled", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTranscriptionEnabled(tc.transcription, tc.legacy); got != tc.want {
				t.Fatalf("parseTranscriptionEnabled(%q, %q) = %t, want %t", tc.transcription, tc.legacy, got, tc.want)
			}
		})
	}
}

func TestParseProgressDelay(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"":   30 * time.Second,
		"1":  time.Second,
		"30": 30 * time.Second,
	} {
		got, err := parseProgressDelay(input)
		if err != nil || got != want {
			t.Errorf("parseProgressDelay(%q) = %s, %v; want %s", input, got, err, want)
		}
	}
	for _, input := range []string{"0", "121", "fast"} {
		if _, err := parseProgressDelay(input); err == nil {
			t.Errorf("parseProgressDelay(%q) accepted an invalid threshold", input)
		}
	}
}

func TestParseDebugEnabledDefaultsToFalseAndRequiresBoolean(t *testing.T) {
	for input, want := range map[string]bool{"": false, "false": false, "true": true} {
		got, err := parseDebugEnabled(input)
		if err != nil || got != want {
			t.Errorf("parseDebugEnabled(%q) = %t, %v; want %t", input, got, err, want)
		}
	}
	for _, input := range []string{"yes", "1", "enabled"} {
		if _, err := parseDebugEnabled(input); err == nil {
			t.Errorf("parseDebugEnabled(%q) accepted a non-boolean value", input)
		}
	}
}

func TestParseLearningRetentionDefaultsAndBounds(t *testing.T) {
	got, err := parseLearningRetention("", "")
	if err != nil || got.TraceDays != 90 || got.CandidateDays != 30 {
		t.Fatalf("default retention = %+v, %v", got, err)
	}
	got, err = parseLearningRetention("120", "14")
	if err != nil || got.TraceDays != 120 || got.CandidateDays != 14 {
		t.Fatalf("configured retention = %+v, %v", got, err)
	}
	for _, invalid := range []string{"0", "3651", "bad"} {
		if _, err := parseLearningRetention(invalid, "30"); err == nil {
			t.Errorf("parseLearningRetention(%q) accepted an invalid trace retention", invalid)
		}
		if _, err := parseLearningRetention("90", invalid); err == nil {
			t.Errorf("parseLearningRetention(candidate=%q) accepted invalid retention", invalid)
		}
	}
}

func TestConfigureVoiceInputWorksWithoutPiper(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			t.Errorf("Whisper path = %q, want /inference", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse transcription request: %v", err)
		}
		if got := r.FormValue("language"); got != "es" {
			t.Errorf("language = %q, want es", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcripción correcta","language":"es"}`))
	}))
	defer whisper.Close()

	bot := &secretary.Bot{}
	if !configureVoiceInput(bot, true, whisper.URL, "", "/usr/bin/ffmpeg") {
		t.Fatal("voice input was not enabled with a Whisper endpoint and no Piper endpoint")
	}
	if !bot.TranscriptionEnabled || bot.Speech == nil || bot.Audio == nil {
		t.Fatalf("voice input dependencies were not configured: %+v", bot)
	}
	transcript, err := bot.Speech.Transcribe(context.Background(), []byte("wav"))
	if err != nil || transcript != "transcripción correcta" {
		t.Fatalf("transcription = %q, %v; want successful Whisper result", transcript, err)
	}
}

func TestWebhookWorkerDoesNotCallTelegramReceptionAPI(t *testing.T) {
	telegramCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telegramCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer api.Close()
	queue, err := secretary.OpenTelegramUpdateQueue(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	bot := &secretary.Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: secretary.New("", "", "", t.TempDir(), map[int64]bool{7: true})}
	run, err := configureTelegram("webhook", mux, bot, queue, "webhook-secret")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := run(ctx); err != nil {
		t.Fatal(err)
	}
	if telegramCalls != 0 {
		t.Fatalf("webhook mode made %d Telegram API calls; reception must use webhook", telegramCalls)
	}
}

func TestDisabledModeStartsNoTelegramConsumer(t *testing.T) {
	telegramCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { telegramCalls++ }))
	defer api.Close()
	mux := http.NewServeMux()
	bot := &secretary.Bot{Base: api.URL, Token: "secret", Client: api.Client(), Service: secretary.New("", "", "", t.TempDir(), map[int64]bool{7: true})}
	run, err := configureTelegram("disabled", mux, bot, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if telegramCalls != 0 {
		t.Fatalf("disabled mode made %d Telegram API calls", telegramCalls)
	}
}

func TestParseProactiveEnabledDefaultsToOffAndRequiresBoolean(t *testing.T) {
	for value, want := range map[string]bool{"": false, " ": false, "false": false, "FALSE": false, "true": true, " True ": true} {
		got, err := parseProactiveEnabled(value)
		if err != nil || got != want {
			t.Fatalf("%q: got %v err=%v", value, got, err)
		}
	}
	for _, value := range []string{"1", "yes", "on", "enabled", "truee"} {
		if _, err := parseProactiveEnabled(value); err == nil {
			t.Fatalf("%q must be rejected instead of guessed", value)
		}
	}
}

func TestProactiveDisabledModeStartsNothingAndTouchesNoState(t *testing.T) {
	state := t.TempDir()
	service := secretary.New("http://127.0.0.1:1", "key", "instructions", state, map[int64]bool{7: true})
	for _, value := range []string{"", "false"} {
		scheduler, err := buildProactive(value, service, nil, map[int64]bool{7: true}, nil)
		if err != nil || scheduler != nil {
			t.Fatalf("%q: scheduler=%v err=%v", value, scheduler, err)
		}
	}
	if service.ScheduledTasks != nil || service.ProactiveOutbox != nil {
		t.Fatal("disabled mode must leave schedule commands inert")
	}
	for _, name := range []string{"scheduled-tasks", "proactive-outbox"} {
		if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
			t.Fatalf("disabled mode must not create %s", name)
		}
	}
}

func TestProactiveEnabledRefusesStartupWithoutExactlyOneBoundOperator(t *testing.T) {
	for name, allowed := range map[string]map[int64]bool{
		"none":      {},
		"disabled":  {7: false},
		"ambiguous": {7: true, 8: true},
	} {
		state := t.TempDir()
		service := secretary.New("http://127.0.0.1:1", "key", "instructions", state, allowed)
		scheduler, err := buildProactive("true", service, nil, allowed, nil)
		if err == nil || scheduler != nil {
			t.Fatalf("%s: startup must be refused, got scheduler=%v err=%v", name, scheduler, err)
		}
		if service.ScheduledTasks != nil {
			t.Fatalf("%s: a refused startup must not arm commands", name)
		}
	}
	service := secretary.New("http://127.0.0.1:1", "key", "instructions", t.TempDir(), map[int64]bool{7: true})
	if _, err := buildProactive("maybe", service, nil, map[int64]bool{7: true}, nil); err == nil {
		t.Fatal("an invalid flag value must fail startup")
	}
}

func TestProactiveEnabledBuildsSchedulerForTheBoundOperatorOnly(t *testing.T) {
	state := t.TempDir()
	service := secretary.New("http://127.0.0.1:1", "key", "instructions", state, map[int64]bool{7: true})
	scheduler, err := buildProactive("true", service, nil, map[int64]bool{7: true}, func(string) {})
	if err != nil || scheduler == nil {
		t.Fatalf("scheduler=%v err=%v", scheduler, err)
	}
	if service.ScheduledTasks == nil || service.ProactiveOutbox == nil {
		t.Fatal("enabled mode arms the commands and the outbox")
	}
	if scheduler.Sender != nil {
		t.Fatal("with Telegram disabled there is no push sender; Open WebUI pulls on the next turn")
	}
	withTelegram, err := buildProactive("true", secretary.New("http://127.0.0.1:1", "key", "i", t.TempDir(), map[int64]bool{7: true}), &secretary.Bot{}, map[int64]bool{7: true}, nil)
	if err != nil || withTelegram.Sender == nil {
		t.Fatalf("telegram sender must be wired when Telegram is active: %v %v", withTelegram, err)
	}
	for _, name := range []string{"scheduled-tasks", "proactive-outbox"} {
		info, err := os.Stat(filepath.Join(state, name))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	// The scheduler is inert until Run: it does not poll on construction.
	if tasks, _ := service.ScheduledTasks.List(7); len(tasks) != 0 {
		t.Fatalf("tasks=%v", tasks)
	}
}

func TestCheckinsDisabledByDefaultAndStrict(t *testing.T) {
	for _, value := range []string{"", "false", "FALSE"} {
		if on, err := parseCheckinsEnabled(value); err != nil || on {
			t.Fatalf("%q must be off: %v %v", value, on, err)
		}
	}
	if on, err := parseCheckinsEnabled("true"); err != nil || !on {
		t.Fatal("true enables")
	}
	if _, err := parseCheckinsEnabled("yes"); err == nil {
		t.Fatal("unknown values are a startup error")
	}
	svc := &secretary.Service{StateDir: t.TempDir()}
	if scheduler, err := buildCheckins("false", svc, nil, map[int64]bool{1: true}); scheduler != nil || err != nil || svc.Journal != nil {
		t.Fatal("disabled mode must touch nothing")
	}
	if _, err := buildCheckins("true", svc, nil, map[int64]bool{1: true}); err == nil {
		t.Fatal("check-ins need Telegram")
	}
	if _, err := buildCheckins("true", svc, noopCheckinSender{}, map[int64]bool{1: true, 2: true}); err == nil {
		t.Fatal("check-ins need exactly one operator")
	}
}

type noopCheckinSender struct{}

func (noopCheckinSender) SendCheckin(context.Context, int64, string) (int64, secretary.ProactiveSendOutcome) {
	return 1, secretary.SendDelivered
}
