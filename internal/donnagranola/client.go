package donnagranola

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	granolaAPIURL       = "https://public-api.granola.ai"
	granolaResponseSize = 1 << 20
)

var (
	ErrNoteNotReady = errors.New("Granola note is not readable yet")
	ErrTemporary    = errors.New("temporary Granola API failure")
	ErrUnauthorized = errors.New("Granola API authorization failed")
)

type GranolaNote struct {
	ID      string
	URL     string
	Title   string
	Summary string
	Date    string
	Owner   string
}

type GranolaAction struct {
	NoteID        string `json:"note_id"`
	NoteURL       string `json:"note_url"`
	MeetingTitle  string `json:"meeting_title"`
	SourceSpan    string `json:"source_span"`
	SourceSummary string `json:"source_summary"`
}

type GranolaNoteReader interface {
	GetNote(context.Context, string) (GranolaNote, error)
}

type GranolaClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewGranolaClient(apiKey string) *GranolaClient {
	return newGranolaClient(granolaAPIURL, apiKey, &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	})
}

func newGranolaClient(baseURL, apiKey string, client *http.Client) *GranolaClient {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &GranolaClient{baseURL: strings.TrimRight(baseURL, "/"), apiKey: strings.TrimSpace(apiKey), http: client}
}

type granolaNoteResponse struct {
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	ShareURL string  `json:"share_url"`
	Title    *string `json:"title"`
	Owner    struct {
		Name string `json:"name"`
	} `json:"owner"`
	CreatedAt     string `json:"created_at"`
	CalendarEvent *struct {
		ScheduledStartTime *string `json:"scheduled_start_time"`
	} `json:"calendar_event"`
	SummaryText     string  `json:"summary_text"`
	SummaryMarkdown *string `json:"summary_markdown"`
}

func (c *GranolaClient) GetNote(ctx context.Context, noteID string) (GranolaNote, error) {
	if c == nil || c.http == nil || c.apiKey == "" || !noteIDPattern.MatchString(noteID) {
		return GranolaNote{}, errors.New("invalid Granola note request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/notes/"+url.PathEscape(noteID), nil)
	if err != nil {
		return GranolaNote{}, errors.New("cannot create Granola request")
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return GranolaNote{}, fmt.Errorf("%w: transport error", ErrTemporary)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusNotFound, http.StatusConflict, http.StatusTooEarly:
			return GranolaNote{}, ErrNoteNotReady
		case http.StatusUnauthorized, http.StatusForbidden:
			return GranolaNote{}, ErrUnauthorized
		case http.StatusTooManyRequests:
			return GranolaNote{}, ErrTemporary
		default:
			if response.StatusCode >= 500 {
				return GranolaNote{}, ErrTemporary
			}
			return GranolaNote{}, fmt.Errorf("Granola API returned HTTP %d", response.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, granolaResponseSize+1))
	if err != nil {
		return GranolaNote{}, fmt.Errorf("%w: cannot read Granola note response", ErrTemporary)
	}
	if len(data) > granolaResponseSize {
		return GranolaNote{}, errors.New("Granola response is invalid or too large")
	}
	var raw granolaNoteResponse
	if err := json.Unmarshal(data, &raw); err != nil || raw.ID != noteID {
		return GranolaNote{}, errors.New("Granola response is invalid")
	}
	summary := raw.SummaryText
	if raw.SummaryMarkdown != nil && strings.TrimSpace(*raw.SummaryMarkdown) != "" {
		summary = *raw.SummaryMarkdown
	}
	date := raw.CreatedAt
	if raw.CalendarEvent != nil && raw.CalendarEvent.ScheduledStartTime != nil && strings.TrimSpace(*raw.CalendarEvent.ScheduledStartTime) != "" {
		date = *raw.CalendarEvent.ScheduledStartTime
	}
	title := ""
	if raw.Title != nil {
		title = strings.TrimSpace(*raw.Title)
	}
	noteURL := strings.TrimSpace(raw.URL)
	if noteURL == "" {
		noteURL = strings.TrimSpace(raw.ShareURL)
	}
	if !validGranolaURL(noteURL) {
		noteURL = ""
	}
	return GranolaNote{ID: raw.ID, URL: noteURL, Title: title, Summary: strings.TrimSpace(summary), Date: strings.TrimSpace(date), Owner: strings.TrimSpace(raw.Owner.Name)}, nil
}

func validGranolaURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}
