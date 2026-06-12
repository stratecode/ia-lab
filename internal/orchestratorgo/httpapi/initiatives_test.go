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

func TestBuildInitiativeObjectiveRuntimeViewSelectsLatestArtifacts(t *testing.T) {
	now := time.Now().UTC()
	reviewSnapshot := `{"initiative_id":"initiative-1","iteration":2,"max_iterations":3,"remaining_retries":1,"work_item_kind":"review","next_expected_action":"close_initiative"}`
	editSnapshot := `{"initiative_id":"initiative-1","iteration":2,"max_iterations":3,"remaining_retries":1,"work_item_kind":"edit","next_expected_action":"validate"}`
	artifacts := []domain.ArtifactResponse{
		{ID: "coder-1", ArtifactType: "coder_packet", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "coder-2", ArtifactType: "coder_packet", CreatedAt: now.Add(-1 * time.Minute)},
		{ID: "review-1", ArtifactType: "review_packet", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: "retrieval-1", ArtifactType: "retrieval_packet", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "planning-1", ArtifactType: "planning_retrieval_packet", CreatedAt: now.Add(-5 * time.Minute)},
		{ID: "repair-1", ArtifactType: "objective_repair_signal", CreatedAt: now.Add(-90 * time.Second)},
		{ID: "snapshot-edit", ArtifactType: "objective_status_snapshot", ContentText: initiativeTestStringPtr(editSnapshot), CreatedAt: now.Add(-30 * time.Second)},
		{ID: "snapshot-review", ArtifactType: "objective_status_snapshot", ContentText: initiativeTestStringPtr(reviewSnapshot), CreatedAt: now.Add(-45 * time.Second)},
	}

	view := buildInitiativeObjectiveRuntimeView(artifacts)
	if view == nil {
		t.Fatal("expected objective runtime view")
	}
	if view.LatestCoderPacket == nil || view.LatestCoderPacket.ID != "coder-2" {
		t.Fatalf("expected latest coder packet, got %#v", view)
	}
	if view.LatestReviewPacket == nil || view.LatestReviewPacket.ID != "review-1" {
		t.Fatalf("expected latest review packet, got %#v", view)
	}
	if view.LatestRetrievalPacket == nil || view.LatestRetrievalPacket.ID != "retrieval-1" {
		t.Fatalf("expected latest retrieval packet, got %#v", view)
	}
	if view.LatestPlanningRetrievalPacket == nil || view.LatestPlanningRetrievalPacket.ID != "planning-1" {
		t.Fatalf("expected latest planning retrieval packet, got %#v", view)
	}
	if view.LatestRepairSignal == nil || view.LatestRepairSignal.ID != "repair-1" {
		t.Fatalf("expected latest repair signal, got %#v", view)
	}
	if view.LatestStatusSnapshot == nil || view.LatestStatusSnapshot.WorkItemKind != "review" {
		t.Fatalf("expected review snapshot to outrank newer edit snapshot, got %#v", view)
	}
}
