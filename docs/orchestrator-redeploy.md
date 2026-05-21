# Orchestrator Redeploy Runbook

This runbook covers a clean redeploy of the Phase 4 orchestrator from repo state only. If you feel tempted to edit `site-packages` on the host, stop. That path leads to archaeology and regret.

## Preconditions

- `.env` exported on the control machine:

```bash
set -a && source .env && set +a
```

- Vault password file available through `ANSIBLE_VAULT_PASSWORD_FILE`
- SSH access to the target host using `ssh/lab`
- Required secrets populated:
  - `LAB_ORCHESTRATOR_CLEANUP_API_KEY`
  - `LAB_TELEGRAM_BOT_TOKEN`
  - `LAB_TELEGRAM_ALLOWED_USERS`
  - `LAB_POSTGRES_PASSWORD`

## Static validation

Run these checks before touching the host:

```bash
./scripts/test-orchestrator-go.sh
ansible-playbook playbooks/bootstrap.yml --syntax-check
```

## Deploy Phase 4 components

For the full stack:

```bash
ansible-playbook playbooks/bootstrap.yml
```

For the Phase 4 slice only:

```bash
ansible-playbook /tmp/deploy-orchestrator-phase4.yml
```

That subset playbook should import:

- `orchestrator`
- `open_webui`
- `monitor`
- `observability`

## Post-switch verification

Verify host state through Ansible:

```bash
ansible all -i inventory.yml -b -m shell -a 'systemctl is-active orchestrator.service orchestrator-workspace-cleanup.timer orchestrator-pg-backup.timer snap.prometheus.prometheus.service nginx.service'

ansible all -i inventory.yml -b -m shell -a 'curl -sf http://127.0.0.1:8100/health'
ansible all -i inventory.yml -b -m shell -a 'curl -sf http://127.0.0.1:8100/metrics | head'

ansible all -i inventory.yml -b -m shell -a 'systemctl start orchestrator-workspace-cleanup.service && systemctl start orchestrator-pg-backup.service'
ansible all -i inventory.yml -b -m shell -a 'systemctl status orchestrator-workspace-cleanup.service orchestrator-pg-backup.service --no-pager'

ansible all -i inventory.yml -b -m shell -a 'curl -sk --resolve ${LAB_COCKPIT_DOMAIN}:443:127.0.0.1 https://${LAB_COCKPIT_DOMAIN}${LAB_ORCHESTRATOR_PROXY_PATH}health'
ansible all -i inventory.yml -b -m shell -a 'curl -skI --resolve ${LAB_CHAT_DOMAIN}:443:127.0.0.1 https://${LAB_CHAT_DOMAIN}/'
```

## Telegram verification

Send a real message using the bot token:

```bash
curl -s -X POST "https://api.telegram.org/bot${LAB_TELEGRAM_BOT_TOKEN}/sendMessage" \
  -d "chat_id=$(echo "$LAB_TELEGRAM_ALLOWED_USERS" | cut -d, -f1)" \
  --data-urlencode "text=Fase 4 redeploy verification OK"
```

Then confirm interactively from Telegram:

```text
/status
/tasks
/coder dime si el orquestador está vivo
```

## Rollback

If the new deploy fails, keep rollback boring:

1. Restore the previous backup tarball from `/srv/ai-lab/backups/`
2. Restore the previous Go runtime binary and the sidecar environment snapshot if required
3. Restore the previous unit files in `/etc/systemd/system/`
4. Restart `orchestrator.service`
5. Confirm `/health`, cleanup timer, and backup timer again

Do not mix partial rollback with fresh code. Hybrid zombies are harder to reason about than clean failure.
