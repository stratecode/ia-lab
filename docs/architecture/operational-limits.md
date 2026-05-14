# Operational Limits

Defines the resource constraints, termination policies, and retry behavior that the platform enforces on every task execution to prevent runaway agents and ensure predictable system behavior.

## Token Budgets

Each task type has a configurable maximum token budget representing the total tokens (input + output) an agent may consume during a single task execution. Values are configurable between 1,000 and 128,000 tokens.

| Task Type | Default Max Tokens | Typical Range | Rationale |
|-----------|-------------------|---------------|-----------|
| coding | 128,000 | 32,000–128,000 | Complex implementations require large context windows |
| review | 64,000 | 16,000–64,000 | Full diff analysis with surrounding context |
| planning | 32,000 | 8,000–32,000 | Decomposition and dependency analysis |
| infrastructure | 16,000 | 4,000–16,000 | Targeted operations with known parameters |
| research | 64,000 | 16,000–64,000 | Broad information gathering and synthesis |

### Token Budget Schema

```yaml
token_budget:
  max_tokens: integer     # 1,000–128,000
```

### Token Counting

- Tokens are counted as the sum of input tokens (prompt + context) and output tokens (agent response)
- The Orchestrator tracks cumulative token usage across all inference calls within a single task execution
- Token count is updated after each inference response is received

## Execution Timeouts

Each task type has a configurable maximum wall-clock time. Values are configurable between 10 and 3,600 seconds.

| Task Type | Default Timeout (s) | Typical Range (s) | Rationale |
|-----------|---------------------|-------------------|-----------|
| coding | 3,600 | 600–3,600 | Multi-step implementation with test cycles |
| review | 600 | 120–600 | Single-pass analysis of bounded diff |
| planning | 300 | 60–300 | Decomposition without execution |
| infrastructure | 1,800 | 300–1,800 | Deployment and verification cycles |
| research | 1,200 | 300–1,200 | Iterative search and synthesis |

### Execution Timeout Schema

```yaml
execution_timeout:
  max_seconds: integer    # 10–3,600
```

### Timeout Measurement

- Wall-clock time starts when the agent acknowledges the task (`task.started` event)
- Time includes all inference calls, tool executions, and wait periods
- The Orchestrator monitors elapsed time and enforces termination when the limit is reached

## Retry Policies

Each task type defines a retry policy for transient failures. Maximum retries are configurable between 1 and 10 attempts.

| Task Type | Max Retries | Backoff Strategy | Retryable Errors |
|-----------|-------------|------------------|------------------|
| coding | 3 | exponential | network_timeout, rate_limit, inference_unavailable |
| review | 2 | fixed | network_timeout, rate_limit, inference_unavailable |
| planning | 3 | exponential | network_timeout, rate_limit, inference_unavailable |
| infrastructure | 5 | exponential | network_timeout, rate_limit, inference_unavailable, resource_busy |
| research | 3 | exponential | network_timeout, rate_limit, inference_unavailable |

### Retry Policy Schema

```yaml
retry_policy:
  max_retries: integer    # 1–10
  backoff: enum           # fixed | exponential
  retryable_errors: [string]
```

### Backoff Strategies

| Strategy | Formula | Example (base = 1s) |
|----------|---------|---------------------|
| fixed | `base_delay` | 1s, 1s, 1s, ... |
| exponential | `base_delay * 2^(attempt - 1)` | 1s, 2s, 4s, 8s, ... |

### Retryable Error Categories

| Error Category | Description | Example |
|---------------|-------------|---------|
| `network_timeout` | Connection to inference endpoint timed out | TCP connect timeout to llama.cpp |
| `rate_limit` | Request rejected due to rate limiting | HTTP 429 from inference API |
| `inference_unavailable` | Inference service temporarily down | HTTP 503 from llama.cpp |
| `resource_busy` | Required resource is locked by another operation | Docker container in use by another task |

Non-retryable errors (permission denied, invalid input, unrecoverable model errors) cause immediate task failure without retry.

## Loop Detection

Loop detection identifies when an agent is stuck repeating the same action without making progress. The default repetition threshold is 3 consecutive repetitions.

### Loop Detection Schema

```yaml
loop_detection:
  repetition_threshold: integer  # Default: 3
```

### Detection Rules

A loop is detected when **all** of the following conditions are met:

1. The agent has produced 3 or more consecutive actions (inference calls or tool invocations)
2. Each action is semantically equivalent to the previous one (same tool call with same parameters, or same inference output pattern)
3. The task output state has not changed between repetitions (no new artifacts, no state transitions)

### Equivalence Criteria

| Action Type | Equivalence Check |
|-------------|-------------------|
| Tool invocation | Same tool name + same parameters (normalized) |
| Inference output | Same structural pattern (ignoring timestamps and IDs) |
| File operation | Same file path + same operation type + same content hash |

## Termination Behavior

When any operational limit is exceeded, the platform executes a deterministic termination sequence.

### Budget Exceeded Termination

1. The Orchestrator halts the current inference call (if in progress)
2. The task state transitions to `failed`
3. Partial results are preserved (see [Partial Result Preservation](#partial-result-preservation))
4. A `task.failed` event is emitted with termination reason `budget-exceeded`

**Event payload:**

```json
{
  "task_id": "task-20250714-001",
  "agent_id": "coder",
  "termination_reason": "budget-exceeded",
  "tokens_consumed": 128500,
  "token_limit": 128000,
  "partial_results_preserved": true
}
```

### Timeout Termination

1. The Orchestrator sends a cancellation signal to the agent
2. The agent has a 5-second grace period to finalize current operation
3. The task state transitions to `failed`
4. Partial results are preserved (see [Partial Result Preservation](#partial-result-preservation))
5. A `task.failed` event is emitted with termination reason `timeout`

**Event payload:**

```json
{
  "task_id": "task-20250714-001",
  "agent_id": "infra",
  "termination_reason": "timeout",
  "elapsed_seconds": 1800,
  "timeout_limit_seconds": 1800,
  "partial_results_preserved": true
}
```

### Loop Detection Termination

1. The Orchestrator identifies the repeated action pattern
2. The agent is halted immediately (no grace period — the agent is not making progress)
3. The task state transitions to `failed`
4. Partial results are preserved (see [Partial Result Preservation](#partial-result-preservation))
5. A `task.failed` event is emitted with termination reason `loop-detected`

**Event payload:**

```json
{
  "task_id": "task-20250714-001",
  "agent_id": "planner",
  "termination_reason": "loop-detected",
  "repeated_action": "tool:filesystem.read_file(/src/config.yaml)",
  "repetition_count": 4,
  "partial_results_preserved": true
}
```

## Event Emission

Each termination type emits a `task.failed` event following the [Event Schema](../events/schemas.md). The events are categorized under `task_lifecycle` with severity `ERROR`.

| Termination Type | Event Type | Severity | Category | Key Payload Fields |
|-----------------|------------|----------|----------|-------------------|
| Budget exceeded | `task.failed` | ERROR | task_lifecycle | task_id, agent_id, tokens_consumed, token_limit |
| Timeout | `task.failed` | ERROR | task_lifecycle | task_id, agent_id, elapsed_seconds, timeout_limit_seconds |
| Loop detected | `task.failed` | ERROR | task_lifecycle | task_id, agent_id, repeated_action, repetition_count |

All termination events include `termination_reason` and `partial_results_preserved` fields in the payload.

## Partial Result Preservation

When a task is terminated due to any operational limit, the platform preserves all partial results produced by the agent prior to termination.

### Preserved Artifacts

| Artifact Type | Preservation Method | Retention |
|--------------|--------------------|-----------| 
| Code changes | Git commit on working branch (uncommitted changes stashed) | Retained with workspace |
| Generated files | Kept in task workspace directory | Retained per workspace policy |
| Inference outputs | Stored in task execution log | Retained with task record |
| Tool execution results | Stored in task execution log | Retained with task record |
| Partial plans | Stored as incomplete task plan artifact | Retained with task record |

### Preservation Guarantees

- Partial results are associated with the failed task record via `task_id`
- Results remain accessible for inspection, debugging, and potential manual continuation
- Workspace retention follows the policies defined in [Workspace Isolation](workspace-isolation.md)
- The `partial_results_preserved: true` flag in the termination event confirms successful preservation

## Complete Operational Limits Configuration

The full configuration combining all limits per task type:

```yaml
operational_limits:
  - task_type: coding
    token_budget:
      max_tokens: 128000
    execution_timeout:
      max_seconds: 3600
    retry_policy:
      max_retries: 3
      backoff: exponential
      retryable_errors:
        - network_timeout
        - rate_limit
        - inference_unavailable
    loop_detection:
      repetition_threshold: 3

  - task_type: review
    token_budget:
      max_tokens: 64000
    execution_timeout:
      max_seconds: 600
    retry_policy:
      max_retries: 2
      backoff: fixed
      retryable_errors:
        - network_timeout
        - rate_limit
        - inference_unavailable
    loop_detection:
      repetition_threshold: 3

  - task_type: planning
    token_budget:
      max_tokens: 32000
    execution_timeout:
      max_seconds: 300
    retry_policy:
      max_retries: 3
      backoff: exponential
      retryable_errors:
        - network_timeout
        - rate_limit
        - inference_unavailable
    loop_detection:
      repetition_threshold: 3

  - task_type: infrastructure
    token_budget:
      max_tokens: 16000
    execution_timeout:
      max_seconds: 1800
    retry_policy:
      max_retries: 5
      backoff: exponential
      retryable_errors:
        - network_timeout
        - rate_limit
        - inference_unavailable
        - resource_busy
    loop_detection:
      repetition_threshold: 3

  - task_type: research
    token_budget:
      max_tokens: 64000
    execution_timeout:
      max_seconds: 1200
    retry_policy:
      max_retries: 3
      backoff: exponential
      retryable_errors:
        - network_timeout
        - rate_limit
        - inference_unavailable
    loop_detection:
      repetition_threshold: 3
```

## Related Documents

- [Task Types](task-types.md) — defines the five task categories that operational limits apply to
- [Event Taxonomy](../events/taxonomy.md) — defines the event categories and types emitted on termination
- [Event Schemas](../events/schemas.md) — specifies the payload structure for termination events
- [Workspace Isolation](workspace-isolation.md) — defines workspace retention policies for partial results

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial operational limits with token budgets, timeouts, retry policies, and loop detection |
