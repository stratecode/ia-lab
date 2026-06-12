# Autonomous Objective Runtime Plan

This document defines the shortest credible path from the current initiative-and-task runtime to a system where an operator can provide one engineering objective and the agent set can develop it with bounded autonomy and useful outcomes.

## Target Capability

The target is not “more tools.” The target is this:

1. an operator submits one objective against a real workspace
2. the orchestrator creates or links an initiative automatically
3. the system builds an execution contract with explicit success criteria
4. agents research, edit, validate, review, and replan without manual babysitting
5. the initiative closes as approved, blocked, or exhausted with artifacts that explain why

If the system cannot do those five things, it is not yet an autonomous engineering runtime.

## Current Verified State

The runtime now has these verified properties:

- direct `POST /objectives` entrypoint creates an initiative and materializes objective work
- the core contract types exist: `ObjectiveRequest`, `ExecutionContract`, `WorkItem`, `ReviewPacket`, `InitiativeResolution`
- ordinary Git repositories no longer auto-fall back to the deterministic benchmark workflow
- local execution supports explicit `aider-task` edits
- review is based on real diff plus validation evidence
- a failed validation or a `changes_requested` review now triggers an automatic repair cycle with bounded retries
- objective iterations now persist explicit summary and repair-signal artifacts
- objective iterations now persist operator-facing status snapshots with current iteration, remaining retries, next action, and blocker reason when applicable
- `aider-task` invocations are now enriched with structured metadata, scope paths, validation commands, repair feedback, and retrieved context
- `aider-task` executions now persist a durable `coder_packet` with changed files, git self-check evidence, and scope-correction metadata, so edit attempts are inspectable as coder output instead of opaque command runs
- `research_project` can now build a repair-oriented plan with failure hypotheses, narrowed scope, and per-file patch intent instead of returning only generic scaffold advice
- the first `edit` work item can now inherit research-derived scope, patch intent, and edit brief before execution, instead of treating research as decorative preamble
- research can now infer the initial edit scope from README/repository evidence even when the objective does not name the target file directly
- README-driven inference now distinguishes “current” from “legacy” candidates when multiple files are plausible first targets
- the planner now persists `initial_scope_hypotheses` and `rejected_scope_hypotheses` in the execution contract, with explicit rank, score, and rejection rationale, so the first scope guess and the discarded nearby competitors are inspectable before runtime research executes
- planner scope hypotheses can now cite retrieved precedent refs directly, so first-pass scope guesses are backed by inspectable retrieval evidence instead of context that only exists off to the side
- the reviewer now cites both selected and rejected planner hypotheses in machine-readable findings, so repair cycles can tell the difference between “the planner guessed the right file”, “the planner guessed the wrong file”, and “the code landed in a path the planner explicitly rejected”
- the reviewer now also records whether the observed diff aligned with the retrieved precedents carried into the edit, so review can distinguish “good scope, bad implementation” from “bad precedent, bad scope”
- the repair loop can now insert a fresh baseline-validation step before the next edit when planner or retrieval evidence was contradicted, so the next attempt can re-anchor on a reproduced failure instead of editing blindly
- the initial planner can now materialize secondary documentation-sync, config-sync, and dependency-sync edit workstreams for mixed objectives, so some first-pass graphs are now genuinely multi-step instead of forcing everything through one edit
- the review packet now carries explicit alignment for `planner_scope_alignment`, `planner_rejected_scope_alignment`, and `research_scope_alignment`, giving replanning enough structure to react differently to a bad shortlist, a bad edit, or a clean exclusion of rejected competitors
- the reviewer now emits whether the actual diff confirmed or contradicted planner and researcher scope expectations, so review outcomes are explainable against prior agent hypotheses
- reviewer outcomes now persist machine-readable alignment findings and the repair loop carries them into replanning metadata, so contradicted planner/research hypotheses can directly shape the next attempt
- the objective integration harness now covers multiple autonomy scenarios: repair-loop completion, approval mid-flight, blocked exhaustion, memory reuse across initiatives, research-guided initial scope, README-guided scope inference, and README-guided disambiguation between competing files
- repo-specific memory retrieval now keeps `workspace_root` boundaries, so objectives do not accidentally retrieve precedent from unrelated temporary workspaces that merely share the same repo profile
- local bridge lease recovery is now verified: if one bridge claims a task and disappears, a second bridge on the same workspace can reclaim the expired task and still close the initiative successfully
- server restart recovery is now verified for the local objective path: if the HTTP runtime is rebuilt against the same Postgres state after an interrupted local-bridge claim, a newly registered bridge can reclaim the persisted interrupted task and still close the initiative successfully
- objective-level time budgets now exist in the runtime contract and are enforced before new repair work is materialized or queued, and also when a non-terminal local step returns success after the deadline has already expired; those overruns now stop the initiative with explicit `time_budget_exhausted` status snapshots instead of pretending progress can continue
- initiative detail responses now expose an objective-first operator view with the latest status snapshot and latest key runtime artifacts, so operators and the objective runner can inspect current state without separate artifact archaeology

That is useful progress. It is still not full autonomy.

## Non-Negotiable Principles

- Objective-first, not task-first. Human entry should be an objective, not a manually curated task graph.
- Contracts before execution. Every autonomous attempt needs explicit completion criteria, validation commands, and approval policy.
- Review is terminal authority for code quality, not scaffolding existence.
- Repair loops are first-class. Validation and review failures must produce the next attempt, not just a red status.
- Initiative memory is primary. Repo-level memory is supporting context, not the main execution identity.
- Evidence beats claims. Every “autonomous” capability must be validated on a real repository path.

## Delivery Plan

### Iteration 1: Objective Runtime Closure

Goal: close the minimal autonomous loop for one objective on one workspace.

Required work:

- keep `POST /objectives` as the human entrypoint
- persist one canonical execution contract per objective
- materialize `research -> edit -> validate -> review`
- auto-start a bounded `replan -> edit -> validate -> review` cycle on failure
- expose final initiative outcome through artifacts and task results

Exit criteria:

- one objective against a real repo runs without manual task creation
- at least one failed review can trigger a second autonomous repair attempt
- the initiative ends in either `completed` or `blocked` with explicit evidence

Evidence:

- end-to-end test using a temporary repo
- stored diff artifact
- stored validation artifact
- stored review packet
- stored repair-cycle artifacts when failure occurs

### Iteration 2: Planner and Replanner Become Real

Goal: stop using placeholder decomposition and make planning materially useful.

Required work:

- replace keyword routing with objective analysis that produces typed `WorkItem`s intentionally
- add a dedicated replanner contract that consumes prior findings, failed validations, and review comments
- let the planner choose among `research`, `edit`, `validate`, `review`, `replan`, and optional `code_analysis`
- preserve iteration history across replan cycles

Exit criteria:

- the planner can explain why each work item exists
- replanning changes the next attempt based on the actual findings, not static templates
- repeated failures become narrower and more specific over iterations

Evidence:

- planner/replanner unit tests on multiple repo shapes
- artifact diff between initial contract and repair contract
- captured examples where later iterations materially differ from the first

### Iteration 3: Real Agent Backends, Not Named Placeholders

Goal: make each agent’s execution path correspond to useful work rather than role labels.

Required work:

- coder executes `inspect -> edit -> test -> diff summary` with explicit tool use
- reviewer consumes `ReviewPacket` artifacts instead of inferring missing state
- researcher gathers local repo evidence and prior findings before edits
- planner/replanner can request static analysis when validation failures suggest structural issues
- the bridge capability contract becomes explicit and enforced per work item

Exit criteria:

- each agent type has a distinct and testable responsibility
- a coder task can be replayed deterministically from its stored context and contract
- reviewer outcomes are reproducible from stored diff and validation artifacts

Evidence:

- replayable task fixtures
- bridge capability audit
- deterministic local tests for diff, validation, and review inputs

### Iteration 4: Initiative-First Learning and Recovery

Goal: let the system improve across attempts and across related initiatives without hallucinating policy.

Required work:

- store initiative-level execution summaries and outcomes as first-class memory objects
- capture repair-loop lessons separately from benchmark lessons
- rank prior initiative evidence using initiative, repo profile, failure class, validation pattern, and fix pattern
- feed successful prior repairs into the next similar objective as bounded context

Exit criteria:

- a second related objective benefits from the first one without copying irrelevant noise
- retrieval prioritizes successful same-pattern initiatives over generic benchmark memory
- reviewers can cite prior precedent artifacts used in the decision path

Evidence:

- initiative-first evidence ranking tests
- before/after traces showing improved context selection
- artifact lineage from prior initiative to current execution

### Iteration 5: Autonomy Hardening

Goal: make the system robust enough that autonomy survives operational reality.

Required work:

- recover objective execution after orchestrator or bridge interruption
- enforce maximum repair iterations and escalation policy
- add objective-level time budgets and stop conditions
- emit operator-facing status summaries that explain current phase, blockers, and next action
- add benchmark-plus-real-repo evaluation for autonomy quality

Exit criteria:

- active objective survives a bridge restart and continues or fails cleanly
- long-running objectives do not loop indefinitely
- operators can inspect why the runtime is continuing, waiting, blocked, or exhausted

Evidence:

- interruption recovery test
- process-restart recovery test
- bounded-repair-loop test
- initiative status snapshots with operator-readable summaries
- autonomy harness runs without teardown warnings caused by orphaned background indexing work

## Definition of “Autonomous and Useful”

The system only earns that description when all of these are true:

- the operator gives one objective, not a handcrafted sequence of tasks
- the runtime produces real repo changes
- validation runs automatically
- review can reject changes and trigger a corrective iteration
- the loop stops for a reason: approved, blocked, or exhausted
- the final state is backed by stored artifacts and repeatable tests

Anything weaker is partial autonomy, not closure.

## Next Engineering Steps

In implementation order:

1. make the planner emit stronger first-pass hypotheses: preferred file, rejected competitors, and confidence rationale as structured artifacts, not only contract fields
2. let the reviewer distinguish confirmed, contradicted, and irrelevant hypotheses with richer severity and per-file rationale, not only binary alignment findings
3. distinguish lease-based task recovery, verified process-restart recovery, and true in-flight command resumption, then implement the last one explicitly instead of implying they are the same problem
4. refine objective-level time budgets from queue-boundary enforcement to richer wall-clock policy, including per-objective grace/approval semantics when a long-running local command crosses the deadline
5. keep expanding the autonomy eval harness until it covers the full scenario set defined in the agent-system plan, including ambiguous objectives that require research before any edit is safe
6. teach the planner and replanner to request deeper analysis selectively when findings indicate structural failures instead of simple file-local bugs
7. promote the memory service contract from an implicit helper to an explicit agent-facing artifact producer with citations, confidence, and retrieval policy
8. separate lease-based local bridge recovery and verified process-restart recovery from true mid-command resumption, then implement that last capability explicitly instead of implying all interruption modes are solved
