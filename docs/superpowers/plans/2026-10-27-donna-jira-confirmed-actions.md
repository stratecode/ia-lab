# Donna Confirmed Jira Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Donna draft Jira issue create, edit, and delete operations and execute each only after an explicit confirmation of the exact action.

**Architecture:** Add an independently deployable Jira action service with typed draft/execute endpoints, a durable single-use approval store, and a restricted Jira REST client. The secretary renders the preview and confirmation request; the action service binds approval to the authenticated operator, channel/conversation, action ID, and payload digest.

**Tech Stack:** Go, standard `net/http`, Jira Cloud REST v3, existing private Ansible configuration and test harness.

**Spec:** `docs/superpowers/specs/2026-10-27-donna-guided-mappings-granola-jira-actions-design.md`

## Global Constraints

- Every create, edit, and delete requires an explicit confirmation for that one action.
- No writes during preview; no batch or blanket approval.
- Confirmation is single-use, expires, and is invalidated by payload changes or a different principal/conversation.
- Do not expose arbitrary Jira REST, JQL, URLs, or provider credentials to the model.
- Read back successful writes and never claim success after an uncertain provider response.
- Preserve unrelated existing worktree changes and keep existing read-only queries working.

## Review Focus

- Changed payload after preview; test that the old approval cannot execute.
- Wrong operator, channel, or conversation; test each rejects without Jira call.
- Expired or replayed confirmation; test no second write is issued.
- Jira write succeeds but read-back fails; test response reports uncertain verification rather than success.
- Delete targets a changed issue summary; test confirmation binds to exact key and summary.

---

### Task 1: Typed Jira action domain and approval store

**Files:**
- Create: `cmd/donna-jira-actions/main.go`
- Create: `internal/donnajira/types.go`
- Create: `internal/donnajira/approval_store.go`
- Create: `internal/donnajira/approval_store_test.go`
- Create: `internal/donnajira/http.go`
- Create: `internal/donnajira/http_test.go`

**Interfaces:**
- Produce `ActionPrincipal{OperatorID int64, Channel string, ConversationID string}`, `type Authenticator interface { Authenticate(*http.Request) (ActionPrincipal, error) }`, and `type Clock func() time.Time`; the handler derives principal from authenticated secretary service context, never from action JSON.
- Produce `ActionKind` values `create`, `edit`, and `delete`; `ActionDraft` contains `ID`, `OperatorID`, `Channel`, `ConversationID`, `Kind`, `ProjectKey`, `IssueKey`, `ExpectedSummary`, `Fields`, `PayloadDigest`, `CreatedAt`, `ExpiresAt`, and `Status`.
- Produce `ApprovalStore.Create(context.Context, ActionDraft) (ActionDraft, error)`, `Get(context.Context, string) (ActionDraft, error)`, and `Consume(context.Context, string, int64, string, string, string, time.Time) (ActionDraft, error)`; `Consume` validates principal, channel, conversation, digest, expiry, and one-time use atomically.
- Produce `NewHandler(ApprovalStore, JiraWriter, Authenticator, Clock) http.Handler` for `POST /v1/actions/draft`, `POST /v1/actions/{id}/execute`, and `GET /healthz`.

- [ ] **Step 1: Write failing approval-store tests** for create/edit/delete draft persistence, exact principal binding, payload digest mismatch, expiry, and one-time consumption across restart.
- [ ] **Step 2: Run `go test ./internal/donnajira -run 'TestApprovalStore' -count=1`** and confirm expected failures before implementation.
- [ ] **Step 3: Implement typed draft storage** with private durable records, canonical JSON payload hashing, and atomic consume semantics.
- [ ] **Step 4: Run `go test ./internal/donnajira -run 'TestApprovalStore' -count=1`** and confirm all store tests pass.
- [ ] **Step 5: Write failing HTTP tests** proving draft creation never calls Jira, request JSON cannot override the authenticated principal, execute rejects absent/invalid confirmation, and execution accepts only a matching authenticated principal.
- [ ] **Step 6: Implement bounded JSON endpoints** with strict request validation and no provider call from the draft endpoint.
- [ ] **Step 7: Run `go test ./internal/donnajira -run 'TestHTTP' -count=1`** and confirm HTTP tests pass.

### Task 2: Restricted Jira REST writer and read-after-write verification

**Files:**
- Create: `internal/donnajira/client.go`
- Create: `internal/donnajira/client_test.go`
- Create: `internal/donnajira/executor.go`
- Create: `internal/donnajira/executor_test.go`

**Interfaces:**
- Produce `JiraWriter.CreateIssue(context.Context, CreateIssueInput) (IssueRef, error)`, `UpdateIssue(context.Context, UpdateIssueInput) error`, `DeleteIssue(context.Context, string) error`, and `GetIssue(context.Context, string) (IssueSnapshot, error)`.
- Produce `Executor.Execute(context.Context, ActionDraft, OperatorContext, string) (ActionResult, error)` that consumes approval before a single write and returns `verified`, `failed`, or `uncertain` outcome.
- Produce `AuditSink.Record(context.Context, ActionAudit) error`; `ActionAudit` contains only action ID, principal ID, operation, issue key, timestamp, and provider outcome, never conversation text or credentials.

- [ ] **Step 1: Write failing REST client tests** for fixed host/path, allowed fields, authentication header redaction, create/edit/delete methods, and response decoding against `httptest.Server`.
- [ ] **Step 2: Run `go test ./internal/donnajira -run 'TestJiraClient' -count=1`** and confirm expected failures before the client exists.
- [ ] **Step 3: Implement the Jira REST client** using a fixed configured cloud ID and REST v3 endpoints; reject unsupported fields, project keys, and malformed issue keys.
- [ ] **Step 4: Write failing executor tests** for create/edit/delete each requiring a distinct approval, read-after-write checks, deletion verification, uncertain outcomes, and audit records that contain no conversation text.
- [ ] **Step 5: Implement the executor** to consume approval atomically, perform one typed write, verify the resulting issue state, and record a bounded audit result.
- [ ] **Step 6: Run `go test ./internal/donnajira -count=1`** and confirm all package tests pass.

### Task 3: Secretary preview and confirmation in both channels

**Files:**
- Create: `internal/secretary/jira_actions_client.go`
- Create: `internal/secretary/jira_actions_client_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/intent.go`
- Modify: `internal/secretary/telegram.go`
- Modify: `internal/secretary/web.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `internal/secretary/telegram_test.go`

**Interfaces:**
- Produce secretary calls `DraftJiraAction(ctx, OperatorContext, ActionRequest) (ActionDraft, error)` and `ExecuteJiraAction(ctx, OperatorContext, actionID, confirmationText string) (ActionResult, error)`.
- The confirmation text uses `CONFIRMAR <actionID>` and is accepted only in the same authenticated conversation that received the preview. Any field change requires a new draft and confirmation.

- [ ] **Step 1: Write failing conversation tests** for create/edit/delete preview-only behavior, exact confirmation text, replay rejection, changed-field re-preview, and per-channel principal isolation.
- [ ] **Step 2: Run `go test ./internal/secretary -run 'TestJiraAction' -count=1`** and confirm failures before the secretary flow is implemented.
- [ ] **Step 3: Implement the typed HTTP client** and its timeout/auth configuration.
- [ ] **Step 4: Implement shared intent routing and confirmation handling** so Telegram and Open WebUI render the exact same action preview and policy; natural-language agreement without the action ID does not execute.
- [ ] **Step 5: Run `go test ./internal/secretary -run 'TestJiraAction|TestOpenWebUI|TestTelegram' -count=1`** and confirm both channel paths pass while existing read-only Jira capability tests remain green.

### Task 4: Deployment, documentation, and validation

**Files:**
- Create: `roles/donna_jira_actions/`
- Create: `playbooks/deploy-donna-jira-actions.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/system-usage.md`

- [ ] **Step 1: Add Ansible deployment** with service auth, private Jira credentials, state directory, bounded network exposure, health checks, and restart policy.
- [ ] **Step 2: Document exact confirmation syntax, supported fields, delete behavior, audit records, failure handling, and rollback.**
- [ ] **Step 3: Add an end-to-end test** with a fake Jira server proving preview makes zero writes and each confirmed action performs one write followed by verification.
- [ ] **Step 4: Run `go test ./internal/donnajira ./internal/secretary ./cmd/lab-secretary` and `git diff --check`**; expected result is all affected tests pass and no whitespace errors.
- [ ] **Step 5: Run the deployment playbook in check mode** and verify it changes only the new Jira action service and its required secretary client configuration.
