# Secretary Telegram Implementation Plan

Goal: reconnect the existing Telegram bot to the principal through a shared,
loopback-only assistant service, then connect Jira and Granola for a read-only POC.
Spec: docs/research/2026-10-11-ai-secretary-tools-skills.md and the approved
multi-channel architecture in this conversation. Execute directly in this chat.

Architecture: a small Go service owns channel identity, bounded Telegram history,
shared instructions and read-only connector commands. Open WebUI uses the same
service through its OpenAI-compatible chat endpoint; the existing telemetry
proxy remains between the service and llama.cpp. Production credentials are
reused from the environment and never checked into Git.

- [x] Telegram getMe/webhook preflight without emitting credentials.
- [x] Tests first: private allowlisted identity, bounded history, upstream errors,
  OpenAI streaming pass-through, polling checkpoint and connector source fidelity.
- [x] Implement internal/secretary and cmd/lab-secretary; plain conversation,
  /start, /help, /new, /status and read-only /jira, /reuniones, /resumen.
- [x] Add one repository-managed short secretary instruction file and deployment
  role/playbook; no legacy orchestrator activation, public inference or shell tools.
- [x] Run repository Go test/build harnesses and deploy the Telegram increment.
- [ ] Verify bot delivery and actual operator message/reply; verify restart and
  unauthorised identity handling through tests. Record redacted evidence.
- [ ] Configure Jira/Granola only using dedicated authorised credentials. If
  unavailable, prepare the tested connectors and identify the authentication
  dependency explicitly; do not report them connected.
- [ ] Update master plan/operations, commit changes, inspect clean Git state.

POC success: a permitted operator can ask for a reply and consult Jira/Granola
with source IDs. Missing sources are explicit; no task creation, email sending,
calendar writes, general tool autonomy or voice implementation in this increment.
Telegram bot setup requires user interaction when starting the bot or authorising
new accounts. Private histories and polling state live under systemd StateDirectory.

## Execution state — October 12

Shared service deployed on 8082; Open WebUI routes to it with authentication and
streaming preserved. Fresh Go test/build, race tests and six OAuth helper tests
pass. Real synthetic streaming: first token 0.620 s, total 0.854 s. Model/tool
permissions remain read-only. Telegram delivery test awaits migration decision;
Jira REST/JQL and Granola queries await their connector credentials and real
read-only acceptance checks.

Telegram preflight passed getMe/no-webhook, but polling while our service was
stopped still returned 409. The operator Mac has Hermes gateway PID 3771 under
`ai.hermes.gateway` with two Telegram API connections. It is a candidate, not yet
confirmed by stopping it or comparing credentials. Operator approved stopping this separate gateway. Launchd reports it unloaded and disabled from auto-start; a standalone long poll returned HTTP 200 with zero pending updates. The lab service then logged `telegram_ready`; no 409 has recurred.

One initial build retry was needed after an existing telemetry streaming test
failed with a closed upstream connection; the final full build passes and that
test passed 20 repetitions. No unrelated telemetry implementation change made.
Evidence preserves the failure and the later checks; no fix is claimed.
