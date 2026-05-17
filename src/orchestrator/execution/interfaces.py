"""Execution module interfaces.

Defines the IWorker and ITaskRunner protocol classes for the execution module.
"""

from __future__ import annotations

from typing import Protocol

from orchestrator.state_machine.transitions import AgentType


class ToolResult:
    """Placeholder for the normalized tool result type.

    The full implementation lives in orchestrator.tools.interfaces.
    This is referenced here for type annotations only.
    """

    ...


class ITaskRunner(Protocol):
    """Interface for task execution.

    Executes a task within a workspace and returns a normalized result.
    """

    async def execute(
        self, task_id: str, agent_type: AgentType, workspace_path: str
    ) -> ToolResult:
        """Execute a task and return the normalized result.

        Args:
            task_id: UUID of the task to execute.
            agent_type: The agent type assigned to this task.
            workspace_path: Filesystem path to the task's workspace.

        Returns:
            A normalized ToolResult with execution output.
        """
        ...


class IWorker(Protocol):
    """Interface for the worker lifecycle.

    A worker registers itself, emits heartbeats, consumes tasks from queues,
    and handles graceful shutdown.
    """

    @property
    def worker_id(self) -> str:
        """Unique identifier for this worker (hostname-PID-start_timestamp)."""
        ...

    @property
    def is_running(self) -> bool:
        """Whether the worker is currently running."""
        ...

    async def start(self) -> None:
        """Start the worker: register in Redis and begin heartbeat emission."""
        ...

    async def stop(self) -> None:
        """Stop the worker: cancel heartbeat, deregister from Redis."""
        ...
