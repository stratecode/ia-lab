# System Usage Guide

This guide explains how to use the platform as it actually exists today: through Telegram, Open WebUI, the orchestrator HTTP API, and the capability layer exposed behind them.

This document describes operational usage. For the canonical product definition, MVP boundaries, and roadmap, use the [Master Plan](architecture/master-plan.md).

## Entry points

There are four practical ways to use the system:

1. `Telegram`
   Best for quick checks, approvals, and direct chat with the local `coder` and `planner` models.
2. `Open WebUI`
   Best for interactive chat in a browser with the configured local models.
3. `Orchestrator API`
   Best for automation, task creation, approvals, system integration, and direct capability invocations.
4. `Capability Layer`
   Best when you need web search, URL fetch, document parsing, or image analysis with traceable artifacts.
5. `Local Bridge + TUI`
   Best when you want to manage initiatives and execute approved work inside a real workspace on the control machine.

There is also a fourth entry point if you enjoy pain: editing production `site-packages`. Do not use that one.

## Access map

| Entry point | URL / Channel | Purpose |
|---|---|---|
| Telegram bot | `@stratecode_bot` | status, approvals, autonomous runs, direct model chat, limited server ops |
| Open WebUI | `https://<chat_domain>` | browser chat UI |
| Orchestrator API | `https://<cockpit_domain>/orchestrator/` | HTTP control plane |
| Capability Layer | API + Telegram + `orchestrator-tools` model | web, documents, images, traceable sources |
| Local Bridge + TUI | `lab-agent`, `lab-agentd`, `lab-agent tui` | initiative governance, local execution, and project scaffolding |
| Direct health | `http://127.0.0.1:8100/health` | local health check on the host |
| Metrics | `http://127.0.0.1:8100/metrics` | Prometheus scrape target |

## Recommended daily workflow

Use this sequence if you want signal without ceremony:

1. Check `/status` in Telegram if you want a fast operational read.
2. Use Open WebUI for conversational work with `coder` or `planner`.
3. Use the HTTP API when you need repeatable automation or task lifecycle control.
4. Launch autonomous work with `/run` or `/plan` from Telegram.
5. Use Telegram approvals when a task is gated.
6. Use the capability endpoints or commands when you need context from the web, documents, or images.
7. Use the local bridge and TUI when the work must touch a real workspace on your machine instead of the remote runtime workspace.
8. Use initiatives when the work is larger than one task and needs requirements, design, plan, approvals, and selective execution.

## Telegram

The bot is access-restricted to the Telegram user IDs configured in `LAB_TELEGRAM_ALLOWED_USERS`.

### Supported commands

| Command | Purpose |
|---|---|
| `/help` | List available commands |
| `/status` | System overview |
| `/tasks` | List active tasks |
| `/task <task_id>` | Show task detail |
| `/cancel <task_id>` | Cancel a task |
| `/approve <approval_id>` | Approve a pending approval |
| `/reject <approval_id>` | Reject a pending approval |
| `/approvals` | List pending approvals |
| `/safe` | Toggle safe mode |
| `/run <objetivo>` | Create a root task and trigger planner + coder flow |
| `/plan <objetivo>` | Create a root task in plan-only mode |
| `/coder <mensaje>` | Direct chat with the local coder model |
| `/planner <mensaje>` | Direct chat with the local planner model |
| `/capabilities` | List available capabilities |
| `/web <consulta>` | Research a topic from web sources and answer directly |
| `/fetch <url>` | Fetch and summarize a web page |
| `/doc <ruta_o_url> [pregunta]` | Read a document and answer or summarize |
| `/image <ruta_o_url>` | Analyze an image and synthesize the result |
| `/research <consulta>` | Run full research mode |
| `/eval <consulta>` | Run research plus OpenAI reference + judge evaluation |
| `/sources <task_id>` | List persisted task sources/artifacts |
| `/server status` | Read-only host summary |
| `/server services` | Service status snapshot |
| `/server disk` | Disk usage snapshot |
| `/initiatives` | List recent initiatives |
| `/initiative <initiative_id>` | Show initiative summary |
| `/idea <workspace_alias> <texto>` | Create initiative from an idea |
| `/approve_phase <initiative_id> <requirements\|design\|plan>` | Approve one initiative phase |
| `/reject_phase <initiative_id> <requirements\|design\|plan>` | Reject one initiative phase |
| `/launch_tasks <initiative_id>` | Launch initiative tasks that are not manual |
| `/initiative_tasks <initiative_id>` | List initiative-linked tasks |

### Current limitations

- Plain text messages are not treated as free-form chat.
  Use `/coder <mensaje>` or `/planner <mensaje>`.
- `/server logs` and `/server restart` are intentionally not available for the current unprivileged `orchestrator` user.
- Telegram model chat is direct `llama.cpp` chat, not full tool-enabled autonomous execution.

### Practical examples

```text
/status
/tasks
/task 9d6c3f2d-5e62-4de1-b1b3-9966b8e32415
/cancel 9d6c3f2d-5e62-4de1-b1b3-9966b8e32415
/approvals
/run prepara un plan e implementa una mejora de logs en el orquestador
/plan diseña el trabajo para migrar el servicio a systemd separado
/coder resume este traceback y dime la causa raíz
/planner diseña un plan de despliegue para el runtime Go del orquestador con Redis y PostgreSQL
/web últimas novedades de PostgreSQL logical replication
/fetch https://example.com/article
/doc https://example.com/spec.pdf resume los riesgos principales
/image https://example.com/screenshot.png
/research compara NATS vs Redis Streams para colas internas
/eval últimas mejoras del runtime Go del orquestador en 2026
/server services
```

## Open WebUI

Open WebUI is exposed at `https://<chat_domain>` and connected to the local `llama.cpp` endpoints plus an OpenAI-compatible backend exposed by the orchestrator.

### What it is good for

- browsing a chat history in a usable interface
- comparing `coder`, `planner`, and `utility`
- longer interactive conversations than Telegram

### What it is not

- it is not the orchestrator control plane
- it is not a shell
- it does not replace approvals, timers, or worker lifecycle management

### First-use steps

1. Open `https://<chat_domain>`
2. Create an account if sign-up is enabled
3. Pick one of the configured model connections:
   - `coder`
   - `planner`
   - `utility`
   - `orchestrator-tools`
4. Start chatting

### Which model to use

| Model | Use it for |
|---|---|
| `coder` | code changes, debugging, implementation plans with code bias |
| `planner` | decomposition, sequencing, architecture, higher-level reasoning |
| `utility` | lightweight questions, short transformations, cheap helper tasks |
| `orchestrator-tools` | research mode with web, documents and images, returning direct answers with source refs |

## Local Bridge, TUI, and Initiatives

The validated local execution flow is no longer theoretical:

1. `lab-agent tui` creates an `initiative` or launches initiative-linked planned work
2. the orchestrator generates phase artifacts and, after approval, a task backlog
3. `lab-agentd` executes selected `agent_local` tasks in the registered local workspace
4. the initiative only advances to execution and completion when the approved local flow completes

Use this when you want real files on disk, not just remote worker output.

Important operational note:

- if the host runtime and the local `lab-agentd` binary are on different versions, local tasks can fail with `unsupported local tool`
- when bridge behavior changes, rebuild the local macOS/Linux agent binaries before testing

Full bridge install and TUI usage lives in [Local Bridge and CLI](./local-bridge.md).

## Initiative API

Use initiatives when one task is too small a unit and you need governed progression:

1. idea
2. requirements
3. design
4. plan
5. selective execution

Main endpoints:

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

## Orchestrator HTTP API

The API is exposed behind Nginx at:

```text
https://<cockpit_domain>/orchestrator/
```

Public endpoints:

- `GET /health`
- `GET /metrics`
- docs endpoints (`/docs`, `/openapi.json`, `/redoc`)

All task, approval, worker, config, and cleanup endpoints require a Bearer API key stored in the orchestrator database.

### Authentication

Use:

```text
Authorization: Bearer <raw_api_key>
```

### Scope model

| Scope | Typical use |
|---|---|
| `readonly` | read-only API usage |
| `bot` | bot-driven reads and limited POST operations |
| `operator` | task CRUD, approvals, cleanup, worker management |
| `admin` | full control including `/config/*` |

### Current operational reality

The deployment seeds the cleanup key automatically for the cleanup timer, but you should provision a dedicated key for human or integration use. Reusing the cleanup key for everything is lazy and eventually indistinguishable from negligence.

### Provision a dedicated API key

Run this on the host if you need an operator key today:

```bash
RAW_KEY="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(32))
PY
)"

KEY_ID="$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"

KEY_HASH="$(RAW_KEY="$RAW_KEY" python3 - <<'PY'
import hashlib
import os
print(hashlib.sha256(os.environ["RAW_KEY"].encode()).hexdigest())
PY
)"

echo "RAW KEY: $RAW_KEY"

docker exec orchestrator-postgres psql \
  -U "$LAB_POSTGRES_USER" \
  -d "$LAB_POSTGRES_DB" \
  -c "INSERT INTO api_keys (id, key_hash, name, scope, is_active, created_at) VALUES ('$KEY_ID', '$KEY_HASH', 'manual-operator', 'operator', true, NOW());"
```

Store the raw key outside the host after creation. The database stores only the SHA-256 hash.

## API examples

Assume:

```bash
export ORCH_BASE="https://<cockpit_domain>/orchestrator"
export ORCH_KEY="<raw_api_key>"
```

### Health

```bash
curl -sk "$ORCH_BASE/health"
```

### Create a task

```bash
curl -sk -X POST "$ORCH_BASE/tasks" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: demo-task-001" \
  -d '{
    "description": "Analiza el estado de Redis y propone mitigaciones",
    "assigned_agent": "planner",
    "priority": "normal",
    "execution_target": "remote",
    "metadata": {
      "repo_name": "orchestrator",
      "branch": "main"
    }
  }'
```

### Invoke web search directly

```bash
curl -sk -X POST "$ORCH_BASE/tools/web/search" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"query":"latest PostgreSQL logical replication improvements"}'
```

### Run research mode directly

```bash
curl -sk -X POST "$ORCH_BASE/research/query" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "query":"compare NATS vs Redis Streams for internal queueing",
    "allowed_capabilities":["web.search","web.fetch"]
  }'
```

### Fetch a URL directly

```bash
curl -sk -X POST "$ORCH_BASE/tools/web/fetch" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/article"}'
```

### Read a document directly

```bash
curl -sk -X POST "$ORCH_BASE/tools/documents/read" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"location":"https://example.com/spec.pdf"}'
```

### Analyze an image directly

```bash
curl -sk -X POST "$ORCH_BASE/tools/images/analyze" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"location":"https://example.com/screenshot.png"}'
```

### Fetch persisted task sources

```bash
curl -sk "$ORCH_BASE/tasks/<task_id>/sources" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Create a reference answer and judge it

This requires `LAB_OPENAI_REFERENCE_API_KEY` to be configured on the orchestrator.

```bash
RESEARCH_RUN_ID="<research_run_id>"

curl -sk -X POST "$ORCH_BASE/evaluations/reference" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"research_run_id\":\"$RESEARCH_RUN_ID\"}"
```

Then:

```bash
EVALUATION_RUN_ID="<evaluation_run_id>"

curl -sk -X POST "$ORCH_BASE/evaluations/judge" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d "{\"evaluation_run_id\":\"$EVALUATION_RUN_ID\"}"
```

### Create a local-execution task

```bash
curl -sk -X POST "$ORCH_BASE/tasks" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Crea un fichero de smoke en el workspace local",
    "assigned_agent": "coder",
    "priority": "normal",
    "execution_target": "local",
    "metadata": {
      "workspace_root": "/abs/path/to/current/workspace",
      "tool_request": {
        "tool": "write_file",
        "path": ".tmp/smoke.txt",
        "content": "hello from local bridge\n"
      }
    }
  }'
```

### List tasks

```bash
curl -sk "$ORCH_BASE/tasks?limit=20" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Get task detail

```bash
curl -sk "$ORCH_BASE/tasks/<task_id>" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### List direct child tasks

```bash
curl -sk "$ORCH_BASE/tasks/<task_id>/children" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Get the full task tree

```bash
curl -sk "$ORCH_BASE/tasks/<task_id>/tree" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Register a local bridge

```bash
LAB_AGENT_BASE_URL="$ORCH_BASE" \
LAB_AGENT_API_KEY="$ORCH_KEY" \
LAB_AGENT_WORKSPACE_ROOT="/abs/path/to/current/workspace" \
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge register
```

### Run the local bridge daemon

```bash
LAB_AGENT_BASE_URL="$ORCH_BASE" \
LAB_AGENT_API_KEY="$ORCH_KEY" \
LAB_AGENT_WORKSPACE_ROOT="/abs/path/to/current/workspace" \
./dist/lab-agentd-linux-amd64 --env-file .env.bridge
```

### Inspect local bridge state

```bash
LAB_AGENT_BASE_URL="$ORCH_BASE" \
LAB_AGENT_API_KEY="$ORCH_KEY" \
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge status
```

### Smoke-test the bridge handshake

```bash
LAB_AGENT_BASE_URL="$ORCH_BASE" \
LAB_AGENT_API_KEY="$ORCH_KEY" \
LAB_AGENT_WORKSPACE_ROOT="/abs/path/to/current/workspace" \
./dist/lab-agent-linux-amd64 --env-file .env.bridge bridge smoke
```

### Inspect local work from the CLI

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tasks list
./dist/lab-agent-linux-amd64 --env-file .env.bridge approvals list
```

### Open the local bridge cockpit

```bash
./dist/lab-agent-linux-amd64 --env-file .env.bridge tui
```

Full operator behavior, views, and keybindings live in:

- [TUI Operator Guide](tui.md)

## Research mode notes

`orchestrator-tools` is no longer just a thin tool gateway. It now runs a server-side research flow:

1. classify the query
2. select capabilities
3. fetch one or several sources
4. synthesize an answer with references
5. persist the run, artifacts, and source links

If `LAB_OPENAI_REFERENCE_API_KEY` is configured, the system can also generate a reference answer through the OpenAI API and score the orchestrator answer with an LLM judge. That is for evaluation and tuning, not for production control logic.

## Local Agent Bridge

The Local Agent Bridge is now the controlled path for local execution from the Go runtime:

- it registers one workspace root
- it polls the orchestrator for `execution_target=local` work
- it only operates inside that workspace
- it only runs a limited tool set
- it now uses Go binaries:
  - `lab-agent`
  - `lab-agentd`
- it now also has a terminal cockpit through:
  - `lab-agent tui`
- full operational guide:
  - [Local Bridge and CLI](local-bridge.md)

Use `lab-agent` as the primary operator interface. Use `lab-agentd` when you want the bridge daemon by itself. Use `curl` against the orchestrator API when you need integration, automation, or low-level inspection.

### Supported tools

| Tool | Purpose |
|---|---|
| `read_file` | read a file inside the workspace |
| `list_files` | list files from a workspace-relative path |
| `write_file` | write a file inside the workspace |
| `apply_patch` | apply a git patch in the workspace |
| `run_command` | run an allowlisted command |
| `git_status` | collect git status |
| `git_diff` | collect git diff |
| `run_tests` | run tests through an allowlisted command |

### Current limitations

- The bridge works on one registered workspace root at a time.
- No arbitrary filesystem access exists outside that root.
- No shell free-for-all exists. `run_command` is allowlisted on purpose.
- The remote `coder` path still requires a real repo configured on the host via `repo_name`.
- The TUI is intentionally operational, not decorative. If you want a spaceship dashboard, Open WebUI already exists.

### Cancel a task

```bash
curl -sk -X POST "$ORCH_BASE/tasks/<task_id>/cancel" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### List approvals

```bash
curl -sk "$ORCH_BASE/approvals" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Approve an approval

```bash
curl -sk -X POST "$ORCH_BASE/approvals/<approval_id>/approve" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"operator":"user:fran"}'
```

### Reject an approval

```bash
curl -sk -X POST "$ORCH_BASE/approvals/<approval_id>/reject" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"operator":"user:fran"}'
```

### List workers

```bash
curl -sk "$ORCH_BASE/workers" \
  -H "Authorization: Bearer $ORCH_KEY"
```

### Toggle safe mode

Requires `admin` scope:

```bash
curl -sk -X POST "$ORCH_BASE/config/safe-mode" \
  -H "Authorization: Bearer $ORCH_KEY" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

### Trigger workspace cleanup

Requires `operator` or `admin` scope:

```bash
curl -sk -X POST "$ORCH_BASE/workspaces/cleanup" \
  -H "Authorization: Bearer $ORCH_KEY"
```

## Operational interpretation

If you only need one mental model, use this:

- `Telegram` is the remote control
- `Open WebUI` is the conversational UI
- `Orchestrator API` is the integration surface
- `Prometheus/Grafana` are the truth serum

## Known gaps

These are current product limitations, not bugs in this document:

- Telegram free-form chat routing is not implemented
- Telegram does not expose arbitrary shell or restart powers
- Open WebUI does not replace orchestrator task execution
- there is no polished self-service API key management endpoint yet
- end-to-end autonomous multi-agent routing is still outside the current MVP closure

## Related documents

- [Getting Started](getting-started.md)
- [Orchestrator Redeploy Runbook](orchestrator-redeploy.md)
- [Architecture Overview](architecture/overview.md)
- [Tool Registry](tools/registry.md)
