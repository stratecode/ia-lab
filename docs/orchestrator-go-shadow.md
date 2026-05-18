# Orchestrator Go Shadow

This document defines the first production-oriented Go shadow core that runs in parallel with the Python orchestrator.

## Current scope

The Go shadow service intentionally covers the compatibility base and the first write-oriented lifecycle wave:

- `GET /health`
- `GET /ready`
- `GET /metrics`
- `GET /tasks`
- `GET /tasks/{id}`
- `GET /tasks/{id}/children`
- `GET /tasks/{id}/tree`
- `POST /tasks`
- `GET /approvals`
- `GET /approvals/{id}`
- `POST /approvals/{id}/approve`
- `POST /approvals/{id}/reject`
- `GET /workers`
- `GET /capabilities`
- `POST /tools/web/search`
- `POST /tools/web/fetch`
- `GET /tools/invocations/{id}`
- `POST /research/query`
- `GET /research/runs/{id}`
- `GET /tasks/{id}/sources`
- `POST /evaluations/reference`
- `POST /evaluations/judge`
- `GET /evaluations/{id}`
- `GET /datasets/evaluation-items`
- `GET /v1/models`
- `POST /v1/chat/completions`
- background worker registration, heartbeat emission, queue claim, and `queued -> assigned -> in_progress` transitions
- embedded minimal runner for `planner` and `coder`

Current write support in the shadow is intentionally narrow:

- `POST /tasks` persists a root task, applies idempotency, classifies it into `planner` or `coder`, and enqueues it in Redis.
- queue capacity is enforced with the same environment contract already used by the Python service.
- if Redis enqueue fails after persistence, the task is compensated to `cancelled` instead of being left queued in fiction only.
- `PATCH /tasks/{id}` enforces valid state transitions.
- `POST /tasks/{id}/cancel` cancels a task only from cancellable states.
- `POST /approvals/{id}/approve` resolves a pending approval, transitions the task back to `queued`, and re-enqueues it for worker pickup.
- `POST /approvals/{id}/reject` resolves a pending approval and cancels the task.
- an embedded shadow worker registers itself in Redis, emits heartbeats, claims queued planner/coder tasks, and moves them into `in_progress`.
- `planner` tasks now complete with a minimal structured plan payload.
- `coder` tasks now complete after performing a minimal real workspace action:
  - default execution report file
  - `tool_request.tool=write_file`
  - `tool_request.tool=append_file`
- `coder` tasks marked with `metadata.requires_approval=true` request approval once, pause in `waiting_approval`, and resume after approval.
- `web.fetch` now fetches and strips HTML content in the Go shadow core.
- `research.query` now supports URL-oriented research with direct summary output.
- `orchestrator-tools` now responds through `/v1/chat/completions` for URL-based prompts, returning answer, confidence, and sources.
- `research.query` and `orchestrator-tools` now persist `research_runs` records in PostgreSQL.
- `search_answer` in the Go shadow now performs adaptive multi-source fetch and synthesized answers instead of stopping at raw snippets.
- the Go research flow now persists `tool_invocations` and `artifacts` for URL-based research runs.
- `GET /tools/invocations/{id}` now returns the persisted invocation plus its artifacts.
- `GET /tasks/{id}/sources` now returns persisted source artifacts linked to the task.
- the Go shadow now persists and serves:
  - `evaluation_runs`
  - `evaluation_dataset_items`
- `POST /evaluations/reference` now creates a reference answer via the configured OpenAI-compatible endpoint.
- `POST /evaluations/judge` now scores orchestrator vs reference and stores the resulting dataset item.
- `POST /research/query` now supports `evaluate_against_reference=true` for inline evaluation during research.

The remaining routes are exposed as explicit `501 Not Implemented` placeholders to preserve route intent without pretending full parity.

What the worker does not do yet:

- run research or `orchestrator-tools`
- execute real Aider/LLM-backed planner-coder logic
- create planner-generated child tasks or reconcile parent-child trees
- support document/image capabilities and evaluation harness
- support document/image capabilities and their sidecar contracts
- implement full multi-source research parity with the Python orchestrator

## Deployment shape

- binary name: `orchestrator-go-linux-amd64`
- default shadow port: `8110`
- same PostgreSQL and Redis as the Python orchestrator
- same auth model via `api_keys`
- same safe-mode, logging, and environment contract

## Build

```bash
./scripts/build-orchestrator-go.sh
```

This uses a Dockerized Go toolchain so the repo does not depend on a local Go installation.

## Shadow enablement

The Ansible role supports an optional shadow unit:

- `orchestrator-go-shadow.service`
- disabled by default
- enabled only when:
  - `LAB_ORCHESTRATOR_GO_SHADOW_ENABLED=true`
  - a built binary is available at `LAB_ORCHESTRATOR_GO_SHADOW_BINARY_PATH`

## Compatibility intent

The Go shadow must remain API-and-persistence compatible with the Python system of record during migration. It is not allowed to introduce new public payload shapes in the shadow phase.
