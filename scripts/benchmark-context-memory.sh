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
GENERATED_CASE_DIR="${REPORT_ROOT}/generated-cases"

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
  append_unique_line "LAB_ORCHESTRATOR_BENCHMARK_API_KEY:${LAB_ORCHESTRATOR_BENCHMARK_API_KEY:-}" "$tmp"
  append_unique_line "LAB_BENCHMARK_API_KEY:${LAB_BENCHMARK_API_KEY:-}" "$tmp"
  append_unique_line "LAB_AGENT_API_KEY:${LAB_AGENT_API_KEY:-}" "$tmp"
  append_unique_line "LAB_ORCHESTRATOR_CLEANUP_API_KEY:${LAB_ORCHESTRATOR_CLEANUP_API_KEY:-}" "$tmp"
  append_unique_line "LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:${LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:-}" "$tmp"
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
  local key_name="${LAB_ORCHESTRATOR_BENCHMARK_KEY_NAME:-${LAB_BENCHMARK_API_KEY_NAME:-benchmark-operator}}"
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

enrich_task_json_for_report() {
  local payload="$1"
  printf '%s' "$payload" | python3 -c '
import json, sys

task = json.load(sys.stdin)
metadata = task.get("metadata") or {}
context = metadata.get("context_package") or {}
results = task.get("results") or {}

if "semantic_context_sources" not in results:
    refs = context.get("source_refs") or []
    if refs:
        results["semantic_context_sources"] = refs

if "semantic_context_chunk_count" not in results:
    chunks = context.get("chunks") or []
    if chunks:
        results["semantic_context_chunk_count"] = len(chunks)

if "semantic_context_hits" not in results:
    hits = []
    for chunk in context.get("chunks") or []:
        meta = chunk.get("metadata") or {}
        hit = {}
        if chunk.get("source_ref"):
            hit["source_ref"] = chunk.get("source_ref")
        if meta.get("memory_match_type"):
            hit["match_type"] = meta.get("memory_match_type")
        if meta.get("repo_profile"):
            hit["repo_profile"] = meta.get("repo_profile")
        if meta.get("repository_url") or meta.get("repo_url"):
            hit["repository_url"] = meta.get("repository_url") or meta.get("repo_url")
        if meta.get("benchmark_case_id"):
            hit["benchmark_case_id"] = meta.get("benchmark_case_id")
        if hit:
            hits.append(hit)
    if hits:
        results["semantic_context_hits"] = hits

task["results"] = results
print(json.dumps(task, indent=2))
'
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
  python3 -c '
import json, sys
path = [p for p in sys.argv[1].split(".") if p]
raw = sys.stdin.read()
if not raw.strip():
    print("")
    raise SystemExit(0)
data = json.loads(raw)
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
' "$path"
}

initiative_task_id_by_agent() {
  local agent="$1"
  python3 -c '
import json, sys
agent = sys.argv[1]
raw = sys.stdin.read()
if not raw.strip():
    raise SystemExit(1)
items = json.loads(raw).get("items", [])
for item in items:
    task = item.get("task") or {}
    if (task.get("assigned_agent") or "") == agent:
        print(item.get("task_id", ""))
        raise SystemExit(0)
raise SystemExit(1)
' "$agent"
}

approval_id_for_task() {
  local task_id="$1"
  python3 -c '
import json, sys
task_id = sys.argv[1]
raw = sys.stdin.read()
if not raw.strip():
    raise SystemExit(1)
items = json.loads(raw).get("items", [])
for item in items:
    if item.get("task_id") == task_id and item.get("status") == "pending":
        print(item.get("id", ""))
        raise SystemExit(0)
raise SystemExit(1)
' "$task_id"
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
    if [[ "$state" == "failed" || "$state" == "cancelled" || "$state" == "error" ]]; then
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
if (coder_meta.get("benchmark_league") or "") != case.get("benchmark_league", ""):
    problems.append("benchmark_league missing or mismatched")
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
    "benchmark_league": coder_meta.get("benchmark_league"),
}
run_dir.joinpath("materialized_plan_check.json").write_text(json.dumps(details, indent=2) + "\n")

if problems:
    raise SystemExit("; ".join(problems))
PY
}

build_planner_task_report() {
  local tasks_json="$1"
  local case_json_path="$2"
  local initiative_id="$3"
  JSON_INPUT="$tasks_json" python3 - "$case_json_path" "$initiative_id" <<'PY'
import json, os, pathlib, sys

tasks = json.loads(os.environ["JSON_INPUT"]).get("items", [])
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
initiative_id = sys.argv[2]
success_contract = case.get("success_contract") or {}
expected_agents = success_contract.get("required_agents") or ["researcher", "coder", "reviewer"]
expected_launch_order = success_contract.get("expected_launch_order") or expected_agents
expected_coder_tool = success_contract.get("expected_coder_tool") or ""
requires_coder_approval = bool(success_contract.get("requires_coder_approval"))

sorted_items = sorted(tasks, key=lambda item: int(item.get("launch_order") or 0))
launch_order = [(item.get("task") or {}).get("assigned_agent") or "" for item in sorted_items]
assigned_agents = [agent for agent in launch_order if agent]
seen_agents = {}
for item in tasks:
    task = item.get("task") or {}
    agent = task.get("assigned_agent") or ""
    if agent and agent not in seen_agents:
        seen_agents[agent] = task

missing_agents = sorted(set(expected_agents) - set(seen_agents))
coder = seen_agents.get("coder") or {}
coder_meta = coder.get("metadata") or {}
coder_tool = ((coder_meta.get("tool_request") or {}).get("tool") or "")
coder_requires_approval = bool(coder_meta.get("requires_approval"))
fallback_detected = ((coder_meta.get("project_request") or {}).get("repo_profile") or "") == "existing_repo_generic"
launch_prefix_ok = launch_order[:len(expected_launch_order)] == expected_launch_order
materialized_plan_valid = (not missing_agents) and launch_prefix_ok and (not expected_coder_tool or coder_tool == expected_coder_tool)
plan_executable = materialized_plan_valid and (coder_requires_approval == requires_coder_approval) and not fallback_detected
state = "completed" if plan_executable else "failed"

print(json.dumps({
    "id": f"planner:{initiative_id}",
    "state": state,
    "assigned_agent": "planner",
    "initiative_id": initiative_id,
    "results": {
        "materialized_plan_valid": materialized_plan_valid,
        "plan_executable": plan_executable,
        "fallback_detected": fallback_detected,
        "assigned_agents": assigned_agents,
        "launch_order": launch_order,
        "requires_approval_agents": [agent for agent, task in seen_agents.items() if bool((task.get("metadata") or {}).get("requires_approval"))],
        "missing_agents": missing_agents,
        "coder_tool": coder_tool,
        "coder_requires_approval": coder_requires_approval,
        "expected_agents": expected_agents,
        "expected_launch_order": expected_launch_order
    }
}, indent=2))
PY
}

build_summary_reports() {
  local report_root="$1"
  python3 - "$report_root" <<'PY'
import json, pathlib, sys
import math

root = pathlib.Path(sys.argv[1])
runs = []
for path in sorted(root.glob("*/benchmark_run.json")):
    row = json.loads(path.read_text())
    row["_path"] = path
    runs.append(row)

def sort_key(row):
    return (
        row.get("benchmark_league") or "",
        row.get("sequence_id") or "",
        int(row.get("sequence_position", 1) or 1),
        row.get("repo_id") or row.get("case_id") or "",
        row.get("mode") or "",
        int(row.get("iteration", 1) or 1),
    )

runs.sort(key=sort_key)

baseline_scores = {}
reference_scores = {}
for row in runs:
    case_key = (row.get("case_id") or "unknown", int(row.get("iteration", 1) or 1))
    if (row.get("mode") or "") == "memory_off":
        baseline_scores[case_key] = float(row.get("score", 0) or 0)
    if (row.get("mode") or "") == "reference_external":
        reference_scores[case_key] = float(row.get("score", 0) or 0)

first_sequence_scores = {}
previous_sequence_scores = {}
for row in runs:
    league = row.get("benchmark_league") or "repo_recall"
    case_id = row.get("case_id") or "unknown"
    mode = row.get("mode") or "unknown"
    iteration = int(row.get("iteration", 1) or 1)
    sequence_id = row.get("sequence_id") or case_id
    sequence_position = int(row.get("sequence_position", 1) or 1)
    base_score = int(row.get("score", 0) or 0)
    score = base_score

    baseline_key = (case_id, iteration)
    baseline_score = baseline_scores.get(baseline_key)
    reference_score = reference_scores.get(baseline_key)
    row["baseline_score"] = baseline_score
    row["reference_score"] = reference_score
    row["delta_vs_memory_off"] = 0 if baseline_score is None or mode == "memory_off" else round(base_score - baseline_score, 1)
    row["delta_vs_reference_external"] = 0 if reference_score is None or mode == "reference_external" else round(base_score - reference_score, 1)
    if mode == "memory_off":
        score_delta_vs_first_run = 0
        score_delta_vs_previous_run = 0
    else:
        score_delta_vs_first_run = base_score - baseline_score if baseline_score is not None else 0
        score_delta_vs_previous_run = 0
        if mode == "memory_on" and baseline_score is not None and base_score > baseline_score:
            score += 10
        if mode == "memory_on" and reference_score is not None:
            distance = abs(base_score - reference_score)
            if distance <= 5:
                score += 5
            elif distance <= 10:
                score += 3
            elif distance <= 20:
                score += 1

    seq_key = (sequence_id, mode, iteration)
    if seq_key not in first_sequence_scores or sequence_position < first_sequence_scores[seq_key][0]:
        first_sequence_scores[seq_key] = (sequence_position, base_score)
    first_pos, first_score = first_sequence_scores[seq_key]
    prev_key = (sequence_id, mode, iteration)
    prev_score = previous_sequence_scores.get(prev_key)
    if prev_score is None:
        score_delta_vs_previous_run = 0
    else:
        score_delta_vs_previous_run = base_score - prev_score
    previous_sequence_scores[prev_key] = base_score
    score_delta_vs_first_run = base_score - first_score

    if mode == "memory_on" and league in {"technology_transfer", "pattern_transfer"} and sequence_position > first_pos and base_score >= first_score:
        score += 10

    row["score"] = score
    row["score_delta_vs_first_run"] = score_delta_vs_first_run
    row["score_delta_vs_previous_run"] = score_delta_vs_previous_run
    row["retrieval_precision_label"] = row.get("retrieval_precision_label") or row.get("memory_effect") or "-"
    row["_path"].write_text(json.dumps({k: v for k, v in row.items() if k != "_path"}, indent=2) + "\n")

aggregates = {}
league_aggregates = {}
sequence_aggregates = {}
progression = {}

for row in runs:
    repo = row.get("repo_id") or row.get("case_id") or "unknown"
    mode = row.get("mode") or "unknown"
    league = row.get("benchmark_league") or "repo_recall"
    sequence_id = row.get("sequence_id") or row.get("case_id") or "unknown"
    iteration = int(row.get("iteration", 1) or 1)

    def update_slot(slot):
        slot["run_count"] += 1
        if row.get("status") == "success":
            slot["success_count"] += 1
        slot["score_total"] += int(row.get("score", 0) or 0)
        slot["hit_total"] += len(row.get("memory_hits") or [])
        slot["experience_source_total"] += int(row.get("experience_source_count", 0) or 0)
        slot["repo_specific_hit_total"] += int(row.get("repo_specific_hit_count", 0) or 0)
        slot["technology_hit_total"] += int(row.get("technology_hit_count", 0) or 0)
        slot["pattern_hit_total"] += int(row.get("pattern_hit_count", 0) or 0)
        slot["forbidden_total"] += int(row.get("forbidden_hit_count", 0) or 0)
        slot["score_delta_vs_first_total"] += int(row.get("score_delta_vs_first_run", 0) or 0)
        slot["score_delta_vs_previous_total"] += int(row.get("score_delta_vs_previous_run", 0) or 0)
        slot["agent_score_total"] += float(row.get("agent_score", 0) or 0)
        slot["handoff_score_total"] += float(row.get("handoff_score", 0) or 0)
        slot["system_score_total"] += float(row.get("system_score", 0) or 0)
        slot["reference_score_total"] += float(row.get("reference_score", 0) or 0)
        slot["delta_vs_reference_total"] += float(row.get("delta_vs_reference_external", 0) or 0)
        effect = row.get("retrieval_precision_label")
        if effect:
            slot["effects"][effect] = slot["effects"].get(effect, 0) + 1
        if not slot.get("resolved_commit"):
            slot["resolved_commit"] = row.get("resolved_commit") or ""
        if not slot.get("maturity_block"):
            slot["maturity_block"] = row.get("maturity_block") or ""

    default_slot = lambda: {
        "run_count": 0,
        "success_count": 0,
        "score_total": 0,
        "hit_total": 0,
        "experience_source_total": 0,
        "repo_specific_hit_total": 0,
        "technology_hit_total": 0,
        "pattern_hit_total": 0,
        "forbidden_total": 0,
        "score_delta_vs_first_total": 0,
        "score_delta_vs_previous_total": 0,
        "agent_score_total": 0.0,
        "handoff_score_total": 0.0,
        "system_score_total": 0.0,
        "reference_score_total": 0.0,
        "delta_vs_reference_total": 0.0,
        "effects": {},
        "resolved_commit": "",
        "maturity_block": "",
    }

    repo_slot = aggregates.setdefault(repo, {}).setdefault(mode, default_slot())
    update_slot(repo_slot)

    league_slot = league_aggregates.setdefault(league, {}).setdefault(mode, default_slot())
    update_slot(league_slot)

    seq_slot = sequence_aggregates.setdefault(sequence_id, {}).setdefault(mode, default_slot())
    seq_slot.setdefault("benchmark_league", league)
    update_slot(seq_slot)

    prog_slot = progression.setdefault(sequence_id, {}).setdefault(str(iteration), {}).setdefault(mode, default_slot())
    prog_slot.setdefault("benchmark_league", league)
    update_slot(prog_slot)

def score_series(sequence_id, mode):
    values = []
    for row in runs:
        if (row.get("sequence_id") or row.get("case_id") or "unknown") != sequence_id:
            continue
        if (row.get("mode") or "unknown") != mode:
            continue
        values.append(float(row.get("score", 0) or 0))
    return values

def calc_stddev(values):
    if len(values) <= 1:
        return 0.0
    mean = sum(values) / len(values)
    variance = sum((value - mean) ** 2 for value in values) / len(values)
    return math.sqrt(variance)

def progression_label(values):
    if not values:
        return "unknown"
    if len(values) == 1:
        return "single_run"
    diffs = [values[i] - values[i - 1] for i in range(1, len(values))]
    if all(diff == 0 for diff in diffs):
        return "flat"
    if all(diff > 0 for diff in diffs):
        return "improving"
    if all(diff >= 0 for diff in diffs):
        return "non_decreasing"
    if all(diff <= 0 for diff in diffs):
        return "degrading"
    return "mixed"

def stability_label(stddev):
    if stddev <= 1:
        return "stable"
    if stddev <= 4:
        return "moderate"
    return "volatile"

summary = {
    "runs": [{k: v for k, v in row.items() if k != "_path"} for row in runs],
    "aggregates": aggregates,
    "league_aggregates": league_aggregates,
    "sequence_aggregates": sequence_aggregates,
}
for sequence_id, modes in sequence_aggregates.items():
    for mode_name in ("memory_off", "memory_on"):
        slot = modes.get(mode_name)
        if not slot:
            continue
        series = score_series(sequence_id, mode_name)
        slot["score_series"] = series
        slot["score_stddev"] = calc_stddev(series)
        slot["progression_label"] = progression_label(series)
        slot["stability_label"] = stability_label(slot["score_stddev"])
(root / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
(root / "progression.json").write_text(json.dumps(progression, indent=2) + "\n")

def avg(slot, field):
    runs = max(1, int(slot.get("run_count", 0) or 1))
    return (int(slot.get(field, 0) or 0) / runs) if slot else 0.0

repo_lines = [
    "# Benchmark Summary",
    "",
    "## By Repo",
    "",
    "| Repo | League | Maturity | Runs | Off Avg | On Avg | Ref Avg | Delta | Delta vs Ref | Agent | Handoff | System | On Hits Avg | Experience | Repo Hits | Tech Hits | Pattern Hits | Forbidden | On Success | Effect | Commit |",
    "|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|",
]
for repo, modes in sorted(aggregates.items()):
    off = modes.get("memory_off", {})
    on = modes.get("memory_on", {})
    ref = modes.get("reference_external", {})
    any_mode = on or off
    league = "-"
    maturity = "-"
    for row in runs:
        if (row.get("repo_id") or row.get("case_id")) == repo:
            league = row.get("benchmark_league") or "-"
            maturity = row.get("maturity_block") or "-"
            break
    off_avg = avg(off, "score_total") if off else 0
    on_avg = avg(on, "score_total") if on else 0
    ref_avg = avg(ref, "score_total") if ref else 0
    delta = on_avg - off_avg
    runs_count = int(on.get("run_count", 0) or off.get("run_count", 0) or 0)
    effect_summary = ", ".join(f"{k}:{v}" for k, v in sorted((on.get("effects") or {}).items())) or "-"
    commit = ((on.get("resolved_commit") or off.get("resolved_commit") or "")[:12]) if any_mode else ""
    repo_lines.append(
        f"| {repo} | {league} | {maturity} | {runs_count} | {off_avg:.1f} | {on_avg:.1f} | {ref_avg:.1f} | {delta:+.1f} | {avg(on, 'delta_vs_reference_total') if on else 0:.1f} | "
        f"{avg(on, 'agent_score_total') if on else 0:.1f} | {avg(on, 'handoff_score_total') if on else 0:.1f} | {avg(on, 'system_score_total') if on else 0:.1f} | "
        f"{avg(on, 'hit_total') if on else 0:.1f} | {avg(on, 'experience_source_total') if on else 0:.1f} | "
        f"{avg(on, 'repo_specific_hit_total') if on else 0:.1f} | {avg(on, 'technology_hit_total') if on else 0:.1f} | "
        f"{avg(on, 'pattern_hit_total') if on else 0:.1f} | {avg(on, 'forbidden_total') if on else 0:.1f} | "
        f"{int(on.get('success_count', 0) or 0)}/{int(on.get('run_count', 0) or 0) if on else 0} | {effect_summary} | {commit} |"
    )

league_lines = [
    "",
    "## By League",
    "",
    "| League | Runs | Off Avg | On Avg | Ref Avg | Delta | Delta vs Ref | Agent | Handoff | System | On Hits Avg | Repo Hits | Tech Hits | Pattern Hits | Forbidden | Effect |",
    "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|",
]
for league, modes in sorted(league_aggregates.items()):
    off = modes.get("memory_off", {})
    on = modes.get("memory_on", {})
    ref = modes.get("reference_external", {})
    off_avg = avg(off, "score_total") if off else 0
    on_avg = avg(on, "score_total") if on else 0
    effect_summary = ", ".join(f"{k}:{v}" for k, v in sorted((on.get("effects") or {}).items())) or "-"
    league_lines.append(
        f"| {league} | {int(on.get('run_count', 0) or off.get('run_count', 0) or 0)} | {off_avg:.1f} | {on_avg:.1f} | {avg(ref, 'score_total') if ref else 0:.1f} | {on_avg - off_avg:+.1f} | {avg(on, 'delta_vs_reference_total') if on else 0:.1f} | "
        f"{avg(on, 'agent_score_total') if on else 0:.1f} | {avg(on, 'handoff_score_total') if on else 0:.1f} | {avg(on, 'system_score_total') if on else 0:.1f} | "
        f"{avg(on, 'hit_total') if on else 0:.1f} | {avg(on, 'repo_specific_hit_total') if on else 0:.1f} | "
        f"{avg(on, 'technology_hit_total') if on else 0:.1f} | {avg(on, 'pattern_hit_total') if on else 0:.1f} | "
        f"{avg(on, 'forbidden_total') if on else 0:.1f} | {effect_summary} |"
    )

sequence_lines = [
    "",
    "## By Sequence",
    "",
    "| Sequence | League | Maturity | Runs | Off Avg | On Avg | Ref Avg | Delta | Delta vs Ref | On Delta vs First | On Delta vs Previous | On Trend | On Stability | On Spread | Agent | Handoff | System | Repo Hits | Tech Hits | Pattern Hits | Forbidden |",
    "|---|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---|---:|---:|---:|---:|---:|---:|---:|---:|",
]
for sequence_id, modes in sorted(sequence_aggregates.items()):
    off = modes.get("memory_off", {})
    on = modes.get("memory_on", {})
    ref = modes.get("reference_external", {})
    league = (on or off).get("benchmark_league", "-") if (on or off) else "-"
    maturity = (on or off).get("maturity_block", "-") if (on or off) else "-"
    sequence_lines.append(
        f"| {sequence_id} | {league} | {maturity} | {int(on.get('run_count', 0) or off.get('run_count', 0) or 0)} | "
        f"{avg(off, 'score_total') if off else 0:.1f} | {avg(on, 'score_total') if on else 0:.1f} | {avg(ref, 'score_total') if ref else 0:.1f} | "
        f"{(avg(on, 'score_total') if on else 0) - (avg(off, 'score_total') if off else 0):+.1f} | "
        f"{avg(on, 'delta_vs_reference_total') if on else 0:.1f} | "
        f"{avg(on, 'score_delta_vs_first_total') if on else 0:.1f} | {avg(on, 'score_delta_vs_previous_total') if on else 0:.1f} | "
        f"{on.get('progression_label', '-') if on else '-'} | {on.get('stability_label', '-') if on else '-'} | {float(on.get('score_stddev', 0) or 0):.2f} | "
        f"{avg(on, 'agent_score_total') if on else 0:.1f} | {avg(on, 'handoff_score_total') if on else 0:.1f} | {avg(on, 'system_score_total') if on else 0:.1f} | "
        f"{avg(on, 'repo_specific_hit_total') if on else 0:.1f} | {avg(on, 'technology_hit_total') if on else 0:.1f} | "
        f"{avg(on, 'pattern_hit_total') if on else 0:.1f} | {avg(on, 'forbidden_total') if on else 0:.1f} |"
    )

root.joinpath("summary.md").write_text("\n".join(repo_lines + league_lines + sequence_lines) + "\n")
root.joinpath("agent_maturity_summary.md").write_text("\n".join(repo_lines + league_lines + sequence_lines) + "\n")

progress_lines = [
    "# Benchmark Progression",
    "",
    "| Sequence | League | Iteration | Off Avg | On Avg | Delta | On Delta vs First | On Delta vs Previous | Repo Hits | Tech Hits | Pattern Hits | Effect |",
    "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|",
]
for sequence_id, iterations in sorted(progression.items()):
    for iteration, modes in sorted(iterations.items(), key=lambda item: int(item[0])):
        off = modes.get("memory_off", {})
        on = modes.get("memory_on", {})
        league = (on or off).get("benchmark_league", "-") if (on or off) else "-"
        effect_summary = ", ".join(f"{k}:{v}" for k, v in sorted((on.get("effects") or {}).items())) or "-"
        progress_lines.append(
            f"| {sequence_id} | {league} | {iteration} | {avg(off, 'score_total') if off else 0:.1f} | "
            f"{avg(on, 'score_total') if on else 0:.1f} | {((avg(on, 'score_total') if on else 0) - (avg(off, 'score_total') if off else 0)):+.1f} | "
            f"{avg(on, 'score_delta_vs_first_total') if on else 0:.1f} | {avg(on, 'score_delta_vs_previous_total') if on else 0:.1f} | "
            f"{avg(on, 'repo_specific_hit_total') if on else 0:.1f} | {avg(on, 'technology_hit_total') if on else 0:.1f} | "
            f"{avg(on, 'pattern_hit_total') if on else 0:.1f} | {effect_summary} |"
        )

root.joinpath("progression.md").write_text("\n".join(progress_lines) + "\n")
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
  local config_repos_file
  config_repos_file="$(config_get repos_file)"
  if [[ -z "$config_repos_file" ]]; then
    config_repos_file="benchmarks/repos.default.json"
  fi
  python3 - "$CONFIG_PATH" "$CASE_DIR" "${ROOT_DIR}/${config_repos_file}" "$GENERATED_CASE_DIR" "$ROOT_DIR/scripts/generate-benchmark-cases.py" <<'PY'
import json, pathlib, sys
config = json.loads(pathlib.Path(sys.argv[1]).read_text())
case_dir = pathlib.Path(sys.argv[2])
repo_catalog = pathlib.Path(sys.argv[3])
generated_dir = pathlib.Path(sys.argv[4])
generator = pathlib.Path(sys.argv[5])
case_mode = config.get("case_mode") or "static"
case_ids = config.get("case_ids") or []

if case_mode == "generated_catalog":
    generated_dir.mkdir(parents=True, exist_ok=True)
    repo_ids_list = config.get("repo_ids") or []
    repo_ids = ",".join(repo_ids_list)
    import subprocess
    cmd = [sys.executable, str(generator), "--repos", str(repo_catalog), "--outdir", str(generated_dir)]
    if repo_ids:
        cmd.extend(["--repo-ids", repo_ids])
    subprocess.run(cmd, check=True)
    if repo_ids_list:
        for repo_id in repo_ids_list:
            path = generated_dir / f"{repo_id}-experience-review.json"
            if path.exists():
                print(path)
    else:
        for path in sorted(generated_dir.glob("*.json")):
            print(path)
elif case_ids:
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
  python3 - "${run_dir}/benchmark_run.json" "${run_dir}/agent_maturity_run.json" <<'PY'
import json, pathlib, sys
src = pathlib.Path(sys.argv[1])
dst = pathlib.Path(sys.argv[2])
data = json.loads(src.read_text())
if data.get("maturity_block"):
    dst.write_text(json.dumps(data, indent=2) + "\n")
PY
  python3 - "${run_dir}/benchmark_run.json" <<'PY'
import json, pathlib, sys
path = pathlib.Path(sys.argv[1])
data = json.loads(path.read_text())
lines = [
    f"# Benchmark Run: {data.get('case_id', 'unknown')}",
    "",
    f"- Mode: {data.get('mode')}",
    f"- League: {data.get('benchmark_league', '-')}",
    f"- Sequence: {data.get('sequence_id', '-')}",
    f"- Sequence Position: {data.get('sequence_position', '-')}",
    f"- Repository: {data.get('repo_url')}",
    f"- Branch: {data.get('branch')}",
    f"- Commit: {data.get('resolved_commit')}",
    f"- Outcome: {data.get('status')}",
    f"- Initiative: {data.get('initiative_id', '')}",
    f"- Score: {data.get('score', 0)}",
    f"- Baseline Mode: {data.get('baseline_mode', '-')}",
    f"- Baseline Score: {data.get('baseline_score', '-')}",
    f"- Delta vs Memory Off: {data.get('delta_vs_memory_off', 0)}",
    f"- Delta vs Reference: {data.get('delta_vs_reference_external', 0)}",
    f"- Score Delta vs First Run: {data.get('score_delta_vs_first_run', 0)}",
    f"- Score Delta vs Previous Run: {data.get('score_delta_vs_previous_run', 0)}",
    f"- Memory Effect: {data.get('memory_effect', '-')}",
    f"- Retrieval Precision: {data.get('retrieval_precision_label', '-')}",
    f"- Forbidden Hits: {data.get('forbidden_hit_count', 0)}",
    f"- Experience Sources: {data.get('experience_source_count', 0)}",
    f"- Maturity Block: {data.get('maturity_block', '-')}",
    f"- Primary Agent: {data.get('primary_agent', '-')}",
    f"- Agent Score: {data.get('agent_score', 0)}",
    f"- Handoff Score: {data.get('handoff_score', 0)}",
    f"- System Score: {data.get('system_score', 0)}",
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

  if [[ "$mode" == "reference_external" ]]; then
    local paired_run_dir reference_payload reference_response judge_payload judge_response
    paired_run_dir="${REPORT_ROOT}/$(basename "${case_path%.json}")-memory_on-run${iteration}"
    if [[ ! -f "${paired_run_dir}/benchmark_run.json" ]]; then
      write_run_report "$run_dir" "$(python3 - "$case_path" <<'PY'
import json, pathlib, sys
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
print(json.dumps({
  "case_id": case["id"],
  "mode": "reference_external",
  "status": "failed",
  "score": 0,
  "reason": "paired memory_on run is required before reference_external"
}, indent=2))
PY
)"
      return 1
    fi
    reference_payload="$(python3 - "$case_path" "${paired_run_dir}/planner_task.json" "${paired_run_dir}/researcher_task.json" "${paired_run_dir}/coder_task.json" "${paired_run_dir}/reviewer_task.json" "${paired_run_dir}/initiative_detail.json" "${paired_run_dir}/benchmark_run.json" <<'PY'
import json, pathlib, sys
case = json.loads(pathlib.Path(sys.argv[1]).read_text())
planner = json.loads(pathlib.Path(sys.argv[2]).read_text())
researcher = json.loads(pathlib.Path(sys.argv[3]).read_text())
coder = json.loads(pathlib.Path(sys.argv[4]).read_text())
reviewer = json.loads(pathlib.Path(sys.argv[5]).read_text())
initiative_detail = json.loads(pathlib.Path(sys.argv[6]).read_text())
run = json.loads(pathlib.Path(sys.argv[7]).read_text())
mode = case.get("reference_eval_mode") or "coordination_quality"

def compact(obj):
    return json.dumps(obj, indent=2, ensure_ascii=False)

def preview(value, limit=600):
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return None
    return text[:limit]

def artifact_titles(task):
    results = task.get("results") or {}
    titles = []
    for item in results.get("artifacts") or []:
        title = item.get("title") or item.get("type")
        if title:
            titles.append(title)
    return titles

def task_evidence(task):
    metadata = task.get("metadata") or {}
    results = task.get("results") or {}
    return {
        "state": task.get("state"),
        "summary": results.get("summary"),
        "status": results.get("status"),
        "exit_code": results.get("exit_code"),
        "changed_files": results.get("changed_files"),
        "artifact_titles": artifact_titles(task),
        "stdout_preview": preview(results.get("stdout")),
        "stderr_preview": preview(results.get("stderr")),
        "semantic_context_chunk_count": results.get("semantic_context_chunk_count"),
        "semantic_context_hit_count": len((results.get("semantic_context_hits") or [])),
        "approval_required": metadata.get("approval_required") or metadata.get("requires_approval"),
        "approval_granted": metadata.get("approval_granted"),
    }

planner_results = planner.get("results") or {}
researcher_results = (researcher.get("results") or {})
coder_results = (coder.get("results") or {})
reviewer_results = (reviewer.get("results") or {})
initiative = initiative_detail.get("initiative") or {}
execution_summary = initiative_detail.get("execution_summary") or {}
reviews = initiative_detail.get("reviews") or []
review_decisions = [{"phase": item.get("phase"), "decision": item.get("decision")} for item in reviews]

if mode == "plan_quality":
    orchestrator_answer = compact({
        "assigned_agents": planner_results.get("assigned_agents"),
        "launch_order": planner_results.get("launch_order"),
        "requires_approval_agents": planner_results.get("requires_approval_agents"),
        "missing_agents": planner_results.get("missing_agents"),
        "coder_tool": planner_results.get("coder_tool"),
        "plan_executable": planner_results.get("plan_executable"),
        "fallback_detected": planner_results.get("fallback_detected"),
        "initiative_status": initiative.get("status"),
        "review_decisions": review_decisions,
    })
elif mode == "review_quality":
    orchestrator_answer = compact({
        "reviewer_evidence": task_evidence(reviewer),
        "coder_evidence": task_evidence(coder),
        "initiative_status": initiative.get("status"),
        "execution_summary": execution_summary,
    })
elif mode == "execution_quality":
    orchestrator_answer = compact({
        "researcher_evidence": task_evidence(researcher),
        "coder_evidence": task_evidence(coder),
        "reviewer_evidence": task_evidence(reviewer),
        "initiative_status": initiative.get("status"),
        "execution_summary": execution_summary,
        "review_decisions": review_decisions,
    })
else:
    orchestrator_answer = compact({
        "planner": planner_results,
        "researcher_evidence": task_evidence(researcher),
        "coder_evidence": task_evidence(coder),
        "reviewer_evidence": task_evidence(reviewer),
        "memory_hits": run.get("memory_hits"),
        "handoff_chain": case.get("expected_handoffs") or [],
        "initiative_status": initiative.get("status"),
        "execution_summary": execution_summary,
        "review_decisions": review_decisions,
    })

sources = []
for hit in run.get("memory_hits") or []:
    ref = hit.get("source_ref") or "benchmark-artifact"
    sources.append({"title": ref, "uri": "", "kind": hit.get("match_type") or "benchmark_memory"})

query = "\n".join([
    case.get("goal") or "",
    "Evaluate the observed governed multi-agent outcome against the success contract and describe the ideal outcome.",
    "Respect this success contract:",
    compact(case.get("success_contract") or {}),
])

print(json.dumps({
    "query": query,
    "orchestrator_answer": orchestrator_answer,
    "sources": sources
}, ensure_ascii=False))
PY
)"
    reference_response="$(request POST "/evaluations/reference" "$reference_payload")"
    printf '%s\n' "$reference_response" > "${run_dir}/evaluation_reference.json"
    judge_payload="$(REFERENCE_JSON="$reference_response" REFERENCE_PAYLOAD="$reference_payload" python3 - <<'PY'
import json, os
ref = json.loads(os.environ["REFERENCE_JSON"])
payload = json.loads(os.environ["REFERENCE_PAYLOAD"])
print(json.dumps({
    "query": payload["query"],
    "orchestrator_answer": payload["orchestrator_answer"],
    "reference_answer": ref["reference_answer"],
    "sources": payload.get("sources") or [],
}))
PY
)"
    judge_response="$(request POST "/evaluations/judge" "$judge_payload")"
    printf '%s\n' "$judge_response" > "${run_dir}/evaluation_judge.json"
    write_run_report "$run_dir" "$(CASE_PATH="$case_path" JUDGE_JSON="$judge_response" BASELINE_RUN="${paired_run_dir}/benchmark_run.json" python3 - <<'PY'
import json, os, pathlib
case = json.loads(pathlib.Path(os.environ["CASE_PATH"]).read_text())
judge = json.loads(os.environ["JUDGE_JSON"])
baseline = json.loads(pathlib.Path(os.environ["BASELINE_RUN"]).read_text())
scores = judge.get("judge_scores") or {}
acc = float(scores.get("accuracy_score", 0) or 0)
cov = float(scores.get("coverage_score", 0) or 0)
src = float(scores.get("source_use_score", 0) or 0)
use = float(scores.get("usefulness_score", 0) or 0)
hall = float(scores.get("hallucination_risk_score", 1) or 1)
reference_score = round(((acc + cov + src + use + (1.0 - hall)) / 5.0) * 100.0, 1)
print(json.dumps({
  "case_id": case["id"],
  "mode": "reference_external",
  "baseline_mode": "reference_external",
  "baseline_score": reference_score,
  "reference_score": reference_score,
  "delta_vs_memory_off": 0,
  "delta_vs_reference_external": 0,
  "benchmark_league": case.get("benchmark_league"),
  "maturity_block": case.get("maturity_block"),
  "primary_agent": case.get("primary_agent"),
  "sequence_id": case.get("sequence_id") or case["id"],
  "sequence_position": int(case.get("sequence_position") or 1),
  "repo_id": baseline.get("repo_id"),
  "repo_url": baseline.get("repo_url"),
  "branch": baseline.get("branch"),
  "resolved_commit": baseline.get("resolved_commit"),
  "status": "success",
  "score": reference_score,
  "agent_score": 0,
  "handoff_score": 0,
  "system_score": 0,
  "agent_scores": {},
  "handoff_scores": {},
  "reference_eval_mode": case.get("reference_eval_mode"),
  "reference_provider": judge.get("mode", "direct"),
  "retrieval_precision_label": "reference_external"
}, indent=2))
PY
)"
    log "completed $(basename "${case_path%.json}") reference_external run ${iteration}"
    return 0
  fi

  local memory_mode="on"
  if [[ "$mode" == "memory_off" ]]; then
    memory_mode="off"
  fi

  local workspace_root case_id repo_id repo_url repo_branch repo_profile case_goal strategy benchmark_league sequence_id sequence_position
  local memory_expectation forbidden_match_types_json maturity_block primary_agent expected_handoffs_json success_contract_json reference_eval_mode
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
    "benchmark_league": case.get("benchmark_league") or "",
    "sequence_id": case.get("sequence_id") or case["id"],
    "sequence_position": case.get("sequence_position") or 1,
    "strategy": case.get("benchmark_memory_strategy") or "$MEMORY_STRATEGY_DEFAULT",
    "memory_expectation": case.get("memory_expectation") or "helpful",
    "forbidden_match_types": case.get("forbidden_match_types") or [],
    "maturity_block": case.get("maturity_block") or "",
    "primary_agent": case.get("primary_agent") or "",
    "expected_handoffs": case.get("expected_handoffs") or [],
    "success_contract": case.get("success_contract") or {},
    "reference_eval_mode": case.get("reference_eval_mode") or ""
}))
PY
)"
  case_id="$(printf '%s' "$run_meta" | json_get case_id)"
  repo_id="$(printf '%s' "$run_meta" | json_get repo_id)"
  repo_url="$(printf '%s' "$run_meta" | json_get repo_url)"
  repo_branch="$(printf '%s' "$run_meta" | json_get default_branch)"
  repo_profile="$(printf '%s' "$run_meta" | json_get repo_profile)"
  case_goal="$(printf '%s' "$run_meta" | json_get goal)"
  benchmark_league="$(printf '%s' "$run_meta" | json_get benchmark_league)"
  sequence_id="$(printf '%s' "$run_meta" | json_get sequence_id)"
  sequence_position="$(printf '%s' "$run_meta" | json_get sequence_position)"
  strategy="$(printf '%s' "$run_meta" | json_get strategy)"
  memory_expectation="$(printf '%s' "$run_meta" | json_get memory_expectation)"
  forbidden_match_types_json="$(printf '%s' "$run_meta" | json_get forbidden_match_types)"
  maturity_block="$(printf '%s' "$run_meta" | json_get maturity_block)"
  primary_agent="$(printf '%s' "$run_meta" | json_get primary_agent)"
  expected_handoffs_json="$(printf '%s' "$run_meta" | json_get expected_handoffs)"
  success_contract_json="$(printf '%s' "$run_meta" | json_get success_contract)"
  reference_eval_mode="$(printf '%s' "$run_meta" | json_get reference_eval_mode)"

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
  build_planner_task_report "$tasks_json" "$workspace_root/.lab/benchmark-case.json" "$initiative_id" > "${run_dir}/planner_task.json"
  request POST "/initiatives/${initiative_id}/approve/plan" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"benchmark\"}" >/dev/null
  researcher_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent researcher)"
  coder_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent coder)"
  reviewer_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent reviewer)"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${researcher_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  local researcher_json
  researcher_json="$(wait_for_task_state "$researcher_task_id" "completed" || request GET "/tasks/${researcher_task_id}")"
  researcher_json="$(enrich_task_json_for_report "$researcher_json")"
  printf '%s\n' "$researcher_json" > "${run_dir}/researcher_task.json"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${coder_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  approval_id="$(wait_for_pending_approval "$coder_task_id")"
  request POST "/approvals/${approval_id}/approve" "{\"operator\":\"${OPERATOR}\"}" >/dev/null
  local coder_json reviewer_json initiative_final initiative_status artifacts_json reviewer_task_json
  coder_json="$(wait_for_task_state "$coder_task_id" "completed" || request GET "/tasks/${coder_task_id}")"
  coder_json="$(enrich_task_json_for_report "$coder_json")"
  printf '%s\n' "$coder_json" > "${run_dir}/coder_task.json"

  request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${reviewer_task_id}\"],\"mode_overrides\":{}}" >/dev/null
  reviewer_json="$(wait_for_task_state "$reviewer_task_id" "completed" || request GET "/tasks/${reviewer_task_id}")"
  reviewer_task_json="$(request GET "/tasks/${reviewer_task_id}")"
  initiative_final="$(wait_for_initiative_terminal "$initiative_id" || request GET "/initiatives/${initiative_id}")"
  initiative_status="$(printf '%s' "$initiative_final" | json_get initiative.status)"
  artifacts_json="$(request GET "/initiatives/${initiative_id}/artifacts")"

  printf '%s\n' "$artifacts_json" > "${run_dir}/initiative_artifacts.json"
  printf '%s\n' "$initiative_final" > "${run_dir}/initiative_detail.json"
  reviewer_task_json="$(enrich_task_json_for_report "$reviewer_task_json")"
  printf '%s\n' "$reviewer_task_json" > "${run_dir}/reviewer_task.json"
  git -C "$workspace_root" diff --no-ext-diff > "${run_dir}/git.diff" || true
  tail -n 80 "$bridge_log" > "${run_dir}/bridge.log.tail" || true

  local report_json
report_json="$(python3 - "${run_dir}/planner_task.json" "${run_dir}/researcher_task.json" "${run_dir}/coder_task.json" "${run_dir}/reviewer_task.json" "${run_dir}/initiative_artifacts.json" <<PY
import json, pathlib, sys
planner = json.loads(pathlib.Path(sys.argv[1]).read_text())
researcher = json.loads(pathlib.Path(sys.argv[2]).read_text())
coder = json.loads(pathlib.Path(sys.argv[3]).read_text())
reviewer = json.loads(pathlib.Path(sys.argv[4]).read_text())
artifacts = json.loads(pathlib.Path(sys.argv[5]).read_text()).get("items", [])
hit_map = {}
for stage_name, task in (("researcher", researcher), ("coder", coder), ("reviewer", reviewer)):
    ctx = (task.get("metadata") or {}).get("context_package") or {}
    for chunk in (ctx.get("chunks") or []):
        meta = chunk.get("metadata") or {}
        key = (
            chunk.get("source_ref") or "",
            meta.get("memory_match_type", "unknown"),
            meta.get("repo_profile"),
        )
        if key not in hit_map:
            hit_map[key] = {
                "source_ref": chunk.get("source_ref"),
                "match_type": meta.get("memory_match_type", "unknown"),
                "repo_profile": meta.get("repo_profile"),
                "stages": [],
            }
        if stage_name not in hit_map[key]["stages"]:
            hit_map[key]["stages"].append(stage_name)
    for hit in ((task.get("results") or {}).get("semantic_context_hits") or []):
        match_type = hit.get("match_type", "unknown")
        repo_profile = hit.get("repo_profile")
        source_ref = hit.get("source_ref")
        key = (source_ref or "", match_type, repo_profile)
        if key not in hit_map:
            hit_map[key] = {
                "source_ref": source_ref,
                "match_type": match_type,
                "repo_profile": repo_profile,
                "stages": [],
            }
        if stage_name not in hit_map[key]["stages"]:
            hit_map[key]["stages"].append(stage_name)
hits = list(hit_map.values())
forbidden_types = set(json.loads('''$forbidden_match_types_json'''))
forbidden_hits = [hit for hit in hits if hit.get("match_type") in forbidden_types]
repo_specific_hits = [hit for hit in hits if hit.get("match_type") == "repo_specific"]
technology_hits = [hit for hit in hits if hit.get("match_type") == "technology_similar"]
pattern_hits = [hit for hit in hits if hit.get("match_type") == "pattern_similar"]
unique_sources = sorted({hit.get("source_ref", "") for hit in hits if hit.get("source_ref")})
status = "success" if "${initiative_status}" == "completed" and reviewer.get("state") == "completed" else "failed"
results = reviewer.get("results") or {}
reviewer_ok = reviewer.get("state") == "completed" and results.get("status") == "success" and results.get("exit_code") == 0
if not reviewer_ok:
    status = "failed"
planner_results = planner.get("results") or {}
researcher_results = researcher.get("results") or {}
coder_results = coder.get("results") or {}
case_success = json.loads('''$success_contract_json''')
expected_handoffs = json.loads('''$expected_handoffs_json''')

def task_completed(task):
    return (task.get("state") or "") == "completed"

def parse_iso(task, field):
    value = task.get(field)
    if not value:
        return None
    return value

launch_order = planner_results.get("launch_order") or []
expected_launch_order = case_success.get("expected_launch_order") or []
launch_order_ok = launch_order[:len(expected_launch_order)] == expected_launch_order if expected_launch_order else True

primary_agent = "$primary_agent"
maturity_block = "$maturity_block"
reference_eval_mode = "$reference_eval_mode"
reviewer_error = " ".join([
    str(results.get("error") or ""),
    str(results.get("stderr") or ""),
    str(reviewer.get("error_message") or ""),
]).lower()
toolchain_confusion = any(token in reviewer_error for token in ["command not found", "docker", "pip install", "module not found", "composer: not found"])

agent_scores = {
    "planner": 0,
    "researcher": 0,
    "coder": 0,
    "reviewer": 0,
}
if planner_results.get("materialized_plan_valid"):
    agent_scores["planner"] += 5
if planner_results.get("plan_executable"):
    agent_scores["planner"] += 5
if not planner_results.get("fallback_detected"):
    agent_scores["planner"] += 5

if task_completed(researcher):
    agent_scores["researcher"] += 5
if researcher_results.get("recommendations") or researcher_results.get("checklist") or researcher_results.get("stack_rationale"):
    agent_scores["researcher"] += 5
if task_completed(researcher) and task_completed(coder):
    agent_scores["researcher"] += 5

if task_completed(coder):
    agent_scores["coder"] += 5
if coder_results.get("changed_files") or pathlib.Path("${run_dir}/git.diff").read_text().strip():
    agent_scores["coder"] += 5
if reviewer_ok:
    agent_scores["coder"] += 5

if task_completed(reviewer):
    agent_scores["reviewer"] += 5
if reviewer_ok:
    agent_scores["reviewer"] += 5
if not toolchain_confusion:
    agent_scores["reviewer"] += 5

handoff_scores = {}
handoff_scores["planner_to_researcher_quality"] = 5 if ("planner->researcher" in expected_handoffs and "researcher" in launch_order[:1] and task_completed(researcher)) else (5 if "planner->researcher" not in expected_handoffs else 0)
handoff_scores["planner_to_coder_quality"] = 5 if ("planner->coder" in expected_handoffs and planner_results.get("coder_tool") == case_success.get("expected_coder_tool") and task_completed(coder)) else (5 if "planner->coder" not in expected_handoffs else 0)
handoff_scores["researcher_to_coder_usefulness"] = 5 if ("researcher->coder" in expected_handoffs and task_completed(researcher) and task_completed(coder)) else (5 if "researcher->coder" not in expected_handoffs else 0)
handoff_scores["coder_to_reviewer_reviewability"] = 5 if ("coder->reviewer" in expected_handoffs and task_completed(coder) and task_completed(reviewer)) else (5 if "coder->reviewer" not in expected_handoffs else 0)
handoff_scores["memory_reuse_across_handoff"] = 5 if ("$mode" == "memory_on" and len(hits) > 0) or ("$memory_expectation" == "avoid_transfer" and len(hits) == 0) else 0
handoff_scores["approval_gate_recovery"] = 5 if (case_success.get("requires_coder_approval") and bool((coder.get("metadata") or {}).get("requires_approval")) and task_completed(coder)) or (not case_success.get("requires_coder_approval")) else 0

handoff_expected_count = max(1, len(expected_handoffs) + 2)
handoff_score = round((sum(handoff_scores.values()) / (handoff_expected_count * 5.0)) * 15.0, 1)
primary_agent_score = float(agent_scores.get(primary_agent, 0))
system_score = 0
if status == "success":
    system_score += 30
if reviewer_ok:
    system_score += 15

memory_score = 0
league = "$benchmark_league"
if league == "repo_recall" and repo_specific_hits:
    memory_score = 10
elif league == "technology_transfer" and technology_hits and not repo_specific_hits:
    memory_score = 10
elif league == "pattern_transfer":
    if pattern_hits and not repo_specific_hits:
        memory_score = 10
    elif technology_hits and not repo_specific_hits:
        memory_score = 6
elif "$memory_expectation" == "avoid_transfer" and not hits:
    memory_score = 10
memory_effect = "baseline"
if "$mode" == "memory_on":
    if forbidden_hits:
        memory_effect = "hurt"
    elif "$memory_expectation" == "avoid_transfer":
        memory_effect = "guarded" if not hits else "neutral"
    elif hits:
        memory_effect = "helped"
    else:
        memory_effect = "neutral"
memory_penalty = 15 if forbidden_hits else 0
handoff_penalty = 15 if any(score == 0 for key, score in handoff_scores.items() if key.startswith(("planner_", "researcher_", "coder_")) and key.replace("_quality", "").replace("_usefulness", "").replace("_reviewability", "").replace("_", "->") in expected_handoffs) else 0
fallback_penalty = 10 if planner_results.get("fallback_detected") else 0
review_penalty = 10 if toolchain_confusion and not reviewer_ok else 0
score = max(0, round(system_score + primary_agent_score + handoff_score + memory_score - memory_penalty - handoff_penalty - fallback_penalty - review_penalty, 1))
print(json.dumps({
    "case_id": "$case_id",
    "mode": "$mode",
    "baseline_mode": "$mode",
    "baseline_score": score if "$mode" == "memory_off" else None,
    "iteration": $iteration,
    "benchmark_league": "$benchmark_league",
    "maturity_block": maturity_block,
    "primary_agent": primary_agent,
    "reference_eval_mode": reference_eval_mode,
    "expected_handoffs": expected_handoffs,
    "handoff_chain": expected_handoffs,
    "sequence_id": "$sequence_id",
    "sequence_position": int("$sequence_position"),
    "memory_mode": "$memory_mode",
    "memory_strategy": "$strategy",
    "memory_expectation": "$memory_expectation",
    "repo_id": "$repo_id",
    "repo_url": "$repo_url",
    "branch": "$repo_branch",
    "resolved_commit": "$resolved_commit",
    "status": status,
    "initiative_id": "$initiative_id",
    "researcher_task_id": "$researcher_task_id",
    "coder_task_id": "$coder_task_id",
    "reviewer_task_id": "$reviewer_task_id",
    "memory_hits": hits,
    "experience_source_count": len(unique_sources),
    "repo_specific_hit_count": len(repo_specific_hits),
    "technology_hit_count": len(technology_hits),
    "pattern_hit_count": len(pattern_hits),
    "forbidden_hit_count": len(forbidden_hits),
    "memory_effect": memory_effect,
    "retrieval_precision_label": memory_effect,
    "artifact_count": len(artifacts),
    "agent_scores": agent_scores,
    "agent_score": primary_agent_score,
    "handoff_scores": handoff_scores,
    "handoff_score": handoff_score,
    "system_score": system_score,
    "memory_score": memory_score,
    "memory_penalty": memory_penalty,
    "handoff_penalty": handoff_penalty,
    "fallback_penalty": fallback_penalty,
    "review_penalty": review_penalty,
    "score": score
}, indent=2))
PY
)"
  write_run_report "$run_dir" "$report_json"
  log "completed ${case_id} ${mode} run ${iteration}"
}

mode_list="${MODES//,/ }"
for case_path in "${CASE_PATHS[@]}"; do
  for ((i=1; i<=RUNS_PER_CASE; i++)); do
    for mode in $mode_list; do
      run_case "$mode" "$case_path" "$i"
    done
  done
done

build_summary_reports "$REPORT_ROOT"
log "benchmark reports written to ${REPORT_ROOT}"
