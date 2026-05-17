"""Worker queue consumer loop — blocking dequeue, at-least-once delivery, result publishing.

The consumer loop:
1. Block-dequeues tasks from agent-type queues (using RedisQueueService.dequeue with timeout)
2. Moves the task to a processing list (processing:{worker_id} LIST) for at-least-once delivery
3. Executes the task via the task runner (ITaskRunner protocol)
4. On success: publishes result to agent response stream, removes from processing list atomically
5. On failure: handles retry logic or dead-letter

Redis keys:
- Processing list: `processing:{worker_id}` LIST (holds task_ids being processed)
- Agent response stream: `events:agent_action` (Redis Stream for results)

The consumer loop runs as an asyncio background task and integrates with the Worker lifecycle.

Requirements: 8.1, 8.5, 8.6, 8.7
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Protocol

import redis.asyncio as aioredis

from orchestrator.queue.event_bus import (
    EventCategory,
    EventEnvelope,
    EventSeverity,
    RedisEventBus,
)
from orchestrator.queue.redis_queue import RedisQueueService
from orchestrator.state_machine.transitions import AgentType

logger = logging.getLogger(__name__)

# Redis key prefix for per-worker processing lists
PROCESSING_KEY_PREFIX = "processing:"

# Default dequeue timeout in seconds (how long to block waiting for a task)
DEFAULT_DEQUEUE_TIMEOUT = 5.0


class ITaskRunner(Protocol):
    """Interface for task execution (matches execution/interfaces.py)."""

    async def execute(
        self, task_id: str, agent_type: AgentType, workspace_path: str
    ) -> Any:
        """Execute a task and return the normalized result."""
        ...


class ITaskLoader(Protocol):
    """Interface for loading execution context for a queued task."""

    async def load_task_context(self, task_id: str) -> dict[str, Any]:
        """Return execution context for a task ID.

        Expected keys:
        - workspace_path
        - description
        - repo_path
        - branch
        """
        ...


class ITaskLifecycleService(Protocol):
    async def on_task_started(
        self,
        task_id: str,
        agent_type: AgentType,
        worker_id: str,
    ) -> None: ...

    async def on_task_finished(
        self,
        task_id: str,
        agent_type: AgentType,
        result: dict[str, Any],
        worker_id: str,
    ) -> None: ...

    async def on_task_failed(
        self,
        task_id: str,
        agent_type: AgentType,
        error_message: str,
        worker_id: str,
    ) -> None: ...


@dataclass(frozen=True)
class TaskResult:
    """Result of a task execution within the consumer loop."""

    task_id: str
    agent_type: AgentType
    status: str  # "success", "error", "timeout"
    output: str = ""
    duration_ms: int = 0
    error_message: str | None = None
    exit_code: int | None = None


@dataclass
class ConsumerLoopConfig:
    """Configuration for the consumer loop."""

    # Agent types this worker can process
    agent_types: list[AgentType] = field(
        default_factory=lambda: list(AgentType)
    )
    # How long to block waiting for a task from each queue (seconds)
    dequeue_timeout: float = DEFAULT_DEQUEUE_TIMEOUT
    # Maximum number of consecutive errors before backing off
    max_consecutive_errors: int = 5
    # Backoff base delay on consecutive errors (seconds)
    error_backoff_base: float = 1.0
    # Maximum backoff delay (seconds)
    error_backoff_max: float = 30.0


class ConsumerLoop:
    """Queue consumer loop with at-least-once delivery semantics.

    The consumer loop runs as an asyncio background task. It:
    1. Polls agent-type queues in round-robin fashion
    2. Moves dequeued tasks to a processing list for crash recovery
    3. Delegates execution to the task runner
    4. Publishes results to the agent response stream
    5. Atomically removes completed tasks from the processing list

    Usage:
        consumer = ConsumerLoop(
            redis_client=redis,
            queue_service=queue_service,
            event_bus=event_bus,
            worker_id="lab-123-1750000000",
        )
        await consumer.start()
        # ... consumer is running ...
        await consumer.stop()
    """

    def __init__(
        self,
        redis_client: aioredis.Redis,
        queue_service: RedisQueueService,
        event_bus: RedisEventBus,
        worker_id: str,
        task_runner: ITaskRunner | None = None,
        task_loader: ITaskLoader | None = None,
        lifecycle_service: ITaskLifecycleService | None = None,
        config: ConsumerLoopConfig | None = None,
    ) -> None:
        """Initialize the consumer loop.

        Args:
            redis_client: Async Redis client for processing list operations.
            queue_service: Queue service for dequeuing tasks.
            event_bus: Event bus for publishing results to agent response stream.
            worker_id: This worker's unique identifier.
            task_runner: Task execution implementation. If None, tasks are
                         acknowledged without execution (useful for testing).
            config: Consumer loop configuration. Defaults to ConsumerLoopConfig().
        """
        self._redis = redis_client
        self._queue_service = queue_service
        self._event_bus = event_bus
        self._worker_id = worker_id
        self._task_runner = task_runner
        self._task_loader = task_loader
        self._lifecycle_service = lifecycle_service
        self._config = config or ConsumerLoopConfig()
        self._running = False
        self._task: asyncio.Task[None] | None = None
        self._consecutive_errors = 0
        self._current_task_id: str | None = None
        self._tasks_processed = 0
        self._tasks_failed = 0

    @property
    def is_running(self) -> bool:
        """Whether the consumer loop is currently running."""
        return self._running

    @property
    def current_task_id(self) -> str | None:
        """ID of the task currently being processed, or None if idle."""
        return self._current_task_id

    @property
    def tasks_processed(self) -> int:
        """Total number of tasks successfully processed."""
        return self._tasks_processed

    @property
    def tasks_failed(self) -> int:
        """Total number of tasks that failed during processing."""
        return self._tasks_failed

    @property
    def processing_key(self) -> str:
        """Redis key for this worker's processing list."""
        return f"{PROCESSING_KEY_PREFIX}{self._worker_id}"

    async def start(self) -> None:
        """Start the consumer loop as a background task.

        Raises:
            RuntimeError: If the consumer loop is already running.
        """
        if self._running:
            raise RuntimeError(
                f"Consumer loop for worker {self._worker_id} is already running"
            )

        self._running = True
        self._task = asyncio.create_task(
            self._run_loop(), name=f"consumer-{self._worker_id}"
        )
        logger.info(
            "Consumer loop started",
            extra={
                "worker_id": self._worker_id,
                "agent_types": [at.value for at in self._config.agent_types],
                "dequeue_timeout": self._config.dequeue_timeout,
            },
        )

    async def stop(self) -> None:
        """Stop the consumer loop gracefully.

        Waits for the current task to finish before stopping.
        Safe to call multiple times.
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

        logger.info(
            "Consumer loop stopped",
            extra={
                "worker_id": self._worker_id,
                "tasks_processed": self._tasks_processed,
                "tasks_failed": self._tasks_failed,
            },
        )

    async def _run_loop(self) -> None:
        """Main consumer loop — polls queues and processes tasks."""
        agent_types = self._config.agent_types
        if not agent_types:
            logger.warning("No agent types configured, consumer loop idle")
            return

        queue_index = 0

        while self._running:
            try:
                # Round-robin across agent type queues
                agent_type = agent_types[queue_index % len(agent_types)]
                queue_index += 1

                # Block-dequeue from the current agent type queue
                task_id = await self._queue_service.dequeue(
                    agent_type=agent_type,
                    timeout=self._config.dequeue_timeout,
                )

                if task_id is None:
                    # No task available, continue to next queue
                    continue

                # Process the task
                await self._process_task(task_id, agent_type)
                self._consecutive_errors = 0

            except asyncio.CancelledError:
                break
            except Exception as exc:
                self._consecutive_errors += 1
                logger.error(
                    "Error in consumer loop iteration",
                    extra={
                        "worker_id": self._worker_id,
                        "consecutive_errors": self._consecutive_errors,
                        "error": str(exc),
                    },
                    exc_info=True,
                )

                # Apply backoff on consecutive errors
                if self._consecutive_errors >= self._config.max_consecutive_errors:
                    backoff = min(
                        self._config.error_backoff_base
                        * (2 ** (self._consecutive_errors - self._config.max_consecutive_errors)),
                        self._config.error_backoff_max,
                    )
                    logger.warning(
                        "Consumer loop backing off due to consecutive errors",
                        extra={
                            "worker_id": self._worker_id,
                            "backoff_seconds": backoff,
                            "consecutive_errors": self._consecutive_errors,
                        },
                    )
                    await asyncio.sleep(backoff)

    async def _process_task(self, task_id: str, agent_type: AgentType) -> None:
        """Process a single task: move to processing list, execute, publish result.

        Args:
            task_id: The task ID dequeued from the queue.
            agent_type: The agent type queue the task came from.
        """
        self._current_task_id = task_id

        try:
            # Step 1: Move task to processing list (at-least-once delivery)
            await self._add_to_processing_list(task_id)

            # Step 2: Execute the task
            if self._lifecycle_service is not None:
                await self._lifecycle_service.on_task_started(
                    task_id=task_id,
                    agent_type=agent_type,
                    worker_id=self._worker_id,
                )

            start_time = time.monotonic()
            result = await self._execute_task(task_id, agent_type)
            duration_ms = int((time.monotonic() - start_time) * 1000)

            # Step 3: Publish result to agent response stream
            task_result = TaskResult(
                task_id=task_id,
                agent_type=agent_type,
                status=result.get("status", "success") if isinstance(result, dict) else "success",
                output=result.get("output", "") if isinstance(result, dict) else str(result or ""),
                duration_ms=duration_ms,
                error_message=result.get("error_message") if isinstance(result, dict) else None,
                exit_code=result.get("exit_code") if isinstance(result, dict) else None,
            )
            await self._publish_result(task_result)

            if self._lifecycle_service is not None:
                await self._lifecycle_service.on_task_finished(
                    task_id=task_id,
                    agent_type=agent_type,
                    result=result or {},
                    worker_id=self._worker_id,
                )

            # Step 4: Atomically remove from processing list on completion
            await self._remove_from_processing_list(task_id)

            self._tasks_processed += 1
            logger.info(
                "Task processed successfully",
                extra={
                    "worker_id": self._worker_id,
                    "task_id": task_id,
                    "agent_type": agent_type.value,
                    "duration_ms": duration_ms,
                },
            )

        except Exception as exc:
            self._tasks_failed += 1
            duration_ms = 0
            error_msg = str(exc)

            if self._lifecycle_service is not None:
                try:
                    await self._lifecycle_service.on_task_failed(
                        task_id=task_id,
                        agent_type=agent_type,
                        error_message=error_msg,
                        worker_id=self._worker_id,
                    )
                except Exception:
                    logger.exception(
                        "Lifecycle failure while handling task failure",
                        extra={"task_id": task_id, "worker_id": self._worker_id},
                    )

            # Publish failure result
            task_result = TaskResult(
                task_id=task_id,
                agent_type=agent_type,
                status="error",
                output="",
                duration_ms=duration_ms,
                error_message=error_msg,
            )
            try:
                await self._publish_result(task_result)
            except Exception as pub_exc:
                logger.error(
                    "Failed to publish error result",
                    extra={
                        "worker_id": self._worker_id,
                        "task_id": task_id,
                        "publish_error": str(pub_exc),
                    },
                )

            # Remove from processing list even on failure
            # (the result has been published, so the task is "handled")
            try:
                await self._remove_from_processing_list(task_id)
            except Exception:
                pass

            logger.error(
                "Task processing failed",
                extra={
                    "worker_id": self._worker_id,
                    "task_id": task_id,
                    "agent_type": agent_type.value,
                    "error": error_msg,
                },
                exc_info=True,
            )

        finally:
            self._current_task_id = None

    async def _execute_task(
        self, task_id: str, agent_type: AgentType
    ) -> dict[str, Any] | None:
        """Execute a task via the task runner.

        If no task runner is configured, returns a placeholder result.

        Args:
            task_id: The task to execute.
            agent_type: The agent type for this task.

        Returns:
            Execution result dict or None.
        """
        if self._task_runner is None:
            # No runner configured — return a no-op result
            logger.debug(
                "No task runner configured, acknowledging task without execution",
                extra={"task_id": task_id, "agent_type": agent_type.value},
            )
            return {"status": "success", "output": "no_runner_configured"}

        task_context = (
            await self._task_loader.load_task_context(task_id)
            if self._task_loader is not None
            else {}
        )
        workspace_path = str(task_context.get("workspace_path") or "").strip()
        if not workspace_path:
            raise ValueError(
                f"Task {task_id} has no workspace_path available for execution"
            )

        result = await self._task_runner.execute(
            task_id=task_id,
            agent_type=agent_type,
            workspace_path=workspace_path,
            description=str(task_context.get("description") or ""),
            repo_path=str(task_context.get("repo_path") or workspace_path),
            branch=str(task_context.get("branch") or "main"),
            metadata=task_context.get("metadata") if isinstance(task_context.get("metadata"), dict) else {},
        )

        # Normalize result to dict
        if result is None:
            return {"status": "success", "output": ""}
        if isinstance(result, dict):
            return result
        # If result has a model_dump or dict method (Pydantic), use it
        if hasattr(result, "model_dump"):
            return result.model_dump()
        if hasattr(result, "__dict__"):
            return vars(result)
        return {"status": "success", "output": str(result)}

    async def _add_to_processing_list(self, task_id: str) -> None:
        """Add a task to this worker's processing list.

        The processing list provides at-least-once delivery: if the worker
        crashes mid-task, the dead worker detector can find the task in
        the processing list and recover it.

        Args:
            task_id: The task ID to add.
        """
        await self._redis.lpush(self.processing_key, task_id)
        logger.debug(
            "Task added to processing list",
            extra={
                "worker_id": self._worker_id,
                "task_id": task_id,
                "processing_key": self.processing_key,
            },
        )

    async def _remove_from_processing_list(self, task_id: str) -> None:
        """Atomically remove a task from this worker's processing list.

        Called after the result has been published successfully.

        Args:
            task_id: The task ID to remove.
        """
        removed = await self._redis.lrem(self.processing_key, 0, task_id)
        if removed:
            logger.debug(
                "Task removed from processing list",
                extra={
                    "worker_id": self._worker_id,
                    "task_id": task_id,
                    "processing_key": self.processing_key,
                },
            )

    async def _publish_result(self, result: TaskResult) -> None:
        """Publish a task result to the agent response stream.

        Uses the event bus to publish to `events:agent_action` stream.

        Args:
            result: The task execution result to publish.
        """
        now = datetime.now(timezone.utc)
        envelope = EventEnvelope(
            event_type="agent_action.task_completed"
            if result.status == "success"
            else "agent_action.task_failed",
            source_component=f"worker:{self._worker_id}",
            timestamp=now.isoformat(),
            correlation_id=result.task_id,
            payload={
                "task_id": result.task_id,
                "agent_type": result.agent_type.value,
                "status": result.status,
                "output": result.output,
                "duration_ms": result.duration_ms,
                "error_message": result.error_message,
                "exit_code": result.exit_code,
                "worker_id": self._worker_id,
            },
            severity=EventSeverity.INFO
            if result.status == "success"
            else EventSeverity.ERROR,
            category=EventCategory.AGENT_ACTION,
        )
        await self._event_bus.publish_event(envelope)
        logger.debug(
            "Task result published to agent response stream",
            extra={
                "worker_id": self._worker_id,
                "task_id": result.task_id,
                "status": result.status,
            },
        )

    async def recover_processing_list(self) -> list[str]:
        """Recover tasks left in the processing list from a previous crash.

        On startup, checks if there are tasks in the processing list from
        a previous worker instance that crashed. Returns the task IDs for
        the caller to handle (re-enqueue or mark as failed).

        Returns:
            List of task IDs found in the processing list.
        """
        tasks: list[str] = []
        length = await self._redis.llen(self.processing_key)
        if length == 0:
            return tasks

        # Read all tasks from the processing list
        raw_tasks = await self._redis.lrange(self.processing_key, 0, -1)
        for raw_task_id in raw_tasks:
            task_id = (
                raw_task_id.decode()
                if isinstance(raw_task_id, bytes)
                else raw_task_id
            )
            tasks.append(task_id)

        if tasks:
            logger.warning(
                "Found tasks in processing list from previous run",
                extra={
                    "worker_id": self._worker_id,
                    "task_count": len(tasks),
                    "task_ids": tasks,
                },
            )

        return tasks
