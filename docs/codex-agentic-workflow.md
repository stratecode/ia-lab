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

- `~/.codex-lab/config.toml`
- `~/.codex-lab/env`

This is intentionally separate from the normal `~/.codex` profile used for OpenAI or ChatGPT-backed Codex.
Mixing both worlds in one home is how you get a local-model session with cloud-flavored amnesia.

If you want a different env file or output directory:

```bash
./scripts/bootstrap-codex-home.sh --env-file /path/to/.env --output-dir /tmp/codex-home
```

## 3. Start Codex Against the Lab Gateway

```bash
export CODEX_HOME="$HOME/.codex-lab"
set -a && source "$CODEX_HOME/env" && set +a
codex
```

Expected outcome:

- Codex uses the `lab-codex-gateway` provider
- requests go to the lab gateway `.../v1`
- repo-local `AGENTS.md` instructions are available automatically in this repository

Recommended launchers:

```bash
./scripts/codex-openai
./scripts/codex-lab-tui --project-dir /absolute/path/to/project
./scripts/codex-lab-app --project-dir /absolute/path/to/project
./scripts/codex-lab-exec --project-dir /absolute/path/to/project --prompt 'Implement the smallest fix that makes the failing test pass.'
./scripts/codex-lab-safe-path --project-dir /absolute/path/to/project
```

What they do:

- `codex-openai`
  Uses the default `~/.codex` home. This is the normal OpenAI-backed path.
- `codex-lab-tui`
  Uses `~/.codex-lab` and starts terminal Codex against the lab gateway.
- `codex-lab-app`
  Uses `~/.codex-lab`, launches the desktop app, and opens a new local thread for the exact project path.
  The deep link reliably targets the right local workspace, but prompt submission still depends on current Codex app behavior; treat it as "open the right local thread" rather than "headless full execution".
- `codex-lab-exec`
  Uses `~/.codex-lab`, runs a smoke write test, then runs a guarded `codex exec`. If the run ends without repository changes, it fails explicitly.
- `codex-lab-safe-path`
  Opens the app with a local-thread bootstrap prompt that forces repo verification before real work.

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

When you need hard evidence that Codex is operating remotely on a local repo through the lab gateway:

```bash
./scripts/verify-codex-lab-remote.sh --real-repo /absolute/path/to/project
```

That harness verifies:

- repeated temp-repo writes through `lab-codex-gateway`
- transcript `cwd` and `model_provider` for each run
- a marker write in the exact real repo path you passed
- a tracked-file edit in a detached worktree created from that same repo, without leaving the main checkout dirty

If you need stability evidence instead of one strong sample:

```bash
./scripts/verify-codex-lab-remote-soak.sh --real-repo /absolute/path/to/project --runs 10
```

That soak harness repeats the full verification cycle, stores per-run artifacts, and emits an aggregate success rate.

## 6. When To Use Codex vs `lab-agent`

Use Codex when:

- you want direct repository work in this checkout
- you need repo-local instructions and fast iterative editing
- the task is coding-heavy and local terminal-driven

Use `lab-agent tui` plus `lab-agentd` when:

- the work should follow the governed initiative path
- you need approvals, selective execution, and initiative reconciliation
- you want the orchestrator to remain the system of record

Those surfaces are complementary. In this repo, the orchestrator path is the primary operating model again; Codex through the gateway is the faster side lane, not the traffic law.

## 7. Failure Modes

### Codex edits the wrong place or "does nothing"

The Codex app has three relevant modes:

- `Local`
  Edits your real checkout.
- `Worktree`
  Edits a separate checkout under `$CODEX_HOME/worktrees`.
- `Cloud`
  Runs remotely and is not the default path we want here.

If you choose `Worktree`, do not stare at your original repo waiting for changes to appear by telepathy.
Use `codex-lab-app` or `codex-lab-safe-path` to force a fresh local thread on the exact project path.

### The model writes a plan instead of changing code

Use a narrower prompt or `./scripts/codex-lab-exec`.

That wrapper forces:

- repo verification
- a reversible write smoke test
- final `git status --short`
- an explicit failure if the run finishes without a repository change

If even that fails, the task is probably too broad for the local model and should move to native GPT.

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
