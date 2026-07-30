#!/usr/bin/env bats

@test "observability backends bind to loopback and single-node Alertmanager disables clustering" {
  run python3 - <<'PY'
from pathlib import Path

tasks = Path("roles/observability/tasks/main.yml").read_text()

required = {
    "Prometheus loopback binding": "--web.listen-address=127.0.0.1:9090",
    "Alertmanager loopback binding": "--web.listen-address=127.0.0.1:{{ alertmanager_port }}",
    "Alertmanager clustering disabled": "--cluster.listen-address=",
    "node-exporter loopback binding": "--web.listen-address=127.0.0.1:9100",
}

missing = [name for name, flag in required.items() if flag not in tasks]
if missing:
    raise SystemExit("missing hardened observability settings: " + ", ".join(missing))
PY

  [ "$status" -eq 0 ]
}

@test "Grafana reaches loopback-only Prometheus through the authenticated Nginx endpoint" {
  run python3 - <<'PY'
from pathlib import Path

tasks = Path("roles/observability/tasks/main.yml").read_text()

if 'url: https://{{ observability_domain }}/prometheus' not in tasks:
    raise SystemExit("Grafana datasource must use the Nginx HTTPS endpoint")
if '"{{ observability_domain }}:host-gateway"' not in tasks:
    raise SystemExit("Grafana container must resolve the observability domain to the host gateway")
if "url: http://host.docker.internal:9090/prometheus" in tasks:
    raise SystemExit("Grafana must not bypass Nginx to a private Prometheus port")
PY

  [ "$status" -eq 0 ]
}
