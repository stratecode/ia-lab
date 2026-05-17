"""Scheduler module interfaces.

Defines the ISchedulerService, IClassifier, and IResourceTracker protocols
for task classification, resource tracking, and agent assignment.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Protocol

from orchestrator.state_machine.transitions import AgentType


@dataclass(frozen=True)
class ClassificationResult:
    """Result of task classification.

    Attributes:
        agent_type: The classified agent type.
        confidence: Confidence score between 0 and 1.
        method: Classification method used ("keyword" or "llm").
    """

    agent_type: AgentType
    confidence: float
    method: str


@dataclass(frozen=True)
class AssignmentResult:
    """Result of a task assignment attempt.

    Attributes:
        success: Whether the assignment succeeded.
        agent_type: The agent type assigned (or attempted).
        workspace_path: Workspace path if assigned, None otherwise.
        reason: Explanation if assignment failed.
    """

    success: bool
    agent_type: AgentType
    workspace_path: str | None = None
    reason: str | None = None


class IClassifier(Protocol):
    """Interface for task classification."""

    async def classify(self, description: str, metadata: dict) -> ClassificationResult:
        """Classify task into agent type with confidence score.

        Args:
            description: The task description text.
            metadata: Additional task metadata for classification hints.

        Returns:
            ClassificationResult with agent_type, confidence, and method.
        """
        ...


class IResourceTracker(Protocol):
    """Interface for resource availability tracking."""

    async def can_assign(self, agent_type: AgentType) -> bool:
        """Check if resources are available for the given agent type.

        Args:
            agent_type: The agent type to check resources for.

        Returns:
            True if sufficient resources exist for assignment.
        """
        ...


class ISchedulerService(Protocol):
    """Interface for the scheduler service.

    Orchestrates classification, resource checking, and task assignment.
    """

    async def can_assign(self, agent_type: AgentType) -> bool:
        """Check if resources are available for agent type.

        Args:
            agent_type: The agent type to check.

        Returns:
            True if assignment is possible.
        """
        ...

    async def assign_task(self, task_id: str, agent_type: AgentType) -> AssignmentResult:
        """Assign task to worker with resource reservation.

        Args:
            task_id: UUID of the task to assign.
            agent_type: The agent type to assign the task to.

        Returns:
            AssignmentResult indicating success or failure with details.
        """
        ...
