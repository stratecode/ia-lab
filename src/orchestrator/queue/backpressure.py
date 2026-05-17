"""Queue backpressure and dead letter handling.

Implements:
- Per-agent queue depth limits (default 50) and global limit (default 200)
- Dead letter queue for tasks exceeding processing timeout (default 3600s)
- Queue depth rejection with appropriate exception for HTTP 429
- Event emission for queue.backpressure and task.dead_lettered events

Requirements: 6.7, 6.8, 9.1, 9.2
"""

from __future__ import annotations

import json
import logging
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone

from redis.asyncio import Redis

from orchestrator.config import QueueSettings
from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
)
from orchestrator.queue.redis_queue import RedisQueueService, _queue_key
from orchestrator.state_machine.transitions import AgentType, Priority

logger = logging.getLogger(__name__)

# Redis key for the dead letter queue (LIST type).
DEAD_LETTER_KEY: str = "queue:dead_letter"

# Default Retry-After header value in seconds when backpressure is active.
DEFAULT_RETRY_AFTER_SECONDS: int = 30


@dataclass(frozen=True)
class DeadLetterEntry:
    """An entry in the dead letter queue.

    Attributes:
        task_id: The task that was dead-lettered.
        reason: Why the task was moved to the dead letter queue.
        timestamp: ISO-8601 UTC timestamp when the task was dead-lettered.
        agent_type: The agent type queue the task was originally in.
    """

    task_id: str
    reason: str
    timestamp: str
    agent_type: str

    def to_json(self) -> str:
        """Serialize to JSON string for Redis LIST storage."""
        return json.dumps(
            {
                "task_id": self.task_id,
                "reason": self.reason,
                "timestamp": self.timestamp,
                "agent_type": self.agent_type,
            }
        )

    @classmethod
    def from_json(cls, data: str | bytes) -> DeadLetterEntry:
        """Deserialize from JSON string."""
        if isinstance(data, bytes):
            data = data.decode("utf-8")
        parsed = json.loads(data)
        return cls(
            task_id=parsed["task_id"],
            reason=parsed["reason"],
            timestamp=parsed["timestamp"],
            agent_type=parsed["agent_type"],
        )


class QueueFullError(Exception):
    """Raised when a queue has reached its capacity limit.

    The API layer should translate this into HTTP 429 with Retry-After header.

    Attributes:
        agent_type: The agent type whose queue is full (or None for global).
        current_depth: Current queue depth at the time of rejection.
        limit: The configured limit that was reached.
        retry_after: Suggested seconds to wait before retrying.
    """

    def __init__(
        self,
        agent_type: AgentType | None,
        current_depth: int,
        limit: int,
        retry_after: int = DEFAULT_RETRY_AFTER_SECONDS,
    ) -> None:
        self.agent_type = agent_type
        self.current_depth = current_depth
        self.limit = limit
        self.retry_after = retry_after
        scope = f"agent:{agent_type.value}" if agent_type else "global"
        super().__init__(
            f"Queue capacity reached ({scope}): "
            f"{current_depth}/{limit}. Retry after {retry_after}s."
        )


class BackpressureQueueService:
    """Queue service with backpressure enforcement and dead letter handling.

    Wraps RedisQueueService to add:
    - Pre-enqueue capacity checks (per-agent and global limits)
    - Dead letter queue management for timed-out tasks
    - Event emission on backpressure activation and dead-lettering

    This class delegates actual queue operations to RedisQueueService
    and adds the capacity enforcement layer on top.
    """

    def __init__(
        self,
        redis: Redis,
        event_bus: RedisEventBus,
        settings: QueueSettings | None = None,
    ) -> None:
        """Initialize with Redis client, event bus, and queue settings.

        Args:
            redis: An async Redis client instance.
            event_bus: The event bus for publishing backpressure/dead-letter events.
            settings: Queue configuration. Uses defaults if not provided.
        """
        self._redis = redis
        self._queue_service = RedisQueueService(redis)
        self._event_bus = event_bus
        self._settings = settings or QueueSettings()

    @property
    def queue_service(self) -> RedisQueueService:
        """Access the underlying queue service for dequeue/depth operations."""
        return self._queue_service

    async def enqueue_with_backpressure(
        self, task_id: str, agent_type: AgentType, priority: Priority
    ) -> None:
        """Enqueue a task with backpressure checks.

        Checks both per-agent and global queue depth limits before
        enqueuing. If either limit is reached, raises QueueFullError
        and emits a queue.backpressure event.

        Args:
            task_id: UUID of the task to enqueue.
            agent_type: The agent type queue to add the task to.
            priority: Task priority level.

        Raises:
            QueueFullError: If per-agent or global queue limit is reached.
        """
        # Check per-agent limit
        agent_depth = await self._get_agent_depth(agent_type)
        if agent_depth >= self._settings.max_per_agent:
            await self._emit_backpressure_event(agent_type, agent_depth)
            raise QueueFullError(
                agent_type=agent_type,
                current_depth=agent_depth,
                limit=self._settings.max_per_agent,
            )

        # Check global limit
        global_depth = await self._get_global_depth()
        if global_depth >= self._settings.max_global:
            await self._emit_backpressure_event(None, global_depth)
            raise QueueFullError(
                agent_type=None,
                current_depth=global_depth,
                limit=self._settings.max_global,
            )

        # Enqueue via the underlying service
        await self._queue_service.enqueue(task_id, agent_type, priority)

    async def move_to_dead_letter(
        self, task_id: str, agent_type: AgentType, reason: str
    ) -> None:
        """Move a task to the dead letter queue.

        Removes the task from its agent queue (if still present) and
        adds it to the dead letter queue LIST. Emits a task.dead_lettered event.

        Args:
            task_id: UUID of the task to dead-letter.
            agent_type: The agent type queue the task was in.
            reason: Why the task is being dead-lettered.
        """
        # Remove from agent queue if still present
        key = _queue_key(agent_type)
        await self._redis.zrem(key, task_id)

        # Add to dead letter queue
        entry = DeadLetterEntry(
            task_id=task_id,
            reason=reason,
            timestamp=datetime.now(timezone.utc).isoformat(),
            agent_type=agent_type.value,
        )
        await self._redis.rpush(DEAD_LETTER_KEY, entry.to_json())

        # Emit event
        await self._emit_dead_letter_event(task_id, agent_type, reason)

        logger.warning(
            "Task moved to dead letter queue",
            extra={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "reason": reason,
            },
        )

    async def check_processing_timeouts(
        self,
        processing_tasks: dict[str, tuple[AgentType, float]],
    ) -> list[str]:
        """Check for tasks that have exceeded the processing timeout.

        Args:
            processing_tasks: Mapping of task_id to (agent_type, start_timestamp).
                The start_timestamp is a Unix epoch float.

        Returns:
            List of task_ids that were moved to the dead letter queue.
        """
        now = time.time()
        timeout = self._settings.dead_letter_timeout
        dead_lettered: list[str] = []

        for task_id, (agent_type, start_ts) in processing_tasks.items():
            elapsed = now - start_ts
            if elapsed > timeout:
                await self.move_to_dead_letter(
                    task_id,
                    agent_type,
                    reason=f"Processing timeout exceeded ({elapsed:.0f}s > {timeout}s)",
                )
                dead_lettered.append(task_id)

        return dead_lettered

    async def get_dead_letter_count(self) -> int:
        """Get the number of entries in the dead letter queue.

        Returns:
            Number of dead-lettered tasks.
        """
        return await self._redis.llen(DEAD_LETTER_KEY)

    async def get_dead_letter_entries(
        self, start: int = 0, end: int = -1
    ) -> list[DeadLetterEntry]:
        """Retrieve entries from the dead letter queue.

        Args:
            start: Start index (inclusive).
            end: End index (inclusive). -1 means all entries.

        Returns:
            List of DeadLetterEntry objects.
        """
        raw_entries = await self._redis.lrange(DEAD_LETTER_KEY, start, end)
        entries: list[DeadLetterEntry] = []
        for raw in raw_entries:
            try:
                entries.append(DeadLetterEntry.from_json(raw))
            except (json.JSONDecodeError, KeyError) as exc:
                logger.warning(
                    "Failed to parse dead letter entry",
                    extra={"error": str(exc)},
                )
        return entries

    async def _get_agent_depth(self, agent_type: AgentType) -> int:
        """Get the current queue depth for a specific agent type."""
        key = _queue_key(agent_type)
        return await self._redis.zcard(key)

    async def _get_global_depth(self) -> int:
        """Get the total queue depth across all agent types."""
        total = 0
        for at in AgentType:
            key = _queue_key(at)
            total += await self._redis.zcard(key)
        return total

    async def _emit_backpressure_event(
        self, agent_type: AgentType | None, depth: int
    ) -> None:
        """Emit a queue.backpressure event."""
        # Gather all depths for the event payload
        all_depths = {}
        for at in AgentType:
            key = _queue_key(at)
            all_depths[at.value] = await self._redis.zcard(key)

        scope = f"agent:{agent_type.value}" if agent_type else "global"
        envelope = EventEnvelope(
            event_type="queue.backpressure",
            source_component="queue",
            timestamp=datetime.now(timezone.utc).isoformat(),
            correlation_id=str(uuid.uuid4()),
            payload={
                "scope": scope,
                "current_depth": depth,
                "limit": (
                    self._settings.max_per_agent
                    if agent_type
                    else self._settings.max_global
                ),
                "queue_depths": all_depths,
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.SYSTEM_HEALTH,
        )
        await self._event_bus.publish_event(envelope)

        logger.warning(
            "Queue backpressure activated",
            extra={
                "scope": scope,
                "depth": depth,
                "queue_depths": all_depths,
            },
        )

    async def _emit_dead_letter_event(
        self, task_id: str, agent_type: AgentType, reason: str
    ) -> None:
        """Emit a task.dead_lettered event."""
        envelope = EventEnvelope(
            event_type="task.dead_lettered",
            source_component="queue",
            timestamp=datetime.now(timezone.utc).isoformat(),
            correlation_id=str(uuid.uuid4()),
            payload={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "reason": reason,
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.TASK_LIFECYCLE,
        )
        await self._event_bus.publish_event(envelope)
