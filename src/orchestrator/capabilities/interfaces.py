"""Capability Layer v1 interfaces and payload schemas."""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any, Literal
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.state_machine.transitions import AgentType


CapabilityName = Literal[
    "web.search",
    "web.fetch",
    "document.read",
    "image.analyze",
]


class SourceRef(BaseModel):
    title: str | None = None
    uri: str | None = None
    snippet: str | None = None
    fetched_at: datetime | None = None
    kind: str = "source"
    metadata: dict[str, Any] = Field(default_factory=dict)


class ArtifactPayload(BaseModel):
    artifact_type: str
    title: str | None = None
    uri: str | None = None
    media_type: str | None = None
    content_text: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)


class CapabilityExecutionResult(BaseModel):
    capability: CapabilityName
    status: Literal["success", "error", "blocked", "timeout"]
    summary: str
    output: dict[str, Any] = Field(default_factory=dict)
    source_refs: list[SourceRef] = Field(default_factory=list)
    artifacts: list[ArtifactPayload] = Field(default_factory=list)
    error_message: str | None = None
    duration_ms: int = Field(default=0, ge=0)
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))


class CapabilityInvocationRecord(BaseModel):
    id: UUID
    task_id: UUID | None = None
    agent_type: AgentType | None = None
    entrypoint: str
    capability: CapabilityName
    input_payload: dict[str, Any] = Field(default_factory=dict)
    output_payload: dict[str, Any] = Field(default_factory=dict)
    status: str
    duration_ms: int
    source_refs: list[SourceRef] = Field(default_factory=list)
    artifact_ids: list[str] = Field(default_factory=list)
    error_message: str | None = None
    created_at: datetime


class CapabilityInvocationContext(BaseModel):
    task_id: UUID | None = None
    agent_type: AgentType | None = None
    entrypoint: str = "api"
    allowed_capabilities: list[str] | None = None


class PlannerContextBundle(BaseModel):
    invocations: list[CapabilityInvocationRecord] = Field(default_factory=list)
    context_blocks: list[str] = Field(default_factory=list)
