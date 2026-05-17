"""Repository for persisted artifacts emitted by tool invocations."""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import Artifact


class ArtifactRepository:
    """Persistence helpers for artifacts."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        *,
        task_id: uuid.UUID | None,
        invocation_id: uuid.UUID | None,
        artifact_type: str,
        title: str | None,
        uri: str | None,
        media_type: str | None,
        content_text: str | None,
        metadata: dict | None,
    ) -> Artifact:
        artifact = Artifact(
            task_id=task_id,
            invocation_id=invocation_id,
            artifact_type=artifact_type,
            title=title,
            uri=uri,
            media_type=media_type,
            content_text=content_text,
            metadata_=metadata or {},
        )
        self._session.add(artifact)
        await self._session.flush()
        return artifact

    async def list_by_task(self, task_id: uuid.UUID) -> list[Artifact]:
        stmt = (
            select(Artifact)
            .where(Artifact.task_id == task_id)
            .order_by(Artifact.created_at.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def list_by_invocation(self, invocation_id: uuid.UUID) -> list[Artifact]:
        stmt = (
            select(Artifact)
            .where(Artifact.invocation_id == invocation_id)
            .order_by(Artifact.created_at.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def list_by_ids(self, artifact_ids: list[uuid.UUID]) -> list[Artifact]:
        if not artifact_ids:
            return []
        stmt = (
            select(Artifact)
            .where(Artifact.id.in_(artifact_ids))
            .order_by(Artifact.created_at.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
