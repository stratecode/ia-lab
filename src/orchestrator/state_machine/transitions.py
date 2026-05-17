"""Task state machine definitions: enums, valid transitions, and helper functions."""

from enum import StrEnum


class TaskState(StrEnum):
    """Lifecycle states for a task in the orchestrator."""

    CREATED = "created"
    QUEUED = "queued"
    ASSIGNED = "assigned"
    IN_PROGRESS = "in_progress"
    WAITING_APPROVAL = "waiting_approval"
    REVIEW = "review"
    RETRYING = "retrying"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELLED = "cancelled"


class AgentType(StrEnum):
    """Specialized agent types that can be assigned to tasks."""

    PLANNER = "planner"
    CODER = "coder"
    REVIEWER = "reviewer"
    INFRA = "infra"
    RESEARCHER = "researcher"


class Priority(StrEnum):
    """Task priority levels (critical > high > normal > low)."""

    CRITICAL = "critical"
    HIGH = "high"
    NORMAL = "normal"
    LOW = "low"


class TaskKind(StrEnum):
    """High-level role of a task within a task tree."""

    ROOT = "root"
    PLAN_STEP = "plan_step"


class ApprovalStatus(StrEnum):
    """Status of an approval request."""

    PENDING = "pending"
    APPROVED = "approved"
    REJECTED = "rejected"
    TIMEOUT = "timeout"


class ApiKeyScope(StrEnum):
    """Scopes for API key authorization."""

    ADMIN = "admin"
    OPERATOR = "operator"
    BOT = "bot"
    READONLY = "readonly"


# Terminal states — tasks in these states cannot transition further.
TERMINAL_STATES: frozenset[TaskState] = frozenset(
    {TaskState.COMPLETED, TaskState.FAILED, TaskState.CANCELLED}
)

# Valid state transitions as defined by the task state machine (Requirement 3.2).
VALID_TRANSITIONS: frozenset[tuple[TaskState, TaskState]] = frozenset(
    {
        (TaskState.CREATED, TaskState.QUEUED),
        (TaskState.CREATED, TaskState.WAITING_APPROVAL),
        (TaskState.CREATED, TaskState.CANCELLED),
        (TaskState.QUEUED, TaskState.ASSIGNED),
        (TaskState.QUEUED, TaskState.CANCELLED),
        (TaskState.ASSIGNED, TaskState.IN_PROGRESS),
        (TaskState.ASSIGNED, TaskState.FAILED),
        (TaskState.ASSIGNED, TaskState.CANCELLED),
        (TaskState.IN_PROGRESS, TaskState.WAITING_APPROVAL),
        (TaskState.IN_PROGRESS, TaskState.REVIEW),
        (TaskState.IN_PROGRESS, TaskState.COMPLETED),
        (TaskState.IN_PROGRESS, TaskState.FAILED),
        (TaskState.IN_PROGRESS, TaskState.CANCELLED),
        (TaskState.IN_PROGRESS, TaskState.RETRYING),
        (TaskState.WAITING_APPROVAL, TaskState.IN_PROGRESS),
        (TaskState.WAITING_APPROVAL, TaskState.QUEUED),
        (TaskState.WAITING_APPROVAL, TaskState.CANCELLED),
        (TaskState.WAITING_APPROVAL, TaskState.FAILED),
        (TaskState.RETRYING, TaskState.IN_PROGRESS),
        (TaskState.RETRYING, TaskState.FAILED),
        (TaskState.RETRYING, TaskState.CANCELLED),
        (TaskState.REVIEW, TaskState.COMPLETED),
        (TaskState.REVIEW, TaskState.IN_PROGRESS),
        (TaskState.REVIEW, TaskState.FAILED),
    }
)


def get_valid_transitions(current_state: TaskState) -> list[TaskState]:
    """Return the list of valid target states reachable from *current_state*."""
    return [target for source, target in VALID_TRANSITIONS if source == current_state]


def is_valid_transition(current: TaskState, target: TaskState) -> bool:
    """Return True if transitioning from *current* to *target* is allowed."""
    return (current, target) in VALID_TRANSITIONS
