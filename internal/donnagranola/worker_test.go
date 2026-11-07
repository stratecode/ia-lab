package donnagranola

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type testReader struct {
	note  GranolaNote
	err   error
	calls int
}

func (r *testReader) GetNote(context.Context, string) (GranolaNote, error) {
	r.calls++
	return r.note, r.err
}

type testSink struct {
	notifications []Notification
	err           error
}

func (s *testSink) Deliver(_ context.Context, _ OperatorContext, n Notification) error {
	if s.err != nil {
		return s.err
	}
	s.notifications = append(s.notifications, n)
	return nil
}

type testWorkerQueue struct {
	complete string
	retried  string
	delay    time.Duration
	failed   string
}

func (*testWorkerQueue) Enqueue(context.Context, GranolaEvent) (bool, error) { return true, nil }
func (*testWorkerQueue) Next(context.Context) (GranolaEvent, error) {
	return GranolaEvent{}, errors.New("unused")
}
func (q *testWorkerQueue) Complete(id string) error { q.complete = id; return nil }
func (q *testWorkerQueue) Retry(id string, delay time.Duration) error {
	q.retried, q.delay = id, delay
	return nil
}
func (q *testWorkerQueue) Fail(id string, _ error) error { q.failed = id; return nil }

func TestFormatGranolaNotificationKeepsMissingOwnerAndDateExplicit(t *testing.T) {
	note := GranolaNote{Title: "PMI semanal", Summary: "## Resumen\nSe revisó el avance.\n\n## Acciones\n- Preparar el informe.\n- Confirmar presupuesto."}
	message := FormatGranolaNotification(note)
	for _, want := range []string{"Fecha: no indicada", "Responsable de la nota: no indicado", "Preparar el informe.", "sin crear tareas"} {
		if !strings.Contains(message, want) {
			t.Errorf("message missing %q: %s", want, message)
		}
	}
}

func TestFormatGranolaNotificationIncludesNoteReferenceAndExtractedSourceSpan(t *testing.T) {
	note := GranolaNote{
		ID:      "note-42",
		URL:     "https://notes.granola.ai/d/note-42",
		Title:   "PMI semanal",
		Summary: "## Próximos pasos\n- Preparar el informe de conciliación.",
	}

	message := FormatGranolaNotification(note)
	for _, want := range []string{"note-42", note.URL, "Preparar el informe de conciliación."} {
		if !strings.Contains(message, want) {
			t.Errorf("notification missing %q: %s", want, message)
		}
	}

	actions := ExtractGranolaActions(note)
	if len(actions) != 1 || actions[0].NoteID != note.ID || actions[0].NoteURL != note.URL || actions[0].SourceSpan != "Preparar el informe de conciliación." {
		t.Fatalf("actions=%+v, want one action with exact note metadata and source span", actions)
	}
}

func TestExtractGranolaActionsIgnoresBulletsOutsideActionHeadings(t *testing.T) {
	note := GranolaNote{ID: "note-43", URL: "https://notes.granola.ai/d/note-43", Summary: "## Decisiones\n- Publicar la nueva tarifa.\n\n## Próximos pasos\n- Revisar el borrador."}

	actions := ExtractGranolaActions(note)
	if len(actions) != 1 || actions[0].SourceSpan != "Revisar el borrador." {
		t.Fatalf("actions=%+v, want only action-section bullets", actions)
	}
}

func TestExtractActionLinesRecognizesSpanishFollowUpHeadings(t *testing.T) {
	for _, heading := range []string{"Siguientes pasos", "Próximos pasos", "Siguientes acciones"} {
		t.Run(heading, func(t *testing.T) {
			got := extractActionLines("## " + heading + "\n- Revisar el informe.")
			if len(got) != 1 || got[0] != "Revisar el informe." {
				t.Fatalf("actions = %v", got)
			}
		})
	}
}

func TestWorkerDeliversGroundedActionsAndCompletesAfterDelivery(t *testing.T) {
	queue := &testWorkerQueue{}
	reader := &testReader{note: GranolaNote{ID: "not_1d3tmYTlCICgjy", Title: "PMI semanal", Summary: "## Acciones\n- Valentina prepara el informe."}}
	sink := &testSink{}
	worker := &Worker{Queue: queue, Notes: reader, Sink: sink, Operator: OperatorContext{OperatorID: 42}}
	event := GranolaEvent{EventID: "evt_1", NoteID: reader.note.ID, EventType: EventNoteGenerated, Scope: ScopePersonal}
	if err := worker.Process(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if queue.complete != event.EventID || len(sink.notifications) != 1 {
		t.Fatalf("complete=%q notifications=%#v", queue.complete, sink.notifications)
	}
	if len(sink.notifications[0].Actions) != 1 || sink.notifications[0].Actions[0] != "Valentina prepara el informe." {
		t.Fatalf("notification = %#v", sink.notifications[0])
	}
}

func TestWorkerRetriesNoteNotYetReadableWithoutCompleting(t *testing.T) {
	queue := &testWorkerQueue{}
	worker := &Worker{Queue: queue, Notes: &testReader{err: ErrNoteNotReady}, Sink: &testSink{}}
	event := GranolaEvent{EventID: "evt_retry", NoteID: "not_1d3tmYTlCICgjy", Attempts: 0}
	if err := worker.Process(context.Background(), event); err == nil {
		t.Fatal("expected retry result")
	}
	if queue.retried != event.EventID || queue.complete != "" || queue.delay != time.Minute {
		t.Fatalf("queue state = %#v", queue)
	}
}

func TestWorkerRetriesTemporaryNotificationDelivery(t *testing.T) {
	queue := &testWorkerQueue{}
	note := GranolaNote{ID: "not_1d3tmYTlCICgjy", Title: "PMI semanal", Summary: "Resumen listo."}
	worker := &Worker{Queue: queue, Notes: &testReader{note: note}, Sink: &testSink{err: ErrTemporary}}
	event := GranolaEvent{EventID: "evt_delivery_retry", NoteID: note.ID}
	if err := worker.Process(context.Background(), event); err == nil {
		t.Fatal("expected retry result")
	}
	if queue.retried != event.EventID || queue.complete != "" || queue.failed != "" {
		t.Fatalf("queue state = %#v", queue)
	}
}

func TestWorkerReportsRetriesAndPermanentFailuresWithoutSourceDetails(t *testing.T) {
	queue := &testWorkerQueue{}
	note := GranolaNote{ID: "not_1d3tmYTlCICgjy", Title: "PMI", Summary: "Resumen listo."}
	var retryID, retryCategory string
	var retryAttempt int
	worker := &Worker{
		Queue: queue, Notes: &testReader{note: note}, Sink: &testSink{err: ErrTemporary},
		OnRetry: func(id, category string, attempt int) { retryID, retryCategory, retryAttempt = id, category, attempt },
	}
	event := GranolaEvent{EventID: "evt_retry_report", NoteID: note.ID}
	_ = worker.Process(context.Background(), event)
	if retryID != event.EventID || retryCategory != "temporary_dependency" || retryAttempt != 1 {
		t.Fatalf("retry report = %q %q %d", retryID, retryCategory, retryAttempt)
	}

	queue = &testWorkerQueue{}
	var failedID, failedCategory string
	worker = &Worker{
		Queue: queue, Notes: &testReader{err: ErrUnauthorized}, Sink: &testSink{},
		OnFail: func(id, category string) { failedID, failedCategory = id, category },
	}
	event.EventID = "evt_auth_failed"
	_ = worker.Process(context.Background(), event)
	if failedID != event.EventID || failedCategory != "granola_authorization" {
		t.Fatalf("failure report = %q %q", failedID, failedCategory)
	}
}

func TestWorkerBoundsRetryAttempts(t *testing.T) {
	queue := &testWorkerQueue{}
	worker := &Worker{Queue: queue, Notes: &testReader{err: ErrNoteNotReady}, Sink: &testSink{}}
	event := GranolaEvent{EventID: "evt_failed", NoteID: "not_1d3tmYTlCICgjy", Attempts: maxEventAttempts - 1}
	if err := worker.Process(context.Background(), event); err == nil {
		t.Fatal("expected exhausted retry result")
	}
	if queue.failed != event.EventID || queue.retried != "" {
		t.Fatalf("queue state = %#v", queue)
	}
}
