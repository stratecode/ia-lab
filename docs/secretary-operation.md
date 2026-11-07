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

## Telegram webhook ingress

Telegram receives messages exclusively through the authenticated webhook at
`https://assistant.stratecode.com/telegram/webhook`. The supported modes are
`webhook` (default) and `disabled`. There is no alternative reception transport
or automatic transport fallback. A retired mode value is rejected at startup. The deployment role also rejects
stale binaries that still contain the retired reception API; rebuild before deploying.
The old Hermes Mac gateway must remain disabled so it cannot reclaim this bot.

Nginx forwards only the exact webhook path to the loopback secretary. The handler
validates Telegram's secret header, limits request size and persists/deduplicates
each update before acknowledging it. A worker processes queued updates and marks
completion after delivery. Pending updates survive a restart. A crash after
sending but before completion can still duplicate a reply; this is at-least-once
delivery, not an exactly-once guarantee. The timer scheduler runs with the worker.

Activate or reconcile the transport with `playbooks/enable-telegram-webhook.yml`
after checking trusted HTTPS and the public ingress path. The playbook tests
wrong-secret rejection, persistent acceptance and duplicate delivery, then calls
`setWebhook` with `drop_pending_updates=false`. Bot tokens and the webhook secret
must never appear in output. To pause reception, use mode `disabled`; restore the
webhook after correcting ingress. Do not delete pending updates during recovery.

Inspect `getWebhookInfo` for the registered URL, pending count and delivery errors,
and inspect the local durable queue for work already accepted. A successful
`setWebhook` only confirms registration; real delivery requires an incoming
Telegram update. Keep ingress restricted to TCP443 and the configured Nginx route.

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
| `/mapeo` | Ayuda para crear mapeos guiados de personas, proyectos, emails y aliases |
| `/mapeo persona NOMBRE`, `/mapeo proyecto TEXTO` | Busca candidatos verificables en Jira y deja cada candidato pendiente |
| `/mapeo email EMAIL para ID_JIRA`, `/mapeo alias ALIAS para ID_JIRA` | Propone un email para una persona confirmada, o un alias para una persona/proyecto confirmado |
| `/mapeo confirmar ID HUELLA_COMPLETA`, `/mapeo descartar ID HUELLA_COMPLETA` | Confirma o descarta únicamente el candidato y la huella mostrados en esta conversación |
| `/mapeo pendientes` | Muestra candidatos pendientes de esta conversación |
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
configured operators fail closed. Open WebUI and Telegram use the same durable
memory store, but recall is filtered by the exact channel and conversation
fingerprint. A Telegram exchange cannot leak into Open WebUI, and one Open WebUI
chat cannot recall another chat's private dialogue. Short-term history is
isolated by the same boundary. Open WebUI
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
priorities or pending activities use Jira Cloud REST with locally compiled JQL,
even if the question also mentions a meeting. Jira requests go to the fixed
`api.atlassian.com/ex/jira/<cloud_id>/rest/api/3/search/jql` endpoint and never
through an MCP. Meeting summaries and decisions without a task request route to
Granola. Cross-source correlation between Granola and
company people or customers is a later phase. Confluence is not yet a registered
capability; when added, it must use Confluence REST/CQL directly. Adding a source
means registering another capability, not changing Telegram or the shared
conversation pipeline. The principal model synthesizes retrieved data and must
not invent facts. Jira content is
treated as untrusted source data. If a source is unavailable or returns no results,
the service says so directly instead of asking the model to improvise from an empty
result. Empty or failed sources are excluded from the model's evidence and reported
as a separate coverage note after synthesis.

For named-person questions such as “cómo van Valentina y Gonzalo con sus tareas”,
Donna resolves each person independently. She queries up to 100 open issues and,
unless the request is only about pending or in-progress work, up to 100 issues
whose Jira status category changed to Done in the last 14 days. She keeps those
groups separate so recent completions cannot hide active work. The reading names
relevant open issues, groups explicit Jira workflow states, highlights overdue
and next-two-day due dates, and proposes the next check. Jira's `new` status
category means “Por hacer”; only an explicit unplanned status means “Sin
planificar”. An old or overdue issue is not labelled blocked without an explicit
blocked status. Missing due dates cannot support a claim that work is on time.
When either query has more than 100 results, the answer marks the sample as
incomplete. Telegram and shared chat show a short source-specific progress
message when retrieval starts. Upcoming work is surfaced when asked; Donna does
not send unsolicited Jira alerts.

In this operator profile, an unqualified question about Valentina's work defaults
to the PMI Jira project. Explicit project scope in the current user request or
recent user-authored conversation takes precedence. Questions asking what she is
working on use Jira's exact `In Progress` status; the broader status category also
contains `Waiting` items in this project. Assistant answers are never used to
establish a person or project scope for a Jira query.

For natural-language Granola questions, `query_granola_meetings` is used only to
locate cited notes. The connector then retrieves those notes with `get_meetings`
and returns their stored summaries with direct note links; Granola's generated
answer is not used as factual content. Transcripts remain excluded. Explicit
meeting-owner questions use deterministic extraction from those summaries where
possible. Cross-source links are rendered only when the comparison validator
finds a shared quoted fact; otherwise the source facts remain separate. Ordinary
multi-source synthesis still uses the principal model and is not a general
claim-by-claim entailment proof, so ambiguous or unsupported facts need a stricter
deterministic renderer before they can be presented as established.

The Jira enhanced search request follows `nextPageToken` for up to ten pages.
Responses that omit a continuation token before `isLast`, repeat a cursor, or
reach the page cap are marked partial. Person/task answers keep incomplete result
counts explicitly qualified; an incomplete empty result is never stated as a
complete “no matches” search. A missing `isLast` in a provider response is not
treated as proof that the result set is complete.

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
identifies one person. Live Jira or Granola data remains authoritative for
current state. `/forget` also removes all learning records for that operator.

The metrics endpoint exposes bounded interaction outcome and duration labels,
memory retrieval counts/hits, trace-write failures and maintenance failures.
Labels exclude user text, names, issue keys, source URLs, provider IDs and
conversation IDs. No automatic weight updates, prompt mutation or rule learning
occurs; this is a supervised feedback and regression loop, not reinforcement
training. Real interaction records stay on the lab; repository fixtures must be
synthetic or redacted.

### Source-exclusion canaries across secretary restarts

The secretary's `/metrics` counters are process-local and reset when the service
restarts. Prometheus retains the scraped samples and `increase()` accounts for
counter resets, so compare a canary against its isolated time window rather than
subtracting two `/metrics` snapshots. The `lab-secretary` scrape interval is 15
seconds; query through the Prometheus API prefix `/prometheus/api/v1/query`.
After a restart, the `capability="none"` series may not exist until the first
interaction. If it is absent, send one separate synthetic no-source warm-up,
wait for two scrapes, and record the raw counter as the baseline before measuring
the next isolated message. An absent `none` series is not a successful canary.

For each synthetic message, record its UTC send and completion times, leave at
least one scrape interval before it and two after it, and keep unrelated
secretary traffic outside that window. Confirm `up{job="lab-secretary"} == 1`
and evaluate these expressions at the end of the window, setting the range
(shown as `2m`) to cover that message and its scrape margin:

```promql
sum(increase(lab_secretary_interactions_total{job="lab-secretary",capability="none"}[2m]))
sum(increase(lab_secretary_interactions_total{job="lab-secretary",capability=~"jira|granola"}[2m])) or vector(0)
sum(resets(lab_secretary_interactions_total{job="lab-secretary"}[2m])) or vector(0)
```

The first query must return a positive delta for the synthetic no-source
interaction; an empty result means no matching samples were retained and is not
proof of zero interactions. The second must remain zero; `or vector(0)` treats a
never-created source series as zero. The third shows observed counter resets,
not every service restart. `increase()` can return a fractional estimate
because Prometheus extrapolates between scrape samples; compare positive versus
zero and approximate deltas. If canary messages are close together, query a
single window covering the whole sequence and compare total expected
interactions with the aggregate source delta, or separate messages by more than
the window length.

Natural-language requests that ask Donna to create, edit, transition or delete a
Jira issue stop before model synthesis and read-only connector calls while no
write operation is registered. The response says the current connector is
read-only; it cannot claim success. The future write path must retain the
operator's always-confirm requirement and report success only after a verified
provider receipt.

Donna also checks model-generated answers before returning or storing them. An
unverified claim or offer to change an external system is replaced with a
read-only statement, including after a short confirmation such as «sí, hazlo».
Sourced answers are rejected when they introduce a Jira issue key absent from
the provider result. This identifier check does not verify every natural-language
fact; source-specific factual acceptance still requires isolated live cases.
Open WebUI may stream progress events immediately, but buffers model answer
chunks until the final answer passes these checks.

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

## Shared web links

When an operator includes a public HTTPS URL in a message, the shared router
reads that page and gives its extracted text and source URL to the model for a
concise answer. The page body is treated as untrusted data, never as
instructions. The source URL accompanies the synthesis context; page content
is not persisted in conversation memory or learning traces. This first increment
accepts HTML, XHTML and plain text only, reads at most 1 MiB, and passes at most
20,000 characters to synthesis. HTTP, nonstandard ports, credentials in URLs, private
or special-use IP addresses, mixed public/private DNS answers, and redirects to
disallowed destinations are rejected. PDFs and office documents are not yet
read from links; document uploads and connector attachments are separate later
increments.

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

The Telegram audio path accepts complete voice notes and audio attachments up
to 20 MiB and 120 seconds when Telegram supplies a duration, only after the
private-chat sender and chat pass the same allowlist as text. The adapter
downloads the audio, converts it to WAV in memory, sends it to local Whisper
with Spanish recognition, then asks the principal model. Download and size
failures receive a short written recovery message instead of silently dropping
the request. Non-audio attachments are identified as unsupported.
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

## Voice calls from Open WebUI

`lab-secretary` serves two OpenAI-compatible audio endpoints under `/v1/audio/`, protected
by the same bearer key as the chat endpoint, so Open WebUI's call mode can use the local
speech services:

| Endpoint | Behaviour |
|---|---|
| `POST /v1/audio/transcriptions` | Multipart upload (`file`). The audio is converted to 16 kHz WAV in memory and sent to Whisper with Spanish recognition and the vocabulary prompt. Answers `{"text": ...}`. Clips of up to 14 s go to the short-utterance Whisper (`LAB_WHISPER_FAST_URL`), longer ones to the full-window instance. |
| `POST /v1/audio/speech` | JSON (`input`, up to 4,000 characters). The text is rewritten to be spoken (`SpokenText`: numbers, issue keys, ISO dates, percentages and euros in words; no Markdown, links or emojis) and synthesised by Piper. Answers a WAV file. |

Neither endpoint stores audio or text, and neither logs content; only the phase counters
`lab_secretary_voice_phase_calls_total` and durations are recorded. `LAB_PIPER_URL` must be
set for synthesis; setting it does not turn on Telegram voice replies, which stay off.
`LAB_SECRETARY_VOCABULARY` is Whisper's initial prompt, also used for Telegram voice notes.
Deploy with `playbooks/deploy-voice-call.yml` after the secretary binary; see
[the voice plan](superpowers/plans/2026-11-07-donna-voice.md) for measurements and options.

### Understanding what was said

Speech recognition is the weakest link, so the voice paths protect against its mistakes:

- **Issue keys.** Whisper writes keys as people say them, so `NormalizeIssueKeys` rewrites
  "STRAT 166", "estrat ciento sesenta y seis", "PMI guion 67", "PMI 60 y 7" and "pe eme i
  sesenta y siete" to `STRAT-166`, `PMI-67` and so on before routing. It applies to typed
  text too and only touches the prefixes in `LAB_SECRETARY_JIRA_KEYS` (default
  `PMI,STRAT,COC,SW`).
- **Echo.** A Telegram voice note first gets "Te he entendido: «…»", so a recognition mistake
  is visible instead of turning into a confusing answer.
- **Low confidence.** Whisper is asked for `verbose_json`; if the mean `avg_logprob` of the
  segments is below −1.0 (Whisper's own fallback threshold) nothing is searched. Telegram
  answers with the guess and asks for a repeat; the audio endpoint answers 422 with the same
  message. Labels Whisper invents for silence or noise ("[Música]") are removed, and a clip
  with nothing else counts as no speech.
- **Diagnostic capture (off by default).** With `-e voice_capture=true`,
  `playbooks/deploy-voice-call.yml` sets `LAB_SECRETARY_VOICE_CAPTURE_DIR` and every clip
  and its transcript is kept in `/var/lib/lab-secretary/voice-capture` (mode 0600) for 24 h
  so the recogniser can be measured on a real voice. This stores audio: turn it off again
  with `-e voice_capture=false`.

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

Durable conversation retrieval runs only when the user explicitly asks Donna to
recall a prior exchange. Ordinary questions do not receive older persisted
dialogue automatically. Short-term history remains available inside the current
chat for follow-ups. Explicit recall uses normalized Spanish keywords and
recency, with a maximum of eight exchanges supplied to the model. Approved work
knowledge uses a separate bounded lexical lookup (up to five items) and includes
validity and evidence references. It needs no vector database or extra model.
Evaluate missed synonyms and irrelevant matches against real queries first; if
lexical retrieval is insufficient, add local embeddings and an index only then.
Prometheus tracks bounded retrieval outcomes and hit counts without conversation
content.

Incoming voice transcription and spoken replies are separate features.
`lab_secretary_transcription_enabled` controls Whisper and Telegram voice-note
input; `lab_secretary_voice_enabled` controls Piper and spoken output. Current
deployment playbooks enable transcription and leave spoken replies disabled, so
a voice note is transcribed and answered with text. Turning Piper off no longer
disables Whisper. The runtime prefers `LAB_SECRETARY_TRANSCRIPTION_ENABLED` and
falls back to the legacy `LAB_SECRETARY_VOICE_ENABLED` setting when the new
variable is absent.

Build the secretary before using `playbooks/deploy-secretary-voice.yml`. Review
its `--check --diff` preview before applying it. This focused playbook manages
Whisper/Piper, updates only the three audio-mode variables in `secretary.env`
without rewriting connector credentials, deploys the binary, and checks
`/health`; on failure it restores the previous environment and binary when
backups exist.

Whisper runs CPU-only from `whisper.cpp` v1.9.5 using the multilingual
`ggml-small-q5_1.bin` model with four threads by default. Piper is optional and
uses the Spanish `es_ES-sharvard-medium` voice, female speaker 1, at length
scale `0.96`. The repository defaults keep the speaker and pace adjustable.
Both services bind only to `127.0.0.1`: Whisper on port 8083 and Piper on 8084.
Their systemd units have separate memory and CPU ceilings. Tune `lab_whisper_threads`,
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

To disable incoming transcription, set `lab_secretary_transcription_enabled:
false`; this stops and disables Whisper. To disable spoken replies, set
`lab_secretary_voice_enabled: false`; this stops and disables Piper while
leaving transcription available. Text Telegram remains active in either case.
Downloaded models and binaries remain on disk for a later re-enable. Both
switches are tracked in Git.

The engine and voice have separate licenses: [whisper.cpp](https://github.com/ggml-org/whisper.cpp/blob/d1be6fde11ac6e0407606b4e42fe72d34add8037/LICENSE)
and [Piper](https://github.com/OHF-Voice/piper1-gpl/blob/v1.8.0/LICENSE.md) are
GPL-3.0-or-later; the [Spanish voice](https://huggingface.co/rhasspy/piper-voices/tree/v1.0.0/es/es_ES/sharvard/medium)
is MIT-licensed. Review each license before redistributing a built image or
model bundle.

## Connector credentials

Run on the operator's Mac; authorize from a browser on that same machine:

```bash
python3 scripts/connect-secretary.py granola --port 18766
```

Each helper validates provider discovery, registers a client, uses PKCE S256 and
checks callback state. It prints a 20-minute authorization link and listens only
on loopback. Tokens are written with mode 0600 to
`~/.config/stratecode-lab/secretary-connectors.json`, outside Git. No changes to
`.env`, vault or SSH credentials are needed. `--discover` probes public metadata
without enrollment. A failed consent is not a connected account.

Jira credentials are not enrolled with this OAuth helper. Store the existing
read-only service-account API key, selected site `cloud_id`, and the operator's
Jira `account_id` in the private connector JSON; do not put these values in the
repository. Donna sends the key as Bearer only to the fixed
`api.atlassian.com/ex/jira/<cloud_id>/...` REST host. Jira natural-language
queries use bounded JQL and request issue summary, status, assignee, due date and
update time. Personal-task searches filter by the configured operator account
ID; they never use `currentUser()` with the service-account token, which would
silently show the service account's own assignments. The configured Jira
credential is never sent to an MCP endpoint.
If the account exposes multiple Jira sites, configure the intended `cloud_id`
explicitly; the adapter refuses to guess. Jira is read-only. Confluence is not
yet connected to Donna; its future connector must use REST/CQL directly.

The private connector JSON has this shape (keep the real token only in the
private file):

```json
{
  "jira": {
    "access_token": "<service-account-api-token>",
    "cloud_id": "<jira-cloud-id>",
    "operator_account_id": "<operator-account-id>"
  },
  "granola": { "server_url": "https://mcp.granola.ai/mcp", "access_token": "<granola-token>" }
}
```

Granola uses its OAuth connector and a hard allowlist of meeting query/detail
tools. Meeting summaries—not transcripts—enter the model context. Verify remote
tool and result schemas with the authorised account before POC acceptance.

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

Telegram webhook reception is enabled by default in `group_vars/all.yml`.
Inspect webhook metadata and the persistent queue when diagnosing delivery.
Preflight uses `playbooks/check-telegram.yml` (`getMe`, webhook metadata only).
The retired orchestrator stays stopped.

## Guardrails de datos sensibles hacia el modelo

Todo texto que Donna envía al modelo (`complete` y `completeStream`) pasa por
`guardOutbound` en `internal/secretary/guardrails.go`. Opera sobre una copia de
los turnos: el historial guardado y lo que ve el operador no cambian. Sustituye
por `[REDACTED:categoría]` solo valores validados:

| Categoría | Qué detecta | Validación |
|---|---|---|
| `private_key` | Bloques de clave privada PEM | Cabecera `BEGIN … PRIVATE KEY` |
| `token` | AWS, GitHub, Slack, Atlassian, `sk-`, JWT, token de bot de Telegram, `Bearer` | Prefijo y longitud fijos |
| `card` | Tarjetas de 13 a 19 dígitos | Luhn; se descartan repeticiones de un dígito |
| `iban` | IBAN con o sin espacios | Módulo 97 |
| `national_id` | DNI y NIE españoles | Letra de control |

Correos, nombres y teléfonos no se tocan: son justo los datos de Jira y Granola
sobre los que Donna razona. Las claves de Jira (`PMI-1234`), fechas y recuentos
no activan ningún detector porque cada patrón exige su comprobación.
`lab_secretary_guardrail_redactions_total{category}` cuenta las redacciones con
las cinco categorías cerradas y nunca incluye el contenido; con el diagnóstico
activo se registra además `guardrail.redacted.<categoría>`. No se redacta la
respuesta del modelo ni el flujo ya emitido por streaming: al recibir la entrada
limpia, el modelo no tiene el valor que repetir.

## Verification and evidence

Tests exercise allowlist rejection, private chat identity, preserved streaming,
restart history, failure handling, durable webhook queues, source fidelity,
malformed source responses, provider credential destination checks and denied
write tools. Jira tests cover REST authentication, safe JQL compilation, and
result validation; OAuth tests cover Granola issuer discovery, allowed hosts and
state validation. Execute the Docker Go harness and Python tests:

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
[Jira Cloud REST API v3](https://developer.atlassian.com/cloud/jira/platform/rest/v3/),
[JQL issue search](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-search/),
[Granola MCP](https://docs.granola.ai/help-center/sharing/integrations/mcp).


## Interpretación de órdenes mediante servicio independiente

El código y el contrato HTTP v1 viven en el repositorio hermano
`../intent-service`. Donna conserva identidad, permisos, memoria y ejecución;
el servicio solo devuelve `goal`, operación semántica y capacidades de lectura
sugeridas. Una petición `change_request` nunca autoriza una escritura. Donna
comprueba cada capacidad contra su registro actual y, si el servicio falla,
prueba primero el enrutado local y clasifica con el modelo solo una intención
que siga siendo ambigua. Los comandos `/jira`, `/reuniones` y `/resumen` siguen
siendo directos.

El perfil `donna-v1` y el binario se despliegan de forma separada. Antes de
desplegar Donna o el playbook principal, compila y prueba el servicio en su
repositorio, y despliega el artefacto con la suma SHA-256 verificada:

```bash
cd ../intent-service
bash scripts/test.sh
bash scripts/test-contract.sh
bash scripts/build.sh
cd ../lab
LAB_INTENT_BINARY="$PWD/../intent-service/dist/intent-service-linux-amd64" \
LAB_INTENT_SHA256="$(shasum -a 256 ../intent-service/dist/intent-service-linux-amd64 | cut -d ' ' -f1)" \
LAB_INTENT_PROFILE="$PWD/../intent-service/profiles/donna-v1.json" \
ansible-playbook playbooks/deploy-intent-service.yml
./scripts/build-orchestrator-go.sh
LAB_INTENT_MODE=enabled ansible-playbook playbooks/deploy-secretary-intent.yml
```

El servicio escucha solo en `127.0.0.1:8091`; `/health` y `/metrics` exponen
estado y contadores agregados sin texto de usuario. El token se genera en el
host, en `/etc/lab-intent-service/client.env`, con permisos de root. El
intérprete mantiene una ruta determinista y se abstiene ante una fuente
ambigua. Donna intenta primero ese servicio; si falla o se abstiene, aplica su
enrutado local y usa el clasificador del modelo solo cuando la petición sigue
siendo ambigua. Si tampoco puede resolverla, pide una aclaración breve. Las
acciones negadas no consultan conectores ni ejecutan cambios.
Si una petición explícita compara varias fuentes y el intérprete devuelve solo
una, Donna añade las capacidades que el enrutador determinista reconoce en esa
misma consulta, respetando exclusiones como «solo Granola».

En respuestas que combinan Granola y Jira, las instrucciones de síntesis piden
enlazar una acción con una incidencia solo por clave compartida o por
coincidencia concreta de tarea, acción y responsable. Un nombre, proyecto o
fecha común no basta; sin evidencia directa, pide presentar las fuentes
separadas. Es una regla del prompt, no una validación determinista de cada
frase generada. El estado actual siempre procede de Jira.
Por defecto responde de forma concisa y evita resaltar información no pedida.
En Telegram una respuesta larga se divide por párrafos o frases completas; en
Open WebUI sigue siendo una respuesta única y breve.

Para volver temporalmente al enrutado anterior sin parar el servicio,
ejecuta `LAB_INTENT_MODE=shadow ansible-playbook playbooks/deploy-secretary-intent.yml`.
El playbook principal conserva `enabled` por defecto mediante `group_vars/all.yml`;
`LAB_INTENT_MODE=shadow` permite una ejecución de diagnóstico.

La verificación conectada, sin contenido privado en Git, está resumida en
[validación del intérprete](validation/2026-10-27-intent-service/README.md).

## Diagnóstico detallado de conversaciones e inferencia

La configuración única `LAB_DEBUG_ENABLED` activa o desactiva las trazas
detalladas de Donna, la pasarela de inferencia, el observador nativo y el
registrador de recursos del host. Por defecto es `false`; acepta `true` o
`false` (sin distinguir mayúsculas y minúsculas, y quitando espacios exteriores).
Para activarlas después de instalar los colectores, ejecuta
`LAB_DEBUG_ENABLED=true ansible-playbook -i inventory.yml playbooks/toggle-debug-tracing.yml`.
La primera vez, instala el observador nativo con
`LAB_DEBUG_ENABLED=true ansible-playbook -i inventory.yml playbooks/deploy-deep-inference-trace.yml`.
Para desactivarlas, ejecuta
`LAB_DEBUG_ENABLED=false ansible-playbook -i inventory.yml playbooks/toggle-debug-tracing.yml`.
Antes, compila los artefactos con `./scripts/build-orchestrator-go.sh`. El
playbook instala Donna y la pasarela actuales y solo cambia esos servicios y los
colectores; no vuelve a desplegar ni reconfigura el modelo.

Con el modo desactivado siguen disponibles los logs de ciclo de vida y error,
health checks, métricas agregadas, journald persistente y protecciones de
recuperación (kdump, watchdog y RAS). Los registros de aprendizaje supervisado
son independientes y conservan su retención configurada.

Cada traza de interacción guarda también `route_decision` y, cuando procede,
`fallback_reason`. Son etiquetas limitadas como `intent_service_multi`,
`local_fallback`, `service_unavailable` o `source_missing`; nunca incluyen la
consulta, la respuesta ni el texto de error. Con diagnósticos detallados activos,
`conversation-steps.jsonl` registra los eventos `router.decision` y
`router.fallback` con esas mismas etiquetas. Esto permite distinguir una ruta
normal de un fallback sin registrar contenido de conversación.

## Bitácora física para diagnosticar apagados

Con `LAB_DEBUG_ENABLED=true`, cada conversación registra checkpoints incrementales de inicio/fin en
`/var/lib/lab-secretary/diagnostics/conversation-steps.jsonl` (0600, directorio0700).
Se sincroniza cada registro antes de continuar. Rotación:16MiB más cuatro copias.
`trace_id` correlaciona el mensaje; `attempt_id` distingue entregas y reintentos;
`boot_id` permite cruzarlo con flight recorder y journal. La bitácora incluye
espera del servicio, contexto, herramientas, límites HTTP, modelo, persistencia
y entrega Telegram. No contiene mensajes, prompts, respuestas ni credenciales.
Una operación con inicio sin cierre identifica el último progreso registrado,
no demuestra qué componente apagó el host. Buscar también
`conversation_diagnostic_write_error` en el journal. Fsync no garantiza el último
instante ante un corte físico. Evidencia y pruebas en
[conversation checkpoints](validation/2026-10-22-conversation-checkpoints/README.md).

## Mapeos guiados de Donna

La búsqueda de personas y proyectos consulta Jira REST en vivo. Las coincidencias
con el mismo nombre se conservan como candidatos diferentes por su ID de Jira;
un nombre visible nunca basta para decidir. Jira no siempre expone email y la
integración actual de Granola devuelve títulos, fechas y resúmenes, no IDs de
asistentes ni emails. Donna no completa esos datos por inferencia: el operador
puede proponer un email para una persona, o un alias para una persona o proyecto
ya confirmado.

Cada propuesta queda pendiente. Donna muestra un ID de candidato y la huella
completa calculada desde su contenido. Confirma con `/mapeo confirmar ID
HUELLA_COMPLETA` o descarta con `/mapeo descartar ID HUELLA_COMPLETA`. Ambos
valores deben coincidir y el candidato debe pertenecer al operador, canal y conversación actuales.
Cambiar el contenido genera otra huella y la confirmación anterior deja de valer.
Las propuestas de otra conversación no aparecen en el comando `/mapeo pendientes`.
Los mapeos aprobados se leen desde el servicio de identidad y alimentan la
resolución exacta de personas y proyectos en consultas posteriores. En
consultas de tareas por persona, Donna usa primero el `accountId` de Jira de un
mapeo aprobado; si no hay correspondencia inequívoca, recurre a la búsqueda
activa de usuarios de Jira y conserva el error ante ambigüedad.

Para compilar y desplegar el servicio (el playbook reinicia Donna para cargar la
integración):

```bash
./scripts/build-donna-identity.sh
ansible-playbook -i inventory.yml playbooks/deploy-donna-identity.yml
```

El servicio escucha solo en `127.0.0.1:8092`, usa un token generado en el host y
guarda mapeos en `/var/lib/donna-identity` con acceso privado. Verifica
`http://127.0.0.1:8092/healthz` y el endpoint de salud de `lab-secretary`.
No edites `.env`, `group_vars/vault.yml` ni `ssh/` para crear la clave.

Para retirar la integración manteniendo los mapeos para una restauración futura:
detén y deshabilita `donna-identity`, elimina la línea
`EnvironmentFile=/etc/lab-secretary/identity.env` de
`/etc/systemd/system/lab-secretary.service`, borra solo
`/etc/lab-secretary/identity.env`, recarga systemd y reinicia `lab-secretary`.
Conserva `/var/lib/donna-identity`; bórralo solo si se decide eliminar también
los mapeos almacenados.

## Seguimientos automáticos de Granola

El microservicio `donna-granola-events` recibe eventos firmados
`note.generated` de la suscripción `personal`, recupera el resumen generado y
entrega un mensaje corto con acciones explícitas como candidatas a Jira. Nunca
crea ni modifica incidencias. Telegram recibe el aviso en el chat privado
allowlisted; Open WebUI conserva el mismo aviso por operador y lo muestra en su
siguiente conversación autenticada.

La cola persiste antes de responder `202`, deduplica por ID de evento y limita
los reintentos de lectura y entrega. La nota se consulta solo con
`GET /v1/notes/{id}`; no se solicita ni se almacena la transcripción. Consulta
[seguimientos de Granola](granola-followups-operation.md) para los requisitos
de Business/Enterprise, el alta con ámbito `personal`, configuración privada,
verificación, fallos y rollback. La implementación local no confirma todavía
la suscripción real, el acceso de la cuenta ni la recepción desde Internet.

Para compilar los dos binarios afectados y ejecutar las pruebas dirigidas:

```bash
./scripts/build-donna-granola-events.sh
```

El despliegue dedicado es `playbooks/deploy-donna-granola-events.yml`. Primero
debe desplegarse con la clave de webhook vacía; el callback responde `503`
hasta guardar la clave que Granola devuelve una sola vez al crear el endpoint.
Después de guardarla en el `.env` privado, vuelve a ejecutar el playbook.

## Propuestas de tareas Jira (implementadas localmente, pendientes de activación)

La escritura se separa en el servicio `donna-jira-actions`, que escucha solo
en `127.0.0.1:8094` y recibe un OAuth access token propio. El modelo y
`lab-secretary` no reciben la credencial de Jira. El servicio solo admite
crear una tarea o añadir un comentario a una tarea; para coincidencias, el
comentario conserva el texto fuente y el enlace de Granola. No cambia campos
de una tarea existente.

Granola conserva localmente el ID de nota, el enlace HTTPS disponible y el
fragmento literal de acción. Cuando falta el enlace, Donna lo pregunta y pide
al operador comprobar que abre esa nota; la URL se valida por formato, no se
visita ni se contrasta independientemente.
Los candidatos duran como máximo 30 días después de completar su entrega. La
propuesta revisa acción, proyecto, tipo, resumen y posibles coincidencias. El
umbral configurado es 90%, con al menos 30 ejemplos correctos por dimensión;
el porcentaje que redacte el modelo no cuenta como evidencia. Todavía no hay
un conjunto de calibración desplegado, por lo que las dimensiones inferidas
que no confirme el operador quedan en pregunta. Una única opción de proyecto o
tipo permitida se puede usar sin ambigüedad. La vista previa se liga al usuario,
canal, conversación, huella exacta del contenido y caducidad de diez minutos.
El botón **Crear en Jira** o **Actualizar tarea** es la única autorización de
escritura. **Descartar propuesta** cancela cualquier borrador sin tocar Jira;
en una coincidencia dudosa, **No es la misma tarea** cancela el comentario y
deja explícito que no se ha encontrado la misma tarea.

La entrada manual de candidatos está conectada localmente desde notificaciones
de Granola. Los adaptadores están implementados para callbacks de Telegram,
aclaraciones en ambos canales y Actions de Open WebUI. El archivo fuente para importación manual es
`roles/open_webui/files/donna_jira_actions.py`; hay que asociarlo solo al
modelo de Donna y configurar sus válvulas con el ID del único administrador
autorizado, el operador allowlisted y el token privado del servicio.

**Estado de activación:** el código local ya permite iniciar una propuesta desde
una acción de Granola y continuar una pregunta en el mismo chat. No se ha
desplegado ni asociado la Action en Open WebUI, ni hay credenciales Jira
configuradas aquí. No hace escrituras en Jira durante la preparación: solo el
botón de confirmación ejecuta la creación o el comentario exacto. Un resultado
incierto consume la autorización y no se reintenta; el operador debe inspeccionar
Jira antes de preparar otra propuesta. El registro de tiempo trabajado queda
para una fase posterior.

La validación local se ejecuta con `./scripts/test-orchestrator-go.sh` y las
pruebas Python de la Action. El despliegue dedicado se prepara con
`playbooks/deploy-donna-jira-actions.yml`; requiere `DONNA_JIRA_CLOUD_ID`,
`DONNA_JIRA_WRITE_TOKEN` OAuth y `DONNA_JIRA_ACTION_SERVICE_TOKEN` exportados
fuera del repositorio, junto con las allowlists `DONNA_JIRA_ALLOWED_PROJECTS` y
`DONNA_JIRA_ALLOWED_ISSUE_TYPES`. La importación y asignación de la Action en
Open WebUI sigue siendo un paso explícito de administración.
