# Codex Agentic Workflow

This is the shortest reproducible path to work on this repository from Codex against the lab, without hand-editing config files or pretending documentation is optional.

Use this document when the goal is:

- run Codex against the lab Codex gateway
- work on this repo with repository-local instructions
- keep the initiative and local-bridge model available when the work must touch a real workspace

For product truth, use [Master Plan](./architecture/master-plan.md).
For bridge execution details, use [Local Bridge and CLI](./local-bridge.md).

## 1. Preconditions

You need:

- the lab deployed with `codex_local_gateway` enabled
- a valid Codex gateway API key
- local access to this repository
- Codex installed on the machine where you will work

Relevant env sources already exist in this repo:

- `.env`
- `.env.example`

Primary variables:

- `LAB_CODEX_GATEWAY_DOMAIN`
- `LAB_CODEX_GATEWAY_API_KEY`
- `LAB_CODEX_GATEWAY_MODEL` or `CODEX_GATEWAY_MODEL`

If the key is not exported, the bootstrap script also checks:

- `~/.config/stratecode-lab/codex_gateway_api_key`

If the public domain is not ready yet, the bootstrap script falls back to `http://127.0.0.1:8180/v1`.

## 2. Bootstrap `CODEX_HOME`

Run:

```bash
./scripts/bootstrap-codex-home.sh
```

This creates:

- `.codex-local/config.toml`
- `.codex-local/env`

Both are ignored by git. That is intentional. Secrets committed to git are not “agentic”; they are evidence.

If you want a different env file or output directory:

```bash
./scripts/bootstrap-codex-home.sh --env-file /path/to/.env --output-dir /tmp/codex-home
```

## 3. Start Codex Against the Lab Gateway

```bash
export CODEX_HOME="$PWD/.codex-local"
set -a && source "$CODEX_HOME/env" && set +a
codex
```

Expected outcome:

- Codex uses the `lab-codex-gateway` provider
- requests go to the lab gateway `.../v1`
- repo-local `AGENTS.md` instructions are available automatically in this repository

## 4. Verify the Gateway Before Blaming Codex

If Codex cannot answer or behaves like it forgot how HTTP works, check the gateway first:

```bash
curl -s http://127.0.0.1:8180/health
curl -s http://127.0.0.1:8180/ready
```

If you are using the public domain:

```bash
curl -sk https://"$LAB_CODEX_GATEWAY_DOMAIN"/health
curl -sk https://"$LAB_CODEX_GATEWAY_DOMAIN"/ready
```

You can also list models:

```bash
curl -s \
  -H "Authorization: Bearer $CODEX_GATEWAY_API_KEY" \
  "${CODEX_GATEWAY_BASE_URL:-http://127.0.0.1:8180/v1}/models"
```

## 5. Repo-Native Verification After Changes

When you change Go runtime, bridge, or gateway code:

```bash
./scripts/test-orchestrator-go.sh
```

When you change deployable Go binaries and need build proof:

```bash
./scripts/build-orchestrator-go.sh
```

When you change local bridge workflow and the required services are available:

```bash
./scripts/smoke-golden-path-bridge.sh
```

## 6. When To Use Codex vs `lab-agent`

Use Codex when:

- you want direct repository work in this checkout
- you need repo-local instructions and fast iterative editing
- the task is coding-heavy and local terminal-driven

Use `lab-agent tui` plus `lab-agentd` when:

- the work should follow the governed initiative path
- you need approvals, selective execution, and initiative reconciliation
- you want the orchestrator to remain the system of record

Those surfaces are complementary. Replacing one with the other would be a category error with better branding.

## 7. Failure Modes

### `Missing Codex gateway API key`

Set one of:

- `CODEX_GATEWAY_API_KEY`
- `LAB_CODEX_GATEWAY_API_KEY`
- `~/.config/stratecode-lab/codex_gateway_api_key`

### Codex points to the wrong host

Override:

```bash
export CODEX_GATEWAY_BASE_URL=https://real-gateway.example.com/v1
./scripts/bootstrap-codex-home.sh
```

### Local tests fail because `go` is missing

Use the repo harnesses. They already execute Go inside Docker:

```bash
./scripts/test-orchestrator-go.sh
./scripts/build-orchestrator-go.sh
```
