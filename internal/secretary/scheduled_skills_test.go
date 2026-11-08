package secretary

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type skillFixture struct {
	runner  *ScheduledSkillRunner
	reads   map[string][]string
	compose *int32
}

func newSkillFixture(jira, granola func(string) (CapabilityResult, error), composed string, composeErr error) skillFixture {
	f := skillFixture{reads: map[string][]string{}, compose: new(int32)}
	caps := []ReadCapability{}
	add := func(name string, fn func(string) (CapabilityResult, error)) {
		if fn == nil {
			return
		}
		caps = append(caps, ReadCapability{Name: name, Read: func(_ context.Context, q string) (CapabilityResult, error) {
			f.reads[name] = append(f.reads[name], q)
			return fn(q)
		}})
	}
	add("Jira", jira)
	add("Granola", granola)
	// Capabilities a skill must never reach.
	for _, forbidden := range []string{"Web", "JiraWrite", "Shell", "Memory"} {
		name := forbidden
		caps = append(caps, ReadCapability{Name: name, Read: func(context.Context, string) (CapabilityResult, error) {
			f.reads[name] = append(f.reads[name], "called")
			return CapabilityResult{ResultCount: 1, Text: "x"}, nil
		}})
	}
	f.runner = &ScheduledSkillRunner{
		Capabilities: func() []ReadCapability { return caps },
		Compose: func(context.Context, string, string) (string, error) {
			atomic.AddInt32(f.compose, 1)
			return composed, composeErr
		},
	}
	return f
}

func jiraResult(compact string, due map[string]string, keys ...string) func(string) (CapabilityResult, error) {
	return func(string) (CapabilityResult, error) {
		var ev []EvidenceRef
		for _, k := range keys {
			ev = append(ev, EvidenceRef{Provider: "Jira", ID: k, Title: "Tarea " + k})
		}
		return CapabilityResult{Text: "Jira " + strings.Join(keys, " "), ResultCount: len(keys), Evidence: ev, Compact: compact, Due: due}, nil
	}
}

func granolaResult(text string, n int) func(string) (CapabilityResult, error) {
	return func(string) (CapabilityResult, error) {
		return CapabilityResult{Text: text, ResultCount: n, Evidence: []EvidenceRef{{Provider: "Granola", ID: "m1", Title: "Daily"}}}, nil
	}
}

func skillTask(skill string) ScheduledTask {
	return ScheduledTask{ID: "t1", OperatorID: 1, Channel: "telegram", SkillID: skill, Schedule: TaskSchedule{Cadence: CadenceDaily, LocalTime: "08:30", Timezone: "Europe/Madrid"}}
}

func TestScheduledSkillRegistryHasExactlyTheBuiltIns(t *testing.T) {
	skills := ScheduledSkills()
	if len(skills) != 2 || skills[0].ID != "deadline-watch" || skills[1].ID != "morning-brief" {
		t.Fatalf("skills=%+v", skills)
	}
	for _, skill := range skills {
		for _, capability := range skill.RequiredCapabilities {
			if capability != "Jira" && capability != "Granola" {
				t.Fatalf("%s declares non read-only source %q", skill.ID, capability)
			}
		}
	}
}

func TestScheduledSkillUnknownIDIsTypedErrorAndReadsNothing(t *testing.T) {
	f := newSkillFixture(jiraResult("x", nil, "PMI-1"), granolaResult("t", 1), "ok", nil)
	for _, id := range []string{"", "shell", "morning-brief; rm -rf /", "MORNING-BRIEF"} {
		_, err := f.runner.Run(context.Background(), skillTask(id), time.Now())
		if !errors.Is(err, ErrUnknownScheduledSkill) {
			t.Fatalf("%q: err=%v", id, err)
		}
	}
	if len(f.reads) != 0 || atomic.LoadInt32(f.compose) != 0 {
		t.Fatalf("an unknown skill must not read or call the model: %v", f.reads)
	}
}

func TestScheduledSkillMorningBriefUsesOnlyDeclaredSourcesAndOneModelCall(t *testing.T) {
	f := newSkillFixture(jiraResult("Tienes 3 abiertas: 2 en curso, 1 por hacer. ¿Detallo alguna?", nil, "PMI-1", "PMI-2", "PMI-3"),
		granolaResult("Reunión daily: se decide priorizar PMI-1.", 1), "Se decidió priorizar PMI-1.", nil)
	result, err := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if err != nil || result.Suppress {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, forbidden := range []string{"Web", "JiraWrite", "Shell", "Memory"} {
		if len(f.reads[forbidden]) != 0 {
			t.Fatalf("%s must never be reachable from a scheduled skill", forbidden)
		}
	}
	if n := atomic.LoadInt32(f.compose); n != 1 {
		t.Fatalf("exactly one model call allowed, got %d", n)
	}
	if !strings.Contains(result.Text, "Tienes 3 abiertas") || !strings.Contains(result.Text, "priorizar PMI-1") || strings.Contains(result.Text, "¿Detallo") {
		t.Fatalf("text=%q", result.Text)
	}
	if len(result.Evidence) != 4 || result.Evidence[0].Provider != "Jira" {
		t.Fatalf("evidence=%+v", result.Evidence)
	}
}

func TestScheduledSkillEvidenceIsBoundedToFive(t *testing.T) {
	keys := []string{"PMI-1", "PMI-2", "PMI-3", "PMI-4", "PMI-5", "PMI-6", "PMI-7"}
	f := newSkillFixture(jiraResult("Tienes 7 abiertas.", nil, keys...), granolaResult("t", 1), "Resumen.", nil)
	result, _ := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if len(result.Evidence) != 5 {
		t.Fatalf("evidence=%d", len(result.Evidence))
	}
}

func TestScheduledSkillMorningBriefStatesMissingSourceWithoutInventing(t *testing.T) {
	f := newSkillFixture(jiraResult("Tienes 2 abiertas.", nil, "PMI-1", "PMI-2"), func(string) (CapabilityResult, error) {
		return CapabilityResult{}, errors.New("Granola no conectado")
	}, "no se usa", nil)
	result, _ := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if result.Suppress || !strings.Contains(result.Text, "No he podido consultar Granola") || !strings.Contains(result.Text, "Tienes 2 abiertas") {
		t.Fatalf("result=%+v", result)
	}
	if atomic.LoadInt32(f.compose) != 0 {
		t.Fatal("no model call without a source to summarize")
	}
	// Both sources down: say so, invent nothing.
	both := newSkillFixture(func(string) (CapabilityResult, error) { return CapabilityResult{}, errors.New("down") }, nil, "", nil)
	result, _ = both.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if result.Suppress || !strings.Contains(result.Text, "No he podido consultar Jira y Granola") {
		t.Fatalf("result=%+v", result)
	}
}

func TestScheduledSkillMorningBriefSuppressesWhenNothingToReport(t *testing.T) {
	empty := func(string) (CapabilityResult, error) { return CapabilityResult{}, nil }
	f := newSkillFixture(empty, empty, "no se usa", nil)
	result, err := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if err != nil || !result.Suppress || result.SuppressReason != "no_change" || result.Text != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestScheduledSkillRejectsModelTextWithUnsourcedIssueKey(t *testing.T) {
	f := newSkillFixture(jiraResult("Tienes 2 abiertas.", nil, "PMI-1", "PMI-2"), granolaResult("Daily sin claves.", 1), "Hay que cerrar PMI-999 hoy.", nil)
	result, _ := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if strings.Contains(result.Text, "PMI-999") || !strings.Contains(result.Text, "No he podido consultar el resumen de Granola") {
		t.Fatalf("an invented key must never reach the message: %q", result.Text)
	}
}

func TestScheduledSkillMorningBriefSingleIssueUsesEvidence(t *testing.T) {
	empty := func(string) (CapabilityResult, error) { return CapabilityResult{}, nil }
	f := newSkillFixture(jiraResult("", nil, "PMI-7"), empty, "", nil)
	result, _ := f.runner.Run(context.Background(), skillTask("morning-brief"), time.Now())
	if !strings.Contains(result.Text, "Tienes 1 tarea abierta: PMI-7 Tarea PMI-7.") {
		t.Fatalf("text=%q", result.Text)
	}
}

func TestScheduledSkillDeadlineWatchReportsOverdueAndSoon(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	scheduled := time.Date(2026, 11, 9, 8, 30, 0, 0, loc)
	due := map[string]string{"PMI-1": "2026-11-05", "PMI-2": "2026-11-09", "PMI-3": "2026-11-10", "PMI-4": "2026-11-11", "PMI-5": "2026-11-30"}
	f := newSkillFixture(jiraResult("ignorado", due, "PMI-1", "PMI-2", "PMI-3", "PMI-4", "PMI-5"), nil, "", nil)
	result, err := f.runner.Run(context.Background(), skillTask("deadline-watch"), scheduled)
	if err != nil || result.Suppress {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, want := range []string{"PMI-1 Tarea PMI-1 (venció el 5/11)", "PMI-2 Tarea PMI-2 (vence hoy)", "PMI-3 Tarea PMI-3 (vence mañana)", "PMI-4 Tarea PMI-4 (vence el 11/11)"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("missing %q in %q", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "PMI-5") {
		t.Fatalf("a far deadline is not an alert: %q", result.Text)
	}
	if atomic.LoadInt32(f.compose) != 0 {
		t.Fatal("deadline-watch is deterministic and needs no model")
	}
	if len(f.reads["Granola"]) != 0 {
		t.Fatal("deadline-watch reads Jira only")
	}
}

func TestScheduledSkillDeadlineWatchSuppressesWithoutDeadlinesAndStatesOutage(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Madrid")
	scheduled := time.Date(2026, 11, 9, 8, 30, 0, 0, loc)
	f := newSkillFixture(jiraResult("x", map[string]string{"PMI-5": "2026-12-30"}, "PMI-5"), nil, "", nil)
	result, _ := f.runner.Run(context.Background(), skillTask("deadline-watch"), scheduled)
	if !result.Suppress || result.SuppressReason != "no_deadlines" {
		t.Fatalf("result=%+v", result)
	}
	down := newSkillFixture(func(string) (CapabilityResult, error) { return CapabilityResult{}, errors.New("down") }, nil, "", nil)
	result, _ = down.runner.Run(context.Background(), skillTask("deadline-watch"), scheduled)
	if result.Suppress || !strings.Contains(result.Text, "No he podido consultar Jira") {
		t.Fatalf("an outage is stated, never read as 'no deadlines': %+v", result)
	}
}

func TestScheduledSkillRunnerWithoutCapabilitiesFailsClosed(t *testing.T) {
	if _, err := (&ScheduledSkillRunner{}).Run(context.Background(), skillTask("morning-brief"), time.Now()); err == nil {
		t.Fatal("an unconfigured runner must not run")
	}
}

func TestScheduledSkillServiceCompositionIsOneModelCallAndLeavesNoHistory(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, `{"choices":[{"message":{"content":"Se decidió priorizar lo urgente. Y una segunda frase. Y una tercera que sobra para el límite de frases pero no de palabras en absoluto nunca jamás."}}]}`)
	}))
	defer upstream.Close()
	state := t.TempDir()
	svc := New(upstream.URL, "key", "Eres Donna.", state, map[int64]bool{1: true})
	svc.Capabilities = []ReadCapability{
		{Name: "Jira", Read: func(context.Context, string) (CapabilityResult, error) { return CapabilityResult{}, nil }},
		{Name: "Granola", Read: func(context.Context, string) (CapabilityResult, error) {
			return CapabilityResult{Text: "Daily: priorizar lo urgente.", ResultCount: 1, Evidence: []EvidenceRef{{Provider: "Granola", ID: "m1"}}}, nil
		}},
	}
	result, err := NewScheduledSkillRunner(svc).Run(context.Background(), skillTask("morning-brief"), time.Now())
	if err != nil || result.Suppress || !strings.Contains(result.Text, "priorizar lo urgente") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("one model call expected, got %d", n)
	}
	matches, _ := filepath.Glob(filepath.Join(state, "telegram-*.json"))
	web, _ := filepath.Glob(filepath.Join(state, "web-*.json"))
	if len(matches)+len(web) != 0 {
		entries, _ := os.ReadDir(state)
		t.Fatalf("a scheduled run must not create chat history: %v", entries)
	}
}
