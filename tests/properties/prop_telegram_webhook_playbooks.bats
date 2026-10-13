#!/usr/bin/env bats

@test "Telegram webhook activation and rollback preserve exclusive transport and updates" {
  run python3 - <<'PY'
from pathlib import Path
import yaml

activate_path = Path("playbooks/enable-telegram-webhook.yml")
rollback_path = Path("playbooks/restore-telegram-polling.yml")
if not activate_path.exists() or not rollback_path.exists():
    raise SystemExit("explicit Telegram activation and rollback playbooks are required")

activate = yaml.safe_load(activate_path.read_text())[0]
rollback = yaml.safe_load(rollback_path.read_text())[0]
group_vars = Path("group_vars/all.yml").read_text()
secretary_role = Path("roles/lab_secretary/tasks/main.yml").read_text()
env_example = Path(".env.example").read_text()

if "default('polling', true)" not in group_vars or "LAB_SECRETARY_TELEGRAM_MODE=polling" not in env_example:
    raise SystemExit("normal deployment must remain in polling mode")
if "telegram_public_probe_passed: false" not in activate_path.read_text():
    raise SystemExit("webhook activation must require a successful external probe")
pre_tasks = activate.get("pre_tasks", [])
if not any("telegram_public_probe_passed | bool" in str(task) for task in pre_tasks):
    raise SystemExit("activation has no external-reachability gate")
if "webhook" != activate.get("vars", {}).get("lab_secretary_telegram_mode"):
    raise SystemExit("activation does not select webhook mode")
if activate.get("vars", {}).get("lab_secretary_voice_enabled") is not True:
    raise SystemExit("webhook activation must preserve the approved voice path")
activate_roles = [item if isinstance(item, str) else item.get("role", "") for item in activate.get("roles", [])]
if "lab_speech" not in activate_roles or activate_roles.index("lab_speech") > activate_roles.index("lab_secretary"):
    raise SystemExit("webhook activation must install speech services before the secretary")
if "polling" != rollback.get("vars", {}).get("lab_secretary_telegram_mode"):
    raise SystemExit("rollback does not select polling mode")

tasks = activate.get("tasks", [])
names = [task.get("name", "") for task in tasks]
required_order = [
    "Persist a synthetic unauthorized update before acknowledging it",
    "Confirm duplicate delivery is acknowledged without a second update",
    "Register Telegram webhook without dropping pending updates",
    "Read Telegram webhook metadata",
]
positions = [names.index(name) for name in required_order if name in names]
if len(positions) != len(required_order) or positions != sorted(positions):
    raise SystemExit("activation must verify persistence and deduplication before setWebhook")
if "drop_pending_updates: \"false\"" not in activate_path.read_text():
    raise SystemExit("activation must preserve Telegram pending updates")
if "drop_pending_updates: \"false\"" not in rollback_path.read_text():
    raise SystemExit("rollback must preserve Telegram pending updates")

secret_tasks = [task for task in tasks if task.get("no_log") is True]
if len(secret_tasks) < 7:
    raise SystemExit("secret-bearing activation tasks are not consistently hidden")
rollback_tasks = rollback.get("tasks", [])
delete_index = next((i for i, task in enumerate(rollback_tasks) if "deleteWebhook" in str(task)), -1)
deploy_index = next((i for i, task in enumerate(rollback_tasks) if any(key.endswith(".import_role") or key == "import_role" for key in task)), -1)
if delete_index < 0 or deploy_index < 0 or delete_index >= deploy_index:
    raise SystemExit("rollback must remove webhook before resuming polling")

if "openssl rand -hex 32" not in secretary_role or "no_log: true" not in secretary_role:
    raise SystemExit("webhook secret must be generated privately on the host")
if "LAB_SECRETARY_TELEGRAM_MODE={{ lab_secretary_telegram_mode }}" not in secretary_role:
    raise SystemExit("secretary role does not deploy the selected Telegram mode")
if "EnvironmentFile=/etc/lab-secretary/telegram-webhook.env" not in secretary_role:
    raise SystemExit("secretary service does not load the private webhook secret")

for deployment in ("playbooks/deploy-lab-surface.yml", "playbooks/deploy-principal-model.yml"):
    play = yaml.safe_load(Path(deployment).read_text())[0]
    if play.get("vars", {}).get("lab_secretary_voice_enabled") is not True:
        raise SystemExit(f"{deployment} must preserve the approved Telegram voice path")
    role_names = [item if isinstance(item, str) else item.get("role", "") for item in play.get("roles", [])]
    if "lab_speech" not in role_names or role_names.index("lab_speech") > role_names.index("lab_secretary"):
        raise SystemExit(f"{deployment} must install speech services before the secretary")
PY

  [ "$status" -eq 0 ]
}
