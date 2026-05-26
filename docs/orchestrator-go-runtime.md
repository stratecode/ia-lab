# Orchestrator Go Runtime

This document defines the production-oriented Go runtime that now owns `orchestrator.service`. Python remains only where it still earns its keep: the document and image sidecars.

This is a runtime scope document, not the product roadmap. For validated workflow, MVP boundaries, and next implementation priorities, use the [Master Plan](architecture/master-plan.md).

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
- `GET /bridges`
- `POST /bridges/register`
- `POST /bridges/{bridge_id}/heartbeat`
- `POST /bridges/{bridge_id}/claim-next`
- `POST /bridges/{bridge_id}/tasks/{task_id}/result`
- `GET /capabilities`
- `POST /tools/web/search`
- `POST /tools/web/fetch`
- `POST /tools/code/analyze`
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
- queue capacity is enforced through the same environment contract used by the current runtime.
- if Redis enqueue fails after persistence, the task is compensated to `cancelled` instead of being left queued in fiction only.
- `PATCH /tasks/{id}` enforces valid state transitions.
- `POST /tasks/{id}/cancel` cancels a task only from cancellable states.
- `POST /approvals/{id}/approve` resolves a pending approval, transitions the task back to `queued`, and re-enqueues it for worker pickup.
- `POST /approvals/{id}/reject` resolves a pending approval and cancels the task.
- an embedded worker registers itself in Redis, emits heartbeats, claims queued planner/coder tasks, and moves them into `in_progress`.
- local bridge registration, heartbeat, task claim, and result submission now live in the Go runtime.
- `planner` tasks can now call the configured planner LLM endpoint and persist the returned structured plan.
- `coder` tasks can now execute real `aider-task` runs when `metadata.repo_name` or `metadata.repo_path` is provided.
- the fallback coder path still supports:
  - default execution report file
  - `tool_request.tool=write_file`
  - `tool_request.tool=append_file`
- `coder` tasks marked with `metadata.requires_approval=true` request approval once, pause in `waiting_approval`, and resume after approval.
- `web.fetch` now fetches and strips HTML content in the Go core.
- `code.analysis` now provides governed repository analysis findings for reviewer-grade validation without handing arbitrary filesystem write access to agents.
- `document.read` and `image.analyze` now run through HTTP sidecars so the Go core stops swallowing PDF/DOCX/OCR complexity whole.
- `execution_target=local` is now serviced by Go binaries:
  - `lab-agent`
  - `lab-agentd`
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

The remaining routes are exposed as explicit `501 Not Implemented` placeholders where parity is still unfinished. No cosplay of completeness.

## Deployment shape

- binary name: `orchestrator-go-linux-amd64`
- default primary port: `8100`
- runtime directory on host: `/srv/ai-lab/orchestrator/runtime`
- same PostgreSQL and Redis contract as the previous runtime
- same auth model via `api_keys`
- same safe-mode, logging, and environment contract
- dedicated Python sidecars for heavy capabilities:
  - docs: `LAB_CAPABILITIES_DOCS_SIDECAR_URL`
  - images: `LAB_CAPABILITIES_IMAGES_SIDECAR_URL`
- sidecar source root on host: `/srv/ai-lab/orchestrator/sidecars`
- sidecar virtualenv on host: `/srv/ai-lab/orchestrator/sidecars-venv`

## Build

```bash
./scripts/build-orchestrator-go.sh
```

This uses a Dockerized Go toolchain so the repo does not depend on a local Go installation.

## Deployment

- `orchestrator.service` -> Go runtime on the primary orchestrator port
- `orchestrator-cap-docs.service` -> document sidecar
- `orchestrator-cap-images.service` -> image sidecar
- `requirements-sidecars.txt` -> minimal Python dependency set for the sidecars only
- old `go-shadow` naming is retired; the main runtime now lives under `runtime/` because words should describe reality at least once in their lives.

Useful variables:

- `LAB_ORCHESTRATOR_GO_BINARY_PATH`
- `LAB_ORCHESTRATOR_GO_HOST`
- `LAB_ORCHESTRATOR_GO_PORT`
- `LAB_AGENT_BASE_URL`
- `LAB_AGENT_API_KEY`
- `LAB_AGENT_WORKSPACE_ROOT`
- `LAB_AGENT_BRIDGE_ID`
- `LAB_CAPABILITIES_DOCS_SIDECAR_URL`
- `LAB_CAPABILITIES_IMAGES_SIDECAR_URL`

## Related docs

- [Local Bridge and CLI](local-bridge.md)
- [System Usage Guide](system-usage.md)

## Compatibility intent

The Go runtime must remain API-and-persistence compatible with the previous system of record during migration. It is not allowed to introduce new public payload shapes casually just because the implementation language changed.
