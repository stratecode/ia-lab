# Secretary Shared Interaction Feedback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax.

**Goal:** Give every active chat channel the same truthful, context-aware acknowledgement and progress semantics, with Telegram reactions and OpenWebUI response-stream rendering.

**Architecture:** `internal/secretary` classifies a request once and emits typed, channel-neutral interaction events. The Telegram and OpenWebUI adapters render those events in native ways. Existing read-only capabilities remain read-only; the shared core cannot announce an action start unless an authorized write capability actually exists.

**Tech Stack:** Go, `net/http`, Telegram Bot API, OpenAI-compatible OpenWebUI SSE, repository Docker Go harness.

**Spec:** [Approved design](../specs/2026-10-13-secretary-shared-interaction-feedback-design.md)

## Global Constraints

- Keep the change generic in the secretary core; no Telegram-specific decision logic in `service.go` or `intent.go`.
- Active adapters in scope are Telegram and OpenWebUI. Define an adapter-neutral contract for future Slack/WhatsApp; do not implement those channels.
- Do not add write capabilities, change OAuth/authentication, memory/RAG, voice, or connector selection policy.
- Preserve identity checks and existing SSE framing. Reject unauthorized Telegram messages before emitting interaction events or making Bot API calls.
- Use only bounded event labels for metrics. Never record message text, task identifiers, prompts, connector payloads, chat IDs, or reaction content.
- Reactions and progress are presentation only. They cannot claim a lookup succeeded or an external action completed.
- For every implementation task, write a failing test first, run the focused package test, then implement and rerun. Keep each task independently reviewable and commit it after its checks pass.

## Task 1 — Define shared interaction semantics and events

**Files:** `internal/secretary/interaction.go` (new), `internal/secretary/interaction_test.go` (new), `internal/secretary/intent.go`, `internal/secretary/service_test.go`.

- [ ] Add failing unit tests for `TestInteractionMeaningForIntent` covering lookup, explicit agreement, requested action, ordinary conversation, and ambiguous intent. Ambiguous input must return `neutral`, not a guessed reaction.
- [ ] Add typed core values in `interaction.go`:
  - `type InteractionMeaning string` with `InteractionLookup`, `InteractionAgreement`, `InteractionAction`, and `InteractionNeutral`.
  - `type InteractionEventKind string` with `EventIntentRecognized`, `EventLookupStarted`, `EventActionRecognized`, `EventActionStarted`, `EventWorkDelayed`, `EventCompleted`, and `EventBlocked`.
  - `type InteractionBlockReason string` with bounded values such as `BlockUnavailable`, `BlockConnectorFailure`, and `BlockTimeout`.
  - `type InteractionEvent struct { Kind InteractionEventKind; Meaning InteractionMeaning; Source string; Action string; Stage string; Reason InteractionBlockReason }`.
  - `type InteractionObserver func(InteractionEvent)`.
  - `func interactionMeaning(intent secretaryIntent, text string) InteractionMeaning`.
- [ ] Implement classification by reusing the already-produced `secretaryIntent` and existing local intent rules. Do not invoke the model a second time for reaction selection. Return neutral when the current router cannot distinguish an explicit agreement or an action reliably.
- [ ] Add table tests proving the event values contain only semantic fields and have no Telegram/OpenWebUI types or message IDs.
- [ ] Run the focused package tests in the repository's Go container: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go test ./internal/secretary'`. Commit as `Add shared secretary interaction events`.

## Task 2 — Emit truthful events from the shared router

**Files:** `internal/secretary/service.go`, `internal/secretary/progress.go`, `internal/secretary/interaction.go`, `internal/secretary/service_test.go`, `internal/secretary/progress_test.go`.

- [ ] Add a failing `TestReplyWithInteractionEmitsIntentAndLookupLifecycle` using a fake read capability. Assert event order is intent recognized, lookup started with the selected capability name, then completed; assert the final answer is still produced.
- [ ] Add failing tests for action recognition with only read capability (`TestReadOnlyActionNeverEmitsActionStarted`), no matching capability (`TestUnsupportedActionDoesNotAnnounceLookup`), connector failure (`TestCapabilityFailureEmitsBlockedNotCompleted`), and neutral/ambiguous prompts (no emoji meaning beyond neutral).
- [ ] Add a shared entry point `ReplyWithInteraction(ctx context.Context, user int64, channel, conversation, text string, observer InteractionObserver) (string, error)` and route the existing conversation APIs through the same internal `reply` path. Keep compatibility wrappers for existing callers.
- [ ] Emit `EventIntentRecognized` only after identity is already authorized by the caller and intent classification has completed. Emit `EventLookupStarted` immediately before invoking a selected `ReadCapability`. Emit `EventActionRecognized` for understood action requests. Emit `EventActionStarted` only after an authorized registered write capability is actually invoked; with today's registry this event must never be emitted. Emit exactly one terminal `EventCompleted` or `EventBlocked` for routed capability work.
- [ ] Replace string-only core progress with typed events while preserving current source wording. Keep the single delayed-progress threshold at 30 seconds and emit `EventWorkDelayed` once with the current stage; cancel the timer on return. Do not emit repeated “still working” events or delay fast replies.
- [ ] Assert against false-positive completion: an upstream error emits blocked and the answer must not say the information was verified. Keep interaction events transient; do not add persistence.
- [ ] Run focused tests in the repository's Go container: `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go test ./internal/secretary -run "TestReplyWithInteraction|TestReadOnlyAction|TestUnsupportedAction|TestCapabilityFailure|TestProgress"'`. Commit as `Emit shared secretary interaction lifecycle`.

## Task 3 — Render events in Telegram and OpenWebUI

**Files:** `internal/secretary/telegram.go`, `internal/secretary/telegram_test.go`, `internal/secretary/service.go`, `internal/secretary/service_test.go`.

- [ ] Telegram: add failing `TestTelegramRendersInteractionReactionAndProgress` for 🔍, ✅, and 👍, asserting `setMessageReaction` targets the authorized inbound message and sends exactly one reaction; assert a selected lookup sends the source message before the final answer.
- [ ] Telegram: add failing `TestTelegramReactionFailureStillDeliversReply`; a Bot API reaction error must be recorded as a bounded presentation event and must not stop routing or suppress the final answer. Preserve current private-chat allowlist checks, and ensure unauthorized updates cause no reaction/progress/API side effect.
- [ ] Implement Telegram rendering in the adapter only. Map typed meanings to the three Unicode emoji and Telegram's `setMessageReaction` payload. Use the incoming chat/message identity only at the adapter boundary. Keep all source/action wording from the shared event copy, not a channel-specific classifier.
- [ ] OpenWebUI: add failing `TestSharedChatStreamingRendersInteractionBeforeAnswer` asserting streamed SSE contains the shared emoji and source progress before final answer content, while preserving existing chunk framing and terminal chunk. Add `TestSharedChatNonStreamingProgressOnlyWhenLookupStarts` and ensure no progress copy appears for neutral or unsupported requests.
- [ ] Implement OpenWebUI rendering by adapting the observer to the existing SSE `emit`/flush path. Preserve authentication and identity checks, response JSON for non-streaming requests, and final substantive answer. Do not add client-side or proxy-specific intent rules.
- [ ] Add/retain negative tests proving OpenWebUI unauthorized requests remain rejected and cannot trigger events. Run `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.23 /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go test ./internal/secretary'`. Commit as `Render shared interaction feedback in chat adapters`.

## Task 4 — Document, verify, and publish

**Files:** `docs/secretary-operation.md`, `docs/system-usage.md`, and implementation files from Tasks 1–3 as needed.

- [ ] Update operational documentation with the event meanings, Telegram/OpenWebUI rendering differences, 30-second single delayed update, current read-only action limitation, and explicit statement that Slack/WhatsApp are future adapters only.
- [ ] Review every event path for privacy: no content-bearing metrics or persisted event records; no event before authorization; no `action_started` for a read capability.
- [ ] Run `./scripts/test-orchestrator-go.sh` and `./scripts/build-orchestrator-go.sh`. If any fails, fix the cause and rerun the applicable complete harness/build; do not count a narrow test as a substitute for either required check.
- [ ] Run `git diff --check`, inspect the complete diff and `git status --short --branch`, and confirm only the secretary feature and its documentation changed.
- [ ] Commit any documentation/final fixes as `Document secretary interaction feedback`, push the implementation commits to `origin/main`, and verify `main` is clean and synchronized.
- [ ] Do not deploy as part of this plan. Deployment needs a separate, post-implementation verification of the currently reachable lab; the recent power/internet interruption makes deployment status an external prerequisite.

## Review Focus

- **Ambiguous or ordinary text:** must remain neutral and must not receive an arbitrary emoji. Covered by `TestInteractionMeaningForIntent`.
- **Unauthorized Telegram input:** must have no reaction, progress, connector, or model side effect. Covered by adapter authorization tests.
- **Telegram reaction API failure:** must not prevent the answer. Covered by `TestTelegramReactionFailureStillDeliversReply`.
- **Read-only or unavailable action:** may acknowledge the request, but must never emit action-started or claim it changed Jira/Granola. Covered by `TestReadOnlyActionNeverEmitsActionStarted` and `TestUnsupportedActionDoesNotAnnounceLookup`.
- **Connector failure / long work:** failure must be blocked rather than completed; long work gets one stage-aware event after 30 seconds and quick work gets none. Covered by `TestCapabilityFailureEmitsBlockedNotCompleted` and progress timer tests.
- **OpenWebUI rendering:** streaming updates must precede the answer without breaking SSE; non-streaming output must not claim a lookup began unless it actually did. Covered by the two OpenWebUI tests in Task 3.

## Self-review

- Spec coverage: includes four meanings, shared typed lifecycle, read-only action boundary, 30-second delayed progress, Telegram reaction fallback, OpenWebUI SSE/non-stream rendering, privacy, and future adapter compatibility.
- Dependency order: core types precede router lifecycle; adapters follow the stable event contract; docs and full harness close the work.
- Interface consistency: every adapter receives `InteractionEvent`; only Telegram translates `MessageID` and Bot API payload (message ID is deliberately absent from the shared event); only OpenWebUI translates events to SSE text.
- Proportion: no new model, database, connector, channel, write permission, or deployment automation is introduced.
- Failure-mode check: ambiguous intent, denied identity, Telegram API presentation error, read-only action, unsupported capability, connector error, and slow/fast completion all have explicit test coverage above.
