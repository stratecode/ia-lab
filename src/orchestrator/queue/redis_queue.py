"""Redis sorted set-based priority queue implementation.

Uses Redis ZSET (sorted sets) for priority ordering. Each agent type has its own
queue at key `queue:agent:{agent_type}`.

Priority scoring: priority_weight * 1e12 + enqueue_timestamp
- Lower score = higher priority (CRITICAL dequeues first)
- Within the same priority, earlier timestamps dequeue first (FIFO)

Priority weights:
- CRITICAL = 0
- HIGH     = 1
- NORMAL   = 2
- LOW      = 3
"""

from __future__ import annotations

import asyncio
import logging
import time

from redis.asyncio import Redis

from orchestrator.state_machine.transitions import AgentType, Priority

logger = logging.getLogger(__name__)

# Priority weights — lower weight means higher priority.
PRIORITY_WEIGHTS: dict[Priority, int] = {
    Priority.CRITICAL: 0,
    Priority.HIGH: 1,
    Priority.NORMAL: 2,
    Priority.LOW: 3,
}

# Multiplier to separate priority levels in the score space.
# Using 1e12 ensures timestamps (in microseconds) never overflow into the next
# priority band.
PRIORITY_MULTIPLIER: float = 1e12

# Key prefix for per-agent-type queues.
QUEUE_KEY_PREFIX: str = "queue:agent:"


def _queue_key(agent_type: AgentType) -> str:
    """Build the Redis key for an agent type's priority queue."""
    return f"{QUEUE_KEY_PREFIX}{agent_type.value}"


def compute_score(priority: Priority, enqueue_timestamp: float) -> float:
    """Compute the sorted set score for a task.

    Score = priority_weight * 1e12 + enqueue_timestamp

    Lower score = higher priority. Within the same priority level,
    earlier timestamps produce lower scores (FIFO).

    The timestamp is a Unix epoch in seconds (e.g. 1750000000.123).
    Current Unix timestamps (~1.75e9) are well below 1e12, ensuring
    priority bands never overlap.

    Args:
        priority: The task's priority level.
        enqueue_timestamp: Unix timestamp in seconds (float).

    Returns:
        The numeric score for Redis ZADD.
    """
    weight = PRIORITY_WEIGHTS[priority]
    return weight * PRIORITY_MULTIPLIER + enqueue_timestamp


class RedisQueueService:
    """Priority queue service backed by Redis sorted sets.

    Implements the IQueueService protocol with per-agent-type queues.
    """

    def __init__(self, redis: Redis) -> None:
        """Initialize with an async Redis client.

        Args:
            redis: An async Redis client instance (redis.asyncio.Redis).
        """
        self._redis = redis

    async def enqueue(
        self, task_id: str, agent_type: AgentType, priority: Priority
    ) -> None:
        """Add a task to the priority queue for the given agent type.

        The task is added to the sorted set with a score computed from
        its priority weight and the current timestamp.

        Args:
            task_id: UUID of the task to enqueue.
            agent_type: The agent type queue to add the task to.
            priority: Task priority level (determines ordering).
        """
        key = _queue_key(agent_type)
        score = compute_score(priority, time.time())
        await self._redis.zadd(key, {task_id: score})
        logger.debug(
            "Task enqueued",
            extra={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "priority": priority.value,
                "score": score,
                "queue_key": key,
            },
        )

    async def dequeue(self, agent_type: AgentType, timeout: float = 0) -> str | None:
        """Pop the highest-priority task for the given agent type.

        Uses ZPOPMIN to atomically remove and return the member with the
        lowest score (highest priority, earliest timestamp).

        If timeout > 0 and the queue is empty, polls at short intervals
        until a task appears or the timeout elapses.

        Args:
            agent_type: The agent type queue to dequeue from.
            timeout: Seconds to block waiting for a task. 0 means non-blocking.

        Returns:
            The task_id string, or None if no task is available.
        """
        key = _queue_key(agent_type)

        if timeout <= 0:
            # Non-blocking: single ZPOPMIN attempt
            result = await self._redis.zpopmin(key, count=1)
            if result:
                task_id = result[0][0]
                # Redis may return bytes or str depending on decode_responses
                if isinstance(task_id, bytes):
                    task_id = task_id.decode("utf-8")
                logger.debug(
                    "Task dequeued",
                    extra={"task_id": task_id, "agent_type": agent_type.value},
                )
                return task_id
            return None

        # Blocking mode: poll with short sleep intervals until timeout
        deadline = time.monotonic() + timeout
        poll_interval = min(0.1, timeout)

        while time.monotonic() < deadline:
            result = await self._redis.zpopmin(key, count=1)
            if result:
                task_id = result[0][0]
                if isinstance(task_id, bytes):
                    task_id = task_id.decode("utf-8")
                logger.debug(
                    "Task dequeued (blocking)",
                    extra={"task_id": task_id, "agent_type": agent_type.value},
                )
                return task_id
            await asyncio.sleep(poll_interval)

        return None

    async def get_depth(
        self, agent_type: AgentType | None = None
    ) -> dict[AgentType, int]:
        """Get current queue depths per agent type.

        Args:
            agent_type: If provided, return depth only for this agent type.
                        If None, return depths for all agent types.

        Returns:
            Dictionary mapping AgentType to queue depth (number of pending tasks).
        """
        if agent_type is not None:
            key = _queue_key(agent_type)
            depth = await self._redis.zcard(key)
            return {agent_type: depth}

        depths: dict[AgentType, int] = {}
        for at in AgentType:
            key = _queue_key(at)
            depth = await self._redis.zcard(key)
            depths[at] = depth
        return depths
