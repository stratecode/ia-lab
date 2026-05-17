"""Repository for research evaluation runs."""

from __future__ import annotations

import uuid

from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import EvaluationRun


class EvaluationRunRepository:
    """Persistence helpers for evaluation runs."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        *,
        research_run_id: uuid.UUID,
        reference_provider: str,
        reference_model: str | None,
        reference_answer: str | None,
        judge_model: str | None,
        judge_verdict: dict | None = None,
        judge_scores: dict | None = None,
        winner: str | None = None,
    ) -> EvaluationRun:
        record = EvaluationRun(
            research_run_id=research_run_id,
            reference_provider=reference_provider,
            reference_model=reference_model,
            reference_answer=reference_answer,
            judge_model=judge_model,
            judge_verdict=judge_verdict or {},
            judge_scores=judge_scores or {},
            winner=winner,
        )
        self._session.add(record)
        await self._session.flush()
        return record

    async def get_by_id(self, evaluation_run_id: uuid.UUID) -> EvaluationRun | None:
        return await self._session.get(EvaluationRun, evaluation_run_id)
