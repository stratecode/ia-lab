#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${LAB_ORCHESTRATOR_BASE_URL:-http://127.0.0.1:8100}"
API_KEY="${LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY:-${LAB_AGENT_API_KEY:-}}"
WORKSPACE_ROOT="${1:-$(mktemp -d /tmp/lab-semantic-runtime-XXXXXX)}"

if [[ -z "$API_KEY" ]]; then
  echo "LAB_ORCHESTRATOR_OPEN_WEBUI_API_KEY or LAB_AGENT_API_KEY is required" >&2
  exit 2
fi

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
  python3 -c 'import json,sys; print(json.load(sys.stdin).get(sys.argv[1], ""))' "$1"
}

transition_task() {
  local task_id="$1"
  local state="$2"
  request PATCH "/tasks/$task_id" "{\"state\":\"$state\",\"reason\":\"semantic runtime smoke\"}" >/dev/null
}

mkdir -p "$WORKSPACE_ROOT"

trusted_task="$(request POST /tasks/ "{\"description\":\"Semantic smoke trusted context: preserve approvals and deterministic tests\",\"metadata\":{\"workspace_root\":\"$WORKSPACE_ROOT\",\"constraints\":[\"preserve approvals\",\"tests must be deterministic\"]},\"assigned_agent\":\"reviewer\",\"execution_target\":\"local\"}")"
trusted_id="$(printf '%s' "$trusted_task" | json_get id)"
transition_task "$trusted_id" assigned
transition_task "$trusted_id" in_progress
transition_task "$trusted_id" completed

failed_task="$(request POST /tasks/ "{\"description\":\"Semantic smoke failed context: deployment failed because LAB_REQUIRED_ENV was missing\",\"metadata\":{\"workspace_root\":\"$WORKSPACE_ROOT\",\"failure_reason\":\"LAB_REQUIRED_ENV missing\",\"validation_rules\":[\"LAB_REQUIRED_ENV must exist before deploy\"]},\"assigned_agent\":\"reviewer\",\"execution_target\":\"local\"}")"
failed_id="$(printf '%s' "$failed_task" | json_get id)"
transition_task "$failed_id" assigned
transition_task "$failed_id" in_progress
transition_task "$failed_id" failed

sleep 1

context_payload="$(request POST /context/build "{\"agent_type\":\"planner\",\"workspace_root\":\"$WORKSPACE_ROOT\",\"task_description\":\"Plan a deployment preserving approvals and avoiding missing env failures\",\"metadata\":{\"mode\":\"PLAN_MODE\"},\"output_format\":\"operational_ir\",\"outcomes\":[\"trusted\",\"failed\",\"rejected\",\"invalid\"],\"include_failed\":true,\"include_rejected\":true,\"max_chunks\":8,\"max_chars\":6000}")"
context_file="$(mktemp /tmp/lab-semantic-context-XXXXXX.json)"
printf '%s' "$context_payload" > "$context_file"

python3 - "$context_file" <<'PY'
import json, sys
with open(sys.argv[1], "r", encoding="utf-8") as handle:
    payload = json.load(handle)
ir = payload.get("operational_ir") or {}
trusted = ir.get("trusted") or []
invalid = ir.get("invalid") or []
if not trusted:
    raise SystemExit("expected at least one trusted context item")
if not invalid:
    raise SystemExit("expected at least one invalid/failed context item")
print(json.dumps({
    "workspace_root": payload.get("workspace_root"),
    "trusted": len(trusted),
    "invalid": len(invalid),
    "source_refs": ir.get("source_refs", []),
}, indent=2))
PY
