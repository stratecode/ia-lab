"""Schemas for Local Agent Bridge registration and task polling."""

from __future__ import annotations

from datetime import datetime
from uuid import UUID

from pydantic import BaseModel, Field


class BridgeRegisterRequest(BaseModel):
    bridge_id: UUID
    name: str = Field(..., min_length=1, max_length=128)
    hostname: str = Field(..., min_length=1, max_length=255)
    workspace_root: str = Field(..., min_length=1, max_length=1024)
    capabilities: dict = Field(default_factory=dict)
    api_key_name: str | None = Field(default=None, max_length=255)


class BridgeHeartbeatRequest(BaseModel):
    status: str = Field(default="active", min_length=1, max_length=32)


class BridgeResponse(BaseModel):
    id: UUID
    name: str
    hostname: str
    workspace_root: str
    status: str
    capabilities: dict = Field(default_factory=dict)
    api_key_name: str | None = None
    last_heartbeat: datetime | None = None
    created_at: datetime | None = None
    updated_at: datetime | None = None


class BridgeTaskClaimResponse(BaseModel):
    task_id: UUID
    root_task_id: UUID | None = None
    parent_task_id: UUID | None = None
    description: str
    assigned_agent: str
    execution_target: str
    workspace_path: str
    workspace_root: str
    metadata: dict = Field(default_factory=dict)
    repo_path: str
    branch: str


class BridgeResultRequest(BaseModel):
    status: str = Field(..., min_length=1, max_length=64)
    summary: str | None = None
    stdout: str | None = None
    stderr: str | None = None
    exit_code: int | None = None
    diff: str | None = None
    changed_files: list[str] = Field(default_factory=list)
    test_results: dict | None = None
    artifacts: list[dict] = Field(default_factory=list)
    error_message: str | None = None
    action_type: str | None = None
    target_resource: str | None = None
    timeout_seconds: int | None = None


class BridgeListResponse(BaseModel):
    items: list[BridgeResponse]
    total: int
