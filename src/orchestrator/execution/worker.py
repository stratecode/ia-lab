"""Worker lifecycle management — registration, heartbeat, consumer loop, and shutdown.

The worker registers itself in Redis on startup, emits periodic heartbeats
with TTL-based auto-expiry detection, runs the consumer loop for task
processing, and deregisters on graceful shutdown.

Worker ID format: hostname-PID-start_timestamp (e.g., "lab-12345-1750000000")
Heartbeat interval: configurable via LAB_WORKER_HEARTBEAT_INTERVAL (default 30s)
Heartbeat TTL: 3.5 * interval (default 105s) — expiry means worker is dead

The consumer loop is started when the worker runs in "all" or "worker" mode
(controlled by LAB_ORCHESTRATOR_MODE). In "api" mode, no consumer loop runs.

Requirements: 8.1, 8.2, 8.3, 8.5, 8.6, 8.7
"""

from __future__ import annotations

import asyncio
import json
import os
import platform
import time
from datetime import datetime, timezone

import redis.asyncio as aioredis

from orchestrator.config import OrchestratorMode, WorkerSettings


# Redis key prefixes
WORKER_REGISTRY_PREFIX = "worker:"
HEARTBEAT_PREFIX = "heartbeat:"


def generate_worker_id(start_timestamp: float | None = None) -> str:
    """Generate a unique worker ID from hostname, PID, and start timestamp.

    Format: hostname-PID-start_timestamp (integer seconds)

    Args:
        start_timestamp: Override for the start time (useful for testing).
                         Defaults to current time.

    Returns:
        Worker ID string.
    """
    hostname = platform.node()
    pid = os.getpid()
    ts = int(start_timestamp if start_timestamp is not None else time.time())
    return f"{hostname}-{pid}-{ts}"


def compute_heartbeat_ttl(interval: int, multiplier: float = 3.5) -> int:
    """Compute the heartbeat key TTL in seconds.

    The TTL is set to interval * multiplier (default 3.5x).
    If the heartbeat key expires, the worker is considered dead.

    Args:
        interval: Heartbeat interval in seconds.
        multiplier: TTL multiplier (default 3.5).

    Returns:
        TTL in seconds (rounded up to nearest integer).
    """
    return int(interval * multiplier + 0.5)  # Round to nearest int


def build_heartbeat_payload(
    worker_id: str,
    current_task_id: str | None,
    memory_usage_mb: float,
    uptime_seconds: float,
    timestamp: float | None = None,
) -> str:
    """Build the JSON heartbeat payload.

    Args:
        worker_id: This worker's unique identifier.
        current_task_id: ID of the task currently being processed, or None.
        memory_usage_mb: Current memory usage in megabytes.
        uptime_seconds: Seconds since the worker started.
        timestamp: Override timestamp (defaults to current time).

    Returns:
        JSON string of the heartbeat payload.
    """
    ts = timestamp if timestamp is not None else time.time()
    payload = {
        "worker_id": worker_id,
        "task_id": current_task_id,
        "memory_mb": round(memory_usage_mb, 1),
        "uptime_s": round(uptime_seconds, 1),
        "ts": ts,
    }
    return json.dumps(payload)


def _get_memory_usage_mb() -> float:
    """Get current process memory usage in megabytes.

    Uses /proc/self/status on Linux, falls back to os-level estimation.
    """
    try:
        import resource

        # getrusage returns max RSS in kilobytes on Linux
        usage = resource.getrusage(resource.RUSAGE_SELF)
        return usage.ru_maxrss / 1024.0  # Convert KB to MB
    except (ImportError, AttributeError):
        # Fallback: not available on all platforms
        return 0.0


class Worker:
    """Worker lifecycle manager with Redis registration, heartbeat, and consumer loop.

    The worker:
    1. Registers itself in Redis on start (worker:{worker_id} HASH)
    2. Emits heartbeats every `interval` seconds (heartbeat:{worker_id} STRING with TTL)
    3. Runs the consumer loop to process tasks from agent-type queues (if mode allows)
    4. Deregisters and cancels heartbeat on stop

    The consumer loop is only started when `orchestrator_mode` is "all" or "worker".
    In "api" mode, the worker only registers and emits heartbeats (for monitoring).

    Usage:
        worker = Worker(redis_client, settings, orchestrator_mode=OrchestratorMode.ALL)
        worker.set_consumer_loop(consumer_loop)
        await worker.start()
        # ... worker is running, heartbeats are emitted, tasks are consumed ...
        await worker.stop()
    """

    def __init__(
        self,
        redis_client: aioredis.Redis,
        settings: WorkerSettings | None = None,
        worker_id: str | None = None,
        orchestrator_mode: OrchestratorMode = OrchestratorMode.ALL,
    ) -> None:
        """Initialize the worker.

        Args:
            redis_client: Async Redis client instance.
            settings: Worker settings (heartbeat interval, TTL multiplier).
                      Defaults to WorkerSettings() if not provided.
            worker_id: Override worker ID (useful for testing).
                       Defaults to auto-generated from hostname/PID/time.
            orchestrator_mode: Process mode controlling whether the consumer
                               loop is active. "all" and "worker" enable it,
                               "api" disables it.
        """
        self._redis = redis_client
        self._settings = settings or WorkerSettings()
        self._start_time = time.time()
        self._worker_id = worker_id or generate_worker_id(self._start_time)
        self._heartbeat_task: asyncio.Task | None = None
        self._is_running = False
        self._current_task_id: str | None = None
        self._orchestrator_mode = orchestrator_mode
        self._consumer_loop: object | None = None  # ConsumerLoop instance

    @property
    def worker_id(self) -> str:
        """Unique identifier for this worker."""
        return self._worker_id

    @property
    def is_running(self) -> bool:
        """Whether the worker is currently running."""
        return self._is_running

    @property
    def current_task_id(self) -> str | None:
        """ID of the task currently being processed, or None if idle."""
        return self._current_task_id

    @current_task_id.setter
    def current_task_id(self, task_id: str | None) -> None:
        """Set the current task being processed."""
        self._current_task_id = task_id

    @property
    def heartbeat_interval(self) -> int:
        """Heartbeat interval in seconds."""
        return self._settings.heartbeat_interval

    @property
    def heartbeat_ttl(self) -> int:
        """Heartbeat key TTL in seconds."""
        return compute_heartbeat_ttl(
            self._settings.heartbeat_interval,
            self._settings.heartbeat_ttl_multiplier,
        )

    @property
    def uptime_seconds(self) -> float:
        """Seconds since the worker started."""
        return time.time() - self._start_time

    @property
    def orchestrator_mode(self) -> OrchestratorMode:
        """The orchestrator mode this worker is running in."""
        return self._orchestrator_mode

    @property
    def consumer_loop_active(self) -> bool:
        """Whether the consumer loop should be active based on mode."""
        return self._orchestrator_mode in (
            OrchestratorMode.ALL,
            OrchestratorMode.WORKER,
        )

    def set_consumer_loop(self, consumer_loop: object) -> None:
        """Attach a ConsumerLoop instance to this worker.

        The consumer loop will be started/stopped with the worker lifecycle
        when the orchestrator mode is "all" or "worker".

        Args:
            consumer_loop: A ConsumerLoop instance with start()/stop() methods.
        """
        self._consumer_loop = consumer_loop

    async def start(self) -> None:
        """Start the worker: register in Redis, begin heartbeat, start consumer loop.

        The consumer loop is only started when orchestrator_mode is "all" or "worker".

        Raises:
            RuntimeError: If the worker is already running.
        """
        if self._is_running:
            raise RuntimeError(f"Worker {self._worker_id} is already running")

        self._is_running = True

        # Register worker in Redis
        await self._register()

        # Emit initial heartbeat
        await self._emit_heartbeat()

        # Start background heartbeat task
        self._heartbeat_task = asyncio.create_task(
            self._heartbeat_loop(), name=f"heartbeat-{self._worker_id}"
        )

        # Start consumer loop if mode allows and a loop is configured
        if self.consumer_loop_active and self._consumer_loop is not None:
            await self._consumer_loop.start()

    async def stop(self) -> None:
        """Stop the worker: stop consumer loop, cancel heartbeat, deregister from Redis.

        Safe to call multiple times. If the worker is not running, this is a no-op.
        """
        if not self._is_running:
            return

        self._is_running = False

        # Stop consumer loop first (allow current task to finish)
        if self._consumer_loop is not None and hasattr(self._consumer_loop, "is_running"):
            if self._consumer_loop.is_running:
                await self._consumer_loop.stop()

        # Cancel heartbeat background task
        if self._heartbeat_task is not None:
            self._heartbeat_task.cancel()
            try:
                await self._heartbeat_task
            except asyncio.CancelledError:
                pass
            self._heartbeat_task = None

        # Deregister from Redis
        await self._deregister()

    async def _register(self) -> None:
        """Register this worker in the Redis worker registry.

        Sets the worker:{worker_id} HASH with registration metadata.
        """
        registry_key = f"{WORKER_REGISTRY_PREFIX}{self._worker_id}"
        now = datetime.now(timezone.utc).isoformat()
        await self._redis.hset(
            registry_key,
            mapping={
                "agent_types": "all",  # Will be refined when queue consumer is added
                "status": "active",
                "current_task": "",
                "registered_at": now,
                "last_heartbeat": now,
            },
        )

    async def _deregister(self) -> None:
        """Remove this worker from the Redis registry and delete heartbeat key."""
        registry_key = f"{WORKER_REGISTRY_PREFIX}{self._worker_id}"
        heartbeat_key = f"{HEARTBEAT_PREFIX}{self._worker_id}"

        # Update status to stopped before removal
        await self._redis.hset(registry_key, "status", "stopped")
        # Remove heartbeat key
        await self._redis.delete(heartbeat_key)

    async def _emit_heartbeat(self) -> None:
        """Emit a single heartbeat to Redis with TTL.

        Sets heartbeat:{worker_id} STRING with JSON payload and TTL.
        Also updates last_heartbeat in the worker registry.
        """
        heartbeat_key = f"{HEARTBEAT_PREFIX}{self._worker_id}"
        registry_key = f"{WORKER_REGISTRY_PREFIX}{self._worker_id}"

        memory_mb = _get_memory_usage_mb()
        uptime_s = self.uptime_seconds

        payload = build_heartbeat_payload(
            worker_id=self._worker_id,
            current_task_id=self._current_task_id,
            memory_usage_mb=memory_mb,
            uptime_seconds=uptime_s,
        )

        ttl = self.heartbeat_ttl

        # Set heartbeat with TTL (atomic SET + EXPIRE)
        await self._redis.set(heartbeat_key, payload, ex=ttl)

        # Update registry with last heartbeat time and current task
        now = datetime.now(timezone.utc).isoformat()
        await self._redis.hset(
            registry_key,
            mapping={
                "last_heartbeat": now,
                "current_task": self._current_task_id or "",
            },
        )

    async def _heartbeat_loop(self) -> None:
        """Background loop that emits heartbeats at the configured interval.

        Runs until cancelled (via stop()) or the worker is no longer running.
        """
        while self._is_running:
            await asyncio.sleep(self._settings.heartbeat_interval)
            if not self._is_running:
                break
            try:
                await self._emit_heartbeat()
            except Exception:
                # Heartbeat failures are non-fatal — the TTL will handle detection.
                # In production, this would be logged via structlog.
                pass
