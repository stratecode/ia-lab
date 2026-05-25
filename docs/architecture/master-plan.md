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
- semantic memory and context packaging as supporting coordination infrastructure

The intended direction is stronger multi-agent autonomy. That direction is real, but it is not yet a closed product capability. The system must not be described as a general autonomous agent platform today.

## Current State

### What is validated now

- Go control plane on `orchestrator.service`
- initiative lifecycle: `idea -> requirements -> design -> plan -> selective execution`
- local bridge flow through `lab-agent`, `lab-agentd`, and `lab-agent tui`
- operator entry points through Telegram, Open WebUI, and the orchestrator API
- research and capability flows for web, documents, and images
- persisted tasks, approvals, artifacts, evaluations, and initiative state
- observability through health, metrics, logs, and persisted execution traces

### What is still incomplete or narrow

- end-to-end autonomous multi-agent routing on arbitrary work
- reviewer behavior that is broadly useful for real repositories, not just constrained flows
- a canonical golden path validated on an existing real repository from start to finish
- mature use of semantic memory for planning, review, and recovery loops

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
- make the official benchmark harness `scripts/benchmark-context-memory.sh` the canonical execution path for repo workflow validation
- run that harness through `initiative -> plan -> tasks -> selective launch -> review -> reconciliation`, not through side runners or manual shortcuts
- verify initiative advancement, backlog generation, selective launch, local execution, review, memory retrieval, and final reconciliation
- expose enough observability to understand failure point, duration, and operator interventions
- produce benchmark artifacts and aggregate summaries that show whether `memory_on` equals or improves on `memory_off` for prepared repo cases
- treat the official harness as the only canonical evidence path for benchmark claims; manual runners remain diagnostic only

### Iteration 3: Real Repository Utility

Objective: move from controlled workflow to useful engineering work on existing code.

- validate a real repository change flow
- ensure tasks produce meaningful diffs and test results
- improve reviewer usefulness on repo-shaped work
- tighten bridge behavior, result reporting, and version compatibility
- use semantic memory to improve context quality, traceability, and recovery
- measure repo utility through three benchmark leagues:
  - `repo_recall` for local continuity on the same repository
  - `technology_transfer` for reuse across repositories that share stack and framework traits
  - `pattern_transfer` for reuse across repositories that share error, fix, or validation patterns
- add an `agent_maturity` suite on top of the official harness so `planner`, `researcher`, `coder`, and `reviewer` are measured as:
  - individual capability
  - handoff quality
  - end-to-end system usefulness
- run official maturity campaigns with `runs_per_case=3` so progression claims are based on repeated evidence instead of a lucky first pass
- use three baselines for curated maturity cases:
  - `memory_off`
  - `memory_on`
  - `reference_external`
- treat reusable technology/pattern memory as an evidence-backed hypothesis with early signal, not as a universal mature capability yet

Current evidence snapshot from the official benchmark harness on May 24, 2026:

- `repo_recall`
  stable and repeatable
  `memory_on` averaged `80` vs `55` baseline across `pydantic`, `typer`, and `fastify`
  signal came from `repo_specific` memory as expected
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
