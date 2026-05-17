"""Health, readiness, and metrics endpoints.

These endpoints do NOT require authentication and provide operational
visibility into the orchestrator's component status.

Endpoints:
- GET /health — Component status (database, redis, workers) + safe_mode
- GET /ready — Returns 200 only when all dependencies are connected
- GET /metrics — Prometheus exposition format metrics
"""

from __future__ import annotations

from datetime import UTC, datetime
from enum import StrEnum
from typing import Any

import structlog
from fastapi import APIRouter, Response, status
from prometheus_client import (
    CONTENT_TYPE_LATEST,
    generate_latest,
)
from pydantic import BaseModel, Field
from redis.asyncio import Redis
from sqlalchemy.ext.asyncio import AsyncEngine

from orchestrator.config import Settings
from orchestrator.persistence.database import check_connectivity as check_db_connectivity

logger = structlog.get_logger(__name__)

router = APIRouter(tags=["health"])


# ---------------------------------------------------------------------------
# Response Schemas
# ---------------------------------------------------------------------------


class ComponentStatus(StrEnum):
    """Status of an individual component."""

    HEALTHY = "healthy"
    UNHEALTHY = "unhealthy"
    DEGRADED = "degraded"


class ComponentHealth(BaseModel):
    """Health status of a single component."""

    status: ComponentStatus
    latency_ms: float | None = None
    details: dict[str, Any] = Field(default_factory=dict)


class HealthResponse(BaseModel):
    """Full health check response with component statuses."""

    status: ComponentStatus
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))
    version: str = "0.1.0"
    safe_mode: bool
    components: dict[str, ComponentHealth]


class ReadyResponse(BaseModel):
    """Readiness probe response."""

    ready: bool
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))
    checks: dict[str, bool]


# ---------------------------------------------------------------------------
# Dependency holders (set during app startup via configure_health_routes)
# ---------------------------------------------------------------------------

_engine: AsyncEngine | None = None
_redis: Redis | None = None
_settings: Settings | None = None


def configure_health_routes(
    engine: AsyncEngine,
    redis: Redis,
    settings: Settings,
) -> None:
    """Configure the health routes with runtime dependencies.

    Called during app startup to inject the database engine, Redis client,
    and settings into the health check endpoints.

    Args:
        engine: The async SQLAlchemy engine for database health checks.
        redis: The async Redis client for Redis health checks.
        settings: The application settings (for safe_mode status).
    """
    global _engine, _redis, _settings
    _engine = engine
    _redis = redis
    _settings = settings
    logger.info("Health routes configured")


# ---------------------------------------------------------------------------
# Health Check Helpers
# ---------------------------------------------------------------------------


async def _check_database() -> ComponentHealth:
    """Check database connectivity and measure latency."""
    if _engine is None:
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            details={"error": "Database engine not configured"},
        )

    start = datetime.now(UTC)
    try:
        is_connected = await check_db_connectivity(_engine)
        latency_ms = (datetime.now(UTC) - start).total_seconds() * 1000

        if is_connected:
            return ComponentHealth(
                status=ComponentStatus.HEALTHY,
                latency_ms=round(latency_ms, 2),
            )
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            latency_ms=round(latency_ms, 2),
            details={"error": "Connection check returned False"},
        )
    except Exception as exc:
        latency_ms = (datetime.now(UTC) - start).total_seconds() * 1000
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            latency_ms=round(latency_ms, 2),
            details={"error": str(exc)},
        )


async def _check_redis() -> ComponentHealth:
    """Check Redis connectivity via PING and measure latency."""
    if _redis is None:
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            details={"error": "Redis client not configured"},
        )

    start = datetime.now(UTC)
    try:
        pong = await _redis.ping()
        latency_ms = (datetime.now(UTC) - start).total_seconds() * 1000

        if pong:
            return ComponentHealth(
                status=ComponentStatus.HEALTHY,
                latency_ms=round(latency_ms, 2),
            )
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            latency_ms=round(latency_ms, 2),
            details={"error": "PING did not return True"},
        )
    except Exception as exc:
        latency_ms = (datetime.now(UTC) - start).total_seconds() * 1000
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            latency_ms=round(latency_ms, 2),
            details={"error": str(exc)},
        )


async def _check_workers() -> ComponentHealth:
    """Check worker status by scanning worker registry in Redis.

    Workers register as `worker:{worker_id}` HASH keys in Redis.
    A healthy state means at least one worker is registered.
    """
    if _redis is None:
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            details={"error": "Redis client not configured"},
        )

    try:
        # Scan for worker keys
        worker_keys: list[str] = []
        async for key in _redis.scan_iter(match="worker:*", count=100):
            if isinstance(key, bytes):
                key = key.decode("utf-8")
            worker_keys.append(key)

        worker_count = len(worker_keys)

        if worker_count > 0:
            return ComponentHealth(
                status=ComponentStatus.HEALTHY,
                details={"registered_workers": worker_count},
            )
        # No workers registered — degraded but not necessarily unhealthy
        # (could be API-only mode)
        return ComponentHealth(
            status=ComponentStatus.DEGRADED,
            details={"registered_workers": 0, "note": "No workers registered"},
        )
    except Exception as exc:
        return ComponentHealth(
            status=ComponentStatus.UNHEALTHY,
            details={"error": str(exc)},
        )


# ---------------------------------------------------------------------------
# Route Handlers
# ---------------------------------------------------------------------------


@router.get(
    "/health",
    response_model=HealthResponse,
    summary="Health check",
    description="Returns component status (database, redis, workers) and safe_mode status. No authentication required.",
    responses={
        200: {"description": "System is healthy or degraded"},
        503: {"description": "System is unhealthy"},
    },
)
async def health_check() -> Response:
    """GET /health — Component status without authentication.

    Returns HTTP 200 if the system is healthy or degraded,
    HTTP 503 if any critical component is unhealthy.
    """
    db_health = await _check_database()
    redis_health = await _check_redis()
    workers_health = await _check_workers()

    components = {
        "database": db_health,
        "redis": redis_health,
        "workers": workers_health,
    }

    # Determine overall status
    statuses = [c.status for c in components.values()]
    if ComponentStatus.UNHEALTHY in statuses:
        overall = ComponentStatus.UNHEALTHY
    elif ComponentStatus.DEGRADED in statuses:
        overall = ComponentStatus.DEGRADED
    else:
        overall = ComponentStatus.HEALTHY

    safe_mode = _settings.security.safe_mode if _settings else True

    response_body = HealthResponse(
        status=overall,
        safe_mode=safe_mode,
        components=components,
    )

    # Return 503 if unhealthy, 200 otherwise
    status_code = (
        status.HTTP_503_SERVICE_UNAVAILABLE
        if overall == ComponentStatus.UNHEALTHY
        else status.HTTP_200_OK
    )

    return Response(
        content=response_body.model_dump_json(),
        media_type="application/json",
        status_code=status_code,
    )


@router.get(
    "/ready",
    response_model=ReadyResponse,
    summary="Readiness probe",
    description="Returns 200 only when all critical dependencies (database, redis) are connected. No authentication required.",
    responses={
        200: {"description": "All dependencies connected"},
        503: {"description": "One or more dependencies not connected"},
    },
)
async def readiness_check() -> Response:
    """GET /ready — Returns 200 only when all deps are connected.

    Used by load balancers and orchestration systems to determine
    if the service is ready to accept traffic.
    """
    db_health = await _check_database()
    redis_health = await _check_redis()

    checks = {
        "database": db_health.status == ComponentStatus.HEALTHY,
        "redis": redis_health.status == ComponentStatus.HEALTHY,
    }

    all_ready = all(checks.values())

    response_body = ReadyResponse(
        ready=all_ready,
        checks=checks,
    )

    status_code = (
        status.HTTP_200_OK if all_ready else status.HTTP_503_SERVICE_UNAVAILABLE
    )

    return Response(
        content=response_body.model_dump_json(),
        media_type="application/json",
        status_code=status_code,
    )


@router.get(
    "/metrics",
    summary="Prometheus metrics",
    description="Returns metrics in Prometheus exposition format. No authentication required.",
    responses={
        200: {"description": "Prometheus metrics in text format"},
    },
)
async def metrics() -> Response:
    """GET /metrics — Prometheus exposition format metrics.

    Exposes all registered prometheus_client metrics including:
    - Queue depths per agent type
    - Safe mode gauge
    - Resource utilization
    - Request latencies
    """
    metrics_output = generate_latest()
    return Response(
        content=metrics_output,
        media_type=CONTENT_TYPE_LATEST,
        status_code=status.HTTP_200_OK,
    )
