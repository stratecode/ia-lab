package research

import "testing"

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
