package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stratecode/lab/internal/orchestratorgo/bridge"
	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/initiative"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

func TestObjectiveAutonomyEvalsIntegration(t *testing.T) {
	if os.Getenv("LAB_RUN_OBJECTIVE_E2E") != "1" {
		t.Skip("set LAB_RUN_OBJECTIVE_E2E=1 to run Docker/Postgres objective integration harness")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for objective integration harness")
	}

	dsn, cleanupContainer := startObjectiveE2EPostgres(t)
	defer cleanupContainer()
	applyObjectiveE2ESchema(t, dsn)
	embeddingsURL, cleanupEmbeddings := startFakeEmbeddingsServer(t)
	defer cleanupEmbeddings()

	t.Run("repair_loop_completes", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:      "repair-loop",
			passAfter: 2,
			maxSteps:  14,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected completed initiative after repair loop, got %#v", result.initiative)
		}
		if len(result.links) < 8 {
			t.Fatalf("expected initial plus repair-cycle tasks, got %d", len(result.links))
		}
		for _, required := range []string{"research", "edit", "validate", "replan-2", "edit-2", "validate-2", "review-2"} {
			if _, ok := result.byWorkItemID[required]; !ok {
				t.Fatalf("expected work item %s in initiative task set: %#v", required, result.byWorkItemID)
			}
		}
		edit2 := result.byWorkItemID["edit-2"].Task
		edit2Results, _ := edit2.Results.(map[string]any)
		edit2Stdout := strings.TrimSpace(asString(edit2Results["stdout"]))
		if !strings.Contains(edit2Stdout, "--metadata") || !strings.Contains(edit2Stdout, "--scope-paths") || !strings.Contains(edit2Stdout, "--test-command") {
			t.Fatalf("expected enriched aider-task invocation on repair edit, got %q", edit2Stdout)
		}
		excluded := anyStringSliceHTTPDefault(edit2.Metadata["excluded_scope_paths"], nil)
		if len(excluded) == 0 {
			t.Fatalf("expected repair edit metadata to include excluded scope paths, got %#v", edit2.Metadata)
		}
		if asString(edit2.Metadata["planner_scope_resolution"]) == "" {
			t.Fatalf("expected repair edit metadata to include planner scope resolution, got %#v", edit2.Metadata)
		}
		if !strings.Contains(strings.TrimSpace(asString(edit2.Metadata["objective_research_brief"])), "Excluded paths for next attempt:") {
			t.Fatalf("expected repair edit brief to carry explicit exclusion guidance, got %#v", edit2.Metadata["objective_research_brief"])
		}
		replan2 := result.byWorkItemID["replan-2"].Task
		replan2Results, _ := replan2.Results.(map[string]any)
		replan2Stdout := strings.TrimSpace(asString(replan2Results["stdout"]))
		if !strings.Contains(replan2Stdout, "\"repair_mode\": true") || !strings.Contains(replan2Stdout, "\"failure_hypotheses\"") {
			t.Fatalf("expected repair-aware replanner output, got %q", replan2Stdout)
		}
		if result.artifactTypes["objective_repair_signal"] == 0 {
			t.Fatalf("expected objective_repair_signal artifact, got %#v", result.artifactTypes)
		}
		if result.artifactTypes["objective_status_snapshot"] == 0 {
			t.Fatalf("expected objective_status_snapshot artifact, got %#v", result.artifactTypes)
		}
		if result.artifactTypes["repair_plan"] == 0 {
			t.Fatalf("expected repair_plan artifact, got %#v", result.artifactTypes)
		}
	})

	t.Run("greenfield_bootstrap_completes", func(t *testing.T) {
		workspaceRoot := t.TempDir()
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "greenfield-bootstrap",
			passAfter:     1,
			maxSteps:      10,
			workspaceRoot: workspaceRoot,
			objective:     "Create a small CLI that prints a deterministic greeting and make the objective flow finish autonomously.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected completed initiative for greenfield objective, got initiative=%#v approvals=%d links=%#v latest_snapshot=%#v", result.initiative, result.approvalsResolved, result.links, result.latestStatusSnapshot)
		}
		if _, ok := result.byWorkItemID["bootstrap"]; !ok {
			t.Fatalf("expected bootstrap work item in greenfield objective, got %#v", result.byWorkItemID)
		}
		bootstrapTask := result.byWorkItemID["bootstrap"].Task
		bootstrapResults, _ := bootstrapTask.Results.(map[string]any)
		if !strings.Contains(strings.TrimSpace(asString(bootstrapResults["stdout"])), "README.md") {
			t.Fatalf("expected scaffold output to include in-place files, got %#v", bootstrapResults)
		}
		if _, err := os.Stat(filepath.Join(workspaceRoot, "main.py")); err != nil {
			t.Fatalf("expected greenfield scaffold to create main.py in place: %v", err)
		}
		editTask := result.byWorkItemID["edit"].Task
		if deps := anyStringSliceHTTPDefault(editTask.Metadata["depends_on"], nil); len(deps) != 2 || deps[1] != "bootstrap" {
			t.Fatalf("expected edit to depend on bootstrap, got %#v", editTask.Metadata["depends_on"])
		}
	})

	t.Run("baseline_validation_runs_before_first_edit", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:      "baseline-validate-first",
			passAfter: 1,
			maxSteps:  12,
			objective: "Fix the failing CLI regression and prove it with validation before and after the patch.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		baselineTask, ok := result.byWorkItemID["validate-baseline"]
		if !ok {
			t.Fatalf("expected validate-baseline work item, got %#v", result.byWorkItemID)
		}
		if !objectiveAsBool(baselineTask.Task.Metadata["objective_baseline_validation"]) {
			t.Fatalf("expected baseline validation metadata flag, got %#v", baselineTask.Task.Metadata)
		}
		editTask := result.byWorkItemID["edit"].Task
		deps := anyStringSliceHTTPDefault(editTask.Metadata["depends_on"], nil)
		if len(deps) < 2 || deps[1] != "validate-baseline" {
			t.Fatalf("expected edit to depend on validate-baseline, got %#v", editTask.Metadata["depends_on"])
		}
		baselineFindings := anyMapSliceHTTPDefault(editTask.Metadata["objective_baseline_findings"], nil)
		if len(baselineFindings) == 0 {
			t.Fatalf("expected baseline validation findings in edit metadata, got %#v", editTask.Metadata)
		}
		baselineResults, _ := editTask.Metadata["objective_baseline_test_results"].(map[string]any)
		if baselineResults == nil || strings.TrimSpace(asString(baselineResults["status"])) == "" {
			t.Fatalf("expected baseline validation test results in edit metadata, got %#v", editTask.Metadata["objective_baseline_test_results"])
		}
	})

	t.Run("approval_mid_flight_completes", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:      "approval-mid-flight",
			passAfter: 1,
			maxSteps:  10,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected completed initiative, got %#v", result.initiative)
		}
		if result.approvalsResolved == 0 {
			t.Fatalf("expected at least one approval resolution, got %#v", result)
		}
		if result.artifactTypes["objective_status_snapshot"] == 0 {
			t.Fatalf("expected status snapshots, got %#v", result.artifactTypes)
		}
		if result.latestStatusSnapshot == nil || result.latestStatusSnapshot.NextExpectedAction != "close_initiative" {
			t.Fatalf("expected final snapshot to close initiative, got %#v", result.latestStatusSnapshot)
		}
	})

	t.Run("max_iterations_block", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:      "max-iterations-block",
			passAfter: 0,
			maxSteps:  18,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusBlocked {
			t.Fatalf("expected blocked initiative after exhausting retries, got %#v", result.initiative)
		}
		if result.artifactTypes["objective_status_snapshot"] == 0 {
			t.Fatalf("expected status snapshots, got %#v", result.artifactTypes)
		}
		if result.latestStatusSnapshot == nil {
			t.Fatalf("expected latest status snapshot, got %#v", result)
		}
		if result.latestStatusSnapshot.NextExpectedAction != "block_initiative" {
			t.Fatalf("expected block_initiative next action, got %#v", result.latestStatusSnapshot)
		}
		if !strings.Contains(result.latestStatusSnapshot.BlockerReason, "Maximum repair iterations exhausted") {
			t.Fatalf("expected blocker reason to mention exhausted iterations, got %#v", result.latestStatusSnapshot)
		}
		if result.artifactTypes["objective_repair_signal"] == 0 {
			t.Fatalf("expected repair signals before blocking, got %#v", result.artifactTypes)
		}
	})

	t.Run("time_budget_block", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:                       "time-budget-block",
			passAfter:                  1,
			maxSteps:                   10,
			objectiveTimeBudgetSeconds: 1,
			aiderSleepSeconds:          2,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusBlocked {
			t.Fatalf("expected initiative to block on time budget exhaustion, got %#v", result.initiative)
		}
		if result.latestStatusSnapshot == nil {
			t.Fatalf("expected latest status snapshot, got %#v", result)
		}
		if result.latestStatusSnapshot.StopReason != "time_budget_exhausted" {
			t.Fatalf("expected time_budget_exhausted stop reason, got %#v", result.latestStatusSnapshot)
		}
		if !strings.Contains(result.latestStatusSnapshot.BlockerReason, "Objective time budget exhausted") {
			t.Fatalf("expected blocker reason to mention time budget exhaustion, got %#v", result.latestStatusSnapshot)
		}
	})

	t.Run("research_guides_initial_edit_scope", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "feature_flag.py"), []byte("FLAG = False\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "research-guides-scope",
			passAfter:     1,
			maxSteps:      10,
			workspaceRoot: workspaceRoot,
			objective:     "Update src/feature_flag.py so the autonomous objective flow enables the marker cleanly.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editTask, ok := result.byWorkItemID["edit"]
		if !ok {
			t.Fatalf("expected edit work item, got %#v", result.byWorkItemID)
		}
		scope := anyStringSliceHTTPDefault(editTask.Task.Metadata["suspected_paths"], nil)
		if len(scope) == 0 || !containsString(scope, "src/feature_flag.py") {
			t.Fatalf("expected research to inject src/feature_flag.py into initial edit scope, got %#v", scope)
		}
		if !strings.Contains(strings.TrimSpace(asString(editTask.Task.Metadata["objective_research_brief"])), "src/feature_flag.py") {
			t.Fatalf("expected research brief to mention inferred file, got %#v", editTask.Task.Metadata["objective_research_brief"])
		}
		editResults, _ := editTask.Task.Results.(map[string]any)
		editStdout := strings.TrimSpace(asString(editResults["stdout"]))
		if !strings.Contains(editStdout, "--scope-paths") || !strings.Contains(editStdout, "src/feature_flag.py") {
			t.Fatalf("expected aider invocation to carry research-derived scope, got %q", editStdout)
		}
	})

	t.Run("research_infers_scope_from_readme", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		readme := "# objective e2e\n\nThe objective toggle lives in src/switchboard.py.\n"
		if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "research-readme-scope",
			passAfter:     1,
			maxSteps:      10,
			workspaceRoot: workspaceRoot,
			objective:     "Enable the objective toggle cleanly for the autonomous flow.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editTask, ok := result.byWorkItemID["edit"]
		if !ok {
			t.Fatalf("expected edit work item, got %#v", result.byWorkItemID)
		}
		scope := anyStringSliceHTTPDefault(editTask.Task.Metadata["suspected_paths"], nil)
		if len(scope) == 0 || scope[0] != "src/switchboard.py" {
			t.Fatalf("expected README-derived scope to lead initial edit, got %#v", scope)
		}
		brief := strings.TrimSpace(asString(editTask.Task.Metadata["objective_research_brief"]))
		if !strings.Contains(brief, "src/switchboard.py") {
			t.Fatalf("expected research brief to mention README-derived file, got %q", brief)
		}
		editResults, _ := editTask.Task.Results.(map[string]any)
		editStdout := strings.TrimSpace(asString(editResults["stdout"]))
		if !strings.Contains(editStdout, "--scope-paths") || !strings.Contains(editStdout, "src/switchboard.py") {
			t.Fatalf("expected aider invocation to carry README-derived scope, got %q", editStdout)
		}
	})

	t.Run("research_readme_disambiguates_competing_files", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		readme := "# objective e2e\n\nThe current objective toggle lives in switchboard.py.\nfeature_toggle.py is legacy and should not be the first target.\n"
		if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "feature_toggle.py"), []byte("LEGACY = True\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "research-readme-ambiguity",
			passAfter:     1,
			maxSteps:      10,
			workspaceRoot: workspaceRoot,
			objective:     "Enable the objective toggle cleanly for the autonomous flow.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editTask, ok := result.byWorkItemID["edit"]
		if !ok {
			t.Fatalf("expected edit work item, got %#v", result.byWorkItemID)
		}
		scope := anyStringSliceHTTPDefault(editTask.Task.Metadata["suspected_paths"], nil)
		if len(scope) < 2 {
			t.Fatalf("expected ranked competing scope paths, got %#v", scope)
		}
		if scope[0] != "src/switchboard.py" {
			t.Fatalf("expected README to rank switchboard first, got %#v", scope)
		}
		editResults, _ := editTask.Task.Results.(map[string]any)
		editStdout := strings.TrimSpace(asString(editResults["stdout"]))
		if !strings.Contains(editStdout, "--scope-paths") || !strings.Contains(editStdout, "src/switchboard.py") {
			t.Fatalf("expected aider invocation to carry README-ranked scope, got %q", editStdout)
		}
	})

	t.Run("planner_ambiguity_triggers_initial_analysis", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		readme := "# objective e2e\n\nThe objective behavior lives in src/switchboard.py.\nThe objective behavior also lives in src/feature_toggle.py.\n"
		if err := os.WriteFile(filepath.Join(workspaceRoot, "README.md"), []byte(readme), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "switchboard.py"), []byte("ENABLED = False\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "feature_toggle.py"), []byte("FLAG = False\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "planner-ambiguity-analysis",
			passAfter:     1,
			maxSteps:      12,
			workspaceRoot: workspaceRoot,
			objective:     "Enable the objective behavior cleanly for the autonomous flow.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		analyzeTask, ok := result.byWorkItemID["analyze"]
		if !ok {
			t.Fatalf("expected initial analyze work item for ambiguous shortlist, got %#v", result.byWorkItemID)
		}
		if !strings.Contains(strings.TrimSpace(asString(analyzeTask.Task.Description)), "ambiguous between") {
			t.Fatalf("expected analyze description to mention ambiguous shortlist, got %q", analyzeTask.Task.Description)
		}
		editTask := result.byWorkItemID["edit"].Task
		deps := anyStringSliceHTTPDefault(editTask.Metadata["depends_on"], nil)
		if len(deps) < 2 || deps[len(deps)-1] != "analyze" {
			t.Fatalf("expected edit to depend on analyze after ambiguous shortlist, got %#v", editTask.Metadata["depends_on"])
		}
	})

	t.Run("mixed_code_and_docs_objective_runs_two_edit_workstreams", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "mixed-code-docs",
			passAfter:     1,
			maxSteps:      12,
			workspaceRoot: workspaceRoot,
			objective:     "Fix src/auth_service.go and update the README documentation so the operator guidance stays accurate.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editDocs, ok := result.byWorkItemID["edit-docs"]
		if !ok {
			t.Fatalf("expected edit-docs work item, got %#v", result.byWorkItemID)
		}
		if got := strings.TrimSpace(asString(editDocs.Task.Metadata["objective_workstream"])); got != "documentation_sync" {
			t.Fatalf("expected documentation_sync workstream, got %#v", editDocs.Task.Metadata)
		}
		deps := anyStringSliceHTTPDefault(editDocs.Task.Metadata["depends_on"], nil)
		if len(deps) < 2 || deps[len(deps)-1] != "edit" {
			t.Fatalf("expected edit-docs to depend on primary edit, got %#v", editDocs.Task.Metadata["depends_on"])
		}
		scope := anyStringSliceHTTPDefault(editDocs.Task.Metadata["suspected_paths"], nil)
		if len(scope) == 0 || scope[0] != "README.md" {
			t.Fatalf("expected edit-docs scope to lead with README.md, got %#v", scope)
		}
		validateTask := result.byWorkItemID["validate"].Task
		validateDeps := anyStringSliceHTTPDefault(validateTask.Metadata["depends_on"], nil)
		if len(validateDeps) != 1 || validateDeps[0] != "edit-docs" {
			t.Fatalf("expected validate to depend on edit-docs, got %#v", validateTask.Metadata["depends_on"])
		}
	})

	t.Run("mixed_code_and_config_objective_runs_two_edit_workstreams", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "docker-compose.yml"), []byte("services:\n  app:\n    build: .\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "mixed-code-config",
			passAfter:     1,
			maxSteps:      12,
			workspaceRoot: workspaceRoot,
			objective:     "Fix src/auth_service.go and update the Dockerfile plus docker-compose.yml deployment configuration so the service still boots correctly.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editConfig, ok := result.byWorkItemID["edit-config"]
		if !ok {
			t.Fatalf("expected edit-config work item, got %#v", result.byWorkItemID)
		}
		if got := strings.TrimSpace(asString(editConfig.Task.Metadata["objective_workstream"])); got != "config_sync" {
			t.Fatalf("expected config_sync workstream, got %#v", editConfig.Task.Metadata)
		}
		deps := anyStringSliceHTTPDefault(editConfig.Task.Metadata["depends_on"], nil)
		if len(deps) < 2 || deps[len(deps)-1] != "edit" {
			t.Fatalf("expected edit-config to depend on primary edit, got %#v", editConfig.Task.Metadata["depends_on"])
		}
		scope := anyStringSliceHTTPDefault(editConfig.Task.Metadata["suspected_paths"], nil)
		if len(scope) < 2 || scope[0] != "Dockerfile" {
			t.Fatalf("expected edit-config scope to lead with Dockerfile, got %#v", scope)
		}
		validateTask := result.byWorkItemID["validate"].Task
		validateDeps := anyStringSliceHTTPDefault(validateTask.Metadata["depends_on"], nil)
		if len(validateDeps) != 1 || validateDeps[0] != "edit-config" {
			t.Fatalf("expected validate to depend on edit-config, got %#v", validateTask.Metadata["depends_on"])
		}
	})

	t.Run("mixed_code_and_dependency_objective_runs_two_edit_workstreams", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "auth_service.ts"), []byte("export const ready = true;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "package.json"), []byte("{\"name\":\"demo\"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "package-lock.json"), []byte("{\"lockfileVersion\":3}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "mixed-code-deps",
			passAfter:     1,
			maxSteps:      12,
			workspaceRoot: workspaceRoot,
			objective:     "Fix src/auth_service.ts and update package.json plus package-lock.json so the dependency upgrade lands cleanly.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete, got %#v", result.initiative)
		}
		editDeps, ok := result.byWorkItemID["edit-deps"]
		if !ok {
			t.Fatalf("expected edit-deps work item, got %#v", result.byWorkItemID)
		}
		if got := strings.TrimSpace(asString(editDeps.Task.Metadata["objective_workstream"])); got != "dependency_sync" {
			t.Fatalf("expected dependency_sync workstream, got %#v", editDeps.Task.Metadata)
		}
		deps := anyStringSliceHTTPDefault(editDeps.Task.Metadata["depends_on"], nil)
		if len(deps) < 2 || deps[len(deps)-1] != "edit" {
			t.Fatalf("expected edit-deps to depend on primary edit, got %#v", editDeps.Task.Metadata["depends_on"])
		}
		scope := anyStringSliceHTTPDefault(editDeps.Task.Metadata["suspected_paths"], nil)
		if len(scope) < 2 || scope[0] != "package.json" {
			t.Fatalf("expected edit-deps scope to lead with package manifests, got %#v", scope)
		}
		validateTask := result.byWorkItemID["validate"].Task
		validateDeps := anyStringSliceHTTPDefault(validateTask.Metadata["depends_on"], nil)
		if len(validateDeps) != 1 || validateDeps[0] != "edit-deps" {
			t.Fatalf("expected validate to depend on edit-deps, got %#v", validateTask.Metadata["depends_on"])
		}
	})

	t.Run("mixed_code_and_docs_repair_cycle_keeps_secondary_workstream", func(t *testing.T) {
		workspaceRoot := setupObjectiveE2EWorkspace(t)
		if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "auth_service.go"), []byte("package src\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:          "mixed-code-docs-repair",
			passAfter:     3,
			maxSteps:      28,
			workspaceRoot: workspaceRoot,
			objective:     "Fix src/auth_service.go and update the README documentation so the operator guidance stays accurate.",
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete after repair cycle, got initiative=%#v latest_snapshot=%#v work_items=%#v approvals=%d", result.initiative, result.latestStatusSnapshot, result.byWorkItemID, result.approvalsResolved)
		}
		if _, ok := result.byWorkItemID["edit-2"]; !ok {
			t.Fatalf("expected repair edit work item, got %#v", result.byWorkItemID)
		}
		editDocsRepair, ok := result.byWorkItemID["edit-docs-2"]
		if !ok {
			t.Fatalf("expected repair documentation workstream, got %#v", result.byWorkItemID)
		}
		if got := strings.TrimSpace(asString(editDocsRepair.Task.Metadata["objective_workstream"])); got != "documentation_sync" {
			t.Fatalf("expected documentation_sync repair workstream, got %#v", editDocsRepair.Task.Metadata)
		}
		deps := anyStringSliceHTTPDefault(editDocsRepair.Task.Metadata["depends_on"], nil)
		if len(deps) != 1 || deps[0] != "edit-2" {
			t.Fatalf("expected edit-docs-2 to depend on edit-2, got %#v", editDocsRepair.Task.Metadata["depends_on"])
		}
		validateRepair := result.byWorkItemID["validate-2"].Task
		validateDeps := anyStringSliceHTTPDefault(validateRepair.Metadata["depends_on"], nil)
		if len(validateDeps) != 1 || validateDeps[0] != "edit-docs-2" {
			t.Fatalf("expected validate-2 to depend on edit-docs-2, got %#v", validateRepair.Metadata["depends_on"])
		}
	})

	t.Run("bridge_interruption_recovers_with_new_bridge", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:                       "bridge-interruption-recovery",
			passAfter:                  1,
			maxSteps:                   14,
			simulateBridgeInterruption: true,
			localBridgeLeaseTTLSeconds: 1,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete after bridge interruption recovery, got %#v", result.initiative)
		}
		if result.interruptedTaskID == "" {
			t.Fatalf("expected interrupted task id to be recorded, got %#v", result)
		}
		if result.recoveredBridgeID == "" || result.recoveredBridgeID == result.initialBridgeID {
			t.Fatalf("expected a second bridge to recover execution, got initial=%q recovered=%q", result.initialBridgeID, result.recoveredBridgeID)
		}
		if result.claimedByBridge[result.initialBridgeID] == 0 || result.claimedByBridge[result.recoveredBridgeID] == 0 {
			t.Fatalf("expected claims from both bridges, got %#v", result.claimedByBridge)
		}
		if result.recoveredTaskID != result.interruptedTaskID {
			t.Fatalf("expected same task to be reclaimed after interruption, got interrupted=%q recovered=%q", result.interruptedTaskID, result.recoveredTaskID)
		}
	})

	t.Run("server_restart_recovers_with_new_bridge", func(t *testing.T) {
		result := runObjectiveAutonomyEval(t, dsn, embeddingsURL, objectiveEvalScenario{
			name:                       "server-restart-recovery",
			passAfter:                  1,
			maxSteps:                   14,
			simulateBridgeInterruption: true,
			simulateServerRestart:      true,
			localBridgeLeaseTTLSeconds: 1,
		})
		if result.initiative == nil || result.initiative.Status != domain.InitiativeStatusCompleted {
			t.Fatalf("expected initiative to complete after server restart recovery, got %#v", result.initiative)
		}
		if !result.serverRestarted {
			t.Fatalf("expected server restart to be recorded, got %#v", result)
		}
		if result.recoveredTaskID != result.interruptedTaskID || result.recoveredTaskID == "" {
			t.Fatalf("expected interrupted task to be recovered after restart, got interrupted=%q recovered=%q", result.interruptedTaskID, result.recoveredTaskID)
		}
		if result.recoveredBridgeID == "" || result.recoveredBridgeID == result.initialBridgeID {
			t.Fatalf("expected recovery bridge after restart, got initial=%q recovered=%q", result.initialBridgeID, result.recoveredBridgeID)
		}
	})
}

type objectiveEvalScenario struct {
	name                       string
	passAfter                  int
	maxSteps                   int
	workspaceRoot              string
	objective                  string
	objectiveTimeBudgetSeconds int
	aiderSleepSeconds          int
	simulateBridgeInterruption bool
	simulateServerRestart      bool
	localBridgeLeaseTTLSeconds int
}

type objectiveEvalResult struct {
	initiative           *domain.InitiativeResponse
	links                []domain.InitiativeTaskLinkResponse
	byWorkItemID         map[string]domain.InitiativeTaskLinkResponse
	artifacts            []domain.ArtifactResponse
	artifactTypes        map[string]int
	latestStatusSnapshot *domain.ObjectiveStatusSnapshot
	approvalsResolved    int
	initialBridgeID      string
	recoveredBridgeID    string
	interruptedTaskID    string
	recoveredTaskID      string
	claimedByBridge      map[string]int
	serverRestarted      bool
}

func runObjectiveAutonomyEval(t *testing.T, dsn, embeddingsURL string, scenario objectiveEvalScenario) objectiveEvalResult {
	t.Helper()
	_ = embeddingsURL
	workspaceRoot := strings.TrimSpace(scenario.workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = setupObjectiveE2EWorkspace(t)
	}
	installFakeAiderTask(t, scenario.passAfter, scenario.aiderSleepSeconds)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	postgres, err := store.NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer postgres.Close()

	cfg := config.Config{LlamaTimeoutSeconds: 10}
	server := newObjectiveE2EServer(postgres, cfg)
	if scenario.localBridgeLeaseTTLSeconds > 0 {
		server.LocalBridgeLeaseTTLSeconds = scenario.localBridgeLeaseTTLSeconds
	}
	router := server.Router(nil)

	bridgeID := "objective-e2e-" + scenario.name
	registerObjectiveE2EBridge(t, router, bridgeID, workspaceRoot)

	var objectiveResp domain.ObjectiveResponse
	postJSONStatus(t, router, http.MethodPost, "/objectives/", domain.ObjectiveRequest{
		Title:             "Objective E2E " + scenario.name,
		Objective:         firstNonEmptyString(strings.TrimSpace(scenario.objective), "Apply a repository change and recover automatically when validation fails."),
		WorkspaceRoot:     workspaceRoot,
		CreatedBy:         "integration-test",
		TimeBudgetSeconds: scenario.objectiveTimeBudgetSeconds,
	}, http.StatusCreated, &objectiveResp)
	if objectiveResp.Initiative == nil {
		t.Fatalf("expected initiative in objective response: %#v", objectiveResp)
	}

	executor, err := bridge.NewWorkspaceExecutor(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}

	maxSteps := scenario.maxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}
	approvalsResolved := 0
	initialBridgeID := bridgeID
	recoveredBridgeID := ""
	interruptedTaskID := ""
	recoveredTaskID := ""
	claimedByBridge := map[string]int{}
	interruptionTriggered := false
	serverRestarted := false
	idlePolls := 0
	for step := 0; step < maxSteps; step++ {
		claim := claimNextBridgeTask(t, router, bridgeID)
		if claim == nil {
			if approval := firstPendingApproval(t, router); approval != nil {
				approveApprovalForTest(t, router, approval.ID)
				approvalsResolved++
				idlePolls = 0
				continue
			}
			currentInitiative, err := postgres.GetInitiative(ctx, objectiveResp.Initiative.ID)
			if err != nil {
				t.Fatal(err)
			}
			if currentInitiative != nil && currentInitiative.Status == domain.InitiativeStatusExecuting {
				idlePolls++
				time.Sleep(200 * time.Millisecond)
				continue
			}
			idlePolls++
			if idlePolls >= 6 {
				break
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		idlePolls = 0
		claimedByBridge[bridgeID]++
		if scenario.simulateBridgeInterruption && !interruptionTriggered {
			interruptionTriggered = true
			interruptedTaskID = claim.TaskID
			if scenario.simulateServerRestart {
				server = newObjectiveE2EServer(postgres, cfg)
				if scenario.localBridgeLeaseTTLSeconds > 0 {
					server.LocalBridgeLeaseTTLSeconds = scenario.localBridgeLeaseTTLSeconds
				}
				router = server.Router(nil)
				serverRestarted = true
			}
			time.Sleep(time.Duration(max(1, server.localBridgeLeaseTTLSeconds())+1) * time.Second)
			bridgeID = initialBridgeID + "-recovery"
			recoveredBridgeID = bridgeID
			registerObjectiveE2EBridge(t, router, bridgeID, workspaceRoot)
			continue
		}
		if interruptionTriggered && recoveredTaskID == "" && claim.TaskID == interruptedTaskID && bridgeID == recoveredBridgeID {
			recoveredTaskID = claim.TaskID
		}
		result, err := executor.Execute(ctx, *claim)
		if err != nil {
			t.Fatalf("bridge execution failed for task %s: %v", claim.TaskID, err)
		}
		postJSONStatus(t, router, http.MethodPost, fmt.Sprintf("/bridges/%s/tasks/%s/result", bridgeID, claim.TaskID), result, http.StatusAccepted, nil)
	}
	initiativeRecord, err := postgres.GetInitiative(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	links, err := postgres.ListInitiativeTasks(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	byWorkItemID := map[string]domain.InitiativeTaskLinkResponse{}
	for _, link := range links {
		workItemID := strings.TrimSpace(asString(link.Task.Metadata["work_item_id"]))
		if workItemID != "" {
			byWorkItemID[workItemID] = link
		}
	}
	artifacts, err := postgres.ListInitiativeArtifacts(ctx, objectiveResp.Initiative.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifactTypes := map[string]int{}
	var latestStatusSnapshot *domain.ObjectiveStatusSnapshot
	var latestSnapshotCreatedAt time.Time
	for _, artifact := range artifacts {
		artifactTypes[artifact.ArtifactType]++
		if artifact.ArtifactType == "objective_status_snapshot" && artifact.ContentText != nil {
			var snapshot domain.ObjectiveStatusSnapshot
			if err := json.Unmarshal([]byte(*artifact.ContentText), &snapshot); err == nil {
				if latestStatusSnapshot == nil || objectiveEvalSnapshotPrecedes(*latestStatusSnapshot, latestSnapshotCreatedAt, snapshot, artifact.CreatedAt) {
					candidate := snapshot
					latestStatusSnapshot = &candidate
					latestSnapshotCreatedAt = artifact.CreatedAt
				}
			}
		}
	}
	return objectiveEvalResult{
		initiative:           initiativeRecord,
		links:                links,
		byWorkItemID:         byWorkItemID,
		artifacts:            artifacts,
		artifactTypes:        artifactTypes,
		latestStatusSnapshot: latestStatusSnapshot,
		approvalsResolved:    approvalsResolved,
		initialBridgeID:      initialBridgeID,
		recoveredBridgeID:    recoveredBridgeID,
		interruptedTaskID:    interruptedTaskID,
		recoveredTaskID:      recoveredTaskID,
		claimedByBridge:      claimedByBridge,
		serverRestarted:      serverRestarted,
	}
}

func newObjectiveE2EServer(postgres *store.PostgresStore, cfg config.Config) *Server {
	return &Server{
		Config:      cfg,
		Postgres:    postgres,
		Initiatives: initiative.New(cfg, nil),
		Now:         time.Now,
		Version:     "test",
	}
}

func registerObjectiveE2EBridge(t *testing.T, handler http.Handler, bridgeID, workspaceRoot string) {
	t.Helper()
	postJSONStatus(t, handler, http.MethodPost, "/bridges/register", domain.LocalBridgeRegisterRequest{
		BridgeID:      bridgeID,
		Name:          "objective-e2e",
		Hostname:      "localhost",
		WorkspaceRoot: workspaceRoot,
		Capabilities:  map[string]any{"tools": []string{"research_project", "scaffold_project", "code_analysis", "run_command", "run_tests", "review_workspace"}},
	}, http.StatusOK, nil)
}

func objectiveEvalSnapshotPrecedes(current domain.ObjectiveStatusSnapshot, currentCreatedAt time.Time, candidate domain.ObjectiveStatusSnapshot, candidateCreatedAt time.Time) bool {
	if candidate.Iteration != current.Iteration {
		return candidate.Iteration > current.Iteration
	}
	currentRank := objectiveEvalSnapshotRank(current)
	candidateRank := objectiveEvalSnapshotRank(candidate)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	return candidateCreatedAt.After(currentCreatedAt)
}

func objectiveEvalSnapshotRank(snapshot domain.ObjectiveStatusSnapshot) int {
	switch snapshot.WorkItemKind {
	case string(domain.WorkItemKindReview):
		return 6
	case string(domain.WorkItemKindValidate):
		return 5
	case string(domain.WorkItemKindEdit):
		return 4
	case string(domain.WorkItemKindAnalyze):
		return 3
	case string(domain.WorkItemKindReplan):
		return 2
	case string(domain.WorkItemKindResearch):
		return 1
	default:
		return 0
	}
}

func objectiveEvalContextPackage(metadata map[string]any) map[string]any {
	contextPackage, _ := metadata["context_package"].(map[string]any)
	return contextPackage
}

func objectiveEvalContextChunks(contextPackage map[string]any) []map[string]any {
	rawChunks, _ := contextPackage["chunks"].([]any)
	chunks := make([]map[string]any, 0, len(rawChunks))
	for _, raw := range rawChunks {
		chunk, _ := raw.(map[string]any)
		if len(chunk) > 0 {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

func objectiveEvalHasForeignInitiativeChunk(contextPackage map[string]any, initiativeID string) bool {
	for _, chunk := range objectiveEvalContextChunks(contextPackage) {
		if strings.TrimSpace(asString(chunk["initiative_id"])) == strings.TrimSpace(initiativeID) {
			return true
		}
	}
	return false
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}

func startFakeEmbeddingsServer(t *testing.T) (string, func()) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/embeddings" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": []map[string]any{{
				"embedding": fakeEmbeddingVector(body.Input),
			}},
		})
	})
	server := httptest.NewServer(handler)
	return server.URL, server.Close
}

func fakeEmbeddingVector(input string) []float64 {
	input = strings.TrimSpace(input)
	if input == "" {
		return []float64{0, 0, 0, 0, 0, 0, 0, 0}
	}
	runes := []rune(input)
	sum := 0
	alpha := 0
	digits := 0
	upper := 0
	for _, r := range runes {
		sum += int(r)
		switch {
		case r >= 'a' && r <= 'z':
			alpha++
		case r >= 'A' && r <= 'Z':
			alpha++
			upper++
		case r >= '0' && r <= '9':
			digits++
		}
	}
	length := float64(len(runes))
	return []float64{
		length / 512.0,
		float64(sum%997) / 997.0,
		float64(alpha) / (length + 1),
		float64(digits) / (length + 1),
		float64(upper) / (length + 1),
		float64(strings.Count(input, "\n")) / (length + 1),
		float64(strings.Count(strings.ToLower(input), "objective")) / 8.0,
		float64(strings.Count(strings.ToLower(input), "repair")) / 8.0,
	}
}

func setupObjectiveE2EWorkspace(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "objective-e2e-repo")
	if err := os.MkdirAll(filepath.Join(root, ".lab"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# objective e2e\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	caseJSON := `{
  "id": "objective-repair-loop",
  "case_type": "integration_repair_loop",
  "repo_profile": "objective_e2e_v1",
  "repo_url": "https://example.invalid/objective-e2e",
  "default_branch": "main",
  "project_type": "existing_repo",
  "runtime_or_stack": "python",
  "project_root": ".",
  "test_focus": "objective repair loop",
  "test_command": ["python3", "-c", "import pathlib,sys; sys.exit(0 if pathlib.Path('PASS').exists() else 1)"],
  "expected_files": ["README.md", "PASS"],
  "language": "python",
  "problem_domain": "objective autonomy"
}`
	if err := os.WriteFile(filepath.Join(root, ".lab", "benchmark-case.json"), []byte(caseJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitInDir(t, root, "init")
	runGitInDir(t, root, "checkout", "-b", "main")
	runGitInDir(t, root, "add", "README.md", ".lab/benchmark-case.json")
	runGitInDir(t, root, "-c", "user.name=Codex", "-c", "user.email=codex@example.com", "commit", "-m", "init")
	return root
}

func installFakeAiderTask(t *testing.T, passAfter int, sleepSeconds int) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "aider-task")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
countfile="$PWD/.aider-count"
count=0
if [ -f "$countfile" ]; then
  count=$(cat "$countfile")
fi
count=$((count+1))
printf '%%s' "$count" > "$countfile"
printf 'aider-run-%%s %%s\n' "$count" "$*"
if [ %d -gt 0 ]; then
  sleep %d
fi
printf '\nedit-%%s\n' "$count" >> "$PWD/README.md"
if [ %d -gt 0 ] && [ "$count" -ge %d ]; then
  printf 'ok\n' > "$PWD/PASS"
fi
`, sleepSeconds, sleepSeconds, passAfter, passAfter)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func startObjectiveE2EPostgres(t *testing.T) (string, func()) {
	t.Helper()
	name := fmt.Sprintf("objective-e2e-%d", time.Now().UnixNano())
	runCommand(t, exec.Command("docker", "run", "-d", "--rm", "--name", name,
		"-e", "POSTGRES_DB=orchestrator",
		"-e", "POSTGRES_USER=orchestrator",
		"-e", "POSTGRES_PASSWORD=orchestrator",
		"-p", "127.0.0.1::5432",
		"pgvector/pgvector:pg16",
	))
	cleanup := func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	}
	t.Cleanup(cleanup)

	portRaw := strings.TrimSpace(runCommand(t, exec.Command("docker", "port", name, "5432/tcp")))
	parts := strings.Split(portRaw, ":")
	port := parts[len(parts)-1]
	host := objectiveE2EDockerHost()
	dsn := fmt.Sprintf("postgres://orchestrator:orchestrator@%s:%s/orchestrator", host, port)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			err = pool.Ping(ctx)
			pool.Close()
		}
		cancel()
		if err == nil {
			return dsn, cleanup
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("postgres container %s did not become ready", name)
	return "", cleanup
}

func objectiveE2EDockerHost() string {
	if host := strings.TrimSpace(os.Getenv("LAB_OBJECTIVE_E2E_DOCKER_HOST")); host != "" {
		return host
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "host.docker.internal"
	}
	return "127.0.0.1"
}

func applyObjectiveE2ESchema(t *testing.T, dsn string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	schema := `
CREATE TYPE taskstate AS ENUM ('created','queued','assigned','in_progress','waiting_approval','review','retrying','completed','failed','cancelled');
CREATE TYPE approvalstatus AS ENUM ('pending','approved','rejected','expired');

CREATE TABLE tasks (
  id UUID PRIMARY KEY,
  state taskstate NOT NULL,
  description TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  assigned_agent TEXT NULL,
  priority TEXT NOT NULL,
  execution_target TEXT NOT NULL,
  workspace_path TEXT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  max_retries INTEGER NOT NULL DEFAULT 3,
  idempotency_key TEXT NULL,
  correlation_id UUID NOT NULL,
  results JSONB NULL,
  error_message TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  started_at TIMESTAMPTZ NULL,
  completed_at TIMESTAMPTZ NULL,
  queued_at TIMESTAMPTZ NULL,
  parent_task_id UUID NULL,
  root_task_id UUID NOT NULL,
  task_kind TEXT NOT NULL,
  initiative_id UUID NULL,
  planned_agent TEXT NULL,
  archived_at TIMESTAMPTZ NULL
);

CREATE TABLE state_transitions (
  id UUID PRIMARY KEY,
  task_id UUID NOT NULL,
  from_state TEXT NOT NULL,
  to_state TEXT NOT NULL,
  actor TEXT NOT NULL,
  reason TEXT NOT NULL,
  timestamp TIMESTAMPTZ NOT NULL
);

CREATE TABLE approvals (
  id UUID PRIMARY KEY,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  action_type TEXT NOT NULL,
  target_resource TEXT NOT NULL,
  status approvalstatus NOT NULL,
  operator TEXT NULL,
  timeout_seconds INTEGER NOT NULL,
  escalation_level INTEGER NOT NULL DEFAULT 1,
  requested_at TIMESTAMPTZ NOT NULL,
  resolved_at TIMESTAMPTZ NULL,
  timeout_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE initiatives (
  id UUID PRIMARY KEY,
  title TEXT NOT NULL,
  workspace_root TEXT NOT NULL,
  goal TEXT NOT NULL,
  status TEXT NOT NULL,
  current_phase TEXT NOT NULL,
  active_requirements_artifact_id UUID NULL,
  active_design_artifact_id UUID NULL,
  active_plan_artifact_id UUID NULL,
  created_by TEXT NOT NULL,
  execution_mode TEXT NOT NULL,
  archived_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE tasks
  ADD CONSTRAINT fk_tasks_initiative
  FOREIGN KEY (initiative_id) REFERENCES initiatives(id) ON DELETE SET NULL;

CREATE TABLE initiative_task_links (
  id UUID PRIMARY KEY,
  initiative_id UUID NOT NULL REFERENCES initiatives(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  phase_origin TEXT NOT NULL,
  epic TEXT NULL,
  launch_group TEXT NULL,
  execution_mode TEXT NOT NULL,
  launch_order INTEGER NOT NULL DEFAULT 10,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (initiative_id, task_id)
);

CREATE TABLE local_bridges (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  hostname TEXT NOT NULL,
  workspace_root TEXT NOT NULL,
  status TEXT NOT NULL,
  capabilities JSONB NOT NULL DEFAULT '{}'::jsonb,
  api_key_name TEXT NULL,
  last_heartbeat TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tool_invocations (
  id UUID PRIMARY KEY,
  task_id UUID NULL REFERENCES tasks(id) ON DELETE SET NULL,
  agent_type TEXT NULL,
  entrypoint TEXT NOT NULL,
  capability TEXT NOT NULL,
  input_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  output_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  status TEXT NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
  artifact_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
  error_message TEXT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE artifacts (
  id UUID PRIMARY KEY,
  task_id UUID NULL REFERENCES tasks(id) ON DELETE SET NULL,
  invocation_id UUID NULL REFERENCES tool_invocations(id) ON DELETE SET NULL,
  artifact_type TEXT NOT NULL,
  title TEXT NULL,
  uri TEXT NULL,
  media_type TEXT NULL,
  content_text TEXT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

`
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
}

func postJSONStatus(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int, out any) {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: expected status %d, got %d body=%s", method, path, wantStatus, rec.Code, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode response: %v body=%s", method, path, err, rec.Body.String())
		}
	}
}

func claimNextBridgeTask(t *testing.T, handler http.Handler, bridgeID string) *domain.LocalBridgeTaskClaimResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/bridges/%s/claim-next", bridgeID), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim next: expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "null" || body == "" {
		return nil
	}
	var claim domain.LocalBridgeTaskClaimResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &claim); err != nil {
		t.Fatalf("claim next decode failed: %v body=%s", err, rec.Body.String())
	}
	return &claim
}

func firstPendingApproval(t *testing.T, handler http.Handler) *domain.ApprovalResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/approvals?status_filter=pending&limit=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list approvals: expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp domain.ApprovalListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("list approvals decode failed: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Items) == 0 {
		return nil
	}
	return &resp.Items[0]
}

func approveApprovalForTest(t *testing.T, handler http.Handler, approvalID string) {
	t.Helper()
	postJSONStatus(t, handler, http.MethodPost, fmt.Sprintf("/approvals/%s/approve", approvalID), domain.ApprovalResolveRequest{
		Operator: "objective-e2e",
	}, http.StatusOK, nil)
}

func runCommand(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %v failed: %v\n%s", cmd.Args, err, string(output))
	}
	return string(output)
}

func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed in %s: %v\n%s", args, dir, err, string(output))
	}
}
