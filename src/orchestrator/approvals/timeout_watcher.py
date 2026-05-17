"""Async background task that monitors pending approvals for timeouts.

Periodically queries the database for pending approvals whose timeout_at
has elapsed. For each timed-out approval:
1. Updates approval status to "timeout"
2. Transitions the associated task to "failed" with reason "approval_timeout"
3. Emits an approval.timeout event

Also handles escalation: at 50% of timeout, if escalation_level == 1,
notifies the secondary approver.

Requirements: 7.5, 7.6
"""

from __future__ import annotations

import asyncio
import logging
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Protocol

from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
)
from orchestrator.state_machine.transitions import ApprovalStatus, TaskState

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Protocols for dependency injection
# ---------------------------------------------------------------------------


class IApprovalRepository(Protocol):
    """Interface for querying and updating approvals."""

    async def get_timed_out_pending(self, now: datetime) -> list[Any]:
        """Return pending approvals where timeout_at < now."""
        ...

    async def get_escalation_candidates(
        self, now: datetime, escalation_threshold_ratio: float
    ) -> list[Any]:
        """Return pending approvals past escalation threshold but not yet timed out."""
        ...

    async def resolve(
        self, approval_id: uuid.UUID, status: ApprovalStatus, operator: str
    ) -> Any:
        """Resolve an approval with the given status."""
        ...

    async def escalate(self, approval_id: uuid.UUID) -> Any:
        """Increment escalation level for an approval."""
        ...


class IStateMachine(Protocol):
    """Interface for task state transitions."""

    async def transition(
        self,
        task_id: str,
        target_state: TaskState,
        actor: str,
        reason: str | None = None,
    ) -> Any:
        """Atomically transition a task's state."""
        ...


class IEventBus(Protocol):
    """Interface for event publishing."""

    async def publish_event(self, envelope: EventEnvelope) -> str:
        """Publish an event envelope to the appropriate stream."""
        ...


class INotificationService(Protocol):
    """Interface for sending notifications (escalation)."""

    async def notify_secondary_approver(
        self, approval_id: str, task_id: str, action_type: str, target_resource: str
    ) -> None:
        """Notify the secondary approver about an escalated approval."""
        ...


# ---------------------------------------------------------------------------
# Data classes
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class TimeoutResult:
    """Result of processing a single timed-out approval."""

    approval_id: str
    task_id: str
    action: str  # "timed_out", "transition_failed", "already_resolved"
    success: bool
    error: str | None = None


@dataclass(frozen=True)
class EscalationResult:
    """Result of processing a single escalation candidate."""

    approval_id: str
    task_id: str
    action: str  # "escalated", "already_escalated", "notification_failed"
    success: bool
    error: str | None = None


# ---------------------------------------------------------------------------
# Timeout Watcher
# ---------------------------------------------------------------------------


class ApprovalTimeoutWatcher:
    """Async background task that monitors pending approvals for timeouts.

    Checks pending approvals every `check_interval` seconds. When an approval's
    timeout_at has elapsed:
    1. Resolves the approval with status "timeout"
    2. Transitions the associated task to "failed" with reason "approval_timeout"
    3. Emits an approval.timeout event

    Also handles escalation at 50% of timeout for level-1 approvals.
    """

    def __init__(
        self,
        approval_repository: IApprovalRepository,
        state_machine: IStateMachine | None = None,
        event_bus: IEventBus | None = None,
        notification_service: INotificationService | None = None,
        check_interval: float = 10.0,
        escalation_threshold_ratio: float = 0.5,
    ) -> None:
        """Initialize the timeout watcher.

        Args:
            approval_repository: Data access for approval queries and updates.
            state_machine: State machine engine for task transitions.
            event_bus: Event bus for publishing timeout events.
            notification_service: Notification service for escalation alerts.
            check_interval: Seconds between each check cycle (default 10).
            escalation_threshold_ratio: Fraction of timeout at which to escalate
                (default 0.5 = 50% of timeout).
        """
        self._repository = approval_repository
        self._state_machine = state_machine
        self._event_bus = event_bus
        self._notification_service = notification_service
        self._check_interval = check_interval
        self._escalation_threshold_ratio = escalation_threshold_ratio
        self._task: asyncio.Task[None] | None = None
        self._running = False

    @property
    def is_running(self) -> bool:
        """Whether the background watcher loop is currently active."""
        return self._running

    async def start(self) -> None:
        """Start the background timeout watcher loop.

        If already running, this is a no-op.
        """
        if self._running:
            return
        self._running = True
        self._task = asyncio.create_task(self._run_loop(), name="approval_timeout_watcher")
        logger.info(
            "Approval timeout watcher started",
            extra={"check_interval": self._check_interval},
        )

    async def stop(self) -> None:
        """Stop the background timeout watcher loop.

        If not running, this is a no-op. Waits for the current iteration
        to complete before returning.
        """
        if not self._running:
            return
        self._running = False
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        logger.info("Approval timeout watcher stopped")

    async def check_timeouts(self) -> list[TimeoutResult]:
        """Check for timed-out approvals and process them.

        This is the core logic, exposed publicly for testing without
        requiring the background loop.

        Returns:
            List of TimeoutResult for each processed approval.
        """
        now = datetime.now(timezone.utc)
        results: list[TimeoutResult] = []

        try:
            timed_out_approvals = await self._repository.get_timed_out_pending(now)
        except Exception as exc:
            logger.error(
                "Failed to query timed-out approvals",
                extra={"error": str(exc)},
            )
            return results

        for approval in timed_out_approvals:
            result = await self._process_timeout(approval)
            results.append(result)

        return results

    async def check_escalations(self) -> list[EscalationResult]:
        """Check for approvals that need escalation.

        Escalation occurs at 50% of timeout for level-1 approvals.

        Returns:
            List of EscalationResult for each processed escalation.
        """
        now = datetime.now(timezone.utc)
        results: list[EscalationResult] = []

        try:
            candidates = await self._repository.get_escalation_candidates(
                now, self._escalation_threshold_ratio
            )
        except Exception as exc:
            logger.error(
                "Failed to query escalation candidates",
                extra={"error": str(exc)},
            )
            return results

        for approval in candidates:
            result = await self._process_escalation(approval)
            results.append(result)

        return results

    async def _run_loop(self) -> None:
        """Background loop that periodically checks for timeouts and escalations."""
        while self._running:
            try:
                await self.check_timeouts()
                await self.check_escalations()
            except asyncio.CancelledError:
                break
            except Exception as exc:
                logger.error(
                    "Unexpected error in timeout watcher loop",
                    extra={"error": str(exc)},
                )

            try:
                await asyncio.sleep(self._check_interval)
            except asyncio.CancelledError:
                break

    async def _process_timeout(self, approval: Any) -> TimeoutResult:
        """Process a single timed-out approval.

        Steps:
        1. Resolve approval with status "timeout"
        2. Transition task to "failed" with reason "approval_timeout"
        3. Emit approval.timeout event
        """
        approval_id = str(approval.id)
        task_id = str(approval.task_id)

        # Step 1: Resolve approval as timed out
        try:
            await self._repository.resolve(
                approval_id=approval.id,
                status=ApprovalStatus.TIMEOUT,
                operator="system:timeout_watcher",
            )
        except Exception as exc:
            logger.error(
                "Failed to resolve timed-out approval",
                extra={
                    "approval_id": approval_id,
                    "task_id": task_id,
                    "error": str(exc),
                },
            )
            return TimeoutResult(
                approval_id=approval_id,
                task_id=task_id,
                action="resolve_failed",
                success=False,
                error=str(exc),
            )

        # Step 2: Transition task to failed
        if self._state_machine is not None:
            try:
                await self._state_machine.transition(
                    task_id=task_id,
                    target_state=TaskState.FAILED,
                    actor="system:timeout_watcher",
                    reason="approval_timeout",
                )
            except Exception as exc:
                logger.warning(
                    "Failed to transition task after approval timeout",
                    extra={
                        "approval_id": approval_id,
                        "task_id": task_id,
                        "error": str(exc),
                    },
                )
                return TimeoutResult(
                    approval_id=approval_id,
                    task_id=task_id,
                    action="transition_failed",
                    success=False,
                    error=str(exc),
                )

        # Step 3: Emit approval.timeout event
        await self._emit_timeout_event(approval_id, task_id, approval)

        logger.info(
            "Approval timed out",
            extra={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": getattr(approval, "action_type", "unknown"),
            },
        )

        return TimeoutResult(
            approval_id=approval_id,
            task_id=task_id,
            action="timed_out",
            success=True,
        )

    async def _process_escalation(self, approval: Any) -> EscalationResult:
        """Process a single escalation candidate.

        Notifies the secondary approver and increments escalation level.
        """
        approval_id = str(approval.id)
        task_id = str(approval.task_id)

        # Only escalate level-1 approvals
        if getattr(approval, "escalation_level", 1) > 1:
            return EscalationResult(
                approval_id=approval_id,
                task_id=task_id,
                action="already_escalated",
                success=True,
            )

        # Increment escalation level
        try:
            await self._repository.escalate(approval.id)
        except Exception as exc:
            logger.error(
                "Failed to escalate approval",
                extra={
                    "approval_id": approval_id,
                    "task_id": task_id,
                    "error": str(exc),
                },
            )
            return EscalationResult(
                approval_id=approval_id,
                task_id=task_id,
                action="escalation_failed",
                success=False,
                error=str(exc),
            )

        # Notify secondary approver
        if self._notification_service is not None:
            try:
                await self._notification_service.notify_secondary_approver(
                    approval_id=approval_id,
                    task_id=task_id,
                    action_type=getattr(approval, "action_type", "unknown"),
                    target_resource=getattr(approval, "target_resource", "unknown"),
                )
            except Exception as exc:
                logger.warning(
                    "Failed to notify secondary approver",
                    extra={
                        "approval_id": approval_id,
                        "task_id": task_id,
                        "error": str(exc),
                    },
                )
                return EscalationResult(
                    approval_id=approval_id,
                    task_id=task_id,
                    action="notification_failed",
                    success=False,
                    error=str(exc),
                )

        logger.info(
            "Approval escalated to secondary approver",
            extra={
                "approval_id": approval_id,
                "task_id": task_id,
            },
        )

        return EscalationResult(
            approval_id=approval_id,
            task_id=task_id,
            action="escalated",
            success=True,
        )

    async def _emit_timeout_event(
        self, approval_id: str, task_id: str, approval: Any
    ) -> None:
        """Emit an approval.timeout event to the event bus."""
        if self._event_bus is None:
            return

        envelope = EventEnvelope(
            event_type="approval.timeout",
            source_component="approval_timeout_watcher",
            timestamp=datetime.now(timezone.utc).isoformat(),
            correlation_id=task_id,
            payload={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": getattr(approval, "action_type", "unknown"),
                "target_resource": getattr(approval, "target_resource", "unknown"),
                "timeout_seconds": getattr(approval, "timeout_seconds", 300),
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.APPROVAL,
        )

        try:
            await self._event_bus.publish_event(envelope)
        except Exception as exc:
            logger.warning(
                "Failed to emit approval.timeout event",
                extra={
                    "approval_id": approval_id,
                    "task_id": task_id,
                    "error": str(exc),
                },
            )
