package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type ApprovalMode string

const (
	ApprovalModeManual    ApprovalMode = "manual"
	ApprovalModeLocalOnly ApprovalMode = "local_only"
)

type ObjectiveRunRequest struct {
	Title             string
	Objective         string
	WorkspaceRoot     string
	CreatedBy         string
	TimeBudgetSeconds int
}

type ObjectiveRunResult struct {
	Objective            *domain.ObjectiveResponse       `json:"objective,omitempty"`
	Initiative           *domain.InitiativeResponse      `json:"initiative,omitempty"`
	LatestStatusSnapshot *domain.ObjectiveStatusSnapshot `json:"latest_status_snapshot,omitempty"`
	BridgeID             string                          `json:"bridge_id"`
	WorkspaceRoot        string                          `json:"workspace_root"`
	ProcessedTasks       int                             `json:"processed_tasks"`
	ResolvedApprovals    int                             `json:"resolved_approvals"`
	StartedAt            time.Time                       `json:"started_at"`
	CompletedAt          time.Time                       `json:"completed_at"`
	TerminalStatus       string                          `json:"terminal_status"`
	LastBlockingReason   *string                         `json:"last_blocking_reason,omitempty"`
}

type ObjectiveRunner struct {
	client         *Client
	executor       ClaimExecutor
	bridgeID       string
	workspaceRoot  string
	name           string
	hostname       string
	approvalMode   ApprovalMode
	pollInterval   time.Duration
	heartbeatEvery time.Duration
	waitTimeout    time.Duration
}

func RunObjective(ctx context.Context, opts CLIOptions, customExecutor ...ClaimExecutor) error {
	executor := ClaimExecutor(nil)
	if len(customExecutor) > 0 {
		executor = customExecutor[0]
	}
	if executor == nil {
		workspaceExecutor, err := NewWorkspaceExecutor(normalizedWorkspaceRoot(opts.WorkspaceRoot))
		if err != nil {
			return err
		}
		executor = workspaceExecutor
	}
	runner := &ObjectiveRunner{
		client:         NewClient(opts.BaseURL, opts.APIKey, 30*time.Second),
		executor:       executor,
		bridgeID:       normalizedBridgeID(opts.BridgeID),
		workspaceRoot:  normalizedWorkspaceRoot(opts.WorkspaceRoot),
		name:           firstNonEmptyString(opts.Name, "lab-agent"),
		hostname:       firstNonEmptyString(opts.Hostname, defaultHostname()),
		approvalMode:   approvalModeOrDefault(opts.ApprovalMode),
		pollInterval:   durationOrDefault(opts.PollInterval, 2*time.Second),
		heartbeatEvery: durationOrDefault(opts.HeartbeatInterval, 15*time.Second),
		waitTimeout:    durationOrDefault(opts.WaitTimeout, 30*time.Minute),
	}
	result, err := runner.Run(ctx, ObjectiveRunRequest{
		Title:             strings.TrimSpace(opts.ObjectiveTitle),
		Objective:         strings.TrimSpace(opts.Objective),
		WorkspaceRoot:     runner.workspaceRoot,
		CreatedBy:         firstNonEmptyString(strings.TrimSpace(opts.CreatedBy), "lab-agent"),
		TimeBudgetSeconds: durationSeconds(opts.ObjectiveTimeBudget),
	})
	if result.Initiative != nil {
		result.CompletedAt = time.Now().UTC()
		result.TerminalStatus = string(result.Initiative.Status)
	}
	if printErr := printJSON(result); printErr != nil {
		return printErr
	}
	return err
}

func (r *ObjectiveRunner) Run(ctx context.Context, req ObjectiveRunRequest) (ObjectiveRunResult, error) {
	req.Objective = strings.TrimSpace(req.Objective)
	req.WorkspaceRoot = firstNonEmptyString(strings.TrimSpace(req.WorkspaceRoot), r.workspaceRoot)
	if req.Objective == "" {
		return ObjectiveRunResult{}, fmt.Errorf("objective is required")
	}
	if r.pollInterval <= 0 {
		r.pollInterval = 2 * time.Second
	}
	if r.heartbeatEvery <= 0 {
		r.heartbeatEvery = 15 * time.Second
	}
	if r.waitTimeout <= 0 {
		r.waitTimeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, r.waitTimeout)
	defer cancel()

	result := ObjectiveRunResult{
		BridgeID:      r.bridgeID,
		WorkspaceRoot: req.WorkspaceRoot,
		StartedAt:     time.Now().UTC(),
	}
	daemon := &Daemon{
		client:         r.client,
		executor:       r.executor,
		bridgeID:       r.bridgeID,
		workspaceRoot:  req.WorkspaceRoot,
		name:           r.name,
		hostname:       r.hostname,
		pollInterval:   r.pollInterval,
		heartbeatEvery: r.heartbeatEvery,
	}
	if err := daemon.register(ctx); err != nil {
		return result, err
	}
	objective, err := r.client.CreateObjective(ctx, domain.ObjectiveRequest{
		Title:             strings.TrimSpace(req.Title),
		Objective:         req.Objective,
		WorkspaceRoot:     req.WorkspaceRoot,
		CreatedBy:         firstNonEmptyString(strings.TrimSpace(req.CreatedBy), "lab-agent"),
		TimeBudgetSeconds: req.TimeBudgetSeconds,
	})
	if err != nil {
		return result, err
	}
	result.Objective = objective
	if objective.Initiative == nil {
		return result, fmt.Errorf("objective response did not include initiative")
	}

	for {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		detail, err := r.client.GetInitiative(ctx, objective.Initiative.ID)
		if err != nil {
			return result, err
		}
		if detail == nil || detail.Initiative == nil {
			return result, fmt.Errorf("initiative %s not found", objective.Initiative.ID)
		}
		result.Initiative = detail.Initiative
		if detail.ObjectiveRuntime != nil && detail.ObjectiveRuntime.LatestStatusSnapshot != nil {
			result.LatestStatusSnapshot = detail.ObjectiveRuntime.LatestStatusSnapshot
		} else if snapshot, err := r.loadLatestStatusSnapshot(ctx, objective.Initiative.ID); err == nil {
			result.LatestStatusSnapshot = snapshot
		}
		switch detail.Initiative.Status {
		case domain.InitiativeStatusCompleted:
			return result, nil
		case domain.InitiativeStatusBlocked, domain.InitiativeStatusCancelled:
			message := fmt.Sprintf("initiative %s ended in %s", detail.Initiative.ID, detail.Initiative.Status)
			if result.LatestStatusSnapshot != nil && strings.TrimSpace(result.LatestStatusSnapshot.BlockerReason) != "" {
				message = result.LatestStatusSnapshot.BlockerReason
			}
			result.LastBlockingReason = &message
			return result, fmt.Errorf(message)
		}

		resolved, err := r.resolveApprovals(ctx, detail.Initiative.ID)
		if err != nil {
			return result, err
		}
		result.ResolvedApprovals += resolved

		claim, err := r.client.ClaimNext(ctx, r.bridgeID)
		if err != nil {
			return result, err
		}
		if claim != nil {
			if _, err := daemon.executeClaim(ctx, *claim); err != nil {
				return result, err
			}
			result.ProcessedTasks++
			continue
		}

		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(r.pollInterval):
		}
	}
}

func durationSeconds(value time.Duration) int {
	if value <= 0 {
		return 0
	}
	return int(value / time.Second)
}

func (r *ObjectiveRunner) loadLatestStatusSnapshot(ctx context.Context, initiativeID string) (*domain.ObjectiveStatusSnapshot, error) {
	artifacts, err := r.client.GetInitiativeArtifacts(ctx, initiativeID)
	if err != nil || len(artifacts) == 0 {
		return nil, err
	}
	var latest *domain.ObjectiveStatusSnapshot
	var latestCreatedAt time.Time
	for _, artifact := range artifacts {
		if artifact.ArtifactType != "objective_status_snapshot" || artifact.ContentText == nil {
			continue
		}
		var snapshot domain.ObjectiveStatusSnapshot
		if err := json.Unmarshal([]byte(*artifact.ContentText), &snapshot); err != nil {
			continue
		}
		if latest == nil || objectiveStatusSnapshotPrecedes(*latest, latestCreatedAt, snapshot, artifact.CreatedAt) {
			candidate := snapshot
			latest = &candidate
			latestCreatedAt = artifact.CreatedAt
		}
	}
	return latest, nil
}

func objectiveStatusSnapshotPrecedes(current domain.ObjectiveStatusSnapshot, currentCreatedAt time.Time, candidate domain.ObjectiveStatusSnapshot, candidateCreatedAt time.Time) bool {
	if candidate.Iteration != current.Iteration {
		return candidate.Iteration > current.Iteration
	}
	currentRank := objectiveStatusSnapshotRank(current)
	candidateRank := objectiveStatusSnapshotRank(candidate)
	if candidateRank != currentRank {
		return candidateRank > currentRank
	}
	return candidateCreatedAt.After(currentCreatedAt)
}

func objectiveStatusSnapshotRank(snapshot domain.ObjectiveStatusSnapshot) int {
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

func (r *ObjectiveRunner) resolveApprovals(ctx context.Context, initiativeID string) (int, error) {
	if r.approvalMode != ApprovalModeLocalOnly {
		return 0, nil
	}
	waiting, err := r.client.ListTasksFiltered(ctx, map[string]string{
		"initiative_id": initiativeID,
		"state":         string(domain.TaskStateWaitingApproval),
	})
	if err != nil {
		return 0, err
	}
	if waiting == nil || len(waiting.Items) == 0 {
		return 0, nil
	}
	waitingTaskIDs := make(map[string]struct{}, len(waiting.Items))
	for _, item := range waiting.Items {
		waitingTaskIDs[item.ID] = struct{}{}
	}
	approvals, err := r.client.ListApprovals(ctx)
	if err != nil {
		return 0, err
	}
	resolved := 0
	for _, approval := range approvals.Items {
		if approval.Status != domain.ApprovalPending {
			continue
		}
		if approval.ActionType != "local_bridge_tool" {
			continue
		}
		if _, ok := waitingTaskIDs[approval.TaskID]; !ok {
			continue
		}
		if _, err := r.client.ResolveApproval(ctx, approval.ID, "lab-agent", true); err != nil {
			return resolved, err
		}
		resolved++
	}
	return resolved, nil
}

func approvalModeOrDefault(value string) ApprovalMode {
	switch ApprovalMode(strings.TrimSpace(value)) {
	case ApprovalModeLocalOnly:
		return ApprovalModeLocalOnly
	case ApprovalModeManual:
		return ApprovalModeManual
	default:
		return ApprovalModeLocalOnly
	}
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}
