# Donna Jira Task Proposals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Donna suggests grounded Jira tasks, asks through Telegram/Open WebUI actions when action, required fields, or an existing-task match is below 90% confidence, then creates or annotates one issue only after an explicit action.

**Architecture:** Keep Donna as the shared interface and add a narrow Jira action service for matching, draft persistence, single-use authorization, typed create/comment operations, and read-after-write verification. Calibrate action/field/match decisions independently against held-out examples; a model's self-reported confidence is never used as the score. Telegram callbacks and an Open WebUI Action function both call the same typed service and carry trusted operator/conversation identity.

**Tech Stack:** Go, standard `net/http`, Jira Cloud REST v3, Telegram Bot API inline keyboards, Open WebUI Action functions, existing Ansible deployment and Docker-based Go harness.

**Spec:** `docs/superpowers/specs/2026-11-01-donna-jira-task-proposals-design.md`

## Global Constraints

- Only generated Granola summaries and explicitly requested source material may ground a candidate; preserve note ID, link, and evidence span.
- Action existence, every required field, and Jira match precision are independently validated to at least 90%; if evidence is insufficient, ask.
- Every create/comment is explicitly approved by one authenticated action bound to the exact payload, principal, channel, conversation, expiry, and single-use draft ID.
- The model receives no Jira credentials, arbitrary REST endpoints, or arbitrary JQL controls.
- Jira writes use restricted typed operations and are read back before success is reported.
- Repeat event delivery is deduplicated by `granola_note_id + normalized_action_text`; an uncertain action remains single-use and must be reconciled by the operator before any replacement is proposed.
- No edit to issue summary, description, status, assignee, or dates; no delete, bulk operation, or work-log/time tracking in this phase.
- Preserve all existing working-tree changes and do not edit secrets in `.env`, `group_vars/vault.yml`, or `ssh/`.
- Use synthetic Granola fixtures and a fake Jira server; no live Jira/Granola writes during development.

## Review Focus

- An action section contains a decision or status rather than a task; prove it does not create a false candidate.
- A task lacks a project, issue type, or summary; prove Donna asks and makes zero Jira writes.
- Two Jira issues have similar summaries; prove the match gate asks rather than selecting by lexical similarity alone.
- Same action arrives again after a webhook retry or after a Jira timeout; prove no duplicate comment/create is issued.
- Telegram callback or Open WebUI Action is replayed or issued by another user/chat; prove no provider write occurs.

---

### Task 1: Grounded task candidates and confidence gates

**Files:**
- Create: `internal/secretary/task_proposals.go`
- Create: `internal/secretary/task_proposals_test.go`
- Modify: `internal/donnagranola/client.go`
- Modify: `internal/donnagranola/worker.go`
- Modify: `internal/donnagranola/worker_test.go`

**Interfaces:**
- Produce `TaskCandidate{GranolaNoteID, NoteURL, MeetingTitle, SourceSpan, ProjectKey, IssueType, Summary, Description, Assignee, DueDate}` and per-gate score/evidence fields for action existence and each required Jira field.
- Produce `EvaluateTaskCandidate(candidate TaskCandidate, policy ConfidencePolicy) CandidateDecision`; a decision is `ready`, `clarify`, or `reject`, with one source-grounded clarification question when required.
- Produce a held-out evaluation helper that reports precision independently for action existence, project, issue type, summary, and match; it must fail closed when a gate lacks minimum evidence.
- Candidate extraction may reuse Granola's generated-summary action section, but must retain the exact source span and note metadata; `GranolaNote` is defined in `internal/donnagranola/client.go` and formatting/extraction are in `worker.go`. Do not treat all bullets as confirmed tasks.

- [x] **Step 1:** Tests cover source evidence, ambiguity, missing fields, optional fields, and independent confidence gates.
- [x] **Step 2:** Targeted Docker Go tests reproduced the expected behavior.
- [x] **Step 3:** Candidate types, conservative evidence rules, and one-question clarification output do not trust model-reported confidence.
- [x] **Step 4:** Granola fixture tests retain note ID, optional HTTPS note URL, and verbatim action span while excluding non-action sections.
- [x] **Step 5:** Targeted `internal/secretary` and `internal/donnagranola` tests pass.

### Task 2: Jira match, draft, comment, and create service

**Files:**
- Create: `cmd/donna-jira-actions/main.go`
- Create: `internal/donnajira/types.go`
- Create: `internal/donnajira/approval_store.go`
- Create: `internal/donnajira/approval_store_test.go`
- Create: `internal/donnajira/http.go`
- Create: `internal/donnajira/http_test.go`
- Create: `internal/donnajira/client.go`
- Create: `internal/donnajira/client_test.go`
- Create: `internal/donnajira/executor.go`
- Create: `internal/donnajira/executor_test.go`

**Interfaces:**
- Produce `ActionKind` values `create` and `comment`, `ActionPrincipal{OperatorID, Channel, ConversationID}`, `TaskDraft`, `MatchCandidate`, `ActionPreview`, and `ActionReceipt`.
- Produce `ApprovalStore.Create`, `Get`, and atomic single-use `Consume`; bind drafts to canonical payload digest, note/action dedupe key, principal, channel, conversation, and expiry.
- Produce `JiraWriter.SearchTaskCandidates`, `CreateIssue`, `AddComment`, `GetIssue`, and `GetComment`; configure a fixed Jira Cloud ID and strict allowlist of writable fields.
- Produce `NewHandler(store, writer, authenticator, clock) http.Handler` with typed endpoints for matching, clarification/draft updates, preview, and apply; draft/preview must make zero write calls.
- Produce `Executor.Apply(ctx, principal, actionID, digest) (ActionReceipt, error)` that atomically consumes authorization, performs one create/comment, reads it back, and persists uncertain outcomes without retrying.

- [x] **Step 1:** Store tests cover principal binding, digest, expiry, replay, restart recovery, and dedupe.
- [x] **Step 2:** Targeted Docker Go tests passed after implementation.
- [x] **Step 3:** Draft persistence, canonical digesting, and atomic single-use consumption are implemented.
- [x] **Step 4:** Fake Jira tests cover fixed host/path, safe JQL, create/comment fields, malformed keys, and response decoding.
- [x] **Step 5:** The service implements bounded matching, create/comment, and read-back verification; Jira credentials remain in that service.
- [x] **Step 6:** Executor tests cover no write before approval, replay rejection, comment-only update, and uncertain outcomes without retry.
- [x] **Step 7:** Typed routes fail closed on identity, allowlist, ambiguity, and persistence errors.
- [x] **Step 8:** Targeted `internal/donnajira` and `cmd/donna-jira-actions` tests pass.

### Task 3: Secretary clarification and action adapters

**Files:**
- Create: `internal/secretary/jira_actions_client.go`
- Create: `internal/secretary/jira_actions_client_test.go`
- Create: `internal/secretary/jira_proposal_flow.go`
- Create: `internal/secretary/jira_proposal_flow_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/telegram_webhook.go`
- Modify: `internal/secretary/service.go`
- Create: `roles/open_webui/files/donna_jira_actions.py`
- Create: `roles/open_webui/files/donna_jira_actions_test.py`

**Interfaces:**
- Produce `JiraActionsClient.Match`, `Draft`, `Preview`, `Apply`, and `Cancel` calls carrying trusted `OperatorContext` from the channel adapter; client request JSON cannot override identity.
- Add Telegram `callback_query` update handling for `Crear`, `Actualizar comentario`, `No es la misma`, and `Descartar`; callback payload references an opaque draft/action ID and is acknowledged once.
- Add an Open WebUI Action adapter that calls the same typed service using the authenticated Open WebUI principal/chat context; it has only the secretary-service credential, never Jira credentials.
- Both channels render the same source evidence, issue key, exact proposed payload/comment, and action choices.

- [x] **Step 1:** Tests cover clarification, low-confidence matches, exact previews, action identity, expiry, replay, and channel isolation.
- [x] **Step 2:** Targeted Docker Go tests pass.
- [x] **Step 3:** Typed client and persistent operator/channel/conversation proposal state are implemented.
- [x] **Step 4:** Telegram callback tests cover correct principal binding, rejection, and no writes before confirmation.
- [x] **Step 5:** Telegram renders exact previews and handles inline action and candidate callbacks.
- [x] **Step 6:** Open WebUI Action tests cover exact digest/action forwarding, admin binding, and no direct Jira access.
- [x] **Step 7:** The `v0.12.2` Action adapter is implemented and its manual import is documented.
- [x] **Step 8:** Secretary, Telegram, Open WebUI, and read-only tests pass in the targeted Docker run.

### Task 4: Granola candidate intake and clarification continuity

**Files:**
- Modify: `internal/donnagranola/worker.go`
- Modify: `internal/donnagranola/secretary_sink.go`
- Modify: `internal/secretary/notifications.go`
- Create: `internal/secretary/jira_proposal_state.go`
- Create: `internal/secretary/jira_proposal_state_test.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/service.go`

- [x] Carry Granola note ID, optional HTTPS note URL, meeting title, and exact action spans into durable secretary notifications. Candidate details expire after 30 days; URL ownership is not independently verified.
- [x] Add proposal state keyed by authenticated operator, channel, and conversation; expire after 30 minutes and survive process restarts.
- [x] Start a proposal from a source-backed action, ask one unresolved question at a time, and apply explicit operator answers.
- [x] Wire Telegram and Open WebUI candidate actions to shared proposal state while preserving read-only routing.
- [x] Test notification → clarification → exact preview with a fake Jira action service; no provider write occurs before confirmation.

### Task 5: Deployment, end-to-end evidence, and operations guide

**Files:**
- Create: `roles/donna_jira_actions/`
- Create: `playbooks/deploy-donna-jira-actions.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/system-usage.md`
- Create: `docs/validation/2026-11-01-donna-jira-proposals/`

- [x] **Step 1:** Added the restricted action-service role, loopback listener, separate private credentials, state directory, systemd hardening, health check, and Secretary configuration. No secret file was edited.
- [x] **Step 2:** Fake-provider tests cover Granola intake, clarification, exact preview, action confirmation, comment-only updates, and verified Jira receipts. The cross-channel proposal path performs no write before confirmation.
- [x] **Step 3:** Documented the uncalibrated fail-closed behavior, 90% threshold, actions, comment-only duplicate handling, expiries, no-retry uncertain outcomes, and deferred time tracking.
- [x] **Step 4:** Full Go harness, Go build script, Open WebUI Action tests, Ansible syntax check, and `git diff --check` pass; results are recorded in `docs/validation/2026-11-01-donna-jira-proposals/verification.md`.
- [x] **Step 5:** Playbook syntax is validated. Lab check mode and deployment are intentionally not run; the feature remains inactive until the operator supplies credentials and explicitly schedules activation.
