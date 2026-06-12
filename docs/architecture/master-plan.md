# Master Plan

This document is the canonical product and execution roadmap for the platform. It defines what the system is today, what counts as validated, what remains outside the current MVP closure, and which implementation steps matter next.

If another document disagrees on product direction, roadmap, MVP boundaries, or the meaning of "validated", this document wins.

## Positioning

The platform is an `initiative-driven` execution system for governed AI-assisted work.

Its current core is:

- initiative lifecycle above individual tasks
- approval gates and explicit human control
- selective execution instead of blind "run everything"
- Go control plane as the system of record
- local bridge and TUI/CLI as the primary workspace execution surface
- narrow Python sidecars for document and image capabilities only

The intended direction is stronger multi-agent autonomy. That direction is real, but it is not yet a closed product capability. The system must not be described as a general autonomous agent platform today.

## Current State

### What is validated now

- Go control plane on `orchestrator.service`
- initiative lifecycle: `idea -> requirements -> design -> plan -> selective execution`
- local bridge flow through `lab-agent`, `lab-agentd`, and `lab-agent tui`
- operator entry points through Telegram, Open WebUI, and the orchestrator API
- research and capability flows for web, documents, and images
- project-scoped capability registry and broker with governed capability policies by `repository_url`
- persisted tasks, approvals, artifacts, evaluations, and initiative state
- observability through health, metrics, logs, and persisted execution traces
- official benchmark harness as the canonical initiative-driven validation path
- benchmark evidence for `repo_recall`, `technology_transfer`, and `pattern_transfer`
- `agent_maturity` evidence for `planner`, `researcher`, `coder`, `reviewer`, and multi-agent coordination
- lease-based local bridge recovery after daemon interruption and full host reboot
- benchmark-level capability provenance through `capability_usage`, `capability_helped`, and per-project policy enforcement

### What is still incomplete or narrow

- end-to-end autonomous multi-agent routing on arbitrary work
- reviewer behavior that is broadly useful for real repositories, not just constrained flows
- fine-grained checkpoint-and-continue for long-running subprocess execution
- external reference evaluation that is stable across broader repo classes

## Golden Path

The official golden path is the workflow the system must close without ambiguity. MVP closure is defined against this path, not against endpoint count.

1. initiative creation
2. requirements generation
3. requirements approval
4. design generation
5. design approval
6. execution plan generation
7. task materialization
8. selective launch
9. local execution inside the registered workspace
10. review and approval where required
11. task reconciliation
12. initiative reconciliation to execution completion

This path is the primary measure of product integrity. If it is not reliable, the platform is still promising infrastructure, not a finished operating model.

## Roadmap

### Iteration 1: Product Alignment

Objective: one coherent story, one vocabulary, one source of truth.

- establish this document as the canonical roadmap
- remove contradictory product narratives from adjacent docs
- standardize the meanings of `validated`, `MVP`, `gap`, `initiative`, and `selective execution`
- keep runtime, operations, and architecture docs descriptive instead of strategic

### Iteration 2: Golden Path Closure

Objective: prove the full governed workflow works end to end.

- define a reproducible E2E scenario for the official golden path
- make the official objective/initiative harness the canonical execution path for repo workflow validation
- run that harness through `initiative -> plan -> tasks -> selective launch -> review -> reconciliation`, not through side runners or manual shortcuts
- verify initiative advancement, backlog generation, selective launch, local execution, review, and final reconciliation
- expose enough observability to understand failure point, duration, and operator interventions
- treat the official harness as the only canonical evidence path for benchmark claims; manual runners remain diagnostic only

### Iteration 3: Real Repository Utility

Objective: move from controlled workflow to useful engineering work on existing code.

- validate a real repository change flow
- ensure tasks produce meaningful diffs and test results
- improve reviewer usefulness on repo-shaped work
- tighten bridge behavior, result reporting, and version compatibility
- measure repo utility through three benchmark leagues:
  - `repo_recall` for local continuity on the same repository
  - `technology_transfer` for reuse across repositories that share stack and framework traits
  - `pattern_transfer` for reuse across repositories that share error, fix, or validation patterns
- add an `agent_maturity` suite on top of the official harness so `planner`, `researcher`, `coder`, and `reviewer` are measured as:
  - individual capability
  - handoff quality
  - end-to-end system usefulness
- extend `agent_maturity` beyond repo-local recall so reviewer quality is measured on `pattern_transfer` sequences too, not only on same-repo memory
- run official maturity campaigns with `runs_per_case=3` so progression claims are based on repeated evidence instead of a lucky first pass
- use two baselines for curated maturity cases:
  - `standard`
  - `reference_external`
- treat reusable technology/pattern memory as an evidence-backed hypothesis with early signal, not as a universal mature capability yet

Current evidence snapshot from the official benchmark harness on May 24, 2026:

- `repo_recall`
  stable and repeatable
  curated repo-local runs remained stronger than the baseline suite across `pydantic`, `typer`, and `fastify`
- `technology_transfer`
  useful but still noisier
  the PHP sequence `monolog -> math-php -> slim` averaged `81.1` vs `55`
  signal came from `technology_similar` memory with no `repo_specific` hits
  variability is still materially higher than `repo_recall`
- `pattern_transfer`
  now validated on the official initiative-driven harness
  the HTTP client sequence `axios -> httpx` averaged `85` vs `55`
  signal came from `pattern_similar` memory with no `repo_specific` hits
- `negative_transfer`
  guarded behavior is working on current curated cases
  `httpie-cli` and `vitest` stayed at `55` vs `55`, with zero forbidden hits and `guarded` effect labels

Interpretation:

- the system clearly remembers local repo history
- the system now shows reusable transfer by technology and pattern
- technology and pattern transfer are not equally mature; transfer works, but stability is lower than repo-local recall
- negative transfer control is part of the validated story now, not a future aspiration
- agent maturity is the next validation layer: not just “did the repo pass”, but “did the right agent and the right handoff create that outcome”

Updated evidence snapshot from the official harness and maturity suite on May 26, 2026:

- `agent_maturity`
  validated across `planner`, `researcher`, `coder`, `reviewer`, and `coordination`
  official campaigns run with `runs_per_case=3`
- `pattern_transfer` inside maturity
  validated on the reviewer HTTP client sequence `axios -> httpx`
  signal came from `pattern_similar` memory with no `repo_specific` hits
- capability-governed execution
  benchmark artifacts now persist `capability_usage`, `capability_helped`, `capability_noise`, and `capability_denied`
  current productive signal comes mainly from `filesystem.read` and `code.analysis`
- project-scoped MCP/capability governance
  capabilities are now discovered through a registry and filtered by project policy on `repository_url`
  this is validated on the runtime and benchmark path, not just in isolated API smoke checks
- bridge recovery
  lease reclaim works after bridge interruption
  reboot recovery is validated against the orchestrator host
  the remaining gap is fine-grained subprocess continuation, not task-level recovery

Iteration 3 closure decision:

- `Iteration 3` is considered closed for roadmap purposes
- closure is based on validated real repository utility through the canonical initiative-driven harness
- closure includes:
  - governed repo execution on existing repositories
  - benchmark evidence across `repo_recall`, `technology_transfer`, and `pattern_transfer`
  - maturity evidence across individual agents, handoffs, and coordinated execution
  - lease-based bridge recovery after interruption and reboot
- remaining weaknesses do not block closure
- remaining weaknesses define the next phase

Deferred from `Iteration 3` into the next phase:

- reviewer generalization on less curated repositories
- broader stability of `reference_external`
- fine-grained resume for long-running subprocesses
- wider validation outside the current curated benchmark catalog
- broader MCP ecosystem expansion and utility ranking beyond the current governed baseline

### Phase 4: Generalization and Operational Hardening

Objective: move from validated curated utility to broader, more reliable operational usefulness.

- validate reviewer usefulness on less curated and less cooperative repositories
- broaden benchmark coverage beyond the current curated catalog without losing interpretability
- harden `reference_external` so it becomes a stable comparison tool instead of an occasionally theatrical one
- implement finer-grained resume for long-running local subprocesses
- improve tool and capability ranking so project-scoped MCP use is driven by observed utility, not only by static policy
- expand MCP-backed evidence sources for `researcher` and `reviewer` while keeping `coder` tightly governed
- keep the benchmark harness canonical and use it to measure generalization, not just re-run friendly cases

### Cross-Cutting: Resilience and Resume

Objective: keep the system useful when the host, bridge, or long-running execution is interrupted.

- treat restart recovery as a product capability, not an operational afterthought
- checkpoint task intent, patch payloads, commands, and execution stage before expensive work begins
- persist recovery checkpoints on worker-owned tasks and re-queue stale interrupted remote work on startup as the first practical recovery layer
- make bridge claims lease-based so interrupted work can be safely resumed or re-queued
- validate lease recovery with deliberate fault injection, not just green-path execution
- validate reboot recovery as well: restart the orchestrator host during active local work and confirm lease-based reclaim closes the task after services return
- persist enough execution state to continue from the last durable phase after reboot
- gate heavy local work behind host health and resource budget checks
- prefer reproducible caches or prebuilt artifacts for expensive dependencies such as `llama.cpp`

## Limits and Non-Goals

The current system is not:

- a general autonomous agent product
- a shell replacement through Telegram
- an Open WebUI-centered control plane
- a Python-based orchestrator runtime
- a finished multi-agent coordination product for arbitrary repositories

The current system does use autonomous components, but autonomy is bounded by initiative governance, approvals, execution modes, and local bridge policy.

## Success Metrics

Track progress using operational outcomes instead of architectural vanity.

- time from idea to approved launchable backlog
- time from approved initiative to first useful execution result
- task completion ratio by execution mode: `manual`, `agent_local`, `agent_remote`
- approval, rejection, and replan rates
- failure distribution by bridge, context, runner, and policy
- percentage of initiatives that complete the golden path without extraordinary manual rescue

## Document Roles

Use the surrounding documents like this:

- `README.md`: high-level repository summary and deployment entry point
- `docs/architecture/overview.md`: architecture and system layering
- `docs/orchestrator-go-runtime.md`: current runtime scope and compatibility surface
- `docs/local-bridge.md`: bridge, TUI, and local execution operations
- `docs/system-usage.md`: day-to-day usage through Telegram, WebUI, API, and bridge

None of those documents should redefine roadmap, MVP boundaries, or validated product scope independently.
