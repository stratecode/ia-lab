package bridge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

var projectNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func (e *WorkspaceExecutor) scaffoldProject(request map[string]any) (domain.LocalBridgeResultRequest, error) {
	parentPath, parentRel, err := e.resolveWithDefault(request["parent_directory"], ".")
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	projectName := sanitizeProjectName(asString(request["project_name"]))
	if projectName == "" {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: "project_name is required"}
	}
	projectType := firstNonEmptyString(strings.TrimSpace(asString(request["project_type"])), "cli_simple")
	stack := firstNonEmptyString(strings.TrimSpace(asString(request["runtime_or_stack"])), "python")
	goal := firstNonEmptyString(strings.TrimSpace(asString(request["goal"])), "Exercise the local bridge and task system")
	testFocus := firstNonEmptyString(strings.TrimSpace(asString(request["test_focus"])), "basic execution")
	initializeGit := asBool(request["initialize_git"])
	testCommand := anyStringSliceDefault(request["test_command"], defaultBridgeTestCommand(projectType, stack))
	expectedFiles := anyStringSliceDefault(request["expected_files"], expectedProjectFiles(projectType))

	projectRoot := filepath.Join(parentPath, projectName)
	if _, err := os.Stat(projectRoot); err == nil {
		return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: fmt.Sprintf("project already exists: %s", projectName)}
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}

	files := scaffoldFiles(projectType, stack, projectName, goal, testFocus)
	changed := make([]string, 0, len(files))
	for relPath, content := range files {
		target := filepath.Join(projectRoot, filepath.Clean(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return domain.LocalBridgeResultRequest{}, err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return domain.LocalBridgeResultRequest{}, err
		}
		changed = append(changed, filepath.ToSlash(filepath.Join(parentRel, projectName, relPath)))
	}

	if initializeGit {
		cmd := exec.Command("git", "init")
		cmd.Dir = projectRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			return domain.LocalBridgeResultRequest{}, LocalExecutionError{Message: fmt.Sprintf("git init failed: %s", strings.TrimSpace(string(output)))}
		}
	}

	summary := fmt.Sprintf("Scaffolded %s project %s", projectType, filepath.ToSlash(filepath.Join(parentRel, projectName)))
	stdout := strings.Join(changed, "\n")
	result, err := e.withGitArtifacts(domain.LocalBridgeResultRequest{
		Status:       "success",
		Summary:      &summary,
		Stdout:       &stdout,
		ChangedFiles: changed,
		Artifacts: []map[string]any{
			{
				"type":         "project_manifest",
				"title":        "Scaffolded project manifest",
				"path":         filepath.ToSlash(filepath.Join(parentRel, projectName, "README.md")),
				"media_type":   "text/markdown",
				"content_text": truncateString(files["README.md"], 2000),
				"project_type": projectType,
				"runtime":      stack,
				"test_command": strings.Join(testCommand, " "),
			},
			{
				"type":         "project_contract",
				"title":        "Expected scaffold contract",
				"path":         filepath.ToSlash(filepath.Join(parentRel, projectName, "lab.json")),
				"media_type":   "application/json",
				"content_text": truncateString(files["lab.json"], 2000),
				"expected_files": expectedFiles,
			},
		},
	})
	if err != nil {
		return domain.LocalBridgeResultRequest{}, err
	}
	return result, nil
}

func scaffoldFiles(projectType, stack, projectName, goal, testFocus string) map[string]string {
	projectType = strings.ToLower(strings.TrimSpace(projectType))
	stack = strings.ToLower(strings.TrimSpace(stack))
	readme := fmt.Sprintf(`# %s

Type: %s
Stack: %s
Goal: %s
Test focus: %s

This mini project exists to test the Lab local bridge, task orchestration, approvals, diffs, and result persistence without requiring a cathedral of dependencies.
`, projectName, projectType, stack, goal, testFocus)

	files := map[string]string{
		"README.md": readme,
	}

	switch projectType {
	case "api_http":
		files["app.py"] = `from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", 8088), Handler).serve_forever()
`
		files["tests/test_app.py"] = `from pathlib import Path


def test_app_has_health_route():
    source = Path("app.py").read_text()
    assert '"/health"' in source
`
	case "web_small":
		files["index.html"] = fmt.Sprintf(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <title>%s</title>
    <link rel="stylesheet" href="style.css" />
  </head>
  <body>
    <main>
      <h1>%s</h1>
      <p>%s</p>
      <button id="ping">Run local test</button>
      <pre id="output">ready</pre>
    </main>
    <script src="app.js"></script>
  </body>
</html>
`, projectName, projectName, goal)
		files["app.js"] = `document.getElementById("ping").addEventListener("click", () => {
  document.getElementById("output").textContent = "bridge-tui-ok";
});
`
		files["style.css"] = `body { font-family: sans-serif; margin: 2rem; } main { max-width: 42rem; }`
		files["tests/test_static.py"] = `from pathlib import Path


def test_index_contains_button():
    html = Path("index.html").read_text()
    assert "Run local test" in html
`
	case "worker_background":
		files["worker.py"] = fmt.Sprintf(`def run_job(payload: dict | None = None) -> str:
    payload = payload or {}
    return "processed:%s:" + str(payload.get("job", "default"))


if __name__ == "__main__":
    print(run_job({"job": "demo"}))
`, projectName)
		files["tests/test_worker.py"] = `from worker import run_job


def test_run_job():
    assert run_job({"job": "sync"}) == "processed:` + projectName + `:sync"
`
	case "debug_regression":
		files["calculator.py"] = `def add(a: int, b: int) -> int:
    return a + b


def divide(a: int, b: int) -> float:
    if b == 0:
        raise ValueError("division by zero")
    return a / b
`
		files["tests/test_calculator.py"] = `from calculator import add, divide


def test_add():
    assert add(2, 3) == 5


def test_divide():
    assert divide(8, 2) == 4
`
	case "toy_repo":
		files["src/service.py"] = `def summarize(items: list[str]) -> str:
    return ", ".join(sorted(items))
`
		files["tests/test_service.py"] = `from src.service import summarize


def test_summarize():
    assert summarize(["beta", "alpha"]) == "alpha, beta"
`
		files["docs/notes.md"] = "This repository is intentionally tiny. The point is orchestration testing, not medieval craftsmanship.\n"
	default:
		files["main.py"] = fmt.Sprintf(`def main() -> str:
    return "hello from %s"


if __name__ == "__main__":
    print(main())
`, projectName)
		files["tests/test_main.py"] = `from main import main


def test_main():
    assert main() == "hello from ` + projectName + `"
`
	}

	files["lab.json"] = fmt.Sprintf(`{
  "project_name": %q,
  "project_type": %q,
  "runtime_or_stack": %q,
  "goal": %q,
  "test_focus": %q,
  "test_command": %q
}
`, projectName, projectType, stack, goal, testFocus, strings.Join(defaultBridgeTestCommand(projectType, stack), " "))

	return files
}

func sanitizeProjectName(value string) string {
	value = strings.TrimSpace(value)
	value = projectNameSanitizer.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.")
	return value
}

func truncateString(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max]
}

func expectedProjectFiles(projectType string) []string {
	switch strings.ToLower(strings.TrimSpace(projectType)) {
	case "api_http":
		return []string{"README.md", "app.py", "tests/test_app.py", "lab.json"}
	case "web_small":
		return []string{"README.md", "index.html", "app.js", "style.css", "tests/test_static.py", "lab.json"}
	case "worker_background":
		return []string{"README.md", "worker.py", "tests/test_worker.py", "lab.json"}
	case "debug_regression":
		return []string{"README.md", "calculator.py", "tests/test_calculator.py", "lab.json"}
	case "toy_repo":
		return []string{"README.md", "src/service.py", "tests/test_service.py", "docs/notes.md", "lab.json"}
	default:
		return []string{"README.md", "main.py", "tests/test_main.py", "lab.json"}
	}
}
