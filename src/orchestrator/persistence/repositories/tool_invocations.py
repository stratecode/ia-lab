"""Repository for capability/tool invocation records."""

from __future__ import annotations

import uuid

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import ToolInvocation
from orchestrator.state_machine.transitions import AgentType


class ToolInvocationRepository:
    """Persistence helpers for tool invocation audit records."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def create(
        self,
        *,
        task_id: uuid.UUID | None,
        agent_type: AgentType | None,
        entrypoint: str,
        capability: str,
        input_payload: dict | None,
        output_payload: dict | None,
        status: str,
        duration_ms: int,
        source_refs: list[dict] | None,
        artifact_ids: list[str] | None,
        error_message: str | None = None,
    ) -> ToolInvocation:
        invocation = ToolInvocation(
            task_id=task_id,
            agent_type=agent_type,
            entrypoint=entrypoint,
            capability=capability,
            input_payload=input_payload or {},
            output_payload=output_payload or {},
            status=status,
            duration_ms=duration_ms,
            source_refs=source_refs or [],
            artifact_ids=artifact_ids or [],
            error_message=error_message,
        )
        self._session.add(invocation)
        await self._session.flush()
        return invocation

    async def get_by_id(self, invocation_id: uuid.UUID) -> ToolInvocation | None:
        return await self._session.get(ToolInvocation, invocation_id)

    async def list_by_task(self, task_id: uuid.UUID) -> list[ToolInvocation]:
        stmt = (
            select(ToolInvocation)
            .where(ToolInvocation.task_id == task_id)
            .order_by(ToolInvocation.created_at.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
