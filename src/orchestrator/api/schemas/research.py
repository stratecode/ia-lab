"""Schemas for research and evaluation APIs."""

from __future__ import annotations

from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.capabilities.interfaces import SourceRef


class ResearchQueryRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=10000)
    task_id: UUID | None = None
    allowed_capabilities: list[str] | None = None
    evaluate_against_reference: bool = False


class ResearchRunResponse(BaseModel):
    id: UUID
    task_id: UUID | None = None
    entrypoint: str
    query: str
    intent_type: str
    selected_capabilities: list[str] = Field(default_factory=list)
    final_answer: str
    confidence: float | None = None
    source_artifact_ids: list[str] = Field(default_factory=list)
    tool_invocation_ids: list[str] = Field(default_factory=list)
    metadata: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime


class EvaluationRunResponse(BaseModel):
    id: UUID
    research_run_id: UUID
    reference_provider: str
    reference_model: str | None = None
    reference_answer: str | None = None
    judge_model: str | None = None
    judge_verdict: dict[str, Any] = Field(default_factory=dict)
    judge_scores: dict[str, Any] = Field(default_factory=dict)
    winner: str | None = None
    created_at: datetime


class EvaluationDatasetItemResponse(BaseModel):
    id: UUID
    research_run_id: UUID
    evaluation_run_id: UUID | None = None
    query: str
    orchestrator_answer: str
    reference_answer: str
    sources: list[dict[str, Any]] = Field(default_factory=list)
    scores: dict[str, Any] = Field(default_factory=dict)
    winner: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime


class ResearchQueryResponse(BaseModel):
    research_run: ResearchRunResponse
    answer: str
    confidence: float | None = None
    sources: list[SourceRef] = Field(default_factory=list)
    tool_invocation_ids: list[str] = Field(default_factory=list)
    evaluation: EvaluationRunResponse | None = None


class EvaluationReferenceRequest(BaseModel):
    research_run_id: UUID


class EvaluationJudgeRequest(BaseModel):
    evaluation_run_id: UUID


class EvaluationDatasetListResponse(BaseModel):
    items: list[EvaluationDatasetItemResponse] = Field(default_factory=list)
    total: int
