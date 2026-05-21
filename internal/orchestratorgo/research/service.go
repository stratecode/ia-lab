package research

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/capabilities"
)

type Intent string

const (
	IntentSearchAnswer Intent = "search_answer"
	IntentURLSummary   Intent = "url_summary"
	IntentDocumentQA   Intent = "document_qa"
	IntentImageQA      Intent = "image_qa"
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
	Intent        Intent         `json:"intent"`
	Answer        string         `json:"answer"`
	Confidence    float64        `json:"confidence"`
	Sources       []Source       `json:"sources"`
	SearchResults []SearchResult `json:"search_results,omitempty"`
}

type Options struct {
	Client        *http.Client
	SearchBaseURL string
	MaxFetchCount int
	Capabilities  *capabilities.Client
	UtilityBaseURL string
	UtilityAPIKey  string
	UtilityTimeoutSeconds int
}

type Service struct {
	client        *http.Client
	searchBaseURL string
	maxFetchCount int
	capabilities  *capabilities.Client
	utilityBaseURL string
	utilityAPIKey string
	utilityTimeoutSeconds int
}

type fetchedSource struct {
	Source
	Content string
}

func New(options Options) *Service {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	searchBaseURL := strings.TrimSpace(options.SearchBaseURL)
	if searchBaseURL == "" {
		searchBaseURL = "https://html.duckduckgo.com/html/"
	}
	maxFetchCount := options.MaxFetchCount
	if maxFetchCount <= 0 {
		maxFetchCount = 5
	}
	return &Service{
		client:        client,
		searchBaseURL: searchBaseURL,
		maxFetchCount: maxFetchCount,
		capabilities:  options.Capabilities,
		utilityBaseURL: strings.TrimRight(strings.TrimSpace(options.UtilityBaseURL), "/"),
		utilityAPIKey: strings.TrimSpace(options.UtilityAPIKey),
		utilityTimeoutSeconds: options.UtilityTimeoutSeconds,
	}
}

func (s *Service) Query(ctx context.Context, query string) (Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}
	if location := extractFirstLocation(query); location != "" {
		lowered := strings.ToLower(query)
		if looksLikeImage(location, lowered) {
			return s.analyzeImage(ctx, location)
		}
		if looksLikeDocument(location, lowered) {
			return s.summarizeDocument(ctx, location)
		}
	}
	if rawURL := extractFirstURL(query); rawURL != "" {
		return s.summarizeURL(ctx, rawURL)
	}
	return s.searchAndSynthesize(ctx, query)
}

func ShouldUseResearch(query string) bool {
	query = strings.TrimSpace(query)
	if query == "" {
		return false
	}
	lowered := strings.ToLower(query)
	if extractFirstURL(query) != "" || extractFirstLocation(query) != "" {
		return true
	}
	researchSignals := []string{
		"latest", "recent", "today", "yesterday", "tomorrow", "current", "currently",
		"news", "release", "releases", "version", "versions", "roadmap", "price", "pricing",
		"actual", "actuales", "actualizada", "actualizado", "actualmente", "hoy", "ayer", "mañana",
		"reciente", "recientes", "último", "última", "últimos", "últimas",
		"noticias", "lanzamiento", "lanzamientos", "versión", "versiones", "precio", "precios",
		"fuentes", "fuente", "source", "sources", "cita", "citas", "verify", "verification", "verifica", "verificar",
	}
	for _, signal := range researchSignals {
		if strings.Contains(lowered, signal) {
			return true
		}
	}
	comparisonSignals := []string{
		"compare", "comparison", "vs", "versus", "difference", "diferencia", "diferencias",
		"comparar", "comparativa", "distingue", "frente a",
	}
	for _, signal := range comparisonSignals {
		if strings.Contains(lowered, signal) {
			return true
		}
	}
	if seemsAmbiguousAcronymQuery(query) {
		return true
	}
	return false
}

func (s *Service) WebSearch(ctx context.Context, query string) ([]SearchResult, error) {
	endpoint := s.searchEndpoint(query)
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

func (s *Service) summarizeDocument(ctx context.Context, location string) (Result, error) {
	if s.capabilities == nil {
		return Result{}, fmt.Errorf("document sidecar is not configured")
	}
	result, err := s.capabilities.ReadDocument(ctx, location)
	if err != nil {
		return Result{}, err
	}
	sources := sourcesFromCapability(result.SourceRefs)
	answer := strings.TrimSpace(result.Summary)
	if answer == "" {
		answer = "No se pudo extraer una síntesis útil del documento."
	}
	return Result{
		Intent:     IntentDocumentQA,
		Answer:     answer,
		Confidence: 0.74,
		Sources:    sources,
	}, nil
}

func (s *Service) analyzeImage(ctx context.Context, location string) (Result, error) {
	if s.capabilities == nil {
		return Result{}, fmt.Errorf("image sidecar is not configured")
	}
	result, err := s.capabilities.AnalyzeImage(ctx, location)
	if err != nil {
		return Result{}, err
	}
	sources := sourcesFromCapability(result.SourceRefs)
	answer := strings.TrimSpace(result.Summary)
	if answer == "" {
		answer = "No se pudo extraer una síntesis útil de la imagen."
	}
	return Result{
		Intent:     IntentImageQA,
		Answer:     answer,
		Confidence: 0.71,
		Sources:    sources,
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

	fetchCount := chooseSearchFetchCount(query, results, s.maxFetchCount)
	fetched := make([]fetchedSource, 0, fetchCount)
	seenURLs := map[string]struct{}{}
	for _, item := range results {
		if len(fetched) >= fetchCount {
			break
		}
		canonicalURL := strings.TrimSpace(item.URL)
		if _, exists := seenURLs[canonicalURL]; exists {
			continue
		}
		source, text, err := s.WebFetch(ctx, item.URL)
		if err != nil {
			continue
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		seenURLs[canonicalURL] = struct{}{}
		fetched = append(fetched, fetchedSource{
			Source:  source,
			Content: text,
		})
	}

	answer := synthesizeSearchAnswer(query, fetched, results)
	if refined, err := s.synthesizeWithUtility(ctx, query, fetched, results); err == nil && strings.TrimSpace(refined) != "" {
		answer = refined
	}
	sources := make([]Source, 0, len(fetched))
	if len(fetched) > 0 {
		for _, item := range fetched {
			sources = append(sources, item.Source)
		}
	} else {
		for _, item := range results[:min(3, len(results))] {
			sources = append(sources, Source{Title: item.Title, URI: item.URL, Kind: "search"})
		}
	}

	return Result{
		Intent:        IntentSearchAnswer,
		Answer:        answer,
		Confidence:    confidenceForSearch(query, fetched, results),
		Sources:       sources,
		SearchResults: results[:min(5, len(results))],
	}, nil
}

func (s *Service) searchEndpoint(query string) string {
	base := strings.TrimSpace(s.searchBaseURL)
	if strings.Contains(base, "{query}") {
		return strings.ReplaceAll(base, "{query}", url.QueryEscape(query))
	}
	if strings.Contains(base, "?") {
		return base + url.QueryEscape(query)
	}
	return strings.TrimRight(base, "/") + "/?q=" + url.QueryEscape(query)
}

func looksLikeURL(input string) bool {
	parsed, err := url.Parse(strings.TrimSpace(input))
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func looksLikeDocument(value, lowered string) bool {
	candidate := strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(candidate, ".pdf") ||
		strings.HasSuffix(candidate, ".docx") ||
		strings.HasSuffix(candidate, ".md") ||
		strings.HasSuffix(candidate, ".txt") ||
		strings.Contains(lowered, "document") ||
		strings.Contains(lowered, "documento") ||
		strings.Contains(lowered, "pdf")
}

func looksLikeImage(value, lowered string) bool {
	candidate := strings.ToLower(strings.TrimSpace(value))
	return strings.HasSuffix(candidate, ".png") ||
		strings.HasSuffix(candidate, ".jpg") ||
		strings.HasSuffix(candidate, ".jpeg") ||
		strings.HasSuffix(candidate, ".gif") ||
		strings.HasSuffix(candidate, ".webp") ||
		strings.Contains(lowered, "image") ||
		strings.Contains(lowered, "imagen") ||
		strings.Contains(lowered, "ocr")
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

func extractFirstLocation(input string) string {
	if rawURL := extractFirstURL(input); rawURL != "" {
		return rawURL
	}
	fields := strings.Fields(input)
	for _, field := range fields {
		candidate := strings.Trim(field, " \t\r\n\"'()[]{}<>,.")
		if strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "./") || strings.HasPrefix(candidate, "~/") {
			return candidate
		}
		if looksLikeDocument(candidate, strings.ToLower(input)) || looksLikeImage(candidate, strings.ToLower(input)) {
			return candidate
		}
	}
	return ""
}

func sourcesFromCapability(items []capabilities.SourceRef) []Source {
	sources := make([]Source, 0, len(items))
	for _, item := range items {
		sources = append(sources, Source{
			Title: item.Title,
			URI:   item.URI,
			Kind:  item.Kind,
		})
	}
	return sources
}

var (
	reAnchor       = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reSnippetBlock = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>|<div[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</div>`)
	reTitle        = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reScript       = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
	reTags         = regexp.MustCompile(`(?is)<[^>]+>`)
	reSpaces       = regexp.MustCompile(`\s+`)
	reSentence     = regexp.MustCompile(`[^.!?]+[.!?]?`)
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

func synthesizeSearchAnswer(query string, fetched []fetchedSource, results []SearchResult) string {
	if len(fetched) == 0 {
		lines := make([]string, 0, min(3, len(results)))
		for idx, item := range results[:min(3, len(results))] {
			line := fmt.Sprintf("%d. %s", idx+1, item.Title)
			if item.Snippet != "" {
				line += ": " + truncateSentence(item.Snippet, 220)
			}
			lines = append(lines, line)
		}
		return strings.TrimSpace(
			"Respuesta preliminar para la consulta:\n\n" +
				strings.Join(lines, "\n") +
				"\n\nConclusión: solo hay snippets disponibles, así que la respuesta es útil pero todavía superficial.",
		)
	}

	consensus := consensusBullets(fetched)
	sourceLines := make([]string, 0, len(fetched))
	for idx, item := range fetched {
		sourceLines = append(sourceLines, fmt.Sprintf("%d. %s: %s", idx+1, item.Title, truncateSentence(item.Content, 220)))
	}
	answer := fmt.Sprintf("Respuesta a la consulta: %s\n\n", strings.TrimSpace(query))
	if len(consensus) > 0 {
		answer += "Síntesis:\n"
		for _, bullet := range consensus {
			answer += "- " + bullet + "\n"
		}
		answer += "\n"
	}
	answer += "Evidencia principal:\n" + strings.Join(sourceLines, "\n")
	answer += "\n\nConclusión: la respuesta se apoya en varias fuentes distintas; útil para decidir, pero no para declararla verdad revelada sin revisar contexto adicional."
	return strings.TrimSpace(answer)
}

func (s *Service) synthesizeWithUtility(ctx context.Context, query string, fetched []fetchedSource, results []SearchResult) (string, error) {
	if s.utilityBaseURL == "" {
		return "", fmt.Errorf("utility base URL is not configured")
	}
	evidence := make([]string, 0, len(fetched)+len(results))
	for idx, item := range fetched {
		evidence = append(evidence, fmt.Sprintf(
			"Fuente %d\nTítulo: %s\nURL: %s\nContenido: %s",
			idx+1,
			item.Title,
			item.URI,
			truncateSentence(item.Content, 900),
		))
	}
	if len(evidence) == 0 {
		for idx, item := range results[:min(5, len(results))] {
			evidence = append(evidence, fmt.Sprintf(
				"Resultado %d\nTítulo: %s\nURL: %s\nSnippet: %s",
				idx+1,
				item.Title,
				item.URL,
				truncateSentence(item.Snippet, 300),
			))
		}
	}
	body := map[string]any{
		"model": "utility",
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "Eres un sintetizador técnico riguroso. Responde solo con la información respaldada por la evidencia dada. No inventes detalles. Si la evidencia es limitada, dilo. Escribe una respuesta breve, directa y útil, en español.",
			},
			{
				"role": "user",
				"content": fmt.Sprintf("[RESEARCH_MODE]\nConsulta:\n%s\n\nEvidencia:\n%s", query, strings.Join(evidence, "\n\n")),
			},
		},
		"temperature": 0.1,
		"max_tokens":  900,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	timeout := time.Duration(s.utilityTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.utilityBaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.utilityAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.utilityAPIKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("utility synthesis failed with status %d", resp.StatusCode)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &completion); err != nil {
		return "", err
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("utility synthesis returned no choices")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func chooseSearchFetchCount(query string, results []SearchResult, maxFetchCount int) int {
	if maxFetchCount <= 0 {
		maxFetchCount = 5
	}
	target := 3
	lowered := strings.ToLower(query)
	comparativeTerms := []string{"compare", "vs", "versus", "difference", "mejor", "peor", "compar", "alternativa", "tradeoff"}
	for _, term := range comparativeTerms {
		if strings.Contains(lowered, term) {
			target = maxFetchCount
			break
		}
	}
	domains := map[string]struct{}{}
	totalSnippetLen := 0
	snippetCount := 0
	for _, item := range results[:min(5, len(results))] {
		if host := canonicalHost(item.URL); host != "" {
			domains[host] = struct{}{}
		}
		if strings.TrimSpace(item.Snippet) != "" {
			totalSnippetLen += len(strings.TrimSpace(item.Snippet))
			snippetCount++
		}
	}
	avgSnippetLen := 0.0
	if snippetCount > 0 {
		avgSnippetLen = float64(totalSnippetLen) / float64(snippetCount)
	}
	if len(domains) < 3 || avgSnippetLen < 50 {
		target = maxInt(target, min(maxFetchCount, 5))
	}
	if len(results) < target {
		target = len(results)
	}
	if target < 1 {
		target = 1
	}
	return target
}

func confidenceForSearch(query string, fetched []fetchedSource, results []SearchResult) float64 {
	confidence := 0.60
	if len(fetched) >= 3 {
		confidence += 0.12
	} else if len(fetched) == 2 {
		confidence += 0.06
	}
	if uniqueSourceHosts(fetched) >= 3 {
		confidence += 0.08
	}
	if strings.Contains(strings.ToLower(query), "compare") || strings.Contains(strings.ToLower(query), "mejor") {
		confidence -= 0.01
	}
	if len(results) >= 5 {
		confidence += 0.03
	}
	if confidence > 0.84 {
		confidence = 0.84
	}
	return confidence
}

func consensusBullets(fetched []fetchedSource) []string {
	if len(fetched) == 0 {
		return nil
	}
	parts := make([]string, 0, len(fetched))
	for _, item := range fetched {
		sentences := pickInterestingSentences(item.Content, 2)
		if len(sentences) == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s destaca %s", item.Title, strings.Join(sentences, " ")))
	}
	if len(parts) == 0 {
		return []string{truncateSentence(fetched[0].Content, 220)}
	}
	sort.Slice(parts, func(i, j int) bool { return len(parts[i]) < len(parts[j]) })
	return parts[:min(3, len(parts))]
}

func pickInterestingSentences(text string, maxSentences int) []string {
	matches := reSentence.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(matches))
	for _, match := range matches {
		clean := strings.TrimSpace(match)
		if len(clean) < 40 {
			continue
		}
		if len(clean) > 220 {
			clean = truncateSentence(clean, 220)
		}
		candidates = append(candidates, clean)
	}
	if len(candidates) == 0 {
		return []string{truncateSentence(text, 220)}
	}
	return candidates[:min(maxSentences, len(candidates))]
}

func canonicalHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	if strings.HasPrefix(host, "www.") {
		host = strings.TrimPrefix(host, "www.")
	}
	return host
}

func uniqueSourceHosts(fetched []fetchedSource) int {
	seen := map[string]struct{}{}
	for _, item := range fetched {
		if host := canonicalHost(item.URI); host != "" {
			seen[host] = struct{}{}
		}
	}
	return len(seen)
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

var reAllCapsToken = regexp.MustCompile(`\b[A-Z]{2,5}\b`)

func seemsAmbiguousAcronymQuery(query string) bool {
	matches := reAllCapsToken.FindAllString(query, -1)
	if len(matches) == 0 {
		return false
	}
	knownSafe := map[string]struct{}{
		"SQL":  {},
		"HTTP": {},
		"HTML": {},
		"JSON": {},
		"REST": {},
		"CRUD": {},
	}
	for _, match := range matches {
		if _, ok := knownSafe[match]; ok {
			continue
		}
		return true
	}
	return false
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
