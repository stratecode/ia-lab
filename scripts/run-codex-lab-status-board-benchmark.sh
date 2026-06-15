#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CASE_DIR="$ROOT_DIR/benchmarks/codex-lab-status-board"
STARTER_DIR="$CASE_DIR/starter"
RUN_ROOT="$ROOT_DIR/benchmarks/runs/codex-lab-status-board"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="$RUN_ROOT/$STAMP"
WORKSPACE="$RUN_DIR/workspace"
MODE="${CODEX_LAB_BENCH_MODE:-files}"
CODEX_BIN="${CODEX_LAB_AGENT_BIN:-codex-lab-agent}"
PATCH_CODEX_BIN="${CODEX_LAB_BIN:-codex-lab}"
TIMEOUT_SECONDS="${CODEX_LAB_BENCH_TIMEOUT_SECONDS:-900}"
TIMEOUT_CMD=()
if command -v timeout >/dev/null 2>&1; then
  TIMEOUT_CMD=(timeout "$TIMEOUT_SECONDS")
elif command -v gtimeout >/dev/null 2>&1; then
  TIMEOUT_CMD=(gtimeout "$TIMEOUT_SECONDS")
fi

mkdir -p "$WORKSPACE"
cp -R "$STARTER_DIR"/. "$WORKSPACE"/
cp "$CASE_DIR/spec.md" "$WORKSPACE/SPEC.md"

if [[ "$MODE" == "patch" || "$MODE" == "files" ]]; then
  {
    if [[ "$MODE" == "files" ]]; then
      cat "$CASE_DIR/prompt.files.md"
    else
      cat "$CASE_DIR/prompt.patch.md"
    fi
    printf '\n\n# SPEC.md\n\n```markdown\n'
    cat "$CASE_DIR/spec.md"
    printf '\n```\n'
    for file in index.html src/data.js src/dashboard.js src/main.js src/styles.css test/dashboard.test.js test/ui-source.test.js scripts/quality-check.mjs; do
      printf '\n# %s\n\n```text\n' "$file"
      cat "$WORKSPACE/$file"
      printf '\n```\n'
    done
  } > "$RUN_DIR/prompt.md"
else
  cat "$CASE_DIR/prompt.md" > "$RUN_DIR/prompt.md"
fi

(
  cd "$WORKSPACE"
  git init -q
  git config user.name "Codex Benchmark"
  git config user.email "codex-benchmark@example.invalid"
  git add .
  git commit -qm "baseline"
)

set +e
(
  cd "$WORKSPACE"
  npm test
) >"$RUN_DIR/baseline.stdout.log" 2>"$RUN_DIR/baseline.stderr.log"
BASELINE_EXIT=$?
set -e

START_EPOCH="$(date +%s)"
START_NS="$(python3 - <<'PY'
import time
print(time.time_ns())
PY
)"

set +e
if [[ "$MODE" == "patch" || "$MODE" == "files" ]]; then
  "${TIMEOUT_CMD[@]}" "$PATCH_CODEX_BIN" exec \
    --skip-git-repo-check \
    --sandbox read-only \
    "$(cat "$RUN_DIR/prompt.md")" \
    >"$RUN_DIR/codex.stdout.log" \
    2>"$RUN_DIR/codex.stderr.log"
else
  "${TIMEOUT_CMD[@]}" "$CODEX_BIN" exec \
    --cd "$WORKSPACE" \
    --skip-git-repo-check \
    --sandbox workspace-write \
    "$(cat "$RUN_DIR/prompt.md")" \
    >"$RUN_DIR/codex.stdout.log" \
    2>"$RUN_DIR/codex.stderr.log"
fi
CODEX_EXIT=$?
set -e

PATCH_APPLY_EXIT=0
if [[ "$MODE" == "files" ]]; then
  set +e
  python3 - <<'PY' "$RUN_DIR/codex.stdout.log" "$WORKSPACE" "$RUN_DIR/generated-files.json"
import json
import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text(errors="replace")
workspace = pathlib.Path(sys.argv[2])
out_path = pathlib.Path(sys.argv[3])
allowed = ["index.html", "src/dashboard.js", "src/main.js", "src/styles.css"]
written = []

for file in allowed:
    pattern = rf"===FILE:{re.escape(file)}===\s*(.*?)\s*===END_FILE==="
    match = re.search(pattern, source, re.S)
    if not match:
        continue
    content = match.group(1)
    target = workspace / file
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content.rstrip() + "\n")
    written.append(file)

out_path.write_text(json.dumps({"written": written, "missing": [f for f in allowed if f not in written]}, indent=2) + "\n")
if set(written) != set(allowed):
    sys.exit(1)
PY
  PATCH_APPLY_EXIT=$?
  set -e
  echo "" > "$RUN_DIR/generated.patch"
  echo "" > "$RUN_DIR/patch-apply.stdout.log"
  if [[ "$PATCH_APPLY_EXIT" -ne 0 ]]; then
    cat "$RUN_DIR/generated-files.json" > "$RUN_DIR/patch-apply.stderr.log"
  else
    echo "" > "$RUN_DIR/patch-apply.stderr.log"
  fi
elif [[ "$MODE" == "patch" ]]; then
  set +e
  python3 - <<'PY' "$RUN_DIR/codex.stdout.log" "$RUN_DIR/generated.patch"
import pathlib
import re
import sys

source = pathlib.Path(sys.argv[1]).read_text(errors="replace")
target = pathlib.Path(sys.argv[2])

fenced = re.search(r"```(?:diff|patch)?\s*(diff --git .*?)```", source, re.S)
if fenced:
    patch = fenced.group(1).strip() + "\n"
else:
    index = source.find("diff --git ")
    patch = source[index:].strip() + "\n" if index >= 0 else ""

target.write_text(patch)
if not patch:
    sys.exit(1)
PY
  EXTRACT_EXIT=$?
  set -e
  if [[ "$EXTRACT_EXIT" -ne 0 ]]; then
    PATCH_APPLY_EXIT=1
  else
    set +e
    (
      cd "$WORKSPACE"
      git apply --whitespace=fix "$RUN_DIR/generated.patch"
    ) >"$RUN_DIR/patch-apply.stdout.log" 2>"$RUN_DIR/patch-apply.stderr.log"
    PATCH_APPLY_EXIT=$?
    set -e
  fi
else
  echo "" > "$RUN_DIR/generated.patch"
  echo "{}" > "$RUN_DIR/generated-files.json"
  echo "" > "$RUN_DIR/patch-apply.stdout.log"
  echo "" > "$RUN_DIR/patch-apply.stderr.log"
fi

END_EPOCH="$(date +%s)"
END_NS="$(python3 - <<'PY'
import time
print(time.time_ns())
PY
)"

set +e
(
  cd "$WORKSPACE"
  npm test
) >"$RUN_DIR/verify.stdout.log" 2>"$RUN_DIR/verify.stderr.log"
VERIFY_EXIT=$?
(
  cd "$WORKSPACE"
  npm run quality
) >"$RUN_DIR/quality.stdout.log" 2>"$RUN_DIR/quality.stderr.log"
QUALITY_EXIT=$?
set -e

(
  cd "$WORKSPACE"
  git status --short > "$RUN_DIR/git-status.txt"
  git diff --stat > "$RUN_DIR/git-diff-stat.txt"
  git diff > "$RUN_DIR/git-diff.patch"
)

QUALITY_JSON='{}'
if [[ -s "$RUN_DIR/quality.stdout.log" ]]; then
  QUALITY_JSON="$(cat "$RUN_DIR/quality.stdout.log")"
fi

python3 - <<'PY' "$RUN_DIR" "$WORKSPACE" "$BASELINE_EXIT" "$CODEX_EXIT" "$VERIFY_EXIT" "$QUALITY_EXIT" "$START_EPOCH" "$END_EPOCH" "$START_NS" "$END_NS" "$TIMEOUT_SECONDS" "$QUALITY_JSON" "$MODE" "$PATCH_APPLY_EXIT"
import json
import pathlib
import sys

run_dir = pathlib.Path(sys.argv[1])
workspace = pathlib.Path(sys.argv[2])
baseline_exit = int(sys.argv[3])
codex_exit = int(sys.argv[4])
verify_exit = int(sys.argv[5])
quality_exit = int(sys.argv[6])
start_epoch = int(sys.argv[7])
end_epoch = int(sys.argv[8])
start_ns = int(sys.argv[9])
end_ns = int(sys.argv[10])
timeout_seconds = int(sys.argv[11])
mode = sys.argv[13]
patch_apply_exit = int(sys.argv[14])
try:
    quality = json.loads(sys.argv[12])
except json.JSONDecodeError:
    raw_quality = sys.argv[12]
    start = raw_quality.find("{")
    end = raw_quality.rfind("}")
    if start >= 0 and end > start:
        try:
            quality = json.loads(raw_quality[start : end + 1])
        except json.JSONDecodeError:
            quality = {"parse_error": True, "raw": raw_quality}
    else:
        quality = {"parse_error": True, "raw": raw_quality}

def tail(path, limit=20):
    value = path.read_text(errors="replace") if path.exists() else ""
    lines = value.splitlines()
    return "\n".join(lines[-limit:])

summary = {
    "benchmark": "codex-lab-status-board",
    "mode": mode,
    "run_dir": str(run_dir),
    "workspace": str(workspace),
    "start_epoch": start_epoch,
    "end_epoch": end_epoch,
    "elapsed_seconds": round((end_ns - start_ns) / 1_000_000_000, 3),
    "timeout_seconds": timeout_seconds,
    "baseline_exit": baseline_exit,
    "codex_exit": codex_exit,
    "patch_apply_exit": patch_apply_exit,
    "verify_exit": verify_exit,
    "quality_exit": quality_exit,
    "passed": codex_exit == 0 and patch_apply_exit == 0 and verify_exit == 0 and quality_exit == 0,
    "quality": quality,
    "git_status": (run_dir / "git-status.txt").read_text(errors="replace").splitlines() if (run_dir / "git-status.txt").exists() else [],
    "diff_stat": (run_dir / "git-diff-stat.txt").read_text(errors="replace"),
    "codex_stdout_tail": tail(run_dir / "codex.stdout.log"),
    "codex_stderr_tail": tail(run_dir / "codex.stderr.log"),
    "patch_apply_stderr_tail": tail(run_dir / "patch-apply.stderr.log"),
    "verify_stdout_tail": tail(run_dir / "verify.stdout.log"),
    "verify_stderr_tail": tail(run_dir / "verify.stderr.log"),
}
(run_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")

quality_score = quality.get("score", "n/a")
quality_max = quality.get("maxScore", "n/a")
summary_md = f"""# Codex Lab Status Board Benchmark

- Mode: `{mode}`
- Passed: `{summary['passed']}`
- Elapsed seconds: `{summary['elapsed_seconds']}`
- Baseline exit: `{baseline_exit}`
- Codex exit: `{codex_exit}`
- Patch apply exit: `{patch_apply_exit}`
- Verify exit: `{verify_exit}`
- Quality exit: `{quality_exit}`
- Quality score: `{quality_score}/{quality_max}`

## Diff Stat

```text
{summary['diff_stat'].strip()}
```

## Codex stdout tail

```text
{summary['codex_stdout_tail']}
```

## Patch apply stderr tail

```text
{summary['patch_apply_stderr_tail']}
```

## Verify stdout tail

```text
{summary['verify_stdout_tail']}
```
"""
(run_dir / "summary.md").write_text(summary_md)
PY

echo "Benchmark run: $RUN_DIR"
cat "$RUN_DIR/summary.md"

if [[ "$PATCH_APPLY_EXIT" -ne 0 || "$VERIFY_EXIT" -ne 0 || "$QUALITY_EXIT" -ne 0 ]]; then
  exit 1
fi
