#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_URL="${LAB_ORCHESTRATOR_BASE_URL:-http://127.0.0.1:8100}"
API_KEY="${LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:-${LAB_AGENT_API_KEY:-}}"
REPO_URL="${LAB_GOLDEN_PATH_REPO_URL:-https://github.com/un33k/python-slugify}"
REPO_BRANCH="${LAB_GOLDEN_PATH_REPO_BRANCH:-master}"
OPERATOR="${LAB_GOLDEN_PATH_OPERATOR:-golden-path-smoke}"
POLL_INTERVAL="${LAB_GOLDEN_PATH_POLL_INTERVAL:-2}"
TIMEOUT_SECONDS="${LAB_GOLDEN_PATH_TIMEOUT_SECONDS:-180}"
BRIDGE_POLL_INTERVAL="${LAB_GOLDEN_PATH_BRIDGE_POLL_INTERVAL:-2s}"
BRIDGE_HEARTBEAT_INTERVAL="${LAB_GOLDEN_PATH_BRIDGE_HEARTBEAT_INTERVAL:-10s}"

if [[ -z "$API_KEY" ]]; then
  echo "LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY or LAB_AGENT_API_KEY is required" >&2
  exit 2
fi

SMOKE_ROOT="$(mktemp -d /tmp/lab-golden-path-bridge-XXXXXX)"
WORKSPACE_ROOT="${SMOKE_ROOT}/python-slugify"
PATCH_FILE="${SMOKE_ROOT}/python-slugify.patch"
BRIDGE_LOG="${SMOKE_ROOT}/lab-agentd.log"
BRIDGE_ID="golden-path-$$"
BRIDGE_NAME="golden-path-smoke"
BRIDGE_PID=""

log() {
  printf '[golden-path] %s\n' "$*"
}

cleanup() {
  if [[ -n "$BRIDGE_PID" ]] && kill -0 "$BRIDGE_PID" >/dev/null 2>&1; then
    kill "$BRIDGE_PID" >/dev/null 2>&1 || true
    wait "$BRIDGE_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$SMOKE_ROOT"
}
trap cleanup EXIT

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $API_KEY" \
      -H "Content-Type: application/json" \
      -d "$body"
  else
    curl -fsS -X "$method" "$BASE_URL$path" \
      -H "Authorization: Bearer $API_KEY"
  fi
}

json_get() {
  local path="$1"
  local payload
  payload="$(cat)"
  JSON_INPUT="$payload" python3 - "$path" <<'PY'
import json, os, sys
path = sys.argv[1].split(".")
data = json.loads(os.environ["JSON_INPUT"])
value = data
for part in path:
    if part == "":
        continue
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
    local task_json
    task_json="$(request GET "/tasks/${task_id}")"
    local state
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
    local initiative_json
    initiative_json="$(request GET "/initiatives/${initiative_id}")"
    local status
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

soft_fail_external() {
  log "SOFT_FAIL_EXTERNAL: $*"
  log "workspace_root=${WORKSPACE_ROOT}"
  if [[ -f "$BRIDGE_LOG" ]]; then
    log "bridge_log_tail:"
    tail -n 40 "$BRIDGE_LOG" || true
  fi
  exit 0
}

internal_fail() {
  log "INTERNAL_FAIL: $*"
  log "workspace_root=${WORKSPACE_ROOT}"
  if [[ -f "$BRIDGE_LOG" ]]; then
    log "bridge_log_tail:"
    tail -n 80 "$BRIDGE_LOG" || true
  fi
  exit 1
}

cat >"$PATCH_FILE" <<'PATCH'
diff --git a/slugify/__main__.py b/slugify/__main__.py
--- a/slugify/__main__.py
+++ b/slugify/__main__.py
@@ -76,6 +76,7 @@ def slugify_params(args: argparse.Namespace) -> dict[str, Any]:
         save_order=args.save_order,
         separator=args.separator,
         stopwords=args.stopwords,
+        regex_pattern=args.regex_pattern,
         lowercase=args.lowercase,
         replacements=args.replacements,
         allow_unicode=args.allow_unicode
diff --git a/test.py b/test.py
--- a/test.py
+++ b/test.py
@@ -612,6 +612,11 @@ class TestCommandParams(unittest.TestCase):
         expected = self.make_params(stopwords=['abba', 'beatles'], max_length=98, separator='+')
         self.assertParamsMatch(expected, params)
 
+    def test_regex_pattern_param(self):
+        params = self.get_params_from_cli('--regex-pattern', '[^a-z]+')
+        expected = self.make_params(regex_pattern='[^a-z]+')
+        self.assertParamsMatch(expected, params)
+
     def test_replacements_right(self):
         params = self.get_params_from_cli('--replacements', 'A->B', 'C->D')
         expected = self.make_params(replacements=[['A', 'B'], ['C', 'D']])
PATCH

log "Verifying orchestrator readiness at ${BASE_URL}"
request GET "/ready" >/dev/null || internal_fail "orchestrator is not ready"

log "Resolving upstream HEAD for ${REPO_URL} (${REPO_BRANCH})"
remote_head="$(git ls-remote --heads "$REPO_URL" "$REPO_BRANCH" | awk '{print $1}')"
if [[ -z "$remote_head" ]]; then
  soft_fail_external "failed to resolve remote branch ${REPO_BRANCH}"
fi
log "resolved_branch=${REPO_BRANCH}"
log "resolved_head=${remote_head}"

log "Cloning external repository"
if ! git clone --depth 1 --branch "$REPO_BRANCH" "$REPO_URL" "$WORKSPACE_ROOT" >/dev/null 2>&1; then
  soft_fail_external "failed to clone ${REPO_URL}#${REPO_BRANCH}"
fi

resolved_commit="$(git -C "$WORKSPACE_ROOT" rev-parse HEAD)"
log "clone_path=${WORKSPACE_ROOT}"
log "clone_commit=${resolved_commit}"

if ! git -C "$WORKSPACE_ROOT" apply --check "$PATCH_FILE" >/dev/null 2>&1; then
  soft_fail_external "upstream drift detected: deterministic patch no longer applies cleanly"
fi

log "Starting local bridge daemon"
(
  cd "$ROOT_DIR"
  exec go run ./cmd/lab-agentd \
    --base-url "$BASE_URL" \
    --api-key "$API_KEY" \
    --bridge-id "$BRIDGE_ID" \
    --workspace-root "$WORKSPACE_ROOT" \
    --name "$BRIDGE_NAME" \
    --poll-interval "$BRIDGE_POLL_INTERVAL" \
    --heartbeat-interval "$BRIDGE_HEARTBEAT_INTERVAL"
) >"$BRIDGE_LOG" 2>&1 &
BRIDGE_PID="$!"

sleep 3
if ! kill -0 "$BRIDGE_PID" >/dev/null 2>&1; then
  internal_fail "lab-agentd failed to start"
fi

initiative_payload="$(python3 - <<PY
import json
print(json.dumps({
    "title": "Golden path bridge smoke",
    "goal": "Patch python-slugify so the CLI forwards regex_pattern and validate the repo end to end.",
    "workspace_root": "$WORKSPACE_ROOT",
    "created_by": "$OPERATOR",
    "execution_mode": "selective",
}))
PY
)"
initiative_json="$(request POST "/initiatives/" "$initiative_payload")"
initiative_id="$(printf '%s' "$initiative_json" | json_get id)"
[[ -n "$initiative_id" ]] || internal_fail "initiative creation did not return an id"
log "initiative_id=${initiative_id}"

request POST "/initiatives/${initiative_id}/advance" "{}" >/dev/null
request POST "/initiatives/${initiative_id}/approve/requirements" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"golden path smoke\"}" >/dev/null
request POST "/initiatives/${initiative_id}/advance" "{}" >/dev/null
request POST "/initiatives/${initiative_id}/approve/design" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"golden path smoke\"}" >/dev/null
request POST "/initiatives/${initiative_id}/tasks/generate" "{}" >/dev/null
request POST "/initiatives/${initiative_id}/approve/plan" "{\"operator\":\"${OPERATOR}\",\"feedback\":\"golden path smoke\"}" >/dev/null

tasks_json="$(request GET "/initiatives/${initiative_id}/tasks")"
researcher_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent researcher)" || internal_fail "researcher task not found"
coder_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent coder)" || internal_fail "coder task not found"
reviewer_task_id="$(printf '%s' "$tasks_json" | initiative_task_id_by_agent reviewer)" || internal_fail "reviewer task not found"
log "researcher_task_id=${researcher_task_id}"
log "coder_task_id=${coder_task_id}"
log "reviewer_task_id=${reviewer_task_id}"

request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${researcher_task_id}\"],\"mode_overrides\":{}}" >/dev/null
researcher_json="$(wait_for_task_state "$researcher_task_id" "completed")" || internal_fail "researcher task did not complete successfully"
log "researcher_state=$(printf '%s' "$researcher_json" | json_get state)"

request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${coder_task_id}\"],\"mode_overrides\":{}}" >/dev/null
approval_id="$(wait_for_pending_approval "$coder_task_id")" || internal_fail "coder approval was not created"
log "coder_approval_id=${approval_id}"
request POST "/approvals/${approval_id}/approve" "{\"operator\":\"${OPERATOR}\"}" >/dev/null

coder_json="$(wait_for_task_state "$coder_task_id" "completed")" || {
  failed_json="$(request GET "/tasks/${coder_task_id}")"
  error_message="$(printf '%s' "$failed_json" | json_get error_message)"
  if [[ "$error_message" == *"patch failed"* || "$error_message" == *"No such file"* || "$error_message" == *"does not apply"* ]]; then
    soft_fail_external "coder patch failed because upstream repository layout drifted"
  fi
  internal_fail "coder task failed after approval"
}
log "coder_state=$(printf '%s' "$coder_json" | json_get state)"

request POST "/initiatives/${initiative_id}/tasks/launch" "{\"task_ids\":[\"${reviewer_task_id}\"],\"mode_overrides\":{}}" >/dev/null
reviewer_json="$(wait_for_task_state "$reviewer_task_id" "completed")" || internal_fail "reviewer task did not complete successfully"
log "reviewer_state=$(printf '%s' "$reviewer_json" | json_get state)"

initiative_final="$(wait_for_initiative_terminal "$initiative_id" || request GET "/initiatives/${initiative_id}")"
initiative_status="$(printf '%s' "$initiative_final" | json_get initiative.status)"
task_count="$(printf '%s' "$initiative_final" | json_get execution_summary.task_count)"
log "initiative_status=${initiative_status}"
log "initiative_task_count=${task_count}"

diff_output="$(request GET "/tasks/${coder_task_id}")"
log "coder_changed_files=$(printf '%s' "$diff_output" | json_get metadata.tool_request.path)"
log "repository_diff:"
git -C "$WORKSPACE_ROOT" diff --no-ext-diff || true
log "bridge_log_tail:"
tail -n 40 "$BRIDGE_LOG" || true

if [[ "$initiative_status" != "completed" && "$initiative_status" != "executing" ]]; then
  internal_fail "unexpected initiative status ${initiative_status}"
fi

log "Golden path bridge smoke completed"
