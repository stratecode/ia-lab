# StrateCode Lab

Ansible-managed homelab infrastructure. Provisions and configures an Ubuntu server as a self-hosted platform for local AI inference, VPN access, observability, and basic hardening.

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
│  ┌──────────┐  ┌──────────┐  ┌────────────────────┐    │
│  │ llama.cpp│  │ WireGuard│  │ Nginx reverse proxy │    │
│  │ :8080    │  │ :51820   │  │ :443 / :80          │    │
│  │ :8081    │  └──────────┘  └────────────────────┘    │
│  └──────────┘                                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │Prometheus│  │ Grafana  │  │Alertmgr  │             │
│  │ :9090    │  │ :3000    │  │ :9093    │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│  ┌──────────┐  ┌──────────┐                            │
│  │ Cockpit  │  │ fail2ban │                            │
│  │ :9091    │  └──────────┘                            │
│  └──────────┘                                           │
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
| 5 | `monitor` | Cockpit + Nginx reverse proxy + TLS (Let's Encrypt) |
| 6 | `observability` | Prometheus, Grafana (Docker), Alertmanager, alert rules |

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
| `https://<observability_domain>` | Grafana | LAN + VPN |
| `https://<observability_domain>/prometheus/` | Prometheus | LAN + VPN |
| `http://127.0.0.1:8080/v1` | llama.cpp (code) | localhost |
| `http://127.0.0.1:8081/v1` | llama.cpp (chat) | localhost |

Domains are configured in `group_vars/all.yml` (`cockpit_domain`, `observability_domain`).

## VPN (WireGuard)

Split tunnel routing only VPN subnet (`10.66.66.0/24`) and LAN (`192.168.0.0/24`) traffic.

- Port: `51820/udp` (requires port forward on the router)
- Dynamic endpoint: configured via `route53_ddns_record_name` (updated by DDNS)
- Client profiles generated at `/etc/wireguard/clients/`

More details in [docs/wireguard.md](docs/wireguard.md).

## Local AI (llama.cpp)

Two instances with Vulkan backend (AMD GPU):

| Instance | Port | Model | Purpose |
|----------|------|-------|---------|
| `llama-cpp-code` | 8080 | Qwen2.5-Coder-3B-Instruct (Q4_K_M) | Code assistance |
| `llama-cpp-chat` | 8081 | Qwen2.5-Coder-3B-Instruct (Q4_K_M) | Chat / orchestration |

OpenAI-compatible API (`/v1/chat/completions`, `/v1/completions`).

## Observability

- **Prometheus** — system metrics via node_exporter
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

- [Getting Started](docs/getting-started.md) — full setup guide from scratch (SSH keys, vault, first run)
- [Server Baseline](docs/server-baseline.md) — base configuration details
- [WireGuard](docs/wireguard.md) — VPN setup and client guide
