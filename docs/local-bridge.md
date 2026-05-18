# Local Bridge and CLI

This document explains how to install, run, and troubleshoot the local execution bridge that connects one workspace on the control machine to the Go orchestrator.

The bridge is intentionally narrow:

- it owns one workspace root
- it polls the orchestrator for `execution_target=local` tasks
- it executes only allowed local tools
- it persists results and artifacts back into the orchestrator

No shell free-for-all. No arbitrary filesystem traversal. No fake autonomy with a loaded shotgun.

## Components

- `lab-agent`
  - CLI for registration, status checks, smoke tests, task listing, and approval listing
- `lab-agentd`
  - long-running daemon that registers, heartbeats, claims local work, executes it, and submits results

## What the bridge can do

Supported local tools:

| Tool | Purpose |
|---|---|
| `read_file` | Read one file inside the registered workspace |
| `list_files` | List files from a workspace-relative path |
| `write_file` | Create or replace a file inside the workspace |
| `apply_patch` | Apply a git patch inside the workspace |
| `run_command` | Run an allowlisted command |
| `git_status` | Capture `git status --short --untracked-files=all` |
| `git_diff` | Capture `git diff --no-ext-diff` |
| `run_tests` | Run tests through an allowlisted command |

Allowed command prefixes for `run_command` and `run_tests`:

- `pytest`
- `python`
- `python3`
- `uv`
- `npm`
- `pnpm`
- `yarn`
- `make`
- `composer`
- `php`
- `git`

## What the bridge will reject

- paths outside the workspace root
- commands outside the allowlist
- local tasks without `metadata.tool_request`
- approval-gated tools before an approval is granted

## Installation

### 1. Build the binaries

Linux host binaries:

```bash
./scripts/build-orchestrator-go.sh
```

If you want to run the bridge on the local macOS control machine as well:

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 /usr/local/go/bin/go build -o dist/lab-agent-darwin-arm64 ./cmd/lab-agent && GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 /usr/local/go/bin/go build -o dist/lab-agentd-darwin-arm64 ./cmd/lab-agentd'
```

### 2. Export the required environment

Minimum variables:

```bash
export LAB_AGENT_BASE_URL="https://<cockpit_domain>/orchestrator"
export LAB_AGENT_API_KEY="<operator-or-admin-api-key>"
export LAB_AGENT_WORKSPACE_ROOT="/absolute/path/to/workspace"
```

Recommended extras:

```bash
export LAB_AGENT_BRIDGE_ID="55555555-5555-4555-8555-555555555555"
export LAB_AGENT_NAME="my-local-bridge"
export LAB_AGENT_POLL_INTERVAL="2s"
export LAB_AGENT_HEARTBEAT_INTERVAL="15s"
```

Notes:

- `LAB_AGENT_API_KEY` must have `operator` or `admin` scope
- `LAB_AGENT_WORKSPACE_ROOT` must be an absolute path to a real directory
- if `LAB_AGENT_BRIDGE_ID` is omitted, use one stable value per workspace instead of generating a new one every morning like a goldfish

## Register the bridge

Linux example:

```bash
./dist/lab-agent-linux-amd64 bridge register
```

macOS example:

```bash
./dist/lab-agent-darwin-arm64 bridge register
```

This creates or updates the bridge row in `/bridges/register`.

## Start the daemon

Linux:

```bash
./dist/lab-agentd-linux-amd64
```

macOS:

```bash
./dist/lab-agentd-darwin-arm64
```

Daemon responsibilities:

1. register the bridge
2. send heartbeats
3. claim the next local task
4. execute it inside the workspace
5. submit results, artifacts, diffs, stdout, stderr, and test output

## CLI usage

### Bridge commands

Register:

```bash
./dist/lab-agent-linux-amd64 bridge register
```

Heartbeat once:

```bash
./dist/lab-agent-linux-amd64 bridge heartbeat
```

Status:

```bash
./dist/lab-agent-linux-amd64 bridge status
```

Smoke handshake:

```bash
./dist/lab-agent-linux-amd64 bridge smoke
```

Start polling daemon from the CLI wrapper:

```bash
./dist/lab-agent-linux-amd64 bridge start
```

### Task and approval commands

List tasks:

```bash
./dist/lab-agent-linux-amd64 tasks list
```

Watch tasks:

```bash
./dist/lab-agent-linux-amd64 tasks watch
```

List approvals:

```bash
./dist/lab-agent-linux-amd64 approvals list
```

## Creating local tasks

Local execution is triggered from the orchestrator API with `execution_target=local`.

### Write a file

```bash
curl -sk -X POST "$ORCH_BASE/tasks" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Write local file",
    "assigned_agent": "coder",
    "execution_target": "local",
    "metadata": {
      "workspace_root": "/absolute/path/to/workspace",
      "tool_request": {
        "tool": "write_file",
        "path": "notes/hello.txt",
        "content": "hello from bridge\n"
      }
    }
  }'
```

### Run tests

```bash
curl -sk -X POST "$ORCH_BASE/tasks" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Run local tests",
    "assigned_agent": "coder",
    "execution_target": "local",
    "metadata": {
      "workspace_root": "/absolute/path/to/workspace",
      "tool_request": {
        "tool": "run_tests",
        "argv": ["python3", "-m", "pytest", "-q", "tests/unit/test_ok.py"]
      }
    }
  }'
```

### Request approval before execution

```bash
curl -sk -X POST "$ORCH_BASE/tasks" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Write local file after approval",
    "assigned_agent": "coder",
    "execution_target": "local",
    "metadata": {
      "workspace_root": "/absolute/path/to/workspace",
      "tool_request": {
        "tool": "write_file",
        "path": "notes/approved.txt",
        "content": "approved\n",
        "requires_approval": true
      }
    }
  }'
```

That task will move to `waiting_approval`. After `POST /approvals/{id}/approve`, the bridge will execute it once. Once is the key word.

## Artifacts and traces

Every local bridge execution persists a tool invocation plus zero or more artifacts:

- stdout
- stderr
- diff
- test results
- extra artifacts sent by the bridge

Useful endpoints:

```bash
curl -sk "$ORCH_BASE/tasks/<task_id>/sources" \
  -H "Authorization: Bearer $ORCH_KEY"
```

```bash
curl -sk "$ORCH_BASE/tools/invocations/<invocation_id>" \
  -H "Authorization: Bearer $ORCH_KEY"
```

## Operational constraints

- one bridge row maps to one workspace root
- local tasks are not enqueued for the embedded remote worker
- `workspace_root` is validated, but not reserved as a remote workspace sandbox
- approvals are remembered so the same local task is not trapped in an approval loop

## Troubleshooting

### The bridge gets `401`

Cause:

- bad API key
- missing `Authorization` header
- wrong scope

Fix:

- use an `operator` or `admin` key
- confirm `LAB_AGENT_API_KEY`

### The bridge gets `502`

Cause:

- orchestrator service restarting
- Nginx hit the backend during a bad moment, which is a poetic way of saying timing issue

Fix:

- check `systemctl status orchestrator.service`
- retry after health is back to `200`

### Local task fails with `path escapes workspace`

Cause:

- the requested path is outside `LAB_AGENT_WORKSPACE_ROOT`

Fix:

- use a workspace-relative path or a path inside the registered root

### Local task fails with `command not allowed`

Cause:

- `run_command` or `run_tests` used a non-allowlisted command

Fix:

- switch to an allowed command prefix
- if the command really belongs in the platform, extend the allowlist deliberately instead of sneaking it in through wishful thinking

## Acceptance checklist

The bridge is considered healthy when all of these pass:

- register works
- heartbeat works
- `write_file` completes
- `run_tests` completes
- disallowed command is rejected explicitly
- escaped path is rejected explicitly
- approval-gated task reaches `waiting_approval` and then `completed`
- `/tasks/{id}/sources` shows persisted artifacts
