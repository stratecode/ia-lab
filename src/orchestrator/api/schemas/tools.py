"""Schemas for capability and tool invocation APIs."""

from __future__ import annotations

from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.capabilities.interfaces import SourceRef


class ToolSearchRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=500)
    task_id: UUID | None = None
    allowed_capabilities: list[str] | None = None


class ToolFetchRequest(BaseModel):
    url: str = Field(..., min_length=1, max_length=2048)
    task_id: UUID | None = None
    allowed_capabilities: list[str] | None = None


class ToolDocumentReadRequest(BaseModel):
    location: str = Field(..., min_length=1, max_length=4096)
    task_id: UUID | None = None
    allowed_capabilities: list[str] | None = None


class ToolImageAnalyzeRequest(BaseModel):
    location: str = Field(..., min_length=1, max_length=4096)
    task_id: UUID | None = None
    allowed_capabilities: list[str] | None = None


class ArtifactResponse(BaseModel):
    id: str | None = None
    artifact_type: str
    title: str | None = None
    uri: str | None = None
    media_type: str | None = None
    content_text: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime | None = None


class ToolInvocationResponse(BaseModel):
    id: UUID
    task_id: UUID | None = None
    entrypoint: str
    capability: str
    status: str
    duration_ms: int
    summary: str | None = None
    output: dict[str, Any] = Field(default_factory=dict)
    source_refs: list[SourceRef] = Field(default_factory=list)
    artifact_ids: list[str] = Field(default_factory=list)
    error_message: str | None = None
    created_at: datetime


class ToolExecutionResponse(BaseModel):
    invocation: ToolInvocationResponse
    artifacts: list[ArtifactResponse] = Field(default_factory=list)


class TaskSourcesResponse(BaseModel):
    items: list[ArtifactResponse] = Field(default_factory=list)
    total: int


class CapabilityListResponse(BaseModel):
    items: list[dict[str, str]]
