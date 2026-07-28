# LocalAI Evaluation Baseline

This runbook documents the reduced lab state prepared for a future LocalAI
installation and evaluation. It describes deployed infrastructure, not the
product roadmap.

Last verified against the `lab` inventory: July 28, 2026.

## Target state

The cleanup preserves:

- Grafana
- Prometheus
- Alertmanager
- Prometheus node exporter
- Nginx and the monitoring/PCP services used by the lab console
- all four `llama.cpp` services:
  - `llama-cpp-code`
  - `llama-cpp-planner`
  - `llama-cpp-utility`
  - `llama-cpp-embeddings`
- WireGuard, including `wg0`, its systemd unit, firewall rules, keys, peer
  configuration, and UDP port `51820`
- the Route53/DDNS and baseline networking configuration needed to reach the lab
- Docker itself and the Grafana data volume

The cleanup retires:

- OpenClaw runtime, user service, and Nginx sites
- Open WebUI runtime remnants and its Docker volume
- the Go orchestrator, document/image sidecars, timers, PostgreSQL container,
  compose project, network, and live database directory
- the Codex local gateway and Codex cleanup units
- Aider services and runtime commands
- Redis, which is not required by the preserved baseline
- obsolete Prometheus file-discovery targets for the orchestrator and Codex
  gateway

The requested name was `OpenCloud`; no deployed component with that name was
found. The installed component matching the scope was OpenClaw, and that is the
component reconciled to absent.

## Reconcile the host

Load the inventory environment without printing it, then run:

```bash
set -a
source .env
set +a

ansible-playbook playbooks/cleanup-localai-baseline.yml --syntax-check
ansible-playbook playbooks/cleanup-localai-baseline.yml
```

The playbook first asserts that WireGuard, monitoring, and every `llama.cpp`
unit are active. It aborts before cleanup if a preserved service is already
unhealthy.

After cleanup it verifies:

- preserved systemd services remain active
- `wg-quick@wg0.service` remains enabled across reboots
- Grafana, Prometheus, and all four `llama.cpp` health endpoints return HTTP 200
- retired ports `5432`, `6379`, `8100`, `8180`, `8221`, `8222`, and `18789`
  are closed

Run the playbook a second time after any change. A clean reconciliation reports
`changed=0` and `failed=0`.

## Bootstrap interaction

`playbooks/bootstrap.yml` skips Aider, Orchestrator, Codex host/gateway, Open
WebUI, and OpenClaw when `retired_stack_cleanup_enabled` is true.

Export the corresponding environment flag before using bootstrap on this
reduced host:

```bash
export LAB_RETIRED_STACK_CLEANUP_ENABLED=true
ansible-playbook playbooks/bootstrap.yml
```

Running bootstrap without that flag follows the full-stack defaults and may
reinstall retired components. The dedicated cleanup playbook remains the source
of truth for the LocalAI evaluation baseline.

## Secrets, backups, and intentional residuals

The cleanup does not delete:

- `.env`, `group_vars/vault.yml`, or files under `ssh/`
- WireGuard keys or `/etc/wireguard`
- retained OpenClaw/Codex/Orchestrator configuration that may contain tokens
- TLS private keys or archived certificates
- PostgreSQL dumps and pre-cleanup archives under `/srv/ai-lab/backups`
- repository checkouts or Codex workspaces that may contain user work

The OpenClaw certificate renewal file is removed so the retired endpoint is no
longer renewed, but existing certificate/key material is retained.

These residuals are inert: no retired systemd unit, user unit, container, Nginx
site, Prometheus target, or listening application port remains. Delete retained
secrets or backups only through a separate, explicitly authorized retention
decision.

## Verified July 28, 2026

The reconciliation completed successfully against `lab`, followed by an
idempotency run with `changed=0` and `failed=0`.

Observed final state:

- only the Grafana application container remained
- WireGuard was active, enabled, and listening on UDP `51820`
- Grafana, Prometheus, and all `llama.cpp` health checks passed
- no retired unit or retired application port remained
- Nginx configuration validation passed
