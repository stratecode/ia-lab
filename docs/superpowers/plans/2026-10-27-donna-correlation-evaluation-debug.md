# Donna Correlation, Evaluation, and Debug Plan

> **For agentic workers:** Execute inline with TDD. Preserve all unrelated working-tree changes; do not commit.

**Goal:** Validate Granola/Jira links against source text, codify multi-turn behavior in regression tests, and make debug traces explain routing and fallbacks without content capture.

**Spec:** `docs/superpowers/specs/2026-10-27-donna-correlation-evaluation-debug.md`

## Task 1: Deterministic cross-source evidence

- [x] Add failing tests for exact quote/evidence validation and rejected fabricated or one-sided link phrases.
- [x] Implement a structured comparison response, validate quotes and source evidence IDs, and render links only from verified overlap.
- [x] Add a service-level test asserting invalid links are omitted and valid links retain citations.

## Task 2: Conversation regression suite

- [x] Add a three-turn same-conversation regression for Granola context, Jira lookup, and anaphoric comparison.
- [x] Assert both sources are read for the final comparison and no unsupported relation is rendered.
- [x] Run the focused tests and repository Go harness.

## Task 3: Actionable privacy-safe routing diagnostics

- [x] Add route-decision/fallback categories to interaction traces and debug checkpoints.
- [x] Add tests for intent-service success, local fallback categories, and absence of private text/error details.
- [x] Update operational documentation with trace fields and troubleshooting interpretation.

## Task 4: Verification and deployment

- [x] Run focused secretary tests, `./scripts/test-orchestrator-go.sh`, `./scripts/build-orchestrator-go.sh`, relevant Ansible syntax checks, and `git diff --check`.
- [x] Deploy with `LAB_HOSTNAME=192.168.0.85 LAB_USER=stratecode`; verify Donna health and run read-only source comparisons.
- [x] Inspect final status and preserve unrelated changes.
