# AGENTS.md

## Purpose

This repository provisions and operates the StrateCode lab. The current validated operating model is initiative-driven execution through the Go orchestrator, `lab-agent`, and `lab-agentd`.

Product truth lives in `docs/architecture/master-plan.md`.
Operational bridge usage lives in `docs/local-bridge.md`.
System entry points live in `docs/system-usage.md`.

## Work Rules For Codex

- Prefer direct fixes over broad refactors.
- Do not touch secrets in `.env`, `group_vars/vault.yml`, or `ssh/`.
- Treat `README.md` as a summary, not the roadmap authority.
- If work changes bridge, orchestrator, or Codex gateway behavior, verify with the Docker-based Go test harness:
  - `./scripts/test-orchestrator-go.sh`
- If work changes release artifacts or deployment-facing Go code, verify with:
  - `./scripts/build-orchestrator-go.sh`
- If work changes the benchmark harness, validate the affected script or benchmark path explicitly.

## Repo Map

- `cmd/orchestrator-go` — main Go control plane entrypoint
- `cmd/lab-agent` — local bridge CLI and TUI
- `cmd/lab-agentd` — local bridge daemon
- `cmd/codex-local-gateway` — OpenAI-compatible gateway for Codex against the local lab model
- `internal/orchestratorgo` — orchestrator runtime, HTTP API, initiative flow, persistence
- `internal/codexlocalgateway` — Codex gateway request/response adapter and tests
- `roles/` — deployment and host configuration
- `docs/` — runtime, workflow, and architecture docs
- `scripts/` — reproducible test, build, smoke, and benchmark helpers

## Preferred Verification

- Go runtime and gateway: `./scripts/test-orchestrator-go.sh`
- End-to-end local bridge golden path: `./scripts/smoke-golden-path-bridge.sh` when the required services and credentials are available
- Static shell validation for new scripts: `bash -n <script>`

## Agentic Success Criteria

When improving Codex compatibility, optimize for this exact path:

1. Codex can authenticate against the lab gateway.
2. Codex has repository-local instructions and authoritative docs.
3. The operator can run Codex against this repo without manual config surgery.
4. Changes are verified with the repo's real test/build harnesses.
