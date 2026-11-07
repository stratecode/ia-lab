# Donna Jira task proposals, confirmed creation, and duplicate updates

> **Draft for operator review. No implementation is authorized by this document until it is approved.**

## Intent

Let Donna turn clearly stated follow-up actions in an authorized Granola meeting summary into Jira task proposals. Donna asks a focused interactive question when the action, a required Jira field, or a match to an existing Jira task is below the operator's 90% clarity threshold. She shows the exact create or update payload and applies it only after an explicit channel action. For an existing task, the proposed first edit is to append a comment containing the new context and Granola source link; it will not overwrite the issue's main fields. Jira results are verified and reported with their issue key.

## Why

Donna can already retrieve meeting summaries, extract candidate actions, and route Jira reads. Her current secretary runtime is read-only, so it cannot safely turn those candidates into durable tasks. This design adds a bounded path from grounded suggestion to one confirmed Jira create while retaining the shared Telegram/Open WebUI conversation identity.

## Goals

- Suggest only actions supported by an authorized Granola generated summary, with a link to the meeting and source evidence.
- Ask via channel actions if it is unclear whether the text describes a real action or if a Jira-required field has less than 90% calibrated confidence.
- Search Jira for a likely existing task, distinguish a grounded match from a possible match, and ask the operator when match confidence is below 90%.
- Show a final preview of the exact create payload or exact comment before asking to apply it.
- Create one Jira task only after a separate confirmation tied to the authenticated operator, channel, conversation, draft ID, payload digest, and expiry.
- Append a comment to an existing task only after an explicit action bound to the exact issue key, comment, and source evidence; verify the comment after writing.
- Verify the created issue through Jira read-after-write and report the issue key only when verification succeeds.
- Deduplicate meeting actions using the Granola note ID plus normalized action text; never silently create a second task for the same source action.
- Keep provider credentials out of the model and preserve the existing read-only behavior for all other requests.

## Non-goals

- Jira edits to summary, description, assignee, status, dates, delete, bulk operations, or standing approval.
- Creating tasks from transcripts, unsupported model inferences, or an unverified customer/client match.
- Automatically assigning an owner or inventing a due date. Missing optional fields stay empty.
- Reworking identity mappings, the general conversation router, or Granola webhook delivery beyond connecting its existing candidate actions to this proposal flow.
- Any live Jira or Granola write during development or verification.

## Constraints and current state

- `lab-secretary` is the shared Telegram/Open WebUI conversation surface and its registered Jira/Granola query capabilities are read-only.
- The separate `donna-granola-events` path already emits candidate actions from Granola's generated summary and keeps missing owners/dates explicit.
- The existing approved architecture selects a narrow Jira action service behind Donna rather than giving the model arbitrary Jira REST or JQL access. This design narrows that path to task creation only.
- The repository pins Open WebUI to `v0.12.2` in `group_vars/all.yml`. Open WebUI supports user-triggered Action buttons ([official Action function documentation](https://docs.openwebui.com/features/extensibility/plugin/functions/action/)); the deployment and admin-managed function installation path still need verification. The Action function is only a UI adapter and must not contain Jira credentials or its own write logic.
- Telegram's inline-keyboard callback pattern exists elsewhere in the repository, but `lab-secretary` does not currently route task-action callbacks. Add that channel path and bind its callback data to the same typed, authenticated draft/clarification/confirmation API used by Open WebUI.
- Both channels must resolve to the same trusted operator identity before creating or changing a draft. Verify the pinned Open WebUI deployment and channel identity binding before enabling the workflow.
- The checkout has extensive unrelated uncommitted changes. Only files needed for this scope may be changed.
- Development and acceptance tests use synthetic Granola summaries and a fake Jira server. No real client data or live provider writes.

## Approaches considered

### 1. Keep this suggestion-only

Donna would ask clarifying questions and display a draft but never create an issue. This is the smallest deployment, but it does not meet the approved goal of creating a task after confirmation. Rejected as the final design; it remains the safe fallback if the write service is unavailable.

### 2. Give `lab-secretary` direct Jira write access

This minimizes service count but places Jira write credentials and mutation behavior in the shared chat process. It weakens the read/write boundary and makes future reuse harder. Rejected.

### 3. Keep Donna as the interface and use a narrow Jira action service

The secretary owns conversation scope, interactive questions, exact previews, and confirmation UX. A typed Jira action service owns draft state, single-use approval validation, restricted create calls, deduplication, audit metadata, and read-after-write verification. **Selected by the operator in the existing Jira actions design; recommended for this narrower scope as well.** It adds a deployable component but provides the needed authorization boundary and can be tested without live Jira.

## Confidence and clarification policy

- The 90% threshold applies both to whether the source states a real action, every Jira-required field, and whether a Jira issue is the same task. The working assumption is that project, task type, and summary are required proposal fields; confirm these against the configured Jira project's create metadata before implementation.
- Confidence must be calibrated against a labelled evaluation set for action existence, each required field, and duplicate matching separately; an overall average cannot hide a weak gate. A model's self-reported percentage is not a confidence measure. Before enabling proposals or automatic match classification, each gate must demonstrate at least 90% precision on held-out synthetic or approved non-sensitive examples, and each candidate must retain its supporting source span. If there is not enough evaluation evidence for a gate, Donna asks instead of assuming it meets 90%.
- If action existence is below 0.90, Donna asks whether to treat the cited sentence as a task. If a required field is below 0.90, Donna asks only about that field and offers source-grounded choices plus “ninguna de estas”. If duplicate-match confidence is below 0.90, she presents the candidate issue and asks whether the actions describe the same task; she must not update or create until the user selects an explicit action.
- Even when a duplicate match is at least 0.90, Donna presents the exact issue and comment, then waits for the **Actualizar tarea** action. The same action both confirms that the tasks match and authorizes that exact comment, avoiding an extra confirmation turn while preserving explicit approval for every write.
- Owner and due date are optional unless the project's Jira configuration says otherwise. If optional and uncertain, leave them blank and make that visible in the preview; never ask or guess merely to fill a field.
- A clarification response updates the draft and invalidates its prior digest. Donna must render a new preview before the final create confirmation.
- If confidence cannot be calibrated or source evidence is missing, Donna asks rather than creating a proposal as if it were certain.

## User flow

1. A Granola event or an explicit user request supplies an authorized generated meeting summary.
2. Donna identifies a candidate action and retains its note ID, link, and supporting text. Jira reads check the configured project/task type and search for a matching issue.
3. Donna asks a channel action for any below-threshold decision or required field. No Jira write occurs.
4. With no existing match, Donna presents the complete task preview and distinct **Crear en Jira** / **Descartar** actions. With a likely match, she presents the issue and exact proposed comment, then offers **Actualizar tarea** / **No es la misma tarea**. Each button authorizes only its displayed payload.
5. On a create or update action, the Jira service validates the authenticated principal, action ID, expiry, digest, and deduplication state, then performs one bounded operation.
6. The service reads the issue or comment back. Donna reports its key and fields only after verification; otherwise she reports failure or uncertain outcome and does not blindly retry.

Every task is confirmed separately. A confirmation can authorize only the exact preview displayed in that channel and conversation.

## Security and persistence

- The model receives bounded candidate and Jira metadata, never provider credentials or arbitrary REST/JQL controls.
- Jira write authority exists only in the Jira action service. The secretary and Open WebUI Action adapter call typed endpoints.
- Duplicate search is read-only and scoped to the configured/explicit Jira project. A text similarity score alone cannot authorize an update.
- Clarification and confirmation actions are bound to the trusted operator, channel, conversation, draft ID, canonical payload digest, and short expiry. They are single-use and invalid after any edit.
- Persist the minimum draft, note/action dedupe key, approval state, Jira receipt, and audit metadata required for recovery. Do not persist transcripts or unrelated conversation text.
- On an uncertain Jira write outcome, keep the authorization single-use and do not retry automatically. The operator must inspect Jira before preparing another proposal; no durable provider-side reconciliation marker is available yet.
- If either channel cannot establish the same trusted operator identity, fail closed and leave the draft pending.

## Acceptance criteria

- Ambiguous action wording below 0.90 produces an interactive question and zero Jira writes.
- Missing/uncertain required project, type, or summary produces a focused interactive question and zero Jira writes.
- A likely Jira match below 0.90 produces a focused same-task question; it cannot create or update an issue without a selected action.
- A match at or above 0.90 shows the exact issue key and proposed comment; no update occurs until **Actualizar tarea** is activated.
- Uncertain optional owner/date remain empty and are shown as such.
- A clarification changes the draft digest and makes any old confirmation unusable.
- The final preview shows the exact create payload, source meeting, and distinct create/discard actions in Telegram and Open WebUI.
- No Jira create occurs before the user activates **Crear en Jira** for that exact draft.
- No comment is added before the user activates **Actualizar tarea** for the exact issue and comment shown.
- Wrong user/channel/conversation, expired, replayed, altered, or duplicate confirmations issue no create call.
- A successful create is read back and reported with the verified Jira issue key. An uncertain provider outcome is never reported as success and is not retried automatically.
- A successful comment is read back and reported with the verified Jira issue key. Existing issue fields remain unchanged.
- Granola note ID plus normalized action text deduplicates repeat webhook delivery and repeated proposals.
- Synthetic tests prove the entire flow with fake Granola and Jira providers. Existing read-only Jira/Granola tests remain green.
- Channel capabilities and the held-out 90% precision evaluation are documented with reproducible evidence before deployment.
- Jira work-log/time tracking is deferred until this proposal/create/update phase is complete.

## Decisions already made and remaining builder choices

- **Already decided by the operator:** use a narrow reusable Jira action service behind the shared secretary; suggest tasks, ask below 90% clarity, create after confirmation, and support modifying tasks when Donna considers them equal or the operator confirms the match.
- **Confirmed by operator:** an existing task is modified by appending a source-linked comment only; uncertain optional owner/date remain blank; every create/update is explicitly confirmed by its displayed action.
- **Deferred by the operator:** work-log/time tracking comes after this first phase.
- **Builder choice after approval:** exact payload schema, callback token format, persistence store consistent with existing deployment practices, and Open WebUI Action packaging, provided all security and acceptance criteria remain true.
- **Not authorized here:** live Jira writes, production deployment, or enabling real Granola events as a write trigger.
