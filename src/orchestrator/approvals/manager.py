"""Approval manager — lifecycle management for approval gates.

Implements the IApprovalService protocol with:
- request_approval(): persist approval, transition task to waiting_approval, emit event
- resolve(): approve (resume task) or reject (cancel task), with operator identity
- Escalation: notify secondary approver at 50% of timeout

Requirements: 7.1, 7.2, 7.3, 7.4, 7.6
"""

from __future__ import annotations

import logging
import uuid
from datetime import datetime, timedelta, timezone
from typing import Any, Awaitable, Callable, Protocol

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.approvals.interfaces import ApprovalDecision
from orchestrator.persistence.models import Approval
from orchestrator.persistence.repositories.approvals import ApprovalRepository
from orchestrator.state_machine.engine import StateMachineEngine
from orchestrator.state_machine.transitions import ApprovalStatus, TaskState

logger = logging.getLogger(__name__)


class IEventPublisher(Protocol):
    """Interface for event publishing to the event bus."""

    async def publish(self, stream: str, event: dict[str, Any]) -> None: ...


class INotificationService(Protocol):
    """Interface for sending notifications (e.g. Telegram)."""

    async def notify_approval_requested(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> None: ...

    async def notify_escalation(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        remaining_seconds: int,
    ) -> None: ...


class NoOpEventPublisher:
    """Placeholder event publisher that logs events without requiring Redis."""

    async def publish(self, stream: str, event: dict[str, Any]) -> None:
        logger.info(
            "Event published (no-op)",
            extra={"stream": stream, "event_type": event.get("event_type")},
        )


class NoOpNotificationService:
    """Placeholder notification service that logs instead of sending."""

    async def notify_approval_requested(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> None:
        logger.info(
            "Approval notification (no-op)",
            extra={"approval_id": approval_id, "task_id": task_id},
        )

    async def notify_escalation(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        remaining_seconds: int,
    ) -> None:
        logger.info(
            "Escalation notification (no-op)",
            extra={"approval_id": approval_id, "task_id": task_id},
        )


class ApprovalNotFoundError(Exception):
    """Raised when an approval_id does not exist."""

    def __init__(self, approval_id: str) -> None:
        self.approval_id = approval_id
        super().__init__(f"Approval {approval_id} not found")


class ApprovalAlreadyResolvedError(Exception):
    """Raised when attempting to resolve an already-resolved approval."""

    def __init__(self, approval_id: str, current_status: ApprovalStatus) -> None:
        self.approval_id = approval_id
        self.current_status = current_status
        super().__init__(
            f"Approval {approval_id} already resolved with status: {current_status.value}"
        )


class ApprovalManager:
    """Manages the full approval lifecycle.

    Coordinates between:
    - Persistence (ApprovalRepository) for storing approval records
    - State machine (StateMachineEngine) for task state transitions
    - Event bus (IEventPublisher) for emitting approval events
    - Notification service (INotificationService) for Telegram alerts
    """

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        state_machine: StateMachineEngine,
        event_publisher: IEventPublisher | None = None,
        notification_service: INotificationService | None = None,
        resume_callback: Callable[[str], Awaitable[None]] | None = None,
    ) -> None:
        self._session_factory = session_factory
        self._state_machine = state_machine
        self._event_publisher = event_publisher or NoOpEventPublisher()
        self._notification_service = notification_service or NoOpNotificationService()
        self._resume_callback = resume_callback

    async def request_approval(
        self,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> str:
        """Create an approval request, pausing the associated task.

        Steps:
        1. Persist approval record in DB (status=pending, timeout_at=now+timeout_seconds)
        2. Transition task to waiting_approval state
        3. Emit approval.requested event
        4. Notify via configured notification channel
        5. Return approval_id

        Args:
            task_id: UUID of the task requiring approval.
            action_type: Type of action needing approval (e.g. "deploy", "delete").
            target_resource: The resource the action targets.
            timeout_seconds: Seconds before the approval times out.

        Returns:
            The approval_id (UUID string) of the created request.
        """
        now = datetime.now(timezone.utc)
        timeout_at = now + timedelta(seconds=timeout_seconds)

        # Step 1: Persist approval record
        async with self._session_factory() as session:
            async with session.begin():
                repo = ApprovalRepository(session)
                approval = await repo.create(
                    task_id=uuid.UUID(task_id),
                    action_type=action_type,
                    target_resource=target_resource,
                    timeout_seconds=timeout_seconds,
                    timeout_at=timeout_at,
                )
                approval_id = str(approval.id)

        # Step 2: Transition task to waiting_approval
        await self._state_machine.transition(
            task_id=task_id,
            target_state=TaskState.WAITING_APPROVAL,
            actor="system",
            reason=f"Approval required: {action_type} on {target_resource}",
        )

        # Step 3: Emit approval.requested event
        await self._publish_event(
            event_type="approval.requested",
            task_id=task_id,
            approval_id=approval_id,
            payload={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": action_type,
                "target_resource": target_resource,
                "timeout_seconds": timeout_seconds,
                "timeout_at": timeout_at.isoformat(),
            },
        )

        # Step 4: Notify via notification channel
        await self._notification_service.notify_approval_requested(
            approval_id=approval_id,
            task_id=task_id,
            action_type=action_type,
            target_resource=target_resource,
            timeout_seconds=timeout_seconds,
        )

        logger.info(
            "Approval requested",
            extra={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": action_type,
                "target_resource": target_resource,
                "timeout_seconds": timeout_seconds,
            },
        )

        return approval_id

    async def resolve(
        self,
        approval_id: str,
        decision: ApprovalDecision,
        operator: str,
    ) -> None:
        """Resolve a pending approval request.

        On APPROVE:
        1. Update approval record (status=approved, operator, resolved_at)
        2. Transition task back to in_progress
        3. Emit approval.approved event

        On REJECT:
        1. Update approval record (status=rejected, operator, resolved_at)
        2. Transition task to cancelled (reason: approval_rejected)
        3. Emit approval.rejected event

        Args:
            approval_id: UUID of the approval to resolve.
            decision: Whether to approve or reject.
            operator: Identity of the person resolving.

        Raises:
            ApprovalNotFoundError: If the approval_id does not exist.
            ApprovalAlreadyResolvedError: If the approval is not pending.
        """
        # Fetch and validate the approval
        async with self._session_factory() as session:
            async with session.begin():
                repo = ApprovalRepository(session)
                approval = await repo.get_by_id(uuid.UUID(approval_id))

                if approval is None:
                    raise ApprovalNotFoundError(approval_id)

                if approval.status != ApprovalStatus.PENDING:
                    raise ApprovalAlreadyResolvedError(
                        approval_id, ApprovalStatus(approval.status)
                    )

                task_id = str(approval.task_id)

                # Resolve the approval record
                new_status = (
                    ApprovalStatus.APPROVED
                    if decision == ApprovalDecision.APPROVE
                    else ApprovalStatus.REJECTED
                )
                await repo.resolve(
                    approval_id=uuid.UUID(approval_id),
                    status=new_status,
                    operator=operator,
                )

        # Transition task based on decision
        if decision == ApprovalDecision.APPROVE:
            await self._state_machine.transition(
                task_id=task_id,
                target_state=TaskState.QUEUED,
                actor=f"operator:{operator}",
                reason="Approval granted",
            )
            if self._resume_callback is not None:
                await self._resume_callback(task_id)
            event_type = "approval.approved"
        else:
            await self._state_machine.transition(
                task_id=task_id,
                target_state=TaskState.CANCELLED,
                actor=f"operator:{operator}",
                reason="approval_rejected",
            )
            event_type = "approval.rejected"

        # Emit event
        await self._publish_event(
            event_type=event_type,
            task_id=task_id,
            approval_id=approval_id,
            payload={
                "approval_id": approval_id,
                "task_id": task_id,
                "decision": decision.value,
                "operator": operator,
            },
        )

        logger.info(
            f"Approval {decision.value}d",
            extra={
                "approval_id": approval_id,
                "task_id": task_id,
                "decision": decision.value,
                "operator": operator,
            },
        )

    async def check_escalation(self, approval: Approval) -> bool:
        """Check if an approval needs escalation and notify if so.

        Escalation triggers at 50% of the timeout duration. If the approval
        is still pending and has not yet been escalated (escalation_level == 1),
        notify the secondary approver and increment the escalation level.

        Args:
            approval: The Approval ORM instance to check.

        Returns:
            True if escalation was triggered, False otherwise.
        """
        if approval.status != ApprovalStatus.PENDING:
            return False

        now = datetime.now(timezone.utc)
        requested_at = approval.requested_at
        if hasattr(requested_at, "tzinfo") and requested_at.tzinfo is None:
            requested_at = requested_at.replace(tzinfo=timezone.utc)

        elapsed = (now - requested_at).total_seconds()
        escalation_threshold = approval.timeout_seconds * 0.5

        if elapsed >= escalation_threshold and approval.escalation_level == 1:
            # Trigger escalation
            remaining = approval.timeout_seconds - int(elapsed)
            remaining = max(remaining, 0)

            await self._notification_service.notify_escalation(
                approval_id=str(approval.id),
                task_id=str(approval.task_id),
                action_type=approval.action_type,
                target_resource=approval.target_resource,
                remaining_seconds=remaining,
            )

            # Update escalation level in DB
            async with self._session_factory() as session:
                async with session.begin():
                    db_approval = await session.get(Approval, approval.id)
                    if db_approval is not None:
                        db_approval.escalation_level = 2

            logger.info(
                "Approval escalated to secondary approver",
                extra={
                    "approval_id": str(approval.id),
                    "task_id": str(approval.task_id),
                    "elapsed_seconds": int(elapsed),
                    "remaining_seconds": remaining,
                },
            )
            return True

        return False

    async def _publish_event(
        self,
        event_type: str,
        task_id: str,
        approval_id: str,
        payload: dict[str, Any],
    ) -> None:
        """Publish an approval event to the event bus.

        Events are best-effort — failures are logged but don't affect
        the approval operation.
        """
        event = {
            "event_type": event_type,
            "source_component": "approvals",
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "correlation_id": task_id,
            "payload": payload,
            "severity": "info",
            "category": "approval",
        }
        try:
            await self._event_publisher.publish("events:approval", event)
        except Exception as exc:
            logger.warning(
                "Failed to publish approval event",
                extra={
                    "event_type": event_type,
                    "approval_id": approval_id,
                    "task_id": task_id,
                    "error": str(exc),
                },
            )
