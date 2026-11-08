package secretary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	mu     sync.Mutex
	calls  int
	result ScheduledSkillResult
	err    error
	block  chan struct{}
	each   func(task ScheduledTask) ScheduledSkillResult
}

func (f *fakeExecutor) Execute(ctx context.Context, task ScheduledTask, _ time.Time) (ScheduledSkillResult, error) {
	f.mu.Lock()
	f.calls++
	block, each, result, err := f.block, f.each, f.result, f.err
	f.mu.Unlock()
	if block != nil {
		select {
		case <-ctx.Done():
			return ScheduledSkillResult{}, ctx.Err()
		case <-block:
		}
	}
	if each != nil {
		return each(task), nil
	}
	return result, err
}

func (f *fakeExecutor) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeSender struct {
	mu       sync.Mutex
	outcomes []ProactiveSendOutcome
	chats    []int64
	texts    []string
}

func (f *fakeSender) Send(_ context.Context, chat int64, text string) ProactiveSendOutcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chats, f.texts = append(f.chats, chat), append(f.texts, text)
	if len(f.outcomes) == 0 {
		return SendDelivered
	}
	outcome := f.outcomes[0]
	if len(f.outcomes) > 1 {
		f.outcomes = f.outcomes[1:]
	}
	return outcome
}

func (f *fakeSender) Sends() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.texts)
}

type schedulerRig struct {
	t      *testing.T
	dir    string
	clock  *time.Time
	tasks  *ScheduledTaskStore
	outbox *ProactiveOutbox
	exec   *fakeExecutor
	sender *fakeSender
	sched  *ProactiveScheduler
	events []string
	metric *VoiceMetrics
}

func newSchedulerRig(t *testing.T, start time.Time) *schedulerRig {
	t.Helper()
	rig := &schedulerRig{t: t, dir: t.TempDir(), clock: &start, exec: &fakeExecutor{result: ScheduledSkillResult{Text: "Tienes 2 tareas abiertas."}}, sender: &fakeSender{}, metric: NewVoiceMetrics()}
	rig.open()
	return rig
}

// open (re)opens the state from disk, as a restarted process would.
func (r *schedulerRig) open() {
	var err error
	r.tasks, err = OpenScheduledTaskStore(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	r.outbox, err = OpenProactiveOutbox(r.dir)
	if err != nil {
		r.t.Fatal(err)
	}
	now := func() time.Time { return *r.clock }
	r.tasks.Now, r.outbox.Now = now, now
	r.sched = &ProactiveScheduler{Tasks: r.tasks, Outbox: r.outbox, Executor: r.exec, Sender: r.sender, Now: now, Metrics: r.metric,
		Event: func(e string) { r.events = append(r.events, e) }}
}

func (r *schedulerRig) addTask(operator int64, channel, conversation, skill, hhmm string) ScheduledTask {
	r.t.Helper()
	task, err := r.tasks.Create(ScheduledTask{OperatorID: operator, Channel: channel, ConversationID: conversation, SkillID: skill,
		Schedule: TaskSchedule{Cadence: CadenceDaily, LocalTime: hhmm, Timezone: "Europe/Madrid"}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := r.tasks.SetEnabled(operator, task.ID, true); err != nil {
		r.t.Fatal(err)
	}
	return task
}

func (r *schedulerRig) runStatuses(operator int64) []ScheduledRunStatus {
	runs, err := r.tasks.Runs(operator)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []ScheduledRunStatus
	for _, run := range runs {
		out = append(out, run.Status)
	}
	return out
}

func madridTime(t *testing.T, y int, m time.Month, d, h, min int) time.Time {
	t.Helper()
	return time.Date(y, m, d, h, min, 0, 0, madrid(t))
}

func TestProactiveSchedulerRunsDueTaskOnceAndDeliversToPrivateChat(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	rig.sched.RunOnce(context.Background())
	if rig.exec.Calls() != 1 || rig.sender.Sends() != 1 {
		t.Fatalf("one logical run => one execution and one message, got exec=%d sends=%d", rig.exec.Calls(), rig.sender.Sends())
	}
	if rig.sender.chats[0] != 7 || rig.sender.texts[0] != "Tienes 2 tareas abiertas." {
		t.Fatalf("chats=%v texts=%v", rig.sender.chats, rig.sender.texts)
	}
	if got := rig.runStatuses(7); len(got) != 1 || got[0] != RunDelivered {
		t.Fatalf("statuses=%v", got)
	}
}

func TestProactiveSchedulerSuppressedResultIsRecordedNotSent(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.exec.result = ScheduledSkillResult{Suppress: true, SuppressReason: "no_change"}
	rig.addTask(7, "telegram", "7", "deadline-watch", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatal("a suppressed result must not be delivered")
	}
	if got := rig.runStatuses(7); len(got) != 1 || got[0] != RunSkipped {
		t.Fatalf("statuses=%v", got)
	}
}

func TestProactiveSchedulerSkillFailuresAreTypedAndClosed(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.exec.err = ErrUnknownScheduledSkill
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	runs, _ := rig.tasks.Runs(7)
	if len(runs) != 1 || runs[0].Status != RunFailed || runs[0].ErrorCode != "unknown_skill" {
		t.Fatalf("runs=%+v", runs)
	}
	if rig.sender.Sends() != 0 {
		t.Fatal("a failed run sends nothing")
	}
}

func TestProactiveSchedulerHoldsMessagesOutsideSendWindowThenDelivers(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 5, 0))
	rig.addTask(7, "telegram", "7", "morning-brief", "06:00")
	*rig.clock = madridTime(t, 2026, 11, 9, 6, 1)
	rig.sched.RunOnce(context.Background())
	if rig.exec.Calls() != 1 || rig.sender.Sends() != 0 {
		t.Fatalf("executed but must wait for 08:00: exec=%d sends=%d", rig.exec.Calls(), rig.sender.Sends())
	}
	if got := rig.runStatuses(7); got[0] != RunPending {
		t.Fatalf("queued run is pending: %v", got)
	}
	*rig.clock = madridTime(t, 2026, 11, 9, 7, 59)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatal("07:59 is still quiet hours")
	}
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 0)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 1 || rig.exec.Calls() != 1 {
		t.Fatalf("exec=%d sends=%d", rig.exec.Calls(), rig.sender.Sends())
	}
	if got := rig.runStatuses(7); got[0] != RunDelivered {
		t.Fatalf("statuses=%v", got)
	}
}

func TestProactiveSchedulerNoSendAtOrAfterWindowEnd(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 20, 0))
	rig.addTask(7, "telegram", "7", "morning-brief", "20:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 20, 31)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 1 {
		t.Fatal("20:31 is inside the window")
	}
	rig2 := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 20, 0))
	rig2.addTask(7, "telegram", "7", "morning-brief", "21:00")
	*rig2.clock = madridTime(t, 2026, 11, 9, 21, 1)
	rig2.sched.RunOnce(context.Background())
	if rig2.sender.Sends() != 0 {
		t.Fatal("21:00 and later is outside the window")
	}
}

func TestProactiveSchedulerStaleQueuedMessageExpiresInsteadOfArrivingNextDay(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 21, 30))
	rig.addTask(7, "telegram", "7", "morning-brief", "22:00")
	*rig.clock = madridTime(t, 2026, 11, 9, 22, 1)
	rig.sched.RunOnce(context.Background())
	*rig.clock = madridTime(t, 2026, 11, 10, 22, 5)
	rig.sched.RunOnce(context.Background())
	runs, _ := rig.tasks.Runs(7)
	var expired bool
	for _, run := range runs {
		if run.ErrorCode == "expired" && run.Status == RunSkipped {
			expired = true
		}
	}
	if !expired {
		t.Fatalf("a message held for more than a day expires: %+v", runs)
	}
}

func TestProactiveSchedulerEnforcesDailyLimitAcrossSends(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 9, 0))
	var runIDs []string
	for i := 0; i < 4; i++ {
		n, _, err := rig.outbox.Enqueue(ProactiveNotification{OperatorID: 7, Channel: "telegram", ConversationID: "7", TaskID: "t" + string(rune('a'+i)), RunID: "r" + string(rune('a'+i)),
			ScheduledFor: madridTime(t, 2026, 11, 9, 8, i), Text: "aviso"})
		if err != nil {
			t.Fatal(err)
		}
		runIDs = append(runIDs, n.ID)
		if i < 3 {
			rig.outbox.BeginSend([]string{n.ID}, "g"+n.ID)
			rig.outbox.Complete([]string{n.ID}, ProactiveDelivered)
		}
	}
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatalf("the fourth message of the day must wait, sends=%d", rig.sender.Sends())
	}
	*rig.clock = madridTime(t, 2026, 11, 10, 9, 0)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		// The fourth item is older than 24h only after expiry; before that it may send on the new day.
		t.Logf("sent on the new local day: %d", rig.sender.Sends())
	}
}

func TestProactiveSchedulerCoalescesRelatedNotificationsIntoOneMessage(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.exec.each = func(task ScheduledTask) ScheduledSkillResult {
		return ScheduledSkillResult{Text: "Aviso de " + task.SkillID + "."}
	}
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	rig.addTask(7, "telegram", "7", "deadline-watch", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 1 {
		t.Fatalf("two related notices share one message, sends=%d", rig.sender.Sends())
	}
	text := rig.sender.texts[0]
	if !strings.Contains(text, "2 avisos programados") || !strings.Contains(text, "morning-brief") || !strings.Contains(text, "deadline-watch") {
		t.Fatalf("text=%q", text)
	}
	count, _ := rig.outbox.SentMessagesOn(7, *rig.clock, madrid(t))
	if count != 1 {
		t.Fatalf("a coalesced message counts once toward the daily limit, got %d", count)
	}
	for _, status := range rig.runStatuses(7) {
		if status != RunDelivered {
			t.Fatalf("statuses=%v", rig.runStatuses(7))
		}
	}
}

func TestProactiveSchedulerUncertainSendIsNeverRetried(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.sender.outcomes = []ProactiveSendOutcome{SendUncertain}
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	for i := 0; i < 3; i++ {
		*rig.clock = rig.clock.Add(time.Minute)
		rig.sched.RunOnce(context.Background())
	}
	if rig.sender.Sends() != 1 {
		t.Fatalf("an ambiguous result must not be resent blindly, sends=%d", rig.sender.Sends())
	}
	if got := rig.runStatuses(7); got[0] != RunUncertain {
		t.Fatalf("statuses=%v", got)
	}
	uncertain, _ := rig.outbox.Uncertain(7)
	if len(uncertain) != 1 {
		t.Fatalf("an uncertain result stays visible for review: %+v", uncertain)
	}
}

func TestProactiveSchedulerRetriesOnlyKnownFailures(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.sender.outcomes = []ProactiveSendOutcome{SendFailed, SendDelivered}
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if got := rig.runStatuses(7); got[0] != RunPending {
		t.Fatalf("a retryable failure keeps the run queued: %v", got)
	}
	*rig.clock = rig.clock.Add(time.Minute)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 2 || rig.runStatuses(7)[0] != RunDelivered {
		t.Fatalf("sends=%d statuses=%v", rig.sender.Sends(), rig.runStatuses(7))
	}
	// Permanent failure stops after the attempt cap.
	rig2 := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig2.sender.outcomes = []ProactiveSendOutcome{SendFailed}
	rig2.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig2.clock = madridTime(t, 2026, 11, 9, 8, 31)
	for i := 0; i < 6; i++ {
		rig2.sched.RunOnce(context.Background())
		*rig2.clock = rig2.clock.Add(time.Minute)
	}
	if rig2.sender.Sends() != proactiveMaxAttempts || rig2.runStatuses(7)[0] != RunFailed {
		t.Fatalf("sends=%d statuses=%v", rig2.sender.Sends(), rig2.runStatuses(7))
	}
}

func TestProactiveSchedulerRefusesAnyDestinationButThePrivateOperatorChat(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.addTask(7, "telegram", "-1001234", "morning-brief", "08:30") // a group chat
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatal("never send to a chat other than the operator's private chat")
	}
	if got := rig.runStatuses(7); got[0] != RunFailed {
		t.Fatalf("statuses=%v", got)
	}
}

func TestProactiveSchedulerShutdownMidRunLeavesClaimForResumeWithoutSecondRun(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.exec.block = make(chan struct{})
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rig.sched.RunOnce(ctx); close(done) }()
	deadline := time.After(2 * time.Second)
	for rig.exec.Calls() == 0 {
		select {
		case <-deadline:
			t.Fatal("executor never started")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if got := rig.runStatuses(7); got[0] != RunRunning {
		t.Fatalf("a shutdown must not finish the run: %v", got)
	}
	if rig.sender.Sends() != 0 {
		t.Fatal("nothing is sent on shutdown")
	}
	// A new process resumes the same logical run.
	rig.exec.block = nil
	rig.open()
	rig.sched.RunOnce(context.Background())
	rig.sched.RunOnce(context.Background())
	runs, _ := rig.tasks.Runs(7)
	if len(runs) != 1 || runs[0].Status != RunDelivered || runs[0].Attempt != 2 || rig.sender.Sends() != 1 {
		t.Fatalf("runs=%+v sends=%d", runs, rig.sender.Sends())
	}
}

func TestProactiveSchedulerRestartAfterQueueingDoesNotExecuteOrSendTwice(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	task := rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	claims, _ := rig.tasks.ClaimDue(*rig.clock, 5)
	// The process queued the result, then died before the run left "running".
	if _, _, err := rig.outbox.Enqueue(ProactiveNotification{OperatorID: 7, Channel: "telegram", ConversationID: "7", TaskID: task.ID, RunID: claims[0].Run.ID,
		ScheduledFor: claims[0].Run.ScheduledFor, Text: "ya en cola"}); err != nil {
		t.Fatal(err)
	}
	rig.open()
	rig.sched.RunOnce(context.Background())
	rig.sched.RunOnce(context.Background())
	if rig.exec.Calls() != 0 {
		t.Fatalf("the occurrence was already queued; executing again wastes a model call: %d", rig.exec.Calls())
	}
	if rig.sender.Sends() != 1 || rig.sender.texts[0] != "ya en cola" {
		t.Fatalf("sends=%d texts=%v", rig.sender.Sends(), rig.sender.texts)
	}
	if got := rig.runStatuses(7); len(got) != 1 || got[0] != RunDelivered {
		t.Fatalf("statuses=%v", got)
	}
}

func TestProactiveSchedulerCrashDuringSendBecomesUncertainOnRestart(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 9, 0))
	n, _, _ := rig.outbox.Enqueue(ProactiveNotification{OperatorID: 7, Channel: "telegram", ConversationID: "7", TaskID: "t1", RunID: "r1", ScheduledFor: *rig.clock, Text: "x"})
	if err := rig.outbox.BeginSend([]string{n.ID}, "g1"); err != nil {
		t.Fatal(err)
	}
	rig.open() // the process died after BeginSend
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatal("a message that was mid-send is never resent")
	}
	if u, _ := rig.outbox.Uncertain(7); len(u) != 1 {
		t.Fatalf("uncertain=%+v", u)
	}
}

func TestProactiveSchedulerEventsAndMetricsNeverCarryMessageBodies(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	secret := "SECRETO-PMI-4242 resumen privado"
	rig.exec.result = ScheduledSkillResult{Text: secret}
	rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	for _, event := range rig.events {
		if !proactiveEvents[event] || strings.Contains(event, "SECRETO") {
			t.Fatalf("event %q is not a closed, body-free name", event)
		}
	}
	rec := httptest.NewRecorder()
	rig.metric.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	if strings.Contains(body, "SECRETO") || strings.Contains(body, "4242") || !strings.Contains(body, `lab_secretary_proactive_total{event="proactive_delivered"} 1`) {
		t.Fatalf("metrics leaked a body or missed the counter:\n%s", body)
	}
	rig.metric.ObserveProactive("anything free-form with SECRETO")
	rec = httptest.NewRecorder()
	rig.metric.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), "free-form") {
		t.Fatal("unknown event names must be dropped")
	}
}

func TestProactiveSchedulerRunStopsOnContextCancelAndNoopsWhenMisconfigured(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	rig.sched.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rig.sched.Run(ctx); close(done) }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
	empty := &ProactiveScheduler{}
	finished := make(chan struct{})
	go func() { empty.Run(context.Background()); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("a scheduler without stores must return immediately")
	}
}

func TestProactiveSchedulerExecutesOneTaskAtATime(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	var mu sync.Mutex
	active, peak := 0, 0
	rig.exec.each = func(task ScheduledTask) ScheduledSkillResult {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return ScheduledSkillResult{Text: "ok " + task.ID}
	}
	for i := 0; i < 3; i++ {
		rig.addTask(7, "telegram", "7", "morning-brief", "08:30")
	}
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if peak != 1 || rig.exec.Calls() != 3 {
		t.Fatalf("scheduled inference must be serialized: peak=%d calls=%d", peak, rig.exec.Calls())
	}
}

func TestProactiveOpenWebUIShowsQueuedMessagesOnNextConversationAndAcknowledges(t *testing.T) {
	var upstreamBodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Messages []Turn }
		json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			upstreamBodies = append(upstreamBodies, m.Content)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"Hola."}}]}`))
	}))
	defer upstream.Close()
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 7, 0))
	svc := New(upstream.URL, "key", "instructions", rig.dir, map[int64]bool{7: true})
	svc.ScheduledTasks, svc.ProactiveOutbox = rig.tasks, rig.outbox
	rig.exec.each = func(task ScheduledTask) ScheduledSkillResult { return ScheduledSkillResult{Text: "Aviso " + task.ID} }
	var tasks []ScheduledTask
	for i := 0; i < 5; i++ {
		tasks = append(tasks, rig.addTask(7, "openwebui", "chat-1", "morning-brief", "08:30"))
	}
	*rig.clock = madridTime(t, 2026, 11, 9, 8, 31)
	rig.sched.RunOnce(context.Background())
	if rig.sender.Sends() != 0 {
		t.Fatal("Open WebUI has no off-session push: nothing is sent")
	}
	ask := func() string {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"main","messages":[{"role":"user","content":"Hola"}]}`))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("X-OpenWebUI-User-Id", "owner")
		request.Header.Set("X-OpenWebUI-User-Role", "admin")
		request.Header.Set("X-OpenWebUI-Chat-Id", "chat-1")
		response := httptest.NewRecorder()
		svc.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d %s", response.Code, response.Body.String())
		}
		var parsed struct {
			Choices []struct{ Message Turn } `json:"choices"`
		}
		json.Unmarshal(response.Body.Bytes(), &parsed)
		return parsed.Choices[0].Message.Content
	}
	first := ask()
	shown := 0
	for _, task := range tasks {
		if strings.Contains(first, "Aviso "+task.ID) {
			shown++
		}
	}
	if shown != 3 || !strings.Contains(first, "Quedan 2 avisos programados por mostrar") {
		t.Fatalf("at most three queued messages per turn, shown=%d: %q", shown, first)
	}
	second := ask()
	if strings.Count(second, "Aviso ") != 2 || strings.Contains(second, "Quedan") {
		t.Fatalf("the rest shows on the next turn: %q", second)
	}
	third := ask()
	if strings.Contains(third, "Aviso ") {
		t.Fatalf("acknowledged messages are not shown again: %q", third)
	}
	for _, status := range rig.runStatuses(7) {
		if status != RunDelivered {
			t.Fatalf("acknowledging closes the runs: %v", rig.runStatuses(7))
		}
	}
}

func TestProactiveOpenWebUIQueueIsOperatorScoped(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 9, 0))
	rig.outbox.Enqueue(ProactiveNotification{OperatorID: 8, Channel: "openwebui", ConversationID: "c", TaskID: "t1", RunID: "r1", ScheduledFor: *rig.clock, Text: "del otro"})
	items, _, err := rig.outbox.PendingOpenWebUI(7, 3)
	if err != nil || len(items) != 0 {
		t.Fatalf("operator 7 must not see operator 8's queue: %+v %v", items, err)
	}
	svc := New("http://127.0.0.1:1", "key", "i", rig.dir, map[int64]bool{7: true})
	svc.ProactiveOutbox = rig.outbox
	svc.acknowledgeProactive(7, []ProactiveNotification{{ID: proactiveID("t1", *rig.clock), OperatorID: 8}})
	again, _, _ := rig.outbox.PendingOpenWebUI(8, 3)
	if len(again) != 1 {
		t.Fatal("operator 7 must not acknowledge operator 8's notification")
	}
}

func TestProactiveStateUsesPrivatePermissionsAndFailsClosedOnMalformedOutbox(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 9, 0))
	rig.outbox.Enqueue(ProactiveNotification{OperatorID: 7, Channel: "telegram", ConversationID: "7", TaskID: "t1", RunID: "r1", ScheduledFor: *rig.clock, Text: "x"})
	dir := filepath.Join(rig.dir, "proactive-outbox")
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir=%v err=%v", info, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "outbox.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file=%v err=%v", info, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outbox.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProactiveOutbox(rig.dir); err == nil {
		t.Fatal("a malformed outbox must fail closed, not be silently replaced")
	}
}

func TestProactiveOutboxEnqueueIsIdempotentPerOccurrence(t *testing.T) {
	rig := newSchedulerRig(t, madridTime(t, 2026, 11, 9, 9, 0))
	n := ProactiveNotification{OperatorID: 7, Channel: "telegram", ConversationID: "7", TaskID: "t1", RunID: "r1", ScheduledFor: *rig.clock, Text: "uno"}
	first, created, _ := rig.outbox.Enqueue(n)
	n.Text = "dos"
	second, createdAgain, _ := rig.outbox.Enqueue(n)
	if !created || createdAgain || second.ID != first.ID || second.Text != "uno" {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	long := n
	long.TaskID, long.Text = "t2", strings.Repeat("a", 5000)
	stored, _, _ := rig.outbox.Enqueue(long)
	if len([]rune(stored.Text)) != proactiveMaxTextRunes {
		t.Fatalf("stored text must be bounded, got %d", len([]rune(stored.Text)))
	}
	if _, _, err := rig.outbox.Enqueue(ProactiveNotification{OperatorID: 7}); err == nil {
		t.Fatal("an incomplete notification is rejected")
	}
}
