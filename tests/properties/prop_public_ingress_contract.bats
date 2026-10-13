#!/usr/bin/env bats

@test "public assistant ingress has a single exact HTTPS route and no public app proxy" {
  run python3 - <<'PY'
from pathlib import Path

group_vars = Path("group_vars/all.yml").read_text()
env_example = Path(".env.example").read_text()
monitor = Path("roles/monitor/tasks/main.yml").read_text()
route53 = Path("roles/route53_ddns/tasks/main.yml").read_text()
log_format = "log_format callback '$request_method $uri $status';"

required = {
    "assistant domain variable": "lab_assistant_domain:",
    "example environment variable": "LAB_ASSISTANT_DOMAIN=assistant.stratecode.com",
    "certificate SAN": "'-d', lab_assistant_domain",
    "assistant vhost": "server_name {{ lab_assistant_domain }};",
    "webhook loopback upstream": "proxy_pass http://127.0.0.1:{{ lab_secretary_port }}/telegram/webhook;",
    "webhook POST restriction": "limit_except POST",
    "Jira OAuth loopback upstream": "proxy_pass http://127.0.0.1:18765/callback;",
    "Granola OAuth loopback upstream": "proxy_pass http://127.0.0.1:18766/callback;",
    "unknown path rejection": "location / {\n              return 404;",
    "assistant CNAME target": "route53_assistant_cname_target:",
    "canonical CNAME target": "regex_replace('\\\\.$', '')) ~ '.'",
    "conflicting DNS type check": "route53_assistant_existing_types",
    "vault credentials guard": "route53_ddns_has_managed_credentials | default(false)",
    "host credential probe": "- sts\n      - get-caller-identity",
}
sources = {
    "assistant domain variable": group_vars,
    "example environment variable": env_example,
    "certificate SAN": monitor,
    "assistant vhost": monitor,
    "webhook loopback upstream": monitor,
    "webhook POST restriction": monitor,
    "Jira OAuth loopback upstream": monitor,
    "Granola OAuth loopback upstream": monitor,
    "unknown path rejection": monitor,
    "assistant CNAME target": group_vars,
    "canonical CNAME target": group_vars,
    "conflicting DNS type check": route53,
    "vault credentials guard": route53,
    "host credential probe": route53,
}
missing = [name for name, needle in required.items() if needle not in sources[name]]
if missing:
    raise SystemExit("public ingress contract missing: " + ", ".join(missing))

if "assistant.stratecode.com" not in route53 and "route53_assistant_cname_target" not in route53:
    raise SystemExit("Route53 role does not reconcile the assistant alias")

assistant_name = monitor.find("server_name {{ lab_assistant_domain }};")
assistant_start = monitor.rfind("      server {", 0, assistant_name)
assistant_end = monitor.find("      server {", assistant_name)
assistant_block = monitor[assistant_start:assistant_end if assistant_end >= 0 else len(monitor)]
if "[::]:443" in assistant_block:
    raise SystemExit("assistant vhost must not add an IPv6 listener")

if log_format not in monitor:
    raise SystemExit("callback access logs must contain only method, normalized path, and status")

if "proxy_pass http://127.0.0.1:{{ open_webui_port }}" in assistant_block:
    raise SystemExit("assistant public vhost must not proxy to Open WebUI")
PY

  [ "$status" -eq 0 ]
}
