#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "LocalAI defaults are opt-in pinned and loopback-only" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
vars_text = (root / "group_vars/all.yml").read_text()
env_text = (root / ".env.example").read_text()
bootstrap = (root / "playbooks/bootstrap.yml").read_text()

required = [
    "localai_enabled:",
    "localai_image:",
    "localai_bind_address:",
    "localai_port:",
    "localai_domain:",
    "localai_api_key:",
    "localai_models_dir:",
    "localai_config_dir:",
    "localai_data_dir:",
    "localai_default_model:",
]
missing = [value for value in required if value not in vars_text]
if missing:
    raise SystemExit("missing vars: " + ", ".join(missing))
if "LAB_LOCALAI_ENABLED') | default('false', true) | bool" not in vars_text:
    raise SystemExit("LocalAI must default disabled")
if "LAB_LOCALAI_BIND_ADDRESS') | default('127.0.0.1', true)" not in vars_text:
    raise SystemExit("LocalAI must default to loopback")
image_line = next(line for line in vars_text.splitlines() if line.startswith("localai_image:"))
if ":latest" in image_line:
    raise SystemExit("LocalAI image must be pinned")
if "name: localai" not in bootstrap:
    raise SystemExit("bootstrap missing localai role")
if "when: localai_enabled" not in bootstrap:
    raise SystemExit("bootstrap LocalAI role must be opt-in")
if "LAB_LOCALAI_API_KEY=replace-with-random-hex" not in env_text:
    raise SystemExit("example API key placeholder missing")
PY
  [ "$status" -eq 0 ]
}

@test "LocalAI role defines an authenticated persistent loopback runtime" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
tasks = (root / "roles/localai/tasks/main.yml").read_text()
environment = (root / "roles/localai/templates/localai.env.j2").read_text()

required_tasks = [
    "Validate LocalAI configuration",
    "Ensure LocalAI persistent directories exist",
    "Install LocalAI runtime environment",
    "Inspect existing LocalAI container",
    "Remove drifted LocalAI container",
    "Reconcile LocalAI container",
    "Wait for LocalAI runtime readiness",
    "Install LocalAI default model",
    "Wait for LocalAI default model",
]
missing = [value for value in required_tasks if value not in tasks]
if missing:
    raise SystemExit("missing runtime behavior: " + ", ".join(missing))
if "localai_bind_address == '127.0.0.1'" not in tasks:
    raise SystemExit("loopback assertion missing")
if "localai_api_key | length >= 24" not in tasks:
    raise SystemExit("strong API key assertion missing")
if '"{{ localai_models_dir }}:/models"' not in tasks:
    raise SystemExit("persistent model mount missing")
if "Authorization" not in tasks or "Bearer {{ localai_api_key }}" not in tasks:
    raise SystemExit("authenticated readiness missing")
if "LOCALAI_API_KEY={{ localai_api_key }}" not in environment:
    raise SystemExit("runtime API key missing")
PY
  [ "$status" -eq 0 ]
}

@test "LocalAI HTTPS is private and cleanup retains persistent data" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
nginx = (root / "roles/localai/templates/localai-nginx.conf.j2").read_text()
tasks = (root / "roles/localai/tasks/main.yml").read_text()
handlers = (root / "roles/localai/handlers/main.yml").read_text()
cleanup = (root / "playbooks/cleanup-localai-baseline.yml").read_text()

nginx_checks = [
    "server_name {{ localai_domain }};",
    "allow 127.0.0.1;",
    "allow {{ server_lan_cidr }};",
    "allow {{ docker_internal_cidr }};",
    "allow {{ wireguard_subnet }};",
    "deny all;",
    "proxy_pass http://{{ localai_bind_address }}:{{ localai_port }};",
    "proxy_buffering off;",
    "proxy_request_buffering off;",
    "proxy_read_timeout 300s;",
]
missing = [value for value in nginx_checks if value not in nginx]
if missing:
    raise SystemExit("missing nginx behavior: " + ", ".join(missing))
if "Generate self-signed certificate for LocalAI" not in tasks:
    raise SystemExit("certificate generation missing")
if "Install LocalAI nginx site" not in tasks:
    raise SystemExit("nginx installation missing")
if "nginx -t" not in handlers:
    raise SystemExit("safe nginx validation missing")
if "Remove LocalAI evaluation container" not in cleanup:
    raise SystemExit("cleanup does not remove LocalAI runtime")
if "/etc/nginx/sites-available/localai.conf" not in cleanup:
    raise SystemExit("cleanup does not remove LocalAI nginx site")
if "/srv/ai-lab/localai/models" in cleanup:
    raise SystemExit("cleanup must retain LocalAI models")
PY
  [ "$status" -eq 0 ]
}

@test "LocalAI verifier checks auth readiness chat and tool calls" {
  fixture_port=18991
  python3 "$repo_root/tests/fixtures/localai_api_fixture.py" "$fixture_port" \
    >"$BATS_TEST_TMPDIR/localai-fixture.log" 2>&1 &
  fixture_pid=$!
  trap 'kill "$fixture_pid" 2>/dev/null || true' EXIT

  for _ in {1..20}; do
    if curl -s "http://127.0.0.1:$fixture_port/readyz" >/dev/null; then
      break
    fi
    sleep 0.1
  done

  run env \
    LAB_LOCALAI_API_KEY=test-localai-api-key-123456 \
    LAB_LOCALAI_DEFAULT_MODEL=qwen3-4b \
    "$repo_root/scripts/verify-localai.sh" \
    --base-url "http://127.0.0.1:$fixture_port"

  kill "$fixture_pid" 2>/dev/null || true
  wait "$fixture_pid" 2>/dev/null || true

  [ "$status" -eq 0 ]
  [[ "$output" == *"authentication: ok"* ]]
  [[ "$output" == *"readiness: ok"* ]]
  [[ "$output" == *"model: ok"* ]]
  [[ "$output" == *"chat: ok"* ]]
  [[ "$output" == *"tool-call: ok"* ]]
}
