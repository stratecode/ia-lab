# Donna diagnostics and Granola webhook design

## Intent

Deliver two operator-facing improvements in order:

1. Add one binary debug setting for the detailed diagnostic instrumentation used to investigate host and inference failures.
2. Receive Granola note webhooks so Donna can fetch a completed meeting note, send Fran an automatic meeting summary, and suggest candidate tasks.

The purpose is to keep deep diagnostics available when needed and make meeting outcomes useful without requiring a manual query. This design does not authorize Donna to create or modify Jira issues.

## Goals

- A single Ansible-managed `lab_debug_enabled` setting enables or disables detailed diagnostic collection across Donna, the LLM telemetry proxy, native inference tracing, and the detailed host flight recorder.
- Basic service health, operational lifecycle logs, and crash recovery protections remain available when debug is off.
- Granola `note.generated` events for the operator's personal notes cause Donna to fetch the note through the Granola API and deliver a concise Spanish summary to the operator's private Telegram chat.
- The summary identifies decisions and candidate actions with wording and dates grounded in the note; it may suggest Jira tasks but does not create them.
- Webhook handling authenticates the exact request body, persists and deduplicates events, acknowledges quickly, and does note retrieval and synthesis asynchronously.
- Retries, duplicate deliveries, API failures, and notes that are not yet readable are observable and recoverable without duplicate user messages where practical.
- Secrets remain outside Git and webhook/API credentials are handled through existing private Ansible configuration.

## Non-goals

- Jira webhook ingestion or Jira issue writes in this increment.
- Autonomous urgency scoring or inferred deadlines from tone; first delivery summarizes explicit commitments and labels uncertain task suggestions as suggestions.
- Search, file reading, broader Donna tools, or other integrations.
- Disabling kdump, watchdog, RAS protections, persistent journald, or minimum service logs when detailed debug is disabled.

## Current state and constraints

- Donna already receives Telegram through an authenticated HTTPS webhook with a durable queue, deduplication, and asynchronous worker.
- Granola is currently a read connector through MCP. Existing operation docs say meeting queries use note summaries and exclude transcripts.
- Granola's current official API docs require Business or Enterprise for API keys; its note webhooks signal `note.generated`, `note.edited`, and note sharing, and the event payload identifies the note rather than carrying its content. Donna must fetch the note with the API.
- Detailed diagnostics currently span secretary conversation checkpoints, inference proxy checkpoints, a native bpftrace observer, and a host flight recorder. They were recently added in this working tree, which contains many unrelated uncommitted changes. Implementation must preserve those changes and edit only necessary files.
- The current active product direction is one local principal model and shared secretary. Current work requires read-only external capabilities; reviewed task suggestions fit that boundary.
- Granola plan, API access scope, and account-side webhook setup remain to be confirmed before live acceptance.

## Approaches considered

### Diagnostics switch

1. One global binary switch for all detailed trace collection, while operational logs and crash protections stay enabled. **Selected.** It matches the operator's explicit preference for an all-or-nothing debug mode.
2. Separate switches per service. Rejected because it makes incident activation less predictable and adds configuration overhead.

### Granola delivery

1. Direct webhook endpoint in `lab-secretary`, using the existing authenticated ingress and durable queue patterns, then fetch and process notes asynchronously. **Recommended.** This keeps one router, one Telegram identity, and one durable processing path.
2. External automation service forwards webhooks to Donna. Rejected as the first implementation because it adds another credential and delivery dependency without being needed by the existing deployment.
3. Poll Granola periodically. Rejected because the operator wants an automatic post-meeting notification and Granola provides note webhooks on eligible plans.

## Proposed behavior

### Debug mode

- Add a tracked boolean with default `false` and an environment override consistent with existing Ansible configuration.
- When enabled, start all detailed application, proxy, native inference, and host resource trace collectors and write their existing private, rotated, content-free event records.
- When disabled, stop detailed collection and suppress creation/appends to detailed trace files. Keep basic service start/stop/failure logs, health endpoints, aggregate metrics, persistent journals, and crash recovery protections.
- Make deployment reconciliation idempotent and verify active/inactive systemd state for collectors. Document the single operator setting and how to enable it for a diagnostic window.
- Do not increase llama.cpp verbosity or persist prompts, note content, model payloads, credentials, or user conversation text in diagnostic traces.

### Granola webhook

- Register an HTTPS callback for `note.generated` on personal notes. Shared notes and later edits are outside the first increment. Granola's other event types are `note.edited` and `note.access_granted`; the latter means a note was shared with the operator.
- Expose a dedicated public HTTPS callback route through the existing ingress to `lab-secretary`.
- Verify Granola's webhook signature against the raw body and a stored signing secret; reject invalid signatures and stale webhook timestamps before queueing.
- Store event ID and note ID in a private durable queue; acknowledge promptly; deduplicate retries.
- Worker fetches the note from Granola API after the `note.generated` event. The API only returns notes after the AI summary and transcript exist. Use bounded retries for transient API errors; retry a not-yet-readable note only as race recovery.
- Produce one Spanish Telegram message containing meeting title/date, decisions, explicit commitments and owners/dates when present, then candidate task suggestions. State clearly when owners or dates are absent. Never present an inferred start/progress state as fact.
- Do not forward transcripts into memory or diagnostics. Keep only the minimum queue metadata needed for retry/deduplication; normal learning trace rules apply to generated interaction metadata.
- Provide status/health visibility for webhook registration and processing errors without exposing note contents.

## Acceptance criteria

- With debug disabled, all detailed trace files stop advancing and the native observer/flight recorder are inactive; minimum logs, metrics, health, and crash protection remain available.
- With debug enabled, detailed collection resumes across all intended collectors, and an inference can be correlated through the existing attempt/trace IDs without recording payload text.
- Invalid Granola signatures are rejected; valid generated-note events are durably queued and acknowledged; duplicate event delivery causes no duplicate summary.
- A fetched synthetic note produces one source-grounded summary and candidate tasks in Telegram, with missing owner/date stated explicitly and no Jira write.
- Processing/API failure is reported without crashing Donna and can be retried or inspected.
- With a public HTTPS callback, valid personal-scope `note.generated` events pass through ingress to Donna.
- Documented Granola tier, scopes, callback requirements, configuration, and rollback are accurate.

## Risks and open constraints

- Granola's webhook endpoint requires a publicly reachable HTTPS URL, Business or Enterprise API access, and an eligible API key. The actual account tier and permissions are not yet verified. Granola returns the webhook signing secret only when the endpoint is created, so enrollment must save it immediately in the private secret store.
- A note may arrive before its generated summary is available; durable retries are required.
- Granola content may include sensitive meeting data. The service must fetch only authorized notes, avoid logging note content, and send summaries only to the authenticated operator's private Telegram chat.
- The existing repository has extensive uncommitted changes. Implementation must inspect diffs and avoid replacing or reverting work outside this scope.

## Implementation order

1. Implement and verify the global debug switch, including Ansible deployment and operator documentation.
2. Implement Granola webhook/API configuration, durable processing, Telegram summary delivery, and synthetic tests.
3. Deploy only after reviewing the concrete diff and checking the Granola account prerequisites. Test a live webhook when the operator next has a Granola meeting.

## Decisions left to the operator

- Confirm this design before code implementation.
- Confirm whether the initial Granola webhook should cover only personal notes or notes in selected shared folders too.
- Confirm whether post-meeting delivery should include only a summary and candidate Jira tasks, or also a proactive follow-up question.
