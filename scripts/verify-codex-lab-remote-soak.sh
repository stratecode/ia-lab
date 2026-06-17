#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIFY_SCRIPT="$ROOT_DIR/scripts/verify-codex-lab-remote.sh"
REAL_REPO=""
RUNS="${VERIFY_CODEX_LAB_SOAK_RUNS:-10}"
TRIALS_PER_RUN="${VERIFY_CODEX_LAB_SOAK_TRIALS_PER_RUN:-3}"
RETRIES_PER_TRIAL="${VERIFY_CODEX_LAB_SOAK_RETRIES_PER_TRIAL:-3}"
ARTIFACT_ROOT="${VERIFY_CODEX_LAB_SOAK_ARTIFACT_ROOT:-$ROOT_DIR/tmp/verify-codex-lab-remote-soak}"
STOP_ON_FAILURE="false"

usage() {
  cat <<'EOF'
Usage:
  scripts/verify-codex-lab-remote-soak.sh --real-repo PATH [--runs N] [--stop-on-failure]

Runs repeated exhaustive remote-local verification cycles and writes an aggregate report.

Each run executes:
  - repeated temp repo write checks
  - one real-repo marker write
  - one tracked-file worktree edit on that real repo

Environment:
  VERIFY_CODEX_LAB_SOAK_RUNS               default: 10
  VERIFY_CODEX_LAB_SOAK_TRIALS_PER_RUN     default: 3
  VERIFY_CODEX_LAB_SOAK_RETRIES_PER_TRIAL  default: 3
  VERIFY_CODEX_LAB_SOAK_ARTIFACT_ROOT      default: ./tmp/verify-codex-lab-remote-soak
EOF
}

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --real-repo)
      REAL_REPO="$2"
      shift 2
      ;;
    --runs)
      RUNS="$2"
      shift 2
      ;;
    --stop-on-failure)
      STOP_ON_FAILURE="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_command python3
[[ -x "$VERIFY_SCRIPT" ]] || die "Missing executable verifier: $VERIFY_SCRIPT"
[[ -n "$REAL_REPO" ]] || die "Missing required --real-repo"
[[ -d "$REAL_REPO" ]] || die "Real repo does not exist: $REAL_REPO"

mkdir -p "$ARTIFACT_ROOT"

timestamp="$(date +%Y%m%d-%H%M%S)"
run_dir="$ARTIFACT_ROOT/$timestamp"
mkdir -p "$run_dir"
summary_file="$run_dir/summary.txt"
report_file="$run_dir/report.json"

pass_count=0
fail_count=0

printf 'verify-codex-lab-remote-soak start\n' | tee "$summary_file"
printf '  real_repo=%s\n' "$REAL_REPO" | tee -a "$summary_file"
printf '  runs=%s\n' "$RUNS" | tee -a "$summary_file"
printf '  trials_per_run=%s\n' "$TRIALS_PER_RUN" | tee -a "$summary_file"
printf '  retries_per_trial=%s\n' "$RETRIES_PER_TRIAL" | tee -a "$summary_file"
printf '  artifacts=%s\n' "$run_dir" | tee -a "$summary_file"

index=1
while [[ "$index" -le "$RUNS" ]]; do
  attempt_dir="$run_dir/run-$index"
  mkdir -p "$attempt_dir"
  stdout_file="$attempt_dir/stdout.log"
  stderr_file="$attempt_dir/stderr.log"
  start_epoch="$(date +%s)"
  printf 'run=%s status=running\n' "$index" | tee -a "$summary_file"
  if VERIFY_CODEX_LAB_TRIALS="$TRIALS_PER_RUN" \
    VERIFY_CODEX_LAB_TRIAL_RETRIES="$RETRIES_PER_TRIAL" \
    VERIFY_CODEX_LAB_ARTIFACT_DIR="$attempt_dir/artifacts" \
    "$VERIFY_SCRIPT" --real-repo "$REAL_REPO" \
    >"$stdout_file" 2>"$stderr_file"; then
    end_epoch="$(date +%s)"
    duration="$((end_epoch - start_epoch))"
    printf 'run=%s status=passed duration_seconds=%s\n' "$index" "$duration" | tee -a "$summary_file"
    pass_count=$((pass_count + 1))
  else
    end_epoch="$(date +%s)"
    duration="$((end_epoch - start_epoch))"
    printf 'run=%s status=failed duration_seconds=%s\n' "$index" "$duration" | tee -a "$summary_file"
    fail_count=$((fail_count + 1))
    if [[ "$STOP_ON_FAILURE" == "true" ]]; then
      break
    fi
  fi
  index=$((index + 1))
done

python3 - "$summary_file" "$report_file" "$REAL_REPO" "$RUNS" "$TRIALS_PER_RUN" "$RETRIES_PER_TRIAL" "$pass_count" "$fail_count" "$run_dir" <<'PY'
import json
import os
import sys

summary_file, report_file, real_repo, runs, trials_per_run, retries_per_trial, pass_count, fail_count, run_dir = sys.argv[1:]
runs = int(runs)
trials_per_run = int(trials_per_run)
retries_per_trial = int(retries_per_trial)
pass_count = int(pass_count)
fail_count = int(fail_count)

results = []
for entry in sorted(os.listdir(run_dir)):
    if not entry.startswith("run-"):
        continue
    attempt_dir = os.path.join(run_dir, entry)
    stdout_path = os.path.join(attempt_dir, "stdout.log")
    stderr_path = os.path.join(attempt_dir, "stderr.log")
    item = {
        "run": entry,
        "stdout": stdout_path,
        "stderr": stderr_path,
        "passed": os.path.exists(stdout_path) and "verify-codex-lab-remote done" in open(stdout_path, "r", encoding="utf-8", errors="replace").read(),
    }
    results.append(item)

report = {
    "real_repo": real_repo,
    "runs_requested": runs,
    "trials_per_run": trials_per_run,
    "retries_per_trial": retries_per_trial,
    "pass_count": pass_count,
    "fail_count": fail_count,
    "success_rate": (pass_count / runs) if runs else 0.0,
    "results": results,
}
with open(report_file, "w", encoding="utf-8") as handle:
    json.dump(report, handle, indent=2)
PY

printf 'verify-codex-lab-remote-soak done\n' | tee -a "$summary_file"
printf '  pass_count=%s\n' "$pass_count" | tee -a "$summary_file"
printf '  fail_count=%s\n' "$fail_count" | tee -a "$summary_file"
printf '  report=%s\n' "$report_file" | tee -a "$summary_file"

if [[ "$fail_count" -gt 0 ]]; then
  exit 1
fi
