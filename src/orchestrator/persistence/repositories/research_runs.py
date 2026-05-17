"""Repository for persisted research runs."""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import ResearchRun


class ResearchRunRepository:
    """Persistence helpers for research runs."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        *,
        task_id: uuid.UUID | None,
        entrypoint: str,
        query: str,
        intent_type: str,
        selected_capabilities: list[str],
        final_answer: str,
        confidence: float | None,
        source_artifact_ids: list[str],
        tool_invocation_ids: list[str],
        metadata: dict | None = None,
    ) -> ResearchRun:
        record = ResearchRun(
            task_id=task_id,
            entrypoint=entrypoint,
            query=query,
            intent_type=intent_type,
            selected_capabilities=selected_capabilities,
            final_answer=final_answer,
            confidence=confidence,
            source_artifact_ids=source_artifact_ids,
            tool_invocation_ids=tool_invocation_ids,
            metadata_=metadata or {},
        )
        self._session.add(record)
        await self._session.flush()
        return record

    async def get_by_id(self, research_run_id: uuid.UUID) -> ResearchRun | None:
        return await self._session.get(ResearchRun, research_run_id)

    async def list_recent(self, limit: int = 50) -> list[ResearchRun]:
        stmt = select(ResearchRun).order_by(ResearchRun.created_at.desc()).limit(limit)
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
