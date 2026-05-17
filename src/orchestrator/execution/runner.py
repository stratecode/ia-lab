"""Task execution runner — orchestrates task execution.

The TaskRunner is responsible for:
- Validating execution inputs (task_id, agent_type, workspace_path)
- Selecting the appropriate adapter based on agent type
- Invoking the adapter (aider-task for coder tasks)
- Capturing and normalizing results into ToolResult schema
- Handling execution errors gracefully

For "coder" agent type tasks, the runner delegates to the AiderAdapter.
Other agent types will be supported in future phases.

Requirements: 18.1, 18.2, 18.3, 18.4, 18.5, 18.6, 18.7
"""

from __future__ import annotations

import logging
import os
import time
from datetime import datetime, timezone

from orchestrator.execution.aider_adapter import (
    AiderAdapter,
    AiderTaskParams,
    DEFAULT_AIDER_TIMEOUT,
)
from orchestrator.capabilities.service import CapabilityService
from orchestrator.observability.metrics import (
    PLANNER_EXECUTION_DURATION_SECONDS,
    record_planner_invalid_output,
)
from orchestrator.planning.service import PlannerOutputError, PlannerService
from orchestrator.state_machine.transitions import AgentType
from orchestrator.tools.interfaces import ToolResult

logger = logging.getLogger(__name__)

# Default branch name when not specified
DEFAULT_BRANCH = "main"


class TaskRunnerError(Exception):
    """Raised when task execution fails due to invalid inputs or configuration."""

    pass


class TaskRunner:
    """Orchestrates task execution by delegating to the appropriate adapter.

    The runner:
    1. Validates inputs (workspace exists, agent type supported)
    2. Selects the adapter (AiderAdapter for coder tasks)
    3. Builds execution parameters
    4. Invokes the adapter
    5. Returns the normalized ToolResult

    Usage:
        runner = TaskRunner(aider_timeout=1800)
        result = await runner.execute(task_id, AgentType.CODER, workspace_path)
    """

    def __init__(
        self,
        aider_adapter: AiderAdapter | None = None,
        planner_service: PlannerService | None = None,
        capability_service: CapabilityService | None = None,
        aider_timeout: float = DEFAULT_AIDER_TIMEOUT,
        default_branch: str = DEFAULT_BRANCH,
    ) -> None:
        """Initialize the task runner.

        Args:
            aider_adapter: AiderAdapter instance (created if not provided).
            aider_timeout: Default timeout for aider-task execution.
            default_branch: Default git branch for aider-task.
        """
        self._aider_adapter = aider_adapter or AiderAdapter(
            default_timeout=aider_timeout
        )
        self._planner_service = planner_service
        self._capability_service = capability_service
        self._aider_timeout = aider_timeout
        self._default_branch = default_branch

    async def execute(
        self,
        task_id: str,
        agent_type: AgentType,
        workspace_path: str,
        description: str = "",
        repo_path: str | None = None,
        branch: str | None = None,
        metadata: dict | None = None,
        timeout: float | None = None,
    ) -> ToolResult:
        """Execute a task and return the normalized result.

        Args:
            task_id: UUID of the task to execute.
            agent_type: The agent type assigned to this task.
            workspace_path: Filesystem path to the task's workspace.
            description: Task description / prompt for the agent.
            repo_path: Path to the git repository (defaults to workspace_path).
            branch: Git branch name (defaults to configured default).
            timeout: Per-task timeout override in seconds.

        Returns:
            A normalized ToolResult with execution output.

        Raises:
            TaskRunnerError: If inputs are invalid (workspace doesn't exist,
                unsupported agent type).
        """
        start_time = time.monotonic()

        # Validate inputs
        self._validate_inputs(task_id, agent_type, workspace_path)

        logger.info(
            "Starting task execution",
            extra={
                "task_id": task_id,
                "agent_type": agent_type,
                "workspace_path": workspace_path,
            },
        )

        # Route to appropriate adapter based on agent type
        if agent_type == AgentType.CODER:
            result = await self._execute_coder_task(
                task_id=task_id,
                workspace_path=workspace_path,
                description=description,
                repo_path=repo_path,
                branch=branch,
                timeout=timeout,
            )
        elif agent_type == AgentType.PLANNER:
            result = await self._execute_planner_task(
                task_id=task_id,
                description=description,
                metadata=metadata or {},
            )
        else:
            # Other agent types not yet implemented
            duration_ms = int((time.monotonic() - start_time) * 1000)
            result = ToolResult(
                tool_name=f"agent-{agent_type}",
                status="error",
                output="",
                duration_ms=duration_ms,
                timestamp=datetime.now(timezone.utc),
                exit_code=None,
                error_message=(
                    f"Agent type '{agent_type}' execution not yet implemented. "
                    f"Only 'coder' agent type is supported in this phase."
                ),
                artifacts=[],
            )

        logger.info(
            "Task execution completed",
            extra={
                "task_id": task_id,
                "agent_type": agent_type,
                "status": result.get("status", "unknown") if isinstance(result, dict) else result.status,
                "duration_ms": result.get("duration_ms", 0) if isinstance(result, dict) else result.duration_ms,
            },
        )

        return result

    def _validate_inputs(
        self, task_id: str, agent_type: AgentType, workspace_path: str
    ) -> None:
        """Validate execution inputs before proceeding.

        Args:
            task_id: Must be a non-empty string.
            agent_type: Must be a valid AgentType.
            workspace_path: Must be a non-empty string pointing to an existing directory.

        Raises:
            TaskRunnerError: If any validation fails.
        """
        if not task_id or not task_id.strip():
            raise TaskRunnerError("task_id must be a non-empty string")

        if not workspace_path or not workspace_path.strip():
            raise TaskRunnerError("workspace_path must be a non-empty string")

        if not os.path.isdir(workspace_path):
            raise TaskRunnerError(
                f"workspace_path does not exist or is not a directory: {workspace_path}"
            )

    async def _execute_coder_task(
        self,
        task_id: str,
        workspace_path: str,
        description: str,
        repo_path: str | None,
        branch: str | None,
        timeout: float | None,
    ) -> ToolResult:
        """Execute a coder task via the AiderAdapter.

        Args:
            task_id: UUID of the task.
            workspace_path: Working directory for execution.
            description: Task prompt/description.
            repo_path: Git repository path (defaults to workspace_path).
            branch: Git branch (defaults to configured default).
            timeout: Timeout override in seconds.

        Returns:
            ToolResult from the aider-task execution.
        """
        if not repo_path or not repo_path.strip():
            raise TaskRunnerError(
                "coder task requires metadata.repo_name (or repo_path/repository_path) for remote execution"
            )
        params = AiderTaskParams(
            task_id=task_id,
            repo_path=repo_path,
            branch=branch or self._default_branch,
            prompt=description,
            workspace_path=workspace_path,
            timeout=timeout or self._aider_timeout,
        )

        return await self._aider_adapter.execute(params)

    async def _execute_planner_task(
        self,
        task_id: str,
        description: str,
        metadata: dict,
    ) -> dict:
        if self._planner_service is None:
            raise TaskRunnerError("planner_service is not configured")

        start_time = time.monotonic()
        try:
            planner_metadata = dict(metadata)
            if self._capability_service is not None:
                bundle = await self._capability_service.build_planner_context(
                    task_id=task_id,
                    description=description,
                    metadata=planner_metadata,
                )
                if bundle.context_blocks:
                    planner_metadata["capability_context"] = bundle.context_blocks
                    planner_metadata["capability_invocation_ids"] = [
                        str(item.id) for item in bundle.invocations
                    ]
            plan = await self._planner_service.create_plan(description, planner_metadata)
        except PlannerOutputError as exc:
            PLANNER_EXECUTION_DURATION_SECONDS.observe(max(time.monotonic() - start_time, 0.0))
            record_planner_invalid_output()
            return {
                "status": "error",
                "output": "",
                "error_message": str(exc),
                "subtasks": [],
            }
        PLANNER_EXECUTION_DURATION_SECONDS.observe(max(time.monotonic() - start_time, 0.0))

        return {
            "status": "success",
            "output": plan.plan,
            "plan_summary": plan.summary,
            "plan_markdown": plan.plan,
            "subtasks": [subtask.model_dump(mode="json") for subtask in plan.subtasks],
            "plan_only": bool(metadata.get("plan_only", False)),
            "raw_response": plan.raw_response,
            "capability_context": planner_metadata.get("capability_context", []),
            "capability_invocation_ids": planner_metadata.get("capability_invocation_ids", []),
        }
