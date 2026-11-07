package secretary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/donnaidentity"
)

func (c *Connectors) FindMappingOptions(ctx context.Context, kind donnaidentity.EntityKind, query string) ([]MappingOption, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 120 || c.Config.Jira.AccessToken == "" {
		return nil, errors.New("Jira lookup unavailable")
	}
	cloudID := strings.TrimSpace(c.Config.Jira.CloudID)
	if !regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`).MatchString(cloudID) {
		return nil, errors.New("Jira cloud ID invalid")
	}
	base := "https://api.atlassian.com"
	if c.allowTestHTTP {
		if u, err := url.Parse(c.jiraAPIBase); err == nil && u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost") {
			base = strings.TrimRight(c.jiraAPIBase, "/")
		}
	}
	values := url.Values{"query": {query}, "maxResults": {"50"}}
	path := ""
	switch kind {
	case donnaidentity.EntityPerson:
		path = "/rest/api/3/user/search"
	case donnaidentity.EntityProject:
		path = "/rest/api/3/project/search"
	default:
		return nil, errors.New("unsupported mapping kind")
	}
	endpoint := base + "/ex/jira/" + url.PathEscape(cloudID) + path + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("Jira mapping lookup rejected")
	}
	req.Header.Set("Authorization", "Bearer "+c.Config.Jira.AccessToken)
	req.Header.Set("Accept", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := conversationHTTP(ctx, client, req, "jira.mapping")
	if err != nil {
		return nil, errors.New("Jira mapping lookup unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Jira mapping lookup HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, errors.New("Jira mapping response invalid")
	}
	var options []MappingOption
	if kind == donnaidentity.EntityPerson {
		var users []jiraPerson
		if json.Unmarshal(data, &users) != nil {
			return nil, errors.New("Jira user response invalid")
		}
		for _, user := range users {
			if user.Active && jiraAccountID.MatchString(user.AccountID) && strings.TrimSpace(user.DisplayName) != "" {
				options = append(options, MappingOption{Kind: kind, CanonicalID: user.AccountID, CanonicalLabel: user.DisplayName, Provider: "Jira", ExternalID: user.AccountID, Evidence: []string{"Jira user search: " + user.DisplayName}})
			}
		}
	} else {
		var result struct {
			Values []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"values"`
		}
		if json.Unmarshal(data, &result) != nil {
			return nil, errors.New("Jira project response invalid")
		}
		for _, project := range result.Values {
			if projectKeyPattern.MatchString(project.Key) && strings.TrimSpace(project.Name) != "" {
				options = append(options, MappingOption{Kind: kind, CanonicalID: project.Key, CanonicalLabel: project.Name, Provider: "Jira", ExternalID: project.Key, Evidence: []string{"Jira project search: " + project.Key + " — " + project.Name}})
			}
		}
	}
	if len(options) > 50 {
		options = options[:50]
	}
	return options, nil
}

var projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,19}$`)
