# Codex Lab Status Board Benchmark Results

## 2026-06-15 Initial Calibration

### Agent mode

Command:

```bash
CODEX_LAB_BENCH_MODE=agent scripts/run-codex-lab-status-board-benchmark.sh
```

Result:

- Elapsed: 120.994 s
- Codex exit: 0
- Diff: empty
- Verification: failed

Interpretation:

The current `/v1/responses` gateway path is text-only for Codex tool use. The model claimed it changed files and ran tests, but no files changed. This is useful negative evidence: raw `codex-lab-agent` is not yet an agentic file-editing route.

### Patch mode

Command:

```bash
CODEX_LAB_BENCH_MODE=patch scripts/run-codex-lab-status-board-benchmark.sh
```

Result:

- Elapsed: 165.389 s
- Codex exit: 0
- Patch apply exit: 128
- Verification: failed

Interpretation:

The model attempted a unified diff, but the patch was corrupt. Unified diff is too brittle as the primary output format for this model.

### Files mode

Command:

```bash
scripts/run-codex-lab-status-board-benchmark.sh
```

Result:

- Elapsed: 347.942 s
- Codex exit: 0
- File write/apply exit: 0
- Verification exit: 1
- Quality exit: 1
- Quality score: 35/100
- Diff: 4 files changed, 131 insertions, 11 deletions

Interpretation:

Files mode is the first viable benchmark mode: the model generated complete replacement files and the runner applied them. The implementation remained incomplete:

- `summarizeServices` averaged null latency as zero.
- `sortServices` used lexical status order instead of explicit severity order.
- HTML omitted status filter buttons.
- `main.js` omitted click handling for status filters.
- CSS omitted `:root` design tokens.
- Static quality score stayed low at 35/100.

### Repair Attempt

A manual repair prompt with test failures and current files exceeded the useful operating envelope. It ran for more than 9 minutes before being cancelled; server metrics showed active generation at roughly 7.6 tokens/s.

Interpretation:

Do not repair this model with broad logs plus full files. Use narrower repair tasks, one file or one failure category at a time.

## Current Benchmark Reading

The local lab route is useful for tightly scoped code generation or review-like suggestions, but not yet for autonomous multi-file implementation through Codex tools.

Best current benchmark mode:

```bash
scripts/run-codex-lab-status-board-benchmark.sh
```

Best next improvement:

Split the benchmark into staged tasks:

1. logic-only `src/dashboard.js`
2. HTML structure
3. JS wiring
4. CSS quality

This will measure whether the model can succeed with smaller context/output slices.
