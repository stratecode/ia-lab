#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "Prometheus scrapes core observability and llama.cpp services" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
tasks = (root / "roles/observability/tasks/main.yml").read_text()
for job in ("grafana", "alertmanager", "llama-cpp-code"):
    if f'job_name: "{job}"' not in tasks:
        raise SystemExit(f"missing Prometheus job: {job}")
PY
  [ "$status" -eq 0 ]
}

@test "Prometheus alerts cover Grafana Alertmanager host reboots and saturation" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
tasks = (root / "roles/observability/tasks/main.yml").read_text()
required_alerts = [
    "GrafanaDown",
    "AlertmanagerDown",
    "HostRecentlyRebooted",
    "HostSwapPressure",
    "RootFilesystemCritical",
]
missing = [name for name in required_alerts if f"alert: {name}" not in tasks]
if missing:
    raise SystemExit("missing alerts: " + ", ".join(missing))
for job in ("node", "grafana", "alertmanager", "llama-cpp-code"):
    expected = f'absent(up{{job="{job}"}})'
    if expected not in tasks:
        raise SystemExit(f"{job} down alert must handle missing targets")
PY
  [ "$status" -eq 0 ]
}

@test "dedicated observability deployment validates Prometheus before restart" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
playbook = (root / "playbooks/deploy-observability.yml").read_text()
handlers = (root / "roles/observability/handlers/main.yml").read_text()
if "name: observability" not in playbook:
    raise SystemExit("dedicated observability playbook missing role")
if "promtool check config" not in handlers:
    raise SystemExit("Prometheus handler must validate config")
if "promtool check rules" not in handlers:
    raise SystemExit("Prometheus handler must validate rules")
if handlers.count("listen: Restart prometheus") != 3:
    raise SystemExit("Prometheus validation and restart handlers must share the notification")
PY
  [ "$status" -eq 0 ]
}
