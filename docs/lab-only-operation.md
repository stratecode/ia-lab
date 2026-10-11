# Lab-only operation

This records the initial October 11 cleanup. The later single-model increment
is documented in [Principal model operation](principal-model-operation.md).

The initial operating boundary kept the four llama.cpp services,
repository-owned Open WebUI, Grafana, Prometheus, Alertmanager, node exporter,
Nginx, Cockpit and WireGuard. The orchestrator and Codex gateway remain retired.
The product roadmap in `architecture/master-plan.md` describes capabilities in
source, not an assertion that the control plane is currently deployed.

Oráculo and SRL/OpenSearch are external test projects. Their containers and
unused Docker networks are retired; images, volumes and source directories are
preserved. Open WebUI for the lab uses its own volume, separate from Oráculo's
chat history. No external data is migrated into the lab chat.

## Reproduce

Load the inventory environment without printing secrets:

```bash
set -a
source .env
set +a
ansible-playbook playbooks/retire-external-projects.yml
ansible-playbook playbooks/deploy-lab-surface.yml
```

The retirement helper defaults to a dry run when invoked directly. It selects
containers by Compose working directories or bind mounts beneath `/opt/oraculo`
and `/opt/srl`, never by container name alone. It disables restart policies,
stops and removes containers without deleting volumes. A root-only recovery
manifest is saved under `/srv/ai-lab/backups/external-projects/` before removal.
No startup references for these projects were found in system or user units or
cron during the inventory.

External projects must be redeployed on a separate host. Their original Compose
files and data remain beneath `/opt/oraculo` and `/opt/srl`; named and anonymous
volumes remain in Docker. Do not run volume pruning on this host unless those
retained datasets have been explicitly reviewed for deletion.

`deploy-lab-surface.yml` restores repository-owned chat and monitoring without
starting the retired control plane. Do not use the historical full bootstrap or
`cleanup-llama-baseline.yml` to reproduce this profile: the former can reinstate
the control plane, while the latter removes Open WebUI.

Resource tuning already present in the working tree is preserved but is not
deployed as part of external-project retirement.

## Verified October 11, 2026

- Removed 20 external containers and five unused external Docker networks.
- A second retirement run reported `changed=0`, `failed=0`.
- All pre-existing Docker volumes and external project directories remained.
- Only `open-webui` and `grafana` application containers remained, both healthy.
- All four inference health endpoints returned HTTP 200.
- Coder, planner and utility model discovery and short generation succeeded
  from the lab Open WebUI container, using its configured connection keys.
- Chat and Grafana health endpoints returned HTTP 200 over verified HTTPS.
- Nginx validation passed and SRL/OpenSearch proxy definitions were absent.
- External ports 5432, 5601, 9200, 8200, 8091, 8000, 8081, 9080, 9092 and
  9443 were closed on loopback.
- Prometheus targets were healthy with no active alerts. The SRL cAdvisor
  target was removed; the pre-existing direct inference scrape coverage
  remains limited to the code instance.
- WireGuard, Nginx, inference and monitoring systemd services remained active.

Recovery configuration snapshot:
`/srv/ai-lab/backups/lab-only/20261011T161938Z`.

External container recovery manifest:
`/srv/ai-lab/backups/external-projects/20261011T162008413414Z/manifest.json`.
