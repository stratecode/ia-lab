"""Dead worker detection and task recovery.

Implements heartbeat monitoring in the API process:
- Periodically scans registered workers in Redis
- Detects missing heartbeats (expired TTL → worker is dead)
- Transitions orphaned tasks to "retrying" (if retries remain) or "failed"
- Emits `worker.dead` event on detection
- Recovers tasks from processing list (BRPOPLPUSH pattern)

Requirements: 8.4, 8.5, 19.5
"""

from __future__ import annotations

import asyncio
import json
import logging
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Protocol

import redis.asyncio as redis

from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
)
from orchestrator.state_machine.transitions import TaskState

logger = logging.getLogger(__name__)


# Redis key patterns
WORKER_KEY_PREFIX = "worker:"
HEARTBEAT_KEY_PREFIX = "heartbeat:"
PROCESSING_KEY_PREFIX = "processing:"


class IStateMachineForRecovery(Protocol):
    """Minimal state machine interface needed for task recovery."""

    async def transition(
        self,
        task_id: str,
        target_state: TaskState,
        actor: str,
        reason: str | None = None,
    ) -> Any:
        """Atomically transition a task's state."""
        ...


@dataclass(frozen=True)
class DeadWorkerInfo:
    """Information about a detected dead worker."""

    worker_id: str
    current_task_id: str | None
    last_heartbeat: str | None
    detected_at: datetime


@dataclass(frozen=True)
class RecoveryResult:
    """Result of recovering a dead worker's task."""

    worker_id: str
    task_id: str | None
    action: str  # "retrying", "failed", "no_task", "already_terminal"
    success: bool
    error: str | None = None


class DeadWorkerDetector:
    """Detects dead workers and recovers their orphaned tasks.

    Runs as an async background task in the API process. Periodically
    scans all registered workers and checks if their heartbeat key
    still exists in Redis (heartbeat keys have TTL = 3.5 * interval).

    When a heartbeat key is missing (expired), the worker is considered dead.
    """

    def __init__(
        self,
        redis_client: redis.Redis,
        event_bus: RedisEventBus,
        state_machine: IStateMachineForRecovery | None = None,
        check_interval: float = 30.0,
        heartbeat_interval: float = 30.0,
    ) -> None:
        """Initialize the dead worker detector.

        Args:
            redis_client: Redis client for reading worker/heartbeat keys.
            event_bus: Event bus for emitting worker.dead events.
            state_machine: State machine engine for task state transitions.
                If None, task recovery will only log (useful for testing).
            check_interval: How often to scan for dead workers (seconds).
            heartbeat_interval: Expected heartbeat interval from workers (seconds).
                Used to determine the dead threshold (3 * interval).
        """
        self._redis = redis_client
        self._event_bus = event_bus
        self._state_machine = state_machine
        self._check_interval = check_interval
        self._heartbeat_interval = heartbeat_interval
        self._running = False
        self._task: asyncio.Task[None] | None = None

    @property
    def is_running(self) -> bool:
        """Whether the detector background task is currently running."""
        return self._running

    async def start(self) -> None:
        """Start the dead worker detection background task."""
        if self._running:
            logger.warning("Dead worker detector already running")
            return

        self._running = True
        self._task = asyncio.create_task(self._run_loop())
        logger.info(
            "Dead worker detector started",
            extra={
                "check_interval": self._check_interval,
                "heartbeat_interval": self._heartbeat_interval,
            },
        )

    async def stop(self) -> None:
        """Stop the dead worker detection background task."""
        self._running = False
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
            self._task = None
        logger.info("Dead worker detector stopped")

    async def _run_loop(self) -> None:
        """Main detection loop — runs until stopped."""
        while self._running:
            try:
                await self.check_workers()
            except asyncio.CancelledError:
                break
            except Exception as exc:
                logger.error(
                    "Error in dead worker detection loop",
                    extra={"error": str(exc)},
                    exc_info=True,
                )
            await asyncio.sleep(self._check_interval)

    async def check_workers(self) -> list[RecoveryResult]:
        """Scan all registered workers and detect dead ones.

        Returns:
            List of recovery results for any dead workers found.
        """
        results: list[RecoveryResult] = []

        # Get all registered worker keys
        worker_ids = await self._get_registered_worker_ids()
        if not worker_ids:
            return results

        for worker_id in worker_ids:
            is_dead = await self._is_worker_dead(worker_id)
            if is_dead:
                result = await self._handle_dead_worker(worker_id)
                results.append(result)

        return results

    async def _get_registered_worker_ids(self) -> list[str]:
        """Get all registered worker IDs from Redis.

        Scans for keys matching the worker:{worker_id} pattern.
        """
        worker_ids: list[str] = []
        prefix = WORKER_KEY_PREFIX
        cursor: int | bytes = 0

        while True:
            cursor, keys = await self._redis.scan(
                cursor=cursor, match=f"{prefix}*", count=100
            )
            for key in keys:
                key_str = key.decode() if isinstance(key, bytes) else key
                # Extract worker_id from "worker:{worker_id}"
                worker_id = key_str[len(prefix):]
                if worker_id:
                    worker_ids.append(worker_id)

            # cursor is 0 when scan is complete
            if cursor == 0 or cursor == b"0":
                break

        return worker_ids

    async def _is_worker_dead(self, worker_id: str) -> bool:
        """Check if a worker is dead by verifying its heartbeat key exists.

        The heartbeat key has a TTL of 3.5 * heartbeat_interval. If the key
        has expired (doesn't exist), the worker hasn't sent a heartbeat in
        more than 3 consecutive intervals and is considered dead.

        Also checks the worker's status field — if already marked as "dead",
        skip re-processing.
        """
        # Check if already marked as dead
        worker_key = f"{WORKER_KEY_PREFIX}{worker_id}"
        status = await self._redis.hget(worker_key, "status")
        if status is not None:
            status_str = status.decode() if isinstance(status, bytes) else status
            if status_str == "dead":
                return False  # Already handled

        # Check heartbeat key existence (TTL-based detection)
        heartbeat_key = f"{HEARTBEAT_KEY_PREFIX}{worker_id}"
        exists = await self._redis.exists(heartbeat_key)
        return exists == 0

    async def _handle_dead_worker(self, worker_id: str) -> RecoveryResult:
        """Handle a detected dead worker: mark dead, recover task, emit event.

        Steps:
        1. Mark worker as dead in Redis registry
        2. Find the task the worker was processing
        3. Recover the task (retrying or failed)
        4. Remove task from processing list
        5. Emit worker.dead event
        """
        worker_key = f"{WORKER_KEY_PREFIX}{worker_id}"
        now = datetime.now(timezone.utc)

        # Step 1: Mark worker as dead
        await self._redis.hset(worker_key, "status", "dead")
        await self._redis.hset(worker_key, "dead_at", now.isoformat())

        # Step 2: Find the task the worker was processing
        current_task_id = await self._get_worker_current_task(worker_id)

        # Step 3 & 4: Recover the task
        if current_task_id:
            result = await self._recover_task(worker_id, current_task_id)
        else:
            result = RecoveryResult(
                worker_id=worker_id,
                task_id=None,
                action="no_task",
                success=True,
            )

        # Step 5: Emit worker.dead event
        await self._emit_worker_dead_event(worker_id, current_task_id, now)

        logger.warning(
            "Dead worker detected",
            extra={
                "worker_id": worker_id,
                "task_id": current_task_id,
                "action": result.action,
                "detected_at": now.isoformat(),
            },
        )

        return result

    async def _get_worker_current_task(self, worker_id: str) -> str | None:
        """Get the task ID currently assigned to a worker.

        Checks the worker:{worker_id} HASH current_task field.
        Falls back to checking the processing:{worker_id} LIST.
        """
        worker_key = f"{WORKER_KEY_PREFIX}{worker_id}"

        # Try the HASH field first
        task_id = await self._redis.hget(worker_key, "current_task")
        if task_id is not None:
            task_str = task_id.decode() if isinstance(task_id, bytes) else task_id
            if task_str and task_str != "null":
                return task_str

        # Fall back to processing list
        processing_key = f"{PROCESSING_KEY_PREFIX}{worker_id}"
        task_id = await self._redis.lindex(processing_key, 0)
        if task_id is not None:
            return task_id.decode() if isinstance(task_id, bytes) else task_id

        return None

    async def _recover_task(self, worker_id: str, task_id: str) -> RecoveryResult:
        """Recover an orphaned task from a dead worker.

        If retries remain: transition to "retrying"
        If no retries remain: transition to "failed"
        Also removes the task from the worker's processing list.
        """
        if self._state_machine is None:
            logger.info(
                "Task recovery skipped (no state machine configured)",
                extra={"worker_id": worker_id, "task_id": task_id},
            )
            return RecoveryResult(
                worker_id=worker_id,
                task_id=task_id,
                action="no_state_machine",
                success=False,
                error="No state machine configured for recovery",
            )

        # Determine retry eligibility from worker hash metadata
        # or attempt the transition and let the state machine decide
        try:
            # Try transitioning to "retrying" first
            await self._state_machine.transition(
                task_id=task_id,
                target_state=TaskState.RETRYING,
                actor=f"system:dead_worker_detector",
                reason=f"Worker {worker_id} detected as dead",
            )
            action = "retrying"
        except Exception as retry_exc:
            # If retrying fails (e.g., invalid transition or max retries),
            # try transitioning to "failed"
            try:
                await self._state_machine.transition(
                    task_id=task_id,
                    target_state=TaskState.FAILED,
                    actor=f"system:dead_worker_detector",
                    reason=f"Worker {worker_id} dead, recovery failed: {retry_exc}",
                )
                action = "failed"
            except Exception as fail_exc:
                # Task might already be in a terminal state
                logger.warning(
                    "Could not recover task from dead worker",
                    extra={
                        "worker_id": worker_id,
                        "task_id": task_id,
                        "retry_error": str(retry_exc),
                        "fail_error": str(fail_exc),
                    },
                )
                return RecoveryResult(
                    worker_id=worker_id,
                    task_id=task_id,
                    action="already_terminal",
                    success=False,
                    error=str(fail_exc),
                )

        # Remove task from processing list
        await self._remove_from_processing_list(worker_id, task_id)

        return RecoveryResult(
            worker_id=worker_id,
            task_id=task_id,
            action=action,
            success=True,
        )

    async def _remove_from_processing_list(
        self, worker_id: str, task_id: str
    ) -> None:
        """Remove a task from the worker's processing list.

        The processing list (processing:{worker_id}) holds tasks that were
        popped from the queue via BRPOPLPUSH for at-least-once delivery.
        """
        processing_key = f"{PROCESSING_KEY_PREFIX}{worker_id}"
        removed = await self._redis.lrem(processing_key, 0, task_id)
        if removed:
            logger.debug(
                "Removed task from processing list",
                extra={"worker_id": worker_id, "task_id": task_id},
            )

    async def _emit_worker_dead_event(
        self,
        worker_id: str,
        task_id: str | None,
        detected_at: datetime,
    ) -> None:
        """Emit a worker.dead event to the event bus."""
        envelope = EventEnvelope(
            event_type="worker.dead",
            source_component="dead_worker_detector",
            timestamp=detected_at.isoformat(),
            correlation_id=task_id or worker_id,
            payload={
                "worker_id": worker_id,
                "task_id": task_id,
                "detected_at": detected_at.isoformat(),
            },
            severity=EventSeverity.WARNING,
            category=EventCategory.SYSTEM_HEALTH,
        )
        try:
            await self._event_bus.publish_event(envelope)
        except Exception as exc:
            # Event emission is best-effort
            logger.warning(
                "Failed to emit worker.dead event",
                extra={
                    "worker_id": worker_id,
                    "error": str(exc),
                },
            )
