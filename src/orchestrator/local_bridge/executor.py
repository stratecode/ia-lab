"""Safe local execution primitives for the Local Agent Bridge."""

from __future__ import annotations

import asyncio
import subprocess
from pathlib import Path
from typing import Any


class LocalExecutionError(Exception):
    """Raised when a local bridge operation is invalid or unsafe."""


class LocalWorkspaceExecutor:
    """Executes a restricted set of filesystem and command operations."""

    _ALLOWED_COMMANDS = {
        "pytest",
        "python",
        "python3",
        "uv",
        "npm",
        "pnpm",
        "yarn",
        "make",
        "composer",
        "php",
        "git",
    }

    def __init__(self, workspace_root: str) -> None:
        self._workspace_root = Path(workspace_root).resolve()

    async def execute(self, task: dict[str, Any]) -> dict[str, Any]:
        metadata = task.get("metadata") if isinstance(task.get("metadata"), dict) else {}
        tool_request = metadata.get("tool_request") if isinstance(metadata.get("tool_request"), dict) else None
        if tool_request is None:
            raise LocalExecutionError("Local task requires metadata.tool_request")

        tool = str(tool_request.get("tool") or "").strip()
        if not tool:
            raise LocalExecutionError("tool_request.tool is required")

        if bool(metadata.get("requires_approval")) or bool(tool_request.get("requires_approval")):
            return {
                "status": "waiting_approval",
                "summary": f"Approval required before executing tool {tool}",
                "action_type": "local_bridge_tool",
                "target_resource": str(tool_request.get("path") or tool),
                "timeout_seconds": int(tool_request.get("timeout_seconds") or 300),
            }

        handlers = {
            "read_file": self._read_file,
            "list_files": self._list_files,
            "write_file": self._write_file,
            "apply_patch": self._apply_patch,
            "run_command": self._run_command,
            "git_status": self._git_status,
            "git_diff": self._git_diff,
            "run_tests": self._run_tests,
        }
        handler = handlers.get(tool)
        if handler is None:
            raise LocalExecutionError(f"Unsupported local tool: {tool}")
        return await handler(tool_request)

    def _resolve(self, raw_path: str | None) -> Path:
        if not raw_path:
            raise LocalExecutionError("path is required")
        candidate = (self._workspace_root / raw_path).resolve()
        if candidate != self._workspace_root and self._workspace_root not in candidate.parents:
            raise LocalExecutionError(f"path escapes workspace: {raw_path}")
        return candidate

    async def _read_file(self, request: dict[str, Any]) -> dict[str, Any]:
        path = self._resolve(str(request.get("path") or ""))
        return {
            "status": "success",
            "summary": f"Read {path.relative_to(self._workspace_root)}",
            "stdout": path.read_text(encoding="utf-8"),
            "changed_files": [],
            "diff": "",
        }

    async def _list_files(self, request: dict[str, Any]) -> dict[str, Any]:
        path = self._resolve(str(request.get("path") or "."))
        pattern = str(request.get("pattern") or "*")
        files = sorted(
            str(item.relative_to(self._workspace_root))
            for item in path.rglob(pattern)
            if item.is_file()
        )
        return {
            "status": "success",
            "summary": f"Listed {len(files)} files",
            "stdout": "\n".join(files),
            "changed_files": [],
            "diff": "",
        }

    async def _write_file(self, request: dict[str, Any]) -> dict[str, Any]:
        path = self._resolve(str(request.get("path") or ""))
        content = str(request.get("content") or "")
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return await self._with_git_artifacts(
            {
                "status": "success",
                "summary": f"Wrote {path.relative_to(self._workspace_root)}",
            }
        )

    async def _apply_patch(self, request: dict[str, Any]) -> dict[str, Any]:
        patch = str(request.get("patch") or "")
        if not patch.strip():
            raise LocalExecutionError("patch is required")
        proc = await asyncio.create_subprocess_exec(
            "git",
            "apply",
            "--whitespace=nowarn",
            "-",
            cwd=str(self._workspace_root),
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate(patch.encode("utf-8"))
        if proc.returncode != 0:
            raise LocalExecutionError((stderr or stdout).decode("utf-8", errors="replace").strip())
        return await self._with_git_artifacts(
            {
                "status": "success",
                "summary": "Applied patch",
                "stdout": stdout.decode("utf-8", errors="replace"),
                "stderr": stderr.decode("utf-8", errors="replace"),
                "exit_code": proc.returncode,
            }
        )

    async def _run_command(self, request: dict[str, Any]) -> dict[str, Any]:
        argv = request.get("argv")
        if not isinstance(argv, list) or not argv:
            raise LocalExecutionError("run_command requires argv list")
        program = str(argv[0])
        if program not in self._ALLOWED_COMMANDS:
            raise LocalExecutionError(f"Command not allowed: {program}")
        return await self._run_subprocess(argv, summary=f"Ran command: {' '.join(map(str, argv))}")

    async def _git_status(self, request: dict[str, Any]) -> dict[str, Any]:
        return await self._run_subprocess(
            ["git", "status", "--short"],
            summary="git status",
        )

    async def _git_diff(self, request: dict[str, Any]) -> dict[str, Any]:
        return await self._run_subprocess(
            ["git", "diff", "--no-ext-diff"],
            summary="git diff",
        )

    async def _run_tests(self, request: dict[str, Any]) -> dict[str, Any]:
        argv = request.get("argv")
        if not isinstance(argv, list) or not argv:
            argv = ["pytest", "-q"]
        return await self._run_command({"argv": argv})

    async def _run_subprocess(self, argv: list[str], *, summary: str) -> dict[str, Any]:
        proc = await asyncio.create_subprocess_exec(
            *argv,
            cwd=str(self._workspace_root),
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await proc.communicate()
        result = {
            "status": "success" if proc.returncode == 0 else "error",
            "summary": summary,
            "stdout": stdout.decode("utf-8", errors="replace"),
            "stderr": stderr.decode("utf-8", errors="replace"),
            "exit_code": proc.returncode,
        }
        if proc.returncode == 0:
            return await self._with_git_artifacts(result)
        result["error_message"] = result["stderr"] or result["stdout"] or summary
        return result

    async def _with_git_artifacts(self, result: dict[str, Any]) -> dict[str, Any]:
        status = subprocess.run(
            ["git", "status", "--short", "--untracked-files=all"],
            cwd=str(self._workspace_root),
            check=False,
            capture_output=True,
            text=True,
        )
        diff = subprocess.run(
            ["git", "diff", "--no-ext-diff"],
            cwd=str(self._workspace_root),
            check=False,
            capture_output=True,
            text=True,
        )
        changed_files: list[str] = []
        for line in status.stdout.splitlines():
            if not line.strip():
                continue
            changed_files.append(line[3:].strip() if len(line) > 3 else line.strip())
        result.setdefault("stdout", "")
        result["changed_files"] = changed_files
        result["diff"] = diff.stdout
        return result
