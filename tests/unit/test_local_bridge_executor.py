from __future__ import annotations

import subprocess

import pytest

from orchestrator.local_bridge.executor import LocalExecutionError, LocalWorkspaceExecutor


@pytest.mark.asyncio
async def test_local_bridge_write_file_and_git_artifacts(tmp_path) -> None:
    subprocess.run(["git", "init"], cwd=tmp_path, check=True, capture_output=True)
    executor = LocalWorkspaceExecutor(str(tmp_path))

    result = await executor.execute(
        {
            "metadata": {
                "tool_request": {
                    "tool": "write_file",
                    "path": "notes/output.txt",
                    "content": "hola\n",
                }
            }
        }
    )

    assert result["status"] == "success"
    assert "notes/output.txt" in result["changed_files"]
    assert "hola" in (tmp_path / "notes" / "output.txt").read_text(encoding="utf-8")


@pytest.mark.asyncio
async def test_local_bridge_rejects_path_escape(tmp_path) -> None:
    executor = LocalWorkspaceExecutor(str(tmp_path))

    with pytest.raises(LocalExecutionError):
        await executor.execute(
            {
                "metadata": {
                    "tool_request": {
                        "tool": "write_file",
                        "path": "../escape.txt",
                        "content": "nope",
                    }
                }
            }
        )
