"""Services for registering and dispatching work to local bridges."""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from typing import Any

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.approvals.manager import ApprovalManager
from orchestrator.orchestration.service import TaskLifecycleService
from orchestrator.persistence.models import Task
from orchestrator.persistence.repositories.local_bridges import LocalBridgeRepository
from orchestrator.state_machine.transitions import AgentType, ExecutionTarget, Priority, TaskState


@dataclass(frozen=True)
class BridgeTaskClaim:
    """Payload returned to a local bridge when claiming work."""

    task_id: str
    root_task_id: str | None
    parent_task_id: str | None
    description: str
    assigned_agent: str
    execution_target: str
    workspace_path: str
    workspace_root: str
    metadata: dict[str, Any]
    repo_path: str
    branch: str


class LocalBridgeService:
    """Registration, heartbeat, claiming, and result ingestion for local bridges."""

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        task_lifecycle: TaskLifecycleService,
        approval_manager: ApprovalManager,
    ) -> None:
        self._session_factory = session_factory
        self._task_lifecycle = task_lifecycle
        self._approval_manager = approval_manager

    async def register_bridge(
        self,
        *,
        bridge_id: str,
        name: str,
        hostname: str,
        workspace_root: str,
        capabilities: dict[str, Any] | None,
        api_key_name: str | None = None,
    ) -> dict[str, Any]:
        async with self._session_factory() as session:
            async with session.begin():
                repo = LocalBridgeRepository(session)
                bridge = await repo.upsert(
                    uuid.UUID(bridge_id),
                    name=name,
                    hostname=hostname,
                    workspace_root=workspace_root,
                    capabilities=capabilities,
                    api_key_name=api_key_name,
                )
                await session.refresh(bridge)
                return self._serialize_bridge(bridge)

    async def heartbeat(self, bridge_id: str, status: str = "active") -> dict[str, Any]:
        async with self._session_factory() as session:
            async with session.begin():
                repo = LocalBridgeRepository(session)
                bridge = await repo.touch_heartbeat(uuid.UUID(bridge_id), status=status)
                if bridge is None:
                    raise ValueError(f"Local bridge {bridge_id} not found")
                await session.refresh(bridge)
                return self._serialize_bridge(bridge)

    async def list_bridges(self) -> list[dict[str, Any]]:
        async with self._session_factory() as session:
            repo = LocalBridgeRepository(session)
            bridges = await repo.list_active()
        return [self._serialize_bridge(bridge) for bridge in bridges]

    async def claim_next_task(self, bridge_id: str) -> BridgeTaskClaim | None:
        async with self._session_factory() as session:
            bridge_repo = LocalBridgeRepository(session)
            bridge = await bridge_repo.get_by_id(uuid.UUID(bridge_id))
            if bridge is None:
                raise ValueError(f"Local bridge {bridge_id} not found")

            stmt = (
                select(Task)
                .where(
                    Task.execution_target == ExecutionTarget.LOCAL,
                    Task.assigned_agent == AgentType.CODER,
                    Task.state == TaskState.QUEUED,
                )
                .order_by(Task.created_at.asc(), Task.id.asc())
                .limit(25)
            )
            result = await session.execute(stmt)
            candidates = list(result.scalars().all())

        for task in candidates:
            metadata = task.metadata_ or {}
            workspace_root = str(metadata.get("workspace_root") or "").strip()
            if workspace_root and workspace_root != bridge.workspace_root:
                continue

            await self._task_lifecycle.on_task_started(
                task_id=str(task.id),
                agent_type=AgentType.CODER,
                worker_id=f"bridge:{bridge_id}",
            )
            return BridgeTaskClaim(
                task_id=str(task.id),
                root_task_id=str(task.root_task_id) if task.root_task_id else None,
                parent_task_id=str(task.parent_task_id) if task.parent_task_id else None,
                description=task.description,
                assigned_agent=str(task.assigned_agent),
                execution_target=str(task.execution_target),
                workspace_path=task.workspace_path or "",
                workspace_root=workspace_root or bridge.workspace_root,
                metadata=metadata,
                repo_path=str(metadata.get("repo_path") or metadata.get("workspace_root") or bridge.workspace_root),
                branch=str(metadata.get("branch") or "main"),
            )
        return None

    async def submit_result(
        self,
        bridge_id: str,
        task_id: str,
        result: dict[str, Any],
    ) -> None:
        async with self._session_factory() as session:
            task = await session.get(Task, uuid.UUID(task_id))
        if task is None or not task.assigned_agent:
            raise ValueError(f"Task {task_id} not found")

        status = str(result.get("status") or "success").lower()
        if status == "waiting_approval":
            action_type = str(result.get("action_type") or "local_bridge")
            target_resource = str(result.get("target_resource") or result.get("summary") or task.description[:200])
            timeout_seconds = int(result.get("timeout_seconds") or 300)
            async with self._session_factory() as session:
                async with session.begin():
                    db_task = await session.get(Task, uuid.UUID(task_id))
                    if db_task is not None:
                        db_task.results = result
            await self._approval_manager.request_approval(
                task_id=task_id,
                action_type=action_type,
                target_resource=target_resource,
                timeout_seconds=timeout_seconds,
            )
            return

        if status == "success":
            await self._task_lifecycle.on_task_finished(
                task_id=task_id,
                agent_type=AgentType(task.assigned_agent),
                result=result,
                worker_id=f"bridge:{bridge_id}",
            )
            return

        await self._task_lifecycle.on_task_failed(
            task_id=task_id,
            agent_type=AgentType(task.assigned_agent),
            error_message=str(result.get("error_message") or result.get("stderr") or "Local bridge execution failed"),
            worker_id=f"bridge:{bridge_id}",
        )

    @staticmethod
    def _serialize_bridge(bridge) -> dict[str, Any]:
        return {
            "id": str(bridge.id),
            "name": bridge.name,
            "hostname": bridge.hostname,
            "workspace_root": bridge.workspace_root,
            "status": bridge.status,
            "capabilities": bridge.capabilities or {},
            "api_key_name": bridge.api_key_name,
            "last_heartbeat": bridge.last_heartbeat.isoformat() if bridge.last_heartbeat else None,
            "created_at": bridge.created_at.isoformat() if bridge.created_at else None,
            "updated_at": bridge.updated_at.isoformat() if bridge.updated_at else None,
        }
