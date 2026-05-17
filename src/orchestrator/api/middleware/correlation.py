"""Correlation ID middleware — ensures every request/response carries a trace ID.

Generates a UUID v4 X-Correlation-ID if not provided by the client.
Attaches the correlation ID to the response headers and makes it available
via request.state for downstream handlers and logging.

Validates: Requirements 1.6
"""

from __future__ import annotations

import uuid

from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import Response

CORRELATION_HEADER = "X-Correlation-ID"


class CorrelationMiddleware(BaseHTTPMiddleware):
    """Middleware that propagates or generates X-Correlation-ID on every request."""

    async def dispatch(
        self, request: Request, call_next: RequestResponseEndpoint
    ) -> Response:
        # Use client-provided correlation ID or generate a new one.
        correlation_id = request.headers.get(CORRELATION_HEADER)
        if not correlation_id:
            correlation_id = str(uuid.uuid4())
        else:
            # Validate format — if invalid, generate a new one.
            try:
                uuid.UUID(correlation_id)
            except ValueError:
                correlation_id = str(uuid.uuid4())

        # Store on request state for downstream access.
        request.state.correlation_id = correlation_id

        response = await call_next(request)

        # Always include in response headers.
        response.headers[CORRELATION_HEADER] = correlation_id
        return response
