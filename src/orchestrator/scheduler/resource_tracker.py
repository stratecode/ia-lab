"""Resource budget tracking for the orchestrator scheduler.

Polls llama.cpp health endpoints to determine available inference slots,
tracks concurrent task counts per agent type from Redis, and provides
the `can_assign(agent_type)` check used before task assignment.

Requirements: 12.1, 12.2, 12.3, 12.4, 12.5
"""

from __future__ import annotations

import asyncio
import logging
from dataclasses import dataclass, field
from typing import Any

import httpx
from redis.asyncio import Redis

from orchestrator.config import SchedulerSettings
from orchestrator.state_machine.transitions import AgentType

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Model port mapping — which llama.cpp instance serves which model role
# ---------------------------------------------------------------------------

MODEL_PORTS: dict[str, int] = {
    "code": 8080,
    "planner": 8082,
    "utility": 8083,
}

# ---------------------------------------------------------------------------
# Resource requirements per agent type (from design specification)
# ---------------------------------------------------------------------------

# Maps each agent type to the model key it requires an inference slot on.
AGENT_MODEL_REQUIREMENTS: dict[AgentType, str] = {
    AgentType.CODER: "code",
    AgentType.PLANNER: "planner",
    AgentType.REVIEWER: "code",
    AgentType.INFRA: "utility",
    AgentType.RESEARCHER: "planner",
}

# Redis keys for resource tracking
REDIS_KEY_INFERENCE_SLOTS = "resources:inference_slots"
REDIS_KEY_ACTIVE_TASKS = "resources:active_tasks"

# Backward-compatible alias
ACTIVE_TASKS_KEY = REDIS_KEY_ACTIVE_TASKS


@dataclass(slots=True)
class InferenceSlotStatus:
    """Status of inference slots for a single model instance."""

    total_slots: int = 0
    idle_slots: int = 0
    processing_slots: int = 0
    is_healthy: bool = False


@dataclass(slots=True)
class ResourceSnapshot:
    """Point-in-time snapshot of all tracked resources."""

    inference_slots: dict[str, InferenceSlotStatus] = field(default_factory=dict)
    active_tasks: dict[str, int] = field(default_factory=dict)
    total_active_tasks: int = 0
    memory_available_mb: float = 0.0


class ResourceTracker:
    """Tracks system resource availability for scheduling decisions.

    Polls llama.cpp health endpoints at a configurable interval to determine
    available inference slots. Reads concurrent task counts from Redis.
    Provides `can_assign(agent_type)` to gate task assignment.

    Supports two initialization patterns:
    1. Full: ResourceTracker(settings, redis_client, http_client)
    2. Simple: ResourceTracker(redis_client, max_concurrent_tasks) — for backward compat

    Args:
        settings: Scheduler configuration (poll interval, max concurrent tasks).
            Can also be a Redis client for backward-compatible usage.
        redis_client: Async Redis client for reading/writing resource state.
            Can also be max_concurrent_tasks int for backward-compatible usage.
        http_client: Optional httpx.AsyncClient for health endpoint polling.
            If not provided, one will be created internally.
    """

    def __init__(
        self,
        settings: SchedulerSettings | Redis,
        redis_client: Redis | int | None = None,
        http_client: httpx.AsyncClient | None = None,
        *,
        max_concurrent_tasks: int | None = None,
    ) -> None:
        # Support backward-compatible constructor: ResourceTracker(redis, max_concurrent_tasks=N)
        if isinstance(settings, Redis):
            # Old-style: settings is actually the redis client
            actual_redis = settings
            if max_concurrent_tasks is not None:
                max_tasks = max_concurrent_tasks
            elif isinstance(redis_client, int):
                max_tasks = redis_client
            else:
                max_tasks = 3
            actual_settings = SchedulerSettings(max_concurrent_tasks=max_tasks)
            actual_http_client = None
        else:
            actual_redis = redis_client  # type: ignore[assignment]
            actual_settings = settings
            if max_concurrent_tasks is not None:
                actual_settings = SchedulerSettings(
                    max_concurrent_tasks=max_concurrent_tasks,
                    resource_poll_interval=actual_settings.resource_poll_interval,
                    classification_confidence_threshold=actual_settings.classification_confidence_threshold,
                    classification_timeout=actual_settings.classification_timeout,
                )
            actual_http_client = http_client

        self._settings = actual_settings
        self._redis: Redis = actual_redis
        self._http_client = actual_http_client
        self._owns_http_client = actual_http_client is None
        self._snapshot = ResourceSnapshot()
        self._poll_task: asyncio.Task[None] | None = None
        self._running = False

    @property
    def max_concurrent_tasks(self) -> int:
        """Return the configured maximum concurrent task limit."""
        return self._settings.max_concurrent_tasks

    @property
    def snapshot(self) -> ResourceSnapshot:
        """Return the current resource snapshot."""
        return self._snapshot

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Start the background polling loop."""
        if self._running:
            return
        if self._owns_http_client or self._http_client is None:
            self._http_client = httpx.AsyncClient(timeout=10.0)
            self._owns_http_client = True
        self._running = True
        self._poll_task = asyncio.create_task(
            self._poll_loop(), name="resource_tracker_poll"
        )
        logger.info(
            "Resource tracker started",
            extra={
                "poll_interval": self._settings.resource_poll_interval,
                "max_concurrent": self._settings.max_concurrent_tasks,
            },
        )

    async def stop(self) -> None:
        """Stop the background polling loop and clean up."""
        self._running = False
        if self._poll_task is not None:
            self._poll_task.cancel()
            try:
                await self._poll_task
            except asyncio.CancelledError:
                pass
            self._poll_task = None
        if self._owns_http_client and self._http_client is not None:
            await self._http_client.aclose()
            self._http_client = None
        logger.info("Resource tracker stopped")

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def can_assign(self, agent_type: AgentType) -> bool:
        """Check if resources are available to assign a task of the given agent type.

        Returns True only if:
        1. The total concurrent task count is below the global maximum.
        2. If inference slot tracking is active (HTTP client configured),
           the required inference slot must be available (idle > 0).

        Args:
            agent_type: The agent type to check resource availability for.

        Returns:
            True if sufficient resources exist for assignment.
        """
        # Refresh active task counts from Redis
        await self._refresh_active_tasks()

        # Check global concurrent task limit
        if self._snapshot.total_active_tasks >= self._settings.max_concurrent_tasks:
            logger.debug(
                "Cannot assign: total active tasks at limit",
                extra={
                    "agent_type": agent_type.value,
                    "total_active": self._snapshot.total_active_tasks,
                    "max_concurrent": self._settings.max_concurrent_tasks,
                },
            )
            return False

        # If no HTTP client is configured, skip inference slot checks
        # (backward-compatible mode — only checks concurrent task limit)
        if self._http_client is None and not self._snapshot.inference_slots:
            return True

        # Check inference slot availability for the required model
        required_model = AGENT_MODEL_REQUIREMENTS.get(agent_type)
        if required_model is None:
            logger.warning(
                "No model requirement defined for agent type",
                extra={"agent_type": agent_type.value},
            )
            return False

        slot_status = self._snapshot.inference_slots.get(required_model)
        if slot_status is None or not slot_status.is_healthy:
            logger.debug(
                "Cannot assign: model not healthy or not polled",
                extra={
                    "agent_type": agent_type.value,
                    "model": required_model,
                },
            )
            return False

        if slot_status.idle_slots <= 0:
            logger.debug(
                "Cannot assign: no idle inference slots",
                extra={
                    "agent_type": agent_type.value,
                    "model": required_model,
                    "total_slots": slot_status.total_slots,
                    "processing_slots": slot_status.processing_slots,
                },
            )
            return False

        return True

    async def refresh(self) -> None:
        """Manually trigger a full resource refresh (poll + Redis read)."""
        await self._poll_inference_slots()
        await self._refresh_active_tasks()

    async def reserve(self, agent_type: AgentType) -> int:
        """Reserve a resource slot for the given agent type.

        Increments the active task count for the agent type in Redis.

        Args:
            agent_type: The agent type to reserve for.

        Returns:
            The new active task count for this agent type.
        """
        count = await self._redis.hincrby(
            REDIS_KEY_ACTIVE_TASKS, agent_type.value, 1
        )
        self._snapshot.active_tasks[agent_type.value] = int(count)
        self._snapshot.total_active_tasks = sum(self._snapshot.active_tasks.values())
        logger.info(
            "Resource reserved",
            extra={"agent_type": agent_type.value, "new_count": count},
        )
        return int(count)

    async def release(self, agent_type: AgentType) -> int:
        """Release a resource slot for the given agent type.

        Decrements the active task count for the agent type in Redis.
        Ensures the count never goes below zero.

        Args:
            agent_type: The agent type to release for.

        Returns:
            The new active task count for this agent type.
        """
        count = await self._redis.hincrby(
            REDIS_KEY_ACTIVE_TASKS, agent_type.value, -1
        )
        if int(count) < 0:
            await self._redis.hset(REDIS_KEY_ACTIVE_TASKS, agent_type.value, 0)
            count = 0
            logger.warning(
                "Resource count went negative, reset to 0",
                extra={"agent_type": agent_type.value},
            )
        else:
            logger.info(
                "Resource released",
                extra={"agent_type": agent_type.value, "new_count": count},
            )
        self._snapshot.active_tasks[agent_type.value] = int(count)
        self._snapshot.total_active_tasks = sum(self._snapshot.active_tasks.values())
        return int(count)

    async def get_active_tasks(self, agent_type: AgentType) -> int:
        """Get the current active task count for an agent type.

        Args:
            agent_type: The agent type to query.

        Returns:
            Number of active tasks for this agent type.
        """
        value = await self._redis.hget(REDIS_KEY_ACTIVE_TASKS, agent_type.value)
        if value is None:
            return 0
        if isinstance(value, bytes):
            value = value.decode()
        return int(value)

    async def get_total_active_tasks(self) -> int:
        """Get the total active task count across all agent types.

        Returns:
            Total number of active tasks.
        """
        await self._refresh_active_tasks()
        return self._snapshot.total_active_tasks

    async def reset(self) -> None:
        """Reset all active task counts to zero. Used during recovery or testing."""
        await self._redis.delete(REDIS_KEY_ACTIVE_TASKS)
        self._snapshot.active_tasks = {}
        self._snapshot.total_active_tasks = 0
        logger.info("Resource tracker reset")

    def update_memory_from_heartbeat(self, memory_mb: float) -> None:
        """Update available memory from a worker heartbeat report.

        Args:
            memory_mb: Available memory in megabytes reported by the worker.
        """
        self._snapshot.memory_available_mb = memory_mb

    # ------------------------------------------------------------------
    # Internal: Polling loop
    # ------------------------------------------------------------------

    async def _poll_loop(self) -> None:
        """Background loop that periodically polls inference slot availability."""
        while self._running:
            try:
                await self._poll_inference_slots()
                await self._refresh_active_tasks()
            except asyncio.CancelledError:
                break
            except Exception:
                logger.exception("Error during resource poll cycle")
            await asyncio.sleep(self._settings.resource_poll_interval)

    async def _poll_inference_slots(self) -> None:
        """Poll all llama.cpp health endpoints and update slot availability."""
        if self._http_client is None:
            return

        tasks = {
            model_key: self._poll_model_health(model_key, port)
            for model_key, port in MODEL_PORTS.items()
        }

        results = await asyncio.gather(*tasks.values(), return_exceptions=True)

        for model_key, result in zip(tasks.keys(), results):
            if isinstance(result, Exception):
                logger.warning(
                    "Failed to poll health for model",
                    extra={"model": model_key, "error": str(result)},
                )
                self._snapshot.inference_slots[model_key] = InferenceSlotStatus(
                    is_healthy=False
                )
            else:
                self._snapshot.inference_slots[model_key] = result

        # Update Redis with current slot availability for observability
        await self._update_redis_slots()

    async def _poll_model_health(
        self, model_key: str, port: int
    ) -> InferenceSlotStatus:
        """Poll a single llama.cpp instance health endpoint.

        The llama.cpp /health endpoint returns JSON with:
        - status: "ok" | "loading model" | "error"
        - slots_idle: number of idle slots
        - slots_processing: number of slots currently processing

        Args:
            model_key: Logical model name (code, planner, utility).
            port: The port the llama.cpp instance listens on.

        Returns:
            InferenceSlotStatus with parsed slot information.
        """
        assert self._http_client is not None
        url = f"http://localhost:{port}/health"

        response = await self._http_client.get(url)
        response.raise_for_status()

        data: dict[str, Any] = response.json()
        status = data.get("status", "")
        slots_idle = int(data.get("slots_idle", 0))
        slots_processing = int(data.get("slots_processing", 0))

        is_healthy = status == "ok"
        total_slots = slots_idle + slots_processing

        return InferenceSlotStatus(
            total_slots=total_slots,
            idle_slots=slots_idle,
            processing_slots=slots_processing,
            is_healthy=is_healthy,
        )

    async def _update_redis_slots(self) -> None:
        """Write current inference slot availability to Redis for observability."""
        pipe = self._redis.pipeline()
        for model_key, slot_status in self._snapshot.inference_slots.items():
            pipe.hset(REDIS_KEY_INFERENCE_SLOTS, model_key, slot_status.idle_slots)
        await pipe.execute()

    # ------------------------------------------------------------------
    # Internal: Active task tracking
    # ------------------------------------------------------------------

    async def _refresh_active_tasks(self) -> None:
        """Read active task counts from Redis."""
        raw = await self._redis.hgetall(REDIS_KEY_ACTIVE_TASKS)
        active: dict[str, int] = {}
        for key, value in raw.items():
            # Redis returns bytes or str depending on decode_responses
            k = key.decode() if isinstance(key, bytes) else key
            v = int(value.decode() if isinstance(value, bytes) else value)
            active[k] = max(v, 0)
        self._snapshot.active_tasks = active
        self._snapshot.total_active_tasks = sum(active.values())
