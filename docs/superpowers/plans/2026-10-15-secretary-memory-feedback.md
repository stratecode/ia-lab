# Secretary Memory, Feedback, and Evaluation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `lab-secretary` so it records source-selection outcomes, accepts explicit operator feedback, retrieves only approved durable knowledge, and regression-tests improvements across Telegram and Open WebUI.

**Architecture:** Keep the existing per-conversation and daily JSONL memory. Add a versioned JSONL learning store behind a shared interface for interaction traces, feedback, and approved knowledge; keep provider payloads out of traces. Return typed capability results with evidence references, support multi-capability routing for meeting-derived tasks, and expose the same feedback/review commands through the common secretary service.

**Tech Stack:** Go 1.23, standard-library JSONL/file storage, existing Prometheus client, Ansible role `lab_secretary`, repository Docker Go test/build harness. Do not add a database, graph store, embedding service, or model-training dependency in this plan.

**Spec:** `docs/superpowers/specs/2026-10-15-secretary-memory-feedback-design.md`

## Global Constraints

- Short-term context remains scoped by operator, channel, and conversation; keep ten exchanges or two hours.
- Operational traces expire after 90 days; unresolved candidates expire after 30 days; approved memories remain until `/forget` or their explicit expiry.
- Keep runtime records on the lab server; Git fixtures must be synthetic or redacted.
- Keep trace directory permissions at `0700` and files at `0600`.
- Never persist complete Granola or Jira REST/JQL payloads or duplicate full prompts and answers in interaction traces.
- `/forget` removes every operator-identifiable history, trace, feedback event, candidate, and approved memory.
- All external capabilities remain read-only. Do not add Jira writes, new channels, online learning, automatic prompt/rule mutation, fine-tuning, or reinforcement learning.
- Prometheus labels are restricted to `channel`, `intent`, `capability`, and `outcome`; never use content, person names, conversation IDs, URLs, Jira keys, or Granola IDs as labels.
- A source is authoritative for its current state; recalled conversation or approved memory cannot replace a live source read.

## Review Focus

- A meeting question asking for follow-up work must select both Granola and Jira; a meeting summary alone selects Granola, and an unrelated task query selects Jira. Pin this in `TestCapabilitiesForIntentSelectsExpectedSources` and `TestMeetingTaskQuestionUsesGranolaAndJira`.
- Identical person names must not be merged without an exact identifier or reviewed alias. Pin this in `TestResolveEntityLeavesAmbiguousAliasUnresolved`.
- Empty connector results and connector failures must remain distinguishable and must not become “no tasks.” Pin this in `TestTraceDistinguishesEmptyFromConnectorFailure`.
- Feedback must bind to the latest interaction in the same authenticated operator/channel/conversation and must never cross those boundaries. Pin this in `TestFeedbackIsScopedToOperatorChannelAndConversation`.
- `/forget` and retention cleanup must delete only the intended operator/expired record classes, not another operator's records or approved unexpired knowledge. Pin this in `TestLearningStoreForgetAndRetention`.

---

## File Map

- Create `internal/secretary/learning_types.go` for versioned trace, feedback, evidence, knowledge, and entity types.
- Create `internal/secretary/learning_store.go` for the `LearningStore` interface and private JSONL implementation.
- Create `internal/secretary/learning_store_test.go` for persistence, permissions, retention, and deletion tests.
- Create `internal/secretary/capability_result.go` for typed evidence-bearing capability results.
- Create `internal/secretary/entities.go` for deterministic entity alias resolution.
- Modify `internal/secretary/service.go` to route one or more capabilities, capture traces, retrieve approved knowledge, and parse common feedback/review commands.
- Modify `internal/secretary/connectors.go` to return stable Granola and Jira REST/JQL evidence references only when provider responses contain them.
- Modify `internal/secretary/intent.go` to preserve source-only intent while identifying mixed meeting/task queries.
- Modify `internal/secretary/memory.go` to include knowledge retrieval and cascade `/forget` through the learning store.
- Modify `internal/secretary/interaction.go` only if a typed interaction event needs to carry the stable interaction ID; keep progress events channel-neutral and ephemeral.
- Modify `internal/secretary/voice_metrics.go` to add bounded interaction and retrieval metrics to the already exposed `/metrics` registry.
- Modify `internal/secretary/service_test.go`, `internal/secretary/intent_test.go`, `internal/secretary/connectors_test.go`, and `internal/secretary/conversation_memory_test.go` for routing, source fidelity, and lifecycle regression coverage.
- Modify `internal/secretary/learning_store_test.go` and create `internal/secretary/learning_feedback_test.go` for learning and feedback behavior.
- Modify `cmd/lab-secretary/main.go`, `group_vars/all.yml`, and `roles/lab_secretary/tasks/main.yml` to wire and configure retention without adding secrets.
- Modify `docs/secretary-operation.md` and `docs/architecture/master-plan.md` to document commands, retention, privacy, and validated behavior.

## Interfaces Locked by This Plan

```go
type CapabilityResult struct {
    Text        string
    ResultCount int
    Evidence    []EvidenceRef
}

type StructuredSourceReader interface {
    GranolaResult(context.Context) (CapabilityResult, error)
    JiraQueryResult(context.Context, string) (CapabilityResult, error)
}

type EvidenceRef struct {
    Provider string
    ID       string
    Title    string
    URL      string
    RetrievedAt time.Time
}

type CapabilityOutcome struct {
    Capability string
    Outcome string // success, empty, error, blocked
    Duration time.Duration
    ResultCount int
    Evidence []EvidenceRef
}

type InteractionTrace struct {
    SchemaVersion int
    ID string
    OperatorID int64
    At time.Time
    Channel string
    ConversationRef string
    Intent string
    Meaning string
    Outcome string
    TotalDuration time.Duration
    ModelLabel string // empty when deployment does not supply a trusted label
    RouterVersion string
    Capabilities []CapabilityOutcome
}

type FeedbackInput struct {
    Label FeedbackLabel
    Correction string
}

type FeedbackEvent struct {
    SchemaVersion int
    ID string
    InteractionID string
    OperatorID int64
    At time.Time
    Label FeedbackLabel
    Correction string
}

type KnowledgeItem struct {
    SchemaVersion int
    ID string
    OperatorID int64
    Type KnowledgeType
    Subject string
    Predicate string
    Value string
    Aliases []string
    ExternalRefs map[string]string
    Sources []EvidenceRef
    ValidFrom time.Time
    ValidUntil time.Time
    Confidence float64
    Status KnowledgeStatus
    CreatedAt time.Time
    UpdatedAt time.Time
}

type LearningRetention struct {
    TraceDays int
    CandidateDays int
}

type LearningStore interface {
    AppendTrace(InteractionTrace) error
    LastTrace(operatorID int64, channel, conversationRef string) (InteractionTrace, bool, error)
    AppendFeedback(FeedbackEvent) error
    SaveKnowledge(KnowledgeItem) error
    ListKnowledge(operatorID int64, status KnowledgeStatus) ([]KnowledgeItem, error)
    Prune(now time.Time, traceDays, candidateDays int) error
    Forget(operatorID int64) error
}

type EntityType string // client, project, person
type KnowledgeStatus string // candidate, approved, rejected
type KnowledgeType string // preference, fact, entity_alias, relationship, procedure
type FeedbackLabel string // correct, incorrect, wrong_source, missing_source, wrong_entity, missing_fact, style
type EntityResolutionStatus string // resolved, ambiguous, not_found

type EntityResolution struct {
    Status EntityResolutionStatus
    Item *KnowledgeItem
    CandidateIDs []string
}

const learningSchemaVersion = 1
const secretaryRouterVersion = "secretary-router-v1"

func NewJSONLLearningStore(stateDir string) *JSONLLearningStore
func (s *Service) answerFromCapabilities(ctx context.Context, question string, capabilities []ReadCapability, memories []conversationMemory, stream func(string), progress func(string), observer InteractionObserver) (string, []CapabilityOutcome, InteractionBlockReason, error)
func (s *Service) SubmitFeedback(operatorID int64, channel, conversation string, input FeedbackInput) error
func (s *Service) ReviewKnowledge(operatorID int64, action, itemID string) error
func ResolveEntity(query string, entityType EntityType, items []KnowledgeItem) EntityResolution
func (s *Service) RunLearningMaintenance(ctx context.Context, retention LearningRetention)
```

`ReadCapability.Read` changes from `func(context.Context, string) (string, error)` to `func(context.Context, string) (CapabilityResult, error)`. Update all existing capability fixtures and registrations in the same task. For provider responses without a stable source ID, leave `Evidence` empty and retain the explicit result count; never synthesize an ID or URL.

Feedback commands shared by both connected channels:

- `/feedback bien`
- `/feedback mal: <motivo>`
- `/feedback corrige: <corrección verificable o propuesta>`
- `/aprendizaje` lists pending candidate IDs and short descriptions.
- `/aprueba <id>` and `/descarta <id>` review one candidate.

These commands reference the most recent eligible trace for the current
operator/channel/conversation. Feedback commands themselves do not create a new
trace. The service must reject unknown labels, oversized payloads, missing
conversation identity, and IDs owned by another operator.

## Task 1: Typed Capability Results and Multi-Source Routing

**Files:**
- Create: `internal/secretary/capability_result.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/intent.go`
- Modify: `internal/secretary/connectors.go`
- Test: `internal/secretary/service_test.go`
- Test: `internal/secretary/intent_test.go`
- Test: `internal/secretary/connectors_test.go`

**Interfaces:**
- Consumes: existing `ReadCapability`, `capabilityFor`, `answerFromCapability`, and Granola and Jira REST/JQL readers.
- Produces: `CapabilityResult`, `EvidenceRef`, `StructuredSourceReader`, `(*Service).capabilitiesFor(query string, history []Turn) ([]ReadCapability, bool)`, and `(*Service).answerFromCapabilities(...)`.

- [ ] **Step 1: Add failing source-selection tests.** Add `TestCapabilitiesForIntentSelectsExpectedSources`, `TestMeetingTaskQuestionUsesGranolaAndJira`, and `TestMeetingSummaryUsesGranolaOnly`. Assert source names and order, and assert ambiguous matches fail closed.
- [ ] **Step 2: Run the secretary test harness and confirm the new tests fail.** Run `./scripts/test-orchestrator-go.sh`. Expected: compile failure or the new mixed-source assertions fail; existing behavior remains the baseline.
- [ ] **Step 3: Introduce `CapabilityResult` and `EvidenceRef`, then change `ReadCapability.Read` to return `CapabilityResult`.** Update production registrations and test fakes together. `ResultCount` is zero for an explicit empty response; errors stay errors.
- [ ] **Step 4: Implement `(*Service).capabilitiesFor(query string, history []Turn) ([]ReadCapability, bool)` and `answerFromCapabilities(...)`.** Route task questions about meetings to Granola plus Jira REST; route meeting summaries without task intent to Granola; route other task questions to Jira REST. Keep the existing single-capability behavior for non-mixed prompts.
- [ ] **Step 5: Populate only provider-backed evidence.** Add `GranolaResult(context.Context) (CapabilityResult, error)` and `JiraQueryResult(context.Context, string) (CapabilityResult, error)` on `Connectors`; retain the existing string-returning `Granola`/`Jira` wrappers for direct commands. Granola evidence uses parsed meeting IDs/titles/links. Jira evidence uses stable issue keys and titles from the REST response. Add `TestCapabilityResultEvidenceIsNotInvented` for missing and malformed IDs.
- [ ] **Step 6: Run `./scripts/test-orchestrator-go.sh` and commit/push the task.** Expected: all tests pass; commit message `feat(secretary): route mixed-source meeting tasks`.

## Task 2: Versioned JSONL Learning Store

**Files:**
- Create: `internal/secretary/learning_types.go`
- Create: `internal/secretary/learning_store.go`
- Create: `internal/secretary/learning_store_test.go`
- Modify: `internal/secretary/service.go`
- Modify: `group_vars/all.yml`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `cmd/lab-secretary/main.go`

**Interfaces:**
- Consumes: versioned learning types from this task and `StateDir` from `Service`.
- Produces: `LearningStore`, `JSONLLearningStore`, and the exact methods in “Interfaces Locked by This Plan”.

- [ ] **Step 1: Write failing store tests.** Add `TestJSONLLearningStoreRoundTripsVersionedRecords`, `TestJSONLLearningStoreSetsPrivatePermissions`, `TestLearningStoreForgetAndRetention`, and `TestLearningStoreLastTraceUsesConversationScope`. Assert schema version, `0700` directories, `0600` files, `90`-day trace expiry, `30`-day candidate expiry, and preservation of approved unexpired items.
- [ ] **Step 2: Run `./scripts/test-orchestrator-go.sh` and verify the new tests fail.** Expected: missing store types and methods.
- [ ] **Step 3: Define data types in `learning_types.go`.** Include `SchemaVersion`, random trace ID, operator ID, channel, non-reversible conversation reference, intent, selected capability names, per-capability outcome/latency/result count/evidence, feedback label/correction, and knowledge status/type/source/validity fields.
- [ ] **Step 4: Implement `LearningStore` and `JSONLLearningStore` in `learning_store.go`.** Partition files by record class, operator and date under `StateDir`; serialize writes with a mutex, use `O_APPEND` plus one encoded record per line, call `Sync`, enforce `0600` on existing and newly created files, and use temp-file/rename for record rewrites. Reject unsupported schema versions and malformed or oversized records without exposing file contents in errors. Feedback records expire with their linked 90-day trace.
- [ ] **Step 5: Wire the default store and retention configuration.** `New(...)` creates the JSONL store without eagerly writing files. Add `lab_secretary_trace_retention_days: 90`, `lab_secretary_candidate_retention_days: 30`, and optional `lab_secretary_model_label`; pass validated environment values through `roles/lab_secretary/tasks/main.yml` and `cmd/lab-secretary/main.go`. Empty model label remains empty rather than being guessed.
- [ ] **Step 6: Run the store tests and full harness; commit/push.** Expected: store tests pass and existing JSONL memory remains readable without migration. Commit message `feat(secretary): add versioned learning store`.

## Task 3: Interaction Traces and Bounded Metrics

**Files:**
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/interaction.go`
- Modify: `internal/secretary/voice_metrics.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `internal/secretary/interaction_test.go`
- Modify: `internal/secretary/voice_metrics_test.go`

**Interfaces:**
- Consumes: `LearningStore`, `CapabilityResult`, and existing channel-neutral `InteractionEvent` callbacks.
- Produces: one `InteractionTrace` per eligible request, linked capability outcomes and evidence, plus metrics on the existing `/metrics` endpoint.

- [ ] **Step 1: Write failing trace tests.** Add `TestTraceRecordsSelectedCapabilitiesAndEvidence`, `TestTraceDistinguishesEmptyFromConnectorFailure`, and `TestTraceDoesNotPersistPromptOrProviderPayload`. Assert the record contains intent/source/outcome/result IDs/latency, but not question, answer, or full tool payload.
- [ ] **Step 2: Run `./scripts/test-orchestrator-go.sh` and verify those tests fail.** Expected: no traces are appended today.
- [ ] **Step 3: Add request-scoped trace collection in `Service.reply` and the shared routing path.** Generate a random interaction ID at request start, derive the existing conversation fingerprint, set `RouterVersion` to `secretary-router-v1`, capture semantic events and per-capability result/error/latency, and append the trace on every terminal path, including blocked, empty and failed requests. Do not let a trace-write failure discard an otherwise valid user answer; emit a bounded internal error counter.
- [ ] **Step 4: Extend `VoiceMetrics` with `ObserveInteraction(intent, capability, outcome string, duration time.Duration)` and `ObserveMemoryRetrieval(outcome string, hits int)`, each with bounded label allowlists.** Register `lab_secretary_interactions_total`, `lab_secretary_interaction_duration_seconds`, and `lab_secretary_memory_retrieval_total` in the existing registry. Prove via `TestInteractionMetricsUseBoundedLabels` that IDs, arbitrary capability strings and user content collapse to `other` or are excluded.
- [ ] **Step 5: Run targeted secretary tests and full harness; commit/push.** Expected: all trace, progress and metric tests pass; `/metrics` remains available at the existing endpoint. Commit message `feat(secretary): trace routed interactions safely`.

## Task 4: Explicit Feedback and Candidate Review

**Files:**
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/learning_store.go`
- Create: `internal/secretary/learning_feedback_test.go`
- Modify: `internal/secretary/service_test.go`
- Modify: `internal/secretary/telegram_test.go`

**Interfaces:**
- Consumes: `LearningStore.LastTrace`, `AppendFeedback`, and `SaveKnowledge`.
- Produces: `SubmitFeedback(...)`, `ReviewKnowledge(...)`, and the common commands specified above.

- [ ] **Step 1: Add failing command and authorization tests.** Add `TestFeedbackIsScopedToOperatorChannelAndConversation`, `TestFeedbackCommandDoesNotCreateTrace`, `TestFeedbackCreatesUnapprovedCandidate`, and `TestKnowledgeReviewRequiresValidCandidate`. Test `/feedback bien`, `/feedback mal: ...`, `/feedback corrige: ...`, `/aprendizaje`, `/aprueba <id>`, and `/descarta <id>` through the same `ReplyInConversation` path used by Telegram and Open WebUI.
- [ ] **Step 2: Run the secretary harness and verify the tests fail.** Expected: commands are not recognized and feedback is not persisted.
- [ ] **Step 3: Implement `SubmitFeedback` and `ReviewKnowledge`.** Look up the latest trace by authenticated operator, channel, and conversation fingerprint. Never accept a trace ID or candidate ID owned by another operator. Bound correction text and reject empty or unknown labels.
- [ ] **Step 4: Add command parsing and concise responses in `Service.reply`.** Feedback commands bypass LLM inference and do not themselves produce interaction traces. A correction creates a `candidate`, not an approved fact; review commands transition only the named candidate to `approved` or `rejected`.
- [ ] **Step 5: Run targeted and full tests; commit/push.** Expected: Telegram and Open WebUI accept the shared text commands; no connector writes occur. Commit message `feat(secretary): capture and review operator feedback`.

## Task 5: Approved Knowledge, Entity Resolution, and Memory Retrieval

**Files:**
- Modify: `internal/secretary/learning_types.go`
- Create: `internal/secretary/entities.go`
- Modify: `internal/secretary/memory.go`
- Modify: `internal/secretary/service.go`
- Modify: `internal/secretary/learning_store.go`
- Create: `internal/secretary/knowledge_test.go`
- Modify: `internal/secretary/conversation_memory_test.go`

**Interfaces:**
- Consumes: approved `KnowledgeItem` records, existing lexical `relevantMemoriesAt`, and `ResolveEntity(...)`.
- Produces: approved knowledge retrieval with source/date metadata and `ResolveEntity(query string, entityType EntityType, items []KnowledgeItem) EntityResolution` from Task 1/2 types.

- [ ] **Step 1: Write failing retrieval and entity tests.** Add `TestResolveEntityUsesExactExternalIDAndApprovedAlias`, `TestResolveEntityLeavesAmbiguousAliasUnresolved`, `TestPendingKnowledgeIsNeverInjected`, `TestApprovedKnowledgeIncludesSourceAndValidity`, and `TestHistoricalKnowledgeDoesNotOverrideLiveSourceState`.
- [ ] **Step 2: Run the secretary harness and verify the tests fail.** Expected: no entity registry or approved-knowledge retrieval exists.
- [ ] **Step 3: Define entity fields in `learning_types.go` and implement `ResolveEntity(query string, entityType EntityType, items []KnowledgeItem) EntityResolution` in `entities.go`.** Match exact external IDs and approved normalized aliases; return unresolved when multiple candidates match or when only a pending candidate matches. Never auto-merge people by a shared display name.
- [ ] **Step 4: Add `Service.relevantKnowledge(operatorID int64, query string, at time.Time) ([]KnowledgeItem, error)`.** Filter by operator, status, validity and date before lexical ranking. Include source labels and dates in the prompt. Keep existing conversation recall as episodic context, clearly distinguished from verified facts.
- [ ] **Step 5: Add the knowledge context to the common synthesis path.** Current Jira REST state must still be fetched live; approved memory may supply context and entity links, not replace live task status. Pending or rejected knowledge never enters model context.
- [ ] **Step 6: Run targeted and full tests; commit/push.** Expected: lexical retrieval stays bounded, ambiguous identities ask for clarification, and approved facts preserve provenance. Commit message `feat(secretary): retrieve approved work knowledge`.

## Task 6: Forget, Retention, Operational Documentation, and Release

**Files:**
- Modify: `internal/secretary/memory.go`
- Modify: `internal/secretary/learning_store.go`
- Modify: `internal/secretary/conversation_memory_test.go`
- Modify: `cmd/lab-secretary/main.go`
- Modify: `roles/lab_secretary/tasks/main.yml`
- Modify: `group_vars/all.yml`
- Modify: `docs/secretary-operation.md`
- Modify: `docs/architecture/master-plan.md`

**Interfaces:**
- Consumes: the learning store deletion/pruning methods and validated retention configuration.
- Produces: end-to-end `/forget` semantics, documented feedback/review operation, and reproducible deployment configuration.

- [ ] **Step 1: Write failing deletion and retention integration tests.** Add `TestForgetRemovesLearningDataAndConversationMemory` and extend `TestLearningStoreForgetAndRetention` to prove one operator's deletion does not remove another operator's state.
- [ ] **Step 2: Run the secretary harness and verify the deletion integration fails.** Expected: `/forget` currently removes conversation history but not learning records.
- [ ] **Step 3: Extend `Service.forget(operatorID int64) error` to cascade through histories, daily/legacy memory, traces, feedback, candidates and approved knowledge.** Return a generic failure without exposing paths or stored text.
- [ ] **Step 4: Implement retention pruning at startup and once every 24 hours.** Use validated 90-day trace and 30-day unresolved-candidate defaults; never prune approved knowledge before explicit expiry. Add `TestLearningMaintenancePrunesOnStartAndDaily`. Ensure cleanup errors do not prevent the HTTP service from starting, but expose a bounded operational metric.
- [ ] **Step 5: Update `docs/secretary-operation.md`, `docs/architecture/master-plan.md`, `group_vars/all.yml`, and `roles/lab_secretary/tasks/main.yml`.** Document command syntax, reviewed-candidate lifecycle, retention, privacy and current read-only/multi-source behavior. Keep all live entity mappings and real interaction data out of Git.
- [ ] **Step 6: Run repository verification.** Run `./scripts/test-orchestrator-go.sh`, `./scripts/build-orchestrator-go.sh`, and `ansible-playbook --syntax-check playbooks/deploy-secretary.yml`. Expected: all secretary and repository Go tests pass, Linux binary builds, and the role syntax is valid.
- [ ] **Step 7: Commit/push and deploy the secretary through its tracked playbook.** Preserve existing configuration and secrets; deploy only the secretary, verify its health and `/metrics`, then run authorized read-only canary prompts for Granola-only, Jira REST-only and mixed-source routing. Roll back the secretary binary/configuration if health or canary checks fail. Commit message `feat(secretary): complete supervised memory learning loop`.

## Completion Criteria

- All six tasks are committed and pushed; `main` is clean and matches `origin/main`.
- Repository test/build harnesses and Ansible syntax check pass.
- Telegram and Open WebUI use identical routing, feedback, review, retrieval and forget semantics.
- Cross-source meeting/task questions retrieve both sources and each factual claim is tied to provider evidence when the provider supplies stable references.
- No raw provider payload is present in traces or Prometheus; feedback corrections remain private and are deleted by `/forget`.
- The canary reports source selection and evidence coverage; it does not infer success from progress emojis or an HTTP health check alone.
