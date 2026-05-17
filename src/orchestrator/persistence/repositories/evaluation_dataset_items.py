"""Repository for evaluation dataset items."""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import EvaluationDatasetItem


class EvaluationDatasetItemRepository:
    """Persistence helpers for evaluation dataset items."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        *,
        research_run_id: uuid.UUID,
        evaluation_run_id: uuid.UUID | None,
        query: str,
        orchestrator_answer: str,
        reference_answer: str,
        sources: list[dict],
        scores: dict | None,
        winner: str | None,
        metadata: dict | None = None,
    ) -> EvaluationDatasetItem:
        record = EvaluationDatasetItem(
            research_run_id=research_run_id,
            evaluation_run_id=evaluation_run_id,
            query=query,
            orchestrator_answer=orchestrator_answer,
            reference_answer=reference_answer,
            sources=sources,
            scores=scores or {},
            winner=winner,
            metadata_=metadata or {},
        )
        self._session.add(record)
        await self._session.flush()
        return record

    async def list_recent(self, limit: int = 100) -> list[EvaluationDatasetItem]:
        stmt = (
            select(EvaluationDatasetItem)
            .order_by(EvaluationDatasetItem.created_at.desc())
            .limit(limit)
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
