package secretary

import (
	"context"
	"strings"
	"testing"
	"time"
)

func journalLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(proactiveTimezone)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestSlotDueOnlyOnWeekdayWorkHoursOncePerSlot(t *testing.T) {
	j, err := OpenWorkJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loc := journalLoc(t)
	at := func(day, hour, min int) time.Time { return time.Date(2026, 11, day, hour, min, 0, 0, loc) }
	// 2026-11-09 is a Monday.
	if _, ok := j.SlotDue(at(9, 10, 2), loc); !ok {
		t.Fatal("Monday 10:02 should be due")
	}
	for name, when := range map[string]time.Time{"before hours": at(9, 8, 5), "after hours": at(9, 19, 1), "late in the hour": at(9, 11, 25), "saturday": at(14, 11, 1)} {
		if _, ok := j.SlotDue(when, loc); ok {
			t.Errorf("%s must not be due", name)
		}
	}
	slot, _ := j.SlotDue(at(9, 10, 2), loc)
	if err := j.Asked(slot, 77, at(9, 10, 2)); err != nil {
		t.Fatal(err)
	}
	if _, ok := j.SlotDue(at(9, 10, 5), loc); ok {
		t.Fatal("a slot is asked once")
	}
	if _, ok := j.SlotDue(at(9, 11, 1), loc); !ok {
		t.Fatal("the next hour is a new slot")
	}
}

func TestAnswerOnlyTiesToTheOpenCheckin(t *testing.T) {
	dir := t.TempDir()
	j, _ := OpenWorkJournal(dir)
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	_ = j.Asked("s1", 42, now)
	if ok, _ := j.Answer(99, "hola", now.Add(time.Minute)); ok {
		t.Fatal("a reply to another message must not be captured")
	}
	if ok, _ := j.Answer(42, "  revisando   el PR  ", now.Add(time.Minute)); !ok {
		t.Fatal("reply to the check-in should be captured")
	}
	if ok, _ := j.Answer(42, "otra vez", now.Add(2*time.Minute)); ok {
		t.Fatal("a check-in is answered once")
	}
	reopened, _ := OpenWorkJournal(dir)
	st := reopened.Status(now, 5)
	if st.Answered != 1 || len(st.LatestAnswers) != 1 || st.LatestAnswers[0].Text != "revisando el PR" {
		t.Fatalf("answer not persisted verbatim: %+v", st)
	}
}

func TestUnansweredCheckinBecomesSkipped(t *testing.T) {
	j, _ := OpenWorkJournal(t.TempDir())
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	_ = j.Asked("s1", 42, now)
	j.Expire(now.Add(journalAnswerWindow + time.Minute))
	if ok, _ := j.Answer(42, "tarde", now.Add(2*time.Hour)); ok {
		t.Fatal("an expired check-in cannot be answered")
	}
	if st := j.Status(now, 5); st.Skipped != 1 || st.Answered != 0 {
		t.Fatalf("expected one skipped entry: %+v", st)
	}
}

func TestWindowEndsAfterTwoWeeksAndAnnouncesOnce(t *testing.T) {
	j, _ := OpenWorkJournal(t.TempDir())
	loc := journalLoc(t)
	start := time.Date(2026, 11, 9, 10, 0, 0, 0, loc)
	_ = j.Asked("s1", 0, start)
	_ = j.Note("algo", start.Add(time.Minute))
	later := start.Add(journalWindow + 25*time.Hour) // a weekday inside work hours
	if _, ok := j.SlotDue(time.Date(2026, 11, 24, 10, 1, 0, 0, loc), loc); ok {
		t.Fatal("no questions after the window")
	}
	n, due := j.DoneNotice(later)
	if !due || n != 1 {
		t.Fatalf("expected one done notice with 1 note, got %d %v", n, due)
	}
	_ = j.MarkDoneNotified()
	if _, due := j.DoneNotice(later); due {
		t.Fatal("the notice is sent once")
	}
}

func TestPauseStopResumeAndForget(t *testing.T) {
	j, _ := OpenWorkJournal(t.TempDir())
	loc := journalLoc(t)
	now := time.Date(2026, 11, 9, 10, 1, 0, 0, loc)
	if _, err := j.Pause(48*time.Hour, now); err == nil {
		t.Fatal("pause is capped at 24 hours")
	}
	_, _ = j.Pause(time.Hour, now)
	if _, ok := j.SlotDue(now, loc); ok {
		t.Fatal("paused")
	}
	_ = j.Resume()
	if _, ok := j.SlotDue(now, loc); !ok {
		t.Fatal("resumed")
	}
	_ = j.Note("x", now)
	_ = j.Stop()
	_ = j.Forget()
	if st := j.Status(now, 5); st.Answered != 0 || !st.Stopped {
		t.Fatalf("forget wipes entries but keeps the stop: %+v", st)
	}
	if _, ok := j.SlotDue(now, loc); ok {
		t.Fatal("a wipe must not restart the questions")
	}
}

type fakeCheckinSender struct {
	outcome ProactiveSendOutcome
	sent    []string
}

func (f *fakeCheckinSender) SendCheckin(_ context.Context, _ int64, text string) (int64, ProactiveSendOutcome) {
	f.sent = append(f.sent, text)
	return 1000 + int64(len(f.sent)), f.outcome
}

func TestSchedulerAsksOncePerSlotAndRetriesOnlyRejectedSends(t *testing.T) {
	loc := journalLoc(t)
	j, _ := OpenWorkJournal(t.TempDir())
	now := time.Date(2026, 11, 9, 10, 1, 0, 0, loc)
	sender := &fakeCheckinSender{outcome: SendFailed}
	c := &CheckinScheduler{Journal: j, Sender: sender, ChatID: 5, Now: func() time.Time { return now }}
	c.RunOnce(context.Background())
	c.RunOnce(context.Background())
	if len(sender.sent) != 2 {
		t.Fatalf("a rejected send is retried within the slot, got %d", len(sender.sent))
	}
	sender.outcome = SendDelivered
	c.RunOnce(context.Background())
	c.RunOnce(context.Background())
	if len(sender.sent) != 3 {
		t.Fatalf("a delivered slot is not repeated, got %d", len(sender.sent))
	}
	if !strings.Contains(sender.sent[2], "Responde a este mensaje") {
		t.Fatalf("question must say how to answer: %q", sender.sent[2])
	}
	if !j.IsOpen(1003, now) {
		t.Fatal("the delivered question must be open for an answer")
	}
	uncertain := &fakeCheckinSender{outcome: SendUncertain}
	j2, _ := OpenWorkJournal(t.TempDir())
	c2 := &CheckinScheduler{Journal: j2, Sender: uncertain, ChatID: 5, Now: func() time.Time { return now }}
	c2.RunOnce(context.Background())
	c2.RunOnce(context.Background())
	if len(uncertain.sent) != 1 {
		t.Fatalf("an uncertain send is never repeated, got %d", len(uncertain.sent))
	}
}

func TestDiarioCommand(t *testing.T) {
	svc := &Service{}
	if answer, handled := svc.journalCommand("/diario"); !handled || !strings.Contains(answer, "no están activados") {
		t.Fatalf("disabled mode must say so: %q %v", answer, handled)
	}
	if _, handled := svc.journalCommand("hola"); handled {
		t.Fatal("only /diario is a journal command")
	}
	svc.Journal, _ = OpenWorkJournal(t.TempDir())
	if answer, _ := svc.journalCommand("/diario hoy estoy con la migración"); answer != "Anotado." {
		t.Fatalf("free text is a note: %q", answer)
	}
	if answer, _ := svc.journalCommand("/diario ver"); !strings.Contains(answer, "migración") {
		t.Fatalf("view shows notes: %q", answer)
	}
	if answer, _ := svc.journalCommand("/diario olvidar"); !strings.Contains(answer, "confirmar") {
		t.Fatalf("wipe needs confirmation: %q", answer)
	}
	if st := svc.Journal.Status(time.Now(), 5); st.Answered != 1 {
		t.Fatal("an unconfirmed wipe must delete nothing")
	}
	_, _ = svc.journalCommand("/diario olvidar confirmar")
	if st := svc.Journal.Status(time.Now(), 5); st.Answered != 0 {
		t.Fatal("confirmed wipe deletes")
	}
	if answer, _ := svc.journalCommand("/diario pausa 5d"); !strings.Contains(answer, "/diario pausa") {
		t.Fatalf("bad pause explained: %q", answer)
	}
}
