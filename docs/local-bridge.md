# Local Bridge and CLI

This document explains how to install, run, and troubleshoot the local execution bridge that connects one workspace on the control machine to the Go orchestrator.

This document is operational. It describes how the bridge works and how to run it. For product direction, validated scope, and roadmap, use the [Master Plan](architecture/master-plan.md).

The bridge is intentionally narrow:

- it owns one workspace root
- it polls the orchestrator for `execution_target=local` tasks
- it executes only allowed local tools
- it persists results and artifacts back into the orchestrator

The bridge is not the MCP registry.

- capability definitions and project policies live on the server
- the bridge only enforces the `allowed_capabilities` it receives with the task
- if the server disables `filesystem.write` for a project, the bridge does not get to improvise a constitutional amendment

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
| `filesystem.read` | Governed alias for file read inside the workspace |
| `filesystem.list` | Governed alias for workspace file listing |
| `filesystem.write` | Governed alias for file write inside the workspace |
| `research_project` | Produce structured project constraints and validation context |
| `scaffold_project` | Create a small test project boilerplate inside the workspace |
| `review_project` | Validate the generated project structure and test command |
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
- capability aliases not present in `metadata.allowed_capabilities`

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

Alternative: use an env file and pass it explicitly:

```bash
cat > .env.bridge <<'EOF'
LAB_AGENT_BASE_URL=https://<cockpit_domain>/orchestrator
LAB_AGENT_API_KEY=<operator-or-admin-api-key>
LAB_AGENT_WORKSPACE_ROOT=/absolute/path/to/workspace
LAB_AGENT_BRIDGE_ID=55555555-5555-4555-8555-555555555555
LAB_AGENT_NAME=my-local-bridge
LAB_AGENT_POLL_INTERVAL=2s
LAB_AGENT_HEARTBEAT_INTERVAL=15s
EOF
```

Notes:

- `LAB_AGENT_API_KEY` must have `operator` or `admin` scope
- `LAB_AGENT_WORKSPACE_ROOT` must be an absolute path to a real directory
- if `LAB_AGENT_BRIDGE_ID` is omitted, use one stable value per workspace instead of generating a new one every morning like a goldfish

## Register the bridge

Linux example:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge register
```

macOS example:

```bash
./dist/lab-agent-darwin-arm64 --env-file .env.bridge bridge register
```

This creates or updates the bridge row in `/bridges/register`.

## Start the daemon

Linux:

```bash
./dist/lab-agentd-linux-amd64 --env-file .env.bridge
```

macOS:

```bash
./dist/lab-agentd-darwin-arm64 --env-file .env.bridge
```

Daemon responsibilities:

1. register the bridge
2. send heartbeats
3. claim the next local task
4. execute it inside the workspace
5. submit results, artifacts, diffs, stdout, stderr, and test output

Important:

- when the bridge contract changes, rebuild the local binaries before blaming the daemon
- if the host runtime and `lab-agentd` are on different versions, local tasks can fail with `unsupported local tool`
- the failure mode is honest but annoying, which is still better than silent corruption

## CLI and TUI usage

The normal operational surface for the bridge is `lab-agent`: CLI for direct commands, TUI for day-to-day operation, and raw `curl` only when you need API-level integration.

### Start the TUI

Linux:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tui
```

macOS:

```bash
./dist/lab-agent-darwin-arm64 --env-file .env.bridge tui
```

The TUI gives you:

- `Initiatives` as the primary operating view
- `Execution` for selective launch and task inspection
- `Approvals` for human gates
- `Bridge` for local bridge operations
- `Projects` for mini lab scaffolds
- `Chat` for a minimal prompt surface
- `Tasks` for raw task inspection

Full operator documentation lives in:

- [TUI Operator Guide](tui.md)

The validated initiative flow is:

1. create an initiative bound to one workspace
2. generate `requirements`
3. approve or reject them
4. generate `design`
5. approve or reject it
6. generate `plan`
7. approve it
8. selectively launch one or more generated tasks

The validated local project flow under that initiative is:

1. the TUI creates initiative-linked planned work on the orchestrator
2. the orchestrator emits local work for `researcher`, `coder`, and `reviewer`
3. the local bridge executes that work inside the registered workspace
4. the task and initiative only complete when the linked execution path completes

It is a cockpit, not a shrine to terminal aesthetics.

## Official benchmark path

The canonical benchmark path is:

- [scripts/benchmark-context-memory.sh](/Users/fran.lopez/Development/StrateCode/lab/scripts/benchmark-context-memory.sh)

That harness is expected to:

1. create an initiative against a real repository workspace
2. advance `requirements`, `design`, and `plan` through approvals
3. generate tasks through the orchestrator
4. selectively launch local work
5. approve the `coder` gate when required
6. wait for `reviewer` completion and initiative reconciliation
7. persist `benchmark_run.json`, task artifacts, diffs, bridge logs, and aggregate summaries

This is the supported benchmark route. It is the path that proves the governed workflow is real.

The older manual benchmark runner remains useful as a diagnostic fallback when the harness itself is broken or when you need to isolate a runtime regression from a harness regression. It is not the product path and should not be treated as equivalent evidence when the official harness can run.

Current benchmark reading, based on the official harness:

- `repo_recall` should look boring in the good way:
  stable score uplift, stable hits, almost no spread
- `technology_transfer` should improve without `repo_specific` hits:
  if it helps only through same-repo memory, the league is being cheated
- `pattern_transfer` should improve through `pattern_similar` hits:
  if the query is overfitted to one repo or one language, this league will lie to you quickly
- `negative_transfer` should usually stay flat and `guarded`:
  a charming `+10` here is often contamination wearing a tie

### Agent maturity suite

The same harness also supports curated `agent_maturity` cases.

Those cases measure three layers at once:

- `individual maturity`
  planner, researcher, coder, reviewer
- `handoff maturity`
  whether the next agent receives context that is actually actionable
- `system maturity`
  whether the governed initiative closes cleanly with the expected approvals, artifacts, and validation

The canonical config template for this suite is:

- [benchmarks/config.agent-maturity.default.json](/Users/fran.lopez/Development/StrateCode/lab/benchmarks/config.agent-maturity.default.json)

The external comparison mode is:

- `reference_external`

That mode does not replace the governed initiative path. It compares the orchestrator-produced outcome against a direct external reference answer and judge, then stores:

- `evaluation_reference.json`
- `evaluation_judge.json`
- `agent_maturity_run.json`

If `reference_external` starts outscoring everything while the governed run stays weak, that is not maturity. That is a benchmark politely telling you the agents still need adult supervision.

### Memory layers

When the benchmark talks about "memory", it is not all one thing:

- `repo_recall`
  local continuity memory for the same repository
- `technology_transfer`
  reusable memory across repositories that share stack, language, or framework shape
- `pattern_transfer`
  reusable memory across repositories that share problem domain, failure class, fix pattern, or validation pattern
- `negative_transfer`
  cases where memory should stay quiet and avoid contaminating the run

Local continuity is useful. It is not the same claim as reusable experience. Mixing both would produce flattering numbers and weak science, which is a popular combination for demos and a terrible one for engineering.

### Valid vs contaminated benchmark signal

A benchmark result is valid when:

- it runs through the official initiative-driven harness
- the materialized plan matches the benchmark case and league
- same-initiative memory is excluded
- same-repo memory is excluded for `technology_transfer`, `pattern_transfer`, and `negative_transfer`
- the summary separates `repo_specific`, `technology_similar`, `pattern_similar`, and forbidden hits

A result is contaminated when:

- the harness silently falls back to a generic flow
- a transfer league improves because it reused same-repo memory
- a negative-transfer case improves because unrelated semantic hits leaked in
- the run is only reproducible through a side runner or one-off script

### TUI keybindings

The current TUI uses `Ctrl+...` shortcuts for actions and keeps plain typing for forms and chat.

Core bindings:

- `Ctrl+N`
  create initiative
- `Ctrl+E`
  generate the current phase
- `Ctrl+A`
  approve
- `Ctrl+X`
  reject or cancel, depending on view
- `Ctrl+T`
  cycle execution mode in `Execution`, or convert chat prompt into task in `Chat`
- `Ctrl+L`
  launch selected execution task

The TUI governs, approves, launches, and inspects. It still does not pretend to be a Markdown IDE, which is a mercy.

### CLI commands

#### Bridge commands

Register:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge register
```

Heartbeat once:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge heartbeat
```

Status:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge status
```

Smoke handshake:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge smoke
```

Start polling daemon from the CLI wrapper:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge start
```

#### Task and approval commands

List tasks:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tasks list
```

Watch tasks:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tasks watch
```

List approvals:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge approvals list
```

Run one objective through the local bridge without opening the TUI:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge objectives run \
  --title "Repair the repo objective loop" \
  --objective "Apply the requested repo change, validate it, and close the initiative only when review approves."
```

Default behavior is intentionally bounded: it auto-approves only `local_bridge_tool` approvals that belong to waiting-approval tasks inside the current objective initiative. Override with `--approval-mode manual` if you want operator gating.

## Initiative API

The TUI is the operator surface. The API is the integration surface.

Initiative endpoints:

- `POST /initiatives`
- `GET /initiatives`
- `GET /initiatives/{id}`
- `GET /initiatives/{id}/artifacts`
- `POST /initiatives/{id}/advance`
- `POST /initiatives/{id}/approve/{phase}`
- `POST /initiatives/{id}/reject/{phase}`
- `POST /initiatives/{id}/tasks/generate`
- `GET /initiatives/{id}/tasks`
- `POST /initiatives/{id}/tasks/{task_id}/mode`
- `POST /initiatives/{id}/tasks/launch`

The bridge does not own governance. It owns execution inside the workspace once the initiative has already been reviewed and launched.

## Rebuild local binaries after bridge changes

Linux runtime binaries:

```bash
./scripts/build-orchestrator-go.sh
```

macOS local bridge binaries:

```bash
docker run --rm \
  -v "$PWD:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go mod download && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/lab-agent-darwin-arm64 ./cmd/lab-agent && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/lab-agentd-darwin-arm64 ./cmd/lab-agentd'
```

If you skip this after changing bridge tools, the daemon can still connect and still fail usefully. That is not compatibility; that is a polite crash.

## Recommended workflow

1. register the workspace with `lab-agent`
2. start the bridge from:
   - `lab-agent ... tui`
   - or `lab-agentd`
3. create an initiative from the TUI
4. advance `requirements -> design -> plan`
5. approve each phase
6. launch one or more execution tasks from `Execution`
7. inspect artifacts, approvals, outputs, and final state from the TUI first
5. use CLI commands for scripted or direct operational work
6. only use direct API calls when you are integrating the orchestrator or creating tasks programmatically

## Creating local tasks

This part uses the orchestrator API because task creation belongs to the orchestrator, not to the bridge binary itself.

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

If you just want to observe the queue and approvals from the bridge side, prefer:

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tasks list
./dist/lab-agent-linux-amd64 --env-file .env.bridge approvals list
```

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

Operationally:

- use `lab-agent` to operate the bridge
- use API endpoints to integrate or inspect stored data
- use the workspace itself to inspect the real file changes

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
- reverse proxy buffering or timeout mismatch on long-lived bridge traffic

Fix:

- check `systemctl status orchestrator.service`
- check the deployed Nginx proxy settings for the orchestrator path
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
Current defaults and operational notes:

- official `agent_maturity` campaigns now default to `runs_per_case=3`
- `planner` and `reviewer` can consume reusable semantic experience outside benchmark-only flows when repo memory scope is available
- `researcher` now carries governed read-oriented capability intent (`web.search`, `web.fetch`, `document.read`) in deterministic repo workflows
- `reviewer` now carries governed `code.analysis` intent and `review_project` can attach a code-analysis artifact during validation
- `coder` can now use governed filesystem aliases (`filesystem.read`, `filesystem.list`, `filesystem.write`) with workspace-root scope and explicit `allowed_capabilities`
- the embedded remote worker now records `recovery_checkpoint` metadata and re-queues stale interrupted remote tasks on startup
- local bridge claims are now lease-based and persist both `local_bridge_lease` and `recovery_checkpoint` metadata during claim, heartbeat, resume, and completion
- while a local task is still running, the bridge now keeps heartbeating with live execution metadata (`stage`, `tool`, `summary`) so recovery is not blind mid-command
- lease recovery has been fault-injected end-to-end: kill bridge, let lease expire, restart same bridge, and confirm task completion without manual rescue
- host reboot recovery has also been fault-injected: reboot the remote orchestrator host during local execution, wait for `orchestrator.service` to return, restart the same bridge, and confirm task completion without manual task repair

This is now the first practical end-to-end recovery layer for local bridges. It is still not full checkpoint-and-continue for arbitrary subprocess state, but it is real resume/reclaim behavior instead of wishful prose.
