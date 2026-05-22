#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL=""
API_KEY=""
API_KEY_SOURCE=""
OPERATOR="${LAB_BENCHMARK_OPERATOR:-benchmark-runner}"
POLL_INTERVAL="${LAB_BENCHMARK_POLL_INTERVAL:-2}"
TIMEOUT_SECONDS="${LAB_BENCHMARK_TIMEOUT_SECONDS:-240}"
BRIDGE_POLL_INTERVAL="${LAB_BENCHMARK_BRIDGE_POLL_INTERVAL:-2s}"
BRIDGE_HEARTBEAT_INTERVAL="${LAB_BENCHMARK_BRIDGE_HEARTBEAT_INTERVAL:-10s}"
WORKDIR_ROOT="${LAB_BENCHMARK_WORKDIR_ROOT:-/tmp}"
REPO_CATALOG_PATH="${ROOT_DIR}/benchmarks/repos.default.json"
CONFIG_PATH="${ROOT_DIR}/benchmarks/benchmark.local.json"
CASE_DIR="${ROOT_DIR}/benchmarks/cases"
REPORT_ROOT="${ROOT_DIR}/benchmarks/runs/$(date -u +%Y%m%dT%H%M%SZ)"

mkdir -p "$REPORT_ROOT"

log() {
  printf '[benchmark] %s\n' "$*"
}

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  local attempt max_attempts tmp code curl_status
  max_attempts=5
  for ((attempt=1; attempt<=max_attempts; attempt++)); do
    tmp="$(mktemp)"
    if [[ -n "$body" ]]; then
      code="$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Content-Type: application/json" \
        -d "$body" 2>"$tmp.stderr" || true)"
    else
      code="$(curl -sS -o "$tmp" -w '%{http_code}' -X "$method" "$BASE_URL$path" \
        -H "Authorization: Bearer $API_KEY" 2>"$tmp.stderr" || true)"
    fi
    curl_status=$?
    if [[ "$curl_status" -eq 0 && "$code" =~ ^2[0-9][0-9]$ ]]; then
      cat "$tmp"
      rm -f "$tmp" "$tmp.stderr"
      return 0
    fi
    if [[ "$attempt" -lt "$max_attempts" && ( "$code" == "502" || "$code" == "503" || "$code" == "504" || "$code" == "000" ) ]]; then
      sleep "$attempt"
      rm -f "$tmp" "$tmp.stderr"
      continue
    fi
    cat "$tmp.stderr" >&2 || true
    cat "$tmp" >&2 || true
    rm -f "$tmp" "$tmp.stderr"
    return 22
  done
}

normalize_base_url() {
  local raw="$1"
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  raw="${raw%/}"
  printf '%s' "$raw"
}

append_unique_line() {
  local value="$1"
  local file="$2"
  [[ -n "$value" ]] || return 0
  if ! grep -Fxq "$value" "$file" 2>/dev/null; then
    printf '%s\n' "$value" >> "$file"
  fi
}

collect_base_url_candidates() {
  local tmp
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' RETURN
  append_unique_line "$(normalize_base_url "${LAB_AGENT_BASE_URL:-}")" "$tmp"
  append_unique_line "$(normalize_base_url "${LAB_ORCHESTRATOR_BASE_URL:-}")" "$tmp"
  if [[ -n "${LAB_COCKPIT_DOMAIN:-}" ]]; then
    append_unique_line "$(normalize_base_url "https://${LAB_COCKPIT_DOMAIN}${LAB_ORCHESTRATOR_PROXY_PATH:-/orchestrator/}")" "$tmp"
  fi
  if [[ -n "${LAB_ORCHESTRATOR_HOST:-}" && -n "${LAB_ORCHESTRATOR_PORT:-}" ]]; then
    append_unique_line "$(normalize_base_url "http://${LAB_ORCHESTRATOR_HOST}:${LAB_ORCHESTRATOR_PORT}")" "$tmp"
  fi
  if [[ -n "${LAB_STATIC_IP:-}" && -n "${LAB_ORCHESTRATOR_PORT:-}" ]]; then
    append_unique_line "$(normalize_base_url "http://${LAB_STATIC_IP}:${LAB_ORCHESTRATOR_PORT}")" "$tmp"
  fi
  cat "$tmp"
}

collect_api_key_candidates() {
  local tmp
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' RETURN
  append_unique_line "LAB_ORCHESTRATOR_CLEANUP_API_KEY:${LAB_ORCHESTRATOR_CLEANUP_API_KEY:-}" "$tmp"
  append_unique_line "LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:${LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:-}" "$tmp"
  append_unique_line "LAB_AGENT_API_KEY:${LAB_AGENT_API_KEY:-}" "$tmp"
  cat "$tmp"
}

probe_base_url() {
  local url="$1"
  local line key status
  while IFS= read -r line; do
    key="${line#*:}"
    [[ -n "$key" ]] || continue
    status="$(curl -ksS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $key" "${url}/ready" || true)"
    if [[ "$status" == "200" ]]; then
      return 0
    fi
  done < <(collect_api_key_candidates)
  return 1
}

select_base_url() {
  local url
  while IFS= read -r url; do
    [[ -n "$url" ]] || continue
    if probe_base_url "$url"; then
      BASE_URL="$url"
      return 0
    fi
  done < <(collect_base_url_candidates)
  return 1
}

validate_api_key_for_base_url() {
  local url="$1"
  local key="$2"
  local approvals_status bridges_status
  [[ -n "$key" ]] || return 1
  approvals_status="$(curl -ksS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $key" "${url}/approvals" || true)"
  bridges_status="$(curl -ksS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $key" "${url}/bridges" || true)"
  [[ "$approvals_status" == "200" && "$bridges_status" == "200" ]]
}

select_api_key() {
  local line name key
  while IFS= read -r line; do
    name="${line%%:*}"
    key="${line#*:}"
    if validate_api_key_for_base_url "$BASE_URL" "$key"; then
      API_KEY="$key"
      API_KEY_SOURCE="$name"
      return 0
    fi
  done < <(collect_api_key_candidates)
  return 1
}

remote_upsert_benchmark_api_key() {
  local ssh_host="${LAB_BENCHMARK_SSH_HOST:-${LAB_COCKPIT_DOMAIN:-${LAB_HOSTNAME:-}}}"
  local ssh_user="${LAB_BENCHMARK_SSH_USER:-${LAB_USER:-}}"
  local ssh_key="${LAB_BENCHMARK_SSH_KEY:-${ROOT_DIR}/ssh/lab}"
  local key_name="${LAB_BENCHMARK_API_KEY_NAME:-benchmark-operator}"
  local raw_key
  raw_key="$(python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(32))
PY
)"
  [[ -n "$ssh_host" && -n "$ssh_user" ]] || return 1
  ssh -i "$ssh_key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${ssh_user}@${ssh_host}" \
    "export RAW_KEY='${raw_key}' KEY_NAME='${key_name}' POSTGRES_USER='${LAB_POSTGRES_USER:-orchestrator}' POSTGRES_DB='${LAB_POSTGRES_DB:-orchestrator}'; /bin/bash -s" <<'SH'
set -euo pipefail
KEY_HASH="$(python3 - <<'PY'
import hashlib
import os
print(hashlib.sha256(os.environ["RAW_KEY"].encode()).hexdigest())
PY
)"
KEY_ID="$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
docker exec -i orchestrator-postgres psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" <<SQL
INSERT INTO api_keys (id, key_hash, name, scope, is_active, created_at)
SELECT '${KEY_ID}'::uuid, '${KEY_HASH}', '${KEY_NAME}', 'operator', true, NOW()
WHERE NOT EXISTS (
  SELECT 1 FROM api_keys WHERE name = '${KEY_NAME}'
);
UPDATE api_keys
   SET key_hash='${KEY_HASH}',
       scope='operator',
       is_active=true,
       revoked_at=NULL
 WHERE name='${KEY_NAME}'
   AND (
     key_hash <> '${KEY_HASH}'
     OR scope <> 'operator'
     OR is_active <> true
     OR revoked_at IS NOT NULL
   );
SQL
SH
  API_KEY="$raw_key"
  API_KEY_SOURCE="generated:${key_name}"
}

benchmark_ssh_host() {
  printf '%s' "${LAB_BENCHMARK_SSH_HOST:-${LAB_HOSTNAME:-${LAB_COCKPIT_DOMAIN:-}}}"
}

benchmark_ssh_user() {
  printf '%s' "${LAB_BENCHMARK_SSH_USER:-${LAB_USER:-}}"
}

benchmark_ssh_key() {
  printf '%s' "${LAB_BENCHMARK_SSH_KEY:-${ROOT_DIR}/ssh/lab}"
}

mirror_workspace_hint_to_remote() {
  local workspace_root="$1"
  local case_json_path="$2"
  local ssh_host ssh_user ssh_key
  ssh_host="$(benchmark_ssh_host)"
  ssh_user="$(benchmark_ssh_user)"
  ssh_key="$(benchmark_ssh_key)"
  [[ -n "$ssh_host" && -n "$ssh_user" && -f "$ssh_key" ]] || return 0
  ssh -i "$ssh_key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${ssh_user}@${ssh_host}" \
    "mkdir -p '${workspace_root}/.lab' '${workspace_root}/.git'"
  cat "$case_json_path" | ssh -i "$ssh_key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${ssh_user}@${ssh_host}" \
    "cat > '${workspace_root}/.lab/benchmark-case.json'"
}

cleanup_remote_workspace_hint() {
  local workspace_root="$1"
  local ssh_host ssh_user ssh_key
  ssh_host="$(benchmark_ssh_host)"
  ssh_user="$(benchmark_ssh_user)"
  ssh_key="$(benchmark_ssh_key)"
  [[ -n "$ssh_host" && -n "$ssh_user" && -f "$ssh_key" ]] || return 0
  ssh -i "$ssh_key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${ssh_user}@${ssh_host}" \
    "rm -rf '${workspace_root}'" >/dev/null 2>&1 || true
}

select_agentd_cmd() {
  local os arch
  os="$(uname -s)"
  arch="$(uname -m)"
  if [[ "$os" == "Darwin" && "$arch" == "arm64" && -x "${ROOT_DIR}/dist/lab-agentd-darwin-arm64" ]]; then
    AGENTD_CMD=("${ROOT_DIR}/dist/lab-agentd-darwin-arm64")
    return 0
  fi
  if [[ "$os" == "Linux" && ( "$arch" == "x86_64" || "$arch" == "amd64" ) && -x "${ROOT_DIR}/dist/lab-agentd-linux-amd64" ]]; then
    AGENTD_CMD=("${ROOT_DIR}/dist/lab-agentd-linux-amd64")
    return 0
  fi
  if command -v go >/dev/null 2>&1; then
    AGENTD_CMD=(go run ./cmd/lab-agentd)
    return 0
  fi
  return 1
}

json_get() {
  local path="$1"
  local payload
  payload="$(cat)"
  JSON_INPUT="$payload" python3 - "$path" <<'PY'
import json, os, sys
path = [p for p in sys.argv[1].split(".") if p]
data = json.loads(os.environ["JSON_INPUT"])
value = data
for part in path:
    if isinstance(value, list):
        value = value[int(part)]
    elif isinstance(value, dict):
        value = value.get(part, "")
    else:
        value = ""
        break
if isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print("" if value is None else value)
PY
}

initiative_task_id_by_agent() {
  local agent="$1"
  local payload
  payload="$(cat)"
  JSON_INPUT="$payload" python3 - "$agent" <<'PY'
import json, os, sys
agent = sys.argv[1]
items = json.loads(os.environ["JSON_INPUT"]).get("items", [])
for item in items:
    task = item.get("task") or {}
    if (task.get("assigned_agent") or "") == agent:
        print(item.get("task_id", ""))
        raise SystemExit(0)
raise SystemExit(1)
PY
}

approval_id_for_task() {
  local task_id="$1"
  local payload
  payload="$(cat)"
  JSON_INPUT="$payload" python3 - "$task_id" <<'PY'
import json, os, sys
task_id = sys.argv[1]
items = json.loads(os.environ["JSON_INPUT"]).get("items", [])
for item in items:
    if item.get("task_id") == task_id and item.get("status") == "pending":
        print(item.get("id", ""))
        raise SystemExit(0)
raise SystemExit(1)
PY
}

wait_for_task_state() {
  local task_id="$1"
  local expected="$2"
  local started
  started="$(date +%s)"
  while true; do
    local task_json state
    task_json="$(request GET "/tasks/${task_id}")"
    state="$(printf '%s' "$task_json" | json_get state)"
    if [[ "$state" == "$expected" ]]; then
      printf '%s' "$task_json"
      return 0
    fi
    if [[ "$state" == "failed" || "$state" == "cancelled" ]]; then
      printf '%s' "$task_json"
      return 1
    fi
    if (( "$(date +%s)" - started > TIMEOUT_SECONDS )); then
      printf '%s' "$task_json"
      return 2
    fi
    sleep "$POLL_INTERVAL"
  done
}

wait_for_pending_approval() {
  local task_id="$1"
  local started
  started="$(date +%s)"
  while true; do
    local approvals_json
    approvals_json="$(request GET "/approvals")"
    if approval_id="$(printf '%s' "$approvals_json" | approval_id_for_task "$task_id" 2>/dev/null)"; then
      printf '%s\n' "$approval_id"
      return 0
    fi
    if (( "$(date +%s)" - started > TIMEOUT_SECONDS )); then
      return 1
    fi
    sleep "$POLL_INTERVAL"
  done
}

wait_for_initiative_terminal() {
  local initiative_id="$1"
  local started
  started="$(date +%s)"
  while true; do
    local initiative_json status
    initiative_json="$(request GET "/initiatives/${initiative_id}")"
    status="$(printf '%s' "$initiative_json" | json_get initiative.status)"
    if [[ "$status" == "completed" || "$status" == "blocked" || "$status" == "cancelled" ]]; then
      printf '%s' "$initiative_json"
      return 0
    fi
    if (( "$(date +%s)" - started > TIMEOUT_SECONDS )); then
      printf '%s' "$initiative_json"
      return 1
    fi
    sleep "$POLL_INTERVAL"
  done
}

bool_json() {
  local value="${1:-false}"
  if [[ "$value" == "1" || "$value" == "true" || "$value" == "True" ]]; then
    printf 'true'
  else
    printf 'false'
  fi
}

render_case_for_workspace() {
  local case_path="$1"
  local output_path="$2"
  local workspace_root="$3"
  local repo_url="$4"
  local repo_branch="$5"
  local repo_profile="$6"
  local memory_mode="$7"
  local strategy="$8"
  python3 - "$case_path" "$output_path" "$workspace_root" "$repo_url" "$repo_branch" "$repo_profile" "$memory_mode" "$strategy" <<'PY'
import json, pathlib, sys

case_path, output_path, workspace_root, repo_url, repo_branch, repo_profile, memory_mode, strategy = sys.argv[1:9]
case = json.loads(pathlib.Path(case_path).read_text())

replacements = {
    "{{ project_root_abs }}": workspace_root,
    "{{ workspace_root }}": workspace_root,
}

def render(value):
    if isinstance(value, str):
        for source, target in replacements.items():
            value = value.replace(source, target)
        return value
    if isinstance(value, list):
        return [render(item) for item in value]
    if isinstance(value, dict):
        return {key: render(item) for key, item in value.items()}
    return value

case = render(case)
case["repo_url"] = repo_url
case["default_branch"] = repo_branch
case["repo_profile"] = repo_profile
case["benchmark_memory_mode"] = memory_mode
case["benchmark_memory_strategy"] = strategy

pathlib.Path(output_path).write_text(json.dumps(case, indent=2) + "\n")
PY
}

verify_remote_workspace_hint() {
  local workspace_root="$1"
  local ssh_host ssh_user ssh_key
  ssh_host="$(benchmark_ssh_host)"
  ssh_user="$(benchmark_ssh_user)"
  ssh_key="$(benchmark_ssh_key)"
  [[ -n "$ssh_host" && -n "$ssh_user" && -f "$ssh_key" ]] || return 0
  ssh -i "$ssh_key" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${ssh_user}@${ssh_host}" \
    "test -d '${workspace_root}/.git' && test -f '${workspace_root}/.lab/benchmark-case.json' && sudo -u orchestrator test -r '${workspace_root}/.lab/benchmark-case.json'"
}

validate_materialized_plan() {
  local tasks_json="$1"
  local case_json_path="$2"
  local run_dir="$3"
  JSON_INPUT="$tasks_json" python3 - "$case_json_path" "$run_dir" <<'PY'
import json, pathlib, sys

tasks = json.loads(__import__("os").environ["JSON_INPUT"]).get("items", [])
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
run_dir = pathlib.Path(sys.argv[2])

required_agents = {"researcher", "coder", "reviewer"}
seen_agents = {}
for item in tasks:
    task = item.get("task") or {}
    agent = task.get("assigned_agent") or ""
    if agent and agent not in seen_agents:
        seen_agents[agent] = task

missing = sorted(required_agents - set(seen_agents))
if missing:
    raise SystemExit(f"missing benchmark tasks for agents: {', '.join(missing)}")

coder = seen_agents["coder"]
coder_meta = coder.get("metadata") or {}
project_request = coder_meta.get("project_request") or {}
tool_request = coder_meta.get("tool_request") or {}

problems = []
if coder_meta.get("repo_workflow") != "repo_workflow_v1":
    problems.append("coder task is not tagged as repo_workflow_v1")
if (coder_meta.get("benchmark_case_id") or "") != case.get("id", ""):
    problems.append("benchmark_case_id missing or mismatched")
if (project_request.get("repository_url") or "") != case.get("repo_url", ""):
    problems.append("repository_url missing or mismatched")
if (project_request.get("repo_profile") or "") != case.get("repo_profile", ""):
    problems.append("repo_profile missing or mismatched")
if (tool_request.get("tool") or "") != case.get("coder_tool", ""):
    problems.append("coder tool_request does not match case")
if case.get("patch") and (tool_request.get("patch") or "") != case.get("patch", ""):
    problems.append("coder patch does not match case")
if case.get("patch_target") and (tool_request.get("path") or "") != case.get("patch_target", ""):
    problems.append("coder patch target does not match case")
if project_request.get("repo_profile") == "existing_repo_generic" and case.get("repo_profile") != "existing_repo_generic":
    problems.append("materialized plan fell back to existing_repo_generic")

details = {
    "case_id": case.get("id"),
    "coder_task_id": coder.get("id"),
    "coder_repo_profile": project_request.get("repo_profile"),
    "coder_repository_url": project_request.get("repository_url"),
    "coder_tool": tool_request.get("tool"),
}
run_dir.joinpath("materialized_plan_check.json").write_text(json.dumps(details, indent=2) + "\n")

if problems:
    raise SystemExit("; ".join(problems))
PY
}

build_summary_reports() {
  local report_root="$1"
  python3 - "$report_root" <<'PY'
import json, pathlib, sys

root = pathlib.Path(sys.argv[1])
runs = []
for path in sorted(root.glob("*/benchmark_run.json")):
    runs.append(json.loads(path.read_text()))

summary_path = root / "summary.json"
summary_path.write_text(json.dumps(runs, indent=2) + "\n")

by_repo = {}
for row in runs:
    repo = row.get("repo_id") or row.get("case_id") or "unknown"
    by_repo.setdefault(repo, {})[row.get("mode")] = row

lines = [
    "# Benchmark Summary",
    "",
    "| Repo | Mode Off | Hits Off | Mode On | Hits On | Delta | Commit |",
    "|---|---:|---:|---:|---:|---:|---|",
]
for repo, modes in sorted(by_repo.items()):
    off = modes.get("memory_off", {})
    on = modes.get("memory_on", {})
    off_score = int(off.get("score", 0) or 0)
    on_score = int(on.get("score", 0) or 0)
    off_hits = len(off.get("memory_hits") or [])
    on_hits = len(on.get("memory_hits") or [])
    delta = on_score - off_score
    commit = (off.get("resolved_commit") or on.get("resolved_commit") or "")[:12]
    lines.append(f"| {repo} | {off_score} | {off_hits} | {on_score} | {on_hits} | {delta:+d} | {commit} |")

root.joinpath("summary.md").write_text("\n".join(lines) + "\n")
PY
}

config_json() {
  python3 - "$CONFIG_PATH" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
if not path.exists():
    raise SystemExit(f"benchmark config not found: {path}. Create it from benchmarks/config.default.json")
print(json.dumps(json.loads(path.read_text())))
PY
}

config_get() {
  local key="$1"
  python3 - "$key" "$CONFIG_PATH" <<'PY'
import json, pathlib, sys
key = sys.argv[1]
data = json.loads(pathlib.Path(sys.argv[2]).read_text())
value = data.get(key, "")
if isinstance(value, (dict, list, bool)):
    print(json.dumps(value))
else:
    print(value)
PY
}

load_repos_json() {
  local config_repos_file
  config_repos_file="$(config_get repos_file)"
  if [[ -n "$config_repos_file" ]]; then
    cat "${ROOT_DIR}/${config_repos_file}"
    return
  fi
  cat "$REPO_CATALOG_PATH"
}

load_case_paths() {
  python3 - "$CONFIG_PATH" "$CASE_DIR" <<'PY'
import json, pathlib, sys
config = json.loads(pathlib.Path(sys.argv[1]).read_text())
case_dir = pathlib.Path(sys.argv[2])
case_ids = config.get("case_ids") or []
if case_ids:
    for case_id in case_ids:
        print(case_dir / f"{case_id}.json")
else:
    for path in sorted(case_dir.glob("*.json")):
        print(path)
PY
}

write_run_report() {
  local run_dir="$1"
  local report_json="$2"
  printf '%s\n' "$report_json" > "${run_dir}/benchmark_run.json"
  python3 - "${run_dir}/benchmark_run.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
lines = [
    f"# Benchmark Run: {data.get('case_id', 'unknown')}",
    "",
    f"- Mode: {data.get('mode')}",
    f"- Repository: {data.get('repo_url')}",
    f"- Branch: {data.get('branch')}",
    f"- Commit: {data.get('resolved_commit')}",
    f"- Outcome: {data.get('status')}",
    f"- Initiative: {data.get('initiative_id', '')}",
    f"- Score: {data.get('score', 0)}",
    "",
    "## Memory Hits",
]
hits = data.get("memory_hits", [])
if not hits:
    lines.append("- none")
else:
    for hit in hits:
        lines.append(f"- {hit.get('source_ref', 'unknown')} [{hit.get('match_type', 'unknown')}]")
path.with_suffix(".md").write_text("\n".join(lines) + "\n")
PY
}

CONFIG_JSON="$(config_json)"
MODES="$(python3 - <<PY
import json
config = json.loads("""$CONFIG_JSON""")
print(",".join(config.get("modes") or ["memory_off", "memory_on"]))
PY
)"
RUNS_PER_CASE="$(python3 - <<PY
import json
config = json.loads("""$CONFIG_JSON""")
print(config.get("runs_per_case", 1))
PY
)"
MEMORY_STRATEGY_DEFAULT="$(python3 - <<PY
import json
config = json.loads("""$CONFIG_JSON""")
print(config.get("default_memory_strategy", "repo_specific_first"))
PY
)"
SOFT_FAIL_EXTERNAL="$(python3 - <<PY
import json
config = json.loads("""$CONFIG_JSON""")
print("1" if config.get("external_soft_fail", True) else "0")
PY
)"
REPOS_JSON="$(load_repos_json)"
mapfile -t CASE_PATHS < <(load_case_paths)
if [[ ${#CASE_PATHS[@]} -eq 0 ]]; then
  echo "No benchmark cases found" >&2
  exit 1
fi
if ! select_base_url; then
  echo "Unable to detect a healthy orchestrator base URL from current environment" >&2
  exit 2
fi
if ! select_api_key; then
  remote_upsert_benchmark_api_key || {
    echo "Unable to validate or generate an operator API key for ${BASE_URL}" >&2
    exit 2
  }
  if ! validate_api_key_for_base_url "$BASE_URL" "$API_KEY"; then
    echo "Generated benchmark API key did not validate against ${BASE_URL}" >&2
    exit 2
  fi
fi
declare -a AGENTD_CMD=()
if ! select_agentd_cmd; then
  echo "No usable lab-agentd command found. Build dist/lab-agentd or install Go." >&2
  exit 2
fi
log "selected_base_url=${BASE_URL}"
log "selected_api_key_source=${API_KEY_SOURCE}"
request GET "/ready" >/dev/null

run_case() {
  local mode="$1"
  local case_path="$2"
  local iteration="$3"
  local run_slug
  run_slug="$(basename "${case_path%.json}")-${mode}-run${iteration}"
  local run_dir="${REPORT_ROOT}/${run_slug}"
  mkdir -p "$run_dir"

  if [[ "$mode" == "codex_baseline" ]]; then
    write_run_report "$run_dir" "$(python3 - "$case_path" <<'PY'
import json, pathlib, sys
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(json.dumps({
  "case_id": case["id"],
  "mode": "codex_baseline",
  "status": "skipped",
  "score": 0,
  "reason": "codex_baseline is not automated by this harness"
}, indent=2))
PY
)"
    log "skipped codex_baseline for $(basename "$case_path")"
    return 0
  fi

  local memory_mode="on"
  if [[ "$mode" == "memory_off" ]]; then
    memory_mode="off"
  fi

  local workspace_root case_id repo_id repo_url repo_branch repo_profile case_goal strategy
  local run_meta
  run_meta="$(python3 - "$case_path" <<PY
import json, sys
case = json.load(open(sys.argv[1]))
repos = json.loads("""$REPOS_JSON""")
repo = None
repo_id = case.get("repo_id")
repo_profile = case.get("repo_profile")
for item in repos:
    if repo_id and item.get("id") == repo_id:
        repo = item
        break
if repo is None and repo_profile:
    for item in repos:
        if item.get("repo_profile") == repo_profile:
            repo = item
            break
if repo is None:
    raise SystemExit("no matching repo found for case")
print(json.dumps({
    "case_id": case["id"],
    "repo_id": case.get("repo_id") or repo.get("id") or "",
    "repo_url": repo["repo_url"],
    "default_branch": repo["default_branch"],
    "repo_profile": case.get("repo_profile") or repo.get("repo_profile"),
    "goal": case["goal"],
    "strategy": case.get("benchmark_memory_strategy") or "$MEMORY_STRATEGY_DEFAULT"
}))
PY
)"
  case_id="$(printf '%s' "$run_meta" | json_get case_id)"
  repo_id="$(printf '%s' "$run_meta" | json_get repo_id)"
  repo_url="$(printf '%s' "$run_meta" | json_get repo_url)"
  repo_branch="$(printf '%s' "$run_meta" | json_get default_branch)"
  repo_profile="$(printf '%s' "$run_meta" | json_get repo_profile)"
  case_goal="$(printf '%s' "$run_meta" | json_get goal)"
  strategy="$(printf '%s' "$run_meta" | json_get strategy)"

  local scratch_root bridge_log bridge_id bridge_name bridge_pid resolved_head resolved_commit
  scratch_root="$(mktemp -d "${WORKDIR_ROOT%/}/lab-benchmark-${run_slug}-XXXXXX")"
  workspace_root="${scratch_root}/repo"
  bridge_log="${run_dir}/lab-agentd.log"
  bridge_id="$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
  bridge_name="benchmark-${case_id}"
  bridge_pid=""

  cleanup_run() {
    if [[ -n "$bridge_pid" ]] && kill -0 "$bridge_pid" >/dev/null 2>&1; then
      kill "$bridge_pid" >/dev/null 2>&1 || true
      wait "$bridge_pid" >/dev/null 2>&1 || true
    fi
    cleanup_remote_workspace_hint "$workspace_root"
    rm -rf "$scratch_root"
  }
  trap cleanup_run RETURN

  resolved_head="$(git ls-remote --heads "$repo_url" "$repo_branch" | awk '{print $1}')"
  if [[ -z "$resolved_head" ]]; then
    local status="soft_fail_external"
    if [[ "$SOFT_FAIL_EXTERNAL" == "0" ]]; then
      status="failed"
    fi
    write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "status": "$status",
  "score": 0,
  "reason": "failed to resolve upstream branch"
}, indent=2))
PY
)"
    log "soft-failed external branch resolution for ${case_id}"
    return 0
  fi

  if ! git clone --depth 1 --branch "$repo_branch" "$repo_url" "$workspace_root" >/dev/null 2>&1; then
    write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "status": "soft_fail_external",
  "score": 0,
  "reason": "failed to clone upstream repository"
}, indent=2))
PY
)"
    log "soft-failed external clone for ${case_id}"
    return 0
  fi
  resolved_commit="$(git -C "$workspace_root" rev-parse HEAD)"

  mkdir -p "${workspace_root}/.lab"
  render_case_for_workspace "$case_path" "$workspace_root/.lab/benchmark-case.json" "$workspace_root" "$repo_url" "$repo_branch" "$repo_profile" "$memory_mode" "$strategy"
  mirror_workspace_hint_to_remote "$workspace_root" "$workspace_root/.lab/benchmark-case.json"
  if ! verify_remote_workspace_hint "$workspace_root"; then
    write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_id": "$repo_id",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "resolved_commit": "$resolved_commit",
  "status": "failed",
  "score": 0,
  "reason": "remote workspace shadow is missing or unreadable by orchestrator user"
}, indent=2))
PY
)"
    return 1
  fi

  if [[ "$(python3 - "$workspace_root/.lab/benchmark-case.json" <<'PY'
import json, sys
case = json.load(open(sys.argv[1]))
print("yes" if case.get("patch") else "no")
PY
)" == "yes" ]]; then
    if ! python3 - "$workspace_root/.lab/benchmark-case.json" <<'PY'
import json, pathlib, subprocess, sys
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
patch = case.get("patch", "")
if not patch:
    raise SystemExit(0)
proc = subprocess.run(["git", "apply", "--check", "-"], cwd=str(pathlib.Path(sys.argv[1]).parent.parent), input=patch.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
raise SystemExit(proc.returncode)
PY
    then
      write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "resolved_commit": "$resolved_commit",
  "status": "soft_fail_external",
  "score": 0,
  "reason": "deterministic patch no longer applies cleanly to upstream"
}, indent=2))
PY
)"
      log "soft-failed patch check for ${case_id}"
      return 0
    fi
  fi

  (
    cd "$ROOT_DIR"
    exec "${AGENTD_CMD[@]}" \
      --base-url "$BASE_URL" \
      --api-key "$API_KEY" \
      --bridge-id "$bridge_id" \
      --workspace-root "$workspace_root" \
      --name "$bridge_name" \
      --poll-interval "$BRIDGE_POLL_INTERVAL" \
      --heartbeat-interval "$BRIDGE_HEARTBEAT_INTERVAL"
  ) >"$bridge_log" 2>&1 &
  bridge_pid="$!"
  sleep 3
  if ! kill -0 "$bridge_pid" >/dev/null 2>&1; then
    write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "resolved_commit": "$resolved_commit",
  "status": "failed",
  "score": 0,
  "reason": "lab-agentd failed to start"
}, indent=2))
PY
)"
    return 1
  fi

  local initiative_payload initiative_json initiative_id tasks_json researcher_task_id coder_task_id reviewer_task_id approval_id
  initiative_payload="$(python3 - "$workspace_root/.lab/benchmark-case.json" "$case_goal" <<PY
import json
import pathlib
import sys
case_payload = pathlib.Path(sys.argv[1]).read_text()
goal = sys.argv[2] + "\n\n[BENCHMARK_CASE_JSON]\n" + case_payload + "\n[/BENCHMARK_CASE_JSON]"
print(json.dumps({
    "title": "Benchmark ${case_id}",
    "goal": goal,
    "workspace_root": "${workspace_root}",
    "created_by": "${OPERATOR}",
    "execution_mode": "selective"
}))
PY
)"
  initiative_json="$(request POST "/initiatives/" "$initiative_payload")"
  initiative_id="$(printf '%s' "$initiative_json" | json_get id)"
  request POST "/initiatives/${initiative_id}/advance" "{}" >/dev/null
  request POST "/initiatives/${initiative_id}/approve/requirements" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"benchmark\"}" >/dev/null
  request POST "/initiatives/${initiative_id}/advance" "{}" >/dev/null
  request POST "/initiatives/${initiative_id}/approve/design" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"benchmark\"}" >/dev/null
  request POST "/initiatives/${initiative_id}/tasks/generate" "{}" >/dev/null

  tasks_json="$(request GET "/initiatives/${initiative_id}/tasks")"
  printf '%s\n' "$tasks_json" > "${run_dir}/initiative_tasks.json"
  if ! validate_materialized_plan "$tasks_json" "$workspace_root/.lab/benchmark-case.json" "$run_dir"; then
    write_run_report "$run_dir" "$(python3 - <<PY
import json
print(json.dumps({
  "case_id": "$case_id",
  "mode": "$mode",
  "repo_url": "$repo_url",
  "branch": "$repo_branch",
  "resolved_commit": "$resolved_commit",
  "initiative_id": "$initiative_id",
  "status": "failed",
  "score": 0,
  "reason": "materialized plan did not match benchmark case expectations"
}, indent=2))
PY
)"
    return 1
  fi
  request POST "/initiatives/${initiative_id}/approve/plan" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"benchmark\"}" >/dev/null
  researcher_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent researcher)"
  coder_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent coder)"
  reviewer_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent reviewer)"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${researcher_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  local researcher_json
  researcher_json="$(wait_for_task_state "$researcher_task_id" "completed" || request GET "/tasks/${researcher_task_id}")"
  printf '%s\n' "$researcher_json" > "${run_dir}/researcher_task.json"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${coder_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  approval_id="$(wait_for_pending_approval "$coder_task_id")"
  request POST "/approvals/${approval_id}/approve" "{\"operator\":\"${OPERATOR}\"}" >/dev/null
  local coder_json reviewer_json initiative_final initiative_status artifacts_json reviewer_task_json
  coder_json="$(wait_for_task_state "$coder_task_id" "completed" || request GET "/tasks/${coder_task_id}")"
  printf '%s\n' "$coder_json" > "${run_dir}/coder_task.json"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${reviewer_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  reviewer_json="$(wait_for_task_state "$reviewer_task_id" "completed" || request GET "/tasks/${reviewer_task_id}")"
  reviewer_task_json="$(request GET "/tasks/${reviewer_task_id}")"
  initiative_final="$(wait_for_initiative_terminal "$initiative_id" || request GET "/initiatives/${initiative_id}")"
  initiative_status="$(printf '%s' "$initiative_final" | json_get initiative.status)"
  artifacts_json="$(request GET "/initiatives/${initiative_id}/artifacts")"

  printf '%s\n' "$artifacts_json" > "${run_dir}/initiative_artifacts.json"
  printf '%s\n' "$initiative_final" > "${run_dir}/initiative_detail.json"
  printf '%s\n' "$reviewer_task_json" > "${run_dir}/reviewer_task.json"
  git -C "$workspace_root" diff --no-ext-diff > "${run_dir}/git.diff" || true
  tail -n 80 "$bridge_log" > "${run_dir}/bridge.log.tail" || true

  local report_json
  report_json="$(python3 - "${run_dir}/reviewer_task.json" "${run_dir}/initiative_artifacts.json" <<PY
import json, pathlib, sys
reviewer = json.loads(pathlib.Path(sys.argv[1]).read_text())
artifacts = json.loads(pathlib.Path(sys.argv[2]).read_text()).get("items", [])
hits = []
ctx = (reviewer.get("metadata") or {}).get("context_package") or {}
for chunk in ctx.get("chunks", []):
    meta = chunk.get("metadata") or {}
    hits.append({
        "source_ref": chunk.get("source_ref"),
        "match_type": meta.get("memory_match_type", "unknown"),
        "repo_profile": meta.get("repo_profile"),
    })
status = "success" if "${initiative_status}" == "completed" and reviewer.get("state") == "completed" else "failed"
results = reviewer.get("results") or {}
reviewer_ok = reviewer.get("state") == "completed" and results.get("status") == "success" and results.get("exit_code") == 0
if not reviewer_ok:
    status = "failed"
score = 0
if status == "success":
    score += 40
test_status = ((results.get("test_results") or {}).get("status") or "")
if reviewer_ok:
    score += 20
if pathlib.Path("${run_dir}/git.diff").read_text().strip():
    score += 15
if hits:
    score += 10
negative = sum(1 for hit in hits if hit.get("match_type") == "semantic_related")
if negative == 0:
    score += 5
print(json.dumps({
    "case_id": "$case_id",
    "mode": "$mode",
    "memory_mode": "$memory_mode",
    "memory_strategy": "$strategy",
    "repo_id": "$repo_id",
    "repo_url": "$repo_url",
    "branch": "$repo_branch",
    "resolved_commit": "$resolved_commit",
    "status": status,
    "initiative_id": "$initiative_id",
    "researcher_task_id": "$researcher_task_id",
    "coder_task_id": "$coder_task_id",
    "reviewer_task_id": "$reviewer_task_id",
    "memory_hits": hits[:5],
    "artifact_count": len(artifacts),
    "score": score
}, indent=2))
PY
)"
  write_run_report "$run_dir" "$report_json"
  log "completed ${case_id} ${mode} run ${iteration}"
}

mode_list="${MODES//,/ }"
for case_path in "${CASE_PATHS[@]}"; do
  for mode in $mode_list; do
    for ((i=1; i<=RUNS_PER_CASE; i++)); do
      run_case "$mode" "$case_path" "$i"
    done
  done
done

build_summary_reports "$REPORT_ROOT"
log "benchmark reports written to ${REPORT_ROOT}"
