package secretary

import (
	"strings"
	"testing"
	"time"
)

func testIssue(key, summary, status, category, due string) jiraIssue {
	var issue jiraIssue
	issue.Key = key
	issue.Fields.Summary = summary
	issue.Fields.Status.Name = status
	issue.Fields.Status.Category.Key = category
	issue.Fields.Due = due
	return issue
}

func TestCompactJiraSummaryGroupsAndHighlights(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	issues := []jiraIssue{
		testIssue("PMI-161", "Crear un gestor genérico y configurable de corresponsales", "Tareas por hacer", "new", ""),
		testIssue("PMI-5", "Automatizar informes contables diarios de PMI", "Backlog", "new", ""),
		testIssue("PMI-160", "Unificar checkout de pagos PMI con Cloudflare Turnstile", "En curso", "indeterminate", ""),
		testIssue("COC-99", "Automatizaciones logística", "Blocked", "indeterminate", ""),
		testIssue("COC-110", "Propuesta loyalty Cocosi", "Blocked", "indeterminate", ""),
	}
	got := compactJiraSummary(issues, now, "Tienes", false)
	for _, want := range []string{"Tienes 5 abiertas", "1 en curso", "2 bloqueadas", "2 por hacer", "COC-99", "COC-110", "Ninguna tiene fecha límite", "¿Detallo alguna?"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %s", want, got)
		}
	}
	if countWords(got) > 70 {
		t.Errorf("summary has %d words, want a short answer: %s", countWords(got), got)
	}
	if strings.Contains(got, "Vencimiento") || strings.Contains(got, "Responsable") {
		t.Errorf("summary repeats raw fields: %s", got)
	}
}

func TestCompactJiraSummaryPrioritisesOverdueAndFlagsSampleLimit(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	issues := []jiraIssue{
		testIssue("PMI-1", "Tarea normal", "En curso", "indeterminate", ""),
		testIssue("PMI-2", "Tarea vencida", "Tareas por hacer", "new", "2026-11-01"),
		testIssue("PMI-3", "Vence mañana", "Tareas por hacer", "new", "2026-11-08"),
	}
	got := compactJiraSummary(issues, now, "Hay", true)
	if !strings.HasPrefix(got, "Hay al menos 3 abiertas") {
		t.Errorf("sample limit not flagged: %s", got)
	}
	if strings.Index(got, "PMI-2") > strings.Index(got, "PMI-3") || !strings.Contains(got, "1 vencida") {
		t.Errorf("overdue not first or not counted: %s", got)
	}
}

func TestCompactJiraSummaryIgnoresDoneAndEmpty(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	if got := compactJiraSummary(nil, now, "Tienes", false); got != "" {
		t.Errorf("empty input = %q", got)
	}
	done := []jiraIssue{testIssue("PMI-9", "Hecha", "Done", "done", "")}
	if got := compactJiraSummary(done, now, "Tienes", false); got != "No hay incidencias abiertas." {
		t.Errorf("done only = %q", got)
	}
}

func TestConversationalAckOnlyForPureCourtesies(t *testing.T) {
	for _, text := range []string{"gracias Donna", "Gracias", "vale", "ok", "perfecto, gracias", "hasta luego"} {
		reply, ok := conversationalAck(text)
		if !ok || reply == "" || strings.Contains(reply, "necesitas a continuación") || strings.Count(reply, " ") > 5 {
			t.Errorf("%q => %q, %v", text, reply, ok)
		}
	}
	for _, text := range []string{"gracias, ahora dime las tareas", "ok y qué tengo hoy", "hola", "hola qué tal van Valentina y Gonzalo", "dime cómo van", "/jira", ""} {
		if reply, ok := conversationalAck(text); ok {
			t.Errorf("%q must not be treated as a courtesy, got %q", text, reply)
		}
	}
}

func TestPolishAnswerDropsFillerButKeepsContent(t *testing.T) {
	in := "Valentina tiene 3 tareas abiertas y una vence mañana. ¿Qué información o acción necesitas a continuación?"
	if got := polishAnswer(in); got != "Valentina tiene 3 tareas abiertas y una vence mañana." {
		t.Errorf("got %q", got)
	}
	multi := "El plazo es el viernes.\nSi necesitas aclarar algún punto, avísame."
	if got := polishAnswer(multi); got != "El plazo es el viernes." {
		t.Errorf("got %q", got)
	}
	onlyFiller := "Aclara lo que necesitas para avanzar."
	if got := polishAnswer(onlyFiller); got != onlyFiller {
		t.Errorf("an answer made only of filler must be kept, got %q", got)
	}
	clean := "El informe está listo. ¿Lo envío al cliente?"
	if got := polishAnswer(clean); got != clean {
		t.Errorf("clean answer changed: %q", got)
	}
}

func TestWantsFullListing(t *testing.T) {
	if !wantsFullListing("dame la lista completa de tareas") || wantsFullListing("qué tengo pendiente") {
		t.Error("full listing detection is wrong")
	}
	// "todas" is ordinary Spanish, not a request for the long format.
	if wantsFullListing("revisa todas las tareas en curso de Valentina") {
		t.Error("\"todas las tareas\" must not force the full listing")
	}
}

func TestResponseWordsMetricUsesBoundedIntent(t *testing.T) {
	m := NewVoiceMetrics()
	m.ObserveResponseWords("general", 12)
	m.ObserveResponseWords("anything-user-supplied", 30)
	families, err := m.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, family := range families {
		if family.GetName() == "lab_secretary_response_words" {
			for _, metric := range family.Metric {
				labels[metric.Label[0].GetValue()] = true
			}
		}
	}
	if !labels["general"] || !labels["other"] || len(labels) != 2 {
		t.Errorf("labels = %v", labels)
	}
}

func TestHowArePeopleDoingRoutesToJiraPeopleOnly(t *testing.T) {
	got := jiraPeople("dime como van Valentina y Gonzalo")
	if len(got) != 2 || got[0] != "Valentina" || got[1] != "Gonzalo" {
		t.Fatalf("people = %v", got)
	}
	if !jiraTaskIntent("dime como van Valentina y Gonzalo") {
		t.Error("a status question about named people must reach Jira")
	}
	for _, text := range []string{"cómo va el proyecto", "como van las ventas", "cómo va todo"} {
		if names := jiraPeople(text); len(names) != 0 {
			t.Errorf("%q must not be read as people: %v", text, names)
		}
	}
}

func TestOffersExternalWriteNeedsTheVerbNextToTheOffer(t *testing.T) {
	status := "Valentina: 3 abiertas. [PMI-161] Crear un gestor genérico de corresponsales. Las abiertas no tienen fecha de entrega en Jira; no puedo decir si van en plazo."
	if offersExternalWrite(status) {
		t.Errorf("a source-backed status must not be read as an offer: %s", status)
	}
	for _, offer := range []string{"Puedo crear la tarea en Jira si quieres.", "¿Quieres que actualice la incidencia? Me encargo de enviar el mensaje.", "Podría asignar la tarea de Jira a Gonzalo."} {
		if !offersExternalWrite(offer) {
			t.Errorf("a real offer was not detected: %s", offer)
		}
	}
}

func TestCompactJiraPersonIsOneShortLine(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	open := jiraSearchResponse{Issues: []jiraIssue{
		testIssue("PMI-77", "Onboarding con OTP", "En curso", "indeterminate", ""),
		testIssue("PMI-67", "Autenticación, sesión y permisos", "Tareas por hacer", "new", "2026-11-03"),
	}}
	done := jiraSearchResponse{Issues: []jiraIssue{testIssue("PMI-60", "Hecha", "Done", "done", "")}}
	got := compactJiraPerson("Valentina", open, done, true, now)
	for _, want := range []string{"Valentina tiene 2 abiertas", "PMI-67", "1 vencida", "1 cerrada en 14 días"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "¿Detallo") || countWords(got) > 45 {
		t.Errorf("person line must be short and without its own offer: %s", got)
	}
}

func TestPresentTenseWriteClaimsAreBlockedAndNegationsAreNot(t *testing.T) {
	for _, claim := range []string{"Creo la tarea: revisar el informe del cliente.", "Perfecto. Envío el correo a Gonzalo ahora.", "Actualizo la incidencia y la cierro."} {
		if !claimsUnverifiedWrite(claim) {
			t.Errorf("claim not detected: %q", claim)
		}
	}
	for _, fine := range []string{"No puedo crear la tarea; solo leo Jira.", "La tarea PMI-77 sigue en curso.", "Te dejo el texto para crear la tarea tú mismo.", "No creo la tarea porque no tengo acceso de escritura."} {
		if claimsUnverifiedWrite(fine) {
			t.Errorf("false positive: %q", fine)
		}
	}
}

func TestMoreFillerQuestionsAreStripped(t *testing.T) {
	in := "Gracias. Todo anotado. ¿Necesitas que te ayude con algún detalle específico?"
	if got := polishAnswer(in); strings.Contains(got, "detalle específico") || !strings.Contains(got, "Todo anotado") {
		t.Errorf("got %q", got)
	}
}

func TestSpokenPeopleQuestionsReachJira(t *testing.T) {
	cases := map[string][]string{
		"Me gustaría que revisaras todas las tareas que tengo en curso y que me digas qué tareas tienen en curso, tanto Valentina como Gonzalo.": {"Valentina", "Gonzalo"},
		"Hola, Donna. Esto es una prueba. Dime qué tareas tienen en cursora, y, como tanto, Valentina como Gonzalo.":                             {"Valentina", "Gonzalo"},
		"Qué tareas tienen en curso Valentina y Gonzalo": {"Valentina", "Gonzalo"},
		"¿En qué están trabajando Valentina y Gonzalo?":  {"Valentina", "Gonzalo"},
		"Qué está haciendo Gonzalo con sus tareas":       {"Gonzalo"},
		"dime las tareas de Valentina y Gonzalo":         {"Valentina", "Gonzalo"},
	}
	for query, want := range cases {
		got := jiraPeople(query)
		if len(got) != len(want) {
			t.Errorf("jiraPeople(%q) = %v, want %v", query, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("jiraPeople(%q) = %v, want %v", query, got, want)
			}
		}
	}
	for _, query := range []string{
		"Tanto Pedro como Ana vinieron a la reunión de ayer",
		"Estuvimos hablando con Valentina y Gonzalo sobre el viaje",
		"Hoy hace sol, tanto en Madrid como en Sevilla",
		"qué tareas tienen en curso los equipos",
	} {
		if got := jiraPeople(query); len(got) != 0 {
			t.Errorf("%q must not be read as people: %v", query, got)
		}
	}
}
