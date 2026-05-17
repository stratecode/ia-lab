"""Redis recovery and epoch counter for data loss detection.

Implements reconciliation logic to recover from Redis restarts with data loss:
- Epoch counter stored in both Redis (STRING at `orchestrator:epoch`) and PostgreSQL
- On startup, compares Redis epoch with PostgreSQL epoch to detect data loss
- If data loss detected: re-enqueue "queued" tasks, flag "assigned"/"in_progress"
  for manual review via `system.redis_recovery` event

Requirements: 6.9, 6.10, 6.11, 6.12
"""

from __future__ import annotations

import logging
import uuid
from datetime import datetime, timezone
from typing import Any

from redis.asyncio import Redis
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import Task
from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
)
from orchestrator.queue.redis_queue import RedisQueueService
from orchestrator.state_machine.transitions import (
    AgentType,
    Priority,
    TaskState,
    TERMINAL_STATES,
)

logger = logging.getLogger(__name__)

# Redis key for the epoch counter
EPOCH_KEY = "orchestrator:epoch"

# Non-terminal states that require reconciliation
_RECONCILABLE_STATES = frozenset(
    {TaskState.QUEUED, TaskState.ASSIGNED, TaskState.IN_PROGRESS}
)


class RedisRecoveryService:
    """Handles Redis data loss detection and task reconciliation.

    Uses a monotonic epoch counter stored in both Redis and PostgreSQL.
    On startup, compares the two values:
    - If Redis epoch matches or is higher: normal startup, no data loss
    - If Redis epoch is missing or lower: data loss detected, trigger reconciliation

    Reconciliation:
    - Re-enqueue tasks in "queued" state back to Redis queues
    - Flag "assigned" and "in_progress" tasks for manual review
    - Emit `system.redis_recovery` event
    - Increment epoch counter on successful reconciliation
    """

    def __init__(
        self,
        redis: Redis,
        queue_service: RedisQueueService,
        event_bus: RedisEventBus,
    ) -> None:
        """Initialize the recovery service.

        Args:
            redis: Async Redis client instance.
            queue_service: The Redis queue service for re-enqueuing tasks.
            event_bus: The event bus for emitting recovery events.
        """
        self._redis = redis
        self._queue_service = queue_service
        self._event_bus = event_bus

    async def get_redis_epoch(self) -> int | None:
        """Read the current epoch counter from Redis.

        Returns:
            The epoch value as an integer, or None if the key does not exist.
        """
        value = await self._redis.get(EPOCH_KEY)
        if value is None:
            return None
        if isinstance(value, bytes):
            value = value.decode("utf-8")
        return int(value)

    async def set_redis_epoch(self, epoch: int) -> None:
        """Set the epoch counter in Redis.

        Args:
            epoch: The epoch value to store.
        """
        await self._redis.set(EPOCH_KEY, str(epoch))

    async def increment_redis_epoch(self) -> int:
        """Atomically increment the Redis epoch counter.

        Returns:
            The new epoch value after increment.
        """
        new_value = await self._redis.incr(EPOCH_KEY)
        return int(new_value)

    async def get_pg_epoch(self, session: AsyncSession) -> int:
        """Read the current epoch counter from PostgreSQL.

        The epoch is stored in a simple key-value table or as a scalar.
        For simplicity, we use a dedicated query against a metadata pattern
        stored in the audit_log with a well-known resource_type.

        If no epoch record exists, returns 0 (initial state).

        Args:
            session: An async database session.

        Returns:
            The epoch value from PostgreSQL, or 0 if not yet initialized.
        """
        from orchestrator.persistence.models import AuditLog

        stmt = (
            select(AuditLog.details)
            .where(AuditLog.resource_type == "system_epoch")
            .where(AuditLog.action == "epoch_update")
            .order_by(AuditLog.timestamp.desc())
            .limit(1)
        )
        result = await session.execute(stmt)
        row = result.scalar_one_or_none()
        if row is None:
            return 0
        return int(row.get("epoch", 0))

    async def save_pg_epoch(self, session: AsyncSession, epoch: int) -> None:
        """Persist the epoch counter to PostgreSQL via an audit log entry.

        Args:
            session: An async database session.
            epoch: The epoch value to persist.
        """
        from orchestrator.persistence.models import AuditLog

        record = AuditLog(
            actor="system:recovery",
            action="epoch_update",
            resource_type="system_epoch",
            resource_id=None,
            details={"epoch": epoch},
            correlation_id=uuid.uuid4(),
        )
        session.add(record)
        await session.flush()

    async def detect_data_loss(self, session: AsyncSession) -> bool:
        """Detect whether Redis has lost data since last known state.

        Compares the Redis epoch counter with the PostgreSQL epoch:
        - If Redis epoch is None (key missing): data loss detected
        - If Redis epoch < PostgreSQL epoch: data loss detected
        - If Redis epoch >= PostgreSQL epoch: no data loss

        Args:
            session: An async database session.

        Returns:
            True if data loss is detected, False otherwise.
        """
        redis_epoch = await self.get_redis_epoch()
        pg_epoch = await self.get_pg_epoch(session)

        if redis_epoch is None:
            logger.warning(
                "Redis epoch key missing — data loss detected",
                extra={"pg_epoch": pg_epoch},
            )
            return True

        if redis_epoch < pg_epoch:
            logger.warning(
                "Redis epoch behind PostgreSQL — data loss detected",
                extra={"redis_epoch": redis_epoch, "pg_epoch": pg_epoch},
            )
            return True

        logger.info(
            "Redis epoch consistent — no data loss",
            extra={"redis_epoch": redis_epoch, "pg_epoch": pg_epoch},
        )
        return False

    async def get_tasks_for_reconciliation(
        self, session: AsyncSession
    ) -> list[Task]:
        """Query PostgreSQL for tasks in non-terminal, reconcilable states.

        Returns tasks in queued, assigned, or in_progress states that
        need reconciliation after Redis data loss.

        Args:
            session: An async database session.

        Returns:
            List of Task instances requiring reconciliation.
        """
        stmt = select(Task).where(
            Task.state.in_([s.value for s in _RECONCILABLE_STATES])
        )
        result = await session.execute(stmt)
        return list(result.scalars().all())

    async def reconcile(self, session: AsyncSession) -> ReconciliationResult:
        """Perform full reconciliation after Redis data loss detection.

        Steps:
        1. Query PostgreSQL for tasks in non-terminal states
        2. Re-enqueue tasks in "queued" state back to Redis queues
        3. Flag "assigned" and "in_progress" tasks for manual review
        4. Emit `system.redis_recovery` event
        5. Increment epoch counter in both Redis and PostgreSQL

        Args:
            session: An async database session.

        Returns:
            A ReconciliationResult with counts of actions taken.
        """
        tasks = await self.get_tasks_for_reconciliation(session)

        re_enqueued: list[str] = []
        flagged_for_review: list[str] = []

        for task in tasks:
            task_id = str(task.id)
            state = TaskState(task.state)

            if state == TaskState.QUEUED:
                # Re-enqueue to Redis queue
                agent_type = (
                    AgentType(task.assigned_agent)
                    if task.assigned_agent
                    else AgentType.PLANNER
                )
                priority = Priority(task.priority)
                await self._queue_service.enqueue(task_id, agent_type, priority)
                re_enqueued.append(task_id)
                logger.info(
                    "Task re-enqueued during reconciliation",
                    extra={
                        "task_id": task_id,
                        "agent_type": agent_type.value,
                        "priority": priority.value,
                    },
                )

            elif state in (TaskState.ASSIGNED, TaskState.IN_PROGRESS):
                # Flag for manual review
                flagged_for_review.append(task_id)
                logger.warning(
                    "Task flagged for manual review during reconciliation",
                    extra={"task_id": task_id, "state": state.value},
                )

        # Emit system.redis_recovery event
        await self._emit_recovery_event(re_enqueued, flagged_for_review)

        # Increment epoch in Redis and persist to PostgreSQL
        new_epoch = await self.increment_redis_epoch()
        await self.save_pg_epoch(session, new_epoch)

        logger.info(
            "Reconciliation complete",
            extra={
                "re_enqueued_count": len(re_enqueued),
                "flagged_for_review_count": len(flagged_for_review),
                "new_epoch": new_epoch,
            },
        )

        return ReconciliationResult(
            data_loss_detected=True,
            re_enqueued=re_enqueued,
            flagged_for_review=flagged_for_review,
            new_epoch=new_epoch,
        )

    async def check_and_reconcile(
        self, session: AsyncSession
    ) -> ReconciliationResult:
        """Check for data loss and reconcile if needed.

        This is the main entry point to call on startup. It:
        1. Detects whether Redis data loss occurred
        2. If yes, performs reconciliation
        3. If no, returns a no-op result

        Args:
            session: An async database session.

        Returns:
            A ReconciliationResult indicating what happened.
        """
        data_loss = await self.detect_data_loss(session)

        if not data_loss:
            return ReconciliationResult(
                data_loss_detected=False,
                re_enqueued=[],
                flagged_for_review=[],
                new_epoch=await self.get_redis_epoch() or 0,
            )

        return await self.reconcile(session)

    async def initialize_epoch(self, session: AsyncSession) -> int:
        """Initialize the epoch counter if it doesn't exist in either store.

        Should be called on first-ever startup to establish the baseline.

        Args:
            session: An async database session.

        Returns:
            The initialized epoch value (1).
        """
        redis_epoch = await self.get_redis_epoch()
        pg_epoch = await self.get_pg_epoch(session)

        if redis_epoch is None and pg_epoch == 0:
            # First-ever startup: initialize both to 1
            await self.set_redis_epoch(1)
            await self.save_pg_epoch(session, 1)
            logger.info("Epoch counter initialized", extra={"epoch": 1})
            return 1

        # Sync Redis to PostgreSQL epoch if Redis is behind or missing
        if redis_epoch is None or redis_epoch < pg_epoch:
            await self.set_redis_epoch(pg_epoch)
            return pg_epoch

        return redis_epoch

    async def _emit_recovery_event(
        self,
        re_enqueued: list[str],
        flagged_for_review: list[str],
    ) -> None:
        """Emit a system.redis_recovery event to the event bus.

        Args:
            re_enqueued: List of task IDs that were re-enqueued.
            flagged_for_review: List of task IDs flagged for manual review.
        """
        envelope = EventEnvelope(
            event_type="system.redis_recovery",
            source_component="queue.recovery",
            timestamp=datetime.now(timezone.utc).isoformat(),
            correlation_id=str(uuid.uuid4()),
            payload={
                "re_enqueued_task_ids": re_enqueued,
                "flagged_for_review_task_ids": flagged_for_review,
                "re_enqueued_count": len(re_enqueued),
                "flagged_for_review_count": len(flagged_for_review),
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.SYSTEM_HEALTH,
        )
        await self._event_bus.publish_event(envelope)


class ReconciliationResult:
    """Result of a reconciliation check/operation.

    Attributes:
        data_loss_detected: Whether Redis data loss was detected.
        re_enqueued: List of task IDs that were re-enqueued.
        flagged_for_review: List of task IDs flagged for manual review.
        new_epoch: The epoch counter value after reconciliation.
    """

    def __init__(
        self,
        data_loss_detected: bool,
        re_enqueued: list[str],
        flagged_for_review: list[str],
        new_epoch: int,
    ) -> None:
        self.data_loss_detected = data_loss_detected
        self.re_enqueued = re_enqueued
        self.flagged_for_review = flagged_for_review
        self.new_epoch = new_epoch

    def __repr__(self) -> str:
        return (
            f"ReconciliationResult("
            f"data_loss_detected={self.data_loss_detected}, "
            f"re_enqueued={len(self.re_enqueued)}, "
            f"flagged_for_review={len(self.flagged_for_review)}, "
            f"new_epoch={self.new_epoch})"
        )
