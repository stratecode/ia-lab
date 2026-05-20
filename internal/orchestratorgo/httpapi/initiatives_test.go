package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestSelectInitiativeTasksFiltersByTaskIDAndGroup(t *testing.T) {
	items := []domain.InitiativeTaskLinkResponse{
		{TaskID: "task-1", LaunchGroup: initiativeTestStringPtr("delivery")},
		{TaskID: "task-2", LaunchGroup: initiativeTestStringPtr("review")},
		{TaskID: "task-3", Epic: initiativeTestStringPtr("manual")},
	}

	byTask := selectInitiativeTasks(items, []string{"task-2"}, nil)
	if len(byTask) != 1 || byTask[0].TaskID != "task-2" {
		t.Fatalf("unexpected task-id filter result: %#v", byTask)
	}

	byGroup := selectInitiativeTasks(items, nil, []string{"delivery", "manual"})
	if len(byGroup) != 2 {
		t.Fatalf("unexpected group filter result: %#v", byGroup)
	}
}

func initiativeTestStringPtr(value string) *string { return &value }

func TestBuildPhaseHistoryIncludesVersionAndDiff(t *testing.T) {
	now := time.Now().UTC()
	initiative := &domain.InitiativeResponse{}
	jsonID := "artifact-1"
	initiative.ActiveRequirementsArtifactID = &jsonID
	reviews := []domain.InitiativePhaseReviewResponse{{
		ID:             "review-1",
		Phase:          domain.InitiativePhaseRequirements,
		Decision:       domain.InitiativeReviewGenerated,
		ArtifactJSONID: &jsonID,
		CreatedAt:      now,
	}}
	artifacts := []domain.ArtifactResponse{{
		ID:           jsonID,
		ArtifactType: "initiative_requirements.json",
		Metadata: map[string]any{
			"version":      2,
			"diff_summary": "changed: scope",
		},
	}}
	history := buildPhaseHistory(initiative, domain.InitiativePhaseRequirements, reviews, artifacts)
	if history.ActiveVersion != 2 {
		t.Fatalf("expected active version 2, got %d", history.ActiveVersion)
	}
	if len(history.Items) != 1 || history.Items[0].Version != 2 {
		t.Fatalf("unexpected history items: %#v", history.Items)
	}
	if history.Items[0].DiffSummary == nil || !strings.Contains(*history.Items[0].DiffSummary, "scope") {
		t.Fatalf("expected diff summary, got %#v", history.Items[0].DiffSummary)
	}
}

func TestBuildInitiativeExecutionSummaryCountsModesAndStates(t *testing.T) {
	item := &domain.InitiativeResponse{Status: domain.InitiativeStatusExecutionReady}
	summary := buildInitiativeExecutionSummary(item, []domain.InitiativeTaskLinkResponse{
		{ExecutionMode: domain.TaskLaunchModeManual, Task: domain.TaskResponse{State: domain.TaskStateCreated}},
		{ExecutionMode: domain.TaskLaunchModeAgentLocal, Task: domain.TaskResponse{State: domain.TaskStateCompleted}},
	})
	if !summary.BacklogMaterialized || summary.TaskCount != 2 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if summary.PendingManual != 1 {
		t.Fatalf("expected one pending manual task, got %#v", summary)
	}
	if summary.ModeCounts[domain.TaskLaunchModeAgentLocal] != 1 {
		t.Fatalf("expected local mode count, got %#v", summary.ModeCounts)
	}
}
