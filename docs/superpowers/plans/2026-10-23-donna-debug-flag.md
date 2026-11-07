# Donna Debug Flag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add one boolean switch that enables or disables detailed conversation, inference, native decode, and host flight-recorder traces while preserving minimum operational logs, aggregate metrics, and crash-recovery safeguards.

**Architecture:** Ansible owns `lab_debug_enabled`, defaulting to false, and renders it into the secretary and telemetry service environments. Go trace writers are constructed only when enabled; telemetry receives no trace directory when disabled. Ansible also reconciles the native observer and existing flight-recorder systemd units to the same setting, without managing or disabling kdump, watchdog, RAS, or persistent journald.

**Tech Stack:** Go, Ansible/Jinja, systemd, repository Go harness.

**Spec:** `docs/superpowers/specs/2026-10-23-donna-diagnostics-granola-webhooks-design.md` (debug-mode sections only; Granola is deferred).

## Global Constraints

- One `lab_debug_enabled` boolean controls all detailed diagnostic collectors.
- Default the flag to `false`; only `true` activates detailed tracing.
- Preserve basic service lifecycle/error logs, health endpoints, Prometheus aggregate metrics, and crash-recovery protections with debug disabled.
- Do not store message, prompt, answer, transcript, model payload, or credential content in diagnostic traces.
- Preserve existing uncommitted changes; edit only files required for the debug switch.

## Review Focus

- Missing, blank, or false configuration must not enable traces; cover default/false/true parsing and wiring.
- Debug-off must prevent creation or append of detailed trace files in both Go services.
- Debug-off must stop the native observer and host flight recorder when their units exist; debug-on must start them.
- Missing optional diagnostic units must not break ordinary deployment; exercise conditional unit handling.
- Existing aggregate metrics, health checks, crash protection, and learning traces must not be coupled to this flag; add assertions where their owning packages allow it.

---

### Task 1: Gate application-level detailed traces

**Files:**
- Modify: `cmd/lab-secretary/main.go`
- Modify: `cmd/lab-secretary/main_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `cmd/llm-telemetry/main.go`
- Modify: `internal/llmtelemetry/proxy.go`
- Modify: `internal/llmtelemetry/proxy_test.go`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `roles/llm_telemetry/tasks/main.yml`
- Modify: `group_vars/all.yml`
- Modify: `.env.example`

**Interfaces:**
- Environment setting: `LAB_DEBUG_ENABLED`, parsed strictly as a boolean; absent means false.
- Ansible setting: `lab_debug_enabled`, sourced from `LAB_DEBUG_ENABLED` with default false.
- Secretary: create `Service.diagnostics` only when enabled.
- Telemetry: configure the proxy trace logger only when enabled; omit `--trace-dir` when disabled.

- [x] **Step 1: Add tests for absent, false, true, and invalid debug configuration.**
- [x] **Step 2: Run focused Go tests and confirm the new cases fail.**
- [x] **Step 3: Gate `newConversationLog` and `SetTraceDir` from `LAB_DEBUG_ENABLED`; keep existing learning-store interaction traces independent.**
- [x] **Step 4: Run secretary and telemetry package tests; assert disabled mode writes no detailed diagnostic file and enabled mode preserves the current trace behavior.**
- [x] **Step 5: Render one shared Ansible variable into the secretary environment and telemetry command line, defaulting to false.**
- [x] **Step 6: Run `ansible-playbook playbooks/deploy-conversation-diagnostics.yml --syntax-check` and inspect rendered configuration for both flag values.**

### Task 2: Reconcile system-level detailed collectors

**Files:**
- Modify: `playbooks/deploy-deep-inference-trace.yml`
- Modify: `playbooks/deploy-principal-model.yml`
- Create: `playbooks/toggle-debug-tracing.yml`
- Create: `roles/lab_debug_tracing/tasks/main.yml`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/principal-model-operation.md`

**Interfaces:**
- `lab_debug_enabled` is the only switch for system-level detailed collectors.
- Existing optional units: `lab-native-inference-trace.service` and `lab-flightrecorder.service`.
- The playbook must detect whether optional units exist before reconciling them; absence must not fail a normal deployment.

- [x] **Step 1: Add an Ansible syntax/behavior test or a check-mode fixture covering true, false, and absent optional units.**
- [x] **Step 2: Update the native-trace deployment so installation/activation follows `lab_debug_enabled`; when false, stop and disable an existing observer without deleting its diagnostic files.**
- [x] **Step 3: Add a focused role to reconcile the native observer and flight recorder from the shared flag, and invoke it from the principal-model deployment, leaving journald, kdump, watchdog, and RAS untouched.**
- [x] **Step 4: Document the default, how to enable the full trace mode temporarily, how to turn it back off, and which minimum observability remains.**
- [x] **Step 5: Run Ansible syntax checks for the principal-model and deep-trace playbooks and `git diff --check`.**

### Task 3: Verify the integrated flag

**Files:**
- No additional files.

- [x] **Step 1: Run `./scripts/test-orchestrator-go.sh`.**
- [x] **Step 2: Run `./scripts/build-orchestrator-go.sh`.**
- [x] **Step 3: Validate Ansible syntax for every changed playbook.**
- [x] **Step 4: Inspect the final diff to confirm only detailed diagnostics follow the flag and unrelated working-tree changes remain untouched.**
- [x] **Step 5: Report that deployment was not performed unless a separate deployment request is made.**
