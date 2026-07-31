#!/usr/bin/env bats

@test "SRL services use dedicated HTTPS domains through the lab proxy" {
  run python3 - <<'PY'
from pathlib import Path

group_vars = Path("group_vars/all.yml").read_text()
monitor = Path("roles/monitor/tasks/main.yml").read_text()
deploy = Path("playbooks/deploy-srl-access.yml")

required_vars = {
    "feature flag": "srl_access_enabled:",
    "dashboard domain": "srl_dashboard_domain:",
    "OpenSearch domain": "srl_opensearch_domain:",
    "dashboard loopback port": "srl_dashboard_port:",
    "OpenSearch loopback port": "srl_opensearch_port:",
}
missing_vars = [name for name, value in required_vars.items() if value not in group_vars]
if missing_vars:
    raise SystemExit("missing SRL variables: " + ", ".join(missing_vars))

required_proxy = {
    "dashboard certificate SAN": "['-d', srl_dashboard_domain]",
    "OpenSearch certificate SAN": "['-d', srl_opensearch_domain]",
    "dashboard vhost": "server_name {{ srl_dashboard_domain }};",
    "OpenSearch vhost": "server_name {{ srl_opensearch_domain }};",
    "dashboard loopback upstream": "proxy_pass http://127.0.0.1:{{ srl_dashboard_port }}",
    "OpenSearch loopback upstream": "proxy_pass http://127.0.0.1:{{ srl_opensearch_port }}",
    "LAN restriction": "allow {{ server_lan_cidr }};",
    "VPN restriction": "allow {{ wireguard_subnet }};",
}
missing_proxy = [name for name, value in required_proxy.items() if value not in monitor]
if missing_proxy:
    raise SystemExit("missing SRL proxy contract: " + ", ".join(missing_proxy))

if not deploy.exists() or "name: monitor" not in deploy.read_text():
    raise SystemExit("missing dedicated SRL access deployment playbook")
PY

  [ "$status" -eq 0 ]
}
