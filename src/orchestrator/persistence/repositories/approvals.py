"""Approval repository — lifecycle queries for approval requests."""

from __future__ import annotations

import uuid
from datetime import datetime, timedelta, timezone

from sqlalchemy import and_, select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import Approval
from orchestrator.state_machine.transitions import ApprovalStatus


class ApprovalRepository:
    """Data access layer for Approval entities."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        task_id: uuid.UUID,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
        timeout_at: datetime,
    ) -> Approval:
        """Create a new approval request.

        Args:
            task_id: The task requiring approval.
            action_type: Type of action needing approval (e.g. "deploy", "delete").
            target_resource: The resource the action targets.
            timeout_seconds: Configured timeout duration.
            timeout_at: Absolute datetime when the approval times out.

        Returns:
            The newly created Approval instance.
        """
        approval = Approval(
            task_id=task_id,
            action_type=action_type,
            target_resource=target_resource,
            timeout_seconds=timeout_seconds,
            timeout_at=timeout_at,
        )
        self._session.add(approval)
        await self._session.flush()
        return approval

    async def get_by_id(self, approval_id: uuid.UUID) -> Approval | None:
        """Retrieve an approval by its primary key.

        Args:
            approval_id: The UUID of the approval.

        Returns:
            The Approval instance or None if not found.
        """
        return await self._session.get(Approval, approval_id)

    async def list_pending(self, limit: int = 50) -> list[Approval]:
        """List all pending approval requests ordered by requested_at.

        Args:
            limit: Maximum number of results to return.

        Returns:
            List of pending Approval instances.
        """
        stmt = (
            select(Approval)
            .where(Approval.status == ApprovalStatus.PENDING)
            .order_by(Approval.requested_at.asc())
            .limit(limit)
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def resolve(
        self,
        approval_id: uuid.UUID,
        status: ApprovalStatus,
        operator: str,
    ) -> Approval | None:
        """Resolve a pending approval (approve, reject, or timeout).

        Sets the resolved_at timestamp, status, and operator fields.

        Args:
            approval_id: The UUID of the approval to resolve.
            status: The resolution status (approved, rejected, timeout).
            operator: Identity of who resolved the approval.

        Returns:
            The updated Approval instance, or None if not found.
        """
        approval = await self._session.get(Approval, approval_id)
        if approval is None:
            return None
        approval.status = status
        approval.operator = operator
        approval.resolved_at = datetime.now(timezone.utc)
        await self._session.flush()
        return approval

    async def get_timed_out_pending(self, now: datetime) -> list[Approval]:
        """Return pending approvals whose timeout_at has elapsed.

        Args:
            now: The current UTC datetime to compare against timeout_at.

        Returns:
            List of Approval instances that have timed out.
        """
        stmt = (
            select(Approval)
            .where(
                and_(
                    Approval.status == ApprovalStatus.PENDING,
                    Approval.timeout_at < now,
                )
            )
            .order_by(Approval.timeout_at.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def get_escalation_candidates(
        self, now: datetime, escalation_threshold_ratio: float = 0.5
    ) -> list[Approval]:
        """Return pending approvals past escalation threshold but not yet timed out.

        An approval is an escalation candidate when:
        - Status is pending
        - escalation_level == 1 (not yet escalated)
        - Time elapsed since requested_at > threshold_ratio * timeout_seconds
        - timeout_at has NOT yet elapsed (not timed out)

        Args:
            now: The current UTC datetime.
            escalation_threshold_ratio: Fraction of timeout at which to escalate.

        Returns:
            List of Approval instances eligible for escalation.
        """
        # We need to find approvals where:
        # requested_at + (timeout_seconds * threshold_ratio) < now < timeout_at
        # AND escalation_level == 1
        stmt = (
            select(Approval)
            .where(
                and_(
                    Approval.status == ApprovalStatus.PENDING,
                    Approval.escalation_level == 1,
                    Approval.timeout_at >= now,  # Not yet timed out
                )
            )
            .order_by(Approval.requested_at.asc())
        )
        result = await self._session.execute(stmt)
        approvals = list(result.scalars().all())

        # Filter in Python for the threshold check since it involves
        # a computation on timeout_seconds per row
        candidates = []
        for approval in approvals:
            threshold_seconds = approval.timeout_seconds * escalation_threshold_ratio
            escalation_at = approval.requested_at + timedelta(seconds=threshold_seconds)
            # Ensure both datetimes are timezone-aware for comparison
            if escalation_at.tzinfo is None:
                escalation_at = escalation_at.replace(tzinfo=timezone.utc)
            compare_now = now if now.tzinfo else now.replace(tzinfo=timezone.utc)
            if compare_now >= escalation_at:
                candidates.append(approval)

        return candidates

    async def escalate(self, approval_id: uuid.UUID) -> Approval | None:
        """Increment the escalation level for an approval.

        Args:
            approval_id: The UUID of the approval to escalate.

        Returns:
            The updated Approval instance, or None if not found.
        """
        approval = await self._session.get(Approval, approval_id)
        if approval is None:
            return None
        approval.escalation_level += 1
        await self._session.flush()
        return approval
