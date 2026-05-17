"""Agent assignment logic.

Orchestrates the full assignment flow: classify → check resources → assign → reserve.
Implements resource reservation on assignment and release on terminal state.
Supports manual agent assignment bypass via API.

Redis key: resources:active_tasks HASH (agent_type → count)

Requirements: 4.3, 4.5, 4.6, 12.2, 12.7
"""

from __future__ import annotations

import logging
import time
import uuid
from datetime import datetime, timezone

from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
)
from orchestrator.scheduler.interfaces import (
    AssignmentResult,
    ClassificationResult,
    IClassifier,
    IResourceTracker,
)
from orchestrator.state_machine.transitions import AgentType

logger = logging.getLogger(__name__)

# Default maximum concurrent tasks across all agents.
DEFAULT_MAX_CONCURRENT_TASKS: int = 3

# Time in seconds after which a resource_wait event is emitted.
RESOURCE_WAIT_TIMEOUT: float = 60.0


class AssignmentService:
    """Orchestrates task assignment to agents.

    Implements the ISchedulerService protocol. Coordinates classification,
    resource checking, reservation, and event emission.

    The assignment flow:
    1. Classify the task (or use manual assignment bypass)
    2. Check resource availability via the resource tracker
    3. If resources available: reserve and return success
    4. If resources unavailable: task stays queued, emit event after 60s

    Resource reservation:
    - On assignment: increment active task count for agent type in Redis
    - On terminal state: decrement active task count

    Manual assignment bypass:
    - When `assigned_agent` is provided in task creation, skip classification
    - Directly attempt resource reservation for the specified agent type
    """

    def __init__(
        self,
        event_bus: RedisEventBus,
        resource_tracker: IResourceTracker,
        classifier: IClassifier,
        max_concurrent_tasks: int = DEFAULT_MAX_CONCURRENT_TASKS,
    ) -> None:
        """Initialize the assignment service.

        Args:
            event_bus: Event bus for emitting scheduler events.
            resource_tracker: Resource tracker for availability checks and reservation.
            classifier: Task classifier for automatic agent assignment.
            max_concurrent_tasks: Maximum concurrent tasks (from LAB_MAX_CONCURRENT_TASKS).
        """
        self._event_bus = event_bus
        self._resource_tracker = resource_tracker
        self._classifier = classifier
        self._max_concurrent_tasks = max_concurrent_tasks
        # Track waiting tasks: task_id → wait_start_timestamp (monotonic)
        self._waiting_tasks: dict[str, float] = {}

    @property
    def max_concurrent_tasks(self) -> int:
        """Return the configured maximum concurrent task limit."""
        return self._max_concurrent_tasks

    async def can_assign(self, agent_type: AgentType) -> bool:
        """Check if resources are available for the given agent type.

        Delegates to the resource tracker's can_assign method which checks
        both inference slot availability and the global concurrent task limit.

        Args:
            agent_type: The agent type to check.

        Returns:
            True if assignment is possible.
        """
        return await self._resource_tracker.can_assign(agent_type)

    async def assign_task(
        self, task_id: str, agent_type: AgentType
    ) -> AssignmentResult:
        """Assign a task to a worker with resource reservation.

        Checks resource availability, reserves resources on success,
        and generates a workspace path for the task.

        Args:
            task_id: UUID of the task to assign.
            agent_type: The agent type to assign the task to.

        Returns:
            AssignmentResult indicating success or failure with details.
        """
        # Check resource availability
        if not await self.can_assign(agent_type):
            # Track waiting start time for resource_wait event
            if task_id not in self._waiting_tasks:
                self._waiting_tasks[task_id] = time.monotonic()

            # Check if we've been waiting long enough to emit an event
            wait_start = self._waiting_tasks[task_id]
            elapsed = time.monotonic() - wait_start
            if elapsed >= RESOURCE_WAIT_TIMEOUT:
                await self._emit_resource_wait_event(task_id, agent_type, elapsed)
                # Reset the timer so we don't spam events
                self._waiting_tasks[task_id] = time.monotonic()

            return AssignmentResult(
                success=False,
                agent_type=agent_type,
                workspace_path=None,
                reason=f"Resources unavailable for agent type '{agent_type.value}'. "
                f"Maximum concurrent tasks ({self._max_concurrent_tasks}) reached.",
            )

        # Resources available — reserve them
        await self._resource_tracker.reserve(agent_type)

        # Generate workspace path
        workspace_path = self._generate_workspace_path(task_id)

        # Clear waiting state if task was previously waiting
        self._waiting_tasks.pop(task_id, None)

        logger.info(
            "Task assigned",
            extra={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "workspace_path": workspace_path,
            },
        )

        return AssignmentResult(
            success=True,
            agent_type=agent_type,
            workspace_path=workspace_path,
            reason=None,
        )

    async def release_resources(self, agent_type: AgentType, task_id: str) -> None:
        """Release resources when a task reaches a terminal state.

        Decrements the active task count for the agent type.

        Args:
            agent_type: The agent type whose resources to release.
            task_id: The task ID (for logging/tracking).
        """
        await self._resource_tracker.release(agent_type)

        # Clean up any waiting state
        self._waiting_tasks.pop(task_id, None)

        logger.info(
            "Resources released for task",
            extra={
                "task_id": task_id,
                "agent_type": agent_type.value,
            },
        )

    async def classify_and_assign(
        self,
        task_id: str,
        description: str,
        metadata: dict,
        assigned_agent: AgentType | None = None,
    ) -> tuple[AssignmentResult, ClassificationResult | None]:
        """Full assignment flow: classify → check resources → assign.

        If `assigned_agent` is provided, skips classification (manual bypass).
        Otherwise, classifies the task first to determine the agent type.

        Args:
            task_id: UUID of the task.
            description: Task description for classification.
            metadata: Task metadata for classification hints.
            assigned_agent: Manual agent override (bypasses classification).

        Returns:
            Tuple of (AssignmentResult, ClassificationResult or None).
            ClassificationResult is None when manual bypass is used.
        """
        # Manual assignment bypass
        if assigned_agent is not None:
            logger.info(
                "Manual agent assignment bypass",
                extra={
                    "task_id": task_id,
                    "agent_type": assigned_agent.value,
                },
            )
            result = await self.assign_task(task_id, assigned_agent)
            return result, None

        # Automatic classification
        classification = await self._classifier.classify(description, metadata)

        logger.info(
            "Task classified",
            extra={
                "task_id": task_id,
                "agent_type": classification.agent_type.value,
                "confidence": classification.confidence,
                "method": classification.method,
            },
        )

        result = await self.assign_task(task_id, classification.agent_type)
        return result, classification

    async def check_waiting_tasks(self) -> list[str]:
        """Check all waiting tasks and emit resource_wait events as needed.

        This method should be called periodically to detect tasks that
        have been waiting for resources longer than RESOURCE_WAIT_TIMEOUT.

        Returns:
            List of task IDs that triggered resource_wait events.
        """
        now = time.monotonic()
        triggered: list[str] = []

        for task_id, wait_start in list(self._waiting_tasks.items()):
            elapsed = now - wait_start
            if elapsed >= RESOURCE_WAIT_TIMEOUT:
                await self._emit_resource_wait_event(
                    task_id, AgentType.CODER, elapsed
                )
                # Reset timer
                self._waiting_tasks[task_id] = now
                triggered.append(task_id)

        return triggered

    def clear_waiting(self, task_id: str) -> None:
        """Remove a task from the waiting tracker.

        Called when a task is cancelled or otherwise removed from the queue.

        Args:
            task_id: The task ID to stop tracking.
        """
        self._waiting_tasks.pop(task_id, None)

    async def _emit_resource_wait_event(
        self, task_id: str, agent_type: AgentType, elapsed_seconds: float
    ) -> None:
        """Emit a scheduler.resource_wait event.

        Emitted after a task has been waiting for resources for 60+ seconds.

        Args:
            task_id: The waiting task ID.
            agent_type: The agent type the task is waiting for.
            elapsed_seconds: How long the task has been waiting.
        """
        envelope = EventEnvelope(
            event_type="scheduler.resource_wait",
            source_component="scheduler",
            timestamp=datetime.now(timezone.utc).isoformat(),
            correlation_id=str(uuid.uuid4()),
            payload={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "elapsed_seconds": round(elapsed_seconds, 1),
                "max_concurrent_tasks": self._max_concurrent_tasks,
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.SYSTEM_HEALTH,
        )
        await self._event_bus.publish_event(envelope)

        logger.warning(
            "Task waiting for resources",
            extra={
                "task_id": task_id,
                "agent_type": agent_type.value,
                "elapsed_seconds": round(elapsed_seconds, 1),
            },
        )

    @staticmethod
    def _generate_workspace_path(task_id: str) -> str:
        """Generate a workspace path for a task.

        The workspace path is derived from the task_id to ensure uniqueness.

        Args:
            task_id: UUID of the task.

        Returns:
            Absolute workspace path string.
        """
        return f"/opt/workspaces/{task_id}"
