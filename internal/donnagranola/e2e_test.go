package donnagranola

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignedWebhookToGroundedNotificationEndToEnd(t *testing.T) {
	now := time.Now().UTC()
	const noteID = "not_1d3tmYTlCICgjy"
	granolaCalls := 0
	granola := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		granolaCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/notes/"+noteID || r.URL.RawQuery != "" {
			t.Errorf("unexpected Granola access, possible transcript or write request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"id":"` + noteID + `","title":"PMI seguimiento","owner":{"name":"Valentina"},"created_at":"2026-10-28T09:00:00Z","summary_text":"## Acciones\n- Revisar el presupuesto."}`))
	}))
	defer granola.Close()
	queue, err := OpenEventQueue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("test secret of sufficient length"))
	handler := NewWebhookHandler(queue, StaticSecret(secret), func() time.Time { return now })
	body := []byte(fmt.Sprintf(`{"event_id":"evt_e2e_1","event_type":"note.generated","note_id":%q,"occurred_at":"%s"}`, noteID, now.Format(time.RFC3339)))
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, signedRequest(t, body, "evt_e2e_1", now, "test secret of sufficient length"))
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("webhook status %d: %s", recorder.Code, recorder.Body.String())
		}
	}
	event, err := queue.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sink := &testSink{}
	worker := &Worker{Queue: queue, Notes: newGranolaClient(granola.URL, "granola-api-key", granola.Client()), Sink: sink, Operator: OperatorContext{OperatorID: 7, TelegramChatID: 7}}
	if err := worker.Process(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if granolaCalls != 1 || len(sink.notifications) != 1 {
		t.Fatalf("Granola calls=%d notifications=%d", granolaCalls, len(sink.notifications))
	}
	got := sink.notifications[0]
	if got.ID != "evt_e2e_1" || got.Owner != "Valentina" || len(got.Actions) != 1 || got.Actions[0] != "Revisar el presupuesto." || got.Message == "" {
		t.Fatalf("notification not grounded in note: %#v", got)
	}
}
