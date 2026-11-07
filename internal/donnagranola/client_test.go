package donnagranola

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingReadCloser) Close() error             { return nil }

func TestGetNoteReturnsOnlyGeneratedSummaryFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/notes/not_1d3tmYTlCICgjy" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer api-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"not_1d3tmYTlCICgjy","url":"https://notes.granola.ai/d/not_1d3tmYTlCICgjy","title":"PMI weekly","owner":{"name":"Valentina","email":"valentina@example.com"},"created_at":"2026-10-28T09:00:00Z","calendar_event":{"scheduled_start_time":"2026-10-28T08:30:00Z"},"summary_text":"Decidimos entregar el informe.","summary_markdown":"## Acciones\n- Valentina prepara el informe para el viernes.","transcript":[{"text":"must never be returned"}],"private_notes_text":"must not be returned"}`))
	}))
	defer server.Close()
	client := newGranolaClient(server.URL, "api-key", server.Client())
	note, err := client.GetNote(context.Background(), "not_1d3tmYTlCICgjy")
	if err != nil {
		t.Fatal(err)
	}
	if note.Title != "PMI weekly" || note.Summary != "## Acciones\n- Valentina prepara el informe para el viernes." || note.Date != "2026-10-28T08:30:00Z" || note.Owner != "Valentina" {
		t.Fatalf("note = %#v", note)
	}
	if note.URL != "https://notes.granola.ai/d/not_1d3tmYTlCICgjy" {
		t.Fatalf("note URL = %q", note.URL)
	}
	if strings.Contains(note.Summary, "must never") || strings.Contains(note.Summary, "must not") {
		t.Fatalf("non-summary content returned: %#v", note)
	}
}

func TestGetNoteAuthorizationFailureIsPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer server.Close()
	_, err := newGranolaClient(server.URL, "api-key", server.Client()).GetNote(context.Background(), "not_1d3tmYTlCICgjy")
	if err == nil || errors.Is(err, ErrNoteNotReady) {
		t.Fatalf("authorization error = %v", err)
	}
}

func TestGetNoteNotYetReadableCanRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer server.Close()
	_, err := newGranolaClient(server.URL, "api-key", server.Client()).GetNote(context.Background(), "not_1d3tmYTlCICgjy")
	if !errors.Is(err, ErrNoteNotReady) {
		t.Fatalf("not-ready error = %v", err)
	}
}

func TestGetNoteBodyReadFailureIsRetryable(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: failingReadCloser{}, Request: request}, nil
	})}
	_, err := newGranolaClient("https://example.test", "api-key", httpClient).GetNote(context.Background(), "not_1d3tmYTlCICgjy")
	if !errors.Is(err, ErrTemporary) {
		t.Fatalf("expected retryable body read error, got %v", err)
	}
}
