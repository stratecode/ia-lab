# Principal model operation

Current approved increment, October 15, 2026: a personal work assistant starts
with two selectable local models and a browser conversation. The later objective is work
context, Jira/Granola/email access and simple everyday agents, with voice through
Telegram, WhatsApp and Slack outside the lab VPN/LAN. The [secretary increment](secretary-operation.md)
is deployed for Open WebUI and Telegram, including voice notes and read-only
Jira/Granola integration support. Telegram currently uses polling. The dedicated assistant HTTPS vhost
and DNS alias are deployed, but external TCP 443 probes time out, so webhook
cutover remains gated on restoring external ingress.

## Configuration owned by this repository

- Engine: llama.cpp revision `6eab47181cbd3532c88a105682b81b4729ab809b`.
- Model: LFM2.5-1.2B Instruct, official text-only GGUF Q4_0 from
  [LiquidAI](https://huggingface.co/LiquidAI/LFM2.5-1.2B-Instruct-GGUF), revision
  `8ed288026e23958ad9dfa92d53ed773a8eee7125`.
- File: `LFM2.5-1.2B-Instruct-Q4_0.gguf`, 695,751,488 bytes.
- SHA256: `2ea801949d760cdf1a2cc04a54262c22c3c0c54f0769d57760c9adeb0e59233f`.
- Host path: `/srv/ai-lab/llama.cpp/models/LFM2.5-1.2B-Instruct-Q4_0.gguf`.
- Service: `llama-cpp-main`, loopback port 8080, alias `main`.
- CPU only, 8 inference and batch threads, one request slot, non-thinking Instruct
  model, context 16,384 tokens including input/output.
- Per-service MemoryMax 4 GiB. Aggregate ceiling 90% of host resources;
  zero reservation floor for this CPU-only profile. No VRAM/BIOS modification.
- Open WebUI image pinned to [v0.12.2](https://github.com/open-webui/open-webui/releases/tag/v0.12.2),
  loopback port 3080, HTTPS at `https://chat.stratecode.com`; `main` goes
  through `lab-secretary` on 8082, then `llm-telemetry` on 8081 and the principal
  on 8080. The selectable `qwen-eval` model uses the same secretary pipeline
  and memory, with its own loopback Qwen backend on 8184.
  Existing Nginx LAN/VPN access restrictions remain; external messaging channels
  are the later route to use outside those networks. Client/proxy timeouts are
  at least 900 seconds to accommodate long CPU-prefill requests.
- Open WebUI configuration comes from repo-managed environment on startup
  (`ENABLE_PERSISTENT_CONFIG=False`); runtime admin settings may therefore reset
  on restart. Conversation history remains persisted in the existing volume.
- Initial browser profile disables builtin tools and automatic follow-up/tag
  generation; titles are also disabled. Default max output is 512 tokens.
  `stream_response=true` is reconciled in the principal model and the existing
  administrator UI preference through the authenticated API; startup interface
  defaults enable streaming for new profiles.
  The managed capability flags are persisted in that model entry too. v0.12 builtin tools otherwise
  injected about 7k prompt tokens into a trivial query in the initial UI probe.
  Native tools remain supported by llama.cpp for later explicit connectors.
- Authentication stays enabled on the HTTPS chat. Login-free browser testing
  uses a separate disposable loopback instance, not the production data volume.

The MAIN environment variables in `.env.example` control context/threads/port.
The API key can use the existing CODE key as a compatibility fallback without
editing `.env` or vault secrets. Legacy CODE/PLANNER/UTILITY/EMBEDDINGS model
settings no longer define instances. Retired source integrations are not running.

## Deployment

```bash
set -a
source .env
set +a
ansible-playbook playbooks/deploy-principal-model.yml
```

Build the secretary binary first with `./scripts/build-orchestrator-go.sh`.
Supply `LAB_SECRETARY_CONNECTORS_FILE` when authorised accounts should be retained
in deployment; see [secretary operation](secretary-operation.md).

This downloads pinned weights with checksum verification, removes stale units,
starts and validates principal generation, and only then removes the explicit
retired-weight paths. It also reconciles chat, monitoring and HTTPS without
starting the retired orchestrator or Codex gateway. `deploy-llama-cpp.yml` also
uses the same single-instance definition, but does not reconcile the chat surface.

Retired disk artifacts: previous Qwen2.5 coder 7B/3B, planner 3B, utility 1.5B,
Nomic embeddings, unused Qwen3 8B and Gemma3 12B caches, Qwen3.5-4B, and the temporary LFM
research GGUF. Research measurements and logs remain; model vocabulary fixtures
inside llama.cpp source are build/test assets and are not inference weights.
External Oráculo/SRL directories and Docker volumes are unrelated and preserved.

Old weights are intentionally deleted, not backed up. Redownloading them is the
recovery method. The existing Open WebUI SQLite database was backed up inside
its volume as `pre-principal-backup.sqlite3` before the image upgrade. Rollback
of the image after a migration requires restoring that database backup, not
simply running the old image against the migrated database.

## Reproducible probes

Run `scripts/smoke-principal-model.py` on the host with `LAB_MODEL_API_KEY` set
without printing it. It validates health, one advertised model, Spanish
conversation context, streaming and native tool arguments (no action execution).
`--long-context` adds a synthetic input above 8k tokens and checks retrieval of a
marker at the beginning. It is a bounded functional probe, not a model-quality
benchmark. Large prompts can take minutes to ingest on this CPU.

To reproduce the temporary login-free browser probe:

```bash
ansible-playbook playbooks/probe-open-webui.yml
ssh -i ssh/lab -N -L 127.0.0.1:13081:127.0.0.1:3081 stratecode@stratecode.local
# Open http://127.0.0.1:13081 and test a synthetic conversation.
# Close the tunnel, then remove the disposable instance and its test data:
ansible-playbook playbooks/probe-open-webui.yml -e open_webui_probe_state=absent
```

The probe uses a separate data directory, has no restart policy, and binds only
to host loopback. Its `WEBUI_AUTH=False` setting is limited to that instance.

## Previous Qwen preflight evidence (historical)

Current LFM measurements and deployment checks are in
`docs/validation/2026-10-11-model-latency/`. The results below belong to
the earlier Qwen3.5 deployment; they do not describe the current model.


The preflight recovered `ALAMEDA-73` from the start of a 9,140-token chat input
in 238.417 seconds, with RSS 5,595,296 KiB (about 5.34 GiB). The live short
Spanish memory probe answered in 2.994 seconds; streaming and a declared native
`service_status` tool call passed. These are individual probes, not latency
percentiles or a general quality guarantee.

The browser probe displayed the correct Spanish answer using only `main`, and
retained it after container recreation and restart. Production still has the
same account and chat content fingerprint after migration/restart. Five
Prometheus targets are UP, including the renamed `llama-cpp-main` target.
Go test/build harnesses passed, resource validation tests passed (27), and
all five affected playbooks passed Ansible syntax checks.

The login-free probe container, disposable data directory and SSH tunnel were
removed after testing; port 3081 is closed. The permanent chat remains healthy
and authenticated. Changes are present in the repository working tree; no
commit or push was made, preserving unrelated pre-existing changes.

Evidence is saved under `docs/validation/2026-10-11-principal-model/` after the
live checks. This documents actual results; future channel/voice functionality
must have separate end-to-end evidence before being described as active.

## Llamadas y latencia

Open WebUI conecta mediante la pasarela de métricas en 127.0.0.1:8081;
llama.cpp conserva 127.0.0.1:8080 y alias `main`. Ver
[operación de telemetría](llm-telemetry-operation.md). Títulos automáticos, tags
y preguntas de seguimiento desactivados para evitar inferencias auxiliares.
