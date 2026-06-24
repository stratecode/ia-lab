#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "group vars define openclaw defaults and llama binding" {
  run bash -lc "cd '$repo_root' && python3 - <<'PY'
from pathlib import Path
text = Path('group_vars/all.yml').read_text()
checks = [
    'openclaw_enabled:',
    'openclaw_user:',
    'openclaw_gateway_port:',
    'openclaw_model_provider_key:',
    'openclaw_model_id:',
    'openclaw_model_provider_api:',
    'openclaw_route_via_codex_gateway:',
    'openclaw_model_base_url:',
    'openclaw_model_api_key:',
    'openclaw_model_context_window:',
    'openclaw_model_force_tool_choice:',
    'openclaw_tools_profile:',
    'openclaw_tool_search_mode:',
]
missing = [item for item in checks if item not in text]
if missing:
    raise SystemExit('missing: ' + ', '.join(missing))
if 'default(true' not in text and \"default('true'\" not in text:
    raise SystemExit('expected env-backed true default for openclaw_enabled')
if \"default('true', true) | bool\" not in text:
    raise SystemExit('expected codex gateway routing to default to true')
PY"
  [ "$status" -eq 0 ]
}

@test "bootstrap and deploy playbooks include openclaw role" {
  run bash -lc "cd '$repo_root' && python3 - <<'PY'
from pathlib import Path
bootstrap = Path('playbooks/bootstrap.yml').read_text()
deploy = Path('playbooks/deploy-orchestrator.yml').read_text()
if 'name: openclaw' not in bootstrap and '- role: openclaw' not in bootstrap:
    raise SystemExit('bootstrap missing openclaw role')
if '- openclaw' not in deploy and '- role: openclaw' not in deploy:
    raise SystemExit('deploy-orchestrator missing openclaw role')
PY"
  [ "$status" -eq 0 ]
}
