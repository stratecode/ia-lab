"""Audit log repository — insertion and querying of audit records."""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import AuditLog


class AuditRepository:
    """Data access layer for AuditLog entities."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        actor: str,
        action: str,
        resource_type: str,
        resource_id: str | None = None,
        details: dict | None = None,
        correlation_id: uuid.UUID | None = None,
    ) -> AuditLog:
        """Insert a new audit log entry.

        Args:
            actor: Identity of who performed the action.
            action: The action performed (e.g. "state_transition", "approval_resolved").
            resource_type: Type of resource affected (e.g. "task", "approval").
            resource_id: Optional identifier of the affected resource.
            details: Optional JSON details about the action.
            correlation_id: Correlation ID for tracing related events.

        Returns:
            The newly created AuditLog instance.
        """
        entry = AuditLog(
            actor=actor,
            action=action,
            resource_type=resource_type,
            resource_id=resource_id,
            details=details or {},
            correlation_id=correlation_id or uuid.uuid4(),
        )
        self._session.add(entry)
        await self._session.flush()
        return entry

    async def list_by_resource(
        self,
        resource_type: str,
        resource_id: str,
        limit: int = 50,
        offset: int = 0,
    ) -> list[AuditLog]:
        """List audit entries for a specific resource.

        Args:
            resource_type: The resource type to filter by.
            resource_id: The resource ID to filter by.
            limit: Maximum number of results.
            offset: Number of results to skip.

        Returns:
            List of matching AuditLog entries ordered by timestamp descending.
        """
        stmt = (
            select(AuditLog)
            .where(
                AuditLog.resource_type == resource_type,
                AuditLog.resource_id == resource_id,
            )
            .order_by(AuditLog.timestamp.desc())
            .limit(limit)
            .offset(offset)
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def list_by_actor(
        self,
        actor: str,
        limit: int = 50,
        offset: int = 0,
    ) -> list[AuditLog]:
        """List audit entries by actor.

        Args:
            actor: The actor identity to filter by.
            limit: Maximum number of results.
            offset: Number of results to skip.

        Returns:
            List of matching AuditLog entries ordered by timestamp descending.
        """
        stmt = (
            select(AuditLog)
            .where(AuditLog.actor == actor)
            .order_by(AuditLog.timestamp.desc())
            .limit(limit)
            .offset(offset)
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def list_by_correlation_id(
        self,
        correlation_id: uuid.UUID,
    ) -> list[AuditLog]:
        """List all audit entries sharing a correlation ID.

        Args:
            correlation_id: The correlation ID to filter by.

        Returns:
            List of matching AuditLog entries ordered by timestamp ascending.
        """
        stmt = (
            select(AuditLog)
            .where(AuditLog.correlation_id == correlation_id)
            .order_by(AuditLog.timestamp.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
