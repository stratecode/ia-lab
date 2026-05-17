from __future__ import annotations

import uuid
from datetime import UTC, datetime

from fastapi import FastAPI
from fastapi.testclient import TestClient

from orchestrator.api.routes.openai_tools import (
    _get_capability_settings,
    _get_research_service as _get_openai_research_service,
    router as openai_router,
)
from orchestrator.api.routes.research import _get_research_service, router as research_router
from orchestrator.api.routes.tools import _get_capability_service, router as tools_router
from orchestrator.capabilities.interfaces import CapabilityInvocationRecord, SourceRef
from orchestrator.config import CapabilitySettings
from orchestrator.research.interfaces import ResearchQueryResult, ResearchRunRecord


class _FakeCapabilityService:
    def __init__(self) -> None:
        self.invocation_id = uuid.uuid4()

    def list_capabilities(self) -> list[dict[str, str]]:
        return [{"name": "web.search", "description": "search"}]

    async def invoke(self, capability: str, payload: dict, **kwargs) -> CapabilityInvocationRecord:
        summary = "Example Result"
        output_payload = {"summary": summary, "results": [{"title": "Example", "url": "https://example.com"}]}
        if capability == "web.fetch":
            output_payload = {"summary": "Fetched page", "content_text": "Fetched body"}
        return CapabilityInvocationRecord(
            id=self.invocation_id,
            task_id=kwargs.get("task_id"),
            agent_type=None,
            entrypoint=kwargs.get("entrypoint", "api"),
            capability=capability,  # type: ignore[arg-type]
            input_payload=payload,
            output_payload=output_payload,
            status="success",
            duration_ms=12,
            source_refs=[
                SourceRef(title="Example", uri="https://example.com", snippet="Snippet")
            ],
            artifact_ids=["artifact-1"],
            error_message=None,
            created_at=datetime.now(UTC),
        )

    async def list_invocation_artifacts(self, invocation_id: uuid.UUID) -> list[dict]:
        return [
            {
                "id": "artifact-1",
                "artifact_type": "search_results",
                "title": "Example",
                "uri": "https://example.com",
                "media_type": "application/json",
                "content_text": "Example content",
                "metadata": {},
                "created_at": datetime.now(UTC),
            }
        ]

    async def get_invocation(self, invocation_id: uuid.UUID) -> CapabilityInvocationRecord | None:
        return await self.invoke("web.search", {"query": "example"})

    async def list_task_sources(self, task_id: uuid.UUID) -> list[dict]:
        return await self.list_invocation_artifacts(self.invocation_id)


class _FakeResearchService:
    async def query(
        self,
        query: str,
        *,
        task_id: str | None = None,
        entrypoint: str = "api",
        allowed_capabilities: list[str] | None = None,
        mode_hint: str | None = None,
        evaluate_against_reference: bool = False,
    ) -> ResearchQueryResult:
        return ResearchQueryResult(
            research_run=ResearchRunRecord(
                id=uuid.uuid4(),
                task_id=uuid.UUID(task_id) if task_id else None,
                entrypoint=entrypoint,
                query=query,
                intent_type="url_summary" if mode_hint == "url_summary" or "https://" in query else "search_answer",
                selected_capabilities=allowed_capabilities or ["web.search", "web.fetch"],
                final_answer="Fetched body with direct answer",
                confidence=0.82,
                source_artifact_ids=["artifact-1"],
                tool_invocation_ids=[str(uuid.uuid4())],
                metadata={"source_count": 1},
                created_at=datetime.now(UTC),
            ),
            answer="Fetched body with direct answer",
            confidence=0.82,
            sources=[SourceRef(title="Example", uri="https://example.com", snippet="Snippet")],
            tool_invocation_ids=[str(uuid.uuid4())],
        )

    async def get_research_run(self, research_run_id: uuid.UUID) -> ResearchRunRecord | None:
        return (
            await self.query("example", entrypoint="api")
        ).research_run


def _build_client() -> TestClient:
    app = FastAPI()
    app.include_router(tools_router)
    app.include_router(research_router)
    app.include_router(openai_router)
    fake_service = _FakeCapabilityService()
    fake_research = _FakeResearchService()
    app.dependency_overrides[_get_capability_service] = lambda: fake_service
    app.dependency_overrides[_get_research_service] = lambda: fake_research
    app.dependency_overrides[_get_openai_research_service] = lambda: fake_research
    app.dependency_overrides[_get_capability_settings] = lambda: CapabilitySettings()
    return TestClient(app)


def test_tools_search_endpoint_returns_invocation() -> None:
    client = _build_client()
    response = client.post("/tools/web/search", json={"query": "example"})

    assert response.status_code == 200
    payload = response.json()
    assert payload["invocation"]["capability"] == "web.search"
    assert payload["artifacts"][0]["uri"] == "https://example.com"


def test_openai_chat_endpoint_uses_fetch_for_url() -> None:
    client = _build_client()
    response = client.post(
        "/v1/chat/completions",
        json={
            "model": "orchestrator-tools",
            "messages": [{"role": "user", "content": "resume https://example.com"}],
        },
    )

    assert response.status_code == 200
    payload = response.json()
    content = payload["choices"][0]["message"]["content"]
    assert "Fetched body with direct answer" in content
    assert "Confidence: 0.82" in content
    assert "https://example.com" in content


def test_research_query_endpoint_returns_sources() -> None:
    client = _build_client()
    response = client.post("/research/query", json={"query": "resume https://example.com"})

    assert response.status_code == 200
    payload = response.json()
    assert payload["answer"] == "Fetched body with direct answer"
    assert payload["sources"][0]["uri"] == "https://example.com"
