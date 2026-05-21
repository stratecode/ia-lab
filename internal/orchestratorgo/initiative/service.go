package initiative

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
)

type Service struct {
	cfg      config.Config
	research *research.Service
	client   *http.Client
}

type PhaseArtifacts struct {
	Markdown string
	JSON     map[string]any
}

func New(cfg config.Config, researchService *research.Service) *Service {
	timeout := time.Duration(cfg.LlamaTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &Service{
		cfg:      cfg,
		research: researchService,
		client:   &http.Client{Timeout: timeout},
	}
}

func (s *Service) GenerateRequirements(ctx context.Context, initiative *domain.InitiativeResponse, feedback string) (PhaseArtifacts, error) {
	researchNotes := s.researchNotes(ctx, initiative.Goal)
	prompt := strings.Join([]string{
		"Eres un analyst técnico. Convierte una idea en requisitos técnicos ampliados para un workspace concreto.",
		`Devuelve SOLO JSON valido con estas claves: title, objective, scope, out_of_scope, constraints, risks, acceptance_criteria, open_questions, assumptions.`,
		"Contexto:",
		fmt.Sprintf("Title: %s", initiative.Title),
		fmt.Sprintf("Workspace: %s", initiative.WorkspaceRoot),
		fmt.Sprintf("Goal: %s", initiative.Goal),
		feedbackBlock(feedback),
		researchNotes,
	}, "\n")
	payload, err := s.callStructured(ctx, prompt)
	if err != nil {
		payload = fallbackRequirements(initiative, feedback, researchNotes)
	} else if err := ValidateRequirementsPayload(payload); err != nil {
		return PhaseArtifacts{}, fmt.Errorf("analyst payload validation failed: %w", err)
	}
	return PhaseArtifacts{
		Markdown: renderRequirementsMarkdown(payload),
		JSON:     payload,
	}, nil
}

func (s *Service) GenerateDesign(ctx context.Context, initiative *domain.InitiativeResponse, requirements map[string]any, feedback string) (PhaseArtifacts, error) {
	reqJSON, _ := json.Marshal(requirements)
	prompt := strings.Join([]string{
		"Eres un architect técnico. Convierte requisitos aprobados en un diseño técnico ejecutable.",
		`Devuelve SOLO JSON valido con estas claves: title, architecture, components, interfaces, data_model, testing_strategy, technical_risks, pending_decisions.`,
		"Contexto:",
		fmt.Sprintf("Title: %s", initiative.Title),
		fmt.Sprintf("Workspace: %s", initiative.WorkspaceRoot),
		fmt.Sprintf("Goal: %s", initiative.Goal),
		"Requirements JSON:",
		string(reqJSON),
		feedbackBlock(feedback),
	}, "\n")
	payload, err := s.callStructured(ctx, prompt)
	if err != nil {
		payload = fallbackDesign(initiative, requirements, feedback)
	} else if err := ValidateDesignPayload(payload); err != nil {
		return PhaseArtifacts{}, fmt.Errorf("architect payload validation failed: %w", err)
	}
	return PhaseArtifacts{
		Markdown: renderDesignMarkdown(payload),
		JSON:     payload,
	}, nil
}

func (s *Service) GenerateExecutionPlan(ctx context.Context, initiative *domain.InitiativeResponse, design map[string]any, feedback string) (PhaseArtifacts, error) {
	designJSON, _ := json.Marshal(design)
	prompt := strings.Join([]string{
		"Eres un planner operativo. Convierte un diseño aprobado en un backlog ejecutable y selectivamente lanzable.",
		`Devuelve SOLO JSON valido con esta forma: {"title":"...","epics":[{"name":"...","group":"...","tasks":[{"title":"...","description":"...","suggested_agent":"coder","priority":"normal","execution_mode":"agent_local","execution_target":"local","approval_required":false,"definition_of_done":"...","metadata":{}}]}]}`,
		"Reglas:",
		"- suggested_agent solo puede ser planner,researcher,coder,reviewer",
		"- execution_mode solo puede ser manual,agent_local,agent_remote",
		"- execution_target solo puede ser local o remote",
		"- incluye metadata suficiente para ejecutar tareas de workspace cuando proceda",
		"Contexto:",
		fmt.Sprintf("Title: %s", initiative.Title),
		fmt.Sprintf("Workspace: %s", initiative.WorkspaceRoot),
		fmt.Sprintf("Goal: %s", initiative.Goal),
		"Design JSON:",
		string(designJSON),
		feedbackBlock(feedback),
	}, "\n")
	payload, err := s.callStructured(ctx, prompt)
	if err != nil {
		payload = fallbackExecutionPlan(initiative, design, feedback)
	} else if err := ValidateExecutionPlanPayload(payload); err != nil {
		return PhaseArtifacts{}, fmt.Errorf("planner payload validation failed: %w", err)
	}
	return PhaseArtifacts{
		Markdown: renderExecutionPlanMarkdown(payload),
		JSON:     payload,
	}, nil
}

func (s *Service) callStructured(ctx context.Context, prompt string) (map[string]any, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(s.cfg.LlamaPlannerBaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("planner base url is not configured")
	}
	requestCtx := ctx
	cancel := func() {}
	if budget := s.requestBudget(); budget > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, budget)
	}
	defer cancel()
	body := map[string]any{
		"model": "planner",
		"messages": []map[string]string{
			{"role": "user", "content": "[PLAN_MODE]\n" + prompt},
		},
		"temperature":     0.1,
		"max_tokens":      2200,
		"response_format": map[string]any{"type": "json_object"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(s.cfg.LlamaPlannerAPIKey); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("planner request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("planner returned no choices")
	}
	content := stripCodeFence(completion.Choices[0].Message.Content)
	var out map[string]any
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) requestBudget() time.Duration {
	budget := 20 * time.Second
	if timeout := s.client.Timeout; timeout > 0 && timeout < budget {
		return timeout
	}
	return budget
}

func (s *Service) researchNotes(ctx context.Context, goal string) string {
	if s.research == nil || strings.TrimSpace(goal) == "" {
		return ""
	}
	if !research.ShouldUseResearch(goal) {
		return ""
	}
	researchCtx := ctx
	cancel := func() {}
	if budget := 5 * time.Second; budget > 0 {
		researchCtx, cancel = context.WithTimeout(ctx, budget)
	}
	defer cancel()
	result, err := s.research.Query(researchCtx, goal)
	if err != nil || strings.TrimSpace(result.Answer) == "" {
		return ""
	}
	return "Research context:\n" + truncate(result.Answer, 1000)
}

func fallbackRequirements(initiative *domain.InitiativeResponse, feedback, researchNotes string) map[string]any {
	return map[string]any{
		"title":     initiative.Title,
		"objective": initiative.Goal,
		"scope": []string{
			"Convert the idea into a validated, executable initiative for a single workspace",
			"Keep operator approvals between major phases",
			"Produce artifacts that can drive task generation and selective execution",
		},
		"out_of_scope": []string{
			"UI polish beyond TUI and Telegram control",
			"Full autonomy without approvals",
		},
		"constraints": []string{
			fmt.Sprintf("All work must remain bound to workspace %s", initiative.WorkspaceRoot),
			"Artifacts are the source of truth; workspace files are execution side-effects",
		},
		"risks": []string{
			"Ambiguous initial idea can leak into weak design if not corrected in review",
			"Selective execution still needs dependency discipline between tasks",
		},
		"acceptance_criteria": []string{
			"Requirements are reviewable and technically actionable",
			"Requirements enumerate acceptance criteria and open questions",
		},
		"open_questions": []string{
			"What parts of the initiative remain manual by policy?",
			"What level of autonomy is acceptable after plan approval?",
		},
		"assumptions": []string{
			firstNonEmptyString(strings.TrimSpace(feedback), "No additional operator corrections were supplied."),
			firstNonEmptyString(strings.TrimSpace(researchNotes), "No external research context was required."),
		},
	}
}

func fallbackDesign(initiative *domain.InitiativeResponse, requirements map[string]any, feedback string) map[string]any {
	return map[string]any{
		"title":        initiative.Title,
		"architecture": "Go orchestrator runtime remains the control plane. Initiative lifecycle sits above tasks and persists versioned phase artifacts. TUI is the authoring and approval surface, Telegram is remote control.",
		"components": []map[string]any{
			{"name": "initiative API", "responsibility": "create, advance, approve, reject, generate and launch"},
			{"name": "initiative store", "responsibility": "persist initiatives, phase reviews, task links"},
			{"name": "artifact layer", "responsibility": "store markdown/json outputs per phase"},
			{"name": "TUI initiative cockpit", "responsibility": "review and launch work from a workspace"},
			{"name": "Telegram control surface", "responsibility": "remote phase approvals and status checks"},
		},
		"interfaces": []string{
			"POST /initiatives",
			"POST /initiatives/{id}/advance",
			"POST /initiatives/{id}/approve/{phase}",
			"POST /initiatives/{id}/tasks/generate",
			"POST /initiatives/{id}/tasks/launch",
		},
		"data_model": map[string]any{
			"initiative":    []string{"status", "current_phase", "active artifact ids", "workspace_root"},
			"phase_reviews": []string{"phase", "decision", "feedback", "artifact ids"},
			"task_links":    []string{"phase_origin", "execution_mode", "launch_group", "launch_order"},
		},
		"testing_strategy": []string{
			"API integration tests per phase transition",
			"TUI flow tests for initiative creation and approval",
			"E2E on temporary workspace with selective launch",
		},
		"technical_risks": []string{
			"Phase artifacts may become inconsistent if transition rules are weak",
			"Task launch ordering can degrade if generation omits explicit launch order",
		},
		"pending_decisions": []string{
			firstNonEmptyString(strings.TrimSpace(feedback), "No additional design corrections were supplied."),
		},
		"requirements_snapshot": requirements,
	}
}

func fallbackExecutionPlan(initiative *domain.InitiativeResponse, design map[string]any, feedback string) map[string]any {
	projectName := sanitizeProjectName(initiative.Title)
	parentDir := initiative.WorkspaceRoot
	goal := firstNonEmptyString(initiative.Goal, initiative.Title)
	projectType := "cli_simple"
	stack := "python"
	testCommand := defaultInitiativeTestCommand(projectType, stack)
	expectedFiles := expectedInitiativeFiles(projectType)
	return map[string]any{
		"title": initiative.Title,
		"epics": []map[string]any{
			{
				"name":  "Context and validation",
				"group": "discovery",
				"tasks": []map[string]any{
					{
						"title":              "Research the initiative constraints",
						"description":        "Produce context, checklist and constraints for the initiative before code execution.",
						"suggested_agent":    "researcher",
						"priority":           "normal",
						"execution_mode":     "agent_local",
						"execution_target":   "local",
						"approval_required":  false,
						"definition_of_done": "Research context is persisted and references the initiative goal.",
						"metadata": map[string]any{
							"initiative_goal": goal,
						},
					},
				},
			},
			{
				"name":  "Initial delivery scaffold",
				"group": "delivery",
				"tasks": []map[string]any{
					{
						"title":              "Create a minimal runnable project",
						"description":        fmt.Sprintf("Create a minimal project in workspace %s for initiative %s.", initiative.WorkspaceRoot, initiative.Title),
						"suggested_agent":    "coder",
						"priority":           "high",
						"execution_mode":     "agent_local",
						"execution_target":   "local",
						"approval_required":  false,
						"definition_of_done": "Workspace contains runnable scaffold, manifest and test file.",
						"metadata": map[string]any{
							"tool_request": map[string]any{
								"tool": "scaffold_project",
							},
							"project_request": map[string]any{
								"project_name":      projectName,
								"parent_directory":  parentDir,
								"project_type":      projectType,
								"runtime_or_stack":  stack,
								"goal":              goal,
								"test_focus":        "initiative execution flow",
								"initialize_git":    true,
								"requires_approval": false,
								"test_command":      testCommand,
								"expected_files":    expectedFiles,
							},
							"workspace_root": initiative.WorkspaceRoot,
						},
					},
					{
						"title":              "Validate the generated scaffold",
						"description":        "Review the scaffolded project and validate expected files and test command.",
						"suggested_agent":    "reviewer",
						"priority":           "normal",
						"execution_mode":     "agent_local",
						"execution_target":   "local",
						"approval_required":  false,
						"definition_of_done": "Reviewer confirms the scaffold passes expected checks.",
						"metadata": map[string]any{
							"tool_request": map[string]any{
								"tool": "review_project",
							},
							"project_request": map[string]any{
								"project_name":      projectName,
								"parent_directory":  parentDir,
								"project_type":      projectType,
								"runtime_or_stack":  stack,
								"goal":              goal,
								"test_focus":        "initiative execution flow",
								"initialize_git":    true,
								"requires_approval": false,
								"test_command":      testCommand,
								"expected_files":    expectedFiles,
							},
							"workspace_root": initiative.WorkspaceRoot,
						},
					},
				},
			},
			{
				"name":  "Human approval and handoff",
				"group": "review",
				"tasks": []map[string]any{
					{
						"title":              "Review initiative outputs manually",
						"description":        "Inspect the generated artifacts, backlog and workspace outcome before expanding execution.",
						"suggested_agent":    "reviewer",
						"priority":           "low",
						"execution_mode":     "manual",
						"execution_target":   "local",
						"approval_required":  false,
						"definition_of_done": firstNonEmptyString(strings.TrimSpace(feedback), "Operator confirms the initiative is ready for broader execution."),
						"metadata": map[string]any{
							"manual_only": true,
						},
					},
				},
			},
		},
		"design_snapshot": design,
	}
}

func renderRequirementsMarkdown(payload map[string]any) string {
	return strings.Join([]string{
		"# Requirements",
		"",
		mdSection("Objective", payload["objective"]),
		mdSection("Scope", payload["scope"]),
		mdSection("Out of scope", payload["out_of_scope"]),
		mdSection("Constraints", payload["constraints"]),
		mdSection("Risks", payload["risks"]),
		mdSection("Acceptance criteria", payload["acceptance_criteria"]),
		mdSection("Open questions", payload["open_questions"]),
		mdSection("Assumptions", payload["assumptions"]),
	}, "\n")
}

func renderDesignMarkdown(payload map[string]any) string {
	return strings.Join([]string{
		"# Technical Design",
		"",
		mdSection("Architecture", payload["architecture"]),
		mdSection("Components", payload["components"]),
		mdSection("Interfaces", payload["interfaces"]),
		mdSection("Data model", payload["data_model"]),
		mdSection("Testing strategy", payload["testing_strategy"]),
		mdSection("Technical risks", payload["technical_risks"]),
		mdSection("Pending decisions", payload["pending_decisions"]),
	}, "\n")
}

func renderExecutionPlanMarkdown(payload map[string]any) string {
	lines := []string{"# Execution Plan", ""}
	for _, epic := range normalizedMapSlice(payload["epics"]) {
		lines = append(lines, "## "+firstNonEmptyString(asString(epic["name"]), "Epic"))
		lines = append(lines, "")
		for _, task := range normalizedMapSlice(epic["tasks"]) {
			lines = append(lines, fmt.Sprintf("- %s [%s / %s / %s]", firstNonEmptyString(asString(task["title"]), "Task"), asString(task["suggested_agent"]), asString(task["execution_mode"]), asString(task["priority"])))
			lines = append(lines, "  "+firstNonEmptyString(asString(task["description"]), ""))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func mdSection(title string, value any) string {
	lines := []string{"## " + title}
	switch v := value.(type) {
	case string:
		lines = append(lines, strings.TrimSpace(v))
	case []any:
		for _, item := range v {
			lines = append(lines, "- "+strings.TrimSpace(fmt.Sprint(item)))
		}
	case []string:
		for _, item := range v {
			lines = append(lines, "- "+strings.TrimSpace(item))
		}
	case map[string]any:
		for key, item := range v {
			lines = append(lines, fmt.Sprintf("- %s: %v", key, item))
		}
	default:
		lines = append(lines, strings.TrimSpace(fmt.Sprint(v)))
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func feedbackBlock(feedback string) string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return "Feedback operator: none"
	}
	return "Feedback operator:\n" + feedback
}

func stripCodeFence(input string) string {
	value := strings.TrimSpace(input)
	value = strings.TrimPrefix(value, "```json")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

func truncate(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

var nonSlugChars = regexp.MustCompile(`[^a-z0-9\-]+`)
var multiDash = regexp.MustCompile(`\-+`)

func sanitizeProjectName(input string) string {
	value := strings.ToLower(strings.TrimSpace(input))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	value = nonSlugChars.ReplaceAllString(value, "-")
	value = multiDash.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "initiative-project"
	}
	return value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func asString(input any) string {
	switch v := input.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func normalizedMapSlice(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			typed, ok := item.(map[string]any)
			if ok {
				out = append(out, typed)
			}
		}
		return out
	default:
		return nil
	}
}

func defaultInitiativeTestCommand(projectType, stack string) []string {
	switch strings.ToLower(strings.TrimSpace(stack)) {
	case "static":
		return []string{"python3", "-c", "from pathlib import Path; html=Path('index.html').read_text(); assert '<html' in html.lower()"}
	case "node":
		return []string{"node", "--check", "app.js"}
	}
	switch strings.ToLower(strings.TrimSpace(projectType)) {
	case "api_http":
		return []string{"python3", "-m", "py_compile", "app.py"}
	case "worker_background":
		return []string{"python3", "-m", "py_compile", "worker.py"}
	case "debug_regression":
		return []string{"python3", "-m", "py_compile", "calculator.py"}
	case "toy_repo":
		return []string{"python3", "-m", "py_compile", "src/service.py"}
	default:
		return []string{"python3", "-m", "py_compile", "main.py"}
	}
}

func expectedInitiativeFiles(projectType string) []string {
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
