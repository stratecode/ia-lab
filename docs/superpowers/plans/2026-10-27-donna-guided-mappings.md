# Donna Guided Mappings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Donna collect and store confirmed person, email, project, and alias mappings discovered from Jira and Granola.

**Architecture:** Keep source retrieval in the secretary's existing Jira and Granola readers. Add a reusable identity-mapping service that stores pending and approved mappings; the secretary presents candidates and sends the authenticated operator's confirmation. The existing `ResolveEntity` behavior continues to leave ambiguous aliases unresolved.

**Tech Stack:** Go, standard `net/http`, current Go test harness, existing private state/configuration conventions.

**Spec:** `docs/superpowers/specs/2026-10-27-donna-guided-mappings-granola-jira-actions-design.md`

## Global Constraints

- Every mapping change requires explicit confirmation of that exact mapping.
- Do not infer hidden emails or merge records on display-name similarity.
- Preserve authenticated operator, channel, and conversation scope.
- Keep provider credentials out of the model and out of Git.
- Preserve unrelated existing worktree changes; implementation must not reset, stash, or overwrite them.

## Review Focus

- Same display name from two source records remains ambiguous; test two candidates with identical labels and different external IDs.
- Provider omits email; test candidate output has no fabricated email.
- Candidate payload is changed after preview; test old confirmation cannot approve it.
- Mapping store is restarted; test approved mapping remains available and pending mapping remains unapproved.
- Open WebUI lacks authenticated operator identity; test mapping read and confirmation fail closed.

---

### Task 1: Identity service domain and durable store

**Files:**
- Create: `cmd/donna-identity/main.go`
- Create: `internal/donnaidentity/types.go`
- Create: `internal/donnaidentity/store.go`
- Create: `internal/donnaidentity/store_test.go`
- Create: `internal/donnaidentity/http.go`
- Create: `internal/donnaidentity/http_test.go`

**Interfaces:**
- Produce `EntityKind` values `person`, `email`, `project`, and `alias`; `MappingStatus` values `pending`, `approved`, and `rejected`; `MappingCandidate` fields `ID`, `OperatorID`, `Kind`, `CanonicalID`, `CanonicalLabel`, `Provider`, `ExternalID`, `Aliases`, `Evidence`, `Status`, `CreatedAt`, `ConfirmedAt`, and `ConfirmedBy`.
- Add `Channel`, `ConversationID`, and `PayloadDigest` to `MappingCandidate`; the digest is computed from the canonical mapping fields and excludes mutable status/timestamps.
- Produce `Store.CreateCandidate(context.Context, MappingCandidate) (MappingCandidate, error)`, `Store.Get(context.Context, int64, string) (MappingCandidate, error)`, `Store.List(context.Context, int64, EntityKind, MappingStatus) ([]MappingCandidate, error)`, and `Store.SetStatus(context.Context, int64, string, string, string, string, MappingStatus, int64, time.Time) (MappingCandidate, error)`. `SetStatus` checks operator, channel, conversation, expected digest, and one-way pending status before updating.
- Produce `type Authenticator interface { Authenticate(*http.Request) (Principal, error) }` and `Principal{OperatorID int64, Channel string, ConversationID string}`. The handler derives identity from authenticated service context, never from mapping JSON.
- Produce `NewHandler(Store, Authenticator) http.Handler` with `GET /healthz`, `POST /v1/mappings`, `GET /v1/mappings`, `GET /v1/mappings/{id}`, and `POST /v1/mappings/{id}/confirm` and `/reject`.

- [ ] **Step 1: Write failing store tests** for operator isolation, pending-to-approved status, repeated confirmation idempotency, and persistence across a reopened file store.
- [ ] **Step 2: Run `go test ./internal/donnaidentity -run 'TestStore' -count=1`** and confirm it fails because the package is not implemented.
- [ ] **Step 3: Implement the typed mapping model and private durable store** using the repository's 0700 directory and atomic-file conventions; reject non-positive operator IDs, empty provider IDs, and invalid status transitions.
- [ ] **Step 4: Run `go test ./internal/donnaidentity -run 'TestStore' -count=1`** and confirm all store tests pass.
- [ ] **Step 5: Write failing HTTP tests** proving authenticated operator scoping, no confirmation without identity, and exact 400/404 behavior for malformed or missing IDs.
- [ ] **Step 6: Run `go test ./internal/donnaidentity -run 'TestHTTP' -count=1`** and confirm the new tests fail before handler implementation.
- [ ] **Step 7: Implement the JSON HTTP handler** with bounded request bodies and no provider access; source candidates are supplied by the secretary, not free-form model payloads.
- [ ] **Step 8: Run `go test ./internal/donnaidentity -count=1`** and confirm all service tests pass.

### Task 2: Candidate search and confirmation flow in the secretary

**Files:**
- Create: `internal/secretary/identity_client.go`
- Create: `internal/secretary/operator_context.go`
- Create: `internal/secretary/identity_client_test.go`
- Modify: `internal/secretary/entities.go`
- Modify: `internal/secretary/learning_types.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `cmd/lab-secretary/main.go`

**Interfaces:**
- Produce `OperatorContext{OperatorID int64, Channel string, ConversationID string}` in `internal/secretary/operator_context.go` from the trusted Telegram/Open WebUI authentication path.
- Consume the identity service endpoints from Task 1 through `IdentityClient.CreateCandidate`, `ListMappings`, and `ConfirmMapping`, each carrying an `OperatorContext` derived from the authenticated channel.
- Produce `IdentityClient.ApprovedKnowledge(context.Context, int64) ([]KnowledgeItem, error)` to translate approved identity-service records into the existing deterministic `ResolveEntity` input without changing its exact-ID/approved-alias behavior.
- Produce a secretary flow that first reads Jira/Granola candidates, presents one mapping at a time, and stores an approved mapping only after a confirmation bound to its candidate ID and canonical payload digest.

- [x] **Step 1: Write failing tests** for source candidate ranking, identical-name ambiguity, absent email, confirmation of the selected candidate only, changed-payload rejection, and missing Open WebUI operator identity.
- [x] **Step 2: Run targeted Go tests**; the payload-mutation regression test failed before the store fix, then passed after recalculating the digest during confirmation.
- [x] **Step 3: Implement the HTTP identity client** with loopback URL validation, internal service authentication, timeouts, and strict response decoding.
- [x] **Step 4: Implement the guided mapping dialogue** using `ResolveEntity` and live Jira readers; candidate evidence is limited to source-backed labels and external IDs.
- [x] **Step 5: Run the guided mapping and entity-resolution tests**; all passed.
- [x] **Step 6: Add Ansible role and playbook** `roles/donna_identity/` and `playbooks/deploy-donna-identity.yml`; the service token is generated privately on the host.
- [x] **Step 7: Update `docs/secretary-operation.md`** with the mapping dialogue, exact confirmation behavior, service health, and rollback.
- [x] **Step 8: Run targeted package tests and `git diff --check`**; the full secretary package suite retains one unrelated pre-existing wording assertion failure in `TestJiraPeopleGivesAUsefulReadingOfOpenWork`.

Verification: identity and command packages pass; guided mapping, source ranking, identity client, and entity-resolution tests pass. `scripts/build-donna-identity.sh`, Ansible syntax check, `bash -n`, and `git diff --check` pass. Full `internal/secretary` package run has one pre-existing Jira wording assertion mismatch (`cola por hacer` expected; `por hacer` returned).
