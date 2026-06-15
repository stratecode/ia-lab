# Codex Lab Status Board Benchmark

This benchmark checks whether `codex-lab-agent` can complete a small, deterministic web task without external dependencies.

Default files-mode run:

```bash
scripts/run-codex-lab-status-board-benchmark.sh
```

Patch-mode diagnostic run:

```bash
CODEX_LAB_BENCH_MODE=patch scripts/run-codex-lab-status-board-benchmark.sh
```

Raw agent-mode diagnostic run:

```bash
CODEX_LAB_BENCH_MODE=agent scripts/run-codex-lab-status-board-benchmark.sh
```

Artifacts are written to:

```text
benchmarks/runs/codex-lab-status-board/<timestamp>/
```

Primary outputs:

- `summary.json`: machine-readable timing and verification result.
- `summary.md`: human-readable report.
- `codex.stdout.log` / `codex.stderr.log`: local model execution logs.
- `verify.stdout.log` / `verify.stderr.log`: final `npm test` logs.
- `quality.json`: static quality gate output.
- `workspace/`: the benchmark workspace after Codex edits.

Interpretation:

- A passing run means the local model can perform a simple code/test task end to end.
- Files mode measures coding quality by asking the model for complete replacement files and writing them externally. This is the most robust mode for small local models.
- Patch mode measures whether the model can produce a valid unified diff.
- Agent mode measures whether the current Codex/gateway route can actually edit files through tools. At the time of creation, this is expected to fail because the gateway does not implement Codex tool-call roundtrips.
- A failing run is still useful if it shows where the local route breaks: planning, patch generation, file edits, test execution, UI quality, or context/latency.
- This benchmark is intentionally smaller than production work. It is for low-stakes local-route calibration, not a claim that the lab model can replace GPT for serious implementation.
