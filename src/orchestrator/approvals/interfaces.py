"""Approval module interfaces and types.

Defines the IApprovalService protocol and ApprovalDecision enum
used by the approval manager and consumers.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Protocol


class ApprovalDecision(StrEnum):
    """Possible decisions for resolving an approval request."""

    APPROVE = "approve"
    REJECT = "reject"


class IApprovalService(Protocol):
    """Protocol for approval lifecycle management.

    Implementations handle:
    - Creating approval requests (pausing task execution)
    - Resolving approvals (resuming or cancelling tasks)
    - Escalation notifications at timeout thresholds
    """

    async def request_approval(
        self,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> str:
        """Create an approval request, pausing the associated task.

        Args:
            task_id: UUID of the task requiring approval.
            action_type: Type of action needing approval (e.g. "deploy", "delete").
            target_resource: The resource the action targets.
            timeout_seconds: Seconds before the approval times out.

        Returns:
            The approval_id (UUID string) of the created request.
        """
        ...

    async def resolve(
        self,
        approval_id: str,
        decision: ApprovalDecision,
        operator: str,
    ) -> None:
        """Resolve a pending approval request.

        Args:
            approval_id: UUID of the approval to resolve.
            decision: Whether to approve or reject.
            operator: Identity of the person resolving (e.g. "user:admin").
        """
        ...
