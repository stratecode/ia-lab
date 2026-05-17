"""Secret scope management for task-scoped secret injection.

Implements Requirements 16.1–16.7:
- Secrets are bound to a specific task_id with optional TTL
- Worker ownership is validated before granting access
- All access events (grant, deny, revoke) are logged to audit_log
- Secrets are automatically revoked when a task reaches a terminal state
- Only secret references/tokens are stored in Redis — not actual values

Redis key pattern: secret:{task_id}:{secret_name}
Redis value: JSON token reference (not the actual secret value)
"""

from __future__ import annotations

import json
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone

import redis.asyncio as redis


# ---------------------------------------------------------------------------
# Data Types
# ---------------------------------------------------------------------------

# Default TTL for secrets (seconds) — used when no explicit TTL is provided
DEFAULT_SECRET_TTL: int = 3600

# Redis key prefix for secret bindings
SECRET_KEY_PREFIX: str = "secret"


@dataclass(frozen=True)
class SecretBinding:
    """Represents a secret bound to a task scope."""

    task_id: str
    secret_name: str
    token: str  # Reference token — NOT the actual secret value
    bound_at: float  # Unix timestamp
    ttl_seconds: int
    worker_id: str


@dataclass(frozen=True)
class SecretAccessResult:
    """Result of a secret access attempt."""

    granted: bool
    token: str | None = None
    reason: str | None = None


# ---------------------------------------------------------------------------
# Secret Injector
# ---------------------------------------------------------------------------


class SecretInjector:
    """Manages task-scoped secret bindings with TTL enforcement.

    The injector stores only reference tokens in Redis — actual secret values
    are never persisted in Redis. Workers use the token to retrieve the real
    value from vault/environment at injection time.

    Lifecycle:
        1. bind() — associate a secret reference with a task_id
        2. access() — worker requests secret for its current task
        3. revoke_task() — revoke all secrets when task reaches terminal state
    """

    def __init__(
        self,
        redis_client: redis.Redis,
        audit_log: AuditLogProtocol | None = None,
        task_ownership_checker: TaskOwnershipProtocol | None = None,
    ) -> None:
        """Initialize the secret injector.

        Args:
            redis_client: Async Redis client for secret storage.
            audit_log: Optional audit logger for access events.
            task_ownership_checker: Optional checker to validate worker→task ownership.
        """
        self._redis = redis_client
        self._audit_log = audit_log
        self._ownership_checker = task_ownership_checker

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def bind(
        self,
        task_id: str,
        secret_name: str,
        worker_id: str,
        ttl_seconds: int | None = None,
        correlation_id: uuid.UUID | None = None,
    ) -> SecretBinding:
        """Bind a secret reference to a task scope.

        Creates a token reference in Redis with TTL enforcement.
        The token can later be used by the worker to retrieve the actual
        secret value from vault.

        Args:
            task_id: The task to bind the secret to.
            secret_name: Name/key of the secret being bound.
            worker_id: The worker performing the binding.
            ttl_seconds: Time-to-live in seconds (default: DEFAULT_SECRET_TTL).
            correlation_id: Correlation ID for audit tracing.

        Returns:
            SecretBinding with the generated token reference.
        """
        effective_ttl = ttl_seconds if ttl_seconds is not None else DEFAULT_SECRET_TTL
        token = str(uuid.uuid4())
        bound_at = time.time()

        binding = SecretBinding(
            task_id=task_id,
            secret_name=secret_name,
            token=token,
            bound_at=bound_at,
            ttl_seconds=effective_ttl,
            worker_id=worker_id,
        )

        # Store reference in Redis with TTL
        key = self._build_key(task_id, secret_name)
        value = json.dumps({
            "token": token,
            "worker_id": worker_id,
            "bound_at": bound_at,
            "task_id": task_id,
            "secret_name": secret_name,
        })
        await self._redis.set(key, value, ex=effective_ttl)

        # Audit: secret bound
        await self._log_audit(
            actor=f"worker:{worker_id}",
            action="secret_bound",
            resource_type="secret",
            resource_id=secret_name,
            details={
                "task_id": task_id,
                "ttl_seconds": effective_ttl,
                "token": token,
            },
            correlation_id=correlation_id,
        )

        return binding

    async def access(
        self,
        task_id: str,
        secret_name: str,
        worker_id: str,
        correlation_id: uuid.UUID | None = None,
    ) -> SecretAccessResult:
        """Request access to a secret for a task.

        Validates:
        1. The secret exists and hasn't expired (TTL)
        2. The requesting worker owns the task

        Args:
            task_id: The task requesting the secret.
            secret_name: Name of the secret to access.
            worker_id: The worker making the request.
            correlation_id: Correlation ID for audit tracing.

        Returns:
            SecretAccessResult indicating whether access was granted.
        """
        # Check if secret binding exists in Redis (TTL enforced by Redis)
        key = self._build_key(task_id, secret_name)
        raw = await self._redis.get(key)

        if raw is None:
            # Secret not found — either never bound or expired
            await self._log_audit(
                actor=f"worker:{worker_id}",
                action="secret_access_denied",
                resource_type="secret",
                resource_id=secret_name,
                details={
                    "task_id": task_id,
                    "reason": "secret_not_found_or_expired",
                },
                correlation_id=correlation_id,
            )
            return SecretAccessResult(
                granted=False,
                reason="secret_not_found_or_expired",
            )

        binding_data = json.loads(raw)

        # Validate worker ownership
        if not await self._validate_ownership(task_id, worker_id):
            await self._log_audit(
                actor=f"worker:{worker_id}",
                action="secret_access_denied",
                resource_type="secret",
                resource_id=secret_name,
                details={
                    "task_id": task_id,
                    "reason": "worker_not_owner",
                    "bound_worker": binding_data.get("worker_id"),
                },
                correlation_id=correlation_id,
            )
            return SecretAccessResult(
                granted=False,
                reason="worker_not_owner",
            )

        # Access granted
        token = binding_data["token"]
        await self._log_audit(
            actor=f"worker:{worker_id}",
            action="secret_access_granted",
            resource_type="secret",
            resource_id=secret_name,
            details={
                "task_id": task_id,
                "token": token,
            },
            correlation_id=correlation_id,
        )

        return SecretAccessResult(granted=True, token=token)

    async def revoke_task(
        self,
        task_id: str,
        reason: str = "task_terminal_state",
        correlation_id: uuid.UUID | None = None,
    ) -> int:
        """Revoke all secrets bound to a task.

        Called when a task reaches a terminal state (completed, failed, cancelled).
        Explicitly deletes all secret keys for the task from Redis.

        Args:
            task_id: The task whose secrets should be revoked.
            reason: Reason for revocation (for audit).
            correlation_id: Correlation ID for audit tracing.

        Returns:
            Number of secrets revoked.
        """
        pattern = f"{SECRET_KEY_PREFIX}:{task_id}:*"
        revoked_count = 0

        # Scan for all keys matching the task pattern
        keys_to_delete: list[str] = []
        async for key in self._redis.scan_iter(match=pattern):
            keys_to_delete.append(key)

        if keys_to_delete:
            # Delete all keys atomically via pipeline
            async with self._redis.pipeline(transaction=True) as pipe:
                for key in keys_to_delete:
                    pipe.delete(key)
                await pipe.execute()
            revoked_count = len(keys_to_delete)

        # Audit: secrets revoked
        await self._log_audit(
            actor="system",
            action="secrets_revoked",
            resource_type="task",
            resource_id=task_id,
            details={
                "reason": reason,
                "revoked_count": revoked_count,
                "keys": [k if isinstance(k, str) else k.decode() for k in keys_to_delete],
            },
            correlation_id=correlation_id,
        )

        return revoked_count

    async def is_secret_bound(self, task_id: str, secret_name: str) -> bool:
        """Check if a secret is currently bound to a task (not expired).

        Args:
            task_id: The task to check.
            secret_name: The secret name to check.

        Returns:
            True if the secret binding exists and hasn't expired.
        """
        key = self._build_key(task_id, secret_name)
        return await self._redis.exists(key) > 0

    # ------------------------------------------------------------------
    # Private Helpers
    # ------------------------------------------------------------------

    def _build_key(self, task_id: str, secret_name: str) -> str:
        """Build the Redis key for a secret binding."""
        return f"{SECRET_KEY_PREFIX}:{task_id}:{secret_name}"

    async def _validate_ownership(self, task_id: str, worker_id: str) -> bool:
        """Validate that the worker owns the task.

        If no ownership checker is configured, falls back to checking
        the worker_id stored in the binding itself.
        """
        if self._ownership_checker is not None:
            return await self._ownership_checker.worker_owns_task(worker_id, task_id)

        # Fallback: check if the worker_id matches the binding's worker_id
        # This is a basic check — production should use the ownership checker
        # that queries the task assignment in the database.
        key_pattern = f"{SECRET_KEY_PREFIX}:{task_id}:*"
        async for key in self._redis.scan_iter(match=key_pattern, count=1):
            raw = await self._redis.get(key)
            if raw:
                data = json.loads(raw)
                return data.get("worker_id") == worker_id
        # No bindings found — cannot validate, deny by default
        return False

    async def _log_audit(
        self,
        actor: str,
        action: str,
        resource_type: str,
        resource_id: str | None = None,
        details: dict | None = None,
        correlation_id: uuid.UUID | None = None,
    ) -> None:
        """Log an audit event if an audit logger is configured."""
        if self._audit_log is not None:
            await self._audit_log.log(
                actor=actor,
                action=action,
                resource_type=resource_type,
                resource_id=resource_id,
                details=details or {},
                correlation_id=correlation_id or uuid.uuid4(),
            )


# ---------------------------------------------------------------------------
# Protocol Interfaces (for dependency injection)
# ---------------------------------------------------------------------------


class AuditLogProtocol:
    """Protocol for audit logging — allows decoupling from persistence layer."""

    async def log(
        self,
        actor: str,
        action: str,
        resource_type: str,
        resource_id: str | None = None,
        details: dict | None = None,
        correlation_id: uuid.UUID | None = None,
    ) -> None:
        """Record an audit event."""
        ...


class TaskOwnershipProtocol:
    """Protocol for validating worker→task ownership."""

    async def worker_owns_task(self, worker_id: str, task_id: str) -> bool:
        """Return True if the worker is assigned to the task."""
        ...
