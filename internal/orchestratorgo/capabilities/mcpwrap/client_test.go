package mcpwrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientExecuteNormalizesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/github/read" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","summary":"ok","output":{"repository":"example/repo"},"source_refs":[{"title":"repo","uri":"https://github.com/example/repo","kind":"github"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 5)
	result, err := client.Execute(context.Background(), "github.read", map[string]any{"repository": "example/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if got := result.Output["repository"]; got != "example/repo" {
		t.Fatalf("unexpected output: %#v", result.Output)
	}
}

func TestClientExecuteSurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 5)
	_, err := client.Execute(context.Background(), "github.read", map[string]any{"repository": "example/repo"})
	if err == nil || !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}
