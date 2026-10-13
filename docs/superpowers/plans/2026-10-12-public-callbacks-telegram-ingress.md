# Public Callbacks and Telegram Ingress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a narrowly scoped HTTPS origin for secretary OAuth callbacks and Telegram webhooks through the home router without publishing Open WebUI or internal model services.

**Architecture:** Route `assistant.stratecode.com` through the existing Route53 DDNS name to a dedicated Nginx vhost on the lab. Nginx proxies three exact routes to loopback; OAuth callbacks traverse short-lived loopback-only SSH reverse forwards, while Telegram updates enter a durable, deduplicated queue in `lab-secretary`.

**Tech Stack:** Go, Python 3 standard library, Ansible, Nginx, Certbot Route53 DNS-01, AWS CLI Route53, Telegram Bot API, Bats.

**Spec:** `docs/superpowers/specs/2026-10-12-public-callbacks-telegram-ingress-design.md`

## Global Constraints

- Route only external IPv4 TCP 443 from the router to `192.168.0.85:443`; do not use UPnP or forward ports 80, 22, or application ports.
- Expose only `POST /telegram/webhook`, `GET /oauth/jira/callback`, and `GET /oauth/granola/callback` on `assistant.stratecode.com`; reject other paths, methods, and unknown TLS hosts.
- Preserve existing LAN/VPN access lists for chat, Cockpit, and monitoring; never proxy the public vhost to Open WebUI, model, telemetry, or administrative interfaces.
- Keep secretary, model, telemetry, and OAuth callback listeners on loopback. Keep reverse SSH forwards bound to lab loopback.
- Preserve OAuth PKCE S256, state checks, provider-host allowlists, token validation, and private token storage. Callback access logs must omit query strings.
- Telegram modes are mutually exclusive: `disabled`, `polling`, or `webhook`. Keep `polling` until public acceptance passes; persist updates before acknowledging and deduplicate by `update_id`.
- Preserve pending Telegram updates during webhook activation and rollback; never set `drop_pending_updates=true`.
- Keep webhook secrets and OAuth/provider credentials out of Git, `.env` changes, vault changes, and deployment output.
- Do not configure IPv6 ingress. Verify router IPv6 exposure before acceptance.
- Use Route53 only for the assistant CNAME and the existing DNS-01 certificate flow; add no cloud compute, load balancer, relay, or tunnel service.

## Review Focus

- **Bad Telegram secret, wrong method, malformed JSON, or oversized request:** reject before queueing; cover with `TestTelegramWebhookRejectsInvalidRequests` in Task 1.
- **Duplicate or replayed `update_id`, including after restart:** acknowledge without a second inference; cover with `TestFileUpdateQueuePersistsAndDeduplicatesAcrossRestart` in Task 1.
- **Crash after durable acceptance or while sending a reply:** recover the queued update with at-least-once semantics; cover with `TestTelegramWebhookWorkerRetriesAfterRestart` in Task 1.
- **OAuth callback with missing, repeated, mismatched state, or provider error:** reject and keep code/query data out of logs; cover with Task 3 OAuth tests and Task 4 sanitized-log contract test.
- **Unknown host/path/method and external access to existing private vhosts:** reject while preserving the current chat ACL; cover with Task 4 static/Nginx checks and Task 5 external probes.

---

### Task 1: Durable Telegram webhook queue and request handler

**Files:**
- Create: `internal/secretary/telegram_webhook.go`
- Create: `internal/secretary/telegram_webhook_test.go`
- Modify: `internal/secretary/telegram.go`

**Interfaces:**
- Produce `TelegramUpdateQueue` with `Enqueue(Update) (bool, error)`, `Next(context.Context) (Update, error)`, and `Complete(int64) error`.
- Produce `OpenTelegramUpdateQueue(stateDir string) (*FileTelegramUpdateQueue, error)` using the service state directory.
- Produce `TelegramWebhook.ServeHTTP(http.ResponseWriter, *http.Request)`.
- Refactor the existing `Bot` update handling into `processUpdate(context.Context, Update) error` so polling and webhook workers enforce identical private-chat allowlisting and reply behavior.
- Queue limits: reject bodies over 1 MiB with 413; cap pending updates at 128 and return 503 when full; use durable pending/completed records and retain completed IDs for at least 48 hours to suppress Telegram retries.

- [x] **Step 1: Add failing tests** for `TestTelegramWebhookRejectsInvalidRequests`, `TestFileUpdateQueuePersistsAndDeduplicatesAcrossRestart`, `TestTelegramWebhookWorkerProcessesAllowedUpdate`, and `TestTelegramWebhookWorkerRetriesAfterRestart`.
- [x] **Step 2: Run the tests and confirm they fail** with missing webhook/queue behavior.

Run: `go test ./internal/secretary -run 'TestTelegramWebhook|TestFileUpdateQueue' -count=1`

Expected: FAIL because the queue and webhook handler do not exist.

- [x] **Step 3: Implement the durable queue** with atomic private files under `StateDirectory`, FIFO selection by `update_id`, duplicate detection across pending and completed records, and recovery of pending work after process restart.
- [x] **Step 4: Implement the HTTP handler** to accept POST only, compare `X-Telegram-Bot-Api-Secret-Token` in constant time before reading JSON, bound the request body to 1 MiB, queue before returning 200, and return 503 if durable acceptance fails.
- [x] **Step 5: Extract shared update processing** into `Bot.processUpdate`; add the webhook worker that retries incomplete updates and records completion only after Telegram accepts the reply.
- [x] **Step 6: Run targeted and full secretary tests.**

Run: `go test ./internal/secretary -count=1`

Expected: PASS, including existing polling behavior and the new queue/replay tests.

- [x] **Step 7: Commit** the queue, handler, shared processor, and tests.

### Task 2: Mutually exclusive secretary Telegram modes

**Files:**
- Modify: `cmd/lab-secretary/main.go`
- Create: `cmd/lab-secretary/main_test.go`
- Modify: `group_vars/all.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`

**Interfaces:**
- Read `LAB_SECRETARY_TELEGRAM_MODE` with allowed values `disabled`, `polling`, and `webhook`; default to `polling` to preserve current behavior.
- In `webhook` mode, mount `TelegramWebhook` at `/telegram/webhook` on the existing loopback HTTP server and run its worker; do not call `getUpdates`.
- In `polling` mode, run the current polling loop and do not accept webhook updates. In `disabled` mode, start neither Telegram consumer.
- Read `LAB_TELEGRAM_WEBHOOK_SECRET` only in webhook mode. Store the generated value in a root-owned mode-0600 host environment file; never include it in task output or the repository.

- [x] **Step 1: Add failing mode tests** `TestParseTelegramMode`, `TestWebhookModeDoesNotStartPolling`, and `TestDisabledModeStartsNoTelegramConsumer`.
- [x] **Step 2: Run the tests and confirm invalid/mutually active modes fail.**

Run: `go test ./cmd/lab-secretary -run 'Test(ParseTelegramMode|WebhookMode|DisabledMode)' -count=1`

Expected: FAIL until mode selection and startup wiring exist.

- [x] **Step 3: Implement and validate mode parsing** in `cmd/lab-secretary/main.go`; reject unknown values at startup without printing secrets.
- [x] **Step 4: Wire the three modes** to the shared loopback HTTP server and the existing polling bot without starting both consumers.
- [x] **Step 5: Update Ansible runtime configuration** to preserve polling by default, generate the webhook secret idempotently on the host, load it through a root-protected systemd environment file, and avoid changing `.env` or `group_vars/vault.yml`.
- [x] **Step 6: Run Go tests, build, and secretary playbook syntax checks.**

Run: `./scripts/test-orchestrator-go.sh && ./scripts/build-orchestrator-go.sh && ansible-playbook --syntax-check playbooks/deploy-secretary.yml`

Expected: PASS; the deployed mode remains `polling`.

- [x] **Step 7: Commit** mode wiring, tests, and Ansible runtime configuration.

### Task 3: Public OAuth redirect URIs and temporary reverse tunnel

**Files:**
- Modify: `scripts/connect-secretary.py`
- Modify: `scripts/test-secretary-oauth.py`
- Modify: `docs/secretary-operation.md`

**Interfaces:**
- Add CLI argument `--public-origin`; when absent, preserve the current loopback redirect URI.
- When set, require exactly `https://assistant.stratecode.com` with no path, credentials, port, query, or fragment. Build provider callbacks as `/oauth/{provider}/callback` while the local helper continues to listen on `127.0.0.1:<port>/callback`.
- Preserve discovery endpoint validation, PKCE S256, state validation, read-only Jira scopes, and private token output.

- [ ] **Step 1: Add failing OAuth tests** for the exact Jira and Granola public callback URIs, callback path mapping, rejected origins, mismatched/repeated state, and unchanged default loopback behavior.
- [ ] **Step 2: Run `python3 scripts/test-secretary-oauth.py` and confirm the new public-origin tests fail.**
- [ ] **Step 3: Implement `--public-origin` and a provider-specific callback URI helper**; keep the existing local callback listener bound to loopback.
- [ ] **Step 4: Document the temporary command** using reverse forwards bound to `127.0.0.1` on the lab and the existing local helper ports 18765/18766. Document that provider consent is successful only after a read-only query.
- [ ] **Step 5: Run OAuth tests and Python syntax validation.**

Run: `python3 scripts/test-secretary-oauth.py && python3 -m py_compile scripts/connect-secretary.py scripts/test-secretary-oauth.py`

Expected: PASS; invalid public origins and invalid callback state are rejected.

- [ ] **Step 6: Commit** OAuth URI support, tests, and operator instructions.

### Task 4: Route53 alias, TLS certificate, and isolated Nginx vhost

**Files:**
- Modify: `.env.example`
- Modify: `group_vars/all.yml`
- Modify: `roles/route53_ddns/tasks/main.yml`
- Modify: `roles/monitor/tasks/main.yml`
- Create: `tests/properties/prop_public_ingress_contract.bats`

**Interfaces:**
- Add `LAB_ASSISTANT_DOMAIN=assistant.stratecode.com` and `lab_assistant_domain`; leave `.env` untouched.
- Ensure Route53 has `assistant.stratecode.com CNAME -> vpn-lab.stratecode.com` in the existing hosted zone. Detect and fail on a conflicting record type instead of replacing it silently.
- Add the assistant hostname to the existing Certbot DNS-01 SAN set.
- Create a dedicated Nginx vhost with exact routes: POST `/telegram/webhook` to `127.0.0.1:8082/telegram/webhook`; GET `/oauth/jira/callback` to `127.0.0.1:18765/callback`; GET `/oauth/granola/callback` to `127.0.0.1:18766/callback`.
- For callback paths, log only method, normalized path, and status. Reject unknown TLS hosts, paths, and methods. Preserve every existing private vhost ACL.

- [x] **Step 1: Add failing Bats contract tests** for the CNAME target, certificate SAN, exact Nginx routes, loopback upstreams, sanitized logs, and rejection of unlisted paths.
- [x] **Step 2: Run `bats tests/properties/prop_public_ingress_contract.bats` and confirm it fails against the current role.**
- [x] **Step 3: Implement the idempotent Route53 CNAME reconciliation** in the existing role without adding work to the five-minute A-record refresh loop.
- [x] **Step 4: Add the hostname to Certbot DNS-01 and implement the dedicated Nginx TLS vhost** with exact path/method handling and an unknown-host TLS rejection.
- [x] **Step 5: Run the Bats contract and all affected Ansible syntax checks.**

Run: `bats tests/properties/prop_public_ingress_contract.bats && for playbook in playbooks/deploy-lab-surface.yml playbooks/deploy-principal-model.yml; do ansible-playbook --syntax-check "$playbook" || exit; done`

Expected: PASS; current chat/monitoring ACLs and the DDNS A-record updater remain unchanged.

- [x] **Step 6: Commit** DNS, certificate, Nginx, and contract-test changes.

### Task 5: Controlled webhook activation and rollback

**Files:**
- Create: `playbooks/enable-telegram-webhook.yml`
- Create: `playbooks/restore-telegram-polling.yml`
- Create: `tests/properties/prop_telegram_webhook_playbooks.bats`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/principal-model-operation.md`
- Modify: `group_vars/all.yml`

**Interfaces:**
- `enable-telegram-webhook.yml` deploys the secretary in `webhook` mode, verifies local health, and calls Telegram `setWebhook` with the exact public URL, secret header configuration, and `drop_pending_updates=false`. Suppress all secret-bearing task output with `no_log: true`.
- `restore-telegram-polling.yml` calls `deleteWebhook` with pending updates preserved before deploying `polling` mode.
- The normal deployment remains on polling until the operator completes the router and public-route checks.

- [x] **Step 1: Add failing/static playbook contract checks** in `tests/properties/prop_telegram_webhook_playbooks.bats` for deployment order, exact public URL, `drop_pending_updates=false`, rollback order, and `no_log: true` on all Telegram token/secret tasks.
- [x] **Step 2: Run the new contract checks and confirm they fail.**
- [x] **Step 3: Implement explicit activation and rollback playbooks** without changing the normal deployment's polling default.
- [ ] **Step 4: Document manual router configuration:** reserve `192.168.0.85`, forward IPv4 TCP 443 only to `192.168.0.85:443`, keep ports 80/22/application ports closed, and verify IPv6 ingress is absent. IPv4 rule documented; external IPv6 exposure still needs verification.
- [x] **Step 5: Run Bats tests, Go harness/build, Python checks, and syntax checks for all affected playbooks.**

Run: `./scripts/test-orchestrator-go.sh && ./scripts/build-orchestrator-go.sh && python3 scripts/test-secretary-oauth.py && python3 -m py_compile scripts/connect-secretary.py scripts/test-secretary-oauth.py && bats tests/properties/prop_public_ingress_contract.bats tests/properties/prop_telegram_webhook_playbooks.bats && for playbook in playbooks/deploy-secretary.yml playbooks/deploy-lab-surface.yml playbooks/deploy-principal-model.yml playbooks/enable-telegram-webhook.yml playbooks/restore-telegram-polling.yml; do ansible-playbook --syntax-check "$playbook" || exit; done`

Expected: all checks PASS before live deployment.

- [x] **Step 6: Deploy DNS, certificate, Nginx, and secretary webhook support while polling remains active.** Verify the certificate, vhost, and secretary route locally before enabling router ingress.
- [ ] **Step 7: Configure the router's manual forward** only after the host vhost is deployed. From an external network verify TCP 443, the certificate, exact routes, current chat ACLs, and that model/telemetry/application ports remain inaccessible. If 443 still times out, stop and diagnose router policy versus ISP filtering. Current check: TCP 443 timed out from six external nodes; ingress activation is blocked pending router/ISP diagnosis.
- [ ] **Step 8: Test OAuth callback forwarding** with both helpers and invalid-state probes. Complete each provider separately; if a provider rejects the custom URI, leave that provider unconnected and continue with Telegram.
- [ ] **Step 9: Activate Telegram webhook** only after a valid secret is enforced and a synthetic update is durably queued. Verify `getWebhookInfo`, deliver one allowlisted private message/reply, and test duplicate delivery and rollback. Change the committed default mode to `webhook` only after this live acceptance succeeds, so ordinary redeploys preserve the active transport.
- [x] **Step 10: Save sanitized live evidence** under `docs/validation/2026-10-12-public-ingress/`; do not include tokens, codes, message bodies, connector data, or private IP/secret-bearing environment output.
- [x] **Step 11: Commit** the runbooks, final operational state, and sanitized evidence (`9e51a78`).

## Self-Review

- **Spec coverage:** DNS alias and DNS-01 certificate are in Task 4; router forwarding and IPv6 checks are in Task 5; exact Nginx routing and private-vhost isolation are in Task 4; OAuth callback/tunnel and provider acceptance are in Tasks 3 and 5; Telegram queue, mode exclusivity, secret validation, activation, no-drop rollback, and at-least-once behavior are in Tasks 1, 2, and 5; credential/logging constraints are in all relevant tasks.
- **Step scan:** each test, implementation, deployment, and commit action has a concrete file set or command and a checkable result. Live external probes are explicitly gated before Telegram mode changes.
- **Type consistency:** `Update` is the existing `internal/secretary.Update`; the queue and handler signatures introduced in Task 1 are the same interfaces consumed by Task 2.
- **Review Focus:** all five listed input/failure classes have named tests or explicit external acceptance checks in their owning tasks.
- **Proportion:** five independently reviewable increments align with the Go queue, runtime mode, OAuth helper, public edge, and live cutover; none requires publishing unrelated channels or services.
