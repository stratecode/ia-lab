#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${CODEX_MODE_ENV_FILE:-$ROOT_DIR/.env}"
OUTPUT_DIR="${CODEX_MODE_OUTPUT_DIR:-$HOME/.codex-lab}"
CODEX_BIN="${CODEX_MODE_CODEX_BIN:-codex}"
CODEX_APP_NAME="${CODEX_MODE_APP_NAME:-Codex}"
SAFE_EXEC_RETRIES="${CODEX_MODE_SAFE_EXEC_RETRIES:-3}"
SAFE_EXEC_RETRY_DELAY_SECONDS="${CODEX_MODE_SAFE_EXEC_RETRY_DELAY_SECONDS:-2}"
EXEC_TIMEOUT_SECONDS="${CODEX_MODE_EXEC_TIMEOUT_SECONDS:-60}"

usage() {
  cat <<'EOF'
Usage:
  scripts/codex-mode.sh interactive [--project-dir PATH] [-- codex_args...]
  scripts/codex-mode.sh interactive-gui [--project-dir PATH] [--prompt TEXT] [--reuse-running-app] [-- codex_app_args...]
  scripts/codex-mode.sh exec --project-dir PATH --prompt TEXT
  scripts/codex-mode.sh safe-exec --project-dir PATH --prompt TEXT
  scripts/codex-mode.sh safe-path --project-dir PATH [--prompt TEXT]
  scripts/codex-mode.sh remote-http [health|ready|models]

Description:
  Wraps the supported Codex working modes for this repo:
  1. interactive local Codex TUI against the lab gateway
  2. interactive local Codex desktop app against the lab gateway
  3. one-shot local agent execution with codex exec
  4. guarded local agent execution with smoke verification
  5. direct remote HTTP access to the lab gateway

Global environment overrides:
  CODEX_MODE_ENV_FILE
  CODEX_MODE_OUTPUT_DIR
  CODEX_MODE_CODEX_BIN
  CODEX_MODE_APP_NAME
  CODEX_MODE_SAFE_EXEC_RETRIES
  CODEX_MODE_SAFE_EXEC_RETRY_DELAY_SECONDS
  CODEX_MODE_EXEC_TIMEOUT_SECONDS
EOF
}

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

bootstrap_codex_home() {
  require_command "$CODEX_BIN"
  "$ROOT_DIR/scripts/bootstrap-codex-home.sh" \
    --env-file "$ENV_FILE" \
    --output-dir "$OUTPUT_DIR" >/dev/null
}

load_runtime_env() {
  export CODEX_HOME="$OUTPUT_DIR"
  set -a
  if [[ -f "$ENV_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$ENV_FILE"
  fi
  # shellcheck disable=SC1090
  source "$OUTPUT_DIR/env"
  set +a
}

gateway_root_url() {
  local base_url="${CODEX_GATEWAY_BASE_URL:-}"
  if [[ -z "$base_url" && -n "${LAB_CODEX_GATEWAY_DOMAIN:-}" ]]; then
    base_url="https://${LAB_CODEX_GATEWAY_DOMAIN}/v1"
  fi
  [[ -n "$base_url" ]] || die "Missing CODEX gateway base URL after bootstrap."
  printf '%s\n' "${base_url%/v1}"
}

urlencode() {
  python3 - "$1" <<'PY'
import sys
import urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=""))
PY
}

lab_guardrails_prefix() {
  cat <<'EOF'
Operate as an execution-first local coding agent against a small lab model.
Mandatory start sequence:
1. Run `pwd`.
2. Run `git rev-parse --show-toplevel`.
3. List 3-5 anchor files from the repository root.
4. Confirm whether you are in the local checkout or a worktree.

Mandatory behavior:
- Inspect the repository before proposing edits.
- If the task requires code changes, modify real project files and run validation commands.
- Do not return a plan unless the user explicitly asked for a plan.
- Abort explicitly if you are in the wrong repository or cannot write.
- Keep the task narrow. If the prompt is too broad for the local model, say so and stop.

Mandatory finish sequence:
1. Run `git status --short`.
2. State the exact files changed.
3. State the validation command(s) executed and their result.
EOF
}

prepare_prompt() {
  local user_prompt="$1"
  printf '%s\n\nUser task:\n%s\n' "$(lab_guardrails_prefix)" "$user_prompt"
}

prepare_safe_exec_prompt() {
  local user_prompt="$1"
  cat <<EOF
Operate as an execution-first local coding agent against a small lab model.
The workspace preflight already succeeded for the correct repository. Do not stop after trivial checks.

Mandatory behavior:
- Modify real project files now if the task requires code changes.
- Keep the task narrow and do not return a plan.
- If you cannot complete the requested change, say so explicitly.
- Before finishing, run \`git status --short\` and state the exact files changed.

User task:
$user_prompt
EOF
}

run_codex_exec() {
  local project_dir="$1"
  local prompt="$2"
  shift 2
  python3 - "$EXEC_TIMEOUT_SECONDS" "$CODEX_BIN" "$project_dir" "$prompt" "$@" <<'PY'
import subprocess
import sys

timeout = int(sys.argv[1])
codex_bin = sys.argv[2]
project_dir = sys.argv[3]
prompt = sys.argv[4]
extra_args = sys.argv[5:]
cmd = [
    codex_bin,
    "exec",
    "--cd",
    project_dir,
    "--dangerously-bypass-approvals-and-sandbox",
    prompt,
    *extra_args,
]
try:
    completed = subprocess.run(cmd, timeout=timeout, check=False)
except subprocess.TimeoutExpired:
    print(f"codex exec timed out after {timeout}s", file=sys.stderr)
    sys.exit(124)
sys.exit(completed.returncode)
PY
}

run_codex_exec_json() {
  local project_dir="$1"
  local prompt="$2"
  shift 2
  python3 - "$EXEC_TIMEOUT_SECONDS" "$CODEX_BIN" "$project_dir" "$prompt" "$@" <<'PY'
import subprocess
import sys

timeout = int(sys.argv[1])
codex_bin = sys.argv[2]
project_dir = sys.argv[3]
prompt = sys.argv[4]
extra_args = sys.argv[5:]
cmd = [
    codex_bin,
    "exec",
    "--json",
    "--cd",
    project_dir,
    "--dangerously-bypass-approvals-and-sandbox",
    prompt,
    *extra_args,
]
try:
    completed = subprocess.run(cmd, timeout=timeout, check=False)
except subprocess.TimeoutExpired:
    print(f"codex exec timed out after {timeout}s", file=sys.stderr)
    sys.exit(124)
sys.exit(completed.returncode)
PY
}

contains_transient_exec_failure() {
  local text="$1"
  local lower
  lower="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"
  [[ "$lower" == *"stream disconnected"* ]] \
    || [[ "$lower" == *"reconnecting"* ]] \
    || [[ "$lower" == *"retrying sampling request"* ]] \
    || [[ "$lower" == *"gateway timeout"* ]] \
    || [[ "$lower" == *"timed out"* ]] \
    || [[ "$lower" == *"connection reset"* ]] \
    || [[ "$lower" == *"transport error"* ]]
}

contains_plan_only_drift() {
  local text="$1"
  local lower
  lower="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"
  [[ "$lower" == *"how can i assist you today"* ]] \
    || [[ "$lower" == *"i will follow these steps"* ]] \
    || [[ "$lower" == *"to update"* && "$lower" == *"here are the steps"* ]]
}

contains_successful_exec_signal() {
  local text="$1"
  [[ "$text" == *$'\nexec\n'* && "$text" == *"succeeded in "* ]] \
    || [[ "$text" == *" succeeded in "* ]]
}

repo_changed_since() {
  local project_dir="$1"
  local before_status="$2"
  local before_head="$3"
  local after_status after_head
  after_status="$(git -C "$project_dir" status --short)"
  if [[ "$after_status" != "$before_status" ]]; then
    return 0
  fi
  after_head="$(git -C "$project_dir" rev-parse --verify HEAD 2>/dev/null || true)"
  if [[ -n "$before_head" && -n "$after_head" && "$before_head" != "$after_head" ]]; then
    return 0
  fi
  return 1
}

sleep_before_retry() {
  local attempt="$1"
  local delay="$SAFE_EXEC_RETRY_DELAY_SECONDS"
  if [[ "$attempt" -le 1 ]]; then
    sleep "$delay"
    return
  fi
  sleep $((delay * attempt))
}

smoke_exec_succeeds() {
  local project_dir="$1"
  local repo_root tmp_file
  repo_root="$(git -C "$project_dir" rev-parse --show-toplevel 2>/dev/null)" || return 1
  printf 'preflight: pwd=%s\n' "$project_dir"
  printf 'preflight: git_root=%s\n' "$repo_root"
  printf 'preflight: anchors\n'
  (cd "$repo_root" && ls -1 | head -n 5) || return 1
  tmp_file="$(mktemp "$repo_root/.codex-lab-smoke.XXXXXX")" || return 1
  printf 'ok\n' >"$tmp_file" || {
    rm -f "$tmp_file"
    return 1
  }
  test -s "$tmp_file" || {
    rm -f "$tmp_file"
    return 1
  }
  rm -f "$tmp_file"
}

run_interactive() {
  local project_dir="$PWD"
  local codex_args=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --project-dir)
        project_dir="$2"
        shift 2
        ;;
      --)
        shift
        codex_args=("$@")
        break
        ;;
      *)
        die "Unknown interactive argument: $1"
        ;;
    esac
  done

  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  bootstrap_codex_home
  load_runtime_env
  exec "$CODEX_BIN" --cd "$project_dir" "${codex_args[@]}"
}

run_interactive_gui() {
  local project_dir="$PWD"
  local codex_args=()
  local prompt=""
  local reuse_running_app="false"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --project-dir)
        project_dir="$2"
        shift 2
        ;;
      --prompt)
        prompt="$2"
        shift 2
        ;;
      --reuse-running-app)
        reuse_running_app="true"
        shift
        ;;
      --)
        shift
        codex_args=("$@")
        break
        ;;
      *)
        die "Unknown interactive-gui argument: $1"
        ;;
    esac
  done

  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  bootstrap_codex_home
  load_runtime_env

  if [[ "$reuse_running_app" != "true" ]] && command -v osascript >/dev/null 2>&1; then
    osascript -e "if application \"$CODEX_APP_NAME\" is running then quit app \"$CODEX_APP_NAME\"" >/dev/null 2>&1 || true
    sleep 1
  fi

  "$CODEX_BIN" app "${codex_args[@]}" "$project_dir" >/dev/null 2>&1 &
  disown || true
  sleep 2

  local deep_link="codex://new?path=$(urlencode "$project_dir")"
  if [[ -n "$prompt" ]]; then
    deep_link="${deep_link}&prompt=$(urlencode "$prompt")"
  fi
  if command -v open >/dev/null 2>&1; then
    exec open "$deep_link"
  fi
  exec "$CODEX_BIN" app "${codex_args[@]}" "$project_dir"
}

run_exec() {
  local project_dir=""
  local prompt=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --project-dir)
        project_dir="$2"
        shift 2
        ;;
      --prompt)
        prompt="$2"
        shift 2
        ;;
      *)
        die "Unknown exec argument: $1"
        ;;
    esac
  done

  [[ -n "$project_dir" ]] || die "exec mode requires --project-dir"
  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  [[ -n "$prompt" ]] || die "exec mode requires --prompt"

  bootstrap_codex_home
  load_runtime_env
  prompt="$(prepare_prompt "$prompt")"
  run_codex_exec "$project_dir" "$prompt"
}

run_safe_exec() {
  local project_dir=""
  local prompt=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --project-dir)
        project_dir="$2"
        shift 2
        ;;
      --prompt)
        prompt="$2"
        shift 2
        ;;
      *)
        die "Unknown safe-exec argument: $1"
        ;;
    esac
  done

  [[ -n "$project_dir" ]] || die "safe-exec mode requires --project-dir"
  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  [[ -n "$prompt" ]] || die "safe-exec mode requires --prompt"

  bootstrap_codex_home
  load_runtime_env

  if ! smoke_exec_succeeds "$project_dir"; then
    die "safe-exec smoke test failed; refusing to run the wider task"
  fi

  local before_head before_status output attempt capture_file
  before_head="$(git -C "$project_dir" rev-parse --verify HEAD 2>/dev/null || true)"
  before_status="$(git -C "$project_dir" status --short)"
  attempt=1
  while [[ "$attempt" -le "$SAFE_EXEC_RETRIES" ]]; do
    capture_file="$(mktemp)"
    if run_codex_exec "$project_dir" "$(prepare_safe_exec_prompt "$prompt")" >"$capture_file" 2>&1; then
      output="$(cat "$capture_file")"
      printf '%s\n' "$output"
      rm -f "$capture_file"
      if repo_changed_since "$project_dir" "$before_status" "$before_head"; then
        exit 0
      fi
      if [[ "$attempt" -lt "$SAFE_EXEC_RETRIES" ]] && contains_plan_only_drift "$output"; then
        printf 'safe-exec retry %s/%s: plan-only drift detected, retrying...\n' "$attempt" "$SAFE_EXEC_RETRIES" >&2
        sleep_before_retry "$attempt"
        attempt=$((attempt + 1))
        continue
      fi
      die "safe-exec finished without a repository change; likely plan-only drift or low-precision completion. Retry with a narrower prompt or native GPT."
    fi

    output="$(cat "$capture_file")"
    printf '%s\n' "$output" >&2
    rm -f "$capture_file"
    if repo_changed_since "$project_dir" "$before_status" "$before_head" && contains_successful_exec_signal "$output"; then
      printf 'safe-exec accepted verified repository change despite transport failure.\n' >&2
      exit 0
    fi
    if [[ "$attempt" -lt "$SAFE_EXEC_RETRIES" ]] && contains_transient_exec_failure "$output"; then
      printf 'safe-exec retry %s/%s: transient transport failure detected, retrying...\n' "$attempt" "$SAFE_EXEC_RETRIES" >&2
      sleep_before_retry "$attempt"
      attempt=$((attempt + 1))
      continue
    fi
    die "safe-exec failed after ${attempt} attempt(s)"
  done

  die "safe-exec exhausted retries without a verified repository change"
}

run_safe_path() {
  local project_dir="$PWD"
  local prompt=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --project-dir)
        project_dir="$2"
        shift 2
        ;;
      --prompt)
        prompt="$2"
        shift 2
        ;;
      *)
        die "Unknown safe-path argument: $1"
        ;;
    esac
  done

  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  bootstrap_codex_home
  load_runtime_env

  local default_prompt
  default_prompt="Start in Local mode for this project. First verify the repo with pwd, git root, anchor files, and a reversible write smoke test. Only after that should you accept a real coding task."
  run_interactive_gui --project-dir "$project_dir" --prompt "${prompt:-$default_prompt}"
}

run_remote_http() {
  local action="${1:-models}"
  bootstrap_codex_home
  load_runtime_env

  local gateway_root
  gateway_root="$(gateway_root_url)"

  case "$action" in
    health)
      exec curl -sk "${gateway_root}/health"
      ;;
    ready)
      exec curl -sk "${gateway_root}/ready"
      ;;
    models)
      exec curl -sk \
        -H "Authorization: Bearer ${CODEX_GATEWAY_API_KEY}" \
        "${gateway_root}/v1/models"
      ;;
    *)
      die "Unknown remote-http action: $action"
      ;;
  esac
}

main() {
  local mode="${1:-}"
  [[ -n "$mode" ]] || {
    usage
    exit 1
  }
  shift

  case "$mode" in
    -h|--help)
      usage
      ;;
    interactive)
      run_interactive "$@"
      ;;
    interactive-gui)
      run_interactive_gui "$@"
      ;;
    exec)
      run_exec "$@"
      ;;
    safe-exec)
      run_safe_exec "$@"
      ;;
    safe-path)
      run_safe_path "$@"
      ;;
    remote-http)
      run_remote_http "$@"
      ;;
    *)
      die "Unknown mode: $mode"
      ;;
  esac
}

main "$@"
