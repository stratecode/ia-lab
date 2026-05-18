"""API routes for research mode and evaluation harness."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, HTTPException

from orchestrator.api.schemas.common import ErrorResponse
from orchestrator.api.schemas.research import (
    EvaluationDatasetItemResponse,
    EvaluationDatasetListResponse,
    EvaluationJudgeRequest,
    EvaluationReferenceRequest,
    EvaluationRunResponse,
    ResearchQueryRequest,
    ResearchQueryResponse,
    ResearchRunResponse,
)
from orchestrator.research.service import ResearchService, ResearchServiceError

router = APIRouter(tags=["research"])


def _get_research_service() -> ResearchService:
    raise NotImplementedError("ResearchService dependency not configured")


def _research_run_response(run) -> ResearchRunResponse:
    return ResearchRunResponse(**run.model_dump(mode="json"))


def _evaluation_run_response(run) -> EvaluationRunResponse:
    return EvaluationRunResponse(**run.model_dump(mode="json"))


@router.post("/research/query", response_model=ResearchQueryResponse)
async def research_query(
    body: ResearchQueryRequest,
    research_service: ResearchService = Depends(_get_research_service),
) -> ResearchQueryResponse:
    try:
        result = await research_service.query(
            body.query,
            task_id=str(body.task_id) if body.task_id else None,
            entrypoint="api",
            allowed_capabilities=body.allowed_capabilities,
            evaluate_against_reference=body.evaluate_against_reference,
        )
    except ResearchServiceError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    return ResearchQueryResponse(
        research_run=_research_run_response(result.research_run),
        answer=result.answer,
        confidence=result.confidence,
        sources=result.sources,
        tool_invocation_ids=result.tool_invocation_ids,
        timings_ms=result.timings_ms,
        evaluation=_evaluation_run_response(result.evaluation) if result.evaluation else None,
    )


@router.get(
    "/research/runs/{research_run_id}",
    response_model=ResearchRunResponse,
    responses={404: {"model": ErrorResponse}},
) 
async def get_research_run(
    research_run_id: uuid.UUID,
    research_service: ResearchService = Depends(_get_research_service),
) -> ResearchRunResponse:
    run = await research_service.get_research_run(research_run_id)
    if run is None:
        raise HTTPException(status_code=404, detail=f"Research run {research_run_id} not found")
    return _research_run_response(run)


@router.post("/evaluations/reference", response_model=EvaluationRunResponse)
async def create_reference_evaluation(
    body: EvaluationReferenceRequest,
    research_service: ResearchService = Depends(_get_research_service),
) -> EvaluationRunResponse:
    try:
        run = await research_service.create_reference(body.research_run_id)
    except ResearchServiceError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    return _evaluation_run_response(run)


@router.post("/evaluations/judge", response_model=EvaluationRunResponse)
async def judge_evaluation(
    body: EvaluationJudgeRequest,
    research_service: ResearchService = Depends(_get_research_service),
) -> EvaluationRunResponse:
    try:
        run = await research_service.judge_evaluation(body.evaluation_run_id)
    except ResearchServiceError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    return _evaluation_run_response(run)


@router.get(
    "/evaluations/{evaluation_run_id}",
    response_model=EvaluationRunResponse,
    responses={404: {"model": ErrorResponse}},
)
async def get_evaluation(
    evaluation_run_id: uuid.UUID,
    research_service: ResearchService = Depends(_get_research_service),
) -> EvaluationRunResponse:
    run = await research_service.get_evaluation_run(evaluation_run_id)
    if run is None:
        raise HTTPException(status_code=404, detail=f"Evaluation run {evaluation_run_id} not found")
    return _evaluation_run_response(run)


@router.get("/datasets/evaluation-items", response_model=EvaluationDatasetListResponse)
async def list_dataset_items(
    research_service: ResearchService = Depends(_get_research_service),
) -> EvaluationDatasetListResponse:
    items = await research_service.list_dataset_items()
    return EvaluationDatasetListResponse(
        items=[EvaluationDatasetItemResponse(**item.model_dump(mode="json")) for item in items],
        total=len(items),
    )
