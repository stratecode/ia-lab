"""Pydantic request/response schemas for task CRUD operations.

Requirements: 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7
"""

from __future__ import annotations

from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, Field

from orchestrator.state_machine.transitions import (
    AgentType,
    ExecutionTarget,
    Priority,
    TaskKind,
    TaskState,
)


class TaskCreate(BaseModel):
    """Request body for creating a new task (POST /tasks)."""

    description: str = Field(..., min_length=1, max_length=10000)
    metadata: dict = Field(default_factory=dict)
    allowed_capabilities: list[str] | None = None
    priority: Priority = Priority.NORMAL
    assigned_agent: AgentType | None = None
    execution_target: ExecutionTarget = ExecutionTarget.REMOTE


class TaskUpdate(BaseModel):
    """Request body for updating a task (PATCH /tasks/{id}).

    Supports state transitions with actor/reason tracking.
    """

    state: TaskState
    reason: str | None = None


class TaskResponse(BaseModel):
    """Response body for a single task."""

    id: UUID
    state: TaskState
    description: str
    metadata: dict
    allowed_capabilities: list[str] = Field(default_factory=list)
    parent_task_id: UUID | None = None
    root_task_id: UUID | None = None
    task_kind: TaskKind = TaskKind.ROOT
    assigned_agent: AgentType | None
    priority: Priority
    execution_target: ExecutionTarget = ExecutionTarget.REMOTE
    workspace_path: str | None
    retry_count: int
    correlation_id: UUID
    results: dict | None
    error_message: str | None
    created_at: datetime
    updated_at: datetime
    started_at: datetime | None
    completed_at: datetime | None

    model_config = {"from_attributes": True}


class TaskListResponse(BaseModel):
    """Paginated response for listing tasks."""

    items: list[TaskResponse]
    cursor: str | None = None
    total: int
    page_size: int


class TaskTreeResponse(TaskResponse):
    children: list["TaskTreeResponse"] = Field(default_factory=list)


TaskTreeResponse.model_rebuild()


class TaskStateConflictResponse(BaseModel):
    """Error response for invalid state transitions (HTTP 409)."""

    error: str = "invalid_state_transition"
    current_state: TaskState
    requested_state: TaskState
    valid_transitions: list[TaskState]
    task_id: str
