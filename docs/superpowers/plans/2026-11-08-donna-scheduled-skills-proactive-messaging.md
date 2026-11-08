# Donna Scheduled Skills and Proactive Messaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Fran explicitly schedule reusable, read-only Donna workflows that can deliver concise, source-grounded messages through configured channels.

**Architecture:** Keep capabilities as the existing typed, read-only source operations; add a small built-in skill registry that composes only approved capabilities. A durable scheduler claims due occurrences, runs one workflow at a time, and records an outbox item before delivery. Telegram is the initial push channel; Open WebUI receives private pending messages on the next authenticated conversation.

**Tech Stack:** Go standard library, existing `internal/secretary` service and Telegram bot, private atomic JSON state under `Service.StateDir`, current Jira/Granola read capabilities, Ansible role configuration, and the Docker-based Go test/build harness.

**Spec:** `docs/architecture/master-plan.md`, `docs/secretary-operation.md`, and `docs/donna-character.md`; this plan implements the scheduled-skills/proactive-messaging design approved in the conversation on 2026-11-07.

## Global Constraints

- `LAB_SECRETARY_PROACTIVE_ENABLED` defaults to `false`; creating and sending scheduled workflows requires explicit operator opt-in.
- Schedules are bound to the authenticated operator and channel context supplied by the adapter; user input cannot choose another recipient.
- Version one supports `once`, `daily`, and `weekdays` schedules in an explicit IANA timezone; the initial default timezone is `Europe/Madrid`. Arbitrary cron expressions are out of scope.
- The first built-in skills are `morning-brief` and `deadline-watch`; both use only existing read-only Jira/Granola capabilities and at most one existing principal-model call per run.
- No external skill code, shell, filesystem writes, Jira writes, autonomous skill creation, or unbounded polling is allowed in this feature.
- Telegram messages are sent only to the authenticated private operator chat. Open WebUI has no off-session push in this plan; it displays queued messages on the next authenticated conversation.
- Outbound sends are bounded to 08:00–21:00 `Europe/Madrid`, at most three proactive messages per operator per local day; related notifications are coalesced. Uncertain Telegram send outcomes are not retried automatically.
- Durable schedule/run/outbox state uses private permissions and atomic writes. Logs and metrics contain IDs, counts, states, and durations, never prompt, summary, or message bodies.
- Preserve secrets and unrelated worktree changes. Do not enable the feature on the host as part of implementation; deployment remains an explicit later operation.

## Review Focus

- A weekly schedule crosses a daylight-saving transition; verify the local wall-clock time remains stable and a nonexistent local time is deferred to the next valid instant.
- A restart occurs after a due occurrence is claimed; verify it is resumed without creating a second logical run.
- Telegram accepts a send but the client times out; verify the outbox marks the result uncertain and does not blindly resend.
- A scheduled request omits skill, time, recurrence, or timezone; verify Donna asks one clarification and creates no active task.
- One data source is unavailable or returns no relevant items; verify the message states the limitation or is suppressed and never invents work.

---

### Task 1: Schedule model and durable store

**Files:**
- Create: `internal/secretary/scheduled_tasks.go`
- Create: `internal/secretary/scheduled_tasks_test.go`
- Modify: `internal/secretary/service.go`

**Interfaces:**
- Define `ScheduleCadence` values `once`, `daily`, and `weekdays`; define `TaskSchedule{Cadence, LocalTime, RunAt, Timezone}` and `ScheduledTask{ID, OperatorID, Channel, ConversationID, SkillID, Schedule, Enabled, CreatedAt, NextRunAt}`.
- Define `ScheduledRun{ID, TaskID, ScheduledFor, Status, Attempt, StartedAt, FinishedAt, ErrorCode}` with statuses `pending`, `running`, `delivered`, `skipped`, `failed`, and `uncertain`.
- Add `ScheduledTaskStore` methods `Create`, `List`, `Get`, `SetEnabled`, `ClaimDue`, and `FinishRun`. `ClaimDue` must atomically deduplicate by `(task_id, scheduled_for_utc)` and persist the claim before returning it.
- Store state in a dedicated `scheduled-tasks/` directory under `Service.StateDir`; do not reuse the one-shot `timers.json` schema.

- [ ] **Step 1: Write failing store tests** for create/list, operator isolation, pause/resume, recurring next-run calculation, duplicate claim suppression, restart recovery, malformed state, and private directory/file permissions.
- [ ] **Step 2: Run `go test ./internal/secretary -run 'TestScheduledTask(Store|Schedule)' -count=1`** in the repository Docker Go environment and confirm the new tests fail for the missing store behavior.
- [ ] **Step 3: Implement schedule validation and local-time calculation** in `internal/secretary/scheduled_tasks.go`; reject invalid IANA zones and unsupported cadence instead of guessing.
- [ ] **Step 4: Implement atomic JSON persistence and idempotent run claims**; use the repository's existing private-state and atomic-JSON patterns.
- [ ] **Step 5: Run the targeted scheduled-store tests** and confirm restart, DST, and duplicate-claim cases pass.

### Task 2: Built-in skill registry and bounded runner

**Files:**
- Create: `internal/secretary/scheduled_skills.go`
- Create: `internal/secretary/scheduled_skills_test.go`
- Modify: `internal/secretary/service.go`

**Interfaces:**
- Define `ScheduledSkill{ID, Version, Description, RequiredCapabilities}` and `ScheduledSkillRunner.Run(ctx context.Context, task ScheduledTask, scheduledFor time.Time) (ScheduledSkillResult, error)`.
- Register exactly `morning-brief` and `deadline-watch` for the first release. Each implementation must call a fixed set of existing read-only capabilities and return `ScheduledSkillResult{Text, Evidence, Suppress, SuppressReason}`.
- For grounded language generation, call the existing capability-answer path with a fixed question and source allowlist; do not call `ReplyInConversation`, so background jobs cannot create or contaminate visible chat history.
- Bound each run to one workflow, one model call, the existing connector timeouts, and a maximum of five evidence references. Suppress a message when the only result is “no change”.

- [ ] **Step 1: Write fake-capability tests** for each skill's allowed sources, evidence retention, missing-source behavior, empty-result suppression, and the one-model-call limit.
- [ ] **Step 2: Run `go test ./internal/secretary -run 'TestScheduledSkill' -count=1`** in the repository Docker Go environment and confirm expected failures.
- [ ] **Step 3: Implement the static registry and runner**; unknown or disabled skill IDs return a typed error and cannot fall through to generic chat/model tool selection.
- [ ] **Step 4: Add tests proving scheduled skills cannot reach Jira write methods, shell, arbitrary URLs, or unrelated memory.**
- [ ] **Step 5: Run the targeted scheduled-skill tests** and confirm all outputs are source-grounded and bounded.

### Task 3: Conversation controls and confirmation

**Files:**
- Create: `internal/secretary/scheduled_commands.go`
- Create: `internal/secretary/scheduled_commands_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `internal/secretary/telegram_test.go`
- Modify: `internal/secretary/web_test.go`

**Interfaces:**
- Add deterministic commands `/skills`, `/programar`, `/tareas`, `/pausar-tarea ID`, `/reanudar-tarea ID`, `/cancelar-tarea ID`, and `/confirmar-tarea ID` through the shared `Service.ReplyInConversation` path.
- `/programar` may create only a disabled draft. It must show skill, recurrence, local time, timezone, channel, data sources, and delivery limits; `/confirmar-tarea ID` activates that exact draft for the authenticated operator.
- Natural-language schedule requests may be parsed only for the supported built-in skill names and cadence forms. Ambiguous input produces one clarification, with no model-generated schedule and no active record.
- Derive recipient and `OperatorID` from trusted channel context; never accept them from the command body.

- [ ] **Step 1: Write failing command tests** for valid draft/confirmation, missing fields, unknown skill, unsupported cron, cross-operator access, pause/resume/cancel, and stable shared behavior across Telegram and Open WebUI.
- [ ] **Step 2: Run `go test ./internal/secretary -run 'TestScheduledCommand' -count=1`** in the repository Docker Go environment and confirm the tests fail before implementation.
- [ ] **Step 3: Implement the deterministic command parser and responses**; preserve existing `/timer`, `/timers`, and `/cancel ID` behavior without command collisions.
- [ ] **Step 4: Add tests that unconfirmed drafts never appear in `ClaimDue` and that every task-management command is operator-scoped.**
- [ ] **Step 5: Run the targeted command and existing timer tests** and confirm both task scheduling and one-shot timers work independently.

### Task 4: Scheduler lifecycle, runner, and outbox

**Files:**
- Create: `internal/secretary/scheduled_runner.go`
- Create: `internal/secretary/scheduled_runner_test.go`
- Create: `internal/secretary/proactive_outbox.go`
- Create: `internal/secretary/proactive_outbox_test.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/notifications.go`
- Modify: `internal/secretary/notifications_test.go`
- Modify: `cmd/lab-secretary/main.go`
- Modify: `cmd/lab-secretary/main_test.go`

**Interfaces:**
- Define `ScheduledTaskExecutor.Execute(ctx context.Context, task ScheduledTask, scheduledFor time.Time) (ScheduledSkillResult, error)` and `ProactiveScheduler.Run(ctx context.Context)`; inject clock, executor, store, and sender for tests.
- Poll due tasks once per minute, execute at most one scheduled inference at a time, and keep runtime on the existing secretary process context. Start it for both Telegram polling and webhook modes only when `LAB_SECRETARY_PROACTIVE_ENABLED=true`.
- Persist a generic `ProactiveNotification{ID, OperatorID, Channel, TaskID, ScheduledFor, Text, Evidence, Status, CreatedAt}` before delivery. Keep Granola-specific payload validation and candidate handling intact.
- Deliver Telegram notifications only to the bound private chat. Queue Open WebUI notifications for the next authenticated turn and acknowledge them with the existing channel/operator isolation rules.
- Mark successful, suppressed, failed, and uncertain outcomes distinctly. Retry only failures known to have occurred before a send was accepted; do not retry ambiguous transport outcomes.

- [ ] **Step 1: Write failing runner/outbox tests** for due claim, skill execution, channel delivery, coalescing, quiet hours, per-day limit, cancellation during shutdown, and each run status transition.
- [ ] **Step 2: Run `go test ./internal/secretary ./cmd/lab-secretary -run 'Test(Proactive|Scheduled)' -count=1`** in the repository Docker Go environment and confirm expected failures.
- [ ] **Step 3: Implement the scheduler loop** using injected clock/ticker and existing context cancellation; ensure no scheduler goroutine starts when the feature flag is off.
- [ ] **Step 4: Implement the durable outbox and Telegram sender adapter**; persist delivery state before/after sending and leave uncertain results visible for operator review.
- [ ] **Step 5: Integrate pending Open WebUI delivery** with the authenticated conversation path; show at most three queued messages per turn and preserve the existing Granola message behavior.
- [ ] **Step 6: Test polling/webhook lifecycle, restart, outbox deduplication, send timeout, no-send windows, and absence of secret/body text in metrics/log events.**
- [ ] **Step 7: Run all secretary and lab-secretary tests** and confirm the scheduler has no effect with the feature flag disabled.

### Task 5: Feature gate, persona, operations, and acceptance

**Files:**
- Create: `roles/lab_secretary/defaults/main.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `internal/secretary/persona.go`
- Modify: `internal/secretary/persona_test.go`
- Modify: `docs/donna-character.md`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/architecture/master-plan.md`
- Modify: `cmd/lab-secretary/main_test.go`

**Interfaces:**
- Add `lab_secretary_proactive_enabled: false` as the Ansible default and pass it as `LAB_SECRETARY_PROACTIVE_ENABLED`; no credentials or connector secrets change.
- Change Donna's character rule from “never sends spontaneous alerts” to “sends only operator-enabled scheduled/event notifications within policy; never invents urgency or initiates unrelated conversation.”
- Document setup, preview, confirmation, listing, pause/cancel, quiet hours, limits, retention, failure/uncertain-delivery states, rollback, and the fact that Open WebUI cannot push while closed.
- Keep the feature disabled in deployed defaults; deployment and live activation are explicitly outside this implementation plan.

- [ ] **Step 1: Add persona tests** for an allowed scheduled notice, suppressed/no-change result, quiet-hour deferral, and the existing rule that Donna does not send unrelated unsolicited messages.
- [ ] **Step 2: Add startup/config tests** proving disabled mode launches no scheduler and enabled mode refuses startup when no safe delivery channel/operator binding exists.
- [ ] **Step 3: Document an operator canary** using synthetic Jira/Granola fixtures and a private Telegram chat; include a rollback that disables the feature flag without deleting scheduled state.
- [ ] **Step 4: Run `./scripts/test-orchestrator-go.sh`** and confirm secretary, Telegram, Open WebUI, and existing bridge tests pass.
- [ ] **Step 5: Run `./scripts/build-orchestrator-go.sh`** and confirm the deployment-facing secretary binary builds.
- [ ] **Step 6: Run `git diff --check` and review the deployment diff**; expected result is the feature remains off by default and no secret files or live integrations were changed.

## Acceptance Criteria

- Fran can inspect built-in skills, create a schedule draft, confirm it, and later list, pause, resume, or cancel it from the shared conversation service.
- A due skill runs once per logical occurrence, uses only its declared read-only capabilities, attaches source evidence, and produces no background chat-history entry.
- Telegram receives an authorized proactive message only inside configured policy; Open WebUI sees its private pending notice on the next authenticated conversation.
- Restart, duplicate ticks, daylight-saving transitions, connector failures, and ambiguous send results cannot create uncontrolled duplicate conversations.
- No model runs until a scheduled skill is due; maximum scheduled concurrency is one, and no second model is installed or loaded.
- The feature remains disabled on the host until Fran explicitly enables and deploys it after the synthetic canary.
