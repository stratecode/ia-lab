package secretary

import (
	"strings"
	"testing"
	"time"
)

func withCharacter(t *testing.T, on bool) {
	t.Helper()
	previous := characterOn.Load()
	SetCharacter(on)
	t.Cleanup(func() { SetCharacter(previous) })
}

func TestRecommendationFollowsTheEvidenceAndStaysSilentWithoutIt(t *testing.T) {
	withCharacter(t, true)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	overdue := []jiraIssue{
		testIssue("A-1", "Reciente", "En curso", "indeterminate", "2026-11-05"),
		testIssue("A-2", "La más antigua", "Tareas por hacer", "new", "2026-10-20"),
		testIssue("A-3", "Bloqueada", "Blocked", "indeterminate", ""),
	}
	if got := recommendation(overdue, now); got != "Yo empezaría por A-2: lleva vencida desde el 20 de octubre." {
		t.Errorf("overdue: %q", got)
	}
	blocked := []jiraIssue{testIssue("B-1", "Normal", "En curso", "indeterminate", ""), testIssue("B-2", "Parada", "Blocked", "indeterminate", "")}
	if got := recommendation(blocked, now); !strings.Contains(got, "B-2") || !strings.Contains(got, "desbloquearía") {
		t.Errorf("blocked: %q", got)
	}
	soon := []jiraIssue{testIssue("C-1", "Mañana", "Tareas por hacer", "new", "2026-11-08")}
	if got := recommendation(soon, now); got != "Lo próximo en caer es C-1, el 8 de noviembre." {
		t.Errorf("soon: %q", got)
	}
	calm := []jiraIssue{testIssue("D-1", "Tranquila", "En curso", "indeterminate", ""), testIssue("D-2", "Otra", "Tareas por hacer", "new", "2026-12-30")}
	if got := recommendation(calm, now); got != "" {
		t.Errorf("no evidence must mean no recommendation, got %q", got)
	}
	done := []jiraIssue{testIssue("E-1", "Cerrada", "Done", "done", "2026-10-01")}
	if got := recommendation(done, now); got != "" {
		t.Errorf("a closed issue is never recommended: %q", got)
	}
}

func TestNeutralModeRemovesCharacterButKeepsFacts(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	issues := []jiraIssue{testIssue("A-2", "Vencida", "Tareas por hacer", "new", "2026-10-20"), testIssue("A-3", "Otra", "En curso", "indeterminate", "")}
	withCharacter(t, true)
	on := compactJiraSummary(issues, now, "Tienes", false)
	withCharacter(t, false)
	off := compactJiraSummary(issues, now, "Tienes", false)
	if !strings.Contains(on, "Yo empezaría por A-2") || strings.Contains(off, "Yo empezaría") {
		t.Errorf("recommendation switch is wrong:\n on=%s\noff=%s", on, off)
	}
	for _, want := range []string{"Tienes 2 abiertas", "1 vencida", "¿Detallo alguna?"} {
		if !strings.Contains(on, want) || !strings.Contains(off, want) {
			t.Errorf("facts must not depend on the character, missing %q", want)
		}
	}
	if got := clarifyQuestion("x", now); got != "¿Qué fuente o información quieres que consulte?" {
		t.Errorf("neutral clarify = %q", got)
	}
}

func TestAcknowledgementsKeepWitOnlyForLowStakes(t *testing.T) {
	withCharacter(t, true)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	for _, kind := range []ackKind{ackThanks, ackConfirm, ackFarewell} {
		for _, seed := range []string{"a", "b", "c", "d", "e", "f"} {
			reply := ackReply(kind, seed, now)
			if reply == "" || countWords(reply) > 7 || strings.Contains(reply, "necesitas a continuación") {
				t.Errorf("kind %d seed %s => %q", kind, seed, reply)
			}
		}
	}
	// The same text gives the same line within a day, and wording varies over days.
	if ackReply(ackThanks, "gracias", now) != ackReply(ackThanks, "gracias", now) {
		t.Error("an acknowledgement must be stable within a day")
	}
	seen := map[string]bool{}
	for day := 0; day < 30; day++ {
		seen[ackReply(ackThanks, "gracias", now.AddDate(0, 0, day))] = true
	}
	if len(seen) < 2 {
		t.Errorf("wording never varies over a month: %v", seen)
	}
}

func TestSeriousAnswersCarryNoJoke(t *testing.T) {
	withCharacter(t, true)
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	issues := []jiraIssue{testIssue("A-2", "Vencida", "Blocked", "indeterminate", "2026-10-20")}
	got := compactJiraSummary(issues, now, "Tienes", false)
	for _, quip := range []string{"favores", "incendies", "Sabía que"} {
		if strings.Contains(got, quip) {
			t.Errorf("a summary with overdue work must stay serious: %s", got)
		}
	}
}

func TestParseCharacterMode(t *testing.T) {
	for value, want := range map[string]bool{"": true, "donna": true, "neutral": false, " NEUTRAL ": false} {
		if got := ParseCharacterMode(value); got != want {
			t.Errorf("%q => %v, want %v", value, got, want)
		}
	}
}

func TestStatusQuestionsWithoutASourceGetAnHonestAnswer(t *testing.T) {
	withCharacter(t, true)
	for _, text := range []string{"¿cómo va el informe del cliente?", "dime cómo va la migración", "¿Cómo van los pagos?", "en qué estado está el despliegue"} {
		if !statusQuestionWithoutSource(text) {
			t.Errorf("%q should be treated as a status question without a source", text)
		}
	}
	for _, text := range []string{"cómo va PMI-77", "cómo van Valentina y Gonzalo", "cómo va la reunión de ayer", "qué tengo pendiente", "cómo va el informe de las tareas de Jira", "hola"} {
		if statusQuestionWithoutSource(text) {
			t.Errorf("%q has a source or is not a status question", text)
		}
	}
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	got := noDataAnswer("x", now)
	if !strings.Contains(got, "Jira") || strings.Contains(got, "más detalles") {
		t.Errorf("answer = %q", got)
	}
	withCharacter(t, false)
	if got := noDataAnswer("x", now); got != "No tengo información sobre eso. ¿Consulto Jira o Granola?" {
		t.Errorf("neutral answer = %q", got)
	}
}
