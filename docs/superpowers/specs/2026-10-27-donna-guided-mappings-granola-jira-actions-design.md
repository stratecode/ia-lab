# Donna guided mappings, Granola follow-ups, and Jira actions

## Intent

Give Donna reusable tools to resolve people and projects from Jira and Granola,
send a concise message when a Granola meeting note is generated, and prepare
Jira task changes. Every mapping change and every Jira create, edit, or delete
must receive the operator's explicit confirmation before it is applied.

The operator approved the recommended design direction on 2026-10-27: keep
Donna as the conversation surface and expose these as reusable capabilities
with service boundaries. The operator explicitly chose confirmation for every
Jira write. This document records that decision; it is not authorization to
perform live Jira or Granola changes during development.

## Goals

- Guide the operator through mapping a person, email, project, and aliases to
  source records discovered in Jira and Granola.
- Store only mappings the operator confirms; preserve source IDs and provenance
  so Donna can use them deterministically and explain them later.
- Receive Granola's generated meeting-note event, retrieve the authorized note,
  and send the note's summary plus grounded candidate actions in a short message.
- Let the operator approve each proposed Jira create, edit, or delete action
  after reviewing the exact payload.
- Revalidate an approved action, execute it once, read back the result, and
  report the outcome with the Jira issue key when available.
- Keep Telegram and Open WebUI on the shared secretary identity, routing, and
  conversation model.
- Keep each capability reusable by Donna and future projects, without giving a
  language model direct provider credentials or unconstrained REST access.

## Non-goals

- No autonomous Jira writes, bulk approval, blanket authorization, or approval
  inferred from a prior conversation.
- No automatic merging of same-name people, emails, or projects.
- No transcript delivery or transcript-derived task creation in the first
  increment; Granola's generated summary is the source for the notification.
- No change to unrelated lab diagnostics, intent behavior, principal model, or
  existing read-only Jira/Granola query semantics.
- No migration of all secretary capabilities into new services.

## Current state and constraints

- `lab-secretary` is the shared Go conversation and channel service. It routes
  Telegram and Open WebUI through a read-only capability registry.
- Jira reads use a fixed Jira REST/JQL path. The current Jira connector and
  runtime capability contract do not expose create, edit, or delete operations.
- Granola meeting reads are already available. The current operating guide says
  natural-language queries retrieve note summaries and exclude transcripts.
- Entity resolution and approved aliases already exist. Exact external IDs and
  approved aliases resolve; ambiguous aliases remain unresolved. The missing
  part is a guided candidate-review flow with durable source references.
- The repository contains a separate uncommitted design,
  `2026-10-23-donna-diagnostics-granola-webhooks-design.md`, that specifies a
  Granola `note.generated` webhook, authenticated ingress, durable queue,
  retries, deduplication, and a concise Telegram notification. Reuse its
  webhook processing requirements where they fit; keep its diagnostics switch
  out of scope here.
- Granola's current official documentation index lists a Webhooks API, note
  events, and API references for creating webhook endpoints and retrieving
  notes. Live account tier, scopes, endpoint enrollment, and public ingress
  remain deployment prerequisites, not assumptions. See the [Granola API
  documentation index](https://docs.granola.ai/llms.txt).
- Telegram is an authenticated private operator channel. Open WebUI is mapped
  to the same authorized operator. Any confirmation must retain this trusted
  identity and must fail closed if the channel cannot establish it.
- Do not edit secrets in `.env`, `group_vars/vault.yml`, or `ssh/`. Use the
  existing private configuration mechanism for provider credentials and
  webhook signing secrets.
- The checkout has extensive unrelated uncommitted changes. Implementation
  must preserve them and limit edits to files needed for this scope.

## Approaches considered

### 1. Put all behavior directly in `lab-secretary`

This minimizes deployment work and reuses its channel adapters. It also binds
identity workflows, Granola event processing, and Jira write policy to Donna's
runtime and makes reuse in other projects harder. Rejected because the operator
wants these capabilities to be reusable.

### 2. Keep Donna as the interface and expose reusable capability services

Keep `lab-secretary` responsible for conversation, authenticated channel
identity, concise presentation, and confirmation prompts. Give identity
mapping, Granola event intake, and Jira writes narrow service contracts with
their own persistence and provider-facing logic. **Selected.** It preserves the
current shared conversation path while allowing each capability to be reused
and deployed independently. Implement and validate the capabilities in stages
to avoid deploying unused infrastructure all at once.

### 3. Create a separate service for every small sub-operation

This maximizes process isolation but adds networking, deployment, secrets,
monitoring, and failure handling before the boundaries have proven useful.
Rejected as the initial shape; split further only when an independently
reusable capability or operational need justifies it.

## Proposed behavior

### Guided identity and project mappings

- Donna searches the authorized Jira and Granola surfaces for candidate people,
  accounts, projects, and aliases, then asks one focused question at a time.
- Each candidate shows the distinguishing evidence available from its source:
  display name, project key/name, account identifier, email only when the
  provider exposes it, and the source reference. Donna must not infer a hidden
  email or equate records from a matching display name alone.
- Supported responses are confirm this mapping, choose another candidate,
  declare that records are different, add/correct an alias, or skip.
- A mapping remains pending until the operator confirms the exact source and
  canonical entity. The confirmation is tied to that mapping payload; edits
  after the prompt invalidate it.
- The mapping record stores entity type, canonical label, aliases, provider and
  external ID, status, provenance, confirmation time, and the authenticated
  operator. It contains no copied meeting transcript or Jira issue content.
- Ambiguous or missing mappings remain unresolved. Donna asks instead of
  silently widening a Jira search or assigning a task to a guessed person.
- Approved mappings feed the existing deterministic entity-resolution path.
  A future Excel import/export can use the same schema, but is not required for
  this first guided flow.

### Granola generated-note notification

- Subscribe to Granola's generated-note event for the operator's authorized
  notes. Start with personal notes; shared-folder scope requires an explicit
  later extension because the existing webhook proposal is personal-note only.
- Authenticate the raw webhook request, reject stale/invalid signatures, and
  persist the event before acknowledging it. Process asynchronously with
  bounded retries and event/note deduplication.
- Fetch the note through the Granola API after the event. Use Granola's
  generated summary as the source text; Donna may format it and label decisions
  and candidate tasks, but may not add facts, owners, or dates absent from the
  note.
- Send a short Spanish notification with the meeting title, date, generated
  summary, and a separate list of candidate Jira actions. Missing owners or
  dates are stated as missing.
- A candidate action is a proposal only. It cannot mutate Jira unless the
  operator confirms its exact action in the current authenticated conversation.
- Do not log or persist transcript or note content in diagnostic records. Keep
  only the minimum event metadata needed for retries, deduplication, and audit.
- Telegram delivery uses the authenticated private bot chat. Open WebUI must
  use a supported notification/inbox path tied to the operator; if it cannot
  push proactively, retain the notification for the next authenticated chat and
  disclose that delivery state rather than claiming it was sent.

### Jira create, edit, and delete

- Expose narrow typed operations for creating an issue, editing an existing
  issue, and deleting an existing issue. Do not give the model arbitrary Jira
  REST, JQL, or URL access.
- First call creates a pending action and returns a human-readable preview with
  action type, project, issue key if applicable, summary, assignee, and all
  fields that will change. No provider write occurs during preview.
- Donna asks for confirmation for every single action. Confirmation is bound
  to the authenticated operator, channel/conversation, action ID, canonical
  payload digest, and a short expiry. It is single-use. A changed payload,
  expired action, duplicate confirmation, or missing identity cannot execute.
- Delete confirmation identifies the exact Jira key and summary. It is never
  implied by “clean up”, a batch confirmation, or a prior approval.
- Before executing, the Jira service validates the target and permitted fields;
  after executing, it reads the issue back (or confirms deletion) and reports
  the verified outcome. Failures and uncertain outcomes are surfaced without
  claiming success.
- Store an audit record with operation, authenticated principal, issue key,
  approval reference, time, and provider result. Do not store unrelated
  conversation text or credentials.
- Keep existing reads available if the write service is unavailable. A
  capability failure must never fall back to an unapproved provider call.

## Shared service boundaries

- **Secretary / channel layer:** authenticate operator, preserve conversation
  scope, present proposals, collect explicit confirmations, and communicate
  status in short messages.
- **Identity mapping capability:** candidate search coordination, mapping
  workflow, confirmed mapping store, and deterministic entity lookup.
- **Granola event capability:** webhook ingress, durable event queue, note
  retrieval, deduplication, grounded summary formatting, and notification
  delivery request.
- **Jira action capability:** typed draft/execute operations, confirmation
  validation, provider writes, read-after-write verification, and action audit.
- Provider credentials remain inside the relevant capability. The model sees
  bounded inputs and outputs, never credentials.

## Acceptance criteria

- A guided mapping can show Jira/Granola candidates and does not become usable
  until the operator confirms the exact mapping.
- Same-name records stay ambiguous unless an exact provider ID or confirmed
  alias resolves them. Correcting a pending mapping requires fresh confirmation.
- A signed Granola generated-note event is acknowledged only after durable
  enqueue; invalid signatures fail closed; retries do not create duplicate
  notifications.
- The delivered message uses Granola's generated summary, is concise, marks
  candidate tasks as suggestions, and does not fabricate owners or dates.
- A Jira proposal causes no write before explicit confirmation. Create, edit,
  and delete each require a distinct valid confirmation bound to the preview.
- Expired, replayed, altered, or wrong-user confirmations cannot write.
- Successful Jira actions are read back and reported with verified identifiers;
  provider failures are not reported as success.
- Telegram and Open WebUI use the same identity and confirmation policy. Tests
  and deployment evidence distinguish proactive delivery from queued delivery
  where a channel lacks push support.
- Existing read-only flows remain functional, and the full repository harness
  passes for affected behavior.

## Implementation sequence

1. Build and verify the guided mapping capability, including its durable schema
   and candidate actions.
2. Reuse the existing Granola webhook proposal to implement generated-note
   delivery and task suggestions, verifying account prerequisites before live
   enrollment.
3. Add the Jira draft/confirm/execute capability, with one explicit approval
   for every create, edit, or delete and read-after-write verification.
4. Connect all three to the secretary's shared Telegram and Open WebUI paths;
   document operational setup, failures, rollback, and test evidence.

## Decisions and constraints for implementation

- **Decided by the operator:** ask for confirmation before every Jira write.
- **Decided by the operator:** keep reusable capability boundaries while Donna
  remains the conversation surface.
- **Builder choice:** exact confirmation syntax and presentation affordances,
  provided they are usable in both Telegram and Open WebUI and cannot authorize
  a changed action.
- **Verify before live deployment:** Granola API plan/scopes, webhook endpoint
  enrollment, signing secret storage, public HTTPS ingress, and Open WebUI's
  proactive notification capability.
- **No live actions during implementation tests:** use synthetic Granola
  payloads and a Jira test double; live write canaries require the operator to
  review the concrete implementation and authorize them separately.
