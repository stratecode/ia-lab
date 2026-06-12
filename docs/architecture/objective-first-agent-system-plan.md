# Objective-First Agent System Plan

This document defines the path from the current autonomous objective runtime to a system where a bounded set of agents can receive one engineering objective and develop it with useful autonomy.

## Target

The target capability is this:

1. an operator submits one objective against one workspace
2. the orchestrator turns that objective into an execution contract
3. specialist agents work that contract without manual task choreography
4. the system validates and reviews its own output
5. the initiative stops with evidence: approved, blocked, or exhausted

If those five points are not true on a real repository, the system is not autonomous enough.

## Agent Set

The system should converge on six agents with explicit contracts.

### 1. Orchestrator

Responsibility:

- own initiative lifecycle
- decide when to continue, replan, escalate, block, or stop
- enforce retry budgets, approval policy, and execution boundaries

Inputs:

- objective request
- initiative state
- task results
- approval state
- operator policy

Outputs:

- initiative state transitions
- work item scheduling
- operator-facing status snapshots

### 2. Planner

Responsibility:

- convert one objective into an execution contract and first-pass work graph

Inputs:

- objective
- workspace context
- retrieved precedent
- operator constraints

Outputs:

- `ExecutionContract`
- initial `WorkItem` sequence
- success criteria and validation contract

The planner is successful only if every work item exists for a reason that can be explained and tested.

### 3. Researcher

Responsibility:

- reduce uncertainty before edits and during repair cycles

Inputs:

- execution contract
- local repository evidence
- prior findings
- prior findings

Outputs:

- narrowed scope
- patch intent
- repair plan
- evidence artifacts that justify the scope

The researcher should stop being generic. It must answer: what files matter, why, and what should the next edit try to achieve?

### 4. Coder

Responsibility:

- execute repository changes with bounded scope

Inputs:

- edit work item
- patch intent
- retrieved context
- validation contract

Outputs:

- diff
- changed file set
- execution notes

The coder is not “a tool wrapper.” Its contract is `inspect -> edit -> self-check -> produce diff`.

### 5. Reviewer

Responsibility:

- decide whether the current attempt is acceptable

Inputs:

- `ReviewPacket`
- validation results
- diff
- initiative precedent

Outputs:

- `approved`
- `changes_requested`
- `rejected`
- structured findings

The reviewer is the quality gate. If review is weak, autonomy is fiction.

The reviewer should not only decide. It should also say whether the observed diff validated or contradicted the prior scope hypotheses from the planner and the researcher.

### 6. Memory/Retrieval Agent

Responsibility:

- serve bounded precedent, not generic noise

Inputs:

- initiative history
- prior repair loops
- repo profile
- validation/failure class

Outputs:

- ranked context package
- cited prior repairs
- confidence-relevant precedent

This can remain a service behind the scenes at first, but the contract must already be explicit.

## Contracts Between Agents

The runtime should standardize these artifacts as the inter-agent language:

- `ObjectiveRequest`
- `ExecutionContract`
- `WorkItem`
- `ReviewPacket`
- `ObjectiveStatusSnapshot`
- `InitiativeResolution`

Each artifact must be durable, inspectable, and replayable. If an agent needs hidden process state to work, the contract is not finished.

The `ExecutionContract` should now be treated as carrying an explicit first-pass scope guess, not just a flat `suspected_paths` list. That means the planner must be able to say:

- what file or area it thinks matters first
- why it believes that
- what evidence it used
- how confident it is

## Runtime Stages

The full objective-first execution loop should be:

1. objective received
2. initiative opened
3. execution contract generated
4. research scope and patch intent produced
5. edit executed
6. validation executed
7. review decided
8. replan if necessary
9. initiative resolved

Every stage must leave artifacts that explain what happened and what comes next.

## Delivery Roadmap

### Stage A: Runtime Closure

Goal:

- one objective runs autonomously with repair loops, review, and operator-visible status

Required:

- objective entrypoint
- execution contract persistence
- bounded repair loop
- initiative status snapshots
- CLI or API path that can run the lifecycle end-to-end

Proof:

- objective E2E on temporary repo
- at least one repair cycle
- terminal evidence artifact
- clean teardown of the autonomy harness with no orphaned background work writing into closed infrastructure

Status:

- verified
- `POST /objectives` exists
- bounded repair loop exists
- CLI runner exists
- the autonomy E2E already covers repair, approval, block, memory reuse, and research-guided first scope

### Stage B: Real Planning

Goal:

- planner and replanner produce materially different work graphs based on evidence

Required:

- planner chooses typed work items intentionally
- replanner consumes structured findings and changed files
- scope narrows over iterations instead of drifting

Proof:

- planner unit tests with multiple repo shapes
- repair iteration artifacts that differ from iteration 1
- first-iteration evidence where research changes the initial edit scope before any patch is attempted
- first-iteration evidence where repository documentation, not explicit file names, drives the initial edit scope
- first-iteration evidence where repository documentation ranks the right file ahead of a plausible but legacy competitor

Status:

- partially verified
- planner now persists `initial_scope_hypotheses`
- planner now also persists rejected nearby competitors with explicit rank, score, and rejection rationale
- planner hypotheses can now carry direct retrieval citation refs, so precedent influences are durable and inspectable instead of remaining implicit in `context_package`
- reviewer now emits machine-readable findings for both selected and rejected planner hypotheses, so replanning can distinguish a bad shortlist from a bad edit against a good shortlist
- reviewer now also emits retrieval-alignment evidence, so replanning can tell whether the problem came from a bad precedent or from a bad edit against otherwise useful precedent
- repair cycles can now branch through a fresh baseline-validation step before re-editing when planner or retrieval evidence has been contradicted, giving the next edit reproduced failure evidence instead of pure narrative feedback
- the planner can now decompose mixed implementation-plus-documentation, implementation-plus-config, and implementation-plus-dependency objectives into coordinated edit workstreams, so the first-pass graph is no longer strictly single-edit when the objective explicitly spans code plus docs, infra/config, or dependency manifests/lockfiles
- review packets now expose explicit alignment dimensions for selected planner scope, rejected planner scope, and research scope, which is enough structure for the replanner to stop treating all scope failures as the same class
- researcher can refine scope before the first edit
- replanner already emits `failure_hypotheses` and `patch_intent`
- still missing: stronger replanner policy that materially changes the next graph from those citations, and richer first-pass branching beyond the current documentation-sync, config-sync, dependency-sync, and selective analysis/baseline branches

### Stage C: Distinct Agent Responsibilities

Goal:

- each agent has testable behavior that is not reducible to a role label

Required:

- researcher emits scope and rationale
- coder emits diff and self-check evidence
- reviewer emits decision plus findings
- orchestrator emits control decisions and stop reasons

Proof:

- replayable fixtures per agent
- capability audit per work item kind

Status:

- partially verified
- researcher, coder, reviewer, and orchestrator now emit distinct artifacts with non-trivial responsibilities
- coder executions now persist a `coder_packet` with changed files, git self-check evidence, and scope-correction metadata, so edit attempts are replayable as coder output instead of opaque `aider-task` runs
- reviewer decisions now emit machine-readable contradiction findings that the repair loop carries into replanning
- still missing: an explicit memory/retrieval contract as a first-class agent output and optional analysis branches that the planner can request intentionally

### Stage D: Memory That Helps

Goal:

- similar future objectives start with better precedent and less noise

Required:

- initiative-first retrieval
- repair-loop memory
- citation of precedent in planning and review

Proof:

- before/after retrieval tests
- trace showing prior initiative improved a later one
- autonomy eval where a second initiative starts with retrieved precedent from the first

Status:

- partially verified
- same-workspace initiative reuse is covered by the autonomy harness
- repo-specific retrieval no longer leaks across unrelated temporary workspaces that merely share the same repo profile
- still missing: stronger retrieval citations in planner/reviewer outputs and explicit retrieval-policy artifacts

### Stage E: Hardening and Governance

Goal:

- the system behaves predictably under interruption, exhaustion, and approvals

Required:

- resume after bridge interruption
- explicit retry and time budgets
- blocked vs exhausted differentiation
- operator summaries that match actual state

Proof:

- interruption recovery test
- bounded loop test
- status snapshot audit

Status:

- started
- retry exhaustion and operator-visible status snapshots are already verified
- stale local-bridge lease recovery is now verified through the autonomy harness: a second bridge can reclaim the interrupted task and finish the initiative
- local server-restart recovery is now also verified through the autonomy harness: the HTTP runtime can be rebuilt on the same persisted state, a new bridge can reclaim the interrupted task, and the initiative still closes correctly
- objective-level time budgets now exist and stop new repair iterations before they are materialized or queued, and they also stop non-terminal overrun results that come back after the deadline, with explicit `time_budget_exhausted` snapshots; this is real progress, but still not true mid-command preemption
- initiative detail responses now expose an objective-first runtime view with the latest status snapshot and latest key artifacts (`coder_packet`, `review_packet`, `repair_signal`, retrieval packets), and the objective runner now consumes that view directly before falling back to raw artifact scans
- still missing: true mid-command resumption after process loss and richer stop-policy enforcement beyond retry ceilings and queue-boundary time budgets

## Evals That Decide Progress

The project should stop using “it feels more agentic” as a success metric. Use these evals instead:

1. objective with one clean edit and no repair
2. objective with one failing validation and one successful repair
3. objective that reaches max repair iterations and blocks cleanly
4. objective requiring approval mid-flight
5. objective that should trigger research before edit
6. second objective on same repo benefiting from prior initiative memory
7. objective harness teardown stays clean: no `closed pool`, no fake-embedding calls after fixture shutdown

If those seven evals are not green, the autonomy claim is incomplete.

## Non-Negotiable Boundaries

- No hidden mutable state between agents.
- No “planner” that just routes by keywords.
- No “review” that checks for scaffold existence instead of behavior.
- No repair loop that retries without narrowing scope.
- No operator summary that guesses instead of reading artifacts.

## Immediate Next Moves

In order:

1. make the planner explain why one first-pass scope hypothesis beat nearby competitors
2. expose `ObjectiveStatusSnapshot` through operator flows that poll initiatives
3. enrich reviewer contradiction findings with per-file rationale and explicit “irrelevant hypothesis” cases so replanning can discriminate noise from genuine scope drift
4. add process-restart recovery evals and then implement bridge/orchestrator recovery until they pass
5. distinguish restart recovery from true mid-command resumption and implement the latter intentionally instead of hand-waving both under the same label
6. extend objective-level time budgets from queue-boundary stops to fuller wall-clock policy, including how long-running edits/reviews should be interrupted, deferred, or approved when they cross the deadline
7. promote retrieval from helper-service status to an explicit agent contract with citations, confidence, and retrieval policy
