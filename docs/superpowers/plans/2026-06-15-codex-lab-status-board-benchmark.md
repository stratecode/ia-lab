# Codex Lab Status Board Benchmark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a deterministic mini web-app benchmark that exercises `codex-lab-agent` on a small, testable frontend task and records timing/quality evidence.

**Architecture:** The benchmark uses a dependency-free static web fixture with failing `node:test` tests. A shell runner copies the fixture into `benchmarks/runs/`, invokes `codex-lab-agent`, then runs tests and a static quality gate to produce JSON/Markdown artifacts.

**Tech Stack:** Bash, Node.js built-in test runner, vanilla HTML/CSS/ESM JavaScript, Codex CLI local profile.

---

### Task 1: Benchmark Fixture

**Files:**
- Create: `benchmarks/codex-lab-status-board/spec.md`
- Create: `benchmarks/codex-lab-status-board/prompt.md`
- Create: `benchmarks/codex-lab-status-board/starter/package.json`
- Create: `benchmarks/codex-lab-status-board/starter/index.html`
- Create: `benchmarks/codex-lab-status-board/starter/src/data.js`
- Create: `benchmarks/codex-lab-status-board/starter/src/dashboard.js`
- Create: `benchmarks/codex-lab-status-board/starter/src/main.js`
- Create: `benchmarks/codex-lab-status-board/starter/src/styles.css`
- Create: `benchmarks/codex-lab-status-board/starter/test/dashboard.test.js`
- Create: `benchmarks/codex-lab-status-board/starter/test/ui-source.test.js`
- Create: `benchmarks/codex-lab-status-board/starter/scripts/quality-check.mjs`

- [ ] **Step 1: Write failing behavior tests**

Create `test/dashboard.test.js` with assertions for service summary, filtering, sorting, and latency formatting.

- [ ] **Step 2: Write failing source quality tests**

Create `test/ui-source.test.js` with assertions for required DOM hooks, responsive CSS, semantic status classes, and event wiring.

- [ ] **Step 3: Create minimal broken implementation**

Create starter files with stubs so the baseline fails for the right reason.

- [ ] **Step 4: Verify baseline fails**

Run: `cd benchmarks/codex-lab-status-board/starter && npm test`

Expected: FAIL because `dashboard.js` functions are not implemented and UI hooks are missing.

### Task 2: Benchmark Runner

**Files:**
- Create: `scripts/run-codex-lab-status-board-benchmark.sh`
- Create: `benchmarks/codex-lab-status-board/README.md`

- [ ] **Step 1: Copy fixture into an isolated run directory**

Runner creates `benchmarks/runs/codex-lab-status-board/<timestamp>/workspace`.

- [ ] **Step 2: Capture baseline**

Runner initializes git and records the baseline failing test output.

- [ ] **Step 3: Invoke local Codex**

Runner calls `codex-lab-agent exec --cd <workspace> --sandbox workspace-write --ask-for-approval never` with the benchmark prompt.

- [ ] **Step 4: Verify output**

Runner executes `npm test` and `npm run quality`, writes `summary.json` and `summary.md`, and stores stdout/stderr/log artifacts.

### Task 3: First Real Run

**Files:**
- Generated only under ignored `benchmarks/runs/`.

- [ ] **Step 1: Execute the runner**

Run: `scripts/run-codex-lab-status-board-benchmark.sh`

- [ ] **Step 2: Inspect artifacts**

Open the latest `summary.json` and `summary.md`; report elapsed time, exit status, tests, quality score, and whether manual mentoring is needed.

---

### Completion Evidence

The benchmark is complete when:
- `npm test` fails on the starter before agent execution.
- The runner produces a run directory with `summary.json`, `summary.md`, `codex.stdout.log`, `codex.stderr.log`, `verify.stdout.log`, `verify.stderr.log`, and `quality.json`.
- At least one real `codex-lab-agent` run has been attempted and reported.
