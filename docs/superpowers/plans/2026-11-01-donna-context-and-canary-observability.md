# Donna Synthetic Context and Canary Observability Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make concise synthetic date follow-ups reliable with the active LFM model, prove source exclusions reset at new topics and `/new`, and verify source-read deltas from Prometheus history across service restarts.

**Architecture:** Keep source authorization in the existing conversation scope. Add a narrowly gated answer fallback only when a synthetic conversation contains exactly one dated action relevant to an anaphoric date question, while still calling the configured model. Exercise that path against live LFM through an opt-in Go acceptance test with fake source readers. Use Prometheus `increase()` over a bounded window for canary evidence; do not add a second persistent counter store.

**Tech Stack:** Go 1.23, `internal/secretary`, Docker Go harness, Prometheus HTTP API and PromQL.

**Spec:** The user's approval of the three next steps in the 2026-11-01 Codex conversation.

## Global Constraints

- Keep LFM as principal; never load Gemma.
- Never query real Jira or Granola; all regression and LFM acceptance tests use fake capability readers.
- Deploy only `lab-secretary` if code changes require a live test.
- Preserve unrelated dirty files and do not modify `.env`, vault, or `ssh/`.
- Source exclusions remain independent of model output and apply before connector reads.

## Review Focus

- A synthetic single-date summary or anaphoric date question returns only the supplied dated fact, with no invented recommendation.
- Multiple dated actions or ambiguous pronouns do not trigger the deterministic fallback.
- A new-topic marker and `/new` clear inherited source exclusions without allowing a real connector in tests.
- Prometheus deltas distinguish completed no-source interactions from Jira/Granola reads across process restarts.

## Tasks

### Task 1: Ground simple synthetic date answers with LFM

**Files:** `internal/secretary/conversation_context.go`, `internal/secretary/service.go`, `internal/secretary/conversation_source_scope_test.go`.

- [x] Add failing unit cases for a single dated synthetic summary and `¿Y ella cuándo?`; fake model returns a generic unsupported recommendation, fake readers count calls.
- [x] Verify RED: final answers omit the date or include the model's unsupported recommendation.
- [x] Add a narrowly gated extractor for exactly one dated action and its synthetic anaphoric date follow-up; keep model inference enabled and use the extracted fact only as the final closed-world answer.
- [x] Run focused tests and the full secretary package in Docker.
- [x] Add an opt-in LFM acceptance test configured by `LAB_SECRETARY_LFM_UPSTREAM`; it sends the same two-turn case through `ReplyInConversation`, requires the visible final answer to contain Friday 6 November, and asserts Jira/Granola fake-reader counts stay at zero.
- [x] Run that acceptance test in a Docker container on the lab host against the already-loaded LFM endpoint, passing its existing model key only through the task environment; do not call real sources.

### Task 2: Pin exclusion reset boundaries

**Files:** `internal/secretary/conversation_source_scope_test.go`.

- [x] Add tests showing an inferred Jira suggestion is blocked in an ordinary follow-up but allowed after a new-topic marker and after `/new`; all readers are fakes.
- [x] Run the tests against the current behavior; if reset cases fail, verify the expected scope defect before changing code.
- [x] Make the smallest scope adjustment only if a test exposes a reset defect; otherwise retain the current behavior and record that no production change was needed.
- [x] Run the focused reset tests and secretary package suite.

### Task 3: Make canary deltas restart-safe

**Files:** `docs/secretary-operation.md` and, if useful, a small testable metrics-query helper.

- [x] Confirm the deployed Prometheus scrape target and API route, then query persisted interactions for the synthetic Telegram messages.
- [x] Document bounded-window PromQL using `increase()` for `capability="none"` and `capability=~"jira|granola"`, with absent source series treated as zero and scrape-window guidance.
- [x] No helper was needed; validate the queries directly against the live Prometheus API.
- [x] Verify the query reports about two completed no-source interactions and zero source-read delta across the canary interval; the query observed one counter reset in that validation window.

### Task 4: Final verification and secretary-only deployment

- [x] Run `./scripts/test-orchestrator-go.sh`, `./scripts/build-orchestrator-go.sh`, and `git diff --check`.
- [x] Confirm LFM model path/hash before and after; deploy only `lab-secretary` if the binary changed.
- [x] Verify health, memory, Prometheus historical deltas, and the service-level synthetic `telegram` conversation; preserve all unrelated working-tree changes.

## Completion Contract

- A live LFM acceptance check returns the supplied date in the final user-visible answer while fake Jira/Granola readers remain at zero.
- Tests prove ordinary follow-ups inherit source exclusions and `/new`/new-topic boundaries reset them.
- Prometheus `increase()` gives restart-safe evidence for successful no-source interactions and no Jira/Granola reads.
- Requested harness, build, diff check, deployment scope, and live verification pass or concrete blockers are reported.
