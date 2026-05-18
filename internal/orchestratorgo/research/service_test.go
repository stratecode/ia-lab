package research

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseDuckDuckGoResults(t *testing.T) {
	htmlBody := `
<html><body>
  <a class="result__a" href="https://example.com/a">Result A</a>
  <div class="result__snippet">Snippet A</div>
  <a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fb">Result B</a>
  <div class="result__snippet">Snippet B</div>
</body></html>`

	results := parseDuckDuckGoResults(htmlBody)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Result A" || results[0].URL != "https://example.com/a" || results[0].Snippet != "Snippet A" {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
	if results[1].URL != "https://example.com/b" {
		t.Fatalf("unexpected normalized URL: %s", results[1].URL)
	}
}

func TestExtractHTMLContent(t *testing.T) {
	title, text := extractHTMLContent(`<html><head><title>Hello</title><style>body{}</style></head><body><h1>Hello world</h1><script>alert(1)</script><p>Example text.</p></body></html>`)
	if title != "Hello" {
		t.Fatalf("unexpected title: %s", title)
	}
	if text == "" || text == "Hello" {
		t.Fatalf("unexpected text extraction: %q", text)
	}
}

func TestChooseSearchFetchCountExpandsForComparativeQuery(t *testing.T) {
	results := []SearchResult{
		{Title: "A", URL: "https://example.com/a", Snippet: "short"},
		{Title: "B", URL: "https://another.com/b", Snippet: "short"},
		{Title: "C", URL: "https://third.com/c", Snippet: "short"},
		{Title: "D", URL: "https://fourth.com/d", Snippet: "short"},
		{Title: "E", URL: "https://fifth.com/e", Snippet: "short"},
	}
	got := chooseSearchFetchCount("compare postgres logical replication vs physical replication", results, 5)
	if got != 5 {
		t.Fatalf("expected 5 fetches, got %d", got)
	}
}

func TestSearchAndSynthesizeUsesMultipleFetchedSources(t *testing.T) {
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		fmt.Fprintf(w, `<html><body>
			<a class="result__a" href="%s/page1">%s guide</a>
			<div class="result__snippet">Recent operational guidance about logical replication.</div>
			<a class="result__a" href="%s/page2">%s monitoring</a>
			<div class="result__snippet">Monitoring improvements and tradeoffs.</div>
			<a class="result__a" href="%s/page3">%s tuning</a>
			<div class="result__snippet">Tuning and caveats for production environments.</div>
		</body></html>`, serverURL, q, serverURL, q, serverURL, q)
	})
	mux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Guide</title></head><body><p>Logical replication gives finer control over replicated tables and publications. It is useful when selective replication matters.</p></body></html>`)
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Monitoring</title></head><body><p>Monitoring has improved through better visibility into replication slots, lag, and apply state. Operators get more practical diagnostics.</p></body></html>`)
	})
	mux.HandleFunc("/page3", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Tuning</title></head><body><p>Production tuning still requires careful network sizing, WAL retention planning, and failure handling. It is not magic, sadly.</p></body></html>`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	serverURL = server.URL

	service := New(Options{
		Client:        server.Client(),
		SearchBaseURL: server.URL + "/search?q=",
		MaxFetchCount: 5,
	})
	result, err := service.Query(context.Background(), "compare logical replication monitoring and tuning")
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent != IntentSearchAnswer {
		t.Fatalf("unexpected intent: %s", result.Intent)
	}
	if len(result.Sources) != 3 {
		t.Fatalf("expected 3 fetched sources, got %d", len(result.Sources))
	}
	if !strings.Contains(result.Answer, "Síntesis:") {
		t.Fatalf("answer missing synthesis block: %s", result.Answer)
	}
	if !strings.Contains(result.Answer, "Evidencia principal:") {
		t.Fatalf("answer missing evidence block: %s", result.Answer)
	}
}

func TestSearchEndpointSupportsPlaceholder(t *testing.T) {
	service := New(Options{SearchBaseURL: "https://search.local/?q={query}"})
	endpoint := service.searchEndpoint("logical replication")
	if !strings.Contains(endpoint, url.QueryEscape("logical replication")) {
		t.Fatalf("unexpected endpoint: %s", endpoint)
	}
}
