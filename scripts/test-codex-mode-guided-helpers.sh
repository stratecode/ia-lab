#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CODEX_MODE_LIBRARY=1
# shellcheck disable=SC1091
source "$ROOT_DIR/scripts/codex-mode.sh"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

assert_eq() {
  local got="$1"
  local want="$2"
  local label="$3"
  [[ "$got" == "$want" ]] || fail "$label: got <$got>, want <$want>"
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local label="$3"
  [[ "$haystack" == *"$needle"* ]] || fail "$label: missing <$needle>"
}

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

json_call='```json
{"type":"function_call","call_id":"call_1","name":"exec_command","arguments":{"cmd":"curl -s https://api.github.com/repos/laravel/framework/releases/latest | jq -r .tag_name"}}
```'
assert_eq "$(extract_pseudo_exec_command "$json_call")" \
  "curl -s https://api.github.com/repos/laravel/framework/releases/latest | jq -r .tag_name" \
  "extract markdown json function_call"

paren_call='exec_command({"cmd":"curl -s https://api.github.com/repos/laravel/framework/releases/latest | grep tag_name"})'
assert_eq "$(extract_pseudo_exec_command "$paren_call")" \
  "curl -s https://api.github.com/repos/laravel/framework/releases/latest | grep tag_name" \
  "extract parenthesized exec_command"

is_guided_supervisor_command_allowed "curl -s https://api.github.com/repos/laravel/framework/releases/latest | jq -r .tag_name" \
  || fail "expected GitHub curl+jq to be allowed"
if is_guided_supervisor_command_allowed "open https://laravel.com/docs/latest/releases"; then
  fail "open command should not be supervisor-allowed"
fi
if is_guided_supervisor_command_allowed "cat /Users/fran.lopez/.codex-lab/skills/.system/openai-docs/SKILL.md"; then
  fail "out-of-workspace cat command should not be supervisor-allowed"
fi

redirect_output="<title>Redirecting to https://laravel.com/docs/13.x/latest</title>"
observation="$(derive_guided_operational_observation "$tmp_dir" "Determine latest and migrate this project from Laravel 11" "$redirect_output" "")"
assert_contains "$observation" "known_latest_laravel_major_from_docs_redirect: 13" "docs redirect major"
assert_contains "$observation" "resolve exact laravel/framework release from GitHub" "docs redirect next action"

cat >"$tmp_dir/composer.json" <<'JSON'
{
    "require": {
        "php": "^8.3",
        "laravel/framework": "^11"
    }
}
JSON
guided_materialize_laravel_composer_constraint "$tmp_dir" "known_latest_version_from_tool_output: v13.16.1" \
  || fail "expected Laravel composer materialization"
framework_constraint="$(python3 - "$tmp_dir/composer.json" <<'PY'
import json
import sys
print(json.load(open(sys.argv[1]))["require"]["laravel/framework"])
PY
)"
assert_eq "$framework_constraint" "^13" "Laravel composer constraint"

printf 'test-codex-mode-guided-helpers ok\n'
