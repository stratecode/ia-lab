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
	envFile, filteredArgs, err := bridge.ExtractEnvFileArg(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		usage()
		os.Exit(1)
	}
	if err := bridge.LoadEnvFile(envFile); err != nil {
		fmt.Fprintf(os.Stderr, "failed to load env file: %v\n", err)
		os.Exit(1)
	}

	if len(filteredArgs) < 2 {
		usage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	opts, command, err := parseCLI(filteredArgs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		usage()
		os.Exit(1)
	}

	var runErr error
	switch command {
	case "bridge register":
		runErr = bridge.RunRegister(ctx, opts)
	case "bridge heartbeat":
		runErr = bridge.RunHeartbeat(ctx, opts)
	case "bridge status":
		runErr = bridge.RunStatus(ctx, opts)
	case "bridge smoke":
		runErr = bridge.RunSmoke(ctx, opts)
	case "bridge start":
		runErr = bridge.RunStart(ctx, opts)
	case "tasks list":
		runErr = bridge.RunTasksList(ctx, opts)
	case "tasks watch":
		runErr = bridge.RunTasksWatch(ctx, opts)
	case "approvals list":
		runErr = bridge.RunApprovalsList(ctx, opts)
	default:
		usage()
		os.Exit(1)
	}
	if runErr != nil && runErr != context.Canceled {
		fmt.Fprintf(os.Stderr, "lab-agent failed: %v\n", runErr)
		os.Exit(1)
	}
}

func parseCLI(args []string) (bridge.CLIOptions, string, error) {
	opts := defaultCLIOptions()
	if len(args) < 2 {
		return opts, "", fmt.Errorf("subcommand is required")
	}
	command := strings.Join(args[:2], " ")
	var fs *flag.FlagSet
	switch command {
	case "bridge register", "bridge heartbeat", "bridge status", "bridge smoke", "bridge start", "tasks list", "tasks watch", "approvals list":
		fs = flag.NewFlagSet(command, flag.ContinueOnError)
	default:
		return opts, "", fmt.Errorf("unsupported command: %s", command)
	}
	fs.SetOutput(ioDiscard{})
	fs.StringVar(&opts.BaseURL, "base-url", opts.BaseURL, "Orchestrator base URL")
	fs.StringVar(&opts.APIKey, "api-key", opts.APIKey, "Operator API key")
	fs.StringVar(&opts.BridgeID, "bridge-id", opts.BridgeID, "Stable bridge UUID")
	fs.StringVar(&opts.WorkspaceRoot, "workspace-root", opts.WorkspaceRoot, "Workspace root")
	fs.StringVar(&opts.Name, "name", opts.Name, "Bridge name")
	fs.StringVar(&opts.Hostname, "hostname", opts.Hostname, "Host name override")
	fs.DurationVar(&opts.PollInterval, "poll-interval", opts.PollInterval, "Poll interval")
	fs.DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", opts.HeartbeatInterval, "Heartbeat interval")
	if err := fs.Parse(args[2:]); err != nil {
		return opts, "", err
	}
	if strings.TrimSpace(opts.APIKey) == "" {
		return opts, "", fmt.Errorf("LAB_AGENT_API_KEY or --api-key is required")
	}
	return opts, command, nil
}

func defaultCLIOptions() bridge.CLIOptions {
	return bridge.CLIOptions{
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

func usage() {
	fmt.Println(`Usage:
  lab-agent [--env-file .env.bridge] bridge register [--base-url ... --api-key ... --workspace-root ...]
  lab-agent [--env-file .env.bridge] bridge heartbeat [--bridge-id ...]
  lab-agent [--env-file .env.bridge] bridge status [--bridge-id ...]
  lab-agent [--env-file .env.bridge] bridge smoke [--bridge-id ...]
  lab-agent [--env-file .env.bridge] bridge start [--bridge-id ... --workspace-root ...]
  lab-agent [--env-file .env.bridge] tasks list
  lab-agent [--env-file .env.bridge] tasks watch
  lab-agent [--env-file .env.bridge] approvals list`)
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
