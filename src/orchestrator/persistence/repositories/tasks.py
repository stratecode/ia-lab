"""Task repository — CRUD operations with cursor-based pagination and filtering."""

from __future__ import annotations

import base64
import json
import uuid
from datetime import datetime

from sqlalchemy import select, and_, func
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import Task
from orchestrator.state_machine.transitions import (
    AgentType,
    Priority,
    TaskKind,
    TaskState,
)

# Pagination defaults
_DEFAULT_LIMIT = 20
_MAX_LIMIT = 100


def _encode_cursor(created_at: datetime, task_id: uuid.UUID) -> str:
    """Encode a (created_at, id) pair into an opaque cursor string."""
    payload = {
        "created_at": created_at.isoformat(),
        "id": str(task_id),
    }
    return base64.urlsafe_b64encode(json.dumps(payload).encode()).decode()


def _decode_cursor(cursor: str) -> tuple[datetime, uuid.UUID]:
    """Decode an opaque cursor string into (created_at, id)."""
    payload = json.loads(base64.urlsafe_b64decode(cursor.encode()).decode())
    return (
        datetime.fromisoformat(payload["created_at"]),
        uuid.UUID(payload["id"]),
    )


class TaskRepository:
    """Data access layer for Task entities."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        description: str,
        metadata: dict | None = None,
        priority: Priority = Priority.NORMAL,
        assigned_agent: AgentType | None = None,
        idempotency_key: str | None = None,
        correlation_id: uuid.UUID | None = None,
        parent_task_id: uuid.UUID | None = None,
        root_task_id: uuid.UUID | None = None,
        task_kind: TaskKind = TaskKind.ROOT,
        workspace_path: str | None = None,
    ) -> Task:
        """Create a new task and add it to the session.

        Args:
            description: Task description text.
            metadata: Optional JSON metadata.
            priority: Task priority level.
            assigned_agent: Optional pre-assigned agent type.
            idempotency_key: Optional client-provided deduplication key.
            correlation_id: Optional correlation ID for tracing.

        Returns:
            The newly created Task instance (not yet committed).
        """
        task = Task(
            description=description,
            metadata_=metadata or {},
            priority=priority,
            assigned_agent=assigned_agent,
            idempotency_key=idempotency_key,
            correlation_id=correlation_id or uuid.uuid4(),
            parent_task_id=parent_task_id,
            root_task_id=root_task_id,
            task_kind=task_kind,
            workspace_path=workspace_path,
        )
        self._session.add(task)
        await self._session.flush()
        return task

    async def get_by_id(self, task_id: uuid.UUID) -> Task | None:
        """Retrieve a task by its primary key.

        Args:
            task_id: The UUID of the task.

        Returns:
            The Task instance or None if not found.
        """
        return await self._session.get(Task, task_id)

    async def list_tasks(
        self,
        state: TaskState | None = None,
        agent_type: AgentType | None = None,
        priority: Priority | None = None,
        parent_task_id: uuid.UUID | None = None,
        root_task_id: uuid.UUID | None = None,
        date_from: datetime | None = None,
        date_to: datetime | None = None,
        cursor: str | None = None,
        limit: int = _DEFAULT_LIMIT,
    ) -> tuple[list[Task], str | None]:
        """List tasks with optional filters and cursor-based pagination.

        Cursor is based on (created_at, id) for stable ordering.

        Args:
            state: Filter by task state.
            agent_type: Filter by assigned agent type.
            priority: Filter by priority level.
            date_from: Filter tasks created on or after this datetime.
            date_to: Filter tasks created on or before this datetime.
            cursor: Opaque pagination cursor from a previous response.
            limit: Maximum number of results (default 20, max 100).

        Returns:
            Tuple of (list of tasks, next_cursor or None).
        """
        limit = max(1, min(limit, _MAX_LIMIT))

        stmt = select(Task)

        # Apply filters
        conditions = []
        if state is not None:
            conditions.append(Task.state == state)
        if agent_type is not None:
            conditions.append(Task.assigned_agent == agent_type)
        if priority is not None:
            conditions.append(Task.priority == priority)
        if parent_task_id is not None:
            conditions.append(Task.parent_task_id == parent_task_id)
        if root_task_id is not None:
            conditions.append(Task.root_task_id == root_task_id)
        if date_from is not None:
            conditions.append(Task.created_at >= date_from)
        if date_to is not None:
            conditions.append(Task.created_at <= date_to)

        # Apply cursor (keyset pagination: created_at DESC, id DESC)
        if cursor is not None:
            cursor_created_at, cursor_id = _decode_cursor(cursor)
            conditions.append(
                (Task.created_at < cursor_created_at)
                | (
                    (Task.created_at == cursor_created_at)
                    & (Task.id < cursor_id)
                )
            )

        if conditions:
            stmt = stmt.where(and_(*conditions))

        # Order by created_at DESC, id DESC for stable pagination
        stmt = stmt.order_by(Task.created_at.desc(), Task.id.desc())
        stmt = stmt.limit(limit + 1)  # Fetch one extra to detect next page

        result = await self._session.execute(stmt)
        tasks = list(result.scalars().all())

        # Determine next cursor
        next_cursor: str | None = None
        if len(tasks) > limit:
            tasks = tasks[:limit]
            last_task = tasks[-1]
            next_cursor = _encode_cursor(last_task.created_at, last_task.id)

        return tasks, next_cursor

    async def list_children(self, parent_task_id: uuid.UUID) -> list[Task]:
        """List direct child tasks for a parent task."""
        stmt = (
            select(Task)
            .where(Task.parent_task_id == parent_task_id)
            .order_by(Task.created_at.asc(), Task.id.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def list_by_root(self, root_task_id: uuid.UUID) -> list[Task]:
        """List all tasks belonging to a task tree rooted at root_task_id."""
        stmt = (
            select(Task)
            .where(Task.root_task_id == root_task_id)
            .order_by(Task.created_at.asc(), Task.id.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def update_state(
        self,
        task_id: uuid.UUID,
        new_state: TaskState,
        actor: str,
        reason: str | None = None,
    ) -> Task | None:
        """Update a task's state field.

        This only updates the state column — it does NOT perform full
        state machine validation or create transition records. That logic
        belongs in the state machine engine.

        Args:
            task_id: The UUID of the task to update.
            new_state: The new state value.
            actor: Identity of who triggered the change.
            reason: Optional reason for the state change.

        Returns:
            The updated Task instance, or None if not found.
        """
        task = await self._session.get(Task, task_id)
        if task is None:
            return None
        task.state = new_state
        await self._session.flush()
        return task

    async def get_by_idempotency_key(self, key: str) -> Task | None:
        """Look up a task by its idempotency key.

        Args:
            key: The idempotency key string.

        Returns:
            The Task instance or None if not found.
        """
        stmt = select(Task).where(Task.idempotency_key == key)
        result = await self._session.execute(stmt)
        return result.scalar_one_or_none()
