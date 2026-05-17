"""Pydantic schemas for approval API endpoints.

Requirements: 7.2
"""

from __future__ import annotations

from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.state_machine.transitions import ApprovalStatus


class ApprovalResolveRequest(BaseModel):
    """Request body for approving or rejecting an approval."""

    operator: str = Field(
        ..., min_length=1, max_length=128, description="Identity of the person resolving"
    )


class ApprovalResponse(BaseModel):
    """Response schema for a single approval record."""

    id: UUID
    task_id: UUID
    action_type: str
    target_resource: str
    status: ApprovalStatus
    operator: str | None = None
    timeout_seconds: int
    escalation_level: int
    requested_at: datetime
    resolved_at: datetime | None = None
    timeout_at: datetime

    model_config = {"from_attributes": True}


class ApprovalListResponse(BaseModel):
    """Response schema for listing approvals."""

    items: list[ApprovalResponse]
    total: int
