# Event Schemas

Defines the canonical Event_Schema structure with mandatory fields, constraints, and validation rules for all system events emitted by the Local AI Agents Platform.

## Event_Schema Definition

Every event in the platform conforms to the following schema. All fields are mandatory unless explicitly marked optional.

```yaml
event:
  event_type: string        # Dot-notation event identifier
  source_component: string  # Component that emitted the event
  timestamp: string         # When the event occurred
  correlation_id: string    # Request-level trace identifier
  payload: object           # Event-specific data
  schema_version: string    # Schema version for this event type
  severity: enum            # Event severity classification
  category: enum            # Event category classification
```

## Field Specifications

| Field | Type | Constraints | Format | Example |
|-------|------|-------------|--------|---------|
| `event_type` | string | Required, max 128 characters | Dot-notation: `<category>.<action>` | `task_lifecycle.created` |
| `source_component` | string | Required, max 64 characters | Lowercase alphanumeric + hyphens | `orchestrator` |
| `timestamp` | string | Required | ISO 8601 with timezone (RFC 3339) | `2025-07-14T10:30:00.000Z` |
| `correlation_id` | string | Required | UUID v4 (36 characters) | `550e8400-e29b-41d4-a716-446655440000` |
| `payload` | object | Required, max 64 KB serialized (JSON) | JSON object | `{"task_id": "t-001", ...}` |
| `schema_version` | string | Required | Semantic versioning MAJOR.MINOR.PATCH | `1.0.0` |
| `severity` | enum | Required | One of: `INFO`, `WARNING`, `ERROR`, `CRITICAL` | `INFO` |
| `category` | enum | Required | One of: `task_lifecycle`, `agent_action`, `approval`, `system_health`, `security` | `task_lifecycle` |

## Field Constraint Details

### event_type

- Maximum length: 128 characters
- Pattern: `^[a-z_]+\.[a-z_]+$` (category prefix + dot + action)
- Must correspond to a registered event type in the [Event Taxonomy](taxonomy.md)

### source_component

- Maximum length: 64 characters
- Pattern: `^[a-z][a-z0-9\-]*$` (lowercase, starts with letter, alphanumeric + hyphens)
- Must be a known platform component (e.g., `orchestrator`, `planner-agent`, `coder-agent`)

### timestamp

- Format: ISO 8601 with mandatory timezone offset or `Z` suffix
- Precision: milliseconds minimum
- Pattern: `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}(Z|[+-]\d{2}:\d{2})$`
- Must represent UTC or include explicit offset

### correlation_id

- Format: UUID v4 (RFC 4122)
- Pattern: `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
- Used for consumer-side deduplication in combination with `event_type`
- All events within a single task execution share the same `correlation_id`

### payload

- Maximum serialized size: 64 KB (65,536 bytes) as JSON
- Must be a valid JSON object (not null, not array at root level)
- Content varies by event type (see examples below)
- Empty payloads represented as `{}`

### schema_version

- Format: Semantic versioning `MAJOR.MINOR.PATCH`
- Pattern: `^\d+\.\d+\.\d+$`
- MAJOR increments indicate breaking payload changes
- MINOR increments indicate additive payload fields
- PATCH increments indicate documentation or metadata corrections

### severity

- Allowed values: `INFO`, `WARNING`, `ERROR`, `CRITICAL`
- Must be uppercase
- Classification rules defined in [severity-levels.md](severity-levels.md)

### category

- Allowed values: `task_lifecycle`, `agent_action`, `approval`, `system_health`, `security`
- Determines routing and retention policies
- Must match the prefix of `event_type`

## Example Event Payloads

### Task Lifecycle — task_lifecycle.created

```json
{
  "event_type": "task_lifecycle.created",
  "source_component": "orchestrator",
  "timestamp": "2025-07-14T10:30:00.123Z",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "payload": {
    "task_id": "task-20250714-001",
    "task_type": "coding",
    "title": "Implement user authentication module",
    "priority": "high",
    "assigned_agent": "coder",
    "input_artifacts": ["requirements.md", "api-spec.yaml"]
  },
  "schema_version": "1.0.0",
  "severity": "INFO",
  "category": "task_lifecycle"
}
```

### Agent Action — agent_action.invoked

```json
{
  "event_type": "agent_action.invoked",
  "source_component": "coder-agent",
  "timestamp": "2025-07-14T10:30:05.456Z",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "payload": {
    "agent_id": "coder",
    "task_id": "task-20250714-001",
    "model_id": "qwen2.5-coder-7b-q4km",
    "context_tokens": 4096,
    "tools_available": ["filesystem", "shell", "git"]
  },
  "schema_version": "1.0.0",
  "severity": "INFO",
  "category": "agent_action"
}
```

### Approval Request — approval.requested

```json
{
  "event_type": "approval.requested",
  "source_component": "orchestrator",
  "timestamp": "2025-07-14T10:35:12.789Z",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "payload": {
    "approval_id": "apr-20250714-001",
    "task_id": "task-20250714-001",
    "requesting_agent": "infra",
    "action_type": "production_deployment",
    "action_description": "Deploy nginx configuration to production reverse proxy",
    "timeout_seconds": 300,
    "escalation_level": 1
  },
  "schema_version": "1.0.0",
  "severity": "WARNING",
  "category": "approval"
}
```

### System Health — system_health.heartbeat

```json
{
  "event_type": "system_health.heartbeat",
  "source_component": "llama-cpp-code",
  "timestamp": "2025-07-14T10:30:00.000Z",
  "correlation_id": "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
  "payload": {
    "component": "llama-cpp-code",
    "status": "healthy",
    "uptime_seconds": 86400,
    "vram_usage_mb": 5120,
    "active_requests": 2,
    "queue_depth": 0
  },
  "schema_version": "1.0.0",
  "severity": "INFO",
  "category": "system_health"
}
```

### Security Violation — security.permission_denied

```json
{
  "event_type": "security.permission_denied",
  "source_component": "permission-enforcer",
  "timestamp": "2025-07-14T10:31:45.321Z",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "payload": {
    "agent_id": "researcher",
    "task_id": "task-20250714-002",
    "resource_type": "filesystem",
    "resource_path": "/etc/shadow",
    "access_requested": "read",
    "policy_rule": "deny-by-default",
    "violation_count": 1
  },
  "schema_version": "1.0.0",
  "severity": "ERROR",
  "category": "security"
}
```

## Schema Validation Rules

Events are validated at emission time against the following rules:

1. All mandatory fields must be present and non-null
2. `event_type` must be registered in the [Event Taxonomy](taxonomy.md)
3. `source_component` must be a known platform component
4. `timestamp` must be valid ISO 8601 with timezone
5. `correlation_id` must be valid UUID v4
6. `payload` serialized size must not exceed 64 KB
7. `schema_version` must follow semantic versioning format
8. `severity` must be one of the four defined levels
9. `category` must match the `event_type` prefix

Events failing validation are logged as emission failures and follow the retry policy defined in the [Event Taxonomy](taxonomy.md) (3 retries, then log and continue without blocking).

## Schema Evolution

| Version Change | Compatibility | Action Required |
|---------------|---------------|-----------------|
| PATCH (x.y.Z) | Fully backward compatible | No consumer changes |
| MINOR (x.Y.0) | Backward compatible (additive) | Consumers may ignore new fields |
| MAJOR (X.0.0) | Breaking change | All consumers must update |

Schema version is tracked per `event_type`. Consumers must handle unknown fields gracefully (ignore, do not reject).

## Related Documents

- [Event Taxonomy](taxonomy.md) — defines event categories, types, and producer-consumer relationships
- [Event Severity Levels](severity-levels.md) — defines severity classification and retention policies

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial event schemas document with field definitions and examples |
