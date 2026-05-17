from __future__ import annotations

import pytest

from orchestrator.execution.runner import TaskRunner
from orchestrator.state_machine.transitions import AgentType


class _FakePlannerService:
    async def create_plan(self, description: str, metadata: dict | None = None):
        class _Subtask:
            def __init__(self) -> None:
                self.title = "Implement feature"
                self.description = "Write the code"
                self.assigned_agent = AgentType.CODER
                self.priority = "normal"
                self.requires_approval = False

            def model_dump(self, mode: str = "json") -> dict:
                return {
                    "title": self.title,
                    "description": self.description,
                    "assigned_agent": self.assigned_agent.value,
                    "priority": self.priority,
                    "requires_approval": self.requires_approval,
                }

        class _Result:
            summary = "A simple plan"
            plan = "1. Write code"
            subtasks = [_Subtask()]
            raw_response = '{"summary":"A simple plan"}'

        return _Result()


@pytest.mark.asyncio
async def test_task_runner_supports_planner_tasks() -> None:
    runner = TaskRunner(planner_service=_FakePlannerService())

    result = await runner.execute(
        task_id="task-123",
        agent_type=AgentType.PLANNER,
        workspace_path="/tmp",
        description="Plan the feature",
        metadata={"plan_only": True},
    )

    assert result["status"] == "success"
    assert result["plan_summary"] == "A simple plan"
    assert result["plan_only"] is True
    assert len(result["subtasks"]) == 1
