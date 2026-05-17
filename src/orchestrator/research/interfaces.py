"""Research and evaluation interfaces."""

from __future__ import annotations

from datetime import datetime
from typing import Any, Literal
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.capabilities.interfaces import SourceRef


ResearchIntent = Literal["search_answer", "url_summary", "document_qa", "image_qa"]


class ResearchRunRecord(BaseModel):
    id: UUID
    task_id: UUID | None = None
    entrypoint: str
    query: str
    intent_type: ResearchIntent
    selected_capabilities: list[str] = Field(default_factory=list)
    final_answer: str
    confidence: float | None = None
    source_artifact_ids: list[str] = Field(default_factory=list)
    tool_invocation_ids: list[str] = Field(default_factory=list)
    metadata: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime


class EvaluationRunRecord(BaseModel):
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


class EvaluationDatasetItemRecord(BaseModel):
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


class ResearchQueryResult(BaseModel):
    research_run: ResearchRunRecord
    answer: str
    confidence: float | None = None
    sources: list[SourceRef] = Field(default_factory=list)
    tool_invocation_ids: list[str] = Field(default_factory=list)
    evaluation: EvaluationRunRecord | None = None


class EvaluationJudgeResult(BaseModel):
    accuracy_score: float = Field(ge=0, le=1)
    coverage_score: float = Field(ge=0, le=1)
    source_use_score: float = Field(ge=0, le=1)
    usefulness_score: float = Field(ge=0, le=1)
    hallucination_risk_score: float = Field(ge=0, le=1)
    winner: Literal["orchestrator", "reference", "tie"]
    reasoning: str = Field(min_length=1)
