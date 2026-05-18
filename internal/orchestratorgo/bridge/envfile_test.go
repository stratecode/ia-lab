package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractEnvFileArg(t *testing.T) {
	path, filtered, err := ExtractEnvFileArg([]string{"bridge", "register", "--env-file", ".env.bridge", "--api-key", "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != ".env.bridge" {
		t.Fatalf("unexpected env file path: %q", path)
	}
	if len(filtered) != 4 || filtered[2] != "--api-key" {
		t.Fatalf("unexpected filtered args: %#v", filtered)
	}
}

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.bridge")
	content := "LAB_AGENT_BASE_URL=https://example.test/orchestrator\nexport LAB_AGENT_NAME=\"bridge-one\"\nLAB_AGENT_API_KEY='secret'\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAB_AGENT_BASE_URL", "")
	t.Setenv("LAB_AGENT_NAME", "")
	t.Setenv("LAB_AGENT_API_KEY", "")
	if err := LoadEnvFile(path); err != nil {
		t.Fatalf("unexpected load error: %v", err)
	}
	if got := os.Getenv("LAB_AGENT_BASE_URL"); got != "https://example.test/orchestrator" {
		t.Fatalf("unexpected base URL: %q", got)
	}
	if got := os.Getenv("LAB_AGENT_NAME"); got != "bridge-one" {
		t.Fatalf("unexpected name: %q", got)
	}
	if got := os.Getenv("LAB_AGENT_API_KEY"); got != "secret" {
		t.Fatalf("unexpected api key: %q", got)
	}
}
