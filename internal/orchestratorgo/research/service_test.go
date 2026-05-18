package research

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
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

func TestQueryDetectsDocumentAndUsesSidecar(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/document/read" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","summary":"README.md\n\nDeployment notes for the orchestrator.","output":{"content_text":"Deployment notes for the orchestrator.","sections":["Intro"]},"source_refs":[{"title":"README.md","uri":"file:///tmp/README.md","kind":"document"}],"artifacts":[{"artifact_type":"document_text","title":"README.md","uri":"file:///tmp/README.md","media_type":"text/markdown","content_text":"Deployment notes for the orchestrator.","metadata":{"sections":["Intro"]}}]}`))
	}))
	defer sidecar.Close()

	service := New(Options{
		Capabilities: capabilities.New(capabilities.Options{
			DocsBaseURL:       sidecar.URL,
			MaxDocumentBytes:  1000000,
			MaxArtifactChars:  16000,
			AllowedURLSchemes: []string{"file"},
			AllowedLocalRoots: []string{"/tmp"},
			TimeoutSeconds:    5,
		}),
	})
	result, err := service.Query(context.Background(), "resume ./README.md")
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent != IntentDocumentQA {
		t.Fatalf("unexpected intent: %s", result.Intent)
	}
	if len(result.Sources) != 1 || result.Sources[0].Kind != "document" {
		t.Fatalf("unexpected sources: %#v", result.Sources)
	}
	if !strings.Contains(result.Answer, "Deployment notes") {
		t.Fatalf("unexpected answer: %s", result.Answer)
	}
}

func TestQueryDetectsImageAndUsesSidecar(t *testing.T) {
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/image/analyze" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","summary":"Image: PNG\nSize: 640x480\nMode: RGB\nOCR: error stack trace","output":{"ocr_text":"error stack trace","metadata":{"width":640,"height":480,"mode":"RGB","format":"PNG"}},"source_refs":[{"title":"screen.png","uri":"file:///tmp/screen.png","kind":"image"}],"artifacts":[{"artifact_type":"image_analysis","title":"screen.png","uri":"file:///tmp/screen.png","media_type":"image/png","content_text":"error stack trace","metadata":{"width":640,"height":480}}]}`))
	}))
	defer sidecar.Close()

	service := New(Options{
		Capabilities: capabilities.New(capabilities.Options{
			ImagesBaseURL:     sidecar.URL,
			MaxImageBytes:     1000000,
			MaxArtifactChars:  16000,
			AllowedURLSchemes: []string{"file"},
			AllowedLocalRoots: []string{"/tmp"},
			TimeoutSeconds:    5,
		}),
	})
	result, err := service.Query(context.Background(), "analiza ./screen.png")
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent != IntentImageQA {
		t.Fatalf("unexpected intent: %s", result.Intent)
	}
	if len(result.Sources) != 1 || result.Sources[0].Kind != "image" {
		t.Fatalf("unexpected sources: %#v", result.Sources)
	}
	if !strings.Contains(result.Answer, "OCR: error stack trace") {
		t.Fatalf("unexpected answer: %s", result.Answer)
	}
}

func TestShouldUseResearchBalancedHeuristic(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{query: "Explica qué es PostgreSQL logical replication", want: false},
		{query: "¿Qué diferencia hay entre replicación lógica y física en PostgreSQL?", want: true},
		{query: "¿Cuáles son las últimas novedades de PostgreSQL logical replication?", want: true},
		{query: "¿Qué es MCP y para qué sirve en sistemas agénticos?", want: true},
		{query: "Resume https://www.postgresql.org/docs/current/logical-replication.html", want: true},
		{query: "Analiza ./screen.png", want: true},
		{query: "Dame fuentes sobre MCP", want: true},
	}
	for _, tc := range cases {
		got := ShouldUseResearch(tc.query)
		if got != tc.want {
			t.Fatalf("ShouldUseResearch(%q)=%v want %v", tc.query, got, tc.want)
		}
	}
}

func TestSearchAndSynthesizeUsesUtilityWhenConfigured(t *testing.T) {
	var searchURL string
	searchMux := http.NewServeMux()
	searchMux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body>
			<a class="result__a" href="%s/page1">Feature one</a>
			<div class="result__snippet">Feature one snippet.</div>
			<a class="result__a" href="%s/page2">Feature two</a>
			<div class="result__snippet">Feature two snippet.</div>
		</body></html>`, searchURL, searchURL)
	})
	searchMux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Page1</title></head><body><p>Feature one improves diagnostics and visibility into replication lag.</p></body></html>`)
	})
	searchMux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Page2</title></head><body><p>Feature two extends generated column replication support.</p></body></html>`)
	})
	searchServer := httptest.NewServer(searchMux)
	defer searchServer.Close()
	searchURL = searchServer.URL

	utilityServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Síntesis afinada por utility."}}]}`))
	}))
	defer utilityServer.Close()

	service := New(Options{
		Client:                searchServer.Client(),
		SearchBaseURL:         searchServer.URL + "/search?q=",
		MaxFetchCount:         5,
		UtilityBaseURL:        utilityServer.URL,
		UtilityAPIKey:         "test",
		UtilityTimeoutSeconds: 5,
	})
	result, err := service.Query(context.Background(), "últimas mejoras en logical replication")
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Síntesis afinada por utility." {
		t.Fatalf("unexpected synthesized answer: %s", result.Answer)
	}
}
