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
- local objective tasks recover semantic context from canonical `workspace_root`
- local execution supports explicit `aider-task` edits
- review is based on real diff plus validation evidence
- a failed validation or a `changes_requested` review now triggers an automatic repair cycle with bounded retries
- objective iterations now persist explicit summary and repair-signal artifacts
- objective-launched tasks now receive semantic context packages before queueing, not only the manual initiative-launch path
- `aider-task` invocations are now enriched with structured metadata, scope paths, validation commands, repair feedback, and retrieved context
- `research_project` can now build a repair-oriented plan from prior findings instead of returning only generic scaffold advice

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
- researcher gathers local repo evidence and semantic precedents before edits
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

### Iteration 4: Initiative-First Memory and Learning

Goal: let the system improve across attempts and across related initiatives without hallucinating policy.

Required work:

- store initiative-level execution summaries and outcomes as first-class memory objects
- capture repair-loop lessons separately from benchmark lessons
- rank semantic retrieval using initiative, repo profile, failure class, validation pattern, and fix pattern
- feed successful prior repairs into the next similar objective as bounded context

Exit criteria:

- a second related objective benefits from the first one without copying irrelevant noise
- retrieval prioritizes successful same-pattern initiatives over generic benchmark memory
- reviewers can cite prior precedent artifacts used in the decision path

Evidence:

- semantic retrieval tests for initiative-first ranking
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
- bounded-repair-loop test
- initiative status snapshots with operator-readable summaries

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

1. add an end-to-end objective test that exercises a real repair cycle
2. persist initiative-level execution and repair summaries as dedicated artifacts
3. make replanning consume prior findings explicitly instead of using static repair templates
4. add operator-visible initiative summaries for current iteration, remaining retries, and blocker reason
