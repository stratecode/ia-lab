package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/stratecode/lab/internal/donnajira"
)

type config struct {
	Listen       string
	CloudID      string
	WriteToken   string
	ServiceToken string
	ProjectKeys  []string
	IssueTypes   []string
	StatePath    string
}

func main() {
	if err := run(); err != nil {
		log.Fatal("donna Jira actions service stopped")
	}
}

func run() error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	store, err := donnajira.OpenApprovalStore(cfg.StatePath)
	if err != nil {
		return err
	}
	writer := donnajira.JiraClient{CloudID: cfg.CloudID, AccessToken: cfg.WriteToken, ProjectKeys: cfg.ProjectKeys, IssueTypes: cfg.IssueTypes, HTTPClient: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	auth := donnajira.HeaderAuthenticator{ServiceToken: cfg.ServiceToken}
	server := &http.Server{
		Addr: cfg.Listen, Handler: donnajira.NewHandler(store, writer, auth, time.Now),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Listen:       strings.TrimSpace(getenv("DONNA_JIRA_ACTION_LISTEN")),
		CloudID:      strings.TrimSpace(getenv("DONNA_JIRA_CLOUD_ID")),
		WriteToken:   strings.TrimSpace(getenv("DONNA_JIRA_WRITE_TOKEN")),
		ServiceToken: strings.TrimSpace(getenv("DONNA_JIRA_ACTION_SERVICE_TOKEN")),
		ProjectKeys:  splitCSV(getenv("DONNA_JIRA_ALLOWED_PROJECTS")),
		IssueTypes:   splitCSV(getenv("DONNA_JIRA_ALLOWED_ISSUE_TYPES")),
		StatePath:    strings.TrimSpace(getenv("DONNA_JIRA_ACTION_STATE")),
	}
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:8094"
	}
	if cfg.StatePath == "" {
		cfg.StatePath = "/var/lib/donna-jira-actions/actions.json"
	}
	host, _, err := net.SplitHostPort(cfg.Listen)
	address := net.ParseIP(host)
	if err != nil || (address == nil || !address.IsLoopback()) || cfg.CloudID == "" || len(cfg.WriteToken) < 32 || len(cfg.ServiceToken) < 32 || cfg.WriteToken == cfg.ServiceToken || !validProjectKeys(cfg.ProjectKeys) || !validIssueTypes(cfg.IssueTypes) {
		return config{}, errors.New("Jira action service credentials are not configured")
	}
	return cfg, nil
}

func splitCSV(value string) []string {
	var result []string
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ",") {
		item := strings.TrimSpace(part)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return result
}

func validProjectKeys(values []string) bool {
	pattern := regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !pattern.MatchString(value) {
			return false
		}
	}
	return true
}

func validIssueTypes(values []string) bool {
	pattern := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,49}$`)
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !pattern.MatchString(value) {
			return false
		}
	}
	return true
}
