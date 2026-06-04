package bridge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type DaemonOptions struct {
	BaseURL           string
	APIKey            string
	BridgeID          string
	WorkspaceRoot     string
	Name              string
	Hostname          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
}

type Daemon struct {
	client         *Client
	executor       *WorkspaceExecutor
	bridgeID       string
	workspaceRoot  string
	name           string
	hostname       string
	pollInterval   time.Duration
	heartbeatEvery time.Duration
	currentTaskID  *string
	currentStage   *string
	currentTool    *string
	currentSummary *string
}

func NewDaemon(opts DaemonOptions) (*Daemon, error) {
	workspaceRoot, err := filepath.Abs(firstNonEmptyString(strings.TrimSpace(opts.WorkspaceRoot), "."))
	if err != nil {
		return nil, err
	}
	executor, err := NewWorkspaceExecutor(workspaceRoot)
	if err != nil {
		return nil, err
	}
	bridgeID := stringsTrim(opts.BridgeID, uuid.NewString())
	name := stringsTrim(opts.Name, "lab-agentd")
	hostname := stringsTrim(opts.Hostname, defaultHostname())
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	heartbeatEvery := opts.HeartbeatInterval
	if heartbeatEvery <= 0 {
		heartbeatEvery = 15 * time.Second
	}
	return &Daemon{
		client:         NewClient(opts.BaseURL, opts.APIKey, 30*time.Second),
		executor:       executor,
		bridgeID:       bridgeID,
		workspaceRoot:  workspaceRoot,
		name:           name,
		hostname:       hostname,
		pollInterval:   pollInterval,
		heartbeatEvery: heartbeatEvery,
	}, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	if _, err := d.client.Register(ctx, domain.LocalBridgeRegisterRequest{
		BridgeID:      d.bridgeID,
		Name:          d.name,
		Hostname:      d.hostname,
		WorkspaceRoot: d.workspaceRoot,
		Capabilities:  bridgeCapabilities(),
	}); err != nil {
		return err
	}
	lastHeartbeat := time.Time{}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(lastHeartbeat) >= d.heartbeatEvery {
			if _, err := d.client.Heartbeat(ctx, d.bridgeID, "active", d.currentTaskID, intPtr(int(d.heartbeatEvery.Seconds())*3), d.currentStage, d.currentTool, d.currentSummary); err != nil {
				return err
			}
			lastHeartbeat = time.Now()
		}
		claim, err := d.client.ClaimNext(ctx, d.bridgeID)
		if err != nil {
			return err
		}
		if claim == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d.pollInterval):
				continue
			}
		}
		d.currentTaskID = &claim.TaskID
		d.currentStage = stringPtr("executing")
		d.currentTool = stringPtr(currentClaimTool(*claim))
		d.currentSummary = stringPtr(currentClaimSummary(*claim))
		type execOutcome struct {
			result  domain.LocalBridgeResultRequest
			execErr error
		}
		outcomeCh := make(chan execOutcome, 1)
		go func() {
			result, execErr := d.executor.Execute(ctx, *claim)
			outcomeCh <- execOutcome{result: result, execErr: execErr}
		}()
		var result domain.LocalBridgeResultRequest
		var execErr error
		executing := true
		for executing {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case outcome := <-outcomeCh:
				result = outcome.result
				execErr = outcome.execErr
				executing = false
			case <-time.After(d.heartbeatEvery):
				if _, err := d.client.Heartbeat(ctx, d.bridgeID, "active", d.currentTaskID, intPtr(int(d.heartbeatEvery.Seconds())*3), d.currentStage, d.currentTool, d.currentSummary); err != nil {
					return err
				}
				lastHeartbeat = time.Now()
			}
		}
		if execErr != nil {
			msg := execErr.Error()
			result = domain.LocalBridgeResultRequest{
				Status:       "error",
				Summary:      stringPtr("Local execution rejected"),
				Stderr:       &msg,
				ErrorMessage: &msg,
			}
		}
		if err := d.client.SubmitResult(ctx, d.bridgeID, claim.TaskID, result); err != nil {
			return fmt.Errorf("submit result for task %s: %w", claim.TaskID, err)
		}
		d.currentTaskID = nil
		d.currentStage = nil
		d.currentTool = nil
		d.currentSummary = nil
		log.Info().Str("bridge_id", d.bridgeID).Str("task_id", claim.TaskID).Str("status", result.Status).Msg("local bridge processed task")
	}
}

func currentClaimTool(claim domain.LocalBridgeTaskClaimResponse) string {
	toolRequest, ok := claim.Metadata["tool_request"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(asString(toolRequest["tool"]))
}

func currentClaimSummary(claim domain.LocalBridgeTaskClaimResponse) string {
	if summary := strings.TrimSpace(asString(claim.Metadata["coder_summary"])); summary != "" {
		return summary
	}
	if summary := strings.TrimSpace(asString(claim.Metadata["review_summary"])); summary != "" {
		return summary
	}
	return strings.TrimSpace(claim.Description)
}

func bridgeCapabilities() map[string]any {
	return map[string]any{
		"tools": []string{
			"read_file",
			"filesystem.read",
			"list_files",
			"filesystem.list",
			"write_file",
			"filesystem.write",
			"research_project",
			"scaffold_project",
			"review_project",
			"review_workspace",
			"apply_patch",
			"run_command",
			"git_status",
			"git_diff",
			"run_tests",
		},
	}
}

func defaultHostname() string {
	name, err := os.Hostname()
	if err != nil || stringsTrim(name, "") == "" {
		return "localhost"
	}
	return name
}

func stringsTrim(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
