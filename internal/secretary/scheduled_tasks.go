package secretary

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the secretary binary must not depend on host zoneinfo
)

// ScheduleCadence is the recurrence of a scheduled task. Cron expressions are
// deliberately unsupported: every accepted form is explicit and testable.
type ScheduleCadence string

const (
	CadenceOnce     ScheduleCadence = "once"
	CadenceDaily    ScheduleCadence = "daily"
	CadenceWeekdays ScheduleCadence = "weekdays"
)

// DefaultScheduleTimezone is used when the operator does not name one.
const DefaultScheduleTimezone = "Europe/Madrid"

const (
	maxScheduledTasksPerOperator = 20
	maxScheduledRunsKept         = 200
	scheduledRunRetention        = 30 * 24 * time.Hour
	// scheduledStaleAfter is how late an occurrence may still run. A service that
	// was down for longer records the occurrence as skipped instead of replaying it.
	scheduledStaleAfter  = 6 * time.Hour
	scheduledMaxAttempts = 3
)

// TaskSchedule says when a task runs. RunAt is used by once; LocalTime by the
// recurring cadences, always evaluated in the named IANA Timezone.
type TaskSchedule struct {
	Cadence   ScheduleCadence `json:"cadence"`
	LocalTime string          `json:"local_time,omitempty"`
	RunAt     time.Time       `json:"run_at,omitempty"`
	Timezone  string          `json:"timezone"`
}

// ScheduledTask is bound to the operator and channel that created it. Nothing
// the operator types can change those bindings.
type ScheduledTask struct {
	ID             string       `json:"id"`
	OperatorID     int64        `json:"operator_id"`
	Channel        string       `json:"channel"`
	ConversationID string       `json:"conversation_id"`
	SkillID        string       `json:"skill_id"`
	Schedule       TaskSchedule `json:"schedule"`
	Enabled        bool         `json:"enabled"`
	Canceled       bool         `json:"canceled,omitempty"`
	// ConfirmedAt is zero while the task is a draft; the first enable sets it.
	ConfirmedAt time.Time `json:"confirmed_at,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	NextRunAt   time.Time `json:"next_run_at"`
}

type ScheduledRunStatus string

const (
	RunPending   ScheduledRunStatus = "pending"
	RunRunning   ScheduledRunStatus = "running"
	RunDelivered ScheduledRunStatus = "delivered"
	RunSkipped   ScheduledRunStatus = "skipped"
	RunFailed    ScheduledRunStatus = "failed"
	RunUncertain ScheduledRunStatus = "uncertain"
)

func (s ScheduledRunStatus) finished() bool {
	switch s {
	case RunDelivered, RunSkipped, RunFailed, RunUncertain:
		return true
	}
	return false
}

// ScheduledRun is one logical occurrence of a task, identified by the task and
// the UTC instant it was scheduled for.
type ScheduledRun struct {
	ID           string             `json:"id"`
	TaskID       string             `json:"task_id"`
	OperatorID   int64              `json:"operator_id"`
	ScheduledFor time.Time          `json:"scheduled_for"`
	Status       ScheduledRunStatus `json:"status"`
	Attempt      int                `json:"attempt"`
	StartedAt    time.Time          `json:"started_at,omitempty"`
	FinishedAt   time.Time          `json:"finished_at,omitempty"`
	ErrorCode    string             `json:"error_code,omitempty"`
}

// ClaimedRun is a persisted claim ready to execute.
type ClaimedRun struct {
	Task ScheduledTask
	Run  ScheduledRun
}

// Validate rejects schedules instead of guessing what was meant.
func (s TaskSchedule) Validate() error {
	if _, err := time.LoadLocation(s.Timezone); err != nil || strings.TrimSpace(s.Timezone) == "" {
		return errors.New("invalid timezone")
	}
	switch s.Cadence {
	case CadenceOnce:
		if s.RunAt.IsZero() {
			return errors.New("a one-off schedule needs a time")
		}
	case CadenceDaily, CadenceWeekdays:
		if _, _, err := parseLocalClock(s.LocalTime); err != nil {
			return err
		}
	default:
		return errors.New("unsupported cadence")
	}
	return nil
}

func parseLocalClock(value string) (int, int, error) {
	t, err := time.Parse("15:04", value)
	if err != nil || len(value) != 5 {
		return 0, 0, errors.New("invalid local time")
	}
	return t.Hour(), t.Minute(), nil
}

// NextRun returns the first occurrence strictly after the given instant, or the
// zero time when a one-off schedule has already fired. A wall-clock time that does
// not exist on a day (spring-forward gap) moves to the first valid instant after
// the gap; an ambiguous one (fall-back) uses its first occurrence.
func (s TaskSchedule) NextRun(after time.Time) (time.Time, error) {
	if err := s.Validate(); err != nil {
		return time.Time{}, err
	}
	loc, _ := time.LoadLocation(s.Timezone)
	if s.Cadence == CadenceOnce {
		if s.RunAt.After(after) {
			return s.RunAt.UTC(), nil
		}
		return time.Time{}, nil
	}
	hour, minute, _ := parseLocalClock(s.LocalTime)
	day := after.In(loc)
	for i := 0; i < 400; i++ {
		date := day.AddDate(0, 0, i)
		if s.Cadence == CadenceWeekdays && (date.Weekday() == time.Saturday || date.Weekday() == time.Sunday) {
			continue
		}
		candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, loc)
		if candidate.Hour() != hour || candidate.Minute() != minute {
			candidate = firstInstantOfGap(candidate)
		}
		if candidate.After(after) {
			return candidate.UTC(), nil
		}
	}
	return time.Time{}, errors.New("no next run found")
}

// firstInstantOfGap walks back to the instant the clock jumped forward.
func firstInstantOfGap(t time.Time) time.Time {
	_, offset := t.Zone()
	for i := 0; i < 180; i++ {
		previous := t.Add(-time.Minute)
		if _, o := previous.Zone(); o != offset {
			return t
		}
		t = previous
	}
	return t
}

// ScheduledTaskStore persists tasks and runs under StateDir/scheduled-tasks.
// It never shares the schema of the one-shot timers.json.
type ScheduledTaskStore struct {
	dir string
	mu  sync.Mutex
	// inflight holds runs this process claimed and has not finished, so a
	// later tick does not mistake them for runs left over by a dead process.
	inflight map[string]bool
	// Now is the clock; tests replace it.
	Now func() time.Time
}

type scheduledTaskFile struct {
	Tasks []ScheduledTask `json:"tasks"`
}

type scheduledRunFile struct {
	Runs []ScheduledRun `json:"runs"`
}

func OpenScheduledTaskStore(stateDir string) (*ScheduledTaskStore, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("state directory required")
	}
	dir := filepath.Join(stateDir, "scheduled-tasks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, errors.New("cannot create scheduled task state")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, errors.New("cannot protect scheduled task state")
	}
	return &ScheduledTaskStore{dir: dir, Now: time.Now, inflight: map[string]bool{}}, nil
}

func (s *ScheduledTaskStore) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}
	return s.Now()
}

func (s *ScheduledTaskStore) tasksPath() string { return filepath.Join(s.dir, "tasks.json") }
func (s *ScheduledTaskStore) runsPath() string  { return filepath.Join(s.dir, "runs.json") }

func (s *ScheduledTaskStore) loadTasks() ([]ScheduledTask, error) {
	b, err := os.ReadFile(s.tasksPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read scheduled task state")
	}
	var file scheduledTaskFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, errors.New("invalid scheduled task state")
	}
	return file.Tasks, nil
}

func (s *ScheduledTaskStore) loadRuns() ([]ScheduledRun, error) {
	b, err := os.ReadFile(s.runsPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read scheduled run state")
	}
	var file scheduledRunFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil, errors.New("invalid scheduled run state")
	}
	return file.Runs, nil
}

func (s *ScheduledTaskStore) saveTasks(tasks []ScheduledTask) error {
	if err := atomicJSON(s.tasksPath(), scheduledTaskFile{Tasks: tasks}); err != nil {
		return errors.New("cannot persist scheduled task state")
	}
	return nil
}

func (s *ScheduledTaskStore) saveRuns(runs []ScheduledRun) error {
	if err := atomicJSON(s.runsPath(), scheduledRunFile{Runs: pruneScheduledRuns(runs, s.now())}); err != nil {
		return errors.New("cannot persist scheduled run state")
	}
	return nil
}

func pruneScheduledRuns(runs []ScheduledRun, now time.Time) []ScheduledRun {
	kept := make([]ScheduledRun, 0, len(runs))
	for _, run := range runs {
		if run.Status.finished() && !run.FinishedAt.IsZero() && now.Sub(run.FinishedAt) > scheduledRunRetention {
			continue
		}
		kept = append(kept, run)
	}
	if len(kept) > maxScheduledRunsKept {
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].ScheduledFor.Before(kept[j].ScheduledFor) })
		kept = kept[len(kept)-maxScheduledRunsKept:]
	}
	return kept
}

func newScheduleID(prefix string) (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("cannot create id")
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

// Create stores a disabled draft. The caller supplies operator, channel and
// conversation from trusted adapter context; ID, CreatedAt and NextRunAt are set here.
func (s *ScheduledTaskStore) Create(task ScheduledTask) (ScheduledTask, error) {
	if task.OperatorID == 0 || strings.TrimSpace(task.Channel) == "" || strings.TrimSpace(task.SkillID) == "" {
		return ScheduledTask{}, errors.New("operator, channel and skill are required")
	}
	if err := task.Schedule.Validate(); err != nil {
		return ScheduledTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return ScheduledTask{}, err
	}
	active := 0
	for _, existing := range tasks {
		if existing.OperatorID == task.OperatorID && !existing.Canceled {
			active++
		}
	}
	if active >= maxScheduledTasksPerOperator {
		return ScheduledTask{}, errors.New("scheduled task limit reached")
	}
	id, err := newScheduleID("t")
	if err != nil {
		return ScheduledTask{}, err
	}
	now := s.now().UTC()
	next, err := task.Schedule.NextRun(now)
	if err != nil {
		return ScheduledTask{}, err
	}
	task.ID, task.Enabled, task.Canceled, task.CreatedAt, task.NextRunAt = id, false, false, now, next
	if err := s.saveTasks(append(tasks, task)); err != nil {
		return ScheduledTask{}, err
	}
	return task, nil
}

// List returns the operator's tasks, newest last. Canceled tasks are omitted.
func (s *ScheduledTaskStore) List(operatorID int64) ([]ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledTask, 0, len(tasks))
	for _, task := range tasks {
		if task.OperatorID == operatorID && !task.Canceled {
			out = append(out, task)
		}
	}
	return out, nil
}

func (s *ScheduledTaskStore) Get(operatorID int64, id string) (ScheduledTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return ScheduledTask{}, false, err
	}
	for _, task := range tasks {
		if task.ID == id && task.OperatorID == operatorID && !task.Canceled {
			return task, true, nil
		}
	}
	return ScheduledTask{}, false, nil
}

var errScheduledTaskNotFound = errors.New("scheduled task not found")

// SetEnabled confirms (true) or pauses (false) a task. Enabling recomputes the
// next run from now, so a resumed task never replays what it missed while paused.
func (s *ScheduledTaskStore) SetEnabled(operatorID int64, id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return err
	}
	for i := range tasks {
		if tasks[i].ID != id || tasks[i].OperatorID != operatorID || tasks[i].Canceled {
			continue
		}
		if enabled {
			next, err := tasks[i].Schedule.NextRun(s.now())
			if err != nil {
				return err
			}
			if next.IsZero() {
				return errors.New("the scheduled time has already passed")
			}
			tasks[i].NextRunAt = next
		}
		tasks[i].Enabled = enabled
		if enabled && tasks[i].ConfirmedAt.IsZero() {
			tasks[i].ConfirmedAt = s.now().UTC()
		}
		return s.saveTasks(tasks)
	}
	return errScheduledTaskNotFound
}

// Cancel removes a task from the operator's view and from scheduling for good.
func (s *ScheduledTaskStore) Cancel(operatorID int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return err
	}
	for i := range tasks {
		if tasks[i].ID == id && tasks[i].OperatorID == operatorID && !tasks[i].Canceled {
			tasks[i].Canceled, tasks[i].Enabled = true, false
			return s.saveTasks(tasks)
		}
	}
	return errScheduledTaskNotFound
}

// Runs returns the operator's recorded runs, oldest first.
func (s *ScheduledTaskStore) Runs(operatorID int64) ([]ScheduledRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs, err := s.loadRuns()
	if err != nil {
		return nil, err
	}
	out := make([]ScheduledRun, 0, len(runs))
	for _, run := range runs {
		if run.OperatorID == operatorID {
			out = append(out, run)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ScheduledFor.Before(out[j].ScheduledFor) })
	return out, nil
}

// ClaimDue persists a claim for each due occurrence before returning it, and
// resumes runs a previous process claimed but never finished. A claim is keyed
// by (task, scheduled UTC instant), so repeated ticks and restarts never create
// a second logical run. At most limit runs are returned.
func (s *ScheduledTaskStore) ClaimDue(now time.Time, limit int) ([]ClaimedRun, error) {
	if limit < 1 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.loadTasks()
	if err != nil {
		return nil, err
	}
	runs, err := s.loadRuns()
	if err != nil {
		return nil, err
	}
	byTask := map[string]ScheduledTask{}
	for _, task := range tasks {
		byTask[task.ID] = task
	}
	var claimed []ClaimedRun
	dirtyRuns, dirtyTasks := false, false

	// Resume unfinished runs first (process died between claim and finish).
	for i := range runs {
		if len(claimed) >= limit {
			break
		}
		run := &runs[i]
		task, ok := byTask[run.TaskID]
		if run.Status.finished() || !ok || task.Canceled || s.inflight[run.ID] {
			continue
		}
		if run.Attempt >= scheduledMaxAttempts {
			run.Status, run.FinishedAt, run.ErrorCode = RunFailed, now.UTC(), "attempts_exhausted"
			dirtyRuns = true
			continue
		}
		if run.Status == RunRunning || run.Status == RunPending {
			run.Attempt++
			run.Status, run.StartedAt = RunRunning, now.UTC()
			dirtyRuns = true
			claimed = append(claimed, ClaimedRun{Task: task, Run: *run})
		}
	}

	seen := map[string]bool{}
	for _, run := range runs {
		seen[runKey(run.TaskID, run.ScheduledFor)] = true
	}
	for i := range tasks {
		task := &tasks[i]
		if !task.Enabled || task.Canceled || task.NextRunAt.IsZero() || task.NextRunAt.After(now) {
			continue
		}
		scheduledFor := task.NextRunAt.UTC()
		key := runKey(task.ID, scheduledFor)
		if !seen[key] {
			if len(claimed) >= limit {
				continue // leave the task due; the next tick picks it up
			}
			id, err := newScheduleID("r")
			if err != nil {
				return nil, err
			}
			run := ScheduledRun{ID: id, TaskID: task.ID, OperatorID: task.OperatorID, ScheduledFor: scheduledFor, Status: RunRunning, Attempt: 1, StartedAt: now.UTC()}
			if now.Sub(scheduledFor) > scheduledStaleAfter {
				run.Status, run.Attempt, run.StartedAt, run.FinishedAt, run.ErrorCode = RunSkipped, 0, time.Time{}, now.UTC(), "missed_while_down"
			} else {
				claimed = append(claimed, ClaimedRun{Task: *task, Run: run})
			}
			runs = append(runs, run)
			seen[key] = true
			dirtyRuns = true
		}
		// Advance after the claim itself is the persisted source of truth: the
		// run is written before the task moves, so a crash in between is a dedupe.
		next, err := task.Schedule.NextRun(now)
		if err != nil {
			return nil, err
		}
		task.NextRunAt = next
		if next.IsZero() {
			task.Enabled = false
		}
		dirtyTasks = true
	}
	if dirtyRuns {
		if err := s.saveRuns(runs); err != nil {
			return nil, err
		}
	}
	if dirtyTasks {
		if err := s.saveTasks(tasks); err != nil {
			return nil, err
		}
	}
	for i := range claimed {
		s.inflight[claimed[i].Run.ID] = true
		if updated, ok := findRun(runs, claimed[i].Run.ID); ok {
			claimed[i].Run = updated
		}
		if task, ok := findTask(tasks, claimed[i].Task.ID); ok {
			claimed[i].Task = task
		}
	}
	return claimed, nil
}

func runKey(taskID string, scheduledFor time.Time) string {
	return fmt.Sprintf("%s@%d", taskID, scheduledFor.UTC().Unix())
}

func findRun(runs []ScheduledRun, id string) (ScheduledRun, bool) {
	for _, run := range runs {
		if run.ID == id {
			return run, true
		}
	}
	return ScheduledRun{}, false
}

func findTask(tasks []ScheduledTask, id string) (ScheduledTask, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return ScheduledTask{}, false
}

// FinishRun records the final status of a run. Finishing twice is rejected so a
// late duplicate cannot overwrite the outcome.
func (s *ScheduledTaskStore) FinishRun(runID string, status ScheduledRunStatus, errorCode string) error {
	if !status.finished() {
		return errors.New("a run finishes with a final status")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs, err := s.loadRuns()
	if err != nil {
		return err
	}
	for i := range runs {
		if runs[i].ID != runID {
			continue
		}
		if runs[i].Status.finished() {
			return errors.New("run already finished")
		}
		runs[i].Status, runs[i].FinishedAt, runs[i].ErrorCode = status, s.now().UTC(), boundedLabel(errorCode)
		delete(s.inflight, runID)
		return s.saveRuns(runs)
	}
	return errors.New("scheduled run not found")
}
