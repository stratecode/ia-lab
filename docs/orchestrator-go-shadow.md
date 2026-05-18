# Orchestrator Go Runtime

This document defines the production-oriented Go runtime that now takes the primary `orchestrator.service` role. The legacy Python API shadow has been retired; only the Python sidecars for heavy document/image capabilities remain.

## Current scope

The Go runtime covers the compatibility base and the first write-oriented lifecycle wave:

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
- `POST /tools/documents/read`
- `POST /tools/images/analyze`
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
- embedded Telegram polling bot when `LAB_TELEGRAM_BOT_TOKEN` and `LAB_TELEGRAM_ALLOWED_USERS` are configured

Current write support is intentionally narrow:

- `POST /tasks` persists a root task, applies idempotency, classifies it into `planner` or `coder`, and enqueues it in Redis.
- queue capacity is enforced with the same environment contract already used by the Python service.
- if Redis enqueue fails after persistence, the task is compensated to `cancelled` instead of being left queued in fiction only.
- `PATCH /tasks/{id}` enforces valid state transitions.
- `POST /tasks/{id}/cancel` cancels a task only from cancellable states.
- `POST /approvals/{id}/approve` resolves a pending approval, transitions the task back to `queued`, and re-enqueues it for worker pickup.
- `POST /approvals/{id}/reject` resolves a pending approval and cancels the task.
- an embedded worker registers itself in Redis, emits heartbeats, claims queued planner/coder tasks, and moves them into `in_progress`.
- `planner` tasks can now call the configured planner LLM endpoint and persist the returned structured plan.
- `coder` tasks can now execute real `aider-task` runs when `metadata.repo_name` or `metadata.repo_path` is provided.
- the fallback coder path still supports:
  - default execution report file
  - `tool_request.tool=write_file`
  - `tool_request.tool=append_file`
- `coder` tasks marked with `metadata.requires_approval=true` request approval once, pause in `waiting_approval`, and resume after approval.
- `web.fetch` now fetches and strips HTML content in the Go core.
- `document.read` and `image.analyze` now run through HTTP sidecars so the Go core stops swallowing PDF/DOCX/OCR complexity whole.
- `research.query` now supports URL-oriented research with direct summary output.
- `research.query` now detects and serves:
  - document paths or URLs via `document.read`
  - image paths or URLs via `image.analyze`
- `orchestrator-tools` now responds through `/v1/chat/completions` for URL-based prompts, returning answer, confidence, and sources.
- `orchestrator-tools` now also routes document and image prompts through the Go research flow.
- `research.query` and `orchestrator-tools` now persist `research_runs` records in PostgreSQL.
- `search_answer` now performs adaptive multi-source fetch and synthesized answers instead of stopping at raw snippets.
- the Go research flow now persists `tool_invocations` and `artifacts` for URL-based research runs.
- `GET /tools/invocations/{id}` now returns the persisted invocation plus its artifacts.
- `GET /tasks/{id}/sources` now returns persisted source artifacts linked to the task.
- the Go runtime now persists and serves:
  - `evaluation_runs`
  - `evaluation_dataset_items`
- `POST /evaluations/reference` now creates a reference answer via the configured OpenAI-compatible endpoint.
- `POST /evaluations/judge` now scores orchestrator vs reference and stores the resulting dataset item.
- `POST /research/query` now supports `evaluate_against_reference=true` for inline evaluation during research.

The remaining routes are exposed as explicit `501 Not Implemented` placeholders to preserve route intent without pretending full parity.

What the worker does not do yet:

- create planner-generated child tasks or reconcile parent-child trees
- implement full multi-source research parity with the Python orchestrator
- replace the Python capability sidecars; they remain transitional on purpose

## Deployment shape

- binary name: `orchestrator-go-linux-amd64`
- default primary port: `8100`
- same PostgreSQL and Redis as the Python orchestrator
- same auth model via `api_keys`
- same safe-mode, logging, and environment contract
- optional sidecars for heavy capabilities:
  - docs: `LAB_CAPABILITIES_DOCS_SIDECAR_URL`
  - images: `LAB_CAPABILITIES_IMAGES_SIDECAR_URL`

## Build

```bash
./scripts/build-orchestrator-go.sh
```

This uses a Dockerized Go toolchain so the repo does not depend on a local Go installation.

## Deployment shape

The Ansible role now deploys:

- `orchestrator.service` -> Go runtime on the primary orchestrator port
- `orchestrator-cap-docs.service` -> document sidecar
- `orchestrator-cap-images.service` -> image sidecar

Useful variables:

- `LAB_ORCHESTRATOR_GO_BINARY_PATH`
- `LAB_ORCHESTRATOR_GO_HOST`
- `LAB_ORCHESTRATOR_GO_PORT`
- `LAB_CAPABILITIES_DOCS_SIDECAR_URL`
- `LAB_CAPABILITIES_IMAGES_SIDECAR_URL`

## Compatibility intent

The Go runtime must remain API-and-persistence compatible with the previous Python system of record during migration. It is not allowed to introduce new public payload shapes casually just because the implementation language changed.
