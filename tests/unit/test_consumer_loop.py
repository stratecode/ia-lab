from __future__ import annotations

import pytest

from orchestrator.execution.consumer_loop import ConsumerLoop
from orchestrator.state_machine.transitions import AgentType


class _FakeRunner:
    def __init__(self) -> None:
        self.calls: list[dict[str, object]] = []

    async def execute(self, **kwargs):
        self.calls.append(kwargs)
        return {"status": "success", "output": "ok"}


class _FakeLoader:
    def __init__(self, payload: dict[str, object]) -> None:
        self.payload = payload

    async def load_task_context(self, task_id: str) -> dict[str, object]:
        result = dict(self.payload)
        result["task_id"] = task_id
        return result


@pytest.mark.asyncio
async def test_consumer_loop_uses_loaded_task_context() -> None:
    runner = _FakeRunner()
    loader = _FakeLoader(
        {
            "workspace_path": "/tmp/workspace",
            "description": "Fix the bug",
            "repo_path": "/srv/ai-lab/repos/project",
            "branch": "agent/test",
        }
    )
    loop = ConsumerLoop(
        redis_client=object(),  # type: ignore[arg-type]
        queue_service=object(),  # type: ignore[arg-type]
        event_bus=object(),  # type: ignore[arg-type]
        worker_id="worker-1",
        task_runner=runner,
        task_loader=loader,
    )

    result = await loop._execute_task("task-1", AgentType.CODER)

    assert result == {"status": "success", "output": "ok"}
    assert runner.calls == [
        {
            "task_id": "task-1",
            "agent_type": AgentType.CODER,
            "workspace_path": "/tmp/workspace",
            "description": "Fix the bug",
            "repo_path": "/srv/ai-lab/repos/project",
            "branch": "agent/test",
            "metadata": {},
        }
    ]


@pytest.mark.asyncio
async def test_consumer_loop_requires_workspace_path() -> None:
    runner = _FakeRunner()
    loader = _FakeLoader({"description": "No workspace"})
    loop = ConsumerLoop(
        redis_client=object(),  # type: ignore[arg-type]
        queue_service=object(),  # type: ignore[arg-type]
        event_bus=object(),  # type: ignore[arg-type]
        worker_id="worker-1",
        task_runner=runner,
        task_loader=loader,
    )

    with pytest.raises(ValueError, match="workspace_path"):
        await loop._execute_task("task-1", AgentType.CODER)
