# 0001. Use Nginx Over Traefik as Reverse Proxy

**Status:** accepted

**Date:** 2025-07-14

## Context

The Local AI Agents Platform requires a reverse proxy to expose internal services (Cockpit, Grafana, llama.cpp API endpoints) over HTTPS on a single Ubuntu server. TLS certificates must be automated via Let's Encrypt using the Route53 DNS challenge, since the server runs behind a residential connection with dynamic IP.

Two primary candidates were evaluated:

- **Nginx** — mature, widely deployed HTTP server and reverse proxy with extensive documentation and ecosystem support.
- **Traefik** — modern cloud-native reverse proxy with automatic service discovery via Docker labels and built-in Let's Encrypt integration.

Key constraints influencing this decision:

1. The platform runs on a single host — there is no container orchestrator (Kubernetes, Swarm) that would benefit from Traefik's automatic service discovery.
2. Several services run as native systemd units (llama.cpp, WireGuard, Cockpit), not as Docker containers, making Traefik's Docker-label-based routing inapplicable for a significant portion of the stack.
3. The team has existing operational experience with Nginx configuration and troubleshooting.
4. TLS certificate management via certbot with the dns-route53 plugin is already proven in the current infrastructure provisioning (Ansible role `monitor`).
5. Nginx's resource footprint is minimal and predictable, which matters on a single server also running GPU inference workloads.

## Decision

Use Nginx as the platform's reverse proxy, with TLS termination handled by certbot using the Route53 DNS-01 challenge for Let's Encrypt certificates.

Rationale:

- Nginx handles both containerized and systemd-native services uniformly through upstream configuration blocks, without requiring Docker socket access or label annotations.
- The certbot + dns-route53 plugin integration is already automated via Ansible and works reliably with the platform's dynamic DNS setup.
- Nginx's static configuration model is simpler to audit, version-control, and reproduce via Ansible than Traefik's dynamic provider model on a single-host deployment.
- Lower memory and CPU overhead compared to Traefik, preserving resources for inference workloads.
- Extensive community documentation and tooling reduces troubleshooting time.

## Consequences

**Positive:**

- Unified proxy configuration for all services regardless of their runtime model (Docker or systemd).
- Proven TLS automation pipeline with certbot and Route53 DNS challenge already in place.
- Minimal resource consumption on a resource-constrained single server.
- Configuration is fully declarative and managed through Ansible, ensuring reproducibility.
- Large ecosystem of modules and community knowledge for edge cases.

**Negative:**

- Manual configuration updates required when adding new services (no automatic discovery).
- No built-in dashboard for real-time traffic visualization (mitigated by Prometheus + Grafana metrics).
- If the platform later moves to a multi-node orchestrated deployment, Traefik's automatic service discovery would become more valuable and this decision may need revisiting.
