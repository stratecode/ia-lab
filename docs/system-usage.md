# System Usage Guide

This guide explains how to use the platform as it actually exists today: through Telegram, Open WebUI, and the orchestrator HTTP API.

## Entry points

There are three practical ways to use the system:

1. `Telegram`
   Best for quick checks, approvals, and direct chat with the local `coder` and `planner` models.
2. `Open WebUI`
   Best for interactive chat in a browser with the configured local models.
3. `Orchestrator API`
   Best for automation, task creation, approvals, and system integration.

There is also a fourth entry point if you enjoy pain: editing production `site-packages`. Do not use that one.

## Access map

| Entry point | URL / Channel | Purpose |
|---|---|---|
| Telegram bot | `@stratecode_bot` | status, approvals, autonomous runs, direct model chat, limited server ops |
| Open WebUI | `https://chat.stratecode.com` | browser chat UI |
| Orchestrator API | `https://lab.stratecode.com/orchestrator/` | HTTP control plane |
| Direct health | `http://127.0.0.1:8100/health` | local health check on the host |
| Metrics | `http://127.0.0.1:8100/metrics` | Prometheus scrape target |

## Recommended daily workflow

Use this sequence if you want signal without ceremony:

1. Check `/status` in Telegram if you want a fast operational read.
2. Use Open WebUI for conversational work with `coder` or `planner`.
3. Use the HTTP API when you need repeatable automation or task lifecycle control.
4. Launch autonomous work with `/run` or `/plan` from Telegram.
5. Use Telegram approvals when a task is gated.

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
| `/server status` | Read-only host summary |
| `/server services` | Service status snapshot |
| `/server disk` | Disk usage snapshot |

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
/planner diseña un plan de despliegue para FastAPI + Redis + PostgreSQL
/server services
```

## Open WebUI

Open WebUI is exposed at `https://chat.stratecode.com` and connected to the local `llama.cpp` endpoints.

### What it is good for

- browsing a chat history in a usable interface
- comparing `coder`, `planner`, and `utility`
- longer interactive conversations than Telegram

### What it is not

- it is not the orchestrator control plane
- it is not a shell
- it does not replace approvals, timers, or worker lifecycle management

### First-use steps

1. Open `https://chat.stratecode.com`
2. Create an account if sign-up is enabled
3. Pick one of the configured model connections:
   - `coder`
   - `planner`
   - `utility`
4. Start chatting

### Which model to use

| Model | Use it for |
|---|---|
| `coder` | code changes, debugging, implementation plans with code bias |
| `planner` | decomposition, sequencing, architecture, higher-level reasoning |
| `utility` | lightweight questions, short transformations, cheap helper tasks |

## Orchestrator HTTP API

The API is exposed behind Nginx at:

```text
https://lab.stratecode.com/orchestrator/
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
export ORCH_BASE="https://lab.stratecode.com/orchestrator"
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
    "metadata": {
      "repo_path": "/srv/ai-lab/orchestrator",
      "branch": "main"
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
