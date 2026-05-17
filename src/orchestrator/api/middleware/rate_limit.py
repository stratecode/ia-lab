"""Rate limiting middleware — sliding window per API key.

Implements a sliding window rate limiter using Redis sorted sets.
Each API key is limited to a configurable number of requests per minute
(default: 20 tasks/min/key). Falls back to in-memory tracking when
Redis is unavailable.

Validates: Requirements 9.3, 15.7
"""

from __future__ import annotations

import time
import uuid
from collections.abc import Callable
from typing import Any

import structlog
from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import JSONResponse, Response

logger = structlog.get_logger(__name__)

# Default configuration.
DEFAULT_RATE_LIMIT_PER_KEY = 20  # requests per minute
DEFAULT_WINDOW_SECONDS = 60


class RateLimitMiddleware(BaseHTTPMiddleware):
    """Sliding window rate limiter per API key using Redis sorted sets.

    Rate limiting is applied only to authenticated requests (those with
    request.state.api_key set by the auth middleware). Unauthenticated
    requests and public paths are not rate-limited.

    The sliding window uses Redis ZSET where:
    - Key: ratelimit:{api_key_id}
    - Members: unique request IDs
    - Scores: request timestamps

    On each request:
    1. Remove entries older than the window
    2. Count remaining entries
    3. If count >= limit, reject with 429
    4. Otherwise, add the new entry

    Args:
        app: The ASGI application.
        redis_client: An async Redis client instance (or None for in-memory fallback).
        max_requests: Maximum requests per window per API key.
        window_seconds: Sliding window duration in seconds.
    """

    def __init__(
        self,
        app,
        redis_client: Any | None = None,
        max_requests: int = DEFAULT_RATE_LIMIT_PER_KEY,
        window_seconds: int = DEFAULT_WINDOW_SECONDS,
    ) -> None:
        super().__init__(app)
        self._redis = redis_client
        self._max_requests = max_requests
        self._window_seconds = window_seconds
        # In-memory fallback when Redis is unavailable.
        self._memory_store: dict[str, list[float]] = {}

    async def dispatch(
        self, request: Request, call_next: RequestResponseEndpoint
    ) -> Response:
        # Only rate-limit authenticated requests.
        api_key = getattr(request.state, "api_key", None)
        if api_key is None:
            return await call_next(request)

        key_id = api_key.key_id
        now = time.time()

        is_limited = await self._check_rate_limit(key_id, now)
        if is_limited:
            retry_after = self._window_seconds
            logger.warning(
                "rate_limit_exceeded",
                key_id=key_id,
                key_name=api_key.name,
                max_requests=self._max_requests,
                window_seconds=self._window_seconds,
            )
            return JSONResponse(
                status_code=429,
                content={
                    "error_code": "rate_limit_exceeded",
                    "message": (
                        f"Rate limit exceeded: {self._max_requests} requests "
                        f"per {self._window_seconds}s. Try again later."
                    ),
                },
                headers={"Retry-After": str(retry_after)},
            )

        return await call_next(request)

    async def _check_rate_limit(self, key_id: str, now: float) -> bool:
        """Check and record a request against the rate limit.

        Returns True if the request should be rejected (limit exceeded).
        """
        if self._redis is not None:
            return await self._check_redis(key_id, now)
        return self._check_memory(key_id, now)

    async def _check_redis(self, key_id: str, now: float) -> bool:
        """Redis-based sliding window rate limit check.

        Uses a sorted set with timestamps as scores. Atomic pipeline:
        1. ZREMRANGEBYSCORE to prune old entries
        2. ZCARD to count current entries
        3. ZADD to record the new request (if under limit)
        4. EXPIRE to auto-cleanup stale keys
        """
        redis_key = f"ratelimit:{key_id}"
        window_start = now - self._window_seconds
        request_id = str(uuid.uuid4())

        try:
            pipe = self._redis.pipeline(transaction=True)
            # Remove entries outside the window.
            pipe.zremrangebyscore(redis_key, "-inf", window_start)
            # Count entries in the window.
            pipe.zcard(redis_key)
            results = await pipe.execute()

            current_count = results[1]

            if current_count >= self._max_requests:
                return True

            # Add new entry and set TTL.
            pipe2 = self._redis.pipeline(transaction=True)
            pipe2.zadd(redis_key, {request_id: now})
            pipe2.expire(redis_key, self._window_seconds + 10)
            await pipe2.execute()

            return False

        except Exception as exc:
            # Redis failure — fall back to in-memory.
            logger.warning(
                "rate_limit_redis_error",
                error=str(exc),
                key_id=key_id,
            )
            return self._check_memory(key_id, now)

    def _check_memory(self, key_id: str, now: float) -> bool:
        """In-memory sliding window fallback when Redis is unavailable."""
        window_start = now - self._window_seconds
        timestamps = self._memory_store.setdefault(key_id, [])

        # Prune old entries.
        timestamps[:] = [t for t in timestamps if t > window_start]

        if len(timestamps) >= self._max_requests:
            return True

        timestamps.append(now)
        return False
