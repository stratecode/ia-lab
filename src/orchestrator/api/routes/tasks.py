"""Task CRUD route handlers.

Implements:
- POST /tasks — Create a new task (with idempotency support)
- GET /tasks/{task_id} — Get a single task by ID
- GET /tasks — List tasks with filters and cursor-based pagination
- PATCH /tasks/{task_id} — Update task state (with state machine validation)
- POST /tasks/{task_id}/cancel — Cancel a task

Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7
"""

from __future__ import annotations

import uuid
from datetime import UTC, datetime, timedelta

import structlog
from fastapi import APIRouter, Depends, Header, HTTPException, Query
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.api.schemas.common import ErrorResponse
from orchestrator.api.schemas.tasks import (
    TaskCreate,
    TaskListResponse,
    TaskResponse,
    TaskStateConflictResponse,
    TaskUpdate,
)
from orchestrator.persistence.models import Task
from orchestrator.persistence.repositories.tasks import TaskRepository
from orchestrator.state_machine.engine import (
    InvalidTransitionError,
    StateMachineEngine,
)
from orchestrator.state_machine.transitions import (
    AgentType,
    Priority,
    TaskState,
    TERMINAL_STATES,
    get_valid_transitions,
    is_valid_transition,
)

logger = structlog.get_logger(__name__)

router = APIRouter(prefix="/tasks", tags=["tasks"])

# Idempotency dedup window (24 hours)
_IDEMPOTENCY_WINDOW = timedelta(hours=24)

# Cancellable states per requirement 2.6
_CANCELLABLE_STATES = frozenset(
    {
        TaskState.CREATED,
        TaskState.QUEUED,
        TaskState.ASSIGNED,
        TaskState.IN_PROGRESS,
        TaskState.WAITING_APPROVAL,
    }
)


def _task_to_response(task: Task) -> TaskResponse:
    """Convert a Task ORM model to a TaskResponse schema."""
    return TaskResponse(
        id=task.id,
        state=TaskState(task.state),
        description=task.description,
        metadata=task.metadata_ or {},
        assigned_agent=AgentType(task.assigned_agent) if task.assigned_agent else None,
        priority=Priority(task.priority),
        workspace_path=task.workspace_path,
        retry_count=task.retry_count,
        correlation_id=task.correlation_id,
        results=task.results,
        error_message=task.error_message,
        created_at=task.created_at,
        updated_at=task.updated_at,
        started_at=task.started_at,
        completed_at=task.completed_at,
    )


# ---------------------------------------------------------------------------
# Dependency injection helpers
# ---------------------------------------------------------------------------

# These will be overridden at app startup via dependency_overrides or
# by providing actual factories. For now, we define the dependency
# signatures that the app factory will wire up.


async def _get_session_factory() -> async_sessionmaker[AsyncSession]:
    """Placeholder dependency — overridden at app startup."""
    raise NotImplementedError("Session factory not configured")


async def _get_state_engine() -> StateMachineEngine:
    """Placeholder dependency — overridden at app startup."""
    raise NotImplementedError("State machine engine not configured")


# ---------------------------------------------------------------------------
# Route handlers
# ---------------------------------------------------------------------------


@router.post(
    "",
    response_model=TaskResponse,
    status_code=201,
    responses={
        409: {"model": ErrorResponse, "description": "Idempotency conflict"},
    },
)
async def create_task(
    body: TaskCreate,
    idempotency_key: str | None = Header(
        None,
        alias="Idempotency-Key",
        max_length=128,
        description="Optional idempotency key for deduplication (24h window)",
    ),
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> TaskResponse:
    """Create a new task.

    If an Idempotency-Key header is provided, repeated requests within 24 hours
    return the original task without creating duplicates.
    """
    async with session_factory() as session:
        async with session.begin():
            repo = TaskRepository(session)

            # Idempotency check: if key provided, look for existing task
            if idempotency_key:
                existing = await repo.get_by_idempotency_key(idempotency_key)
                if existing is not None:
                    # Check if within the 24h dedup window
                    created_at = existing.created_at
                    if isinstance(created_at, str):
                        created_at = datetime.fromisoformat(created_at)
                    now = datetime.now(UTC)
                    if now - created_at < _IDEMPOTENCY_WINDOW:
                        logger.info(
                            "Idempotent task creation — returning existing task",
                            task_id=str(existing.id),
                            idempotency_key=idempotency_key,
                        )
                        return _task_to_response(existing)

            task = await repo.create(
                description=body.description,
                metadata=body.metadata,
                priority=body.priority,
                assigned_agent=body.assigned_agent,
                idempotency_key=idempotency_key,
            )

        logger.info(
            "Task created",
            task_id=str(task.id),
            priority=body.priority.value,
            assigned_agent=body.assigned_agent.value if body.assigned_agent else None,
        )
        return _task_to_response(task)


@router.get(
    "/{task_id}",
    response_model=TaskResponse,
    responses={404: {"model": ErrorResponse}},
)
async def get_task(
    task_id: uuid.UUID,
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> TaskResponse:
    """Get a single task by ID."""
    async with session_factory() as session:
        repo = TaskRepository(session)
        task = await repo.get_by_id(task_id)

    if task is None:
        raise HTTPException(
            status_code=404,
            detail=f"Task {task_id} not found",
        )

    return _task_to_response(task)


@router.get(
    "",
    response_model=TaskListResponse,
)
async def list_tasks(
    state: TaskState | None = Query(None, description="Filter by task state"),
    agent: AgentType | None = Query(None, description="Filter by assigned agent"),
    priority: Priority | None = Query(None, description="Filter by priority"),
    date_from: datetime | None = Query(None, description="Filter tasks created on or after"),
    date_to: datetime | None = Query(None, description="Filter tasks created on or before"),
    cursor: str | None = Query(None, description="Pagination cursor from previous response"),
    limit: int = Query(20, ge=1, le=100, description="Page size (default 20, max 100)"),
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> TaskListResponse:
    """List tasks with optional filters and cursor-based pagination.

    Returns tasks ordered by creation time (newest first) with a cursor
    for fetching the next page.
    """
    async with session_factory() as session:
        repo = TaskRepository(session)
        tasks, next_cursor = await repo.list_tasks(
            state=state,
            agent_type=agent,
            priority=priority,
            date_from=date_from,
            date_to=date_to,
            cursor=cursor,
            limit=limit,
        )

    return TaskListResponse(
        items=[_task_to_response(t) for t in tasks],
        cursor=next_cursor,
        total=len(tasks),
        page_size=limit,
    )


@router.patch(
    "/{task_id}",
    response_model=TaskResponse,
    responses={
        404: {"model": ErrorResponse},
        409: {"model": TaskStateConflictResponse},
    },
)
async def update_task(
    task_id: uuid.UUID,
    body: TaskUpdate,
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
    state_engine: StateMachineEngine = Depends(_get_state_engine),
) -> TaskResponse:
    """Update a task's state.

    Validates the state transition against the state machine. Returns 409
    if the transition is invalid, including the current state and valid
    transitions from that state.
    """
    try:
        await state_engine.transition(
            task_id=str(task_id),
            target_state=body.state,
            actor="api",
            reason=body.reason,
        )
    except ValueError:
        raise HTTPException(
            status_code=404,
            detail=f"Task {task_id} not found",
        )
    except InvalidTransitionError as exc:
        raise HTTPException(
            status_code=409,
            detail={
                "error": "invalid_state_transition",
                "current_state": exc.current_state.value,
                "requested_state": exc.target_state.value,
                "valid_transitions": [s.value for s in exc.valid_transitions],
                "task_id": str(exc.task_id),
            },
        )

    # Re-fetch the task to return the updated state
    async with session_factory() as session:
        repo = TaskRepository(session)
        task = await repo.get_by_id(task_id)

    if task is None:
        raise HTTPException(status_code=404, detail=f"Task {task_id} not found")

    return _task_to_response(task)


@router.post(
    "/{task_id}/cancel",
    response_model=TaskResponse,
    responses={
        404: {"model": ErrorResponse},
        409: {"model": TaskStateConflictResponse},
    },
)
async def cancel_task(
    task_id: uuid.UUID,
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
    state_engine: StateMachineEngine = Depends(_get_state_engine),
) -> TaskResponse:
    """Cancel a task.

    A task can only be cancelled from cancellable states:
    created, queued, assigned, in_progress, waiting_approval.

    Returns 409 if the task is in a non-cancellable state.
    """
    # First check the current state to provide a better error message
    async with session_factory() as session:
        repo = TaskRepository(session)
        task = await repo.get_by_id(task_id)

    if task is None:
        raise HTTPException(
            status_code=404,
            detail=f"Task {task_id} not found",
        )

    current_state = TaskState(task.state)
    if current_state not in _CANCELLABLE_STATES:
        raise HTTPException(
            status_code=409,
            detail={
                "error": "invalid_state_transition",
                "current_state": current_state.value,
                "requested_state": TaskState.CANCELLED.value,
                "valid_transitions": [s.value for s in get_valid_transitions(current_state)],
                "task_id": str(task_id),
            },
        )

    try:
        await state_engine.transition(
            task_id=str(task_id),
            target_state=TaskState.CANCELLED,
            actor="api",
            reason="Cancelled via API",
        )
    except InvalidTransitionError as exc:
        raise HTTPException(
            status_code=409,
            detail={
                "error": "invalid_state_transition",
                "current_state": exc.current_state.value,
                "requested_state": exc.target_state.value,
                "valid_transitions": [s.value for s in exc.valid_transitions],
                "task_id": str(exc.task_id),
            },
        )

    # Re-fetch to return updated state
    async with session_factory() as session:
        repo = TaskRepository(session)
        task = await repo.get_by_id(task_id)

    if task is None:
        raise HTTPException(status_code=404, detail=f"Task {task_id} not found")

    logger.info("Task cancelled", task_id=str(task_id))
    return _task_to_response(task)
