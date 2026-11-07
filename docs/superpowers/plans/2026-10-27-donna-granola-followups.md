# Donna Granola Follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Notify the operator when Granola generates a personal meeting note, send its concise summary, and suggest grounded Jira tasks without creating them.

**Architecture:** Add an independently deployable Granola event service with signed webhook ingress, durable deduplication/queueing, and a bounded Granola API reader. Deliver through the secretary's authenticated notification interface; Telegram pushes immediately and Open WebUI keeps a private notification for the next authenticated conversation if it cannot push.

**Tech Stack:** Go, standard `net/http`, HMAC validation, durable file queue following the existing Telegram webhook queue pattern, Granola API.

**Spec:** `docs/superpowers/specs/2026-10-27-donna-guided-mappings-granola-jira-actions-design.md`; reuse the webhook processing requirements in `docs/superpowers/specs/2026-10-23-donna-diagnostics-granola-webhooks-design.md` without implementing its diagnostics switch.

## Global Constraints

- Initial scope is personal `note.generated` events; do not expand to shared folders or note edits.
- Verify webhook signatures over the raw body and persist/deduplicate before acknowledging.
- Use Granola's generated summary; do not include transcripts or invent owners, dates, or tasks.
- Suggest Jira actions only; every eventual Jira write is handled by the separately approved Jira action flow.
- Keep API and signing secrets in private Ansible configuration, not Git or diagnostic output.
- Preserve unrelated existing worktree changes.

## Review Focus

- Invalid signature or stale timestamp; test reject without queueing.
- Duplicate event after restart; test only one notification is sent.
- Granola note not yet readable; test bounded retry without losing the event.
- Summary lacks owner/date; test notification marks them absent rather than inferring them.
- Open WebUI cannot push proactively; test notification remains scoped to the operator and is delivered on next authenticated conversation.

---

### Task 1: Signed webhook and durable event queue

**Files:**
- Create: `cmd/donna-granola-events/main.go`
- Create: `internal/donnagranola/webhook.go`
- Create: `internal/donnagranola/webhook_test.go`
- Create: `internal/donnagranola/queue.go`
- Create: `internal/donnagranola/queue_test.go`

**Interfaces:**
- Produce `GranolaEvent{EventID, NoteID, EventType, Scope, ReceivedAt}` and `EventQueue.Enqueue(context.Context, GranolaEvent) (bool, error)`, where `bool` reports first delivery versus duplicate. Granola's payload has no scope field, so the accepted event is tagged with the endpoint's configured `personal` scope; reject any unexpected payload scope.
- Produce `VerifySignature(rawBody []byte, webhookID, signature, timestamp, secret string, now time.Time) error` and `NewWebhookHandler(EventQueue, SecretProvider, Clock) http.Handler` for `POST /webhooks/granola` and `GET /healthz`.

- [x] **Step 1: Write failing tests** for valid event acceptance, invalid HMAC, stale timestamp, unexpected scope rejection, duplicate event, malformed body, and body-size limit.
- [x] **Step 2: Run `go test ./internal/donnagranola -run 'TestWebhook|TestQueue' -count=1`** and confirm expected failures before implementation.
- [x] **Step 3: Implement signature verification and event parsing** over the raw request bytes; accept only personal `note.generated` events and reject before enqueue on invalid input.
- [x] **Step 4: Implement a private durable queue** with event ID deduplication across restart and atomic persistence.
- [x] **Step 5: Run `go test ./internal/donnagranola -run 'TestWebhook|TestQueue' -count=1`** and confirm all webhook/queue tests pass.

### Task 2: Granola retrieval, grounded message, and notification delivery

**Files:**
- Create: `internal/donnagranola/client.go`
- Create: `internal/donnagranola/client_test.go`
- Create: `internal/donnagranola/worker.go`
- Create: `internal/donnagranola/worker_test.go`
- Create: `internal/secretary/notifications.go`
- Create: `internal/secretary/notifications_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/web.go`

**Interfaces:**
- Produce `GranolaNoteReader.GetNote(context.Context, string) (GranolaNote, error)` with note ID, title, generated summary, date, and explicit action items only.
- Produce `NotificationSink.Deliver(context.Context, OperatorContext, Notification) error`; Telegram sends to the authenticated private chat; Open WebUI stores a private pending notification if no supported proactive delivery API exists.
- Produce `FormatGranolaNotification(GranolaNote) string`; include a concise summary and separately labelled candidate tasks, preserving missing owners/dates as missing.

- [x] **Step 1: Write failing client tests** for successful note retrieval, API authorization failure, and retryable not-yet-readable responses using an HTTP test server.
- [x] **Step 2: Run `go test ./internal/donnagranola -run 'TestGetNote' -count=1`** and confirm failures before adding the client.
- [x] **Step 3: Implement the bounded Granola API client** with fixed provider host, request timeout, response-size limit, and no transcript endpoint calls.
- [x] **Step 4: Write failing worker tests** for message content, missing owners/dates, retry, restart, and duplicate-notification suppression.
- [x] **Step 5: Implement the event worker** to fetch only note summary fields, format a concise Spanish message, and mark an event complete only after durable delivery.
- [x] **Step 6: Implement the secretary notification sink** with Telegram private-chat delivery and Open WebUI operator-scoped pending delivery on next authenticated request.
- [x] **Step 7: Run `go test ./internal/donnagranola ./internal/secretary`** and confirm worker, notification, and existing channel tests pass.

### Task 3: Deployment, docs, and acceptance

**Files:**
- Create: `roles/donna_granola_events/`
- Create: `playbooks/deploy-donna-granola-events.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/architecture/master-plan.md`

- [x] **Step 1: Add Ansible service deployment** with webhook URL, private signing secret, Granola API key, state directory, health check, and restart policy.
- [x] **Step 2: Add documented enrollment and rollback steps**; distinguish required Granola plan/scopes and public HTTPS reachability from code test results.
- [x] **Step 3: Add synthetic end-to-end test** that sends a signed event, fetches a fake note, checks one notification per event, and proves no Jira write is called.
- [x] **Step 4: Run `go test ./internal/donnagranola ./internal/secretary ./cmd/lab-secretary` and `git diff --check`**; expected result is all affected tests pass and no whitespace errors.
- [x] **Step 5: Run the deployment playbook in check mode** and verify it changes only the new event service and its required ingress/configuration.
