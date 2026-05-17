"""Atomic state transition engine.

Implements the IStateMachine protocol with:
- SELECT FOR UPDATE row locking within a transaction
- Transition record creation (from_state, to_state, actor, reason, timestamp)
- Rollback on any failure — no partial state changes
- Post-commit event emission to Redis Stream
"""

from __future__ import annotations

import logging
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Protocol

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.persistence.models import StateTransition, Task
from orchestrator.state_machine.transitions import (
    TaskState,
    get_valid_transitions,
    is_valid_transition,
)

logger = logging.getLogger(__name__)


class InvalidTransitionError(Exception):
    """Raised when a requested state transition is not allowed."""

    def __init__(
        self,
        task_id: str,
        current_state: TaskState,
        target_state: TaskState,
    ) -> None:
        self.task_id = task_id
        self.current_state = current_state
        self.target_state = target_state
        self.valid_transitions = get_valid_transitions(current_state)
        super().__init__(
            f"Invalid transition for task {task_id}: "
            f"{current_state.value} → {target_state.value}. "
            f"Valid targets: {[s.value for s in self.valid_transitions]}"
        )


@dataclass(frozen=True)
class TransitionResult:
    """Result of a successful state transition."""

    task_id: str
    from_state: TaskState
    to_state: TaskState
    actor: str
    reason: str | None
    timestamp: datetime
    transition_id: str


class IEventPublisher(Protocol):
    """Interface for post-commit event publishing."""

    async def publish(self, stream: str, event: dict[str, Any]) -> None:
        """Publish an event to a Redis Stream."""
        ...


class NoOpEventPublisher:
    """Placeholder event publisher that logs events without requiring Redis."""

    async def publish(self, stream: str, event: dict[str, Any]) -> None:
        """Log the event instead of publishing to Redis."""
        logger.info(
            "Event published (no-op)",
            extra={"stream": stream, "event_type": event.get("event_type")},
        )


class StateMachineEngine:
    """Atomic state transition engine.

    Accepts a session factory (dependency injection) and an optional event
    publisher for post-commit notifications.
    """

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        event_publisher: IEventPublisher | None = None,
    ) -> None:
        self._session_factory = session_factory
        self._event_publisher = event_publisher or NoOpEventPublisher()

    def get_valid_transitions(self, current_state: TaskState) -> list[TaskState]:
        """Return valid target states from current state."""
        return get_valid_transitions(current_state)

    async def transition(
        self,
        task_id: str,
        target_state: TaskState,
        actor: str,
        reason: str | None = None,
    ) -> TransitionResult:
        """Atomically transition a task's state.

        Steps:
        1. Open transaction
        2. SELECT FOR UPDATE the task row (row lock)
        3. Validate transition is legal
        4. Update task state
        5. Insert StateTransition record
        6. Commit transaction
        7. Publish event to Redis Stream (post-commit)

        If any step fails, the transaction rolls back and no side effects execute.

        Args:
            task_id: UUID of the task to transition.
            target_state: The desired new state.
            actor: Identity of who/what triggered the transition.
            reason: Optional human-readable reason for the transition.

        Returns:
            TransitionResult with details of the completed transition.

        Raises:
            InvalidTransitionError: If the transition is not allowed.
            ValueError: If the task does not exist.
        """
        transition_id = str(uuid.uuid4())
        now = datetime.now(timezone.utc)
        from_state: TaskState | None = None

        async with self._session_factory() as session:
            async with session.begin():
                # Step 1-2: SELECT FOR UPDATE — acquire row lock
                stmt = (
                    select(Task)
                    .where(Task.id == uuid.UUID(task_id))
                    .with_for_update()
                )
                result = await session.execute(stmt)
                task = result.scalar_one_or_none()

                if task is None:
                    raise ValueError(f"Task {task_id} not found")

                from_state = TaskState(task.state)

                # Step 3: Validate transition
                if not is_valid_transition(from_state, target_state):
                    raise InvalidTransitionError(task_id, from_state, target_state)

                # Step 4: Update task state
                task.state = target_state

                # Step 5: Insert StateTransition record
                transition_record = StateTransition(
                    id=uuid.UUID(transition_id),
                    task_id=uuid.UUID(task_id),
                    from_state=from_state,
                    to_state=target_state,
                    actor=actor,
                    reason=reason,
                    timestamp=now,
                )
                session.add(transition_record)

            # Transaction committed successfully at this point

        # Step 6: Post-commit event emission (outside transaction)
        await self._publish_transition_event(
            task_id=task_id,
            from_state=from_state,
            to_state=target_state,
            actor=actor,
            reason=reason,
            timestamp=now,
            transition_id=transition_id,
        )

        return TransitionResult(
            task_id=task_id,
            from_state=from_state,
            to_state=target_state,
            actor=actor,
            reason=reason,
            timestamp=now,
            transition_id=transition_id,
        )

    async def _publish_transition_event(
        self,
        task_id: str,
        from_state: TaskState,
        to_state: TaskState,
        actor: str,
        reason: str | None,
        timestamp: datetime,
        transition_id: str,
    ) -> None:
        """Publish a state transition event to the event bus.

        This runs after the transaction has committed. If publishing fails,
        the transition is still persisted — events are best-effort.
        """
        event = {
            "event_type": f"task_lifecycle.{to_state.value}",
            "source_component": "state_machine",
            "timestamp": timestamp.isoformat(),
            "correlation_id": task_id,
            "payload": {
                "task_id": task_id,
                "from_state": from_state.value,
                "to_state": to_state.value,
                "actor": actor,
                "reason": reason,
                "transition_id": transition_id,
            },
        }
        try:
            await self._event_publisher.publish("events:task_lifecycle", event)
        except Exception as exc:
            # Event publishing is best-effort; log but don't fail the operation
            logger.warning(
                "Failed to publish transition event",
                extra={
                    "task_id": task_id,
                    "transition_id": transition_id,
                    "error": str(exc),
                },
            )
