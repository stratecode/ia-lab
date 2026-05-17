"""Tool result normalizer — validates and stores ToolResults.

Validates incoming ToolResults against the schema defined in interfaces.py,
logs malformed results without crashing, and persists the last valid result
per task in the database.

Requirements: 17.1, 17.2, 17.5, 17.6
"""

from __future__ import annotations

import logging
import uuid
from typing import Any

from pydantic import ValidationError
from sqlalchemy import update
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import Task
from orchestrator.tools.interfaces import ToolResult

logger = logging.getLogger(__name__)

# Maximum output size for ToolResult (64KB)
MAX_OUTPUT_SIZE = 65536


class NormalizationError(Exception):
    """Raised when a ToolResult cannot be normalized."""

    def __init__(self, message: str, raw_data: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.raw_data = raw_data


class ResultNormalizer:
    """Validates and normalizes incoming tool results.

    Ensures all ToolResults conform to the schema before storage.
    Malformed results are logged for debugging but do not crash the system.
    """

    def validate(self, data: dict[str, Any]) -> ToolResult | None:
        """Validate raw data against the ToolResult schema.

        Args:
            data: Raw dictionary representing a tool result.

        Returns:
            A validated ToolResult instance, or None if validation fails.
            Malformed results are logged at WARNING level.
        """
        try:
            result = ToolResult.model_validate(data)
            return result
        except ValidationError as exc:
            logger.warning(
                "Malformed ToolResult rejected",
                extra={
                    "validation_errors": exc.errors(),
                    "raw_data_keys": list(data.keys()) if isinstance(data, dict) else [],
                    "tool_name": data.get("tool_name", "<missing>") if isinstance(data, dict) else "<missing>",
                },
            )
            return None
        except Exception as exc:
            logger.warning(
                "Unexpected error validating ToolResult",
                extra={
                    "error": str(exc),
                    "error_type": type(exc).__name__,
                    "raw_data_keys": list(data.keys()) if isinstance(data, dict) else [],
                },
            )
            return None

    def validate_or_raise(self, data: dict[str, Any]) -> ToolResult:
        """Validate raw data, raising NormalizationError on failure.

        Args:
            data: Raw dictionary representing a tool result.

        Returns:
            A validated ToolResult instance.

        Raises:
            NormalizationError: If validation fails.
        """
        try:
            return ToolResult.model_validate(data)
        except ValidationError as exc:
            raise NormalizationError(
                f"ToolResult validation failed: {exc.error_count()} error(s)",
                raw_data=data,
            ) from exc

    async def store_result(
        self,
        session: AsyncSession,
        task_id: uuid.UUID,
        result: ToolResult,
    ) -> None:
        """Store the last ToolResult for a task in the database.

        Updates the task's `results` JSONB field with the serialized ToolResult.

        Args:
            session: Active async database session.
            task_id: The UUID of the task to update.
            result: The validated ToolResult to store.
        """
        result_dict = result.model_dump(mode="json")

        stmt = (
            update(Task)
            .where(Task.id == task_id)
            .values(results=result_dict)
        )
        await session.execute(stmt)
        await session.flush()

        logger.info(
            "ToolResult stored for task",
            extra={
                "task_id": str(task_id),
                "tool_name": result.tool_name,
                "status": result.status,
                "duration_ms": result.duration_ms,
            },
        )

    async def validate_and_store(
        self,
        session: AsyncSession,
        task_id: uuid.UUID,
        data: dict[str, Any],
    ) -> ToolResult | None:
        """Validate a raw result and store it if valid.

        Combines validation and storage in a single operation.
        Returns None and logs a warning if validation fails.

        Args:
            session: Active async database session.
            task_id: The UUID of the task.
            data: Raw dictionary representing a tool result.

        Returns:
            The validated ToolResult if successful, None otherwise.
        """
        result = self.validate(data)
        if result is None:
            logger.warning(
                "Skipping storage for malformed ToolResult",
                extra={"task_id": str(task_id)},
            )
            return None

        await self.store_result(session, task_id, result)
        return result
