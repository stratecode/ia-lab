"""Authentication middleware — Bearer token validation and scope enforcement.

Validates API keys against the database (SHA-256 hash lookup), enforces
scope-based access control, and implements brute-force protection by
blocking sources after repeated failed attempts.

Validates: Requirements 1.4, 1.5, 15.1, 15.2, 15.3, 15.7
"""

from __future__ import annotations

import hashlib
import time
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field

import structlog
from starlette.middleware.base import BaseHTTPMiddleware, RequestResponseEndpoint
from starlette.requests import Request
from starlette.responses import JSONResponse, Response

from orchestrator.state_machine.transitions import ApiKeyScope

logger = structlog.get_logger(__name__)

# Paths that do not require authentication.
PUBLIC_PATHS: frozenset[str] = frozenset({"/health", "/ready", "/metrics", "/docs", "/openapi.json", "/redoc"})

# Scope hierarchy: which scopes are allowed for each HTTP method category.
# admin: full access
# operator: task CRUD, approvals, worker management
# bot: task creation, status queries
# readonly: GET endpoints only
_WRITE_METHODS = frozenset({"POST", "PUT", "PATCH", "DELETE"})

# Scope permissions: maps scope to allowed HTTP methods.
SCOPE_ALLOWED_METHODS: dict[ApiKeyScope, frozenset[str]] = {
    ApiKeyScope.ADMIN: frozenset({"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}),
    ApiKeyScope.OPERATOR: frozenset({"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}),
    ApiKeyScope.BOT: frozenset({"GET", "POST", "HEAD", "OPTIONS"}),
    ApiKeyScope.READONLY: frozenset({"GET", "HEAD", "OPTIONS"}),
}

# Paths restricted to specific scopes (prefix matching).
# Admin-only paths.
ADMIN_ONLY_PREFIXES: tuple[str, ...] = (
    "/config/",
    "/audit",
)


@dataclass
class AuthenticatedKey:
    """Represents a successfully authenticated API key."""

    key_id: str
    name: str
    scope: ApiKeyScope


@dataclass
class BruteForceTracker:
    """In-memory tracker for brute-force detection per source IP.

    Tracks failed authentication attempts within a sliding window.
    Blocks sources that exceed the threshold for a configurable duration.
    """

    max_attempts: int = 10
    window_seconds: int = 60
    block_duration_seconds: int = 300

    # source_ip -> list of failure timestamps
    _failures: dict[str, list[float]] = field(default_factory=dict)
    # source_ip -> block expiry timestamp
    _blocked: dict[str, float] = field(default_factory=dict)

    def is_blocked(self, source: str) -> bool:
        """Check if a source is currently blocked."""
        expiry = self._blocked.get(source)
        if expiry is None:
            return False
        if time.time() >= expiry:
            # Block expired, clean up.
            del self._blocked[source]
            self._failures.pop(source, None)
            return False
        return True

    def record_failure(self, source: str) -> bool:
        """Record a failed attempt. Returns True if source is now blocked."""
        now = time.time()
        failures = self._failures.setdefault(source, [])

        # Prune old entries outside the window.
        cutoff = now - self.window_seconds
        failures[:] = [t for t in failures if t > cutoff]

        failures.append(now)

        if len(failures) >= self.max_attempts:
            self._blocked[source] = now + self.block_duration_seconds
            logger.warning(
                "brute_force_detected",
                source=source,
                attempts=len(failures),
                block_duration=self.block_duration_seconds,
            )
            return True
        return False

    def reset(self, source: str) -> None:
        """Reset failure tracking for a source (on successful auth)."""
        self._failures.pop(source, None)


def _hash_key(raw_key: str) -> str:
    """Compute SHA-256 hash of a raw API key for database lookup."""
    return hashlib.sha256(raw_key.encode()).hexdigest()


def _get_client_ip(request: Request) -> str:
    """Extract client IP from request, considering X-Forwarded-For."""
    forwarded = request.headers.get("X-Forwarded-For")
    if forwarded:
        # First IP in the chain is the original client.
        return forwarded.split(",")[0].strip()
    if request.client:
        return request.client.host
    return "unknown"


def _is_public_path(path: str) -> bool:
    """Check if the request path is public (no auth required)."""
    # Exact match or prefix match for docs paths.
    if path in PUBLIC_PATHS:
        return True
    # Allow /docs/* and /redoc/* sub-paths.
    if path.startswith("/docs") or path.startswith("/redoc"):
        return True
    return False


def _check_scope(scope: ApiKeyScope, method: str, path: str) -> str | None:
    """Check if the API key scope allows the requested operation.

    Returns None if allowed, or an error message if forbidden.
    """
    # Admin-only paths.
    for prefix in ADMIN_ONLY_PREFIXES:
        if path.startswith(prefix) and scope != ApiKeyScope.ADMIN:
            return f"Path '{path}' requires 'admin' scope, got '{scope.value}'"

    # Method-based scope check.
    allowed_methods = SCOPE_ALLOWED_METHODS.get(scope, frozenset())
    if method not in allowed_methods:
        return (
            f"Method '{method}' not allowed for scope '{scope.value}'. "
            f"Required scope: 'operator' or 'admin'"
        )

    return None


class AuthMiddleware(BaseHTTPMiddleware):
    """Bearer token authentication middleware with brute-force protection.

    Requires a `get_key_by_hash` async callable that looks up an API key
    by its SHA-256 hash and returns key metadata or None.

    Args:
        app: The ASGI application.
        get_key_by_hash: Async function (hash: str) -> dict | None.
            Expected dict keys: id, name, scope (ApiKeyScope value string).
        brute_force_tracker: Optional BruteForceTracker instance.
            If not provided, a default one is created from settings.
    """

    def __init__(
        self,
        app,
        get_key_by_hash: Callable[[str], object] | None = None,
        brute_force_tracker: BruteForceTracker | None = None,
        max_attempts: int = 10,
        window_seconds: int = 60,
        block_duration_seconds: int = 300,
    ) -> None:
        super().__init__(app)
        self._get_key_by_hash = get_key_by_hash
        self._brute_force = brute_force_tracker or BruteForceTracker(
            max_attempts=max_attempts,
            window_seconds=window_seconds,
            block_duration_seconds=block_duration_seconds,
        )

    async def dispatch(
        self, request: Request, call_next: RequestResponseEndpoint
    ) -> Response:
        # Skip auth for public paths.
        if _is_public_path(request.url.path):
            return await call_next(request)

        client_ip = _get_client_ip(request)

        # Check brute-force block.
        if self._brute_force.is_blocked(client_ip):
            logger.warning(
                "auth_blocked_source",
                source=client_ip,
                path=request.url.path,
            )
            return JSONResponse(
                status_code=403,
                content={
                    "error_code": "source_blocked",
                    "message": "Too many failed authentication attempts. Try again later.",
                },
            )

        # Extract Bearer token.
        auth_header = request.headers.get("Authorization")
        if not auth_header or not auth_header.startswith("Bearer "):
            self._brute_force.record_failure(client_ip)
            logger.info(
                "auth_missing_token",
                source=client_ip,
                path=request.url.path,
            )
            return JSONResponse(
                status_code=401,
                content={
                    "error_code": "missing_token",
                    "message": "Authorization header with Bearer token is required.",
                },
            )

        raw_token = auth_header[7:]  # Strip "Bearer " prefix.
        if not raw_token:
            self._brute_force.record_failure(client_ip)
            return JSONResponse(
                status_code=401,
                content={
                    "error_code": "empty_token",
                    "message": "Bearer token cannot be empty.",
                },
            )

        # Look up key by hash.
        key_hash = _hash_key(raw_token)

        if self._get_key_by_hash is None:
            # No key lookup configured — reject all.
            return JSONResponse(
                status_code=500,
                content={
                    "error_code": "auth_not_configured",
                    "message": "Authentication backend not configured.",
                },
            )

        key_record = await self._get_key_by_hash(key_hash)

        if key_record is None:
            self._brute_force.record_failure(client_ip)
            logger.info(
                "auth_invalid_key",
                source=client_ip,
                path=request.url.path,
            )
            return JSONResponse(
                status_code=401,
                content={
                    "error_code": "invalid_key",
                    "message": "Invalid or revoked API key.",
                },
            )

        # Successful authentication — reset brute-force counter.
        self._brute_force.reset(client_ip)

        # Parse scope.
        scope = ApiKeyScope(key_record["scope"])

        # Check scope permissions.
        scope_error = _check_scope(scope, request.method, request.url.path)
        if scope_error:
            logger.warning(
                "auth_scope_denied",
                source=client_ip,
                key_name=key_record["name"],
                scope=scope.value,
                method=request.method,
                path=request.url.path,
            )
            return JSONResponse(
                status_code=403,
                content={
                    "error_code": "insufficient_scope",
                    "message": scope_error,
                },
            )

        # Attach authenticated key info to request state.
        request.state.api_key = AuthenticatedKey(
            key_id=key_record["id"],
            name=key_record["name"],
            scope=scope,
        )

        return await call_next(request)
