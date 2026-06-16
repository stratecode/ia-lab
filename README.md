# StrateCode Lab

Ansible-managed homelab infrastructure. Provisions and configures an Ubuntu server as a self-hosted platform for local AI inference, orchestrated task execution, VPN access, observability, Open WebUI, and basic hardening.

## Prerequisites

### Control machine (where you run Ansible)

- Python 3.10+
- Ansible core 2.15+ with the `community.general` collection
- SSH access to the target host (keypair in `ssh/lab`)
- Vault password file — path configured via `ANSIBLE_VAULT_PASSWORD_FILE` in `.env` (defaults to `~/.config/ia-lab/ansible-vault-pass`)
- `.env` file with environment configuration (copy from `.env.example`)

### Target host

- **Ubuntu Server 22.04 or 24.04** — the playbooks use `apt`, `systemd`, `netplan`, and LVM tooling specific to Debian/Ubuntu. Other distributions are not supported.
- AMD GPU with Vulkan drivers (for llama.cpp inference). NVIDIA/CUDA is not currently configured.
- At least 16 GB RAM recommended (AI models + Docker services)
- AWS CLI profile `route53-dns` configured for DDNS and Let's Encrypt DNS challenge (only needed if `route53_ddns_enabled` and `monitor_tls_mode` are active)

## Quick start

```bash
# Copy and fill in environment configuration
cp .env.example .env
# Edit .env with your values

# Load environment variables
set -a && source .env && set +a

# Test connectivity
ansible all -m ping

# Run the full playbook
ansible-playbook playbooks/bootstrap.yml

# Dry-run with diff
ansible-playbook playbooks/bootstrap.yml --check --diff

# Run a single role
ansible-playbook playbooks/bootstrap.yml --tags wireguard
```

## Architecture

A single Ubuntu Server host on a home LAN with an AMD GPU (Vulkan).

```
┌─────────────────────────────────────────────────────────┐
│  lab host                                               │
│                                                         │
│  ┌────────────────────────────┐  ┌────────────────────┐ │
│  │ llama.cpp                  │  │ Nginx reverse proxy│ │
│  │ :8080 :8082 :8083 :8084   │  │ :443 / :80         │ │
│  └────────────────────────────┘  └────────────────────┘ │
│  ┌──────────────┐  ┌──────────┐  ┌──────────────────┐  │
│  │ Orchestrator │  │ WireGuard│  │ Open WebUI       │  │
│  │ :8100        │  │ :51820   │  │ :3001            │  │
│  └──────────────┘  └──────────┘  └──────────────────┘  │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │Prometheus│  │ Grafana  │  │Alertmgr  │              │
│  │ :9090    │  │ :3000    │  │ :9093    │              │
│  └──────────┘  └──────────┘  └──────────┘              │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │ Cockpit  │  │ PostgreSQL│ │ fail2ban │              │
│  │ :9091    │  │ Docker    │ └──────────┘              │
│  └──────────┘  └──────────┘                             │
└─────────────────────────────────────────────────────────┘
```

## Roles

Roles are applied in dependency order:

| # | Role | Description |
|---|------|-------------|
| 1 | `server_baseline` | Base packages, Docker, fail2ban, LVM, networking, node_exporter |
| 2 | `route53_ddns` | Dynamic DNS update via Route53 (systemd timer) |
| 3 | `wireguard` | WireGuard VPN server + client profiles |
| 4 | `llama_cpp` | Build llama.cpp with Vulkan, systemd services |
| 5 | `aider` | Aider task runtime and shell wrappers |
| 6 | `orchestrator` | Go control plane, PostgreSQL, Python capability sidecars, timers, Telegram bot |
| 7 | `open_webui` | Chat UI connected to local llama.cpp endpoints |
| 8 | `monitor` | Cockpit + Nginx reverse proxy + TLS (Let's Encrypt) |
| 9 | `observability` | Prometheus, Grafana (Docker), Alertmanager, alert rules |

## Project structure

```
.
├── ansible.cfg              # Ansible configuration
├── inventory.yml            # Inventory (single host)
├── .env.example             # Environment variables template
├── .env                     # Real environment values (git-ignored)
├── group_vars/
│   ├── all.yml              # Global variables (reads from env vars)
│   └── vault.yml            # Encrypted secrets (ansible-vault)
├── host_vars/
│   └── lab.yml              # Host overrides (reads from env vars)
├── playbooks/
│   └── bootstrap.yml        # Main playbook
├── roles/                   # One directory per role (tasks + handlers)
├── docs/                    # Per-subsystem documentation
└── ssh/                     # SSH keypair (git-ignored)
```

## Exposed services

| URL | Service | Access |
|-----|---------|--------|
| `https://<cockpit_domain>` | Cockpit | LAN + VPN |
| `https://<cockpit_domain>/orchestrator/health` | Orchestrator API | LAN + VPN |
| `https://<observability_domain>` | Grafana | LAN + VPN |
| `https://<observability_domain>/prometheus/` | Prometheus | LAN + VPN |
| `https://<chat_domain>` | Open WebUI | LAN + VPN |
| `http://127.0.0.1:8080/v1` | llama.cpp (code) | localhost |
| `http://127.0.0.1:8082/v1` | llama.cpp (planner) | localhost |
| `http://127.0.0.1:8083/v1` | llama.cpp (utility) | localhost |
| `http://127.0.0.1:8084/v1` | llama.cpp (embeddings) | localhost |

Domains are configured in `group_vars/all.yml` (`cockpit_domain`, `observability_domain`, `open_webui_domain`).

## VPN (WireGuard)

Split tunnel routing only VPN subnet (`10.66.66.0/24`) and LAN (`192.168.0.0/24`) traffic.

- Port: `51820/udp` (requires port forward on the router)
- Dynamic endpoint: configured via `route53_ddns_record_name` (updated by DDNS)
- Client profiles generated at `/etc/wireguard/clients/`

More details in [docs/wireguard.md](docs/wireguard.md).

## Local AI (llama.cpp)

Four instances with Vulkan backend (AMD GPU/CPU mix depending on model):

| Instance | Port | Model | Purpose |
|----------|------|-------|---------|
| `llama-cpp-code` | 8080 | Qwen2.5-Coder-7B-Instruct (GGUF) | Code assistance |
| `llama-cpp-planner` | 8082 | Qwen2.5-3B-Instruct (GGUF) | Planning / reasoning |
| `llama-cpp-utility` | 8083 | Qwen2.5-1.5B-Instruct (GGUF) | Lightweight utility tasks |
| `llama-cpp-embeddings` | 8084 | nomic-embed-text-v1.5 (GGUF) | Embeddings |

OpenAI-compatible API (`/v1/chat/completions`, `/v1/completions`).

## Orchestrator

The platform now runs a Go orchestrator on `127.0.0.1:8100` with:

- PostgreSQL persistence in Docker
- Redis-backed queue/event bus
- initiative lifecycle above tasks (`idea -> requirements -> design -> plan -> selective execution`)
- worker loop integrated with `aider-task`
- Telegram bot for status, approvals, model chat, and constrained server ops
- cleanup and database backup timers
- Prometheus metrics at `/metrics`
- Python sidecars only for document and image capabilities

Deployment now includes the minimum PostgreSQL compatibility patch required by the current Go runtime, including task archiving support. Bootstrap is expected to be reproducible without manual SQL after `ansible-playbook playbooks/bootstrap.yml`.

The canonical deployment path is the `orchestrator` role plus the Go binary built from `cmd/orchestrator-go`. The remaining Python runtime is intentionally narrow and limited to `src/cap_sidecars/` because PDF/DOCX/OCR tooling is still better there than in a performative rewrite.

For the official product direction, validated workflow, and next implementation priorities, use the [Master Plan](docs/architecture/master-plan.md). This README summarizes the repository and deployment shape; it is not the roadmap authority.

## Observability

- **Prometheus** — system metrics via node_exporter
- **Orchestrator metrics** — API and worker metrics scraped from `127.0.0.1:8100/metrics`
- **Grafana** — dashboards (Docker, port 3000)
- **Alertmanager** — Slack alerts (optional, requires webhook in vault)
- **PCP** — Performance Co-Pilot for advanced metrics
- **Cockpit** — web administration console

Configured alerts: node down, high CPU, high memory, root filesystem full, disk I/O pressure, high temperature.

## Secrets management

```bash
# View secrets
ansible-vault view group_vars/vault.yml

# Edit secrets
ansible-vault edit group_vars/vault.yml
```

Sensitive variables in the vault:
- `vault_grafana_admin_password`
- `vault_alertmanager_slack_webhook_url`
- AWS credentials for Route53
- WireGuard keys

## Additional documentation

- [Master Plan](docs/architecture/master-plan.md) — canonical product direction, MVP boundaries, and roadmap
- [Codex Agentic Workflow](docs/codex-agentic-workflow.md) — bootstrap Codex against the lab gateway and work on this repo with repo-local instructions
- [Getting Started](docs/getting-started.md) — full setup guide from scratch (SSH keys, vault, first run)
- [System Usage Guide](docs/system-usage.md) — Telegram, Open WebUI, and orchestrator API usage
- [Orchestrator Go Runtime](docs/orchestrator-go-runtime.md) — Go runtime architecture, build flow, and deployment model
- [Local Bridge and TUI/CLI](docs/local-bridge.md) — install, operate, and troubleshoot the local execution bridge and initiative workflow
- [Server Baseline](docs/server-baseline.md) — base configuration details
- [WireGuard](docs/wireguard.md) — VPN setup and client guide
