"""Approval management API routes.

Endpoints:
- POST /approvals/{approval_id}/approve — Approve a pending approval
- POST /approvals/{approval_id}/reject — Reject a pending approval
- GET /approvals — List pending approvals
- GET /approvals/{approval_id} — Get approval detail

Requirements: 7.2
"""

from __future__ import annotations

import uuid

import structlog
from fastapi import APIRouter, Depends, HTTPException, status
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.api.schemas.approvals import (
    ApprovalListResponse,
    ApprovalResolveRequest,
    ApprovalResponse,
)
from orchestrator.api.schemas.common import ErrorResponse
from orchestrator.approvals.interfaces import ApprovalDecision
from orchestrator.approvals.manager import (
    ApprovalAlreadyResolvedError,
    ApprovalManager,
    ApprovalNotFoundError,
)
from orchestrator.persistence.repositories.approvals import ApprovalRepository
from orchestrator.state_machine.transitions import ApprovalStatus

logger = structlog.get_logger(__name__)

router = APIRouter(prefix="/approvals", tags=["approvals"])


def _get_approval_manager() -> ApprovalManager:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("ApprovalManager dependency not configured")


def _get_session_factory() -> async_sessionmaker[AsyncSession]:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("Session factory dependency not configured")


@router.post(
    "/{approval_id}/approve",
    response_model=ApprovalResponse,
    status_code=status.HTTP_200_OK,
    responses={
        404: {"model": ErrorResponse, "description": "Approval not found"},
        409: {"model": ErrorResponse, "description": "Approval already resolved"},
    },
)
async def approve_approval(
    approval_id: uuid.UUID,
    body: ApprovalResolveRequest,
    approval_manager: ApprovalManager = Depends(_get_approval_manager),
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> ApprovalResponse:
    """Approve a pending approval request.

    Resumes the paused task execution and transitions it back to in_progress.
    """
    logger.info(
        "Approval approve requested",
        approval_id=str(approval_id),
        operator=body.operator,
    )

    try:
        await approval_manager.resolve(
            approval_id=str(approval_id),
            decision=ApprovalDecision.APPROVE,
            operator=body.operator,
        )
    except ApprovalNotFoundError:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f"Approval {approval_id} not found",
        )
    except ApprovalAlreadyResolvedError as exc:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f"Approval {approval_id} already resolved with status: {exc.current_status.value}",
        )

    # Fetch the updated approval to return
    async with session_factory() as session:
        repo = ApprovalRepository(session)
        approval = await repo.get_by_id(approval_id)
        if approval is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=f"Approval {approval_id} not found after resolution",
            )
        return ApprovalResponse.model_validate(approval)


@router.post(
    "/{approval_id}/reject",
    response_model=ApprovalResponse,
    status_code=status.HTTP_200_OK,
    responses={
        404: {"model": ErrorResponse, "description": "Approval not found"},
        409: {"model": ErrorResponse, "description": "Approval already resolved"},
    },
)
async def reject_approval(
    approval_id: uuid.UUID,
    body: ApprovalResolveRequest,
    approval_manager: ApprovalManager = Depends(_get_approval_manager),
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> ApprovalResponse:
    """Reject a pending approval request.

    Cancels the pending action and transitions the task to cancelled state.
    """
    logger.info(
        "Approval reject requested",
        approval_id=str(approval_id),
        operator=body.operator,
    )

    try:
        await approval_manager.resolve(
            approval_id=str(approval_id),
            decision=ApprovalDecision.REJECT,
            operator=body.operator,
        )
    except ApprovalNotFoundError:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=f"Approval {approval_id} not found",
        )
    except ApprovalAlreadyResolvedError as exc:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=f"Approval {approval_id} already resolved with status: {exc.current_status.value}",
        )

    # Fetch the updated approval to return
    async with session_factory() as session:
        repo = ApprovalRepository(session)
        approval = await repo.get_by_id(approval_id)
        if approval is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=f"Approval {approval_id} not found after resolution",
            )
        return ApprovalResponse.model_validate(approval)


@router.get(
    "",
    response_model=ApprovalListResponse,
    status_code=status.HTTP_200_OK,
)
async def list_approvals(
    status_filter: ApprovalStatus | None = None,
    limit: int = 50,
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> ApprovalListResponse:
    """List approval requests, optionally filtered by status.

    Defaults to listing pending approvals if no status filter is provided.
    """
    async with session_factory() as session:
        repo = ApprovalRepository(session)
        if status_filter == ApprovalStatus.PENDING or status_filter is None:
            approvals = await repo.list_pending(limit=limit)
        else:
            # For non-pending statuses, query directly
            from sqlalchemy import select

            from orchestrator.persistence.models import Approval

            stmt = (
                select(Approval)
                .where(Approval.status == status_filter)
                .order_by(Approval.requested_at.desc())
                .limit(limit)
            )
            result = await session.execute(stmt)
            approvals = list(result.scalars().all())

        items = [ApprovalResponse.model_validate(a) for a in approvals]
        return ApprovalListResponse(items=items, total=len(items))


@router.get(
    "/{approval_id}",
    response_model=ApprovalResponse,
    status_code=status.HTTP_200_OK,
    responses={
        404: {"model": ErrorResponse, "description": "Approval not found"},
    },
)
async def get_approval(
    approval_id: uuid.UUID,
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
) -> ApprovalResponse:
    """Get detailed information about a specific approval."""
    async with session_factory() as session:
        repo = ApprovalRepository(session)
        approval = await repo.get_by_id(approval_id)
        if approval is None:
            raise HTTPException(
                status_code=status.HTTP_404_NOT_FOUND,
                detail=f"Approval {approval_id} not found",
            )
        return ApprovalResponse.model_validate(approval)
