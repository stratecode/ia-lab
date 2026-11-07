# Donna Intent and Conversation Style Implementation Plan

> **For agentic workers:** Use inline execution with TDD. Do not commit because this checkout contains unrelated user changes.

**Goal:** Improve ambiguous intent fallback, cautious Granola/Jira correlation, and Donna's concise conversational delivery.

**Architecture:** Keep intent parsing and source reads in the current secretary package. Prefer the intent microservice, fall back to local routing, and use the existing model classifier only when local intent remains ambiguous. Put cross-source evidence rules in the synthesis prompt and apply message splitting only in the Telegram transport for long final answers.

**Tech Stack:** Go, existing `internal/secretary` tests, Markdown persona instructions.

**Spec:** `docs/superpowers/specs/2026-10-27-donna-intent-and-conversation-style.md`

## Global Constraints

- Use only registered, read-only source capabilities.
- Instruct synthesis not to infer a Granola-to-Jira link from a shared person, project, or date alone; this is a model instruction, not a deterministic output validator.
- Preserve distinct channel/conversation history and source evidence references.
- Keep short Telegram answers in one message; split long answers at semantic boundaries.
- Preserve pre-existing changes in the shared checkout and do not commit them.

## Review Focus

- Negated or mutation requests must not become source reads after fallback — regression test in `internal/secretary/intent_client_test.go`.
- Intent-service failure must not classify ordinary clear questions as ambiguous or trigger a model call — regression test in `internal/secretary/intent_client_test.go`.
- An incomplete one-source suggestion must not suppress an explicit multi-source lookup; deterministic local matches supplement it while exclusions still apply.
- Combined-source synthesis must receive the evidence rule; verify representative live answers because generated prose is not deterministically validated in this increment — prompt-contract test in `internal/secretary/service_test.go`.
- Telegram must preserve Unicode, formatting boundaries, and short-answer single-message behavior — test in `internal/secretary/telegram_test.go`.
- Donna must not over-highlight or produce a wall of text — update the canonical persona instructions and verify exact policy text.

### Task 1: Ambiguous intent fallback and evidence-safe synthesis

**Files:**
- Modify `internal/secretary/service.go`
- Test `internal/secretary/intent_client_test.go`
- Test `internal/secretary/service_test.go`

- [x] Add failing tests for intent service unresolved/failure falling through local routing and model classification only when ambiguous; keep negated action blocked.
- [x] Add a failing test asserting that combined Granola/Jira synthesis receives the evidence-based linking rule.
- [x] Run the focused tests and confirm they fail for the intended behavior.
- [x] Implement the minimal fallback and append correlation guidance only when both sources contribute results.
- [x] Re-run the focused tests and confirm they pass.
- [x] Supplement an incomplete single-source lookup suggestion when deterministic routing recognizes an explicit multi-source comparison; regression test the scenario.

### Task 2: Concise Donna voice and long Telegram delivery

**Files:**
- Modify `skills/secretary-core/instructions.md`
- Modify `internal/secretary/telegram.go`
- Test `internal/secretary/telegram_test.go`

- [x] Add failing tests for long Telegram response splitting at paragraph/sentence boundaries and short response preservation.
- [x] Run the focused test and confirm it fails.
- [x] Replace arbitrary rune slicing with bounded semantic chunks, retaining Unicode and existing HTML escaping.
- [x] Update Donna's persona instructions for direct answers, short paragraphs, minimal emphasis, and exceptional multi-message delivery.
- [x] Re-run focused tests and inspect the instruction diff.

### Task 3: Verification and live read-only coherence check

**Files:**
- Verify the changed files and the complete diff without staging unrelated work.

- [x] Run `go test ./internal/secretary`.
- [x] Run `./scripts/test-orchestrator-go.sh` and `./scripts/build-orchestrator-go.sh`.
- [x] Run `git diff --check` and inspect `git status` and the task diff.
- [x] Deploy the verified secretary changes using the repository's documented deployment playbook, then check service health and exercise only read-only Donna queries against Granola and Jira.
