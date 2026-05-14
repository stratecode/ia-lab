# Event Severity Levels

Classifies all 19 platform event types into exactly one of four severity levels and defines escalation requirements, retention policies, and notification delivery rules for each classification.

## Severity Level Definitions

| Level | Description | Escalation Requirement | Retention | Notification Policy |
|-------|-------------|----------------------|-----------|---------------------|
| INFO | Normal operational events; no action required | None | 30 days | Log-only |
| WARNING | Conditions that may require attention if they persist | Re-notify via higher-priority channel if no acknowledgment within 120s | 90 days | Notify operator |
| ERROR | Failures requiring investigation; non-critical impact | Re-notify via higher-priority channel if no acknowledgment within 120s | 180 days | Notify operator |
| CRITICAL | Security violations and infrastructure failures requiring immediate response | Re-notify via higher-priority channel if no acknowledgment within 120s | 365 days | Page operator (≤30s SLA) |

## CRITICAL Classification Criteria

Events are classified as CRITICAL when they indicate:

- **Security violations**: unauthorized access attempts, authentication failures, permission boundary breaches, rate limiting enforcement (`security.auth_failed`, `security.permission_denied`, `security.rate_limited`)
- **Infrastructure failures**: service crashes, host unreachable conditions, disk I/O errors, container restart loops (`system.degraded`)

CRITICAL events MUST deliver operator notification within **30 seconds** of emission.

## Event Type Severity Matrix

### Task Lifecycle Events

| Event Type | Severity | Escalation | Retention | Notification Policy |
|-----------|----------|------------|-----------|---------------------|
| `task.created` | INFO | None | 30 days | Log-only |
| `task.assigned` | INFO | None | 30 days | Log-only |
| `task.started` | INFO | None | 30 days | Log-only |
| `task.completed` | INFO | None | 30 days | Log-only |
| `task.failed` | ERROR | Re-notify if no ack within 120s | 180 days | Notify operator |

### Agent Action Events

| Event Type | Severity | Escalation | Retention | Notification Policy |
|-----------|----------|------------|-----------|---------------------|
| `agent.invoked` | INFO | None | 30 days | Log-only |
| `agent.responded` | INFO | None | 30 days | Log-only |
| `agent.errored` | ERROR | Re-notify if no ack within 120s | 180 days | Notify operator |
| `agent.timed_out` | WARNING | Re-notify if no ack within 120s | 90 days | Notify operator |

### Approval Request Events

| Event Type | Severity | Escalation | Retention | Notification Policy |
|-----------|----------|------------|-----------|---------------------|
| `approval.requested` | WARNING | Re-notify if no ack within 120s | 90 days | Notify operator |
| `approval.approved` | INFO | None | 30 days | Log-only |
| `approval.rejected` | WARNING | Re-notify if no ack within 120s | 90 days | Notify operator |
| `approval.expired` | ERROR | Re-notify if no ack within 120s | 180 days | Notify operator |

### System Health Events

| Event Type | Severity | Escalation | Retention | Notification Policy |
|-----------|----------|------------|-----------|---------------------|
| `system.heartbeat` | INFO | None | 30 days | Log-only |
| `system.degraded` | CRITICAL | Re-notify if no ack within 120s | 365 days | Page operator (≤30s SLA) |
| `system.recovered` | INFO | None | 30 days | Log-only |

### Security Violation Events

| Event Type | Severity | Escalation | Retention | Notification Policy |
|-----------|----------|------------|-----------|---------------------|
| `security.auth_failed` | CRITICAL | Re-notify if no ack within 120s | 365 days | Page operator (≤30s SLA) |
| `security.permission_denied` | CRITICAL | Re-notify if no ack within 120s | 365 days | Page operator (≤30s SLA) |
| `security.rate_limited` | CRITICAL | Re-notify if no ack within 120s | 365 days | Page operator (≤30s SLA) |

## Notification Delivery SLA

| Severity | Maximum Delivery Time | Channel |
|----------|----------------------|---------|
| INFO | N/A (log-only) | Event log |
| WARNING | Best-effort | Operator notification (Telegram/Slack) |
| ERROR | Best-effort | Operator notification (Telegram/Slack) |
| CRITICAL | ≤30 seconds | Page operator (PagerDuty/Alertmanager) |

## Notification Delivery Failure Handling

When a notification delivery attempt fails, the system applies the following retry policy:

1. **Retry 1**: Immediate retry after 100ms delay
2. **Retry 2**: Retry after 500ms delay (exponential backoff)
3. **Retry 3**: Final retry after 2000ms delay

If all 3 retry attempts fail:

- A separate ERROR event is logged containing: original event_type, target notification channel, failure reason, and timestamp
- The system continues processing without blocking — notification failure must never halt event pipeline operations
- The failed notification is recorded for manual review during the next operational check

## Retention Policy Summary

| Severity | Minimum Retention | Storage Tier |
|----------|------------------|--------------|
| INFO | 30 days | Standard |
| WARNING | 90 days | Standard |
| ERROR | 180 days | Long-term |
| CRITICAL | 365 days | Long-term + Audit |

Retention durations are minimums. Events may be retained longer based on operational needs or compliance requirements. CRITICAL events are additionally stored in the audit log for compliance purposes.

## Related Documents

- [Event Taxonomy](taxonomy.md) — defines event categories, types, and producer-consumer relationships
- [Event Schemas](schemas.md) — defines the mandatory fields and constraints for all event payloads

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial severity levels with 19 event classifications, retention, and notification policies |
