"""Workspace management API routes.

Endpoints:
- POST /workspaces/cleanup — Remove reclaimable workspaces older than retention period

Requirements: 10.4, 10.7
"""

from __future__ import annotations

import shutil
from datetime import datetime, timedelta, timezone
from pathlib import Path

import structlog
from fastapi import APIRouter, Depends, status
from pydantic import BaseModel, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.config import SecuritySettings
from orchestrator.persistence.models import Task
from orchestrator.state_machine.transitions import TERMINAL_STATES

logger = structlog.get_logger(__name__)

router = APIRouter(prefix="/workspaces", tags=["workspaces"])


class WorkspaceCleanupResponse(BaseModel):
    """Response after workspace cleanup operation."""

    cleaned_count: int = Field(description="Number of workspaces removed")
    errors: list[str] = Field(
        default_factory=list, description="Paths that failed to clean up"
    )
    retention_hours: int = Field(description="Retention period used for cleanup")
    message: str


def _get_session_factory() -> async_sessionmaker[AsyncSession]:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("Session factory dependency not configured")


def _get_security_settings() -> SecuritySettings:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("SecuritySettings dependency not configured")


@router.post(
    "/cleanup",
    response_model=WorkspaceCleanupResponse,
    status_code=status.HTTP_200_OK,
)
async def cleanup_workspaces(
    session_factory: async_sessionmaker[AsyncSession] = Depends(_get_session_factory),
    security_settings: SecuritySettings = Depends(_get_security_settings),
) -> WorkspaceCleanupResponse:
    """Remove reclaimable workspaces older than the configured retention period.

    A workspace is reclaimable when:
    1. Its owning task is in a terminal state (completed, failed, cancelled)
    2. The task reached the terminal state more than `retention_hours` ago

    This endpoint removes the workspace directory from the filesystem and
    clears the workspace_path field on the task record.

    Emits a workspace.cleaned event with the count of removed workspaces.
    Requires admin or operator-scoped API key (enforced by auth middleware).
    """
    retention_hours = security_settings.workspace_retention_hours
    cutoff = datetime.now(timezone.utc) - timedelta(hours=retention_hours)

    # Find tasks in terminal states with workspace_path set and completed before cutoff
    async with session_factory() as session:
        stmt = (
            select(Task)
            .where(
                Task.state.in_([s.value for s in TERMINAL_STATES]),
                Task.workspace_path.isnot(None),
                Task.completed_at.isnot(None),
                Task.completed_at < cutoff,
            )
            .limit(100)  # Process in batches to avoid long transactions
        )
        result = await session.execute(stmt)
        tasks = list(result.scalars().all())

    cleaned_count = 0
    errors: list[str] = []

    for task in tasks:
        workspace_path = task.workspace_path
        if not workspace_path:
            continue

        # Attempt to remove the workspace directory
        path = Path(workspace_path)
        try:
            if path.exists():
                shutil.rmtree(path)
                logger.info(
                    "Workspace directory removed",
                    workspace_path=workspace_path,
                    task_id=str(task.id),
                )
            # Clear workspace_path on the task record
            async with session_factory() as session:
                async with session.begin():
                    db_task = await session.get(Task, task.id)
                    if db_task is not None:
                        db_task.workspace_path = None
            cleaned_count += 1
        except OSError as exc:
            error_msg = f"{workspace_path}: {exc}"
            errors.append(error_msg)
            logger.warning(
                "Failed to remove workspace directory",
                workspace_path=workspace_path,
                task_id=str(task.id),
                error=str(exc),
            )

    logger.info(
        "Workspace cleanup completed",
        cleaned_count=cleaned_count,
        errors_count=len(errors),
        retention_hours=retention_hours,
    )

    return WorkspaceCleanupResponse(
        cleaned_count=cleaned_count,
        errors=errors,
        retention_hours=retention_hours,
        message=f"Cleaned {cleaned_count} workspace(s) older than {retention_hours}h",
    )
