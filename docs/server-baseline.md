# Server Baseline

This lab bootstraps the server with a baseline suitable for running local AI services and supporting infrastructure.

## What it configures

- Expands the root logical volume to consume all free space in `ubuntu-vg`.
- Installs operational packages:
  - `docker.io`, `docker-compose-v2`
  - `prometheus-node-exporter`
  - `smartmontools`, `lm-sensors`, `nvme-cli`
  - `fail2ban`
  - `mesa-vulkan-drivers`, `vulkan-tools`
  - `ethtool`, `tcpdump`, `acl`, `bash-completion`
- Configures Docker with:
  - data root at `/opt/docker`
  - log rotation (`10m`, `3` files)
- Creates:
  - `/srv/ai-lab`
  - `/srv/containers`
  - `/opt/docker`
- Enables and starts:
  - `docker`
  - `prometheus-node-exporter`
  - `smartmontools`
  - `fail2ban`
- Adds the primary user to the `docker` group.
- Configures `smartd` to scan disks automatically.
- Configures `fail2ban` for `sshd`.
- Configures Prometheus to scrape `node_exporter` on `localhost:9100`.
- Adds a netplan overlay that prefers wired Ethernet over Wi-Fi.
- Installs `Cockpit`, `Grafana`, `Prometheus`, and `Alertmanager`.
- Splits access by hostname:
  - `<cockpit_domain>` -> Cockpit
  - `<observability_domain>` -> Grafana
  - `<observability_domain>/prometheus/` -> Prometheus
- Installs `llama.cpp` with Vulkan as the local inference backend.
- Installs `Tailscale` for remote private access to the lab.
- Provisions a baseline Grafana dashboard and Prometheus alert rules.

## Network behavior

The netplan overlay is written to `/etc/netplan/90-lab-network.yaml`.

Interface preference:

- `enp3s0` (Intel I226-V): metric `100`
- `eno1` (Realtek): metric `110`
- `wlp2s0` (Wi-Fi): metric `600`

Result:

- While no cable is connected, Wi-Fi remains active.
- Once a cable is connected, the Intel NIC should win routing priority automatically.

## Storage result

The root filesystem is expected to grow from about `100G` to the full LVM capacity of the disk.

## DDNS toggle

Route53 DDNS is disabled by default and is controlled by:

```bash
ansible-playbook playbooks/bootstrap.yml
```

If disabled, `route53-ddns.timer` is stopped and disabled.

## Secrets and vault

Secrets are loaded from:

- `group_vars/vault.yml` (encrypted with `ansible-vault`)

The vault password file is configured in `ansible.cfg` and stored locally at:

- `~/.config/ia-lab/ansible-vault-pass` (default, configurable via `ANSIBLE_VAULT_PASSWORD_FILE` in `.env`)

Sensitive values moved to vault:

- Grafana admin password
- AWS Route53 credentials
- Optional Alertmanager Slack webhook URL

Useful commands:

```bash
ansible-vault view group_vars/vault.yml
ansible-vault edit group_vars/vault.yml
```

## Observability and gateway

Grafana runs in Docker and stores its admin password in:

- `/srv/ai-lab/observability/grafana.env`

Prometheus alert rules are stored in:

- `/var/snap/prometheus/current/promreg/lab-alerts.yml`

Current alert coverage:

- node down
- high CPU
- high memory
- root filesystem high usage
- disk I/O pressure
- high temperature

`OpenClaw` is part of the current lab surface again, but as a complementary
gateway/UI layered over the local `llama.cpp` code model and the
orchestrator-led runtime. Treat its configuration as Ansible-managed host
state, not manual drift.

The host exposes local `llama.cpp` endpoints for testing:

- `http://127.0.0.1:8080/v1` for code
- `http://127.0.0.1:8081/v1` for chat

Alertmanager is installed and active, but notifications are only sent if a webhook URL is set in vault:

```yaml
vault_alertmanager_slack_webhook_url: "https://hooks.slack.com/services/..."
```

This can be used for:

- Slack incoming webhooks

## Operational notes

- If you want the monitor to be truly local-only, also remove or disable inbound `443`/`80` forwarding on the router. Disabling DDNS alone does not guarantee isolation if your public IP does not change.
- Docker group membership applies to new login sessions. Reconnect your shell after the playbook if you want to run `docker` as the non-root user.
- `prometheus-node-exporter` is scraped by the local Prometheus instance.
- The `llama.cpp` units are configured to restart when the compiled binary or unit definition changes, so context-size or runtime tuning updates actually reach the running processes instead of staying trapped in systemd files like decorative lies.
- On Ryzen mini PCs with `amd-pstate-epp`, compiling `llama.cpp` with aggressive parallelism can push the host into sustained `85–90C` territory and trigger hard reboots. Treat `llama_cpp_build_jobs` as a host-specific thermal limit, not as “number of cores minus superstition”.
- For thermally constrained hosts, use CPU tuning before heavy builds and prefer `llama_cpp_build_jobs=1` as the safe baseline. `2` and above must be revalidated on that exact machine under real cooling conditions.
- If a build is interrupted by reboot, assume the source checkout and `build/bin` artifacts may be corrupted. Purge `/opt/llama.cpp/build` and, if Git objects are damaged, `/opt/llama.cpp/src` before retrying.

## Security note

Secrets are no longer meant to live in plaintext Ansible vars. Keep them in `ansible-vault` or another secret backend before continuing the lab.

## WireGuard

- The lab can now expose a WireGuard VPN server for remote access to the LAN.
- Default design: split tunnel only for the VPN subnet and the home LAN, not full-tunnel internet.
- The server configuration lives under `/etc/wireguard/wg0.conf`.
- Generated client profiles live under `/etc/wireguard/clients/`.
- The router still needs a UDP port forward for `51820/udp` to the server LAN IP.
