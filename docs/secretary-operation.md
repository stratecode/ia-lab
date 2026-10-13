# Shared secretary operation

Increment prepared on October 12, 2026. The active LFM principal is unchanged.
`lab-secretary` adds shared instructions and deterministic read commands in front
of the existing telemetry proxy. It runs on host loopback `127.0.0.1:8082`.
Open WebUI at `https://chat.stratecode.com` uses `/v1` on that service.

```text
Open WebUI ───────────┐
Telegram private chat ├─ lab-secretary :8082 ─ llm-telemetry :8081 ─ llama.cpp :8080
                     └─ fixed read-only MCP calls ─ Jira / Granola
```

Telegram polling is enabled and active in the lab. The operator approved stopping
Hermes. Its Mac launchd service `ai.hermes.gateway` is stopped and disabled
from auto-start for this user. `launchctl enable gui/$(id -u)/ai.hermes.gateway`
reverses the auto-start setting; `hermes gateway start` starts it again. Afterwards, a
standalone `getUpdates` returned HTTP 200 with zero pending messages, and the lab
service logged `telegram_ready` without another 409. The check `getMe` confirmed
`@stratecode_bot`; no webhook is set. An actual private operator message and model
reply remains the final delivery check. If Hermes is restarted, it may reclaim the
bot and cause another polling conflict.

## Telegram webhook ingress

The repository provisions `https://assistant.stratecode.com/telegram/webhook`
through an exact-match Nginx route to the loopback secretary. The public vhost
rejects other paths and TLS hosts; callback access logs contain only method,
normalized path and status. The webhook handler authenticates Telegram's secret
header, limits request bodies, and persists/deduplicates updates before returning
success. Polling, webhook and disabled modes are mutually exclusive. The normal
deployment remains on polling.

The assistant DNS alias, TLS certificate and Nginx route are deployed. Local
Nginx validation passed and the loopback webhook path rejects GET with 403 as
configured. Three TCP 443 probes from external networks timed out while the
assistant DNS address matched the lab's current public egress address. The
router/upstream ingress path is therefore still unverified; do not run the
activation playbook until an external probe confirms TCP 443 and trusted HTTPS.
The secretary remains available through Telegram polling while this is resolved.
The router rule must forward IPv4 TCP 443 only to the reserved lab address
`192.168.0.85:443`; do not forward ports 80, 22 or application ports, and do not
add IPv6 ingress.

After the external check succeeds, activate the transport with:

```bash
set -a
source .env
set +a
ansible-playbook playbooks/enable-telegram-webhook.yml \
  -e telegram_public_probe_passed=true
```

The playbook verifies wrong-secret rejection, durable acceptance and duplicate
delivery locally before calling Telegram `setWebhook` with
`drop_pending_updates=false`. It suppresses token and secret output. If delivery
fails, use `playbooks/restore-telegram-polling.yml`; it deletes the webhook
without dropping pending updates, then restarts the exclusive polling consumer.
Do not run both consumers at once. The queue is at-least-once: a crash after a
Telegram reply but before local completion can duplicate that reply.

## Commands and permissions

| Command | Result |
|---|---|
| Plain text | Principal LFM conversation with shared secretary instructions |
| `/help`, `/start` | Telegram help |
| `/new` | Clear this operator's Telegram conversation; UI chats stay in Open WebUI |
| `/status` | Connector configuration status; does not prove provider connectivity |
| `/jira` | Up to five open issues assigned to the authorised Jira account |
| `/reuniones` | Executive summary of up to five Granola notes from the last seven days; uses note summaries, never transcripts |
| `/resumen` | Source-labelled Jira and Granola results, with explicit missing coverage |
| `Ponme un temporizador de 10 minutos` / `/timer 10m` | Persist a one-shot timer and notify this private Telegram chat when it expires |
| `/timers` | List this operator's active timers and remaining time |
| `/cancel ID` | Cancel one of this operator's active timers |

Source commands also work in Open WebUI. `/reuniones` asks the principal model to
turn up to five recent Granola note summaries into an executive brief with decisions,
actions and reminders. The source window is the last seven days, not a full calendar
query; missing owners and dates are never inferred, and transcripts are excluded.
Jira and `/resumen` still return source-labelled fields directly. This POC does not
yet support natural-language tool selection. Telegram history is persisted as complete
exchanges, bounded to eight turns / 4,000 characters; source command results enter
that context when they fit the limit. Open WebUI owns its separate conversation
history. Shared persona and tools do **not** imply cross-channel memory syncing.

Telegram accepts only private chats whose sender and chat IDs match an explicit
operator allowlist. No group chats, shell, legacy `/launch` commands, Jira writes,
calendar modifications or external message-sending tools are exposed. WhatsApp
and Slack remain later adapters. Poll offsets are checkpointed before
processing; after a crash during a request, resend that message rather than
expecting replay. Model calls continue through Prometheus telemetry. Source-only
commands bypass inference and are not counted as LLM generations.

Timers are handled deterministically by `lab-secretary`, not by the model. The
first version accepts one duration in seconds, minutes or hours, up to 24 hours;
it sends one Telegram message at expiry and supports up to ten active timers per
operator. Timer state is stored in the service StateDirectory and survives
restarts. If a timer expires while the service is down, its notification is sent
after restart. Delivery is retried while Telegram is unavailable; because the
send and local state update are separate operations, a crash between them can
cause a duplicate notification.

## Telegram voice notes

The first voice path accepts complete Telegram voice notes up to 20 MiB and 120
seconds, only after the private-chat sender and chat pass the same allowlist as
text. The adapter downloads the note, converts OGG/Opus to WAV in memory, sends
it to local Whisper with Spanish recognition, asks the principal model, delivers
the written answer, then synthesizes and sends a Telegram voice reply. If speech
synthesis or audio delivery fails, the written answer remains available. No audio
or transcript is written to application logs or metrics; scratch conversion data
is held in memory, and Whisper runs with a private temporary directory.

Whisper runs CPU-only from `whisper.cpp` v1.9.5 using the multilingual
`ggml-small-q5_1.bin` model with four threads by default. Piper runs
`piper-tts` 1.8.0 with the Spanish `es_ES-sharvard-medium` voice. Both services
bind only to `127.0.0.1`: Whisper on port 8083 and Piper on 8084. Their systemd
units have separate memory and CPU ceilings. Tune `lab_whisper_threads`,
`lab_whisper_cpu_quota`, `lab_whisper_memory_max`, `lab_piper_cpu_quota`, and
`lab_piper_memory_max` in the tracked Ansible defaults before deployment.

Voice timings are exposed on the secretary's loopback `/metrics` endpoint as
`lab_secretary_voice_phase_duration_seconds`,
`lab_secretary_voice_phase_calls_total`, and
`lab_secretary_voice_request_duration_seconds`. Labels are fixed phase/result
values; they never contain transcript, prompt, chat, user, or file identifiers.
Use `python3 scripts/smoke-secretary-voice.py` on the lab host for a synthetic
Piper → FFmpeg → Whisper check. It prints engine pins and timings, not the
recognized phrase, and keeps audio in memory.

To roll back the voice feature, set `lab_secretary_voice_enabled: false` in
`playbooks/deploy-secretary.yml` and redeploy. This stops and disables Whisper
and Piper and keeps text Telegram active; downloaded models and binaries remain
on disk for a later re-enable. The switch is tracked in Git.

The engine and voice have separate licenses: [whisper.cpp](https://github.com/ggml-org/whisper.cpp/blob/d1be6fde11ac6e0407606b4e42fe72d34add8037/LICENSE)
and [Piper](https://github.com/OHF-Voice/piper1-gpl/blob/v1.8.0/LICENSE.md) are
GPL-3.0-or-later; the [Spanish voice](https://huggingface.co/rhasspy/piper-voices/tree/v1.0.0/es/es_ES/sharvard/medium)
is MIT-licensed. Review each license before redistributing a built image or
model bundle.

## OAuth enrollment

Run on the operator's Mac; authorize from a browser on that same machine:

```bash
python3 scripts/connect-secretary.py jira
python3 scripts/connect-secretary.py granola --port 18766
```

Each helper validates provider discovery, registers a client, uses PKCE S256 and
checks callback state. It prints a 20-minute authorization link and listens only
on loopback. Tokens are written with mode 0600 to
`~/.config/stratecode-lab/secretary-connectors.json`, outside Git. No changes to
`.env`, vault or SSH credentials are needed. `--discover` probes public metadata
without enrollment. A failed consent is not a connected account.

Jira requests `read:me`, `read:jira:agent-interface`,
`search:jira:agent-interface` and `offline_access`, without write scopes. If the
account exposes multiple Jira sites, enroll with `--cloud-id <selected-site-id>`;
the adapter refuses to guess. Granola exposes its general `mcp` OAuth scope;
the adapter's hard allowlist permits `list_meetings` only. Its current response
is markup rather than JSON, so the adapter extracts only meeting ID, title and
date; it does not pass transcript or note bodies into chat. Verify remote tool
and result schemas with the authorised account before POC acceptance.

```bash
./scripts/test-orchestrator-go.sh
./scripts/build-orchestrator-go.sh
set -a
source .env
set +a
export LAB_SECRETARY_CONNECTORS_FILE="$HOME/.config/stratecode-lab/secretary-connectors.json"
ansible-playbook playbooks/deploy-secretary.yml
```

To update OAuth connectors without redeploying Telegram, the model or Open WebUI,
use the narrow playbook below. It stores only the connector payload in a root-only
environment file and restarts `lab-secretary`:

```bash
export LAB_HOSTNAME=lab.stratecode.com
export LAB_USER=stratecode
export LAB_SECRETARY_CONNECTORS_FILE="$HOME/.config/stratecode-lab/secretary-connectors.json"
ansible-playbook playbooks/deploy-secretary-connectors.yml
```

The connector playbook does not load `group_vars/vault.yml` and does not change
the main secretary environment. The full secretary deployment remains the
authoritative path when binaries, Telegram settings, or model configuration
change.

Do not use `--diff` for secret-bearing deployment. The role loads the private JSON
into `/etc/lab-secretary/connectors.env` with root-only permissions; refreshed tokens are stored privately under
systemd's StateDirectory. Re-deployment needs the same private connector file;
omitting `LAB_SECRETARY_CONNECTORS_FILE` intentionally deploys no OAuth accounts.
The supplied token state must be refreshed/re-enrolled if the provider revokes it.
Keep any host refresh cache private and out of evidence or Git.

Telegram polling is enabled in `group_vars/all.yml`. Do not issue manual
`getUpdates` requests while the service polls: that creates a competing consumer.
Preflight uses `playbooks/check-telegram.yml` (`getMe`, webhook metadata only).
The retired orchestrator stays stopped.

## Verification and evidence

Tests exercise allowlist rejection, private chat identity, preserved streaming,
restart history, failure handling, durable polling offsets, source fidelity,
malformed source responses, provider credential destination checks and denied
write tools. OAuth tests cover issuer path discovery, allowed hosts, state and
read-only requested Jira scopes. Execute the Docker Go harness and Python tests:

```bash
python3 scripts/test-secretary-oauth.py
python3 -m py_compile scripts/connect-secretary.py scripts/smoke-secretary.py
```

On the host, run `scripts/smoke-secretary.py` with `LAB_SECRETARY_MODEL_KEY` set
privately. It checks health, HTTP 401 without credentials, one model, shared status
SSE and a synthetic streamed answer with first-token / total time. It does not
print real task or meeting content. Final acceptance also needs an actual
operator message/reply on Telegram and real readonly Jira/Granola queries.

Voice evidence: [validation directory](validation/2026-10-12-secretary-voice/).
Telegram evidence: [validation directory](validation/2026-10-11-secretary-telegram/).
Primary provider contracts: [Telegram Bot API](https://core.telegram.org/bots/api),
[Atlassian MCP](https://atlassian.github.io/atlassian-mcp-server/),
[supported tools](https://support.atlassian.com/atlassian-ai-gateway/docs/supported-tools/),
[Granola MCP](https://docs.granola.ai/help-center/sharing/integrations/mcp).
