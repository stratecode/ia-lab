"""Notification router with bounded retry queue and exponential backoff.

Routes notifications from internal components to the Telegram bot.
Implements the INotificationService protocol with:
- Bounded retry queue (max 100 messages, oldest dropped first on overflow)
- Exponential backoff on Telegram API failures (initial 5s, max 300s)
- Configurable state change notifications (only notify on selected states)
- Runs as part of the orchestrator process (internal function calls, not HTTP)

Requirements: 13.2, 13.6, 13.7
"""

from __future__ import annotations

import asyncio
import logging
import time
from collections import deque
from dataclasses import dataclass, field
from typing import Any, Callable, Coroutine

from orchestrator.notifications.interfaces import (
    Notification,
    NotificationType,
)
from orchestrator.state_machine.transitions import TaskState

logger = logging.getLogger(__name__)

# Default states that trigger notifications
DEFAULT_NOTIFY_STATES: frozenset[TaskState] = frozenset(
    {
        TaskState.COMPLETED,
        TaskState.FAILED,
        TaskState.CANCELLED,
        TaskState.WAITING_APPROVAL,
    }
)

# Backoff configuration
DEFAULT_INITIAL_BACKOFF: float = 5.0  # seconds
DEFAULT_MAX_BACKOFF: float = 300.0  # seconds (5 minutes)
DEFAULT_MAX_QUEUE_SIZE: int = 100


@dataclass
class QueuedMessage:
    """A message waiting in the retry queue.

    Attributes:
        notification: The notification to deliver.
        attempts: Number of delivery attempts so far.
        next_retry_at: Monotonic timestamp for next retry attempt.
        created_at: Monotonic timestamp when the message was first queued.
    """

    notification: Notification
    attempts: int = 0
    next_retry_at: float = 0.0
    created_at: float = field(default_factory=time.monotonic)


# Type alias for the send function (Telegram bot's send method)
SendFunc = Callable[[Notification], Coroutine[Any, Any, bool]]


class NotificationRouter:
    """Routes notifications to the Telegram bot with retry and backoff.

    The router accepts notifications from internal components and delivers
    them via a configured send function (typically the Telegram bot's send
    method). Failed deliveries are re-queued with exponential backoff.

    The retry queue is bounded: when it reaches max_queue_size, the oldest
    message is dropped to make room for new ones (FIFO eviction).
    """

    def __init__(
        self,
        send_func: SendFunc | None = None,
        *,
        notify_states: frozenset[TaskState] | None = None,
        initial_backoff: float = DEFAULT_INITIAL_BACKOFF,
        max_backoff: float = DEFAULT_MAX_BACKOFF,
        max_queue_size: int = DEFAULT_MAX_QUEUE_SIZE,
    ) -> None:
        """Initialize the notification router.

        Args:
            send_func: Async callable that delivers a notification.
                If None, notifications are logged but not delivered.
            notify_states: Set of TaskState values that trigger notifications.
                Defaults to completed, failed, cancelled, waiting_approval.
            initial_backoff: Initial backoff delay in seconds after first failure.
            max_backoff: Maximum backoff delay in seconds.
            max_queue_size: Maximum number of messages in the retry queue.
        """
        self._send_func = send_func
        self._notify_states = notify_states or DEFAULT_NOTIFY_STATES
        self._initial_backoff = initial_backoff
        self._max_backoff = max_backoff
        self._max_queue_size = max_queue_size

        # Bounded retry queue (deque with maxlen is not used because we need
        # explicit eviction logging; we manage bounds manually)
        self._retry_queue: deque[QueuedMessage] = deque()

        # Current backoff state — reset on successful send
        self._current_backoff: float = 0.0
        self._consecutive_failures: int = 0

        # Background retry task
        self._retry_task: asyncio.Task | None = None
        self._running: bool = False

    @property
    def is_running(self) -> bool:
        """Whether the background retry loop is active."""
        return self._running

    @property
    def queue_size(self) -> int:
        """Current number of messages in the retry queue."""
        return len(self._retry_queue)

    @property
    def current_backoff(self) -> float:
        """Current backoff delay in seconds."""
        return self._current_backoff

    @property
    def consecutive_failures(self) -> int:
        """Number of consecutive send failures."""
        return self._consecutive_failures

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Start the background retry loop."""
        if self._running:
            return
        self._running = True
        self._retry_task = asyncio.create_task(self._retry_loop())
        logger.info("Notification router started")

    async def stop(self) -> None:
        """Stop the background retry loop."""
        if not self._running:
            return
        self._running = False
        if self._retry_task is not None:
            self._retry_task.cancel()
            try:
                await self._retry_task
            except asyncio.CancelledError:
                pass
            self._retry_task = None
        logger.info(
            "Notification router stopped",
            extra={"pending_messages": len(self._retry_queue)},
        )

    # ------------------------------------------------------------------
    # INotificationService implementation
    # ------------------------------------------------------------------

    async def send_notification(self, notification: Notification) -> bool:
        """Send a notification, queuing for retry on failure.

        Returns True if delivered or queued, False if dropped.
        """
        if self._send_func is None:
            logger.debug(
                "No send function configured, notification logged only",
                extra={"type": notification.type, "title": notification.title},
            )
            return True

        # Attempt immediate delivery
        success = await self._try_send(notification)
        if success:
            return True

        # Queue for retry
        return self._enqueue(notification)

    async def notify_task_state_change(
        self,
        task_id: str,
        from_state: str,
        to_state: str,
        description: str = "",
    ) -> bool:
        """Notify about a task state transition (if state is configured)."""
        # Check if this state change should trigger a notification
        try:
            target_state = TaskState(to_state)
        except ValueError:
            logger.warning(
                "Unknown task state, skipping notification",
                extra={"to_state": to_state},
            )
            return False

        if target_state not in self._notify_states:
            logger.debug(
                "State not in notify_states, skipping",
                extra={"to_state": to_state, "task_id": task_id},
            )
            return True  # Not an error, just not configured to notify

        desc_summary = description[:100] if description else ""
        title = f"Task {task_id[:8]}… → {to_state}"
        body = (
            f"Task `{task_id}` transitioned from **{from_state}** to **{to_state}**"
        )
        if desc_summary:
            body += f"\n\n_{desc_summary}_"

        notification = Notification(
            type=NotificationType.TASK_STATE_CHANGE,
            title=title,
            body=body,
            metadata={
                "task_id": task_id,
                "from_state": from_state,
                "to_state": to_state,
            },
        )
        return await self.send_notification(notification)

    async def notify_approval_request(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        description: str = "",
    ) -> bool:
        """Notify about a pending approval request with inline buttons."""
        title = f"🔐 Approval Required: {action_type}"
        body = (
            f"**Action:** {action_type}\n"
            f"**Resource:** `{target_resource}`\n"
            f"**Task:** `{task_id}`"
        )
        if description:
            body += f"\n**Description:** _{description[:100]}_"

        # Inline keyboard for approve/reject
        reply_markup = {
            "inline_keyboard": [
                [
                    {"text": "✅ Approve", "callback_data": f"approve:{approval_id}"},
                    {"text": "❌ Reject", "callback_data": f"reject:{approval_id}"},
                ]
            ]
        }

        notification = Notification(
            type=NotificationType.APPROVAL_REQUEST,
            title=title,
            body=body,
            metadata={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": action_type,
                "target_resource": target_resource,
            },
            reply_markup=reply_markup,
        )
        return await self.send_notification(notification)

    async def notify_worker_failure(
        self,
        worker_id: str,
        task_id: str | None = None,
        reason: str = "",
    ) -> bool:
        """Notify about a worker failure."""
        title = f"⚠️ Worker Dead: {worker_id[:20]}"
        body = f"**Worker:** `{worker_id}`\n**Status:** Dead (heartbeat lost)"
        if task_id:
            body += f"\n**Affected Task:** `{task_id}`"
        if reason:
            body += f"\n**Reason:** {reason}"

        notification = Notification(
            type=NotificationType.WORKER_FAILURE,
            title=title,
            body=body,
            metadata={
                "worker_id": worker_id,
                **({"task_id": task_id} if task_id else {}),
            },
        )
        return await self.send_notification(notification)

    async def notify_system_alert(
        self,
        alert_type: str,
        message: str,
        details: dict[str, str] | None = None,
    ) -> bool:
        """Notify about a system alert."""
        title = f"🚨 System Alert: {alert_type}"
        body = f"**Alert:** {alert_type}\n**Message:** {message}"
        if details:
            detail_lines = "\n".join(f"  • {k}: {v}" for k, v in details.items())
            body += f"\n**Details:**\n{detail_lines}"

        notification = Notification(
            type=NotificationType.SYSTEM_ALERT,
            title=title,
            body=body,
            metadata={"alert_type": alert_type, **(details or {})},
        )
        return await self.send_notification(notification)

    async def notify_secondary_approver(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
    ) -> None:
        """Notify the secondary approver about an escalated approval."""
        title = f"⏰ Escalated Approval: {action_type}"
        body = (
            f"**Escalated** — primary approver did not respond.\n\n"
            f"**Action:** {action_type}\n"
            f"**Resource:** `{target_resource}`\n"
            f"**Task:** `{task_id}`"
        )

        reply_markup = {
            "inline_keyboard": [
                [
                    {"text": "✅ Approve", "callback_data": f"approve:{approval_id}"},
                    {"text": "❌ Reject", "callback_data": f"reject:{approval_id}"},
                ]
            ]
        }

        notification = Notification(
            type=NotificationType.APPROVAL_REQUEST,
            title=title,
            body=body,
            metadata={
                "approval_id": approval_id,
                "task_id": task_id,
                "action_type": action_type,
                "target_resource": target_resource,
                "escalated": "true",
            },
            reply_markup=reply_markup,
        )
        await self.send_notification(notification)

    # ------------------------------------------------------------------
    # Internal: Send with backoff
    # ------------------------------------------------------------------

    async def _try_send(self, notification: Notification) -> bool:
        """Attempt to send a notification via the configured send function.

        On success, resets the backoff state.
        On failure, increments the consecutive failure counter.

        Returns True on success, False on failure.
        """
        try:
            result = await self._send_func(notification)
            if result:
                self._reset_backoff()
                return True
            # send_func returned False — treat as failure
            self._increment_backoff()
            return False
        except Exception as exc:
            logger.warning(
                "Notification send failed",
                extra={
                    "type": notification.type,
                    "title": notification.title,
                    "error": str(exc),
                    "consecutive_failures": self._consecutive_failures + 1,
                },
            )
            self._increment_backoff()
            return False

    def _reset_backoff(self) -> None:
        """Reset backoff state after a successful send."""
        if self._consecutive_failures > 0:
            logger.info(
                "Backoff reset after successful send",
                extra={"previous_failures": self._consecutive_failures},
            )
        self._consecutive_failures = 0
        self._current_backoff = 0.0

    def _increment_backoff(self) -> None:
        """Increase backoff after a failed send."""
        self._consecutive_failures += 1
        if self._consecutive_failures == 1:
            self._current_backoff = self._initial_backoff
        else:
            self._current_backoff = min(
                self._current_backoff * 2, self._max_backoff
            )

    def _compute_backoff_for_attempt(self, attempts: int) -> float:
        """Compute the backoff delay for a given attempt number.

        Uses exponential backoff: initial * 2^(attempts-1), capped at max.
        """
        if attempts <= 0:
            return 0.0
        delay = self._initial_backoff * (2 ** (attempts - 1))
        return min(delay, self._max_backoff)

    # ------------------------------------------------------------------
    # Internal: Bounded retry queue
    # ------------------------------------------------------------------

    def _enqueue(self, notification: Notification, attempts: int = 1) -> bool:
        """Add a notification to the retry queue.

        If the queue is at capacity, the oldest message is dropped (FIFO eviction).

        Args:
            notification: The notification to queue.
            attempts: Number of attempts already made.

        Returns:
            True if the message was queued (possibly after eviction),
            False should not normally occur.
        """
        # Evict oldest if at capacity
        if len(self._retry_queue) >= self._max_queue_size:
            evicted = self._retry_queue.popleft()
            logger.warning(
                "Retry queue full, dropping oldest message",
                extra={
                    "evicted_type": evicted.notification.type,
                    "evicted_title": evicted.notification.title,
                    "evicted_attempts": evicted.attempts,
                    "queue_size": self._max_queue_size,
                },
            )

        backoff_delay = self._compute_backoff_for_attempt(attempts)
        msg = QueuedMessage(
            notification=notification,
            attempts=attempts,
            next_retry_at=time.monotonic() + backoff_delay,
        )
        self._retry_queue.append(msg)

        logger.debug(
            "Notification queued for retry",
            extra={
                "type": notification.type,
                "title": notification.title,
                "attempts": attempts,
                "backoff_delay": backoff_delay,
                "queue_size": len(self._retry_queue),
            },
        )
        return True

    # ------------------------------------------------------------------
    # Internal: Background retry loop
    # ------------------------------------------------------------------

    async def _retry_loop(self) -> None:
        """Background loop that retries queued notifications."""
        while self._running:
            try:
                await self._process_retry_queue()
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                logger.error(
                    "Unexpected error in retry loop",
                    extra={"error": str(exc)},
                )
            # Sleep briefly between iterations
            await asyncio.sleep(1.0)

    async def _process_retry_queue(self) -> None:
        """Process messages in the retry queue that are ready for retry."""
        if not self._retry_queue or self._send_func is None:
            return

        now = time.monotonic()
        # Process messages that are ready (next_retry_at <= now)
        # We iterate from the front (oldest) and process ready ones
        messages_to_retry: list[QueuedMessage] = []
        remaining: deque[QueuedMessage] = deque()

        while self._retry_queue:
            msg = self._retry_queue.popleft()
            if msg.next_retry_at <= now:
                messages_to_retry.append(msg)
            else:
                remaining.append(msg)

        # Put back messages not yet ready
        self._retry_queue = remaining

        # Attempt to send ready messages
        for msg in messages_to_retry:
            success = await self._try_send(msg.notification)
            if not success:
                # Re-queue with incremented attempt count
                new_attempts = msg.attempts + 1
                self._enqueue(msg.notification, attempts=new_attempts)
