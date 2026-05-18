package research

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Intent string

const (
	IntentSearchAnswer Intent = "search_answer"
	IntentURLSummary   Intent = "url_summary"
)

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type Source struct {
	Title string `json:"title"`
	URI   string `json:"uri"`
	Kind  string `json:"kind"`
}

type Result struct {
	Intent       Intent         `json:"intent"`
	Answer       string         `json:"answer"`
	Confidence   float64        `json:"confidence"`
	Sources      []Source       `json:"sources"`
	SearchResults []SearchResult `json:"search_results,omitempty"`
}

type Service struct {
	client *http.Client
}

func New() *Service {
	return &Service{
		client: &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *Service) Query(ctx context.Context, query string) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}
	if rawURL := extractFirstURL(query); rawURL != "" {
		return s.summarizeURL(ctx, rawURL)
	}
	return s.searchAndSynthesize(ctx, query)
}

func (s *Service) WebSearch(ctx context.Context, query string) ([]SearchResult, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "StrateCode-Orchestrator-Go/0.1 (+https://example.com)")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search request failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoResults(string(body)), nil
}

func (s *Service) WebFetch(ctx context.Context, rawURL string) (Source, string, error) {
	rawURL = strings.TrimSpace(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Source{}, "", err
	}
	req.Header.Set("User-Agent", "StrateCode-Orchestrator-Go/0.1 (+https://example.com)")
	resp, err := s.client.Do(req)
	if err != nil {
		return Source{}, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Source{}, "", fmt.Errorf("fetch request failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	if err != nil {
		return Source{}, "", err
	}
	title, text := extractHTMLContent(string(body))
	if title == "" {
		title = rawURL
	}
	return Source{Title: title, URI: rawURL, Kind: "web"}, text, nil
}

func (s *Service) summarizeURL(ctx context.Context, rawURL string) (Result, error) {
	source, text, err := s.WebFetch(ctx, rawURL)
	if err != nil {
		return Result{}, err
	}
	answer := synthesizeSingleSource(source, text)
	return Result{
		Intent:     IntentURLSummary,
		Answer:     answer,
		Confidence: 0.76,
		Sources:    []Source{source},
	}, nil
}

func (s *Service) searchAndSynthesize(ctx context.Context, query string) (Result, error) {
	results, err := s.WebSearch(ctx, query)
	if err != nil {
		return Result{}, err
	}
	if len(results) == 0 {
		return Result{}, fmt.Errorf("no web results found")
	}
	sources := make([]Source, 0, min(3, len(results)))
	summaries := make([]string, 0, min(3, len(results)))
	for _, item := range results[:min(3, len(results))] {
		source, text, err := s.WebFetch(ctx, item.URL)
		if err != nil {
			continue
		}
		sources = append(sources, source)
		summaries = append(summaries, fmt.Sprintf("%s: %s", source.Title, truncateSentence(text, 320)))
	}
	if len(sources) == 0 {
		for _, item := range results[:min(3, len(results))] {
			sources = append(sources, Source{Title: item.Title, URI: item.URL, Kind: "search"})
			if item.Snippet != "" {
				summaries = append(summaries, fmt.Sprintf("%s: %s", item.Title, item.Snippet))
			}
		}
	}
	answer := "Resumen de la búsqueda:\n\n"
	for i, line := range summaries {
		answer += fmt.Sprintf("%d. %s\n", i+1, line)
	}
	answer += "\nConclusión: estas son las fuentes más relevantes encontradas para responder la consulta. Úsalas como base y no como evangelio grabado en piedra."
	return Result{
		Intent:        IntentSearchAnswer,
		Answer:        strings.TrimSpace(answer),
		Confidence:    0.64,
		Sources:       sources,
		SearchResults: results[:min(5, len(results))],
	}, nil
}

func looksLikeURL(input string) bool {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func extractFirstURL(input string) string {
	fields := strings.Fields(input)
	for _, field := range fields {
		candidate := strings.Trim(field, " \t\r\n\"'()[]{}<>,.")
		if looksLikeURL(candidate) {
			return candidate
		}
	}
	return ""
}

var (
	reAnchor = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reSnippetBlock = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>|<div[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</div>`)
	reTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	reTags = regexp.MustCompile(`(?is)<[^>]+>`)
	reSpaces = regexp.MustCompile(`\s+`)
)

func parseDuckDuckGoResults(body string) []SearchResult {
	matches := reAnchor.FindAllStringSubmatch(body, 8)
	snippets := reSnippetBlock.FindAllStringSubmatch(body, 8)
	results := make([]SearchResult, 0, len(matches))
	for idx, match := range matches {
		link := html.UnescapeString(strings.TrimSpace(match[1]))
		title := cleanText(match[2])
		snippet := ""
		if idx < len(snippets) {
			snippet = cleanText(firstNonEmpty(snippets[idx][1], snippets[idx][2]))
		}
		if title == "" || link == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   title,
			URL:     normalizeDuckDuckGoLink(link),
			Snippet: snippet,
		})
	}
	return results
}

func normalizeDuckDuckGoLink(link string) string {
	if strings.HasPrefix(link, "//") {
		return "https:" + link
	}
	if strings.HasPrefix(link, "/l/?uddg=") {
		raw := strings.TrimPrefix(link, "/l/?uddg=")
		if parsed, err := url.QueryUnescape(raw); err == nil {
			return parsed
		}
	}
	if parsed, err := url.Parse(link); err == nil {
		if strings.Contains(parsed.Host, "duckduckgo.com") && parsed.Path == "/l/" {
			if uddg := parsed.Query().Get("uddg"); uddg != "" {
				if decoded, err := url.QueryUnescape(uddg); err == nil {
					return decoded
				}
				return uddg
			}
		}
	}
	return link
}

func extractHTMLContent(raw string) (string, string) {
	title := cleanText(firstMatch(reTitle, raw))
	text := reScript.ReplaceAllString(raw, " ")
	text = html.UnescapeString(text)
	text = reTags.ReplaceAllString(text, " ")
	text = reSpaces.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	return title, text
}

func synthesizeSingleSource(source Source, text string) string {
	summary := truncateSentence(text, 700)
	return strings.TrimSpace(fmt.Sprintf("Resumen de %s:\n\n%s", source.Title, summary))
}

func truncateSentence(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	cut := text[:max]
	if idx := strings.LastIndexAny(cut, ".!?"); idx > 100 {
		return strings.TrimSpace(cut[:idx+1])
	}
	if idx := strings.LastIndex(cut, " "); idx > 100 {
		return strings.TrimSpace(cut[:idx]) + "..."
	}
	return strings.TrimSpace(cut) + "..."
}

func cleanText(input string) string {
	value := html.UnescapeString(reTags.ReplaceAllString(input, " "))
	value = reSpaces.ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func firstMatch(re *regexp.Regexp, input string) string {
	match := re.FindStringSubmatch(input)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
