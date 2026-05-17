"""Shared API response schemas used across all routes."""

from datetime import UTC, datetime
from typing import Generic, TypeVar
from uuid import UUID

from pydantic import BaseModel, Field

T = TypeVar("T")


class ErrorResponse(BaseModel):
    """Standard error response returned by all API error handlers."""

    error: str
    detail: str | None = None
    correlation_id: UUID | None = None
    timestamp: datetime = Field(default_factory=lambda: datetime.now(UTC))


class PaginatedResponse(BaseModel, Generic[T]):
    """Cursor-based paginated response wrapper."""

    items: list[T]
    cursor: str | None = None
    total: int
    page_size: int
