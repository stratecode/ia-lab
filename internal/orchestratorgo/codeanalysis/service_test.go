package codeanalysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeFindsMissingLockAndSecret(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"left-pad":"*"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte(`const apiKey = "supersecret123"; eval("2+2")`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Analyze(root, []string{"dependencies", "security"})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if result.Summary.TotalFindings < 3 {
		t.Fatalf("expected at least 3 findings, got %#v", result)
	}
	if result.Summary.BySeverity["critical"] == 0 {
		t.Fatalf("expected critical finding, got %#v", result.Summary.BySeverity)
	}
	if result.Summary.BySeverity["warning"] == 0 {
		t.Fatalf("expected warning finding, got %#v", result.Summary.BySeverity)
	}
}

func TestAnalyzeDefaultsToDependenciesAndSecurity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/test"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Analyze(root, nil)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if result.Summary.TotalFindings == 0 {
		t.Fatalf("expected default analysis to emit findings")
	}
}
