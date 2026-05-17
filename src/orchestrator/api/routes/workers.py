"""Worker management API routes.

Endpoints:
- GET /workers — List all registered workers with status and resource usage

Requirements: 8.8
"""

from __future__ import annotations

import json
from datetime import datetime, timezone

import structlog
from fastapi import APIRouter, Depends, status
from pydantic import BaseModel
from redis.asyncio import Redis

from orchestrator.execution.worker import HEARTBEAT_PREFIX, WORKER_REGISTRY_PREFIX

logger = structlog.get_logger(__name__)

router = APIRouter(prefix="/workers", tags=["workers"])


class WorkerInfo(BaseModel):
    """Response schema for a single worker's status."""

    worker_id: str
    status: str
    agent_types: str
    current_task: str | None = None
    registered_at: str | None = None
    last_heartbeat: str | None = None
    memory_mb: float | None = None
    uptime_s: float | None = None


class WorkerListResponse(BaseModel):
    """Response schema for listing workers."""

    workers: list[WorkerInfo]
    total: int


def _get_redis() -> Redis:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("Redis dependency not configured")


@router.get(
    "",
    response_model=WorkerListResponse,
    status_code=status.HTTP_200_OK,
)
async def list_workers(
    redis: Redis = Depends(_get_redis),
) -> WorkerListResponse:
    """List all registered workers with their status, current task, and resource usage.

    Scans the Redis worker registry for all worker:{id} keys and enriches
    with heartbeat data when available.
    """
    workers: list[WorkerInfo] = []

    # Scan for all worker registry keys
    worker_keys: list[bytes] = []
    async for key in redis.scan_iter(match=f"{WORKER_REGISTRY_PREFIX}*"):
        worker_keys.append(key)

    for key in worker_keys:
        key_str = key.decode() if isinstance(key, bytes) else key
        worker_id = key_str.removeprefix(WORKER_REGISTRY_PREFIX)

        # Get worker registry data
        registry_data = await redis.hgetall(key)
        if not registry_data:
            continue

        # Decode registry fields
        decoded = {
            (k.decode() if isinstance(k, bytes) else k): (
                v.decode() if isinstance(v, bytes) else v
            )
            for k, v in registry_data.items()
        }

        # Get heartbeat data for memory/uptime info
        heartbeat_key = f"{HEARTBEAT_PREFIX}{worker_id}"
        heartbeat_raw = await redis.get(heartbeat_key)

        memory_mb: float | None = None
        uptime_s: float | None = None
        worker_status = decoded.get("status", "unknown")

        if heartbeat_raw:
            try:
                hb_data = json.loads(
                    heartbeat_raw.decode()
                    if isinstance(heartbeat_raw, bytes)
                    else heartbeat_raw
                )
                memory_mb = hb_data.get("memory_mb")
                uptime_s = hb_data.get("uptime_s")
            except (json.JSONDecodeError, AttributeError):
                pass
        else:
            # No heartbeat key means it expired — worker may be dead
            if worker_status == "active":
                worker_status = "dead"

        current_task = decoded.get("current_task") or None

        workers.append(
            WorkerInfo(
                worker_id=worker_id,
                status=worker_status,
                agent_types=decoded.get("agent_types", "unknown"),
                current_task=current_task if current_task else None,
                registered_at=decoded.get("registered_at"),
                last_heartbeat=decoded.get("last_heartbeat"),
                memory_mb=memory_mb,
                uptime_s=uptime_s,
            )
        )

    logger.info("Workers listed", total=len(workers))
    return WorkerListResponse(workers=workers, total=len(workers))
