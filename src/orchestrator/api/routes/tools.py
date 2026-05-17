"""API routes for Capability Layer v1."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, HTTPException

from orchestrator.api.schemas.common import ErrorResponse
from orchestrator.api.schemas.tools import (
    ArtifactResponse,
    CapabilityListResponse,
    TaskSourcesResponse,
    ToolDocumentReadRequest,
    ToolExecutionResponse,
    ToolFetchRequest,
    ToolImageAnalyzeRequest,
    ToolInvocationResponse,
    ToolSearchRequest,
)
from orchestrator.capabilities.service import CapabilityService

router = APIRouter(tags=["tools"])


def _get_capability_service() -> CapabilityService:
    raise NotImplementedError("CapabilityService dependency not configured")


def _to_response(record, artifacts: list[dict]) -> ToolExecutionResponse:
    return ToolExecutionResponse(
        invocation=ToolInvocationResponse(
            id=record.id,
            task_id=record.task_id,
            entrypoint=record.entrypoint,
            capability=record.capability,
            status=record.status,
            duration_ms=record.duration_ms,
            summary=str(record.output_payload.get("summary") or record.output_payload.get("content_text") or record.output_payload.get("query") or ""),
            output=record.output_payload,
            source_refs=record.source_refs,
            artifact_ids=record.artifact_ids,
            error_message=record.error_message,
            created_at=record.created_at,
        ),
        artifacts=[ArtifactResponse(**artifact) for artifact in artifacts],
    )


@router.get("/capabilities", response_model=CapabilityListResponse)
async def list_capabilities(
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> CapabilityListResponse:
    return CapabilityListResponse(items=capability_service.list_capabilities())


@router.post("/tools/web/search", response_model=ToolExecutionResponse)
async def search_web(
    body: ToolSearchRequest,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> ToolExecutionResponse:
    record = await capability_service.invoke(
        "web.search",
        {"query": body.query},
        task_id=str(body.task_id) if body.task_id else None,
        entrypoint="api",
        allowed_capabilities=body.allowed_capabilities,
    )
    artifacts = await capability_service.list_invocation_artifacts(record.id)
    return _to_response(record, artifacts)


@router.post("/tools/web/fetch", response_model=ToolExecutionResponse)
async def fetch_web(
    body: ToolFetchRequest,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> ToolExecutionResponse:
    record = await capability_service.invoke(
        "web.fetch",
        {"url": body.url},
        task_id=str(body.task_id) if body.task_id else None,
        entrypoint="api",
        allowed_capabilities=body.allowed_capabilities,
    )
    artifacts = await capability_service.list_invocation_artifacts(record.id)
    return _to_response(record, artifacts)


@router.post("/tools/documents/read", response_model=ToolExecutionResponse)
async def read_document(
    body: ToolDocumentReadRequest,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> ToolExecutionResponse:
    record = await capability_service.invoke(
        "document.read",
        {"location": body.location},
        task_id=str(body.task_id) if body.task_id else None,
        entrypoint="api",
        allowed_capabilities=body.allowed_capabilities,
    )
    artifacts = await capability_service.list_invocation_artifacts(record.id)
    return _to_response(record, artifacts)


@router.post("/tools/images/analyze", response_model=ToolExecutionResponse)
async def analyze_image(
    body: ToolImageAnalyzeRequest,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> ToolExecutionResponse:
    record = await capability_service.invoke(
        "image.analyze",
        {"location": body.location},
        task_id=str(body.task_id) if body.task_id else None,
        entrypoint="api",
        allowed_capabilities=body.allowed_capabilities,
    )
    artifacts = await capability_service.list_invocation_artifacts(record.id)
    return _to_response(record, artifacts)


@router.get(
    "/tools/invocations/{invocation_id}",
    response_model=ToolExecutionResponse,
    responses={404: {"model": ErrorResponse}},
)
async def get_tool_invocation(
    invocation_id: uuid.UUID,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> ToolExecutionResponse:
    record = await capability_service.get_invocation(invocation_id)
    if record is None:
        raise HTTPException(status_code=404, detail=f"Invocation {invocation_id} not found")
    artifacts = await capability_service.list_invocation_artifacts(invocation_id)
    return _to_response(record, artifacts)


@router.get(
    "/tasks/{task_id}/sources",
    response_model=TaskSourcesResponse,
    responses={404: {"model": ErrorResponse}},
)
async def get_task_sources(
    task_id: uuid.UUID,
    capability_service: CapabilityService = Depends(_get_capability_service),
) -> TaskSourcesResponse:
    items = await capability_service.list_task_sources(task_id)
    return TaskSourcesResponse(
        items=[ArtifactResponse(**item) for item in items],
        total=len(items),
    )
