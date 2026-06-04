package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type CLIOptions struct {
	BaseURL           string
	APIKey            string
	BridgeID          string
	WorkspaceRoot     string
	Name              string
	Hostname          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	ObjectiveTitle    string
	Objective         string
	CreatedBy         string
	ApprovalMode      string
	WaitTimeout       time.Duration
}

func RunRegister(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	record, err := client.Register(ctx, registerPayload(opts))
	if err != nil {
		return err
	}
	return printJSON(record)
}

func RunHeartbeat(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	record, err := client.Heartbeat(ctx, normalizedBridgeID(opts.BridgeID), "active", nil, nil, nil, nil, nil)
	if err != nil {
		return err
	}
	return printJSON(record)
}

func RunStatus(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	items, err := client.ListBridges(ctx)
	if err != nil {
		return err
	}
	target := normalizedBridgeID(opts.BridgeID)
	if target == "" {
		return printJSON(items)
	}
	for _, item := range items.Items {
		if item.ID == target {
			return printJSON(item)
		}
	}
	return fmt.Errorf("bridge %s not found", target)
}

func RunSmoke(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	if _, err := client.Register(ctx, registerPayload(opts)); err != nil {
		return err
	}
	if _, err := client.Heartbeat(ctx, normalizedBridgeID(opts.BridgeID), "active", nil, nil, nil, nil, nil); err != nil {
		return err
	}
	claim, err := client.ClaimNext(ctx, normalizedBridgeID(opts.BridgeID))
	if err != nil {
		return err
	}
	if claim == nil {
		fmt.Println("null")
		return nil
	}
	return printJSON(claim)
}

func RunStart(ctx context.Context, opts CLIOptions) error {
	daemon, err := NewDaemon(DaemonOptions{
		BaseURL:           opts.BaseURL,
		APIKey:            opts.APIKey,
		BridgeID:          opts.BridgeID,
		WorkspaceRoot:     opts.WorkspaceRoot,
		Name:              opts.Name,
		Hostname:          opts.Hostname,
		PollInterval:      opts.PollInterval,
		HeartbeatInterval: opts.HeartbeatInterval,
	})
	if err != nil {
		return err
	}
	return daemon.Run(ctx)
}

func RunTasksList(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	items, err := client.ListTasks(ctx, "local")
	if err != nil {
		return err
	}
	return printJSON(items)
}

func RunTasksWatch(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		items, err := client.ListTasks(ctx, "local")
		if err != nil {
			return err
		}
		if err := printJSON(items); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func RunApprovalsList(ctx context.Context, opts CLIOptions) error {
	client := NewClient(opts.BaseURL, opts.APIKey, 20*time.Second)
	items, err := client.ListApprovals(ctx)
	if err != nil {
		return err
	}
	return printJSON(items)
}

func registerPayload(opts CLIOptions) domain.LocalBridgeRegisterRequest {
	return domain.LocalBridgeRegisterRequest{
		BridgeID:      normalizedBridgeID(opts.BridgeID),
		Name:          firstNonEmptyString(opts.Name, "lab-agentd"),
		Hostname:      firstNonEmptyString(opts.Hostname, defaultHostname()),
		WorkspaceRoot: normalizedWorkspaceRoot(opts.WorkspaceRoot),
		Capabilities:  bridgeCapabilities(),
	}
}

func normalizedBridgeID(value string) string {
	value = stringsTrim(value, "")
	if value == "" {
		return uuid.NewString()
	}
	return value
}

func normalizedWorkspaceRoot(value string) string {
	value = stringsTrim(value, "")
	if value == "" {
		cwd, _ := os.Getwd()
		value = cwd
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return abs
}

func printJSON(value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(raw))
	return nil
}
