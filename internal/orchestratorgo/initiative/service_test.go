package initiative

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/research"
)

func TestGenerateRequirementsFallsBackToStructuredShape(t *testing.T) {
	svc := New(config.Config{}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-1",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Turn an idea into approved requirements",
	}
	artifacts, err := svc.GenerateRequirements(context.Background(), item, "Focus on operator approvals")
	if err != nil {
		t.Fatal(err)
	}
	if artifacts.JSON["objective"] != item.Goal {
		t.Fatalf("unexpected requirements payload: %#v", artifacts.JSON)
	}
	if artifacts.Markdown == "" {
		t.Fatal("expected markdown output")
	}
}

func TestGenerateRequirementsFallsBackWhenPlannerPayloadIsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"title":"broken","scope":["x"]}`}},
			},
		})
	}))
	defer server.Close()

	svc := New(config.Config{
		LlamaPlannerBaseURL: server.URL + "/v1",
		LlamaTimeoutSeconds: 5,
		LlamaPlannerAPIKey:  "",
	}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-invalid-req",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Turn an idea into approved requirements",
	}
	artifacts, err := svc.GenerateRequirements(context.Background(), item, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["objective"]); got != item.Goal {
		t.Fatalf("expected fallback objective %q, got %q", item.Goal, got)
	}
}

func TestGenerateExecutionPlanFallsBackToMultiAgentBacklog(t *testing.T) {
	svc := New(config.Config{}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-2",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Create a runnable MVP scaffold",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	foundAgents := map[string]bool{}
	switch rawEpics := artifacts.JSON["epics"].(type) {
	case []any:
		for _, epicRaw := range rawEpics {
			epic, _ := epicRaw.(map[string]any)
			rawTasks, _ := epic["tasks"].([]any)
			for _, taskRaw := range rawTasks {
				task, _ := taskRaw.(map[string]any)
				foundAgents[asString(task["suggested_agent"])] = true
			}
		}
	case []map[string]any:
		for _, epic := range rawEpics {
			switch rawTasks := epic["tasks"].(type) {
			case []any:
				for _, taskRaw := range rawTasks {
					task, _ := taskRaw.(map[string]any)
					foundAgents[asString(task["suggested_agent"])] = true
				}
			case []map[string]any:
				for _, task := range rawTasks {
					foundAgents[asString(task["suggested_agent"])] = true
				}
			}
		}
	default:
		t.Fatalf("unexpected execution plan payload: %#v", artifacts.JSON)
	}
	for _, agent := range []string{"researcher", "coder", "reviewer"} {
		if !foundAgents[agent] {
			t.Fatalf("expected %s in generated backlog, got %#v", agent, artifacts.JSON)
		}
	}
}

func TestGenerateExecutionPlanFallsBackWhenPlannerPayloadIsInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"title":"broken","epics":[]}`}},
			},
		})
	}))
	defer server.Close()

	svc := New(config.Config{
		LlamaPlannerBaseURL: server.URL + "/v1",
		LlamaTimeoutSeconds: 5,
	}, nil)
	item := &domain.InitiativeResponse{
		ID:            "initiative-invalid-plan",
		Title:         "Workspace delivery MVP",
		WorkspaceRoot: "/tmp/initiative-workspace",
		Goal:          "Create a runnable MVP scaffold",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["title"]); got != item.Title {
		t.Fatalf("expected fallback title %q, got %q", item.Title, got)
	}
}

func TestGenerateExecutionPlanUsesRepoWorkflowForExistingGitRepo(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-3",
		Title:         "Repo maintenance MVP",
		WorkspaceRoot: filepath.Join(root, "python-slugify"),
		Goal:          "Patch the CLI regression and validate the repository",
	}
	if err := os.MkdirAll(item.WorkspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(item.WorkspaceRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got == "repo_workflow_v1" {
		t.Fatalf("expected generic execution plan for ordinary git repo, got %q", got)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	metadata := coderTask["metadata"].(map[string]any)
	toolRequest := metadata["tool_request"].(map[string]any)
	if asString(toolRequest["tool"]) != "scaffold_project" {
		t.Fatalf("expected scaffold_project in generic plan, got %#v", toolRequest["tool"])
	}
}

func TestGenerateExecutionPlanUsesBenchmarkCaseFileWhenPresent(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "bench-repo")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	casePayload := map[string]any{
		"id":               "bench-case-1",
		"case_type":        "review_only",
		"repo_profile":     "python_cli_framework",
		"repo_url":         "https://github.com/pallets/click",
		"default_branch":   "main",
		"project_type":     "existing_repo",
		"runtime_or_stack": "python",
		"project_root":     ".",
		"test_focus":       "option parsing review",
		"test_command":     []string{"python3", "-m", "pytest", "-q", "tests/test_arguments.py"},
		"expected_files":   []string{"src/click/core.py", "tests/test_arguments.py"},
		"coder_tool":       "write_file",
		"patch_target":     ".lab/repo-workflow-marker.txt",
		"write_content":    "benchmark marker\n",
		"coder_summary":    "Apply the benchmark-defined deterministic repo change.",
	}
	raw, _ := json.Marshal(casePayload)
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".lab", "benchmark-case.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-4",
		Title:         "Repo benchmark",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	metadata := coderTask["metadata"].(map[string]any)
	if got := asString(metadata["benchmark_case_id"]); got != "bench-case-1" {
		t.Fatalf("expected benchmark case id in metadata, got %q", got)
	}
	projectRequest := metadata["project_request"].(map[string]any)
	if got := asString(projectRequest["repository_url"]); got != "https://github.com/pallets/click" {
		t.Fatalf("expected repository_url from benchmark case, got %q", got)
	}
}

func TestGenerateObjectiveExecutionContractForRepoObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Fix CLI regression",
		Objective:     "Patch the repository so the CLI forwards regex_pattern correctly and prove it with tests.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(contract.NormalizedObjective) == "" {
		t.Fatal("expected normalized objective")
	}
	if contract.ExecutionBackend != "aider-task" {
		t.Fatalf("expected aider-task backend, got %q", contract.ExecutionBackend)
	}
	if contract.WorkspaceRoot != root {
		t.Fatalf("expected workspace root %q, got %q", root, contract.WorkspaceRoot)
	}
	if len(contract.WorkItems) < 4 {
		t.Fatalf("expected at least 4 work items, got %#v", contract.WorkItems)
	}
	gotKinds := []string{}
	for _, item := range contract.WorkItems {
		gotKinds = append(gotKinds, string(item.Kind))
	}
	wantKinds := []string{"research", "edit", "validate", "review"}
	for _, want := range wantKinds {
		found := false
		for _, got := range gotKinds {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected work item kind %q, got %#v", want, gotKinds)
		}
	}
	var editItem *domain.WorkItem
	for idx := range contract.WorkItems {
		item := &contract.WorkItems[idx]
		if item.Kind == domain.WorkItemKindEdit {
			editItem = item
			break
		}
	}
	if editItem == nil {
		t.Fatal("expected edit work item")
	}
	if editItem.Backend != "aider-task" {
		t.Fatalf("expected aider-task edit backend, got %q", editItem.Backend)
	}
	if !editItem.ApprovalRequired {
		t.Fatal("expected edit work item to require approval")
	}
	if len(editItem.ValidationCommands) == 0 {
		t.Fatal("expected edit work item validation commands")
	}
}

func TestGenerateObjectiveExecutionContractIncludesInitialScopeHypotheses(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# planner-demo\n\nThe current objective toggle lives in src/switchboard.py.\nfeature_toggle.py is legacy and should not be the first target.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "feature_toggle.py"), []byte("LEGACY = True\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Enable objective toggle",
		Objective:     "Enable the objective toggle cleanly for the autonomous flow.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.InitialScopeHypotheses) < 2 {
		t.Fatalf("expected ranked initial_scope_hypotheses, got %#v", contract.InitialScopeHypotheses)
	}
	if contract.InitialScopeHypotheses[0].Path != "src/switchboard.py" {
		t.Fatalf("expected planner to rank switchboard first, got %#v", contract.InitialScopeHypotheses)
	}
	if contract.InitialScopeHypotheses[0].Rationale == "" || contract.InitialScopeHypotheses[0].Evidence == "" {
		t.Fatalf("expected rationale and evidence on first hypothesis, got %#v", contract.InitialScopeHypotheses[0])
	}
	if contract.InitialScopeHypotheses[0].Disposition != "selected" || contract.InitialScopeHypotheses[0].Rank != 1 || contract.InitialScopeHypotheses[0].Score <= 0 {
		t.Fatalf("expected structured selected hypothesis metadata, got %#v", contract.InitialScopeHypotheses[0])
	}
	if len(contract.RejectedScopeHypotheses) == 0 {
		t.Fatalf("expected rejected scope hypotheses, got %#v", contract)
	}
	if contract.RejectedScopeHypotheses[0].Path != "src/feature_toggle.py" {
		t.Fatalf("expected feature_toggle.py to appear as rejected competitor, got %#v", contract.RejectedScopeHypotheses)
	}
	if contract.RejectedScopeHypotheses[0].Disposition != "rejected" || contract.RejectedScopeHypotheses[0].RejectedBecause == "" {
		t.Fatalf("expected rejected hypothesis rationale, got %#v", contract.RejectedScopeHypotheses[0])
	}
	if len(contract.SuspectedPaths) == 0 || contract.SuspectedPaths[0] != "src/switchboard.py" {
		t.Fatalf("expected suspected_paths to follow ranked hypotheses, got %#v", contract.SuspectedPaths)
	}
	editItem := contract.WorkItems[1]
	hypotheses, _ := editItem.Metadata["initial_scope_hypotheses"].([]domain.ScopeHypothesis)
	if len(hypotheses) == 0 || hypotheses[0].Path != "src/switchboard.py" {
		t.Fatalf("expected edit metadata to carry planner hypotheses, got %#v", editItem.Metadata["initial_scope_hypotheses"])
	}
	rejected, _ := editItem.Metadata["rejected_scope_hypotheses"].([]domain.ScopeHypothesis)
	if len(rejected) == 0 || rejected[0].Path != "src/feature_toggle.py" {
		t.Fatalf("expected edit metadata to carry rejected planner hypotheses, got %#v", editItem.Metadata["rejected_scope_hypotheses"])
	}
}

func TestGenerateObjectiveExecutionContractForGreenfieldObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Ship a small CLI",
		Objective:     "Create a small CLI that prints a deterministic greeting and can be validated locally.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if contract.ExecutionBackend != "local_bridge+aider-task" {
		t.Fatalf("expected mixed backend for greenfield objective, got %q", contract.ExecutionBackend)
	}
	if !strings.Contains(contract.ChangeStrategy, "scaffold a minimal runnable project in place") {
		t.Fatalf("expected bootstrap-aware change strategy, got %q", contract.ChangeStrategy)
	}
	if contract.RepoProfile != "greenfield_cli_simple" {
		t.Fatalf("expected greenfield repo profile, got %q", contract.RepoProfile)
	}
	if len(contract.WorkItems) != 5 {
		t.Fatalf("expected research + bootstrap + edit + validate + review, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].ID != "bootstrap" {
		t.Fatalf("expected bootstrap work item in second position, got %#v", contract.WorkItems[1])
	}
	if asString(contract.WorkItems[1].ToolRequest["tool"]) != "scaffold_project" {
		t.Fatalf("expected scaffold_project tool for bootstrap, got %#v", contract.WorkItems[1].ToolRequest)
	}
	if asString(contract.WorkItems[1].ToolRequest["project_root"]) != "." {
		t.Fatalf("expected in-place scaffold project_root, got %#v", contract.WorkItems[1].ToolRequest)
	}
	if contract.WorkItems[1].ApprovalRequired {
		t.Fatalf("expected bootstrap scaffold to avoid approval, got %#v", contract.WorkItems[1])
	}
	editItem := contract.WorkItems[2]
	if editItem.ID != "edit" {
		t.Fatalf("expected standard edit item after bootstrap, got %#v", editItem)
	}
	if len(editItem.DependsOn) != 2 || editItem.DependsOn[0] != "research" || editItem.DependsOn[1] != "bootstrap" {
		t.Fatalf("expected edit to depend on research and bootstrap, got %#v", editItem.DependsOn)
	}
	projectRequest, _ := editItem.Metadata["project_request"].(map[string]any)
	if asString(projectRequest["project_type"]) != "cli_simple" {
		t.Fatalf("expected greenfield project_type in metadata, got %#v", projectRequest)
	}
	if bootstrapWorkspace, ok := projectRequest["bootstrap_workspace"].(bool); !ok || !bootstrapWorkspace {
		t.Fatalf("expected bootstrap_workspace flag in project_request, got %#v", projectRequest)
	}
}

func TestGenerateObjectiveExecutionContractInsertsInitialAnalysisForStructuralObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Audit repo security",
		Objective:     "Audit the repository for dependency drift and hardcoded secrets before applying the fix.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) != 5 {
		t.Fatalf("expected research + analyze + edit + validate + review, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].Kind != domain.WorkItemKindAnalyze {
		t.Fatalf("expected analyze work item after research, got %#v", contract.WorkItems[1])
	}
	if asString(contract.WorkItems[1].ToolRequest["tool"]) != "code_analysis" {
		t.Fatalf("expected code_analysis tool, got %#v", contract.WorkItems[1].ToolRequest)
	}
	analysisTypes := anyStringSlice(contract.WorkItems[1].ToolRequest["analysis_types"])
	if len(analysisTypes) != 2 || analysisTypes[0] != "security" || analysisTypes[1] != "dependencies" {
		t.Fatalf("expected security/dependencies analysis, got %#v", contract.WorkItems[1].ToolRequest["analysis_types"])
	}
	editItem := contract.WorkItems[2]
	if len(editItem.DependsOn) != 2 || editItem.DependsOn[0] != "research" || editItem.DependsOn[1] != "analyze" {
		t.Fatalf("expected edit to depend on research and analyze, got %#v", editItem.DependsOn)
	}
}

func TestGenerateObjectiveExecutionContractInsertsBaselineValidationForBugfixObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Fix failing CLI regression",
		Objective:     "Fix the failing CLI regression and prove it with validation before and after the patch.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) < 5 {
		t.Fatalf("expected baseline validate work item, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].ID != "validate-baseline" || contract.WorkItems[1].Kind != domain.WorkItemKindValidate {
		t.Fatalf("expected validate-baseline as second item, got %#v", contract.WorkItems[1])
	}
	if got, ok := contract.WorkItems[1].Metadata["objective_baseline_validation"].(bool); !ok || !got {
		t.Fatalf("expected baseline validate metadata flag, got %#v", contract.WorkItems[1].Metadata)
	}
	editItem := contract.WorkItems[2]
	if len(editItem.DependsOn) < 2 || editItem.DependsOn[1] != "validate-baseline" {
		t.Fatalf("expected edit to depend on baseline validation, got %#v", editItem.DependsOn)
	}
}

func TestGenerateObjectiveExecutionContractUsesPlanningContextForInitialAnalysis(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	score := 0.93
	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:                       "Stabilize repo",
		Objective:                   "Stabilize the repo before editing the objective path.",
		WorkspaceRoot:               root,
		CreatedBy:                   "test",
		PlanningRetrievalArtifactID: "artifact-planning-1",
		PlanningContext: &domain.ContextPackage{
			AgentType:      string(domain.AgentTypePlanner),
			WorkspaceRoot:  &root,
			Query:          "stabilize repo",
			MemoryMode:     "auto",
			MemoryStrategy: "repo_specific_first",
			SourceRefs:     []string{"artifact:repair_plan:1"},
			Chunks: []domain.ContextChunk{
				{
					SourceRef:   "artifact:repair_plan:1",
					SourceType:  "artifact",
					SourceID:    "repair-plan-1",
					ContentText: "Previous objective required static security analysis before touching the auth files because a hardcoded secret was discovered.",
					Score:       &score,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if contract.WorkItems[1].Kind != domain.WorkItemKindAnalyze {
		t.Fatalf("expected planning context to trigger analyze work item, got %#v", contract.WorkItems)
	}
	editItem := contract.WorkItems[2]
	if len(editItem.DependsOn) != 2 || editItem.DependsOn[1] != "analyze" {
		t.Fatalf("expected edit to wait for analyze after planning-context trigger, got %#v", editItem.DependsOn)
	}
	precedents, _ := editItem.Metadata["planning_retrieval_precedents"].([]map[string]any)
	if len(precedents) == 0 || asString(precedents[0]["source_ref"]) != "artifact:repair_plan:1" {
		t.Fatalf("expected planning precedents in edit metadata, got %#v", editItem.Metadata["planning_retrieval_precedents"])
	}
	if _, ok := editItem.Metadata["context_package"].(*domain.ContextPackage); !ok {
		t.Fatalf("expected planning context package on edit metadata, got %#v", editItem.Metadata["context_package"])
	}
	if got := asString(editItem.Metadata["planning_retrieval_packet_artifact_id"]); got != "artifact-planning-1" {
		t.Fatalf("expected planning retrieval artifact id in metadata, got %#v", editItem.Metadata["planning_retrieval_packet_artifact_id"])
	}
}

func TestGenerateObjectiveExecutionContractInsertsInitialAnalysisForAmbiguousShortlist(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# planner-ambiguity\n\nThe objective behavior lives in src/switchboard.py.\nThe objective behavior also lives in src/feature_toggle.py.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "feature_toggle.py"), []byte("FLAG = False\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Disambiguate objective scope",
		Objective:     "Enable the objective behavior cleanly for the autonomous flow.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) < 5 {
		t.Fatalf("expected analyze to be inserted for ambiguous shortlist, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].Kind != domain.WorkItemKindAnalyze {
		t.Fatalf("expected analyze work item after research, got %#v", contract.WorkItems[1])
	}
	if !strings.Contains(contract.WorkItems[1].Description, "ambiguous between src/feature_toggle.py and src/switchboard.py") &&
		!strings.Contains(contract.WorkItems[1].Description, "ambiguous between src/switchboard.py and src/feature_toggle.py") {
		t.Fatalf("expected analyze description to mention ambiguous shortlist, got %q", contract.WorkItems[1].Description)
	}
	analysisTypes := anyStringSlice(contract.WorkItems[1].ToolRequest["analysis_types"])
	if len(analysisTypes) == 0 || analysisTypes[0] != "complexity" {
		t.Fatalf("expected complexity analysis for ambiguous shortlist, got %#v", contract.WorkItems[1].ToolRequest["analysis_types"])
	}
}

func TestGenerateObjectiveExecutionContractCarriesRetrievalCitationsIntoScopeHypotheses(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	score := 0.88
	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Harden auth service",
		Objective:     "Harden the auth flow before shipping.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
		PlanningContext: &domain.ContextPackage{
			AgentType:      string(domain.AgentTypePlanner),
			WorkspaceRoot:  &root,
			Query:          "harden auth flow",
			MemoryMode:     "auto",
			MemoryStrategy: "repo_specific_first",
			SourceRefs:     []string{"artifact:repair_plan:7"},
			Chunks: []domain.ContextChunk{{
				SourceRef:   "artifact:repair_plan:7",
				SourceType:  "artifact",
				SourceID:    "repair-plan-7",
				ContentText: "Previous auth_service.go repair narrowed the patch to src/auth_service.go before validation.",
				Score:       &score,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.InitialScopeHypotheses) == 0 {
		t.Fatalf("expected hypotheses, got %#v", contract)
	}
	first := contract.InitialScopeHypotheses[0]
	if first.Path != "src/auth_service.go" {
		t.Fatalf("expected auth_service.go first, got %#v", contract.InitialScopeHypotheses)
	}
	if len(first.CitationRefs) == 0 || first.CitationRefs[0] != "artifact:repair_plan:7" {
		t.Fatalf("expected retrieval citation refs on hypothesis, got %#v", first)
	}
	if !strings.Contains(first.Rationale, "retrieved precedent supports this path") {
		t.Fatalf("expected rationale to mention retrieval precedent, got %#v", first)
	}
	if !strings.Contains(first.Evidence, "auth_service.go") {
		t.Fatalf("expected evidence to cite precedent summary, got %#v", first)
	}
}

func TestGenerateObjectiveExecutionContractAddsDocumentationWorkstreamForMixedObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Fix auth flow and docs",
		Objective:     "Fix src/auth_service.go and update the README documentation so the operator guidance stays accurate.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) != 5 {
		t.Fatalf("expected research + edit + edit-docs + validate + review, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].ID != "edit" || contract.WorkItems[1].Kind != domain.WorkItemKindEdit {
		t.Fatalf("expected primary edit as second item, got %#v", contract.WorkItems[1])
	}
	if contract.WorkItems[2].ID != "edit-docs" || contract.WorkItems[2].Kind != domain.WorkItemKindEdit {
		t.Fatalf("expected documentation sync edit as third item, got %#v", contract.WorkItems[2])
	}
	if len(contract.WorkItems[2].DependsOn) < 2 || contract.WorkItems[2].DependsOn[len(contract.WorkItems[2].DependsOn)-1] != "edit" {
		t.Fatalf("expected docs edit to depend on primary edit, got %#v", contract.WorkItems[2].DependsOn)
	}
	if len(contract.WorkItems[2].SuspectedPaths) == 0 || contract.WorkItems[2].SuspectedPaths[0] != "README.md" {
		t.Fatalf("expected docs edit to target README.md, got %#v", contract.WorkItems[2].SuspectedPaths)
	}
	if got := asString(contract.WorkItems[2].Metadata["objective_workstream"]); got != "documentation_sync" {
		t.Fatalf("expected documentation workstream metadata, got %#v", contract.WorkItems[2].Metadata)
	}
	if contract.WorkItems[3].ID != "validate" || len(contract.WorkItems[3].DependsOn) != 1 || contract.WorkItems[3].DependsOn[0] != "edit-docs" {
		t.Fatalf("expected validate to depend on docs edit, got %#v", contract.WorkItems[3])
	}
}

func TestGenerateObjectiveExecutionContractAddsConfigWorkstreamForMixedObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services:\n  app:\n    build: .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Fix auth flow and deployment config",
		Objective:     "Fix src/auth_service.go and update the Dockerfile plus docker-compose.yml deployment configuration so the service still boots correctly.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) != 5 {
		t.Fatalf("expected research + edit + edit-config + validate + review, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].ID != "edit" || contract.WorkItems[1].Kind != domain.WorkItemKindEdit {
		t.Fatalf("expected primary edit as second item, got %#v", contract.WorkItems[1])
	}
	if contract.WorkItems[2].ID != "edit-config" || contract.WorkItems[2].Kind != domain.WorkItemKindEdit {
		t.Fatalf("expected config sync edit as third item, got %#v", contract.WorkItems[2])
	}
	if len(contract.WorkItems[2].DependsOn) < 2 || contract.WorkItems[2].DependsOn[len(contract.WorkItems[2].DependsOn)-1] != "edit" {
		t.Fatalf("expected config edit to depend on primary edit, got %#v", contract.WorkItems[2].DependsOn)
	}
	if len(contract.WorkItems[2].SuspectedPaths) < 2 || contract.WorkItems[2].SuspectedPaths[0] != "Dockerfile" {
		t.Fatalf("expected config edit to target infra paths, got %#v", contract.WorkItems[2].SuspectedPaths)
	}
	if got := asString(contract.WorkItems[2].Metadata["objective_workstream"]); got != "config_sync" {
		t.Fatalf("expected config workstream metadata, got %#v", contract.WorkItems[2].Metadata)
	}
	if contract.WorkItems[3].ID != "validate" || len(contract.WorkItems[3].DependsOn) != 1 || contract.WorkItems[3].DependsOn[0] != "edit-config" {
		t.Fatalf("expected validate to depend on config edit, got %#v", contract.WorkItems[3])
	}
}

func TestGenerateObjectiveExecutionContractAddsDependencyWorkstreamForMixedObjective(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{\"name\":\"demo\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("{\"lockfileVersion\":3}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "auth_service.ts"), []byte("export const ready = true;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	contract, err := svc.GenerateObjectiveExecutionContract(context.Background(), ObjectiveInput{
		Title:         "Fix auth flow and dependency manifests",
		Objective:     "Fix src/auth_service.ts and update package.json plus package-lock.json so the dependency upgrade lands cleanly.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.WorkItems) != 6 {
		t.Fatalf("expected research + analyze + edit + edit-deps + validate + review, got %#v", contract.WorkItems)
	}
	if contract.WorkItems[1].ID != "analyze" || contract.WorkItems[1].Kind != domain.WorkItemKindAnalyze {
		t.Fatalf("expected analyze as second item, got %#v", contract.WorkItems[1])
	}
	if contract.WorkItems[3].ID != "edit-deps" || contract.WorkItems[3].Kind != domain.WorkItemKindEdit {
		t.Fatalf("expected dependency sync edit as fourth item, got %#v", contract.WorkItems[3])
	}
	if got := asString(contract.WorkItems[3].Metadata["objective_workstream"]); got != "dependency_sync" {
		t.Fatalf("expected dependency workstream metadata, got %#v", contract.WorkItems[3].Metadata)
	}
	if len(contract.WorkItems[3].DependsOn) < 2 || contract.WorkItems[3].DependsOn[len(contract.WorkItems[3].DependsOn)-1] != "edit" {
		t.Fatalf("expected dependency edit to depend on primary edit, got %#v", contract.WorkItems[3].DependsOn)
	}
	if len(contract.WorkItems[3].SuspectedPaths) < 2 || contract.WorkItems[3].SuspectedPaths[0] != "package-lock.json" && contract.WorkItems[3].SuspectedPaths[0] != "package.json" {
		t.Fatalf("expected dependency edit to target dependency manifests, got %#v", contract.WorkItems[3].SuspectedPaths)
	}
	if contract.WorkItems[4].ID != "validate" || len(contract.WorkItems[4].DependsOn) != 1 || contract.WorkItems[4].DependsOn[0] != "edit-deps" {
		t.Fatalf("expected validate to depend on dependency edit, got %#v", contract.WorkItems[4])
	}
}

func TestGenerateExecutionPlanUsesRepoWorkflowForBenchmarkTitleWithoutGitVisibility(t *testing.T) {
	svc := New(config.Config{}, nil)
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, "bench-repo")
	if err := os.MkdirAll(filepath.Join(workspaceRoot, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"id":               "bench-case-2",
		"case_type":        "deterministic_bugfix",
		"repo_profile":     "benchmark_repo_workflow",
		"repo_url":         "https://github.com/example/demo",
		"default_branch":   "main",
		"project_type":     "existing_repo",
		"runtime_or_stack": "python",
		"project_root":     ".",
		"coder_tool":       "write_file",
		"patch_target":     ".lab/repo-workflow-marker.txt",
		"write_content":    "benchmark marker\n",
		"coder_summary":    "Apply the benchmark-defined deterministic repo change.",
	})
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".lab", "benchmark-case.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	item := &domain.InitiativeResponse{
		ID:            "initiative-5",
		Title:         "Benchmark demo-case",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1 for benchmark title, got %q", got)
	}
}

func TestGenerateExecutionPlanUsesEmbeddedBenchmarkCaseWhenWorkspaceFileIsInvisible(t *testing.T) {
	svc := New(config.Config{}, nil)
	workspaceRoot := "/tmp/private-benchmark-workspace/repo"
	casePayload := map[string]any{
		"id":               "bench-case-inline",
		"case_type":        "review_only",
		"repo_profile":     "go_cli_tui",
		"repo_url":         "https://github.com/charmbracelet/glow",
		"default_branch":   "main",
		"project_type":     "existing_repo",
		"runtime_or_stack": "go",
		"project_root":     ".",
		"test_focus":       "rendering review",
		"test_command":     []string{"docker", "run", "--rm", "golang:1.25"},
		"expected_files":   []string{"main.go", "glow_test.go"},
		"coder_tool":       "write_file",
		"patch_target":     ".lab/repo-workflow-marker.txt",
		"write_content":    "benchmark marker\n",
		"coder_summary":    "Write a deterministic benchmark marker file before repository review.",
	}
	raw, _ := json.Marshal(casePayload)
	item := &domain.InitiativeResponse{
		ID:            "initiative-6",
		Title:         "Benchmark glow-render-review",
		WorkspaceRoot: workspaceRoot,
		Goal:          "Run deterministic benchmark workflow.\n\n[BENCHMARK_CASE_JSON]\n" + string(raw) + "\n[/BENCHMARK_CASE_JSON]",
	}
	artifacts, err := svc.GenerateExecutionPlan(context.Background(), item, map[string]any{
		"architecture": "Go orchestrator plus local bridge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := asString(artifacts.JSON["project_flow"]); got != "repo_workflow_v1" {
		t.Fatalf("expected repo_workflow_v1 for embedded benchmark case, got %q", got)
	}
	epics := artifacts.JSON["epics"].([]map[string]any)
	coderTask := epics[1]["tasks"].([]map[string]any)[0]
	metadata := coderTask["metadata"].(map[string]any)
	projectRequest := metadata["project_request"].(map[string]any)
	if got := asString(projectRequest["repo_profile"]); got != "go_cli_tui" {
		t.Fatalf("expected repo_profile from embedded benchmark case, got %q", got)
	}
	if got := asString(projectRequest["repository_url"]); got != "https://github.com/charmbracelet/glow" {
		t.Fatalf("expected repository_url from embedded benchmark case, got %q", got)
	}
}

func TestValidateContracts(t *testing.T) {
	if err := ValidateRequirementsPayload(map[string]any{
		"objective":           "Ship something",
		"scope":               []any{"one"},
		"out_of_scope":        []any{"two"},
		"constraints":         []any{"three"},
		"risks":               []any{"four"},
		"acceptance_criteria": []any{"five"},
		"open_questions":      []any{"six"},
		"assumptions":         []any{"seven"},
	}); err != nil {
		t.Fatalf("requirements contract should validate: %v", err)
	}
	if err := ValidateDesignPayload(map[string]any{
		"architecture":      "Go control plane",
		"components":        []any{"api"},
		"interfaces":        []any{"POST /initiatives"},
		"data_model":        map[string]any{"initiative": []any{"status"}},
		"testing_strategy":  []any{"integration"},
		"technical_risks":   []any{"drift"},
		"pending_decisions": []any{"none"},
	}); err != nil {
		t.Fatalf("design contract should validate: %v", err)
	}
	if err := ValidateExecutionPlanPayload(map[string]any{
		"epics": []any{
			map[string]any{
				"name": "Delivery",
				"tasks": []any{
					map[string]any{
						"title":              "Create scaffold",
						"description":        "Build the initial project",
						"suggested_agent":    "coder",
						"priority":           "normal",
						"execution_mode":     "agent_local",
						"execution_target":   "local",
						"definition_of_done": "Files exist",
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("execution plan contract should validate: %v", err)
	}
}

func TestResolveExecutionPolicyAndModeValidation(t *testing.T) {
	policy := ResolveExecutionPolicy("/srv/ai-lab/orchestrator/workspaces", "/tmp/demo")
	if policy.Scope != "local_bridge" {
		t.Fatalf("expected local_bridge scope, got %q", policy.Scope)
	}
	task := domain.InitiativeTaskLinkResponse{
		TaskID:        "task-1",
		ExecutionMode: domain.TaskLaunchModeAgentLocal,
		Task: domain.TaskResponse{
			Metadata: map[string]any{},
		},
	}
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentLocal); err != nil {
		t.Fatalf("agent_local should be allowed: %v", err)
	}
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentRemote); err == nil {
		t.Fatal("agent_remote should be blocked for local_bridge scope")
	}
	task.Task.Metadata["approval_required"] = true
	if err := ValidateTaskLaunchAgainstPolicy(policy, task, domain.TaskLaunchModeAgentLocal); err != nil {
		t.Fatalf("approval-gated local task should still launch and request execution approval later: %v", err)
	}
}

func TestResearchNotesSkipsNonResearchGoals(t *testing.T) {
	svc := New(config.Config{}, research.New(research.Options{}))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if got := svc.researchNotes(ctx, "Validate initiative hardening with local execution"); got != "" {
		t.Fatalf("expected no research notes for non-research goal, got %q", got)
	}
}

func anyStringSlice(value any) []string {
	switch raw := value.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
