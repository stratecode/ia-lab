"""Capability Layer application service with persistence and policy checks."""

from __future__ import annotations

import uuid
from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.capabilities.interfaces import (
    CapabilityExecutionResult,
    CapabilityInvocationContext,
    CapabilityInvocationRecord,
    PlannerContextBundle,
)
from orchestrator.capabilities.router import CapabilityRouter
from orchestrator.config import CapabilitySettings
from orchestrator.observability.metrics import (
    record_artifact_created,
    record_capability_invocation,
)
from orchestrator.persistence.repositories.artifacts import ArtifactRepository
from orchestrator.persistence.repositories.tool_invocations import (
    ToolInvocationRepository,
)
from orchestrator.state_machine.transitions import AgentType


class CapabilityService:
    """Invokes capabilities, persists artifacts, and exposes query helpers."""

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        router: CapabilityRouter,
        settings: CapabilitySettings,
    ) -> None:
        self._session_factory = session_factory
        self._router = router
        self._settings = settings

    def list_capabilities(self) -> list[dict[str, str]]:
        return self._router.list_capabilities()

    async def invoke(
        self,
        capability: str,
        payload: dict[str, Any],
        *,
        task_id: str | None = None,
        agent_type: AgentType | None = None,
        entrypoint: str = "api",
        allowed_capabilities: list[str] | None = None,
    ) -> CapabilityInvocationRecord:
        task_uuid = uuid.UUID(task_id) if task_id else None
        result = await self._router.execute(
            capability,  # type: ignore[arg-type]
            payload,
            CapabilityInvocationContext(
                task_id=task_uuid,
                agent_type=agent_type,
                entrypoint=entrypoint,
                allowed_capabilities=allowed_capabilities,
            ),
        )
        return await self._store_invocation(
            result=result,
            payload=payload,
            task_id=task_uuid,
            agent_type=agent_type,
            entrypoint=entrypoint,
        )

    async def get_invocation(self, invocation_id: uuid.UUID) -> CapabilityInvocationRecord | None:
        async with self._session_factory() as session:
            repo = ToolInvocationRepository(session)
            item = await repo.get_by_id(invocation_id)
            if item is None:
                return None
        return CapabilityInvocationRecord.model_validate(item, from_attributes=True)

    async def list_invocation_artifacts(self, invocation_id: uuid.UUID) -> list[dict[str, Any]]:
        async with self._session_factory() as session:
            repo = ArtifactRepository(session)
            items = await repo.list_by_invocation(invocation_id)
        return [
            {
                "id": str(item.id),
                "artifact_type": item.artifact_type,
                "title": item.title,
                "uri": item.uri,
                "media_type": item.media_type,
                "content_text": item.content_text,
                "metadata": item.metadata_ or {},
                "created_at": item.created_at,
            }
            for item in items
        ]

    async def list_task_sources(self, task_id: uuid.UUID) -> list[dict[str, Any]]:
        async with self._session_factory() as session:
            repo = ArtifactRepository(session)
            items = await repo.list_by_task(task_id)
        return [
            {
                "id": str(item.id),
                "artifact_type": item.artifact_type,
                "title": item.title,
                "uri": item.uri,
                "media_type": item.media_type,
                "content_text": item.content_text,
                "metadata": item.metadata_ or {},
                "created_at": item.created_at.isoformat() if item.created_at else None,
            }
            for item in items
        ]

    async def build_planner_context(
        self,
        *,
        task_id: str,
        description: str,
        metadata: dict[str, Any],
    ) -> PlannerContextBundle:
        allowed = self._resolve_agent_allowlist(
            agent_type=AgentType.PLANNER,
            requested=metadata.get("allowed_capabilities"),
        )
        invocations: list[CapabilityInvocationRecord] = []
        blocks: list[str] = []
        document_location = (
            metadata.get("document_location")
            or metadata.get("document_path")
            or metadata.get("document_url")
        )

        if "web.search" in allowed:
            search_record = await self.invoke(
                "web.search",
                {"query": description},
                task_id=task_id,
                agent_type=AgentType.PLANNER,
                entrypoint=str(metadata.get("entrypoint") or "planner"),
                allowed_capabilities=allowed,
            )
            invocations.append(search_record)
            top_urls = [
                ref.uri
                for ref in search_record.source_refs
                if ref.uri
            ][:2]
            blocks.append(
                "Search results:\n" + "\n".join(
                    f"- {ref.title or ref.uri}: {ref.snippet or ''}".strip()
                    for ref in search_record.source_refs[:5]
                )
            )
            if "web.fetch" in allowed:
                for url in top_urls:
                    fetch_record = await self.invoke(
                        "web.fetch",
                        {"url": url},
                        task_id=task_id,
                        agent_type=AgentType.PLANNER,
                        entrypoint=str(metadata.get("entrypoint") or "planner"),
                        allowed_capabilities=allowed,
                    )
                    invocations.append(fetch_record)
                    blocks.append(f"Fetched source {url}:\n{fetch_record.output_payload.get('content_text', '')[:1800]}")

        if document_location and "document.read" in allowed:
            doc_record = await self.invoke(
                "document.read",
                {"location": str(document_location)},
                task_id=task_id,
                agent_type=AgentType.PLANNER,
                entrypoint=str(metadata.get("entrypoint") or "planner"),
                allowed_capabilities=allowed,
            )
            invocations.append(doc_record)
            blocks.append(f"Document context:\n{doc_record.output_payload.get('content_text', '')[:2000]}")

        return PlannerContextBundle(invocations=invocations, context_blocks=blocks)

    def _resolve_agent_allowlist(
        self,
        *,
        agent_type: AgentType,
        requested: list[str] | None,
    ) -> list[str]:
        configured = (
            self._settings.planner_capability_allowlist
            if agent_type == AgentType.PLANNER
            else self._settings.coder_capability_allowlist
        )
        if not requested:
            return configured
        allowed = set(configured)
        return [item for item in requested if item in allowed]

    async def _store_invocation(
        self,
        *,
        result: CapabilityExecutionResult,
        payload: dict[str, Any],
        task_id: uuid.UUID | None,
        agent_type: AgentType | None,
        entrypoint: str,
    ) -> CapabilityInvocationRecord:
        async with self._session_factory() as session:
            async with session.begin():
                artifact_repo = ArtifactRepository(session)
                invocation_repo = ToolInvocationRepository(session)
                artifact_ids: list[str] = []
                artifact_records = []
                for artifact in result.artifacts:
                    record = await artifact_repo.create(
                        task_id=task_id,
                        invocation_id=None,
                        artifact_type=artifact.artifact_type,
                        title=artifact.title,
                        uri=artifact.uri,
                        media_type=artifact.media_type,
                        content_text=(artifact.content_text or "")[: self._settings.max_artifact_text_chars] or None,
                        metadata=artifact.metadata,
                    )
                    artifact_ids.append(str(record.id))
                    artifact_records.append(record)
                    record_artifact_created(artifact.artifact_type, entrypoint)

                invocation = await invocation_repo.create(
                    task_id=task_id,
                    agent_type=agent_type,
                    entrypoint=entrypoint,
                    capability=result.capability,
                    input_payload=payload,
                    output_payload={**result.output, "summary": result.summary},
                    status=result.status,
                    duration_ms=result.duration_ms,
                    source_refs=[item.model_dump(mode="json") for item in result.source_refs],
                    artifact_ids=artifact_ids,
                    error_message=result.error_message,
                )

                for record in artifact_records:
                    record.invocation_id = invocation.id

            await session.refresh(invocation)

        record_capability_invocation(
            result.capability,
            entrypoint,
            result.status,
            result.duration_ms / 1000.0,
        )

        return CapabilityInvocationRecord(
            id=invocation.id,
            task_id=invocation.task_id,
            agent_type=AgentType(invocation.agent_type) if invocation.agent_type else None,
            entrypoint=invocation.entrypoint,
            capability=invocation.capability,  # type: ignore[arg-type]
            input_payload=invocation.input_payload or {},
            output_payload=invocation.output_payload or {},
            status=invocation.status,
            duration_ms=invocation.duration_ms,
            source_refs=invocation.source_refs or [],
            artifact_ids=invocation.artifact_ids or [],
            error_message=invocation.error_message,
            created_at=invocation.created_at,
        )
