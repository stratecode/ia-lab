#!/usr/bin/env bats

@test "monitor role gives the unattended Certbot service its Route53 profile" {
  run python3 - <<'PY'
from pathlib import Path

tasks = Path("roles/monitor/tasks/main.yml").read_text()

required = {
    "systemd drop-in directory": "/etc/systemd/system/certbot.service.d",
    "managed service override": "/etc/systemd/system/certbot.service.d/route53.conf",
    "Route53 profile": 'Environment="AWS_PROFILE={{ monitor_letsencrypt_aws_profile }}"',
    "shared credentials path": 'Environment="AWS_SHARED_CREDENTIALS_FILE=/root/.aws/credentials"',
    "AWS config path": 'Environment="AWS_CONFIG_FILE=/root/.aws/config"',
}

missing = [name for name, setting in required.items() if setting not in tasks]
if missing:
    raise SystemExit(
        "unattended Certbot is missing AWS configuration: " + ", ".join(missing)
    )
PY

  [ "$status" -eq 0 ]
}
