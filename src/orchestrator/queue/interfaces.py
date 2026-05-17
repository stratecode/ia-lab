"""Queue module interfaces.

Defines the IQueueService protocol for priority queue operations.
"""

from __future__ import annotations

from typing import Protocol

from orchestrator.state_machine.transitions import AgentType, Priority


class IQueueService(Protocol):
    """Interface for the priority queue service.

    Manages per-agent-type priority queues backed by Redis sorted sets.
    """

    async def enqueue(
        self, task_id: str, agent_type: AgentType, priority: Priority
    ) -> None:
        """Add a task to the priority queue for the given agent type.

        Args:
            task_id: UUID of the task to enqueue.
            agent_type: The agent type queue to add the task to.
            priority: Task priority level (determines ordering).
        """
        ...

    async def dequeue(self, agent_type: AgentType, timeout: float = 0) -> str | None:
        """Pop the highest-priority task for the given agent type.

        Returns the task_id of the highest-priority task (lowest score),
        or None if the queue is empty and timeout elapses.

        Args:
            agent_type: The agent type queue to dequeue from.
            timeout: Seconds to block waiting for a task. 0 means non-blocking.

        Returns:
            The task_id string, or None if no task is available.
        """
        ...

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
        ...
