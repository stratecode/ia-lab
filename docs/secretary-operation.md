# Shared secretary operation

Increment prepared on October 12, 2026. The active LFM principal is unchanged.
`lab-secretary` is the shared channel router for the principal model, registered
read-only capabilities, conversation memory and telemetry. It runs on host
loopback `127.0.0.1:8082`.
Open WebUI at `https://chat.stratecode.com` uses `/v1` on that service.

```text
Open WebUI (main/Qwen) ─┐
Telegram private chat ──┼─ shared router + memory ─ selected model backend
Future channels ───────┘                    └──── registered read-only capabilities
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

After building the Linux binary, update only this service with
`ansible-playbook playbooks/deploy-secretary-runtime.yml`. It replaces only the
secretary executable and updates only its non-secret progress-delay setting,
restarts `lab-secretary`, checks its health endpoint and restores the previous
binary if health verification fails. Credentials and other service configuration
stay in place; Open WebUI, telemetry and speech services are not restarted.

To deploy only Piper voice settings and shared response instructions, run
`ansible-playbook playbooks/deploy-secretary-voice.yml`. This restarts `lab-piper`
when its unit changes and `lab-secretary` when the instructions change.

| Command | Result |
|---|---|
| Plain text | Natural-language routing, shared operator memory and principal LFM synthesis, from Telegram or Open WebUI |
| `/help`, `/start` | Secretary help |
| `/new` | Clear the secretary's recent shared context; channel-native chat histories and durable memory remain |
| `/forget` | Delete the operator's conversation memory, traces, feedback, knowledge and recent shared context |
| `/feedback bien` | Mark the latest response in this channel conversation as correct |
| `/feedback mal: motivo` | Record a negative rating and create a private candidate for review |
| `/feedback corrige: corrección` | Record a correction as an unapproved candidate; it cannot affect answers until reviewed |
| `/aprendizaje` | List candidate correction IDs and brief descriptions |
| `/aprueba ID`, `/descarta ID` | Approve or reject one candidate owned by this operator |
| `/status` | Connector configuration status; does not prove provider connectivity |
| `/jira` | Up to five open issues assigned to the authorised Jira account |
| `/reuniones` | Executive summary of up to five Granola notes from the last seven days; uses note summaries, never transcripts |
| `/resumen` | Source-labelled Jira and Granola results, with explicit missing coverage |
| `Ponme un temporizador de 10 minutos` / `/timer 10m` | Persist a one-shot timer and notify this private Telegram chat when it expires |
| `/timers` | List this operator's active timers and remaining time |
| `/cancel ID` | Cancel one of this operator's active timers |

All text prompts in Open WebUI now use the same secretary router as Telegram;
they are no longer forwarded directly to the model. Open WebUI requests are
accepted only for its authenticated administrator, mapped to the lab's single
allowlisted operator. Requests without trusted admin identity or with multiple
configured operators fail closed. Open WebUI and Telegram share durable memory,
while short-term history stays isolated by channel and conversation. Open WebUI
can select `main` (LFM) or `qwen-eval` (Qwen); both use the same routing,
connector and memory pipeline. Telegram remains pinned to LFM. Open WebUI
currently accepts text-only user messages through this route; multimodal messages
receive an explicit client error instead of silently bypassing the router.
The Open WebUI deployment enables `ENABLE_FORWARD_USER_INFO_HEADERS`; apply that
setting without re-rendering credentials with
`ansible-playbook playbooks/enable-open-webui-identity-headers.yml`.

Slash commands also work in Open WebUI. `/reuniones` asks the principal model to
turn up to five recent Granola note summaries into an executive brief with decisions,
actions and reminders. The source window is the last seven days, not a full calendar
query; missing owners and dates are never inferred, and transcripts are excluded.
Jira and `/resumen` still return source-labelled fields directly. Natural-language
routing selects from a registry of read-only capabilities, each with its own
matching rule, retrieval function and description. Questions about plans, tasks,
priorities or pending activities route to Atlassian Rovo across Jira and Confluence,
even if the question mentions a meeting. Meeting summaries and decisions without a
task request route to Granola. Cross-source correlation between Granola, Rovo and
company people or customers is a later phase. Adding a source means registering
another capability, not changing Telegram or the shared conversation pipeline.
The principal model synthesizes retrieved data and must not invent facts. Rovo
uses standard semantic search with the MCP server's minimum ten-result request;
retrieved content is
treated as untrusted source data. If a source is unavailable or returns no results,
the service says so directly instead of asking the model to improvise from an empty
result. Empty or failed sources are excluded from the model's evidence and reported
as a separate coverage note after synthesis.

For natural-language Granola questions, `query_granola_meetings` is used only to
locate cited notes. The connector then retrieves those notes with `get_meetings`
and gives their summaries, rather than Granola's generated answer, to the principal
model. Transcripts remain excluded. This keeps responsibility assignments grounded
in the source note when the provider's generated answer paraphrases an owner incorrectly.

## Supervised memory and evaluation

Each eligible natural-language request stores a versioned interaction trace under
the service's private `StateDirectory`. A trace records channel, a one-way
conversation fingerprint, intent, selected capability, outcome, latency, result
count, provider-backed evidence references and trusted model/router labels. It
does not store the prompt, answer, transcript or complete connector payload.
Trace retention defaults to 90 days; unresolved candidates default to 30 days.
Both values are configurable through the tracked Ansible variables
`lab_secretary_trace_retention_days` and
`lab_secretary_candidate_retention_days`. Cleanup runs at service startup and
every 24 hours. Cleanup failures are counted in
`lab_secretary_learning_maintenance_errors_total` and do not stop the service.

Feedback binds to the latest interaction for the same authenticated operator,
channel and conversation. A correction remains a candidate until `/aprueba ID`;
only approved items enter retrieval, with their source and validity dates. Similar
person names remain unresolved unless an exact external ID or approved alias
identifies one person. Live Jira/Rovo or Granola data remains authoritative for
current state. `/forget` also removes all learning records for that operator.

The metrics endpoint exposes bounded interaction outcome and duration labels,
memory retrieval counts/hits, trace-write failures and maintenance failures.
Labels exclude user text, names, issue keys, source URLs, provider IDs and
conversation IDs. No automatic weight updates, prompt mutation or rule learning
occurs; this is a supervised feedback and regression loop, not reinforcement
training. Real interaction records stay on the lab; repository fixtures must be
synthetic or redacted.

## Shared interaction feedback

The router emits short-lived, channel-neutral events after classifying an
authorized request. A lookup uses 🔍, agreement uses ✅, and a recognized action
request uses 👍. The signal describes the request's meaning; it does not claim
that a lookup succeeded or that an external change was made.

Telegram reacts to the authorized incoming message. When a registered source is
actually selected, it sends one short source update before the final answer
(for example, “Déjame revisar Granola.”). A recognized action against today's
read-only tools says that it is looking up the item and can only consult it; the
final answer states that no change was applied. If processing exceeds 30 seconds,
the channel sends at most one further update naming the current phase. Fast
answers have no generic acknowledgement. If Telegram rejects a reaction, the
secretary records only a bounded presentation error and continues the answer.

Open WebUI renders the same emoji and source update as assistant content before
the answer, preserving streaming responses. Non-streaming replies include those
items only when the corresponding events occurred. Neutral conversation does
not invent a lookup update, and an unsupported action does not claim a source
was checked.

These events exist only while handling the request: they are not persisted and
do not add message content, prompts, identifiers or connector data to metrics.
Slack and WhatsApp are not connected yet; future adapters should render this
same event contract using their own native presentation, without adding a
channel-specific classifier.

Conversation memory is stored on the lab server as per-operator JSONL records
with private file permissions. Lexical retrieval selects up to three relevant
prior exchanges for context; `/new` clears only the short conversation window,
while `/forget` removes both that window and durable memory. Memory stores text,
including voice transcripts and replies, but never audio. Open WebUI retains its
own visible chat history while its new turns also enter the secretary's shared
operator memory.

Telegram accepts only private chats whose sender and chat IDs match an explicit
operator allowlist. No group chats, shell, legacy `/launch` commands, Jira writes,
calendar modifications or external message-sending tools are exposed. WhatsApp
and Slack remain later adapters; they should register transport identity and
call this same router rather than implement their own model or tool flow. Poll offsets are checkpointed before
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
it to local Whisper with Spanish recognition, then asks the principal model.
Both voice and text input receive written responses; Telegram speech synthesis
is currently disabled. The adapter sends a brief written acknowledgement before
processing, alternating between a small set of short phrasings so consecutive
requests do not receive the same canned line. When a registered source is
selected, it sends one short progress message before reading it. If a request
still runs after `lab_secretary_progress_delay_seconds` (30 seconds by
default), it sends at most one additional update naming the current phase, such
as transcribing, interpreting, consulting a source, or preparing the answer.
The update is canceled when the reply finishes before the threshold. The value
is configurable from one to 120 seconds in `group_vars/all.yml`. The adapter
finishes with one concise answer. No audio or
transcript is written to application logs or metrics; scratch conversion data
is held in memory, and Whisper runs with a private temporary directory. Telegram
renders Markdown bold as HTML and escapes other markup.

## Conversation memory

Short-term history is scoped to the operator, channel, and conversation ID. It
keeps up to ten exchanges and expires turns after two hours; `/new` resets only
the current conversation. Telegram uses the private chat ID. Open WebUI uses
`X-OpenWebUI-Chat-Id` (or the compatible `chat_id` request field); the routed
endpoint rejects a request without a stable chat ID rather than silently mixing
unrelated chats. Existing Telegram history remains readable during migration.

Long-term memory stores each question and answer in a local JSONL file partitioned
by calendar day in Europe/Madrid, with the channel and a one-way conversation
fingerprint. The old shared archive remains readable. Recall filters by date
before ranking: for example, “la semana pasada” opens only the previous Monday
through Sunday partitions. Queries without a date search the latest 30 days and
the legacy archive. `/forget` removes both formats and every short-term history
for the operator.

Conversation retrieval currently uses normalized Spanish keywords and recency,
with a maximum of eight exchanges supplied to the model. Approved work knowledge
uses a separate bounded lexical lookup (up to five items) and includes validity
and evidence references. It needs no vector database or extra model. Evaluate
missed synonyms and irrelevant matches against real queries first; if lexical
retrieval is insufficient, add local embeddings and an index only then. Prometheus
tracks bounded retrieval outcomes and hit counts without conversation content.

Whisper runs CPU-only from `whisper.cpp` v1.9.5 using the multilingual
`ggml-small-q5_1.bin` model with four threads by default. Piper remains
configured as a local service with the Spanish `es_ES-sharvard-medium` voice,
female speaker 1, at length scale `0.96`, but the Telegram reply path does not
invoke it. The repository defaults keep the speaker and pace adjustable. Both services
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

Jira OAuth requests `read:me`, `read:jira:agent-interface`,
`search:jira:agent-interface` and `offline_access`, without write scopes. The
natural-language Rovo capability uses the Atlassian MCP `search` tool and needs an
API key with `search:rovo:agent-interface`; it also reads matching issue details
with `getJiraIssue`, which requires `read:jira:agent-interface`. `/jira` continues
to use JQL and needs `search:jira:agent-interface`. This connector accepts a
service-account API key using Bearer auth; a personal API token uses Basic auth
and is not supported by this credential format. For API-key enrollment, request
the three read-only scopes above and have an organisation admin enable API-token
access in Rovo. Do not add write scopes. If the account exposes multiple Jira
sites, enroll with `--cloud-id <selected-site-id>`; the adapter refuses to guess.
Granola exposes its general `mcp` OAuth scope;
the adapter's hard allowlist permits `list_meetings` only. Its current response
is markup rather than JSON, so the adapter extracts only meeting ID, title and
date; it does not pass transcript or note bodies into chat. Verify remote tool
and result schemas with the authorised account before POC acceptance. Atlassian's
[Rovo supported-tools reference](https://developer.atlassian.com/cloud/rovo-mcp/guides/supported-tools/)
lists `read:jira:agent-interface` for issue reads, `search:jira:agent-interface`
for JQL, and `search:rovo:agent-interface` for semantic search. See Atlassian's
[API-token authentication guide](https://developer.atlassian.com/cloud/rovo-mcp/guides/configuring-authentication-via-api-token/)
for the Basic-versus-Bearer distinction.

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
