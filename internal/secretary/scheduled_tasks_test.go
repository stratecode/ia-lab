package secretary

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func madrid(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func newTestScheduleStore(t *testing.T, now time.Time) (*ScheduledTaskStore, string, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	clock := now
	store, err := OpenScheduledTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Now = func() time.Time { return clock }
	return store, dir, &clock
}

func dailyTask(operator int64) ScheduledTask {
	return ScheduledTask{OperatorID: operator, Channel: "telegram", ConversationID: "1", SkillID: "morning-brief",
		Schedule: TaskSchedule{Cadence: CadenceDaily, LocalTime: "08:30", Timezone: "Europe/Madrid"}}
}

func TestScheduledTaskScheduleRejectsInvalidInput(t *testing.T) {
	bad := []TaskSchedule{
		{Cadence: "cron", LocalTime: "08:00", Timezone: "Europe/Madrid"},
		{Cadence: CadenceDaily, LocalTime: "25:00", Timezone: "Europe/Madrid"},
		{Cadence: CadenceDaily, LocalTime: "8", Timezone: "Europe/Madrid"},
		{Cadence: CadenceDaily, LocalTime: "08:00", Timezone: "Mars/Olympus"},
		{Cadence: CadenceDaily, LocalTime: "08:00"},
		{Cadence: CadenceOnce, Timezone: "Europe/Madrid"},
	}
	for _, schedule := range bad {
		if err := schedule.Validate(); err == nil {
			t.Errorf("schedule %+v should be rejected", schedule)
		}
	}
}

func TestScheduledTaskScheduleNextRunDailyAndWeekdays(t *testing.T) {
	loc := madrid(t)
	// Friday 2026-11-06 09:00 local: the daily 08:30 run is tomorrow, weekdays skip the weekend.
	after := time.Date(2026, 11, 6, 9, 0, 0, 0, loc)
	daily := TaskSchedule{Cadence: CadenceDaily, LocalTime: "08:30", Timezone: "Europe/Madrid"}
	next, err := daily.NextRun(after)
	if err != nil || !next.Equal(time.Date(2026, 11, 7, 8, 30, 0, 0, loc)) {
		t.Fatalf("daily next=%v err=%v", next, err)
	}
	weekdays := TaskSchedule{Cadence: CadenceWeekdays, LocalTime: "08:30", Timezone: "Europe/Madrid"}
	next, err = weekdays.NextRun(after)
	if err != nil || !next.Equal(time.Date(2026, 11, 9, 8, 30, 0, 0, loc)) {
		t.Fatalf("weekdays next=%v err=%v", next, err)
	}
	// Exactly at the scheduled instant the next occurrence is strictly later.
	next, _ = daily.NextRun(time.Date(2026, 11, 6, 8, 30, 0, 0, loc))
	if !next.Equal(time.Date(2026, 11, 7, 8, 30, 0, 0, loc)) {
		t.Fatalf("occurrence at 'after' must not repeat, got %v", next)
	}
}

func TestScheduledTaskScheduleOnce(t *testing.T) {
	loc := madrid(t)
	runAt := time.Date(2026, 11, 9, 10, 0, 0, 0, loc)
	once := TaskSchedule{Cadence: CadenceOnce, RunAt: runAt, Timezone: "Europe/Madrid"}
	next, err := once.NextRun(runAt.Add(-time.Hour))
	if err != nil || !next.Equal(runAt) {
		t.Fatalf("once next=%v err=%v", next, err)
	}
	if next, err = once.NextRun(runAt); err != nil || !next.IsZero() {
		t.Fatalf("a spent one-off schedule has no next run, got %v %v", next, err)
	}
}

func TestScheduledTaskScheduleKeepsWallClockAcrossDST(t *testing.T) {
	loc := madrid(t)
	// Madrid leaves summer time on 2026-10-25 (CEST -> CET). A weekly Monday-style
	// daily 08:30 schedule must stay at 08:30 local, i.e. 25 hours apart that day.
	daily := TaskSchedule{Cadence: CadenceDaily, LocalTime: "08:30", Timezone: "Europe/Madrid"}
	before := time.Date(2026, 10, 24, 8, 0, 0, 0, loc)
	first, _ := daily.NextRun(before)
	second, _ := daily.NextRun(first)
	third, _ := daily.NextRun(second)
	for _, tm := range []time.Time{first, second, third} {
		in := tm.In(loc)
		if in.Hour() != 8 || in.Minute() != 30 {
			t.Fatalf("wall clock drifted: %v", in)
		}
	}
	if second.Sub(first) != 25*time.Hour {
		t.Fatalf("fall-back day should be 25h long, got %v", second.Sub(first))
	}
}

func TestScheduledTaskScheduleDefersNonexistentLocalTime(t *testing.T) {
	loc := madrid(t)
	// 2026-03-29 02:30 does not exist in Madrid (02:00 jumps to 03:00).
	daily := TaskSchedule{Cadence: CadenceDaily, LocalTime: "02:30", Timezone: "Europe/Madrid"}
	next, err := daily.NextRun(time.Date(2026, 3, 28, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	local := next.In(loc)
	if local.Year() != 2026 || local.Month() != 3 || local.Day() != 29 {
		t.Fatalf("the occurrence must not be skipped or moved a day: %v", local)
	}
	if local.Hour() != 3 || local.Minute() != 0 {
		t.Fatalf("a nonexistent time moves to the first valid instant after the gap (03:00), got %v", local)
	}
}

func TestScheduledTaskStoreCreateListAndOperatorIsolation(t *testing.T) {
	loc := madrid(t)
	store, _, _ := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	mine, err := store.Create(dailyTask(1))
	if err != nil {
		t.Fatal(err)
	}
	if mine.ID == "" || mine.Enabled || mine.NextRunAt.IsZero() {
		t.Fatalf("a created task is a disabled draft with a next run: %+v", mine)
	}
	if _, err := store.Create(dailyTask(2)); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(1)
	if err != nil || len(list) != 1 || list[0].ID != mine.ID {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if _, ok, _ := store.Get(2, mine.ID); ok {
		t.Fatal("another operator must not read the task")
	}
	if err := store.SetEnabled(2, mine.ID, true); err == nil {
		t.Fatal("another operator must not enable the task")
	}
}

func TestScheduledTaskStorePauseResumeRecomputesNextRun(t *testing.T) {
	loc := madrid(t)
	store, _, clock := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	if err := store.SetEnabled(1, task.ID, true); err != nil {
		t.Fatal(err)
	}
	*clock = time.Date(2026, 11, 20, 12, 0, 0, 0, loc)
	if err := store.SetEnabled(1, task.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(1, task.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _, _ := store.Get(1, task.ID)
	if !got.Enabled || !got.NextRunAt.Equal(time.Date(2026, 11, 21, 8, 30, 0, 0, loc)) {
		t.Fatalf("resume must not replay missed days: %+v", got)
	}
}

func TestScheduledTaskStoreUnconfirmedDraftIsNeverClaimed(t *testing.T) {
	loc := madrid(t)
	store, _, clock := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	if _, err := store.Create(dailyTask(1)); err != nil {
		t.Fatal(err)
	}
	*clock = time.Date(2026, 11, 10, 9, 0, 0, 0, loc)
	claims, err := store.ClaimDue(*clock, 10)
	if err != nil || len(claims) != 0 {
		t.Fatalf("draft claimed: %+v %v", claims, err)
	}
}

func TestScheduledTaskStoreClaimDueDeduplicatesAndAdvances(t *testing.T) {
	loc := madrid(t)
	store, _, clock := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	store.SetEnabled(1, task.ID, true)
	*clock = time.Date(2026, 11, 7, 8, 31, 0, 0, loc)
	first, err := store.ClaimDue(*clock, 10)
	if err != nil || len(first) != 1 || first[0].Task.ID != task.ID || first[0].Run.Status != RunRunning {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := store.ClaimDue(*clock, 10)
	if err != nil || len(second) != 0 {
		t.Fatalf("a second tick must not claim the occurrence again: %+v %v", second, err)
	}
	got, _, _ := store.Get(1, task.ID)
	if !got.NextRunAt.Equal(time.Date(2026, 11, 8, 8, 30, 0, 0, loc)) {
		t.Fatalf("next run not advanced: %v", got.NextRunAt)
	}
}

func TestScheduledTaskStoreRestartResumesClaimedRunOnce(t *testing.T) {
	loc := madrid(t)
	store, dir, clock := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	store.SetEnabled(1, task.ID, true)
	*clock = time.Date(2026, 11, 7, 8, 31, 0, 0, loc)
	claims, _ := store.ClaimDue(*clock, 10)
	if len(claims) != 1 {
		t.Fatalf("claims=%+v", claims)
	}
	// Process dies before FinishRun; a new process opens the same state.
	reopened, err := OpenScheduledTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.Now = func() time.Time { return *clock }
	resumed, err := reopened.ClaimDue(*clock, 10)
	if err != nil || len(resumed) != 1 {
		t.Fatalf("the unfinished run must be resumed: %+v %v", resumed, err)
	}
	if resumed[0].Run.ID != claims[0].Run.ID || resumed[0].Run.Attempt != 2 {
		t.Fatalf("resume must reuse the logical run: first=%+v resumed=%+v", claims[0].Run, resumed[0].Run)
	}
	if err := reopened.FinishRun(resumed[0].Run.ID, RunDelivered, ""); err != nil {
		t.Fatal(err)
	}
	again, _ := reopened.ClaimDue(*clock, 10)
	if len(again) != 0 {
		t.Fatalf("a finished run is never claimed again: %+v", again)
	}
	runs, _ := reopened.Runs(1)
	if len(runs) != 1 {
		t.Fatalf("exactly one logical run expected, got %d", len(runs))
	}
}

func TestScheduledTaskStoreStaleOccurrenceIsSkippedNotReplayed(t *testing.T) {
	loc := madrid(t)
	store, _, clock := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	store.SetEnabled(1, task.ID, true)
	// The service was down for three days: only a stale, skipped record remains.
	*clock = time.Date(2026, 11, 10, 15, 0, 0, 0, loc)
	claims, err := store.ClaimDue(*clock, 10)
	if err != nil || len(claims) != 0 {
		t.Fatalf("a stale occurrence must not run: %+v %v", claims, err)
	}
	runs, _ := store.Runs(1)
	if len(runs) != 1 || runs[0].Status != RunSkipped {
		t.Fatalf("stale occurrence must be recorded as skipped: %+v", runs)
	}
	got, _, _ := store.Get(1, task.ID)
	if !got.NextRunAt.After(*clock) {
		t.Fatalf("next run must be in the future: %v", got.NextRunAt)
	}
}

func TestScheduledTaskStoreOnceTaskFiresOnceThenDisables(t *testing.T) {
	loc := madrid(t)
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, loc)
	store, _, clock := newTestScheduleStore(t, now)
	task := dailyTask(1)
	task.Schedule = TaskSchedule{Cadence: CadenceOnce, RunAt: now.Add(time.Hour), Timezone: "Europe/Madrid"}
	created, err := store.Create(task)
	if err != nil {
		t.Fatal(err)
	}
	store.SetEnabled(1, created.ID, true)
	*clock = now.Add(time.Hour + time.Minute)
	claims, _ := store.ClaimDue(*clock, 10)
	if len(claims) != 1 {
		t.Fatalf("claims=%+v", claims)
	}
	got, _, _ := store.Get(1, created.ID)
	if got.Enabled || !got.NextRunAt.IsZero() {
		t.Fatalf("a one-off task disables itself after its claim: %+v", got)
	}
}

func TestScheduledTaskStoreCancelHidesTask(t *testing.T) {
	loc := madrid(t)
	store, _, _ := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	if err := store.Cancel(2, task.ID); err == nil {
		t.Fatal("another operator must not cancel")
	}
	if err := store.Cancel(1, task.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := store.List(1); len(list) != 0 {
		t.Fatalf("canceled task should not be listed: %+v", list)
	}
	if err := store.SetEnabled(1, task.ID, true); err == nil {
		t.Fatal("a canceled task cannot be re-enabled")
	}
}

func TestScheduledTaskStoreLimitsActiveTasksPerOperator(t *testing.T) {
	loc := madrid(t)
	store, _, _ := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	for i := 0; i < maxScheduledTasksPerOperator; i++ {
		if _, err := store.Create(dailyTask(1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Create(dailyTask(1)); err == nil {
		t.Fatal("the per-operator task limit must be enforced")
	}
	if _, err := store.Create(dailyTask(2)); err != nil {
		t.Fatal("another operator is unaffected")
	}
}

func TestScheduledTaskStoreMalformedStateIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "scheduled-tasks")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "tasks.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenScheduledTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(dailyTask(1)); err == nil {
		t.Fatal("malformed state must fail closed")
	}
	if b, _ := os.ReadFile(path); string(b) != "{not json" {
		t.Fatalf("malformed state was overwritten: %q", b)
	}
}

func TestScheduledTaskStoreUsesPrivatePermissions(t *testing.T) {
	loc := madrid(t)
	store, dir, _ := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	task, _ := store.Create(dailyTask(1))
	store.SetEnabled(1, task.ID, true)
	root := filepath.Join(dir, "scheduled-tasks")
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v %v", info, err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) == 0 {
		t.Fatal("no state written")
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
		fi, _ := entry.Info()
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", entry.Name(), fi.Mode().Perm())
		}
	}
}

func TestScheduledTaskStoreDoesNotReuseTimerState(t *testing.T) {
	loc := madrid(t)
	store, dir, _ := newTestScheduleStore(t, time.Date(2026, 11, 6, 9, 0, 0, 0, loc))
	store.Create(dailyTask(1))
	if _, err := os.Stat(filepath.Join(dir, "timers.json")); !os.IsNotExist(err) {
		t.Fatal("scheduled tasks must not touch timers.json")
	}
}
