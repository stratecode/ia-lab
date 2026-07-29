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
