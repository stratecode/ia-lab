#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${CODEX_MODE_ENV_FILE:-$ROOT_DIR/.env}"
OUTPUT_DIR="${CODEX_MODE_OUTPUT_DIR:-$HOME/.codex-lab}"
CODEX_BIN="${CODEX_MODE_CODEX_BIN:-codex}"
CODEX_APP_NAME="${CODEX_MODE_APP_NAME:-Codex}"
SAFE_EXEC_RETRIES="${CODEX_MODE_SAFE_EXEC_RETRIES:-3}"
SAFE_EXEC_RETRY_DELAY_SECONDS="${CODEX_MODE_SAFE_EXEC_RETRY_DELAY_SECONDS:-2}"
EXEC_TIMEOUT_SECONDS="${CODEX_MODE_EXEC_TIMEOUT_SECONDS:-90}"
GUIDED_EXEC_MAX_STEPS="${CODEX_MODE_GUIDED_EXEC_MAX_STEPS:-5}"

usage() {
  cat <<'EOF'
Usage:
  scripts/codex-mode.sh interactive [--project-dir PATH] [-- codex_args...]
  scripts/codex-mode.sh interactive-gui [--project-dir PATH] [--prompt TEXT] [--reuse-running-app] [-- codex_app_args...]
  scripts/codex-mode.sh exec --project-dir PATH --prompt TEXT
  scripts/codex-mode.sh guided-exec --project-dir PATH --prompt TEXT
  scripts/codex-mode.sh safe-exec --project-dir PATH --prompt TEXT
  scripts/codex-mode.sh safe-path --project-dir PATH [--prompt TEXT]
  scripts/codex-mode.sh remote-http [health|ready|models]

Description:
  Wraps the supported Codex working modes for this repo:
  1. interactive local Codex TUI against the lab gateway
  2. interactive local Codex desktop app against the lab gateway
  3. one-shot local single-step execution with codex exec
  4. guided local agentic sequence with checkpoints
  5. guarded local agent execution with smoke verification
  6. direct remote HTTP access to the lab gateway

Global environment overrides:
  CODEX_MODE_ENV_FILE
  CODEX_MODE_OUTPUT_DIR
  CODEX_MODE_CODEX_BIN
  CODEX_MODE_APP_NAME
  CODEX_MODE_SAFE_EXEC_RETRIES
  CODEX_MODE_SAFE_EXEC_RETRY_DELAY_SECONDS
  CODEX_MODE_EXEC_TIMEOUT_SECONDS
  CODEX_MODE_GUIDED_EXEC_MAX_STEPS
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

prepare_guided_exec_prompt() {
  local user_prompt="$1"
  local step="$2"
  local max_steps="$3"
  local state_block="${4:-}"
  cat <<EOF
Operate as an execution-first local coding agent against a small lab model.
Mode: guided_agentic_sequence.
Current step: $step/$max_steps.

Rules:
- Execute exactly one concrete next action in this turn.
- Use tools first. No plan, no essay, no roadmap.
- Prefer the smallest action that advances the task and can be checked from real repo state.
- If the task is already complete, run final verification and then print a line starting with GUIDED_SEQUENCE_COMPLETE: followed by exact files changed and validation result.
- If blocked, say BLOCKED: with the concrete blocker after attempting the next diagnostic command.

Current observed state:
$state_block

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

has_guided_sequence_complete_signal() {
  local text="$1"
  printf '%s\n' "$text" | rg -q '^GUIDED_SEQUENCE_COMPLETE:'
}

has_blocked_signal() {
  local text="$1"
  printf '%s\n' "$text" | rg -q '^BLOCKED:'
}

looksLikeSingleFileSmokeTask() {
  local text="$1"
  local lower
  lower="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"
  [[ "$lower" == *"create a file named "* ]] \
    || [[ "$lower" == *"append exactly this single line to "* ]]
}

smoke_task_satisfied() {
  local project_dir="$1"
  local prompt="$2"
  python3 - "$project_dir" "$prompt" <<'PY'
import pathlib
import re
import sys

project_dir = pathlib.Path(sys.argv[1])
prompt = sys.argv[2]

create_match = re.search(
    r"Create a file named\s+(\S+)\s+at repository root with exactly this single line:\s+(.+?)\.\s+Do not modify any other file\.",
    prompt,
    re.S,
)
append_match = re.search(
    r"Append exactly this single line to\s+(\S+)\s+as a new final line:\s+(.+?)\.\s+Do not modify any other file\.",
    prompt,
    re.S,
)

if create_match:
    rel_path, expected = create_match.groups()
    target = project_dir / rel_path
    if not target.is_file():
        sys.exit(1)
    content = target.read_text().replace("\r", "")
    sys.exit(0 if content == expected else 1)

if append_match:
    rel_path, expected = append_match.groups()
    target = project_dir / rel_path
    if not target.is_file():
        sys.exit(1)
    lines = target.read_text().replace("\r", "").splitlines()
    sys.exit(0 if lines and lines[-1] == expected else 1)

sys.exit(1)
PY
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

build_guided_state_block() {
  local project_dir="$1"
  local last_output="${2:-}"
  local external_observation="${3:-}"
  local operational_observation="${4:-}"
  local branch status changed
  branch="$(git -C "$project_dir" branch --show-current 2>/dev/null || true)"
  status="$(git -C "$project_dir" status --short 2>/dev/null | head -n 20)"
  changed="$(git -C "$project_dir" diff --name-only 2>/dev/null | head -n 20)"
  printf 'cwd: %s\n' "$project_dir"
  printf 'branch: %s\n' "${branch:-detached-or-unknown}"
  printf 'git status --short:\n%s\n' "${status:-<clean>}"
  printf 'git diff --name-only:\n%s\n' "${changed:-<no unstaged diff>}"
  if [[ -n "$last_output" ]]; then
    printf 'last agent output excerpt:\n%s\n' "$(guided_output_excerpt "$last_output")"
  fi
  if [[ -n "$external_observation" ]]; then
    printf 'external observation:\n%s\n' "$external_observation"
  fi
  if [[ -n "$operational_observation" ]]; then
    printf 'operational observation:\n%s\n' "$operational_observation"
  fi
}

guided_output_excerpt() {
  local text="$1"
  local excerpt
  excerpt="$(printf '%s\n' "$text" | rg '^(exec|codex|BLOCKED:|GUIDED_SEQUENCE_COMPLETE:|/bin/zsh -lc| exited | succeeded in |v[0-9]+\.[0-9]+\.[0-9]+|[0-9]+\.[0-9]+\.[0-9]+|jq:|Redirecting to |<title>|url=|canonical:|title:|excerpt:|curl |echo |composer |php |git status)' | tail -n 24 || true)"
  if [[ -n "$excerpt" ]]; then
    printf '%s\n' "$excerpt"
    return
  fi
  printf '%s\n' "$text" | tail -n 20
}

extract_guided_fetch_url() {
  local text="$1"
  local lower url
  lower="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"
  url="$(printf '%s\n' "$text" | rg -o 'https?://[^ )"'\''<>]+' | head -n 1 || true)"
  if [[ "$text" == *"Redirecting to"* && -n "$url" ]]; then
    printf '%s\n' "$url"
    return 0
  fi
  if [[ "$lower" != *"http://"* && "$lower" != *"https://"* ]]; then
    return 1
  fi
  if [[ "$lower" != *"open a web browser"* && "$lower" != *"navigate to"* && "$lower" != *"official website"* && "$lower" != *"docs"* && "$lower" != *"download page"* ]]; then
    return 1
  fi
  [[ -n "$url" ]] || return 1
  printf '%s\n' "$url"
}

fetch_guided_observation() {
  local url="$1"
  local tmp_file
  tmp_file="$(mktemp)"
  if ! curl -fsSL "$url" >"$tmp_file" 2>/dev/null; then
    rm -f "$tmp_file"
    return 1
  fi
  python3 - "$url" "$tmp_file" <<'PY'
import html
import pathlib
import re
import sys

url = sys.argv[1]
text = pathlib.Path(sys.argv[2]).read_text(errors="ignore")
title = re.search(r"<title[^>]*>(.*?)</title>", text, re.I | re.S)
canonical = re.search(r'<link[^>]+rel=["\\\']canonical["\\\'][^>]+href=["\\\']([^"\\\']+)["\\\']', text, re.I)
plain = re.sub(r"(?is)<script.*?</script>|<style.*?</style>", " ", text)
plain = re.sub(r"(?s)<[^>]+>", " ", plain)
plain = html.unescape(re.sub(r"\s+", " ", plain)).strip()
parts = [f"url: {url}"]
if canonical:
    parts.append(f"canonical: {canonical.group(1)}")
if title:
    parts.append(f"title: {html.unescape(title.group(1).strip())}")
parts.append(f"excerpt: {plain[:2500]}")
print("\n".join(parts))
PY
  rm -f "$tmp_file"
}

extract_pseudo_exec_command() {
  local text="$1"
  python3 - "$text" <<'PY'
import json
import re
import sys

text = sys.argv[1]

def extract_from_obj(data):
    if not isinstance(data, dict):
        return None
    name = data.get("name")
    args = data.get("arguments")
    if name == "exec_command" and isinstance(args, dict):
        cmd = args.get("cmd")
        if isinstance(cmd, str):
            return cmd
    cmd = data.get("cmd")
    if isinstance(cmd, str):
        return cmd
    return None

json_candidates = []
for match in re.finditer(r"```(?:json)?\s*(.*?)```", text, re.S | re.I):
    json_candidates.append(match.group(1).strip())
for match in re.finditer(r"exec_command\s*:?\s*(\{.*?\})", text, re.S):
    json_candidates.append(match.group(1))
for match in re.finditer(r"exec_command\s*\(\s*(\{.*?\})\s*\)", text, re.S):
    json_candidates.append(match.group(1))

for raw in json_candidates:
    try:
        data = json.loads(raw)
    except Exception:
        continue
    cmd = extract_from_obj(data)
    if isinstance(cmd, str) and cmd.strip() and "{{" not in cmd and "}}" not in cmd:
        print(cmd.strip())
        sys.exit(0)
sys.exit(1)
PY
}

extract_supervisor_shell_commands() {
  local text="$1"
  python3 - "$text" <<'PY'
import re
import sys

text = sys.argv[1]
commands = []
for match in re.finditer(r"```(?:sh|bash|shell|zsh)?\s*\n(.*?)```", text, re.I | re.S):
    for raw_line in match.group(1).splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        commands.append(line)

for command in commands[:3]:
    print(command)
PY
}

is_guided_supervisor_command_allowed() {
  local command="$1"
  python3 - "$command" <<'PY'
import re
import sys

command = sys.argv[1].strip()
lower = command.lower()

blocked = [
    "rm -",
    "sudo ",
    "chmod ",
    "chown ",
    "| sh",
    "| bash",
    "| zsh",
    "> /",
    "../",
]
if any(token in lower for token in blocked):
    sys.exit(1)

if lower == "git status --short":
    sys.exit(0)

if lower.startswith(("curl -s https://", "curl -fssl https://")):
    if "|" not in lower or any(pipe in lower for pipe in ("| jq ", "| grep ", "| cut ", "| head ", "| awk ", "| tr ")):
        sys.exit(0)
    sys.exit(1)

if re.fullmatch(r'echo\s+"?v?\d+\.\d+\.\d+"?\s*>\s*[a-z0-9._-]+\.txt', lower):
    sys.exit(0)

if re.fullmatch(r'echo\s+"\$\(curl\s+-s\s+https://[^\s]+\s+\|\s+jq\s+-r\s+[^)]*\)"\s*>\s*[a-z0-9._-]+\.txt', lower):
    sys.exit(0)

sys.exit(1)
PY
}

run_guided_supervisor_exec() {
  local project_dir="$1"
  local command="$2"
  local capture_file
  capture_file="$(mktemp)"
  if /bin/zsh -lc "$command" >"$capture_file" 2>&1; then
    printf 'supervisor_exec\n/bin/zsh -lc %q in %s\nsucceeded:\n%s\n' "$command" "$project_dir" "$(cat "$capture_file")"
    rm -f "$capture_file"
    return 0
  fi
  printf 'supervisor_exec\n/bin/zsh -lc %q in %s\nfailed:\n%s\n' "$command" "$project_dir" "$(cat "$capture_file")"
  rm -f "$capture_file"
  return 1
}

derive_guided_operational_observation() {
  local project_dir="$1"
  local prompt="$2"
  local output="$3"
  local existing="${4:-}"
  local latest_version status lower
  status="$(git -C "$project_dir" status --short 2>/dev/null || true)"
  lower="$(printf '%s' "$output" | tr '[:upper:]' '[:lower:]')"

  latest_version="$(printf '%s\n' "$output" | rg -o 'v?[0-9]+\.[0-9]+\.[0-9]+' | awk '{v=$0; sub(/^v/, "", v); split(v, p, "."); if (p[1] >= 10) print $0}' | tail -n 1 || true)"
  {
    if [[ -n "$existing" ]]; then
      printf '%s\n' "$existing"
    fi
    if [[ -n "$latest_version" ]]; then
      printf 'known_latest_version_from_tool_output: %s\n' "$latest_version"
    fi
    if [[ "$prompt" == *"migrate this project from Laravel 11"* && "$lower" == *"laravel.com/docs/"* ]]; then
      docs_major="$(printf '%s\n' "$output" | sed -n 's#.*laravel.com/docs/\([0-9][0-9]*\)\.x/.*#\1#p' | tail -n 1)"
      if [[ -n "$docs_major" ]]; then
        printf 'known_latest_laravel_major_from_docs_redirect: %s\n' "$docs_major"
        printf 'required_next_action: resolve exact laravel/framework release from GitHub, inspect https://laravel.com/docs/%s.x/upgrade, then update composer.json with JSON-aware tooling.\n' "$docs_major"
      fi
    fi
    if [[ -z "$status" && "$lower" == *"next step"* && "$lower" == *"composer.json"* ]]; then
      printf 'required_next_action: edit composer.json or run the package manager; do not repeat the same release lookup.\n'
    fi
    if [[ -n "$latest_version" && -z "$status" && "$prompt" == *"migrate this project from Laravel 11"* ]]; then
      major="${latest_version#v}"
      major="${major%%.*}"
      printf 'required_next_action: modify composer.json laravel/framework constraint to ^%s now using a JSON-aware command; do not run the release lookup again.\n' "$major"
    fi
    if [[ -z "$status" && "$lower" == *"guided_sequence_complete:"* && "$lower" == *"no files changed"* ]]; then
      printf 'invalid_completion: task requested migration/update but repository has no diff; continue with a concrete edit or validation command.\n'
    fi
    if [[ -z "$status" && "$lower" == *"already"* && "$lower" == *"latest stable"* && "$prompt" == *"from Laravel 11 to that version"* ]]; then
      printf 'contradiction_guardrail: do not claim Laravel 11 is latest when a retrieved version fact indicates another major version.\n'
    fi
    if [[ "$lower" == *"command not found: composer"* || "$lower" == *"composer command is not installed"* ]]; then
      if [[ -f "$project_dir/docker-compose.yml" || -f "$project_dir/compose.yml" || -f "$project_dir/compose.yaml" ]]; then
        printf 'composer_unavailable_on_host: inspect docker-compose services and run Composer through the project Docker workflow instead of blocking.\n'
      fi
    fi
    if [[ "$lower" == *"sed:"* && "$lower" == *"bad flag in substitute command"* && "$prompt" == *"migrate this project from Laravel 11"* ]]; then
      latest_version="$(printf '%s\n' "$existing" "$output" | sed -n 's/^known_latest_version_from_tool_output: //p' | tail -n 1)"
      if [[ -n "$latest_version" ]]; then
        major="${latest_version#v}"
        major="${major%%.*}"
        printf 'json_edit_required_after_sed_failure: update composer.json require.laravel/framework to ^%s with python3/json, not sed.\n' "$major"
      else
        printf 'json_edit_required_after_sed_failure: update composer.json with python3/json, not sed.\n'
      fi
    fi
  } | awk 'NF && !seen[$0]++'
}

guided_materialize_simple_result() {
  local project_dir="$1"
  local prompt="$2"
  local observation="$3"
  local target latest_version
  target="$(printf '%s\n' "$prompt" | sed -n 's/.*create \([^ ]*\\.txt\) containing only.*/\1/p' | head -n 1)"
  [[ -n "$target" ]] || return 1
  latest_version="$(printf '%s\n' "$observation" | sed -n 's/^known_latest_version_from_tool_output: //p' | tail -n 1)"
  [[ -n "$latest_version" ]] || return 1
  case "$target" in
    */*|..*) return 1 ;;
  esac
  printf '%s\n' "$latest_version" >"$project_dir/$target"
}

guided_materialize_laravel_composer_constraint() {
  local project_dir="$1"
  local observation="$2"
  local latest_version major
  latest_version="$(printf '%s\n' "$observation" | sed -n 's/^known_latest_version_from_tool_output: //p' | tail -n 1)"
  [[ -n "$latest_version" ]] || return 1
  major="${latest_version#v}"
  major="${major%%.*}"
  [[ "$major" =~ ^[0-9]+$ && "$major" -ge 12 ]] || return 1
  [[ -f "$project_dir/composer.json" ]] || return 1

  python3 - "$project_dir/composer.json" "$major" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
major = sys.argv[2]
data = json.loads(path.read_text())
require = data.setdefault("require", {})
if require.get("laravel/framework") == f"^{major}":
    sys.exit(1)
if "laravel/framework" not in require:
    sys.exit(1)
require["laravel/framework"] = f"^{major}"
path.write_text(json.dumps(data, indent=4, ensure_ascii=False) + "\n")
PY
}

guided_docker_compose_observation() {
  local project_dir="$1"
  local capture_file services compose_file composer_service
  compose_file=""
  for candidate in docker-compose.yml docker-compose.yaml compose.yml compose.yaml; do
    if [[ -f "$project_dir/$candidate" ]]; then
      compose_file="$candidate"
      break
    fi
  done
  [[ -n "$compose_file" ]] || return 1
  command -v docker >/dev/null 2>&1 || return 1

  capture_file="$(mktemp)"
  if ! (cd "$project_dir" && docker compose -f "$compose_file" config --services >"$capture_file" 2>&1); then
    printf 'docker_compose_probe_failed:\n%s\n' "$(cat "$capture_file")"
    rm -f "$capture_file"
    return 0
  fi
  services="$(cat "$capture_file")"
  rm -f "$capture_file"
  composer_service="$(printf '%s\n' "$services" | rg '^(php|app|workspace|laravel|api)$' | head -n 1 || true)"
  {
    printf 'docker_compose_file: %s\n' "$compose_file"
    printf 'docker_compose_services:\n%s\n' "$services"
    if [[ -n "$composer_service" ]]; then
      printf 'recommended_composer_command: docker compose -f %s run --rm %s composer show laravel/framework --all\n' "$compose_file" "$composer_service"
      printf 'recommended_artisan_command: docker compose -f %s run --rm %s php artisan --version\n' "$compose_file" "$composer_service"
    fi
  }
}

guided_laravel_release_fallback_observation() {
  local prompt="$1"
  local output="$2"
  local capture_file version major
  [[ "$prompt" == *"Laravel"* || "$prompt" == *"laravel"* ]] || return 1
  [[ "$output" == *"laravel.com/api/v1/releases"* \
    || "$output" == *"api.laravel.com/docs/v1/releases"* \
    || "$prompt" == *"migrate this project from Laravel 11"* && "$output" == *"laravel.com/docs/"*".x/"* \
    || "$output" == *"packagist.org/packages/laravel/framework.json"* && "$output" == *"null"* \
    || "$prompt" == *"framework release tag"* && "$output" == *"api.github.com/repos/laravel/laravel/releases/latest"* ]] || return 1
  command -v curl >/dev/null 2>&1 || return 1
  command -v jq >/dev/null 2>&1 || return 1

  capture_file="$(mktemp)"
  if ! curl -fsSL https://api.github.com/repos/laravel/framework/releases/latest >"$capture_file" 2>/dev/null; then
    rm -f "$capture_file"
    return 1
  fi
  version="$(jq -r '.tag_name // empty' "$capture_file" 2>/dev/null || true)"
  rm -f "$capture_file"
  [[ -n "$version" && "$version" != "null" ]] || return 1
  major="${version#v}"
  major="${major%%.*}"
  printf 'laravel_release_source_fallback: https://api.github.com/repos/laravel/framework/releases/latest\n'
  printf 'known_latest_version_from_tool_output: %s\n' "$version"
  printf 'required_next_action: inspect https://laravel.com/docs/%s.x/upgrade or use project Composer/Docker workflow before changing dependencies.\n' "$major"
}

guided_laravel_upgrade_guide_observation() {
  local prompt="$1"
  local observation="$2"
  local latest_version major url tmp_file
  [[ "$prompt" == *"migrate this project from Laravel 11"* ]] || return 1
  [[ "$observation" != *"laravel_upgrade_guide_checked:"* ]] || return 1
  latest_version="$(printf '%s\n' "$observation" | sed -n 's/^known_latest_version_from_tool_output: //p' | tail -n 1)"
  [[ -n "$latest_version" ]] || return 1
  major="${latest_version#v}"
  major="${major%%.*}"
  [[ "$major" =~ ^[0-9]+$ && "$major" -ge 12 ]] || return 1
  command -v curl >/dev/null 2>&1 || return 1

  url="https://laravel.com/docs/${major}.x/upgrade"
  tmp_file="$(mktemp)"
  if ! curl -fsSL "$url" >"$tmp_file" 2>/dev/null; then
    rm -f "$tmp_file"
    printf 'laravel_upgrade_guide_unavailable: %s\n' "$url"
    return 0
  fi
  python3 - "$url" "$tmp_file" <<'PY'
import html
import pathlib
import re
import sys

url = sys.argv[1]
path = pathlib.Path(sys.argv[2])
text = path.read_text(errors="ignore")
title = re.search(r"<title[^>]*>(.*?)</title>", text, re.I | re.S)
plain = re.sub(r"(?is)<script.*?</script>|<style.*?</style>", " ", text)
plain = re.sub(r"(?s)<[^>]+>", " ", plain)
plain = html.unescape(re.sub(r"\s+", " ", plain)).strip()
print(f"laravel_upgrade_guide_checked: {url}")
if title:
    print(f"laravel_upgrade_guide_title: {html.unescape(title.group(1).strip())}")
print(f"laravel_upgrade_guide_excerpt: {plain[:700]}")
PY
  rm -f "$tmp_file"
}

guided_simple_result_satisfied() {
  local project_dir="$1"
  local prompt="$2"
  python3 - "$project_dir" "$prompt" <<'PY'
import pathlib
import re
import sys

project_dir = pathlib.Path(sys.argv[1])
prompt = sys.argv[2]
match = re.search(r"create\s+([A-Za-z0-9._-]+\.txt)\s+containing\s+only\s+the\s+tag", prompt, re.I)
if not match:
    sys.exit(1)
target = project_dir / match.group(1)
if not target.is_file():
    sys.exit(1)
content = target.read_text(errors="ignore").strip()
if re.fullmatch(r"v?\d+\.\d+\.\d+", content):
    sys.exit(0)
sys.exit(1)
PY
}

guided_repo_change_validation_summary() {
  local project_dir="$1"
  local status changed compose_file
  status="$(git -C "$project_dir" status --short 2>/dev/null || true)"
  [[ -n "$status" ]] || return 1
  changed="$(git -C "$project_dir" diff --name-only 2>/dev/null || true)"

  printf 'guided-exec verified repository change after agent/tool failure.\n'
  printf 'git status --short:\n%s\n' "$status"
  printf 'git diff --name-only:\n%s\n' "${changed:-<no unstaged diff>}"

  if printf '%s\n' "$changed" | rg -q '^composer\.json$'; then
    if ! python3 - "$project_dir/composer.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
framework = data.get("require", {}).get("laravel/framework")
if not isinstance(framework, str) or not framework.strip():
    raise SystemExit("composer.json is missing require.laravel/framework")
print(f"composer_json_valid: laravel/framework={framework}")
PY
    then
      return 1
    fi
  fi

  compose_file=""
  for candidate in docker-compose.yml docker-compose.yaml compose.yml compose.yaml; do
    if [[ -f "$project_dir/$candidate" ]]; then
      compose_file="$candidate"
      break
    fi
  done
  if [[ -n "$compose_file" && -x "$(command -v docker 2>/dev/null || true)" ]]; then
    if (cd "$project_dir" && docker compose -f "$compose_file" config --services >/dev/null); then
      printf 'docker_compose_config_valid: %s\n' "$compose_file"
    else
      printf 'docker_compose_config_invalid: %s\n' "$compose_file" >&2
      return 1
    fi
  fi
}

run_guided_exec() {
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
        die "Unknown guided-exec argument: $1"
        ;;
    esac
  done

  [[ -n "$project_dir" ]] || die "guided-exec mode requires --project-dir"
  [[ -d "$project_dir" ]] || die "Project directory does not exist: $project_dir"
  [[ -n "$prompt" ]] || die "guided-exec mode requires --prompt"

  bootstrap_codex_home
  load_runtime_env

  if ! smoke_exec_succeeds "$project_dir"; then
    die "guided-exec smoke test failed; refusing to run the wider task"
  fi

  local before_head before_status step state_block output capture_file guided_observation operational_observation fetch_url fetched pseudo_cmd shell_cmd supervisor_output docker_observation
  before_head="$(git -C "$project_dir" rev-parse --verify HEAD 2>/dev/null || true)"
  before_status="$(git -C "$project_dir" status --short)"
  output=""
  guided_observation=""
  operational_observation=""

  for ((step = 1; step <= GUIDED_EXEC_MAX_STEPS; step++)); do
    state_block="$(build_guided_state_block "$project_dir" "$output" "$guided_observation" "$operational_observation")"
    capture_file="$(mktemp)"
    if ! run_codex_exec "$project_dir" "$(prepare_guided_exec_prompt "$prompt" "$step" "$GUIDED_EXEC_MAX_STEPS" "$state_block")" >"$capture_file" 2>&1; then
      output="$(cat "$capture_file")"
      printf '%s\n' "$output" >&2
      rm -f "$capture_file"
      if contains_transient_exec_failure "$output" && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: transient failure, retrying step...\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        sleep_before_retry "$step"
        continue
      fi
      if repo_changed_since "$project_dir" "$before_status" "$before_head" && guided_repo_change_validation_summary "$project_dir"; then
        printf 'guided-exec accepted validated repository change despite final agent/tool failure.\n' >&2
        exit 0
      fi
      if looksLikeSingleFileSmokeTask "$prompt" && smoke_task_satisfied "$project_dir" "$prompt"; then
        printf 'guided-exec accepted satisfied smoke task after non-zero exit.\n' >&2
        exit 0
      fi
      die "guided-exec failed at step ${step}/${GUIDED_EXEC_MAX_STEPS}"
    fi
    output="$(cat "$capture_file")"
    rm -f "$capture_file"
    printf '%s\n' "$output"

    pseudo_cmd="$(extract_pseudo_exec_command "$output" || true)"
    if [[ -n "$pseudo_cmd" ]] && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
      if is_guided_supervisor_command_allowed "$pseudo_cmd"; then
        supervisor_output="$(cd "$project_dir" && run_guided_supervisor_exec "$project_dir" "$pseudo_cmd" || true)"
        printf '%s\n' "$supervisor_output"
        output="${output}"$'\n'"${supervisor_output}"
      else
        printf 'guided-exec skipped unsafe or out-of-scope pseudo exec command: %s\n' "$pseudo_cmd" >&2
      fi
    fi
    if [[ -z "$pseudo_cmd" ]] && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
      while IFS= read -r shell_cmd; do
        [[ -n "$shell_cmd" ]] || continue
        if ! is_guided_supervisor_command_allowed "$shell_cmd"; then
          continue
        fi
        supervisor_output="$(cd "$project_dir" && run_guided_supervisor_exec "$project_dir" "$shell_cmd" || true)"
        printf '%s\n' "$supervisor_output"
        output="${output}"$'\n'"${supervisor_output}"
        break
      done < <(extract_supervisor_shell_commands "$output" || true)
    fi
    operational_observation="$(derive_guided_operational_observation "$project_dir" "$prompt" "$output" "$operational_observation")"
    if docker_observation="$(guided_laravel_release_fallback_observation "$prompt" "$output" || true)"; then
      if [[ -n "$docker_observation" ]]; then
        operational_observation="$(printf '%s\n%s\n' "$operational_observation" "$docker_observation" | awk 'NF && !seen[$0]++')"
        printf 'guided-exec recovered Laravel release version via GitHub fallback.\n' >&2
      fi
    fi
    if docker_observation="$(guided_laravel_upgrade_guide_observation "$prompt" "$operational_observation" || true)"; then
      if [[ -n "$docker_observation" ]]; then
        operational_observation="$(printf '%s\n%s\n' "$operational_observation" "$docker_observation" | awk 'NF && !seen[$0]++')"
        printf 'guided-exec checked Laravel upgrade guide before dependency edit.\n' >&2
      fi
    fi
    if guided_materialize_simple_result "$project_dir" "$prompt" "$operational_observation"; then
      printf 'guided-exec materialized simple result from verified tool output.\n' >&2
    fi
    if [[ "$operational_observation" == *"composer_unavailable_on_host"* && "$operational_observation" != *"docker_compose_services:"* ]]; then
      docker_observation="$(guided_docker_compose_observation "$project_dir" || true)"
      if [[ -n "$docker_observation" ]]; then
        operational_observation="$(printf '%s\n%s\n' "$operational_observation" "$docker_observation" | awk 'NF && !seen[$0]++')"
        printf 'guided-exec discovered Docker Compose services for Composer fallback.\n' >&2
      fi
    fi
    if [[ "$operational_observation" == *"json_edit_required_after_sed_failure"* \
      || "$operational_observation" == *"composer_unavailable_on_host"* && "$operational_observation" == *"known_latest_version_from_tool_output"* \
      || "$operational_observation" == *"required_next_action: modify composer.json laravel/framework constraint"* && "$operational_observation" == *"known_latest_version_from_tool_output"* ]]; then
      if [[ "$prompt" == *"migrate this project from Laravel 11"* && "$operational_observation" != *"laravel_upgrade_guide_checked:"* && "$operational_observation" != *"laravel_upgrade_guide_unavailable:"* ]]; then
        printf 'guided-exec deferred Laravel composer.json edit until upgrade guide is checked.\n' >&2
      elif guided_materialize_laravel_composer_constraint "$project_dir" "$operational_observation"; then
        printf 'guided-exec materialized Laravel composer.json constraint from verified release observation.\n' >&2
        output="${output}"$'\n''supervisor_json_edit: composer.json laravel/framework constraint updated from verified release observation.'
        operational_observation="$(derive_guided_operational_observation "$project_dir" "$prompt" "$output" "$operational_observation")"
        if guided_repo_change_validation_summary "$project_dir"; then
          printf 'guided-exec accepted supervised Laravel composer.json change after local validation.\n' >&2
          exit 0
        fi
      fi
    fi
    if guided_simple_result_satisfied "$project_dir" "$prompt"; then
      printf 'guided-exec accepted satisfied simple result from repository state.\n' >&2
      exit 0
    fi

    fetch_url="$(extract_guided_fetch_url "$output" || true)"
    if [[ -n "$fetch_url" ]] && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
      if fetched="$(fetch_guided_observation "$fetch_url")"; then
        guided_observation="$fetched"
        printf 'guided-exec step %s/%s: fetched external context from %s and continuing.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" "$fetch_url" >&2
        continue
      fi
    fi

    if has_guided_sequence_complete_signal "$output"; then
      if repo_changed_since "$project_dir" "$before_status" "$before_head" || [[ -n "$(git -C "$project_dir" status --short)" ]]; then
        exit 0
      fi
      if guided_materialize_simple_result "$project_dir" "$prompt" "$operational_observation"; then
        printf 'guided-exec materialized simple reported result from verified tool output.\n' >&2
        exit 0
      fi
      if [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: rejected no-diff completion, continuing with operational observation.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      die "guided-exec reported completion without a repository change; refusing false positive"
    fi
    if looksLikeSingleFileSmokeTask "$prompt" && smoke_task_satisfied "$project_dir" "$prompt"; then
      printf 'guided-exec accepted completed smoke-task result.\n' >&2
      exit 0
    fi
    if has_blocked_signal "$output"; then
      if [[ "$operational_observation" == *"laravel_release_source_fallback"* && "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: release endpoint failed; continuing with GitHub Laravel release fallback.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      if [[ "$operational_observation" == *"json_edit_required_after_sed_failure"* && "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: sed JSON edit failed; continuing with JSON-aware edit instruction.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      if [[ "$operational_observation" == *"composer_unavailable_on_host"* && "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: composer unavailable on host; continuing toward Docker workflow.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      if [[ -n "$guided_observation" ]] && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: external observation available after BLOCKED, continuing.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      if looksLikeSingleFileSmokeTask "$prompt" && [[ "$step" -lt "$GUIDED_EXEC_MAX_STEPS" ]]; then
        printf 'guided-exec step %s/%s: model reported BLOCKED before satisfying smoke task, continuing with observed state.\n' "$step" "$GUIDED_EXEC_MAX_STEPS" >&2
        continue
      fi
      if repo_changed_since "$project_dir" "$before_status" "$before_head" && guided_repo_change_validation_summary "$project_dir"; then
        printf 'guided-exec accepted validated repository change despite agent BLOCKED response.\n' >&2
        exit 0
      fi
      die "guided-exec blocked at step ${step}/${GUIDED_EXEC_MAX_STEPS}"
    fi
  done

  if repo_changed_since "$project_dir" "$before_status" "$before_head"; then
    if guided_repo_change_validation_summary "$project_dir"; then
      printf 'guided-exec accepted validated repository change without explicit agent completion.\n' >&2
      exit 0
    fi
    die "guided-exec exhausted ${GUIDED_EXEC_MAX_STEPS} steps with an invalid or unverified repository change"
  fi
  die "guided-exec exhausted ${GUIDED_EXEC_MAX_STEPS} steps without a verified repository change"
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
    guided-exec)
      run_guided_exec "$@"
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

if [[ "${CODEX_MODE_LIBRARY:-0}" != "1" ]]; then
  main "$@"
fi
