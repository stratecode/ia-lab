#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "Prometheus monitors router aliases on port 8080 without stale router targets" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
tasks = (root / "roles/observability/tasks/router_monitoring.yml").read_text()
if "validate: promtool check rules %s" not in tasks:
    raise SystemExit("Prometheus rules must validate before replacement")
handlers = (root / "roles/observability/handlers/main.yml").read_text()
if "promtool check config /var/snap/prometheus/current/prometheus.yml" not in handlers:
    raise SystemExit("Prometheus configuration must validate at its installed snap path before restart")
if "promtool check rules /var/snap/prometheus/current/promreg/*.yml" not in handlers:
    raise SystemExit("Prometheus rules must validate at their installed path before restart")
for alias, job in (("main", "llama-cpp-main"), ("qwen-eval", "llama-cpp-qwen-eval")):
    marker = f'job_name: "{job}"'
    if marker not in tasks:
        raise SystemExit(f"missing Prometheus job {job}")
    block = tasks.split(marker, 1)[1].split("- job_name:", 1)[0]
    if f"model: [{alias}]" not in block:
        raise SystemExit(f"job {job} must select model alias {alias}")
    if 'credentials_file: /var/snap/prometheus/current/llama-api-key' not in block:
        raise SystemExit(f"job {job} must use the managed credentials file")
    if f'alias: "{alias}"' not in block:
        raise SystemExit(f"job {job} must expose alias={alias}")
    if 'targets: ["127.0.0.1:{{ (llama_cpp_instances | first).port }}"]' not in block:
        raise SystemExit(f"job {job} must scrape the shared router endpoint")
if "8185" in tasks or "llama-cpp-router-" in tasks:
    raise SystemExit("stale router-eval targets must not be managed")
if "--collector.textfile.directory=/var/lib/prometheus/node-exporter" not in tasks:
    raise SystemExit("node-exporter must scrape the managed textfile metric directory")
node_job = tasks.split('- job_name: "node"', 1)[1].split("- job_name:", 1)[0]
if "honor_labels: true" not in node_job:
    raise SystemExit("node-exporter textfile series must retain their service instance labels")

rules = tasks.split("- alert: LlamaCppDown", 1)[1].split("- alert:", 1)[0]
if 'up{job="llama-cpp-main"}' not in rules:
    raise SystemExit("critical LlamaCppDown must track only the main router alias")
if 'llama-cpp-qwen-eval' in rules:
    raise SystemExit("an unloaded qwen-eval alias must not be part of critical down alert")
for metric_alert in ("LlamaCppMemoryHigh", "LlamaCppQueueBacklog"):
    block = tasks.split(f"- alert: {metric_alert}", 1)[1].split("- alert:", 1)[0]
    if 'instance="llama-cpp-main"' not in block:
        raise SystemExit(f"{metric_alert} must retain the main alias instance label")
PY
  [ "$status" -eq 0 ]
}

@test "key sync supports inline and file inputs without logging the key and records success" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
script = (root / "roles/lab_alerting/files/bin/sync-llama-prom-key").read_text()
for option in ("--api-key", "--api-key-file"):
    if option not in script:
        raise SystemExit(f"missing {option} support")
for contract in ("lab_llama_prom_key_in_sync", "lab_llama_prom_key_sync_timestamp_seconds", "chmod 0640"):
    if contract not in script:
        raise SystemExit(f"missing safe sync contract: {contract}")
units = root / "roles/lab_alerting/files/systemd"
timer = units / "sync-llama-prom-key.timer"
if not timer.exists():
    raise SystemExit("missing periodic key sync timer")
timer_text = timer.read_text()
if "OnBootSec=" not in timer_text or "OnUnitActiveSec=5min" not in timer_text:
    raise SystemExit("key sync timer must run at boot and every five minutes")
tasks = (root / "roles/lab_alerting/tasks/main.yml").read_text()
key_tasks = (root / "roles/lab_alerting/tasks/llama_key_sync.yml").read_text()
if "sync-llama-prom-key.timer" not in key_tasks:
    raise SystemExit("role must install and enable the periodic timer")
PY
  [ "$status" -eq 0 ]
}

@test "key sync failures publish zero and alert after ten minutes" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
rules = (root / "roles/lab_alerting/files/prometheus-rules/lab-extra-alerts.yml").read_text()
if "LlamaPromKeyOutOfSync" not in rules:
    raise SystemExit("missing key synchronization alert")
block = rules.split("- alert: LlamaPromKeyOutOfSync", 1)[1].split("- alert:", 1)[0]
if "lab_llama_prom_key_in_sync == 0" not in block:
    raise SystemExit("alert must fire for failed key synchronization")
if "for: 10m" not in block:
    raise SystemExit("key synchronization alert must wait ten minutes")
if "lab_llama_prom_key_sync_timestamp_seconds" not in rules:
    raise SystemExit("missing sync freshness metric")
if "LlamaPromKeySyncStale" not in rules:
    raise SystemExit("missing alert for a stopped or stale timer")
PY
  [ "$status" -eq 0 ]
}
