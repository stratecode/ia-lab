package main

import "testing"

func TestConfigRequiresSeparateWriteAndServiceCredentials(t *testing.T) {
	getenv := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if _, err := loadConfig(getenv(map[string]string{"DONNA_JIRA_CLOUD_ID": "cloud-1"})); err == nil {
		t.Fatal("missing write token and service token must reject startup")
	}
	config, err := loadConfig(getenv(map[string]string{
		"DONNA_JIRA_CLOUD_ID":             "cloud-1",
		"DONNA_JIRA_WRITE_TOKEN":          "jira-write-token-that-is-long-enough-0001",
		"DONNA_JIRA_ACTION_SERVICE_TOKEN": "secretary-service-token-long-enough-0002",
		"DONNA_JIRA_ACTION_STATE":         "/state/actions.json",
		"DONNA_JIRA_ACTION_LISTEN":        "127.0.0.1:8094",
		"DONNA_JIRA_ALLOWED_PROJECTS":     "PMI",
		"DONNA_JIRA_ALLOWED_ISSUE_TYPES":  "Task,Bug",
	}))
	if err != nil || config.CloudID != "cloud-1" || config.WriteToken != "jira-write-token-that-is-long-enough-0001" || config.ServiceToken != "secretary-service-token-long-enough-0002" || config.StatePath != "/state/actions.json" || len(config.ProjectKeys) != 1 || config.ProjectKeys[0] != "PMI" || len(config.IssueTypes) != 2 {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	if _, err := loadConfig(getenv(map[string]string{
		"DONNA_JIRA_CLOUD_ID":             "cloud-1",
		"DONNA_JIRA_WRITE_TOKEN":          "jira-write-token-that-is-long-enough-0001",
		"DONNA_JIRA_ACTION_SERVICE_TOKEN": "secretary-service-token-long-enough-0002",
		"DONNA_JIRA_ACTION_LISTEN":        "0.0.0.0:8094",
		"DONNA_JIRA_ALLOWED_PROJECTS":     "PMI",
		"DONNA_JIRA_ALLOWED_ISSUE_TYPES":  "Task",
	})); err == nil {
		t.Fatal("write service must not listen on a public interface")
	}
}
