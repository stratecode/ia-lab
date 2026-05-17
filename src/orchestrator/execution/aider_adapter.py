"""Aider-task subprocess adapter.

Wraps the /usr/local/bin/aider-task script with:
- Subprocess execution as the `aider` system user
- Per-task timeout enforcement (SIGTERM → 10s grace → SIGKILL)
- stdout/stderr capture
- Exit code collection
- ToolResult schema wrapping

Requirements: 18.1, 18.2, 18.3, 18.6, 18.7
"""

from __future__ import annotations

import asyncio
import logging
import time
from dataclasses import dataclass
from datetime import datetime, timezone

from orchestrator.tools.interfaces import ToolResult

logger = logging.getLogger(__name__)

# Default path to the aider-task script (Phase 3 tool)
AIDER_TASK_PATH = "/usr/local/bin/aider-task"

# Default timeout for aider-task execution (seconds)
DEFAULT_AIDER_TIMEOUT = 1800

# Grace period between SIGTERM and SIGKILL (seconds)
SIGTERM_GRACE_PERIOD = 10

# Maximum output size (64KB as per ToolResult schema)
MAX_OUTPUT_SIZE = 65536


@dataclass(frozen=True)
class AiderTaskParams:
    """Parameters for invoking the aider-task script.

    Attributes:
        task_id: UUID of the task being executed.
        repo_path: Path to the git repository.
        branch: Git branch name to work on.
        prompt: Task description / prompt for the aider agent.
        workspace_path: Working directory for execution.
        timeout: Maximum execution time in seconds.
    """

    task_id: str
    repo_path: str
    branch: str
    prompt: str
    workspace_path: str
    timeout: float = DEFAULT_AIDER_TIMEOUT


@dataclass(frozen=True)
class SubprocessResult:
    """Raw result from subprocess execution.

    Attributes:
        stdout: Captured standard output.
        stderr: Captured standard error.
        exit_code: Process exit code (None if killed/timeout).
        timed_out: Whether the process was killed due to timeout.
        duration_ms: Execution duration in milliseconds.
    """

    stdout: str
    stderr: str
    exit_code: int | None
    timed_out: bool
    duration_ms: int


def _build_aider_command(params: AiderTaskParams) -> list[str]:
    """Build the command line for invoking aider-task as the aider user.

    Uses sudo -u aider to execute as the aider system user with the
    workspace as the working directory.

    Args:
        params: Aider task parameters.

    Returns:
        Command as a list of strings for asyncio.create_subprocess_exec.
    """
    return [
        "sudo",
        "-u",
        "aider",
        "--",
        AIDER_TASK_PATH,
        "--task-id",
        params.task_id,
        "--repo",
        params.repo_path,
        "--branch",
        params.branch,
        "--prompt",
        params.prompt,
    ]


def _truncate_output(output: str, max_size: int = MAX_OUTPUT_SIZE) -> str:
    """Truncate output to fit within the ToolResult schema limit.

    If truncation occurs, appends a marker indicating the output was cut.

    Args:
        output: Raw output string.
        max_size: Maximum allowed size in characters.

    Returns:
        Truncated output string.
    """
    if len(output) <= max_size:
        return output
    marker = "\n... [output truncated at 64KB]"
    return output[: max_size - len(marker)] + marker


class AiderAdapter:
    """Subprocess wrapper for the aider-task script.

    Handles:
    - Building the command with proper arguments
    - Executing as the `aider` system user via sudo
    - Enforcing per-task timeout (SIGTERM → 10s → SIGKILL)
    - Capturing stdout/stderr/exit_code
    - Wrapping results in the ToolResult schema
    """

    def __init__(
        self,
        aider_path: str = AIDER_TASK_PATH,
        default_timeout: float = DEFAULT_AIDER_TIMEOUT,
        grace_period: float = SIGTERM_GRACE_PERIOD,
    ) -> None:
        """Initialize the adapter.

        Args:
            aider_path: Path to the aider-task executable.
            default_timeout: Default timeout in seconds.
            grace_period: Seconds between SIGTERM and SIGKILL.
        """
        self._aider_path = aider_path
        self._default_timeout = default_timeout
        self._grace_period = grace_period

    async def execute(self, params: AiderTaskParams) -> ToolResult:
        """Execute aider-task and return a normalized ToolResult.

        Args:
            params: Parameters for the aider-task invocation.

        Returns:
            ToolResult with execution output, status, and metadata.
        """
        timeout = params.timeout if params.timeout > 0 else self._default_timeout
        start_time = time.monotonic()

        try:
            result = await self._run_subprocess(params, timeout)
        except Exception as exc:
            duration_ms = int((time.monotonic() - start_time) * 1000)
            logger.error(
                "aider-task execution failed with exception",
                extra={
                    "task_id": params.task_id,
                    "error": str(exc),
                    "duration_ms": duration_ms,
                },
            )
            return ToolResult(
                tool_name="aider-task",
                status="error",
                output="",
                duration_ms=duration_ms,
                timestamp=datetime.now(timezone.utc),
                exit_code=None,
                error_message=f"Subprocess execution error: {exc}",
                artifacts=[],
            )

        return self._build_tool_result(result, params.task_id)

    async def _run_subprocess(
        self, params: AiderTaskParams, timeout: float
    ) -> SubprocessResult:
        """Run the aider-task subprocess with timeout enforcement.

        Timeout handling:
        1. Wait for process to complete within timeout
        2. If timeout exceeded: send SIGTERM
        3. Wait grace_period seconds for graceful shutdown
        4. If still running: send SIGKILL

        Args:
            params: Aider task parameters.
            timeout: Maximum execution time in seconds.

        Returns:
            SubprocessResult with captured output and metadata.
        """
        cmd = _build_aider_command(params)
        start_time = time.monotonic()

        logger.info(
            "Starting aider-task subprocess",
            extra={
                "task_id": params.task_id,
                "workspace": params.workspace_path,
                "timeout": timeout,
            },
        )

        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            cwd=params.workspace_path,
        )

        timed_out = False
        try:
            stdout_bytes, stderr_bytes = await asyncio.wait_for(
                process.communicate(), timeout=timeout
            )
        except asyncio.TimeoutError:
            timed_out = True
            stdout_bytes, stderr_bytes = await self._terminate_process(process)

        duration_ms = int((time.monotonic() - start_time) * 1000)

        stdout = stdout_bytes.decode("utf-8", errors="replace") if stdout_bytes else ""
        stderr = stderr_bytes.decode("utf-8", errors="replace") if stderr_bytes else ""

        exit_code = process.returncode if not timed_out else None

        logger.info(
            "aider-task subprocess completed",
            extra={
                "task_id": params.task_id,
                "exit_code": exit_code,
                "timed_out": timed_out,
                "duration_ms": duration_ms,
            },
        )

        return SubprocessResult(
            stdout=stdout,
            stderr=stderr,
            exit_code=exit_code,
            timed_out=timed_out,
            duration_ms=duration_ms,
        )

    async def _terminate_process(
        self, process: asyncio.subprocess.Process
    ) -> tuple[bytes, bytes]:
        """Terminate a process with SIGTERM → grace period → SIGKILL.

        Args:
            process: The running subprocess to terminate.

        Returns:
            Tuple of (stdout_bytes, stderr_bytes) captured before/during termination.
        """
        logger.warning(
            "aider-task timeout exceeded, sending SIGTERM",
            extra={"pid": process.pid},
        )

        # Send SIGTERM for graceful shutdown
        try:
            process.terminate()
        except ProcessLookupError:
            # Process already exited
            pass

        # Wait grace period for graceful shutdown
        try:
            stdout_bytes, stderr_bytes = await asyncio.wait_for(
                process.communicate(), timeout=self._grace_period
            )
            return stdout_bytes, stderr_bytes
        except asyncio.TimeoutError:
            pass

        # Grace period exceeded — send SIGKILL
        logger.warning(
            "aider-task did not terminate after SIGTERM, sending SIGKILL",
            extra={"pid": process.pid},
        )
        try:
            process.kill()
        except ProcessLookupError:
            pass

        # Collect any remaining output
        stdout_bytes, stderr_bytes = await process.communicate()
        return stdout_bytes, stderr_bytes

    def _build_tool_result(
        self, result: SubprocessResult, task_id: str
    ) -> ToolResult:
        """Convert a SubprocessResult into a normalized ToolResult.

        Args:
            result: Raw subprocess execution result.
            task_id: Task ID for logging context.

        Returns:
            Normalized ToolResult.
        """
        # Combine stdout and stderr for the output field
        combined_output = result.stdout
        if result.stderr:
            combined_output += f"\n--- stderr ---\n{result.stderr}"

        output = _truncate_output(combined_output)

        if result.timed_out:
            status = "timeout"
            error_message = (
                f"aider-task exceeded timeout, process terminated "
                f"(SIGTERM + {self._grace_period}s grace → SIGKILL)"
            )
        elif result.exit_code == 0:
            status = "success"
            error_message = None
        else:
            status = "error"
            error_message = (
                f"aider-task exited with code {result.exit_code}"
            )

        return ToolResult(
            tool_name="aider-task",
            status=status,
            output=output,
            duration_ms=result.duration_ms,
            timestamp=datetime.now(timezone.utc),
            exit_code=result.exit_code,
            error_message=error_message,
            artifacts=[],
        )
