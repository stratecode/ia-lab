package initiative

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/config"
	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

func TestAutonomousRunResultCapturesLifecycle(t *testing.T) {
	result := domain.AutonomousRunResult{
		InitiativeID:  "initiative-1",
		WorkspaceRoot: "/srv/workspaces/repo",
		Status:        domain.InitiativeStatusExecutionReady,
		CurrentPhase:  domain.InitiativePhaseExecution,
		TasksCreated:  3,
		TasksLaunched: 2,
		PendingAction: "approval_required",
	}

	if result.InitiativeID != "initiative-1" {
		t.Fatalf("unexpected initiative id: %q", result.InitiativeID)
	}
	if result.Status != domain.InitiativeStatusExecutionReady {
		t.Fatalf("unexpected status: %s", result.Status)
	}
	if result.PendingAction == "" {
		t.Fatal("expected pending action to be populated")
	}
}

func TestStartFromChannelRunsInitiativeToLaunch(t *testing.T) {
	store := newFakeAutonomousStore()
	queue := &fakeTaskQueue{}
	generator := fakePhaseGenerator{}
	runner := NewAutonomousRunner(config.Config{WorkspaceRoot: "/srv/default"}, store, queue, generator)

	result, err := runner.StartFromChannel(context.Background(), domain.AutonomousInitiativeRequest{
		Surface:           "openclaw.telegram",
		WorkspaceRoot:     "/srv/workspaces/repo",
		Goal:              "Implementa un fix minimo",
		OperatorID:        "telegram:user-1",
		AutoApprovePhases: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InitiativeID == "" {
		t.Fatal("expected initiative id")
	}
	if result.TasksCreated != 2 {
		t.Fatalf("expected 2 generated tasks, got %d", result.TasksCreated)
	}
	if result.TasksLaunched != 1 {
		t.Fatalf("expected 1 launched task, got %d", result.TasksLaunched)
	}
	if result.Status != domain.InitiativeStatusExecutionReady && result.Status != domain.InitiativeStatusExecuting {
		t.Fatalf("unexpected final status: %s", result.Status)
	}
	if len(store.phaseReviews) != 6 {
		t.Fatalf("expected generated+approved reviews, got %d", len(store.phaseReviews))
	}
}

func TestStartFromChannelRejectsEmptyGoal(t *testing.T) {
	runner := NewAutonomousRunner(config.Config{WorkspaceRoot: "/srv/default"}, newFakeAutonomousStore(), &fakeTaskQueue{}, fakePhaseGenerator{})
	_, err := runner.StartFromChannel(context.Background(), domain.AutonomousInitiativeRequest{})
	if err == nil || err.Error() != "goal is required" {
		t.Fatalf("expected goal error, got %v", err)
	}
}

func TestStartFromChannelUsesDefaultWorkspaceWhenRequestIsEmpty(t *testing.T) {
	store := newFakeAutonomousStore()
	runner := NewAutonomousRunner(config.Config{WorkspaceRoot: "/srv/default"}, store, &fakeTaskQueue{}, fakePhaseGenerator{})
	_, err := runner.StartFromChannel(context.Background(), domain.AutonomousInitiativeRequest{
		Surface:           "openclaw.telegram",
		Goal:              "Implementa un fix minimo",
		OperatorID:        "telegram:user-1",
		AutoApprovePhases: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.initiatives[0].WorkspaceRoot != "/srv/default" {
		t.Fatalf("expected default workspace root, got %q", store.initiatives[0].WorkspaceRoot)
	}
}

type fakePhaseGenerator struct{}

func (fakePhaseGenerator) GenerateRequirements(_ context.Context, initiative *domain.InitiativeResponse, _ string) (PhaseArtifacts, error) {
	return PhaseArtifacts{
		Markdown: "# Requirements",
		JSON: map[string]any{
			"objective":           initiative.Goal,
			"scope":               []any{"fix"},
			"out_of_scope":        []any{"none"},
			"constraints":         []any{"tests"},
			"risks":               []any{"regression"},
			"acceptance_criteria": []any{"pass"},
			"open_questions":      []any{"none"},
			"assumptions":         []any{"repo available"},
		},
	}, nil
}

func (fakePhaseGenerator) GenerateDesign(_ context.Context, _ *domain.InitiativeResponse, _ map[string]any, _ string) (PhaseArtifacts, error) {
	return PhaseArtifacts{
		Markdown: "# Design",
		JSON: map[string]any{
			"architecture":      "small change",
			"components":        []any{"runner"},
			"interfaces":        []any{"command"},
			"data_model":        []any{"initiative"},
			"testing_strategy":  []any{"go test"},
			"technical_risks":   []any{"none"},
			"pending_decisions": []any{"none"},
		},
	}, nil
}

func (fakePhaseGenerator) GenerateExecutionPlan(_ context.Context, _ *domain.InitiativeResponse, _ map[string]any, _ string) (PhaseArtifacts, error) {
	return PhaseArtifacts{
		Markdown: "# Plan",
		JSON: map[string]any{
			"epics": []any{
				map[string]any{
					"name":  "Build",
					"group": "build",
					"tasks": []any{
						map[string]any{
							"title":              "Implement code",
							"description":        "Change code",
							"suggested_agent":    "coder",
							"priority":           "normal",
							"execution_mode":     "agent_local",
							"execution_target":   "local",
							"approval_required":  false,
							"definition_of_done": "code updated",
							"metadata":           map[string]any{},
						},
						map[string]any{
							"title":              "Manual check",
							"description":        "Review output",
							"suggested_agent":    "reviewer",
							"priority":           "normal",
							"execution_mode":     "manual",
							"execution_target":   "local",
							"approval_required":  false,
							"definition_of_done": "review done",
							"metadata":           map[string]any{},
						},
					},
				},
			},
		},
	}, nil
}

type fakeTaskQueue struct {
	enqueued []string
}

func (f *fakeTaskQueue) EnqueueTask(_ context.Context, taskID string, _ domain.AgentType, _ domain.Priority) error {
	f.enqueued = append(f.enqueued, taskID)
	return nil
}

type fakeAutonomousStore struct {
	initiatives  []*domain.InitiativeResponse
	artifacts    map[string]domain.ArtifactResponse
	phaseReviews []store.CreateInitiativePhaseReviewParams
	links        []domain.InitiativeTaskLinkResponse
	tasks        map[string]*domain.TaskResponse
}

func newFakeAutonomousStore() *fakeAutonomousStore {
	return &fakeAutonomousStore{
		artifacts: map[string]domain.ArtifactResponse{},
		tasks:     map[string]*domain.TaskResponse{},
	}
}

func (f *fakeAutonomousStore) CreateInitiative(_ context.Context, params store.CreateInitiativeParams) (*domain.InitiativeResponse, error) {
	item := &domain.InitiativeResponse{
		ID:            fmt.Sprintf("initiative-%d", len(f.initiatives)+1),
		Title:         params.Title,
		WorkspaceRoot: params.WorkspaceRoot,
		Goal:          params.Goal,
		Status:        domain.InitiativeStatusIdeaSubmitted,
		CurrentPhase:  domain.InitiativePhaseRequirements,
		CreatedBy:     params.CreatedBy,
		ExecutionMode: params.ExecutionMode,
	}
	f.initiatives = append(f.initiatives, item)
	return item, nil
}

func (f *fakeAutonomousStore) GetInitiative(_ context.Context, initiativeID string) (*domain.InitiativeResponse, error) {
	for _, item := range f.initiatives {
		if item.ID == initiativeID {
			return item, nil
		}
	}
	return nil, nil
}

func (f *fakeAutonomousStore) UpdateInitiative(_ context.Context, initiativeID string, params store.UpdateInitiativeParams) (*domain.InitiativeResponse, error) {
	item, _ := f.GetInitiative(context.Background(), initiativeID)
	if item == nil {
		return nil, fmt.Errorf("not found")
	}
	if params.Status != nil {
		item.Status = *params.Status
	}
	if params.CurrentPhase != nil {
		item.CurrentPhase = *params.CurrentPhase
	}
	if params.ActiveRequirementsArtifactID != nil {
		item.ActiveRequirementsArtifactID = params.ActiveRequirementsArtifactID
	}
	if params.ActiveDesignArtifactID != nil {
		item.ActiveDesignArtifactID = params.ActiveDesignArtifactID
	}
	if params.ActivePlanArtifactID != nil {
		item.ActivePlanArtifactID = params.ActivePlanArtifactID
	}
	return item, nil
}

func (f *fakeAutonomousStore) ListInitiativeTasks(_ context.Context, initiativeID string) ([]domain.InitiativeTaskLinkResponse, error) {
	out := []domain.InitiativeTaskLinkResponse{}
	for _, link := range f.links {
		if link.InitiativeID == initiativeID {
			out = append(out, link)
		}
	}
	return out, nil
}

func (f *fakeAutonomousStore) UpdateTaskLaunchMode(_ context.Context, taskID, mode string) error {
	task := f.tasks[taskID]
	if task == nil {
		return fmt.Errorf("task not found")
	}
	if mode == domain.TaskLaunchModeAgentLocal {
		task.ExecutionTarget = domain.ExecutionTargetLocal
	}
	return nil
}

func (f *fakeAutonomousStore) UpdateInitiativeTaskMode(_ context.Context, initiativeID, taskID, mode string) (*domain.InitiativeTaskLinkResponse, error) {
	for i := range f.links {
		if f.links[i].InitiativeID == initiativeID && f.links[i].TaskID == taskID {
			f.links[i].ExecutionMode = mode
			return &f.links[i], nil
		}
	}
	return nil, fmt.Errorf("link not found")
}

func (f *fakeAutonomousStore) QueueTaskForLaunch(_ context.Context, taskID, _, _ string) (*domain.TaskResponse, error) {
	task := f.tasks[taskID]
	if task == nil {
		return nil, fmt.Errorf("task not found")
	}
	task.State = domain.TaskStateQueued
	return task, nil
}

func (f *fakeAutonomousStore) ReconcileInitiativeExecution(_ context.Context, initiativeID string) (*domain.InitiativeResponse, error) {
	item, _ := f.GetInitiative(context.Background(), initiativeID)
	if item == nil {
		return nil, fmt.Errorf("not found")
	}
	item.Status = domain.InitiativeStatusExecuting
	item.CurrentPhase = domain.InitiativePhaseExecution
	return item, nil
}

func (f *fakeAutonomousStore) CreateInitiativePhaseReview(_ context.Context, params store.CreateInitiativePhaseReviewParams) (*domain.InitiativePhaseReviewResponse, error) {
	f.phaseReviews = append(f.phaseReviews, params)
	return &domain.InitiativePhaseReviewResponse{InitiativeID: params.InitiativeID, Phase: params.Phase, Decision: params.Decision}, nil
}

func (f *fakeAutonomousStore) CreateArtifact(_ context.Context, params store.CreateArtifactParams) (string, error) {
	id := fmt.Sprintf("artifact-%d", len(f.artifacts)+1)
	f.artifacts[id] = domain.ArtifactResponse{ID: id, ContentText: params.ContentText, Metadata: params.Metadata}
	return id, nil
}

func (f *fakeAutonomousStore) ListArtifactsByIDs(_ context.Context, ids []string) ([]domain.ArtifactResponse, error) {
	out := []domain.ArtifactResponse{}
	for _, id := range ids {
		if item, ok := f.artifacts[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeAutonomousStore) CreateTask(_ context.Context, params store.CreateTaskParams) (*domain.TaskResponse, error) {
	id := fmt.Sprintf("task-%d", len(f.tasks)+1)
	task := &domain.TaskResponse{
		ID:              id,
		State:           domain.TaskStateCreated,
		Description:     params.Description,
		Metadata:        params.Metadata,
		Priority:        params.Priority,
		ExecutionTarget: params.ExecutionTarget,
		InitiativeID:    params.InitiativeID,
		WorkspacePath:   params.WorkspacePath,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if params.AssignedAgent != "" {
		task.AssignedAgent = &params.AssignedAgent
	}
	f.tasks[id] = task
	return task, nil
}

func (f *fakeAutonomousStore) LinkInitiativeTask(_ context.Context, params store.LinkInitiativeTaskParams) (*domain.InitiativeTaskLinkResponse, error) {
	task := f.tasks[params.TaskID]
	link := domain.InitiativeTaskLinkResponse{
		InitiativeID:  params.InitiativeID,
		TaskID:        params.TaskID,
		PhaseOrigin:   params.PhaseOrigin,
		Epic:          params.Epic,
		LaunchGroup:   params.LaunchGroup,
		ExecutionMode: params.ExecutionMode,
		LaunchOrder:   params.LaunchOrder,
		Task:          *task,
	}
	f.links = append(f.links, link)
	return &link, nil
}

func TestGetActiveInitiativeArtifactJSONReadsStoredPayload(t *testing.T) {
	fakeStore := newFakeAutonomousStore()
	raw, _ := json.Marshal(map[string]any{"objective": "test"})
	id, err := fakeStore.CreateArtifact(context.Background(), store.CreateArtifactParams{
		ContentText: cleanAutonomousStringPtr(string(raw)),
		Metadata:    map[string]any{"payload": map[string]any{"objective": "test"}},
	})
	if err != nil {
		t.Fatalf("unexpected artifact error: %v", err)
	}
	runner := NewAutonomousRunner(config.Config{}, fakeStore, &fakeTaskQueue{}, fakePhaseGenerator{})
	payload, err := runner.getActiveInitiativeArtifactJSON(context.Background(), &id)
	if err != nil {
		t.Fatalf("unexpected read error: %v", err)
	}
	if payload["objective"] != "test" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
