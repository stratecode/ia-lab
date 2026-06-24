package browserverify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientVerifyNormalizesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/browser/verify" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","summary":"browser ok","output":{"assertions":["title"]},"artifacts":[{"artifact_type":"browser_screenshot","title":"screenshot","media_type":"image/png"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token", 5)
	result, err := client.Verify(context.Background(), Request{URL: "https://example.com", AssertText: "Welcome"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestClientVerifySurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 5)
	_, err := client.Verify(context.Background(), Request{URL: "https://example.com"})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("expected HTTP error, got %v", err)
	}
}
