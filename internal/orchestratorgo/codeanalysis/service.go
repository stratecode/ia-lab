package codeanalysis

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type Finding struct {
	Severity string         `json:"severity"`
	Message  string         `json:"message"`
	Location map[string]any `json:"location,omitempty"`
}

type Summary struct {
	TotalFindings int            `json:"total_findings"`
	BySeverity    map[string]int `json:"by_severity"`
}

type Result struct {
	Findings []Finding `json:"findings"`
	Summary  Summary   `json:"summary"`
}

var secretPattern = regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*["'][A-Za-z0-9_\-]{8,}["']`)

func Analyze(root string, analysisTypes []string) (*Result, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil, fmt.Errorf("analysis root is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("analysis root is not a directory")
	}
	analysisTypes = normalizeTypes(analysisTypes)
	findings := make([]Finding, 0)
	for _, analysisType := range analysisTypes {
		switch analysisType {
		case "dependencies":
			findings = append(findings, analyzeDependencies(root)...)
		case "security":
			findings = append(findings, analyzeSecurity(root)...)
		case "complexity":
			findings = append(findings, analyzeComplexity(root)...)
		case "lint":
			findings = append(findings, analyzeLint(root)...)
		}
	}
	return &Result{
		Findings: findings,
		Summary:  summarize(findings),
	}, nil
}

func normalizeTypes(items []string) []string {
	if len(items) == 0 {
		return []string{"dependencies", "security"}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := strings.ToLower(strings.TrimSpace(item))
		if normalized == "" {
			continue
		}
		switch normalized {
		case "dependencies", "security", "complexity", "lint":
		default:
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return []string{"dependencies", "security"}
	}
	slices.Sort(out)
	return out
}

func analyzeDependencies(root string) []Finding {
	findings := make([]Finding, 0)
	hasFile := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	if hasFile("package.json") && !hasFile("package-lock.json") && !hasFile("pnpm-lock.yaml") && !hasFile("yarn.lock") {
		findings = append(findings, Finding{
			Severity: "warning",
			Message:  "package.json present without npm/yarn/pnpm lockfile",
			Location: map[string]any{"file": "package.json"},
		})
	}
	if hasFile("composer.json") && !hasFile("composer.lock") {
		findings = append(findings, Finding{
			Severity: "warning",
			Message:  "composer.json present without composer.lock",
			Location: map[string]any{"file": "composer.json"},
		})
	}
	if hasFile("go.mod") && !hasFile("go.sum") {
		findings = append(findings, Finding{
			Severity: "warning",
			Message:  "go.mod present without go.sum",
			Location: map[string]any{"file": "go.mod"},
		})
	}
	if hasFile("pyproject.toml") && !hasFile("poetry.lock") && !hasFile("uv.lock") && !hasFile("requirements.lock") {
		findings = append(findings, Finding{
			Severity: "info",
			Message:  "pyproject.toml present without a dedicated lockfile",
			Location: map[string]any{"file": "pyproject.toml"},
		})
	}
	packageJSON := filepath.Join(root, "package.json")
	if raw, err := os.ReadFile(packageJSON); err == nil {
		var payload map[string]any
		if json.Unmarshal(raw, &payload) == nil {
			for _, section := range []string{"dependencies", "devDependencies"} {
				deps, _ := payload[section].(map[string]any)
				for name, versionRaw := range deps {
					version := strings.TrimSpace(fmt.Sprint(versionRaw))
					if version == "*" || strings.EqualFold(version, "latest") {
						findings = append(findings, Finding{
							Severity: "warning",
							Message:  fmt.Sprintf("%s uses floating dependency %q", name, version),
							Location: map[string]any{"file": "package.json", "dependency": name},
						})
					}
				}
			}
		}
	}
	return findings
}

func analyzeSecurity(root string) []Finding {
	findings := make([]Finding, 0)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if shouldSkipPath(path) || !isLikelyCodeFile(path) {
			return nil
		}
		fileFindings, _ := scanSecurityFile(root, path)
		findings = append(findings, fileFindings...)
		return nil
	})
	return findings
}

func analyzeComplexity(root string) []Finding {
	findings := make([]Finding, 0)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if shouldSkipPath(path) || !isLikelyCodeFile(path) {
			return nil
		}
		lineCount, readErr := countLines(path)
		if readErr != nil {
			return nil
		}
		if lineCount > 500 {
			rel, _ := filepath.Rel(root, path)
			findings = append(findings, Finding{
				Severity: "info",
				Message:  fmt.Sprintf("large source file (%d lines)", lineCount),
				Location: map[string]any{"file": filepath.ToSlash(rel)},
			})
		}
		return nil
	})
	return findings
}

func analyzeLint(root string) []Finding {
	findings := make([]Finding, 0)
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if shouldSkipPath(path) || !isLikelyCodeFile(path) {
			return nil
		}
		file, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		line := 0
		for scanner.Scan() {
			line++
			text := scanner.Text()
			if strings.HasSuffix(text, " ") || strings.HasSuffix(text, "\t") {
				rel, _ := filepath.Rel(root, path)
				findings = append(findings, Finding{
					Severity: "info",
					Message:  "trailing whitespace",
					Location: map[string]any{"file": filepath.ToSlash(rel), "line": line},
				})
				break
			}
		}
		return nil
	})
	return findings
}

func scanSecurityFile(root, path string) ([]Finding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rel, _ := filepath.Rel(root, path)
	rel = filepath.ToSlash(rel)
	findings := make([]Finding, 0)
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		trimmed := strings.TrimSpace(text)
		switch {
		case secretPattern.MatchString(text):
			findings = append(findings, Finding{
				Severity: "critical",
				Message:  "possible hardcoded secret",
				Location: map[string]any{"file": rel, "line": line},
			})
		case strings.Contains(text, "shell=True"):
			findings = append(findings, Finding{
				Severity: "warning",
				Message:  "subprocess shell=True can introduce command injection risk",
				Location: map[string]any{"file": rel, "line": line},
			})
		case strings.Contains(text, "eval("):
			findings = append(findings, Finding{
				Severity: "warning",
				Message:  "eval(...) spotted; review code execution surface",
				Location: map[string]any{"file": rel, "line": line},
			})
		case strings.Contains(text, "innerHTML") && strings.Contains(trimmed, "="):
			findings = append(findings, Finding{
				Severity: "info",
				Message:  "innerHTML assignment found; verify sanitization",
				Location: map[string]any{"file": rel, "line": line},
			})
		}
	}
	return findings, nil
}

func shouldSkipPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		switch part {
		case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build":
			return true
		}
	}
	return false
}

func isLikelyCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".php", ".rb", ".java", ".rs", ".sh":
		return true
	default:
		return false
	}
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}

func summarize(findings []Finding) Summary {
	bySeverity := map[string]int{
		"info":     0,
		"warning":  0,
		"error":    0,
		"critical": 0,
	}
	for _, finding := range findings {
		severity := strings.ToLower(strings.TrimSpace(finding.Severity))
		if _, ok := bySeverity[severity]; !ok {
			severity = "info"
		}
		bySeverity[severity]++
	}
	return Summary{
		TotalFindings: len(findings),
		BySeverity:    bySeverity,
	}
}
