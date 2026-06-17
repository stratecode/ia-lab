package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestShouldStartObjectiveRepairCycleForReviewChangesRequested(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "error",
		ReviewDecision: stringPtr("changes_requested"),
	}
	if !shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected review changes_requested to trigger repair cycle")
	}
}

func TestBuildObjectivePlanningContextCreatesCompactContextPackage(t *testing.T) {
	server := &Server{}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	readme := "# smartfm api\n\nThis repo fixes auth regressions and validates CLI/API behavior through focused tests.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "auth_service.go"), []byte("package internal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	initiative := &domain.InitiativeResponse{
		ID:            "initiative-ctx-1",
		Title:         "Fix auth regression",
		WorkspaceRoot: root,
		Goal:          "Fix the auth regression with minimal scope.",
	}
	body := domain.ObjectiveRequest{
		Title:         "Fix auth regression",
		Objective:     "Fix the auth regression and prove it with tests.",
		WorkspaceRoot: root,
		CreatedBy:     "test",
	}

	pkg, err := server.buildObjectivePlanningContext(context.Background(), initiative, body)
	if err != nil {
		t.Fatal(err)
	}
	if pkg == nil {
		t.Fatal("expected context package")
	}
	if pkg.MemoryMode != "auto" {
		t.Fatalf("expected auto memory mode, got %q", pkg.MemoryMode)
	}
	if pkg.MemoryStrategy != "repo_specific_first" {
		t.Fatalf("expected repo_specific_first strategy, got %q", pkg.MemoryStrategy)
	}
	if pkg.TotalChars <= 0 {
		t.Fatalf("expected compact context chars, got %d", pkg.TotalChars)
	}
	if pkg.TotalChars > 2400 {
		t.Fatalf("expected planning context budget <= 2400 chars, got %d", pkg.TotalChars)
	}
	if len(pkg.Chunks) == 0 {
		t.Fatalf("expected compact retrieval chunks, got %#v", pkg)
	}
	if len(pkg.PromptSection) == 0 {
		t.Fatalf("expected prompt section, got %#v", pkg)
	}
	if len(pkg.WhyThesePrecedents) == 0 {
		t.Fatalf("expected why-these-precedents trace, got %#v", pkg)
	}
	foundRepoSpecific := false
	for _, chunk := range pkg.Chunks {
		if strings.TrimSpace(chunk.MemoryClass) == "repo_specific" {
			foundRepoSpecific = true
		}
		if len(chunk.ContentText) > 220 {
			t.Fatalf("expected compact chunk summary, got %d chars", len(chunk.ContentText))
		}
	}
	if !foundRepoSpecific {
		t.Fatalf("expected at least one repo_specific chunk, got %#v", pkg.Chunks)
	}
}

func TestShouldStartObjectiveRepairCycleForValidateFailure(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-validate",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindValidate),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{Status: "error"}
	if !shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected validation failure to trigger repair cycle")
	}
}

func TestShouldStartObjectiveRepairCycleStopsAtMaxIterations(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-review",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      3,
			"objective_max_iterations": 3,
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:         "error",
		ReviewDecision: stringPtr("changes_requested"),
	}
	if shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected repair cycle to stop at max iterations")
	}
}

func TestShouldStartObjectiveRepairCycleStopsWhenTimeBudgetIsExhausted(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	task := &domain.TaskResponse{
		ID:            "task-validate",
		AssignedAgent: &reviewer,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"objective_entrypoint":          true,
			"work_item_kind":                string(domain.WorkItemKindValidate),
			"objective_iteration":           1,
			"objective_max_iterations":      3,
			"objective_time_budget_seconds": 1,
			"objective_started_at":          time.Now().Add(-3 * time.Second).UTC().Format(time.RFC3339Nano),
			"objective_deadline_at":         time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano),
		},
	}
	result := domain.LocalBridgeResultRequest{Status: "error"}
	if shouldStartObjectiveRepairCycle(task, result) {
		t.Fatalf("expected repair cycle to stop when time budget is exhausted")
	}
}

func TestBuildObjectiveRepairCycleCreatesSequentialWorkItems(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-review-1",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":           "/tmp/repo",
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"project_request": map[string]any{
				"project_name": "demo-repo",
				"project_root": ".",
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"internal/orchestratorgo/httpapi/objectives.go"},
		},
	}
	contract := domain.ExecutionContract{
		Title:               "Fix demo repo",
		NormalizedObjective: "Fix the failing objective loop",
		WorkspaceRoot:       "/tmp/repo",
		ExecutionBackend:    "aider-task",
		ValidationCommands:  []string{"go", "test", "./..."},
		SuspectedPaths:      []string{"internal/orchestratorgo/httpapi/objectives.go"},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		Summary:      stringPtr("Review requested changes"),
		ErrorMessage: stringPtr("tests still fail"),
		ChangedFiles: []string{"internal/orchestratorgo/httpapi/objectives.go"},
		Findings: []map[string]any{
			{
				"kind":     "validation_failure",
				"severity": "high",
				"message":  "Validation still fails in internal/orchestratorgo/httpapi/objectives.go.",
			},
			{
				"kind":               "planner_scope_contradicted",
				"severity":           "medium",
				"message":            "The observed diff contradicts the planner's initial scope hypothesis.",
				"recommended_action": "Update the next plan using the files actually changed.",
			},
		},
		ReviewComments: []map[string]any{
			{"severity": "high", "message": "Fix the validation failure before approval."},
		},
		ReviewDecision: stringPtr("changes_requested"),
	}

	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(workItems) != 6 {
		t.Fatalf("expected 6 repair work items with baseline+analysis, got %d", len(workItems))
	}
	if workItems[0].Kind != domain.WorkItemKindReplan || workItems[0].ID != "replan-2" {
		t.Fatalf("unexpected replan item: %#v", workItems[0])
	}
	if workItems[1].Kind != domain.WorkItemKindValidate || workItems[1].ID != "validate-baseline-2" {
		t.Fatalf("expected repair baseline validate item, got %#v", workItems[1])
	}
	if got, ok := workItems[1].Metadata["objective_baseline_validation"].(bool); !ok || !got {
		t.Fatalf("expected baseline metadata on repair validate item, got %#v", workItems[1].Metadata)
	}
	if workItems[2].Kind != domain.WorkItemKindAnalyze || workItems[2].ID != "analyze-2" {
		t.Fatalf("expected analyze item after repair baseline, got %#v", workItems[2])
	}
	if len(workItems[2].DependsOn) != 1 || workItems[2].DependsOn[0] != "replan-2" {
		t.Fatalf("unexpected analyze dependencies: %#v", workItems[2].DependsOn)
	}
	if workItems[3].Kind != domain.WorkItemKindEdit || workItems[3].ID != "edit-2" {
		t.Fatalf("unexpected edit item: %#v", workItems[3])
	}
	if len(workItems[3].DependsOn) != 3 || workItems[3].DependsOn[0] != "replan-2" || workItems[3].DependsOn[1] != "validate-baseline-2" || workItems[3].DependsOn[2] != "analyze-2" {
		t.Fatalf("unexpected edit dependencies: %#v", workItems[3].DependsOn)
	}
	if tool := strings.TrimSpace(asString(workItems[3].ToolRequest["tool"])); tool != "run_command" {
		t.Fatalf("expected edit tool run_command, got %#v", workItems[3].ToolRequest)
	}
	argv := anyStringSliceHTTPDefault(workItems[3].ToolRequest["argv"], nil)
	if len(argv) == 0 || argv[0] != "aider-task" {
		t.Fatalf("expected aider-task invocation, got %#v", argv)
	}
	if !strings.Contains(workItems[3].Description, "tests still fail") {
		t.Fatalf("expected edit description to include repair feedback, got %q", workItems[3].Description)
	}
	if len(workItems[3].SuspectedPaths) != 1 || workItems[3].SuspectedPaths[0] != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected suspected paths narrowed to changed files, got %#v", workItems[3].SuspectedPaths)
	}
	intent, _ := workItems[3].Metadata["objective_patch_intent"].([]map[string]any)
	if len(intent) == 0 {
		t.Fatalf("expected objective_patch_intent metadata, got %#v", workItems[3].Metadata)
	}
	if asString(intent[0]["path"]) != "internal/orchestratorgo/httpapi/objectives.go" {
		t.Fatalf("expected patch intent to target changed file, got %#v", intent[0])
	}
	hypotheses, _ := workItems[3].Metadata["objective_failure_hypotheses"].([]map[string]any)
	if len(hypotheses) == 0 {
		t.Fatalf("expected objective_failure_hypotheses metadata, got %#v", workItems[3].Metadata)
	}
	if asString(hypotheses[0]["failure_mode"]) != "validation_failure" {
		t.Fatalf("expected validation_failure hypothesis, got %#v", hypotheses[0])
	}
	priorFindings := anyMapSliceHTTPDefault(workItems[0].ToolRequest["prior_findings"], nil)
	if len(priorFindings) < 2 {
		t.Fatalf("expected repair cycle to carry review findings forward, got %#v", workItems[0].ToolRequest["prior_findings"])
	}
	if asString(priorFindings[1]["kind"]) != "planner_scope_contradicted" {
		t.Fatalf("expected planner contradiction finding to propagate, got %#v", priorFindings[1])
	}
	if got := objectiveIteration(workItems[5].Metadata); got != 2 {
		t.Fatalf("expected iteration metadata to advance to 2, got %d", got)
	}
	if reviewTool := strings.TrimSpace(asString(workItems[5].ToolRequest["tool"])); reviewTool != "review_workspace" {
		t.Fatalf("expected review_workspace tool, got %#v", workItems[5].ToolRequest)
	}
}

func TestBuildObjectiveRepairCycleInsertsAnalysisForRejectedScopeContradiction(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-review-1",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-1"),
		Metadata: map[string]any{
			"workspace_root":           "/tmp/repo",
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"project_request": map[string]any{
				"project_name": "demo-repo",
				"project_root": ".",
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"internal/orchestratorgo/httpapi/objectives.go"},
		},
	}
	contract := domain.ExecutionContract{
		Title:               "Fix demo repo",
		NormalizedObjective: "Fix the failing objective loop",
		WorkspaceRoot:       "/tmp/repo",
		ExecutionBackend:    "aider-task",
		ValidationCommands:  []string{"go", "test", "./..."},
		SuspectedPaths:      []string{"internal/orchestratorgo/httpapi/objectives.go"},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		ChangedFiles: []string{"internal/orchestratorgo/httpapi/legacy_objectives.go"},
		Findings: []map[string]any{
			{
				"kind":     "planner_rejected_scope_contradicted",
				"severity": "high",
				"message":  "The edit landed in a path the planner had rejected.",
				"path":     "internal/orchestratorgo/httpapi/legacy_objectives.go",
			},
		},
		ReviewComments: []map[string]any{
			{"severity": "high", "message": "Do not keep editing the rejected legacy path blindly."},
		},
		ReviewDecision: stringPtr("changes_requested"),
	}

	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(workItems) != 6 {
		t.Fatalf("expected 6 repair work items with baseline+analysis step, got %d", len(workItems))
	}
	if workItems[1].Kind != domain.WorkItemKindValidate || workItems[1].ID != "validate-baseline-2" {
		t.Fatalf("expected baseline validate work item in slot 2, got %#v", workItems[1])
	}
	if workItems[2].Kind != domain.WorkItemKindAnalyze || workItems[2].ID != "analyze-2" {
		t.Fatalf("expected analyze work item in slot 3, got %#v", workItems[2])
	}
	if tool := strings.TrimSpace(asString(workItems[2].ToolRequest["tool"])); tool != "code_analysis" {
		t.Fatalf("expected code_analysis tool, got %#v", workItems[2].ToolRequest)
	}
	analysisTypes := anyStringSliceHTTPDefault(workItems[2].ToolRequest["analysis_types"], nil)
	if len(analysisTypes) == 0 || analysisTypes[0] != "complexity" {
		t.Fatalf("expected structural analysis types, got %#v", workItems[2].ToolRequest["analysis_types"])
	}
	if workItems[3].Kind != domain.WorkItemKindEdit || workItems[3].ID != "edit-2" {
		t.Fatalf("expected edit after analyze, got %#v", workItems[3])
	}
	if len(workItems[3].DependsOn) != 3 || workItems[3].DependsOn[0] != "replan-2" || workItems[3].DependsOn[1] != "validate-baseline-2" || workItems[3].DependsOn[2] != "analyze-2" {
		t.Fatalf("expected edit to depend on replan, baseline validate and analyze, got %#v", workItems[3].DependsOn)
	}
}

func TestBuildObjectiveRepairCycleInsertsBaselineForRetrievalContradictionWithoutAnalysis(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-review-2",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-2"),
		Metadata: map[string]any{
			"workspace_root":           "/tmp/repo",
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"project_request": map[string]any{
				"project_name": "demo-repo",
				"project_root": ".",
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"README.md"},
		},
	}
	contract := domain.ExecutionContract{
		Title:               "Fix demo repo",
		NormalizedObjective: "Repair retrieval drift",
		WorkspaceRoot:       "/tmp/repo",
		ExecutionBackend:    "aider-task",
		ValidationCommands:  []string{"go", "test", "./..."},
		SuspectedPaths:      []string{"README.md"},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		ChangedFiles: []string{"actual.py"},
		Findings: []map[string]any{
			{
				"kind":     "retrieval_scope_contradicted",
				"severity": "medium",
				"message":  "The observed diff does not align with the retrieved precedents that were carried into the edit.",
			},
		},
		ReviewDecision: stringPtr("changes_requested"),
	}

	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(workItems) != 6 {
		t.Fatalf("expected baseline+analysis repair graph, got %d items", len(workItems))
	}
	if workItems[1].ID != "validate-baseline-2" || workItems[1].Kind != domain.WorkItemKindValidate {
		t.Fatalf("expected repair baseline validate item, got %#v", workItems[1])
	}
	if !strings.Contains(workItems[1].Description, "retrieved precedent") {
		t.Fatalf("expected retrieval contradiction rationale in baseline validate description, got %q", workItems[1].Description)
	}
}

func TestBuildObjectiveRepairCycleCarriesSecondaryEditWorkstreams(t *testing.T) {
	coder := domain.AgentTypeCoder
	task := &domain.TaskResponse{
		ID:            "task-review-mixed-1",
		AssignedAgent: &coder,
		InitiativeID:  stringPtr("initiative-mixed-1"),
		Metadata: map[string]any{
			"workspace_root":           "/tmp/repo",
			"objective_entrypoint":     true,
			"work_item_kind":           string(domain.WorkItemKindReview),
			"objective_iteration":      1,
			"objective_max_iterations": 3,
			"project_request": map[string]any{
				"project_name": "demo-repo",
				"project_root": ".",
			},
			"validation_commands": []string{"go", "test", "./..."},
			"suspected_paths":     []string{"src/auth_service.go"},
		},
	}
	contract := domain.ExecutionContract{
		Title:               "Fix mixed objective",
		NormalizedObjective: "Fix src/auth_service.go and keep docs plus deployment config aligned.",
		WorkspaceRoot:       "/tmp/repo",
		ExecutionBackend:    "aider-task",
		ValidationCommands:  []string{"go", "test", "./..."},
		SuspectedPaths:      []string{"src/auth_service.go"},
		WorkItems: []domain.WorkItem{
			{
				ID:             "edit",
				Kind:           domain.WorkItemKindEdit,
				SuspectedPaths: []string{"src/auth_service.go"},
			},
			{
				ID:               "edit-docs",
				Kind:             domain.WorkItemKindEdit,
				Title:            "Synchronize documentation with the code change",
				Description:      "Update the documentation after the code patch.",
				DefinitionOfDone: "Docs reflect the implementation change.",
				SuspectedPaths:   []string{"README.md"},
				Metadata:         map[string]any{"objective_workstream": "documentation_sync"},
			},
			{
				ID:               "edit-config",
				Kind:             domain.WorkItemKindEdit,
				Title:            "Synchronize configuration and infra with the code change",
				Description:      "Update config after the code patch.",
				DefinitionOfDone: "Config reflects the implementation change.",
				SuspectedPaths:   []string{"Dockerfile", "docker-compose.yml"},
				Metadata:         map[string]any{"objective_workstream": "config_sync"},
			},
			{
				ID:               "edit-deps",
				Kind:             domain.WorkItemKindEdit,
				Title:            "Synchronize dependency manifests and lockfiles with the code change",
				Description:      "Update dependency manifests after the code patch.",
				DefinitionOfDone: "Dependency manifests reflect the implementation change.",
				SuspectedPaths:   []string{"package.json", "package-lock.json"},
				Metadata:         map[string]any{"objective_workstream": "dependency_sync"},
			},
		},
	}
	result := domain.LocalBridgeResultRequest{
		Status:       "error",
		Summary:      stringPtr("Review requested changes"),
		ErrorMessage: stringPtr("tests still fail"),
		ChangedFiles: []string{"src/auth_service.go"},
		Findings: []map[string]any{
			{
				"kind":     "validation_failure",
				"severity": "high",
				"message":  "Validation still fails in src/auth_service.go.",
			},
		},
		ReviewDecision: stringPtr("changes_requested"),
	}

	workItems, err := buildObjectiveRepairCycle(contract, task, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(workItems) != 7 {
		t.Fatalf("expected replan + edit + docs + config + deps + validate + review, got %d items: %#v", len(workItems), workItems)
	}
	if workItems[2].ID != "edit-docs-2" || strings.TrimSpace(asString(workItems[2].Metadata["objective_workstream"])) != "documentation_sync" {
		t.Fatalf("expected documentation repair workstream in slot 3, got %#v", workItems[2])
	}
	if len(workItems[2].DependsOn) != 1 || workItems[2].DependsOn[0] != "edit-2" {
		t.Fatalf("expected edit-docs-2 to depend on primary edit, got %#v", workItems[2].DependsOn)
	}
	if workItems[3].ID != "edit-config-2" || strings.TrimSpace(asString(workItems[3].Metadata["objective_workstream"])) != "config_sync" {
		t.Fatalf("expected config repair workstream in slot 4, got %#v", workItems[3])
	}
	if len(workItems[3].DependsOn) != 1 || workItems[3].DependsOn[0] != "edit-docs-2" {
		t.Fatalf("expected edit-config-2 to depend on edit-docs-2, got %#v", workItems[3].DependsOn)
	}
	if workItems[4].ID != "edit-deps-2" || strings.TrimSpace(asString(workItems[4].Metadata["objective_workstream"])) != "dependency_sync" {
		t.Fatalf("expected dependency repair workstream in slot 5, got %#v", workItems[4])
	}
	if len(workItems[4].DependsOn) != 1 || workItems[4].DependsOn[0] != "edit-config-2" {
		t.Fatalf("expected edit-deps-2 to depend on edit-config-2, got %#v", workItems[4].DependsOn)
	}
	if workItems[5].ID != "validate-2" || len(workItems[5].DependsOn) != 1 || workItems[5].DependsOn[0] != "edit-deps-2" {
		t.Fatalf("expected validate-2 to depend on final dependency edit, got %#v", workItems[5])
	}
	validatePaths := workItems[5].SuspectedPaths
	for _, required := range []string{"src/auth_service.go", "README.md", "Dockerfile", "docker-compose.yml", "package.json", "package-lock.json"} {
		if !containsString(validatePaths, required) {
			t.Fatalf("expected validate scope to include %s, got %#v", required, validatePaths)
		}
	}
}

func TestObjectiveQueueMetadataPatchUsesResearchOutputForInitialEdit(t *testing.T) {
	researcher := domain.AgentTypeResearcher
	editTask := &domain.TaskResponse{
		ID: "task-edit-1",
		Metadata: map[string]any{
			"work_item_kind": string(domain.WorkItemKindEdit),
			"depends_on":     []string{"research"},
			"project_request": map[string]any{
				"project_name":   "demo-repo",
				"expected_files": []string{"README.md", "PASS"},
			},
		},
	}
	researchPayload := `{
  "recommended_scope_paths": ["src/feature_flag.py", "README.md"],
  "planner_scope_resolution": "contradicted",
  "planner_rejected_scope_resolution": "confirmed",
  "scope_correction_reason": "drop the contradicted shortlist and keep the rejected competitor excluded",
  "excluded_scope_paths": ["src/legacy_flag.py"],
  "retrieval_precedents": [{"source_ref":"artifact:repair_plan:1","summary":"Previous repair kept the patch narrow and passed."}],
  "failure_hypotheses": [{"path":"src/feature_flag.py","failure_mode":"missing_behavior","priority":"medium"}],
  "patch_intent": [{"path":"src/feature_flag.py","action":"update implementation","rationale":"objective names the file directly"}],
  "next_edit_brief": "Focus the first edit on src/feature_flag.py before touching validation artifacts."
}`
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{
		"research": {
			Task: domain.TaskResponse{
				ID:            "task-research-1",
				State:         domain.TaskStateCompleted,
				AssignedAgent: &researcher,
				Metadata: map[string]any{
					"work_item_kind": string(domain.WorkItemKindResearch),
				},
				Results: map[string]any{
					"stdout": researchPayload,
				},
			},
		},
	}

	patch := objectiveQueueMetadataPatch(editTask, byWorkItemID)
	if len(patch) == 0 {
		t.Fatalf("expected non-empty metadata patch")
	}
	scope := anyStringSliceHTTPDefault(patch["suspected_paths"], nil)
	if len(scope) == 0 || scope[0] != "src/feature_flag.py" {
		t.Fatalf("expected research output to lead suspected_paths, got %#v", patch["suspected_paths"])
	}
	if containsString(scope, "PASS") {
		t.Fatalf("expected validation artifact path to be filtered from suspected_paths, got %#v", scope)
	}
	projectRequest, _ := patch["project_request"].(map[string]any)
	expectedFiles := anyStringSliceHTTPDefault(projectRequest["expected_files"], nil)
	if len(expectedFiles) == 0 || expectedFiles[0] != "src/feature_flag.py" {
		t.Fatalf("expected patched project_request expected_files, got %#v", projectRequest)
	}
	if !strings.Contains(asString(patch["objective_research_brief"]), "src/feature_flag.py") {
		t.Fatalf("expected research brief in patch, got %#v", patch)
	}
	intent := anyMapSliceHTTPDefault(patch["objective_patch_intent"], nil)
	if len(intent) == 0 || asString(intent[0]["path"]) != "src/feature_flag.py" {
		t.Fatalf("expected patch intent from research output, got %#v", patch["objective_patch_intent"])
	}
	excluded := anyStringSliceHTTPDefault(patch["excluded_scope_paths"], nil)
	if len(excluded) == 0 || excluded[0] != "src/legacy_flag.py" {
		t.Fatalf("expected excluded scope paths from research output, got %#v", patch["excluded_scope_paths"])
	}
	if asString(patch["planner_scope_resolution"]) != "contradicted" {
		t.Fatalf("expected planner scope resolution in patch, got %#v", patch["planner_scope_resolution"])
	}
	if asString(patch["planner_rejected_scope_resolution"]) != "confirmed" {
		t.Fatalf("expected rejected planner scope resolution in patch, got %#v", patch["planner_rejected_scope_resolution"])
	}
	if !strings.Contains(asString(patch["scope_correction_reason"]), "rejected competitor") {
		t.Fatalf("expected scope correction reason in patch, got %#v", patch["scope_correction_reason"])
	}
	precedents := anyMapSliceHTTPDefault(patch["objective_retrieval_precedents"], nil)
	if len(precedents) == 0 || asString(precedents[0]["source_ref"]) != "artifact:repair_plan:1" {
		t.Fatalf("expected retrieval precedents from research output, got %#v", patch["objective_retrieval_precedents"])
	}
}

func TestObjectiveQueueMetadataPatchPreservesDocumentationWorkstreamPriority(t *testing.T) {
	researcher := domain.AgentTypeResearcher
	editTask := &domain.TaskResponse{
		ID: "task-edit-docs-1",
		Metadata: map[string]any{
			"work_item_kind":       string(domain.WorkItemKindEdit),
			"objective_workstream": "documentation_sync",
			"depends_on":           []string{"research", "edit"},
			"suspected_paths":      []string{"README.md"},
			"project_request": map[string]any{
				"project_name":   "demo-repo",
				"expected_files": []string{"README.md"},
			},
		},
	}
	researchPayload := `{
  "recommended_scope_paths": ["src/auth_service.go", "PASS", "README.md"],
  "next_edit_brief": "Sync docs after the code patch."
}`
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{
		"research": {
			Task: domain.TaskResponse{
				ID:            "task-research-1",
				State:         domain.TaskStateCompleted,
				AssignedAgent: &researcher,
				Metadata: map[string]any{
					"work_item_kind": string(domain.WorkItemKindResearch),
				},
				Results: map[string]any{
					"stdout": researchPayload,
				},
			},
		},
	}

	patch := objectiveQueueMetadataPatch(editTask, byWorkItemID)
	scope := anyStringSliceHTTPDefault(patch["suspected_paths"], nil)
	if len(scope) < 2 || scope[0] != "README.md" || scope[1] != "src/auth_service.go" {
		t.Fatalf("expected documentation workstream to keep README first while retaining code context, got %#v", scope)
	}
	if containsString(scope, "PASS") {
		t.Fatalf("expected validation artifact path to be filtered from documentation workstream scope, got %#v", scope)
	}
	projectRequest, _ := patch["project_request"].(map[string]any)
	expectedFiles := anyStringSliceHTTPDefault(projectRequest["expected_files"], nil)
	if len(expectedFiles) < 2 || expectedFiles[0] != "README.md" {
		t.Fatalf("expected project_request expected_files to preserve documentation priority, got %#v", projectRequest)
	}
}

func TestObjectiveQueueMetadataPatchPreservesDependencyWorkstreamPriority(t *testing.T) {
	researcher := domain.AgentTypeResearcher
	editTask := &domain.TaskResponse{
		ID: "task-edit-deps-1",
		Metadata: map[string]any{
			"work_item_kind":       string(domain.WorkItemKindEdit),
			"objective_workstream": "dependency_sync",
			"depends_on":           []string{"research", "edit"},
			"suspected_paths":      []string{"package.json", "package-lock.json"},
			"project_request": map[string]any{
				"project_name":   "demo-repo",
				"expected_files": []string{"package.json", "package-lock.json"},
			},
		},
	}
	researchPayload := `{
  "recommended_scope_paths": ["src/auth_service.ts", "package-lock.json", "PASS", "package.json"],
  "next_edit_brief": "Sync dependency manifests after the code patch."
}`
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{
		"research": {
			Task: domain.TaskResponse{
				ID:            "task-research-1",
				State:         domain.TaskStateCompleted,
				AssignedAgent: &researcher,
				Metadata: map[string]any{
					"work_item_kind": string(domain.WorkItemKindResearch),
				},
				Results: map[string]any{
					"stdout": researchPayload,
				},
			},
		},
	}

	patch := objectiveQueueMetadataPatch(editTask, byWorkItemID)
	scope := anyStringSliceHTTPDefault(patch["suspected_paths"], nil)
	if len(scope) < 3 || scope[0] != "package.json" || scope[1] != "package-lock.json" {
		t.Fatalf("expected dependency workstream to keep manifests first while retaining code context, got %#v", scope)
	}
	if containsString(scope, "PASS") {
		t.Fatalf("expected validation artifact path to be filtered from dependency workstream scope, got %#v", scope)
	}
}

func TestObjectiveQueueMetadataPatchUsesAnalysisOutputForEdit(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	editTask := &domain.TaskResponse{
		ID: "task-edit-2",
		Metadata: map[string]any{
			"work_item_kind": string(domain.WorkItemKindEdit),
			"depends_on":     []string{"analyze"},
			"suspected_paths": []string{
				"internal/orchestratorgo/httpapi/objectives.go",
			},
			"project_request": map[string]any{
				"project_name":   "demo-repo",
				"expected_files": []string{"internal/orchestratorgo/httpapi/objectives.go"},
			},
		},
	}
	analysisPayload := `{
  "findings": [
    {"severity":"warning","message":"large source file (540 lines)","location":{"file":"internal/orchestratorgo/httpapi/legacy_objectives.go"}},
    {"severity":"info","message":"trailing whitespace","location":{"file":"internal/orchestratorgo/httpapi/objectives.go","line":12}}
  ],
  "summary": {"total_findings": 2, "by_severity": {"warning": 1, "info": 1}}
}`
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{
		"analyze": {
			Task: domain.TaskResponse{
				ID:            "task-analyze-1",
				State:         domain.TaskStateCompleted,
				AssignedAgent: &reviewer,
				Metadata: map[string]any{
					"work_item_kind": string(domain.WorkItemKindAnalyze),
				},
				Results: map[string]any{
					"stdout": analysisPayload,
				},
			},
		},
	}

	patch := objectiveQueueMetadataPatch(editTask, byWorkItemID)
	if len(patch) == 0 {
		t.Fatalf("expected non-empty metadata patch from analysis output")
	}
	if summary, ok := patch["objective_analysis_summary"].(map[string]any); !ok || intValue(summary["total_findings"]) != 2 {
		t.Fatalf("expected analysis summary in patch, got %#v", patch["objective_analysis_summary"])
	}
	findings := anyMapSliceHTTPDefault(patch["objective_analysis_findings"], nil)
	if len(findings) != 2 {
		t.Fatalf("expected analysis findings in patch, got %#v", patch["objective_analysis_findings"])
	}
	scope := anyStringSliceHTTPDefault(patch["suspected_paths"], nil)
	if len(scope) < 2 || scope[0] != "internal/orchestratorgo/httpapi/legacy_objectives.go" {
		t.Fatalf("expected analysis file to lead suspected_paths, got %#v", patch["suspected_paths"])
	}
}

func TestObjectiveQueueMetadataPatchUsesBaselineValidateOutputForEdit(t *testing.T) {
	reviewer := domain.AgentTypeReviewer
	editTask := &domain.TaskResponse{
		ID: "task-edit-baseline",
		Metadata: map[string]any{
			"work_item_kind": string(domain.WorkItemKindEdit),
			"depends_on":     []string{"validate-baseline"},
		},
	}
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{
		"validate-baseline": {
			Task: domain.TaskResponse{
				ID:            "task-validate-0",
				State:         domain.TaskStateCompleted,
				AssignedAgent: &reviewer,
				Metadata: map[string]any{
					"work_item_kind":                string(domain.WorkItemKindValidate),
					"objective_baseline_validation": true,
				},
				Results: map[string]any{
					"summary": "Baseline validation failed before editing.",
					"stdout":  "pytest -q failed",
					"test_results": map[string]any{
						"exit_code": 1,
						"status":    "error",
					},
					"findings": []map[string]any{
						{"kind": "validation_failure", "severity": "high", "message": "CLI regression reproduced before editing."},
					},
				},
			},
		},
	}

	patch := objectiveQueueMetadataPatch(editTask, byWorkItemID)
	if len(patch) == 0 {
		t.Fatalf("expected baseline validate patch")
	}
	if asString(patch["objective_baseline_summary"]) != "Baseline validation failed before editing." {
		t.Fatalf("expected baseline summary in patch, got %#v", patch["objective_baseline_summary"])
	}
	findings := anyMapSliceHTTPDefault(patch["objective_baseline_findings"], nil)
	if len(findings) != 1 || asString(findings[0]["message"]) != "CLI regression reproduced before editing." {
		t.Fatalf("expected baseline findings in patch, got %#v", patch["objective_baseline_findings"])
	}
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func TestDecodeObjectiveExecutionContractFromMetadataMap(t *testing.T) {
	value := map[string]any{
		"title":                "Objective",
		"normalized_objective": "Ship autonomous objective flow",
		"workspace_root":       "/tmp/repo",
		"execution_backend":    "aider-task",
		"change_strategy":      "edit, validate, review",
		"validation_commands":  []string{"go", "test", "./..."},
		"completion_criteria":  []string{"tests pass", "review approved"},
		"risk_level":           "medium",
		"approval_required":    true,
		"work_items": []map[string]any{
			{"id": "edit", "kind": "edit", "title": "Edit", "description": "desc", "assigned_agent": "coder", "execution_mode": "agent_local", "execution_target": "local", "backend": "aider-task", "approval_required": true, "definition_of_done": "done"},
		},
	}
	contract, err := decodeObjectiveExecutionContract(value)
	if err != nil {
		t.Fatal(err)
	}
	if contract.NormalizedObjective != "Ship autonomous objective flow" {
		t.Fatalf("unexpected contract: %#v", contract)
	}
	if len(contract.WorkItems) != 1 || contract.WorkItems[0].Kind != domain.WorkItemKindEdit {
		t.Fatalf("unexpected work items: %#v", contract.WorkItems)
	}
}
