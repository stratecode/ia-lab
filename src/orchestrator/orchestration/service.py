"""Task orchestration and lifecycle services for Phase 5A."""

from __future__ import annotations

import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Protocol

import structlog
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.approvals.manager import ApprovalManager
from orchestrator.persistence.models import Task
from orchestrator.persistence.repositories.tasks import TaskRepository
from orchestrator.observability.metrics import (
    ROOT_TASK_DURATION_SECONDS,
    record_planner_subtask_created,
    record_root_task_created,
    record_task_created,
)
from orchestrator.queue.backpressure import BackpressureQueueService, QueueFullError
from orchestrator.scheduler.classifier import KeywordClassifier
from orchestrator.state_machine.engine import StateMachineEngine
from orchestrator.state_machine.transitions import (
    AgentType,
    Priority,
    TaskKind,
    TaskState,
    TERMINAL_STATES,
)

logger = structlog.get_logger(__name__)


class INotificationBot(Protocol):
    async def send_message(self, text: str, parse_mode: str = "Markdown") -> None: ...

    async def send_approval_request(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> None: ...


@dataclass(frozen=True)
class OrchestratedTaskRequest:
    description: str
    metadata: dict[str, Any]
    priority: Priority
    assigned_agent: AgentType | None
    idempotency_key: str | None
    entrypoint: str


class TaskLifecycleService:
    """Coordinates task state changes, queueing, approvals, and aggregation."""

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        state_machine: StateMachineEngine,
        queue_controller: BackpressureQueueService,
        classifier: KeywordClassifier,
        planner_service,
        approval_manager: ApprovalManager,
        resource_tracker,
        notification_bot: INotificationBot | None = None,
        workspace_root: str = "/srv/ai-lab/orchestrator/workspaces",
    ) -> None:
        self._session_factory = session_factory
        self._state_machine = state_machine
        self._queue_controller = queue_controller
        self._classifier = classifier
        self._planner_service = planner_service
        self._approval_manager = approval_manager
        self._resource_tracker = resource_tracker
        self._notification_bot = notification_bot
        self._workspace_root = workspace_root

    async def create_task(self, req: OrchestratedTaskRequest) -> Task:
        async with self._session_factory() as session:
            async with session.begin():
                repo = TaskRepository(session)
                if req.idempotency_key:
                    existing = await repo.get_by_idempotency_key(req.idempotency_key)
                    if existing is not None:
                        return existing

                agent_type = req.assigned_agent
                if agent_type is None:
                    classification = await self._classifier.classify(
                        req.description,
                        req.metadata,
                    )
                    agent_type = (
                        AgentType.PLANNER
                        if classification.agent_type == AgentType.PLANNER
                        else AgentType.CODER
                    )
                elif agent_type not in {AgentType.PLANNER, AgentType.CODER}:
                    raise ValueError(
                        f"Phase 5A only supports planner and coder, got {agent_type.value}"
                    )

                task = await repo.create(
                    description=req.description,
                    metadata={
                        **req.metadata,
                        "entrypoint": req.entrypoint,
                    },
                    priority=req.priority,
                    assigned_agent=agent_type,
                    idempotency_key=req.idempotency_key,
                    task_kind=TaskKind.ROOT,
                )
                task.root_task_id = task.id
                task.workspace_path = self._workspace_path(task.id)
                self._ensure_workspace_dir(task.workspace_path)
            task_id = str(task.id)

        record_task_created(agent_type.value, req.priority.value)
        record_root_task_created(req.entrypoint, agent_type.value)

        await self._queue_task(
            task_id=task_id,
            agent_type=agent_type,
            priority=req.priority,
            actor=f"{req.entrypoint}:create",
        )

        async with self._session_factory() as session:
            repo = TaskRepository(session)
            db_task = await repo.get_by_id(task.id)
            assert db_task is not None
            return db_task

    async def on_task_started(
        self,
        task_id: str,
        agent_type: AgentType,
        worker_id: str,
    ) -> None:
        await self._resource_tracker.reserve(agent_type)
        await self._state_machine.transition(
            task_id=task_id,
            target_state=TaskState.ASSIGNED,
            actor=f"worker:{worker_id}",
            reason=f"Dequeued for {agent_type.value}",
        )
        await self._state_machine.transition(
            task_id=task_id,
            target_state=TaskState.IN_PROGRESS,
            actor=f"worker:{worker_id}",
            reason="Execution started",
        )
        async with self._session_factory() as session:
            async with session.begin():
                task = await session.get(Task, uuid.UUID(task_id))
                if task is not None:
                    task.started_at = datetime.now(timezone.utc)

    async def on_task_finished(
        self,
        task_id: str,
        agent_type: AgentType,
        result: dict[str, Any],
        worker_id: str,
    ) -> None:
        try:
            if agent_type == AgentType.PLANNER:
                await self._handle_planner_success(task_id, result)
            else:
                await self._handle_coder_success(task_id, result)
        finally:
            await self._resource_tracker.release(agent_type)

    async def on_task_failed(
        self,
        task_id: str,
        agent_type: AgentType,
        error_message: str,
        worker_id: str,
    ) -> None:
        async with self._session_factory() as session:
            async with session.begin():
                task = await session.get(Task, uuid.UUID(task_id))
                if task is not None:
                    task.error_message = error_message
                    task.completed_at = datetime.now(timezone.utc)
        try:
            await self._state_machine.transition(
                task_id=task_id,
                target_state=TaskState.FAILED,
                actor=f"worker:{worker_id}",
                reason=error_message[:512],
            )
        finally:
            await self._resource_tracker.release(agent_type)
        await self._reconcile_root_task(task_id)

    async def list_children(self, task_id: uuid.UUID) -> list[Task]:
        async with self._session_factory() as session:
            repo = TaskRepository(session)
            return await repo.list_children(task_id)

    async def resume_approved_task(self, task_id: str) -> None:
        task = await self._get_task(task_id)
        if task is None or not task.assigned_agent:
            raise ValueError(f"Approved task {task_id} has no assigned_agent")
        await self._queue_controller.enqueue_with_backpressure(
            task_id=task_id,
            agent_type=AgentType(task.assigned_agent),
            priority=Priority(task.priority),
        )
        async with self._session_factory() as session:
            async with session.begin():
                db_task = await session.get(Task, uuid.UUID(task_id))
                if db_task is not None:
                    db_task.queued_at = datetime.now(timezone.utc)

    async def get_task_tree(self, task_id: uuid.UUID) -> dict[str, Any]:
        async with self._session_factory() as session:
            repo = TaskRepository(session)
            root = await repo.get_by_id(task_id)
            if root is None:
                raise ValueError(f"Task {task_id} not found")
            root_id = root.root_task_id or root.id
            tasks = await repo.list_by_root(root_id)

        items = [self._serialize_task(task) for task in tasks]
        by_parent: dict[str, list[dict[str, Any]]] = {}
        root_item: dict[str, Any] | None = None
        for item in items:
            if item["id"] == str(root_id):
                root_item = item
            parent_id = item.get("parent_task_id")
            if parent_id:
                by_parent.setdefault(parent_id, []).append(item)

        def attach(node: dict[str, Any]) -> dict[str, Any]:
            children = [attach(child) for child in by_parent.get(node["id"], [])]
            return {**node, "children": children}

        if root_item is None:
            raise ValueError(f"Root task {root_id} not found")
        return attach(root_item)

    async def get_pending_approvals(self, limit: int = 20) -> list[dict[str, Any]]:
        from orchestrator.persistence.repositories.approvals import ApprovalRepository

        async with self._session_factory() as session:
            repo = ApprovalRepository(session)
            approvals = await repo.list_pending(limit=limit)
        return [
            {
                "id": str(approval.id),
                "task_id": str(approval.task_id),
                "action_type": approval.action_type,
                "target_resource": approval.target_resource,
                "timeout_seconds": approval.timeout_seconds,
            }
            for approval in approvals
        ]

    async def _handle_planner_success(self, task_id: str, result: dict[str, Any]) -> None:
        plan_only = bool(result.get("plan_only", False))
        async with self._session_factory() as session:
            async with session.begin():
                task = await session.get(Task, uuid.UUID(task_id))
                if task is None:
                    raise ValueError(f"Task {task_id} not found")
                task.results = result

        subtasks_payload = list(result.get("subtasks") or [])
        if plan_only or not subtasks_payload:
            await self._mark_completed(task_id, "planner completed without execution subtasks")
            return

        created_subtask_ids: list[str] = []
        for idx, payload in enumerate(subtasks_payload, start=1):
            assigned_agent = AgentType(payload.get("assigned_agent", AgentType.CODER))
            priority = Priority(payload.get("priority", Priority.NORMAL))
            requires_approval = bool(payload.get("requires_approval", False))
            description = str(payload.get("description") or payload.get("title") or "").strip()
            title = str(payload.get("title") or f"Step {idx}").strip()

            async with self._session_factory() as session:
                async with session.begin():
                    repo = TaskRepository(session)
                    root_task = await repo.get_by_id(uuid.UUID(task_id))
                    assert root_task is not None
                    child = await repo.create(
                        description=description,
                        metadata={
                            "title": title,
                            "requires_approval": requires_approval,
                            "planner_generated": True,
                            "parent_task_id": task_id,
                        },
                        priority=priority,
                        assigned_agent=assigned_agent,
                        parent_task_id=root_task.id,
                        root_task_id=root_task.root_task_id or root_task.id,
                        task_kind=TaskKind.PLAN_STEP,
                    )
                    child.workspace_path = self._workspace_path(
                        root_task.root_task_id or root_task.id,
                        child.id,
                    )
                    self._ensure_workspace_dir(child.workspace_path)
                    child_id = str(child.id)
            created_subtask_ids.append(child_id)

            if requires_approval:
                await self._approval_manager.request_approval(
                    task_id=child_id,
                    action_type="planner_subtask",
                    target_resource=title,
                    timeout_seconds=300,
                )
            else:
                await self._queue_task(
                    task_id=child_id,
                    agent_type=assigned_agent,
                    priority=priority,
                    actor="planner:create_subtask",
                )
            record_planner_subtask_created(assigned_agent.value, requires_approval)

        async with self._session_factory() as session:
            async with session.begin():
                task = await session.get(Task, uuid.UUID(task_id))
                if task is not None:
                    current = dict(task.results or {})
                    current["created_subtask_ids"] = created_subtask_ids
                    task.results = current

    async def _handle_coder_success(self, task_id: str, result: dict[str, Any]) -> None:
        async with self._session_factory() as session:
            async with session.begin():
                task = await session.get(Task, uuid.UUID(task_id))
                if task is not None:
                    task.results = result
                    task.completed_at = datetime.now(timezone.utc)
        await self._mark_completed(task_id, "coder execution completed")
        await self._reconcile_root_task(task_id)

    async def _mark_completed(self, task_id: str, reason: str) -> None:
        task = await self._get_task(task_id)
        if task is None:
            return
        if TaskState(task.state) == TaskState.COMPLETED:
            return
        completed_at = datetime.now(timezone.utc)
        async with self._session_factory() as session:
            async with session.begin():
                db_task = await session.get(Task, uuid.UUID(task_id))
                if db_task is not None:
                    db_task.completed_at = completed_at
        await self._state_machine.transition(
            task_id=task_id,
            target_state=TaskState.COMPLETED,
            actor="orchestrator:lifecycle",
            reason=reason,
        )
        if task.parent_task_id is None:
            self._record_root_duration(task, "completed")

    async def _queue_task(
        self,
        task_id: str,
        agent_type: AgentType,
        priority: Priority,
        actor: str,
    ) -> None:
        task = await self._get_task(task_id)
        if task is None:
            raise ValueError(f"Task {task_id} not found")
        try:
            await self._queue_controller.enqueue_with_backpressure(
                task_id=task_id,
                agent_type=agent_type,
                priority=priority,
            )
        except QueueFullError as exc:
            async with self._session_factory() as session:
                async with session.begin():
                    db_task = await session.get(Task, uuid.UUID(task_id))
                    if db_task is not None:
                        db_task.error_message = str(exc)
            await self._state_machine.transition(
                task_id=task_id,
                target_state=TaskState.FAILED,
                actor=actor,
                reason=str(exc),
            )
            raise

        await self._state_machine.transition(
            task_id=task_id,
            target_state=TaskState.QUEUED,
            actor=actor,
            reason=f"Queued for {agent_type.value}",
        )
        async with self._session_factory() as session:
            async with session.begin():
                db_task = await session.get(Task, uuid.UUID(task_id))
                if db_task is not None:
                    db_task.queued_at = datetime.now(timezone.utc)

    async def _reconcile_root_task(self, task_id: str) -> None:
        task = await self._get_task(task_id)
        if task is None:
            return
        root_id = task.root_task_id or task.id
        if root_id == task.id and task.parent_task_id is None:
            return

        async with self._session_factory() as session:
            stmt = select(Task).where(Task.root_task_id == root_id)
            result = await session.execute(stmt)
            tasks = list(result.scalars().all())

        root_task = next((item for item in tasks if item.id == root_id), None)
        child_tasks = [item for item in tasks if item.id != root_id]
        if root_task is None or not child_tasks:
            return

        child_states = {TaskState(child.state) for child in child_tasks}
        if any(state in {TaskState.FAILED, TaskState.CANCELLED} for state in child_states):
            if TaskState(root_task.state) not in TERMINAL_STATES:
                self._record_root_duration(root_task, "failed")
                await self._state_machine.transition(
                    task_id=str(root_id),
                    target_state=TaskState.FAILED,
                    actor="orchestrator:aggregate",
                    reason="One or more subtasks failed",
                )
            return

        if all(state == TaskState.COMPLETED for state in child_states):
            if TaskState(root_task.state) != TaskState.COMPLETED:
                async with self._session_factory() as session:
                    async with session.begin():
                        db_root = await session.get(Task, root_id)
                        if db_root is not None:
                            db_root.completed_at = datetime.now(timezone.utc)
                self._record_root_duration(root_task, "completed")
                await self._state_machine.transition(
                    task_id=str(root_id),
                    target_state=TaskState.COMPLETED,
                    actor="orchestrator:aggregate",
                    reason="All subtasks completed",
                )

    async def _get_task(self, task_id: str) -> Task | None:
        async with self._session_factory() as session:
            repo = TaskRepository(session)
            return await repo.get_by_id(uuid.UUID(task_id))

    def _workspace_path(self, root_task_id: uuid.UUID, task_id: uuid.UUID | None = None) -> str:
        actual_task_id = task_id or root_task_id
        return str(Path(self._workspace_root) / str(root_task_id) / str(actual_task_id))

    @staticmethod
    def _ensure_workspace_dir(workspace_path: str) -> None:
        Path(workspace_path).mkdir(parents=True, exist_ok=True)

    @staticmethod
    def _record_root_duration(task: Task, final_state: str) -> None:
        if task.created_at is None:
            return
        duration = (datetime.now(timezone.utc) - task.created_at).total_seconds()
        ROOT_TASK_DURATION_SECONDS.labels(final_state=final_state).observe(max(duration, 0.0))

    @staticmethod
    def _serialize_task(task: Task) -> dict[str, Any]:
        return {
            "id": str(task.id),
            "state": str(task.state),
            "description": task.description,
            "metadata": task.metadata_ or {},
            "assigned_agent": str(task.assigned_agent) if task.assigned_agent else None,
            "priority": str(task.priority),
            "task_kind": str(task.task_kind),
            "parent_task_id": str(task.parent_task_id) if task.parent_task_id else None,
            "root_task_id": str(task.root_task_id) if task.root_task_id else None,
            "workspace_path": task.workspace_path,
            "retry_count": task.retry_count,
            "correlation_id": str(task.correlation_id),
            "created_at": task.created_at.isoformat() if task.created_at else None,
            "updated_at": task.updated_at.isoformat() if task.updated_at else None,
            "started_at": task.started_at.isoformat() if task.started_at else None,
            "completed_at": task.completed_at.isoformat() if task.completed_at else None,
            "results": task.results,
            "error_message": task.error_message,
        }
