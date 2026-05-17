"""Tool execution interfaces and result schema."""

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, Field


class ToolResult(BaseModel):
    """Normalized result from any tool execution (Requirement 17.1)."""

    tool_name: str = Field(..., max_length=64)
    status: Literal["success", "error", "timeout", "blocked"]
    output: str = Field(..., max_length=65536)  # 64 KB
    duration_ms: int = Field(..., ge=0)
    timestamp: datetime
    exit_code: int | None = None
    error_message: str | None = None
    artifacts: list[str] = Field(default_factory=list)
