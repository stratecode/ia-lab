"""Notification module interfaces.

Defines the INotificationService protocol for delivering notifications
to operators via configured channels (Telegram for MVP).
"""

from __future__ import annotations

from dataclasses import dataclass, field
from enum import StrEnum
from typing import Protocol


class NotificationType(StrEnum):
    """Types of notifications the router can deliver."""

    TASK_STATE_CHANGE = "task_state_change"
    APPROVAL_REQUEST = "approval_request"
    WORKER_FAILURE = "worker_failure"
    SYSTEM_ALERT = "system_alert"


@dataclass(frozen=True)
class Notification:
    """A notification message to be delivered.

    Attributes:
        type: The notification category.
        title: Short summary for the notification.
        body: Detailed message content.
        metadata: Additional context (task_id, worker_id, etc.).
        reply_markup: Optional inline keyboard data for Telegram buttons.
    """

    type: NotificationType
    title: str
    body: str
    metadata: dict[str, str] = field(default_factory=dict)
    reply_markup: dict | None = None


class INotificationService(Protocol):
    """Protocol for notification delivery.

    Implementations route notifications to configured channels
    (Telegram bot for MVP) with retry and backoff semantics.
    """

    async def send_notification(self, notification: Notification) -> bool:
        """Send a notification to the configured channel.

        Args:
            notification: The notification to deliver.

        Returns:
            True if the notification was delivered (or queued for retry),
            False if the notification was dropped (e.g., queue full).
        """
        ...

    async def notify_task_state_change(
        self,
        task_id: str,
        from_state: str,
        to_state: str,
        description: str = "",
    ) -> bool:
        """Notify about a task state transition.

        Args:
            task_id: UUID of the task.
            from_state: Previous state.
            to_state: New state.
            description: Optional task description summary.

        Returns:
            True if notification was accepted, False if dropped.
        """
        ...

    async def notify_approval_request(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        description: str = "",
    ) -> bool:
        """Notify about a pending approval request with inline buttons.

        Args:
            approval_id: UUID of the approval.
            task_id: UUID of the associated task.
            action_type: Type of action requiring approval.
            target_resource: Resource the action targets.
            description: Optional task description summary.

        Returns:
            True if notification was accepted, False if dropped.
        """
        ...

    async def notify_worker_failure(
        self,
        worker_id: str,
        task_id: str | None = None,
        reason: str = "",
    ) -> bool:
        """Notify about a worker failure (dead worker detected).

        Args:
            worker_id: ID of the failed worker.
            task_id: Optional task that was being processed.
            reason: Reason for the failure.

        Returns:
            True if notification was accepted, False if dropped.
        """
        ...

    async def notify_system_alert(
        self,
        alert_type: str,
        message: str,
        details: dict[str, str] | None = None,
    ) -> bool:
        """Notify about a system alert (backpressure, resource exhaustion).

        Args:
            alert_type: Type of alert (e.g., "backpressure", "resource_exhaustion").
            message: Human-readable alert message.
            details: Optional additional context.

        Returns:
            True if notification was accepted, False if dropped.
        """
        ...

    async def notify_secondary_approver(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
    ) -> None:
        """Notify the secondary approver about an escalated approval.

        Args:
            approval_id: UUID of the approval.
            task_id: UUID of the associated task.
            action_type: Type of action requiring approval.
            target_resource: Resource the action targets.
        """
        ...
