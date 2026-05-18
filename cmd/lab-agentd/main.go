package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/stratecode/lab/internal/orchestratorgo/bridge"
)

func main() {
	opts := defaultOptions()
	fs := flag.NewFlagSet("lab-agentd", flag.ExitOnError)
	fs.StringVar(&opts.BaseURL, "base-url", opts.BaseURL, "Orchestrator base URL")
	fs.StringVar(&opts.APIKey, "api-key", opts.APIKey, "Operator API key")
	fs.StringVar(&opts.BridgeID, "bridge-id", opts.BridgeID, "Stable bridge UUID")
	fs.StringVar(&opts.WorkspaceRoot, "workspace-root", opts.WorkspaceRoot, "Workspace root")
	fs.StringVar(&opts.Name, "name", opts.Name, "Bridge name")
	fs.StringVar(&opts.Hostname, "hostname", opts.Hostname, "Host name override")
	fs.DurationVar(&opts.PollInterval, "poll-interval", opts.PollInterval, "Poll interval")
	fs.DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", opts.HeartbeatInterval, "Heartbeat interval")
	fs.Parse(os.Args[1:])

	if strings.TrimSpace(opts.APIKey) == "" {
		fmt.Fprintln(os.Stderr, "LAB_AGENT_API_KEY or --api-key is required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	daemon, err := bridge.NewDaemon(bridge.DaemonOptions{
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
		fmt.Fprintf(os.Stderr, "failed to initialize lab-agentd: %v\n", err)
		os.Exit(1)
	}
	if err := daemon.Run(ctx); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "lab-agentd failed: %v\n", err)
		os.Exit(1)
	}
}

func defaultOptions() bridge.DaemonOptions {
	return bridge.DaemonOptions{
		BaseURL:           getenv("LAB_AGENT_BASE_URL", "http://127.0.0.1:8100"),
		APIKey:            os.Getenv("LAB_AGENT_API_KEY"),
		BridgeID:          getenv("LAB_AGENT_BRIDGE_ID", ""),
		WorkspaceRoot:     getenv("LAB_AGENT_WORKSPACE_ROOT", "."),
		Name:              getenv("LAB_AGENT_NAME", "lab-agentd"),
		Hostname:          getenv("LAB_AGENT_HOSTNAME", ""),
		PollInterval:      parseDurationEnv("LAB_AGENT_POLL_INTERVAL", 2*time.Second),
		HeartbeatInterval: parseDurationEnv("LAB_AGENT_HEARTBEAT_INTERVAL", 15*time.Second),
	}
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}
