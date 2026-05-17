"""Structured logging and request access middleware."""

from __future__ import annotations

import contextvars
import logging
import time
from collections.abc import Callable
from typing import Any

import structlog
from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import Response

_correlation_id: contextvars.ContextVar[str | None] = contextvars.ContextVar(
    "correlation_id", default=None
)


def set_correlation_id(value: str | None) -> None:
    """Store the active correlation ID for the current context."""
    _correlation_id.set(value)


def get_correlation_id() -> str | None:
    """Return the active correlation ID for the current context."""
    return _correlation_id.get()


def _add_correlation_id(
    _: logging.Logger, __: str, event_dict: dict[str, Any]
) -> dict[str, Any]:
    correlation_id = get_correlation_id()
    if correlation_id and "correlation_id" not in event_dict:
        event_dict["correlation_id"] = correlation_id
    return event_dict


def configure_logging(log_level: str) -> None:
    """Configure stdlib logging and structlog for JSON output."""
    level = getattr(logging, str(log_level).upper(), logging.INFO)

    logging.basicConfig(
        level=level,
        format="%(message)s",
    )

    for noisy_logger in ("httpx", "httpcore"):
        logging.getLogger(noisy_logger).setLevel(max(level, logging.WARNING))

    structlog.configure(
        processors=[
            structlog.contextvars.merge_contextvars,
            _add_correlation_id,
            structlog.processors.TimeStamper(fmt="iso", utc=True, key="timestamp"),
            structlog.stdlib.add_log_level,
            structlog.processors.StackInfoRenderer(),
            structlog.processors.format_exc_info,
            structlog.processors.JSONRenderer(),
        ],
        wrapper_class=structlog.make_filtering_bound_logger(level),
        logger_factory=structlog.stdlib.LoggerFactory(),
        cache_logger_on_first_use=True,
    )


class AccessLogMiddleware(BaseHTTPMiddleware):
    """Emit structured access logs for every HTTP request."""

    async def dispatch(
        self, request: Request, call_next: RequestResponseEndpoint
    ) -> Response:
        logger = structlog.get_logger("access_log")
        start = time.perf_counter()
        response: Response | None = None

        try:
            response = await call_next(request)
            return response
        finally:
            duration_ms = round((time.perf_counter() - start) * 1000, 2)
            status_code = response.status_code if response is not None else 500
            client_ip = request.client.host if request.client else "unknown"
            logger.info(
                "request completed",
                method=request.method,
                path=request.url.path,
                status_code=status_code,
                duration_ms=duration_ms,
                client_ip=client_ip,
                component="access_log",
            )
