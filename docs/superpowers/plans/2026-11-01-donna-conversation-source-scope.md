# Donna Conversation Source Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Keep explicit Jira/Granola source exclusions active for anaphoric follow-ups in the same conversation while preserving a later explicit read-only request.

**Architecture:** Derive conversation source scope from persisted user turns and enforce it at the route boundary before any connector `Read` call. A current explicit read request can authorize its named source for that turn; inferred intent and anaphora cannot widen the scope.

**Tech Stack:** Go 1.23, `internal/secretary`, existing Docker Go harness, Ansible deployment role.

**Spec:** User request in the 2026-11-01 Codex conversation.

## Global Constraints

- Never query real Jira or Granola during tests or deployment verification; use fake capability readers with counters and synthetic UI prompts.
- Keep LFM as the principal model; do not load Gemma.
- Deploy only `lab-secretary`; do not modify Jira, Granola, secrets, or unrelated dirty files.
- Preserve context facts for follow-ups while applying source exclusions independently of model or intent-service output.

## Review Focus

- An intent-service suggestion for an excluded source cannot reach a connector `Read` call.
- An explicit later read-only request for Jira remains routable when allowed.
- Exclusions do not leak to an unrelated new topic or a reset conversation.

## Tasks

### Task 1: Reproduce the anaphoric source-scope leak

- [x] Add a service-level regression with a synthetic first turn that excludes Jira and Granola, followed by `¿Y ella cuándo?`.
- [x] Configure the fake intent service to suggest Jira on the follow-up and fake connectors to count reads.
- [x] Assert both source counters are zero after each turn and the follow-up retains the synthetic facts.
- [x] Run the focused test and record the expected RED result before production changes.

### Task 2: Enforce conversation source scope and preserve explicit permission

- [x] Add the smallest conversation-scope helper that recognizes explicit source exclusions in relevant persisted user turns.
- [x] Apply the scope to intent-service and local routing before any connector is called; keep the source facts available to answer the follow-up.
- [x] Add a regression for a later explicit read-only Jira query and assert only Jira is called once while Granola remains at zero.
- [x] Run focused regressions and secretary package tests.

### Task 3: Run repository verification

- [x] Run `./scripts/test-orchestrator-go.sh` and inspect its complete result.
- [x] Run `./scripts/build-orchestrator-go.sh`.
- [x] Run `git diff --check`.

### Task 4: Deploy and verify the secretary only

- [x] Confirm the live router still serves the pinned LFM artifact and do not restart or reconfigure it.
- [x] Deploy only `lab-secretary` using `playbooks/deploy-secretary-runtime.yml`.
- [x] Verify model/hash, service health, cgroup memory, and zero Jira/Granola reads during synthetic Telegram tests of the initial exclusion and anaphoric follow-up.
- [x] Verify a later explicit read-only request only with fake/local test readers; do not exercise a real connector.
- [x] Confirm Go test containers and test temporary directories were cleaned automatically; retain the Ansible-managed service rollback behavior and the synthetic Telegram transcript as verification evidence.

## Completion Contract

- The anaphoric regression is observed failing before the fix and passing afterward.
- Both excluded-source counters remain zero for the initial and follow-up turns.
- A later explicit read-only request remains routable in the fake-reader regression.
- Requested harnesses/build/diff checks and live secretary-only deployment verification complete, or their concrete blockers are reported.
