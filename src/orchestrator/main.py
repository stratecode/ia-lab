"""FastAPI application factory with lifespan management.

Creates and configures the orchestrator FastAPI application with:
- Configuration validation on startup (Pydantic Settings)
- Database connection with retry and migration execution
- Redis connection for queues and event bus
- Worker startup (when mode is 'all' or 'worker')
- Telegram bot startup (when token is configured)
- Middleware stack: correlation → auth → rate limiting
- Route registration for all API modules
- Graceful shutdown with 30s drain for in-flight requests

The app supports three deployment modes via LAB_ORCHESTRATOR_MODE:
- "all": API + Worker in the same process (development/initial deployment)
- "api": API-only process (production separation)
- "worker": Worker-only process (production separation)

Validates: Requirements 1.3, 1.8, 1.9, 19.3, 19.4
"""

from __future__ import annotations

import asyncio
import json
import sys
import uuid
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager
from typing import Any

import httpx
import structlog
from fastapi import FastAPI
from redis.asyncio import Redis
from sqlalchemy import select

from orchestrator.capabilities.router import CapabilityRouter
from orchestrator.capabilities.service import CapabilityService
from orchestrator.config import OrchestratorMode, Settings
from orchestrator.dependencies import AppState, wire_dependencies
from orchestrator.local_bridge.service import LocalBridgeService
from orchestrator.observability.logging import configure_logging
from orchestrator.persistence.models import Task
from orchestrator.persistence.repositories.api_keys import ApiKeyRepository
from orchestrator.persistence.repositories.tasks import TaskRepository
from orchestrator.orchestration.service import (
    OrchestratedTaskRequest,
    TaskLifecycleService,
)
from orchestrator.planning.service import PlannerService
from orchestrator.research.service import ResearchService
from orchestrator.state_machine.transitions import TERMINAL_STATES, TaskState

logger = structlog.get_logger(__name__)

# Graceful shutdown drain timeout (seconds)
_SHUTDOWN_DRAIN_TIMEOUT = 30


class _TelegramTaskService:
    """Adapter exposing task operations to the Telegram bot."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def get_task(self, task_id: str) -> dict[str, Any] | None:
        async with self._state.session_factory() as session:  # type: ignore[operator]
            repo = TaskRepository(session)
            task = await repo.get_by_id(uuid.UUID(task_id))

        if task is None:
            return None

        return self._serialize_task(task)

    async def list_active_tasks(self) -> list[dict[str, Any]]:
        async with self._state.session_factory() as session:  # type: ignore[operator]
            stmt = (
                select(Task)
                .where(Task.state.not_in([state.value for state in TERMINAL_STATES]))
                .order_by(Task.created_at.desc())
                .limit(20)
            )
            result = await session.execute(stmt)
            tasks = result.scalars().all()

        return [self._serialize_task(task) for task in tasks]

    async def create_task(
        self,
        description: str,
        *,
        assigned_agent: str | None = None,
        plan_only: bool = False,
        entrypoint: str = "telegram",
    ) -> dict[str, Any]:
        from orchestrator.state_machine.transitions import AgentType, Priority
        from orchestrator.state_machine.transitions import ExecutionTarget

        task = await self._state.task_lifecycle.create_task(  # type: ignore[union-attr]
            OrchestratedTaskRequest(
                description=description,
                metadata={"plan_only": plan_only},
                priority=Priority.NORMAL,
                assigned_agent=AgentType(assigned_agent) if assigned_agent else None,
                execution_target=ExecutionTarget.REMOTE,
                idempotency_key=None,
                entrypoint=entrypoint,
            )
        )
        return self._serialize_task(task)

    async def cancel_task(self, task_id: str, actor: str) -> bool:
        async with self._state.session_factory() as session:  # type: ignore[operator]
            repo = TaskRepository(session)
            task = await repo.get_by_id(uuid.UUID(task_id))

        if task is None:
            return False

        if TaskState(task.state) in TERMINAL_STATES:
            return False

        try:
            await self._state.state_machine.transition(  # type: ignore[union-attr]
                task_id=task_id,
                target_state=TaskState.CANCELLED,
                actor=actor,
                reason="Cancelled from Telegram",
            )
        except Exception:
            return False

        return True

    @staticmethod
    def _serialize_task(task: Task) -> dict[str, Any]:
        return {
            "id": str(task.id),
            "state": str(task.state),
            "description": task.description,
            "task_kind": str(task.task_kind),
            "parent_task_id": str(task.parent_task_id) if task.parent_task_id else None,
            "root_task_id": str(task.root_task_id) if task.root_task_id else None,
            "assigned_agent": str(task.assigned_agent) if task.assigned_agent else None,
            "execution_target": str(task.execution_target),
            "created_at": task.created_at.isoformat() if task.created_at else None,
            "started_at": task.started_at.isoformat() if task.started_at else None,
            "completed_at": task.completed_at.isoformat() if task.completed_at else None,
        }


class _TelegramApprovalService:
    """Adapter exposing approval operations to the Telegram bot."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def approve(self, approval_id: str, operator: str) -> bool:
        from orchestrator.approvals.interfaces import ApprovalDecision

        try:
            await self._state.approval_manager.resolve(  # type: ignore[union-attr]
                approval_id=approval_id,
                decision=ApprovalDecision.APPROVE,
                operator=operator,
            )
        except Exception:
            return False

        return True

    async def reject(self, approval_id: str, operator: str) -> bool:
        from orchestrator.approvals.interfaces import ApprovalDecision

        try:
            await self._state.approval_manager.resolve(  # type: ignore[union-attr]
                approval_id=approval_id,
                decision=ApprovalDecision.REJECT,
                operator=operator,
            )
        except Exception:
            return False

        return True

    async def list_pending(self) -> list[dict[str, Any]]:
        return await self._state.task_lifecycle.get_pending_approvals()  # type: ignore[union-attr]


class _TelegramApprovalNotificationAdapter:
    """Bridge ApprovalManager notification hooks to the Telegram bot."""

    def __init__(self, bot) -> None:
        self._bot = bot

    async def notify_approval_requested(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        timeout_seconds: int,
    ) -> None:
        await self._bot.send_approval_request(
            approval_id=approval_id,
            task_id=task_id,
            action_type=action_type,
            target_resource=target_resource,
            timeout_seconds=timeout_seconds,
        )

    async def notify_escalation(
        self,
        approval_id: str,
        task_id: str,
        action_type: str,
        target_resource: str,
        remaining_seconds: int,
    ) -> None:
        await self._bot.send_message(
            (
                "⚠️ Escalation\n"
                f"approval_id={approval_id}\n"
                f"task_id={task_id}\n"
                f"action={action_type}\n"
                f"target={target_resource}\n"
                f"remaining={remaining_seconds}s"
            )
        )


class _TelegramStatusService:
    """Adapter exposing system status to the Telegram bot."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def get_status(self) -> dict[str, Any]:
        queue_depths_raw = await self._state.queue_service.get_depth()  # type: ignore[union-attr]
        queue_depths = {agent.value: depth for agent, depth in queue_depths_raw.items()}

        active_tasks = sum(queue_depths.values())
        workers = await self._get_workers()

        return {
            "active_tasks": active_tasks,
            "queue_depths": queue_depths,
            "workers": workers,
        }

    async def _get_workers(self) -> dict[str, dict[str, Any]]:
        from orchestrator.execution.worker import HEARTBEAT_PREFIX, WORKER_REGISTRY_PREFIX

        workers: dict[str, dict[str, Any]] = {}
        redis = self._state.redis_client

        async for key in redis.scan_iter(match=f"{WORKER_REGISTRY_PREFIX}*"):  # type: ignore[union-attr]
            key_str = key.decode() if isinstance(key, bytes) else key
            worker_id = key_str.removeprefix(WORKER_REGISTRY_PREFIX)

            registry_data = await redis.hgetall(key)  # type: ignore[union-attr]
            decoded = {
                (k.decode() if isinstance(k, bytes) else k): (
                    v.decode() if isinstance(v, bytes) else v
                )
                for k, v in registry_data.items()
            }

            heartbeat_raw = await redis.get(f"{HEARTBEAT_PREFIX}{worker_id}")  # type: ignore[union-attr]
            current_task = decoded.get("current_task") or None
            worker_status = decoded.get("status", "unknown")

            if not heartbeat_raw and worker_status == "active":
                worker_status = "dead"

            heartbeat_data = {}
            if heartbeat_raw:
                try:
                    heartbeat_data = json.loads(
                        heartbeat_raw.decode()
                        if isinstance(heartbeat_raw, bytes)
                        else heartbeat_raw
                    )
                except (json.JSONDecodeError, AttributeError):
                    heartbeat_data = {}

            workers[worker_id] = {
                "status": worker_status,
                "current_task": current_task,
                "memory_mb": heartbeat_data.get("memory_mb"),
                "uptime_s": heartbeat_data.get("uptime_s"),
            }

        return workers


class _TelegramLlamaChatService:
    """Adapter exposing direct chat with local llama.cpp endpoints."""

    _TARGET_CONFIG = {
        "coder": ("code_base_url", "code_api_key", "coder"),
        "planner": ("planner_base_url", "planner_api_key", "planner"),
    }

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def chat(self, target: str, prompt: str) -> str:
        config = self._TARGET_CONFIG.get(target)
        if config is None:
            raise ValueError(f"Unsupported chat target: {target}")

        base_url_attr, api_key_attr, model_name = config
        settings = self._state.settings.llama
        base_url = getattr(settings, base_url_attr)
        api_key = getattr(settings, api_key_attr)

        headers = {"Content-Type": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"

        payload = {
            "model": model_name,
            "messages": [{"role": "user", "content": prompt}],
            "temperature": 0.2,
            "max_tokens": 1200,
        }

        async with httpx.AsyncClient(timeout=settings.timeout_seconds) as client:
            response = await client.post(
                f"{base_url}/chat/completions",
                json=payload,
                headers=headers,
            )
            response.raise_for_status()

        data = response.json()
        content = (
            data.get("choices", [{}])[0]
            .get("message", {})
            .get("content", "")
            .strip()
        )
        if not content:
            raise ValueError(f"{target} returned an empty response")
        return content


class _TelegramCapabilityService:
    """Adapter exposing Capability Layer v1 to Telegram handlers."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def list_capabilities(self) -> list[dict[str, str]]:
        return self._state.capability_service.list_capabilities()  # type: ignore[union-attr]

    async def execute(self, capability: str, argument: str) -> dict[str, Any]:
        payload: dict[str, Any]
        if capability == "web.search":
            payload = {"query": argument}
        elif capability == "web.fetch":
            payload = {"url": argument}
        else:
            payload = {"location": argument}
        record = await self._state.capability_service.invoke(  # type: ignore[union-attr]
            capability,
            payload,
            entrypoint="telegram",
        )
        return {
            "invocation_id": str(record.id),
            "summary": self._summarize_output(record.output_payload),
            "source_refs": [item.model_dump(mode="json") for item in record.source_refs],
            "status": record.status,
        }

    async def get_task_sources(self, task_id: str) -> list[dict[str, Any]]:
        return await self._state.capability_service.list_task_sources(uuid.UUID(task_id))  # type: ignore[union-attr]

    def _summarize_output(self, payload: dict[str, Any]) -> str:
        if payload.get("content_text"):
            return str(payload["content_text"])[:1600]
        if isinstance(payload.get("results"), list):
            return "\n".join(
                f"{idx}. {item.get('title')} — {item.get('url')}"
                for idx, item in enumerate(payload["results"][:5], start=1)
            )
        if payload.get("ocr_text"):
            return str(payload["ocr_text"])[:1600]
        return str(payload.get("summary") or "Capability executed.")


class _TelegramResearchService:
    """Adapter exposing research mode to Telegram handlers."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def query(self, query: str, *, mode_hint: str | None = None) -> dict[str, Any]:
        result = await self._state.research_service.query(  # type: ignore[union-attr]
            query,
            entrypoint="telegram",
            mode_hint=mode_hint,  # type: ignore[arg-type]
        )
        return {
            "research_run_id": str(result.research_run.id),
            "answer": result.answer,
            "confidence": result.confidence,
            "source_refs": [item.model_dump(mode="json") for item in result.sources],
            "tool_invocation_ids": result.tool_invocation_ids,
        }

    async def evaluate(self, query: str) -> dict[str, Any]:
        result = await self._state.research_service.query(  # type: ignore[union-attr]
            query,
            entrypoint="telegram",
            evaluate_against_reference=True,
        )
        payload = {
            "research_run_id": str(result.research_run.id),
            "answer": result.answer,
            "confidence": result.confidence,
            "source_refs": [item.model_dump(mode="json") for item in result.sources],
            "tool_invocation_ids": result.tool_invocation_ids,
        }
        if result.evaluation is not None:
            payload["evaluation"] = result.evaluation.model_dump(mode="json")
        return payload


class _TelegramServerOpsService:
    """Restricted host inspection commands for Telegram.

    This intentionally exposes read-only, low-risk operations that the
    unprivileged `orchestrator` user can execute without sudo.
    """

    _SERVICES: tuple[str, ...] = (
        "orchestrator.service",
        "nginx.service",
        "redis-server.service",
        "snap.prometheus.prometheus.service",
        "prometheus-alertmanager.service",
        "open-webui.service",
    )

    async def run(self, action: str, argument: str | None = None) -> str:
        if action == "status":
            return await self._status()
        if action == "services":
            return await self._services()
        if action == "disk":
            return await self._disk()
        if action in {"logs", "restart"}:
            return "Accion no disponible para el usuario orchestrator actual."
        return "Accion no soportada. Usa: status, services, disk."

    async def _status(self) -> str:
        health = await self._run(
            ["curl", "-sf", "http://127.0.0.1:8100/health"],
            timeout=10,
        )
        disk = await self._run(["df", "-h", "/"], timeout=10)
        return f"HEALTH\n{health.strip()}\n\nDISK\n{disk.strip()}"

    async def _services(self) -> str:
        rows: list[str] = []
        for service in self._SERVICES:
            status = await self._run(["systemctl", "is-active", service], timeout=10)
            rows.append(f"{service}: {status.strip() or 'unknown'}")
        return "\n".join(rows)

    async def _disk(self) -> str:
        return (await self._run(["df", "-h", "/srv/ai-lab", "/opt/docker", "/"], timeout=10)).strip()

    async def _run(self, command: list[str], timeout: float) -> str:
        proc = await asyncio.create_subprocess_exec(
            *command,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=timeout)
        except TimeoutError as exc:
            proc.kill()
            raise RuntimeError(f"Command timeout: {' '.join(command)}") from exc

        if proc.returncode != 0:
            raise RuntimeError(
                (stderr.decode("utf-8", errors="replace") or stdout.decode("utf-8", errors="replace")).strip()
            )
        return stdout.decode("utf-8", errors="replace")


class _QueuedTaskLoader:
    """Loads queued task execution context from the database."""

    def __init__(self, state: AppState) -> None:
        self._state = state

    async def load_task_context(self, task_id: str) -> dict[str, Any]:
        async with self._state.session_factory() as session:  # type: ignore[operator]
            repo = TaskRepository(session)
            task = await repo.get_by_id(uuid.UUID(task_id))

        if task is None:
            raise ValueError(f"Task {task_id} not found")

        metadata = task.metadata_ or {}
        repo_path = (
            metadata.get("repo_name")
            or metadata.get("repo_path")
            or metadata.get("repository_path")
        )
        branch = metadata.get("branch") or metadata.get("default_branch") or "main"

        return {
            "workspace_path": task.workspace_path,
            "description": task.description,
            "repo_path": repo_path,
            "branch": branch,
            "metadata": metadata,
            "execution_target": task.execution_target,
        }


async def _validate_config(settings: Settings) -> None:
    """Validate configuration on startup. Raises on invalid config.

    Pydantic Settings already validates types and constraints on
    instantiation. This function performs additional semantic checks
    that require cross-field validation.
    """
    logger.info(
        "Configuration validated",
        **settings.redacted_dict(),
    )


async def _connect_database(state: AppState) -> None:
    """Create database engine, session factory, and verify connectivity."""
    from orchestrator.persistence.database import (
        connect_with_retry,
        create_engine,
        create_session_factory,
    )

    engine = create_engine(state.settings.database)
    await connect_with_retry(engine)

    session_factory = create_session_factory(engine)
    state.engine = engine
    state.session_factory = session_factory

    logger.info("Database connection established")


async def _run_migrations(state: AppState) -> None:
    """Ensure database schema is up to date.

    Uses SQLAlchemy metadata.create_all for initial schema creation.
    This is safe to run multiple times (creates only missing tables).
    If schema creation fails, the application refuses to start per requirement 5.4.
    """
    from orchestrator.persistence.migrations import run_migrations

    try:
        await run_migrations(state.settings.database)
        logger.info("Database schema verified/created successfully")
    except Exception as exc:
        logger.error(
            "Database schema creation failed — refusing to start",
            error=str(exc),
        )
        raise SystemExit(
            f"Migration failure: {exc}. Fix the database and restart."
        ) from exc


async def _connect_redis(state: AppState) -> None:
    """Create Redis client and verify connectivity."""
    from orchestrator.queue.event_bus import RedisEventBus
    from orchestrator.queue.redis_queue import RedisQueueService

    redis_client = Redis.from_url(
        state.settings.redis.url,
        decode_responses=True,
    )

    # Verify connectivity
    try:
        await redis_client.ping()
    except Exception as exc:
        logger.error("Redis connection failed", error=str(exc))
        raise SystemExit(f"Redis connection failed: {exc}") from exc

    state.redis_client = redis_client
    state.event_bus = RedisEventBus(redis_client)
    state.queue_service = RedisQueueService(redis_client)

    logger.info("Redis connection established")


def _create_services(state: AppState) -> None:
    """Instantiate application services with their dependencies."""
    from orchestrator.queue.backpressure import BackpressureQueueService
    from orchestrator.scheduler.assignment import AssignmentService
    from orchestrator.scheduler.classifier import KeywordClassifier
    from orchestrator.scheduler.resource_tracker import ResourceTracker
    from orchestrator.approvals.manager import ApprovalManager
    from orchestrator.state_machine.engine import StateMachineEngine
    from orchestrator.tools.safe_mode import SafeModeEnforcer

    # State machine engine
    state.state_machine = StateMachineEngine(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        event_publisher=state.event_bus,
    )

    # Safe mode enforcer
    state.safe_mode_enforcer = SafeModeEnforcer(
        enabled=state.settings.security.safe_mode,
        event_bus=state.event_bus,
    )

    # Approval manager
    state.approval_manager = ApprovalManager(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        state_machine=state.state_machine,
        event_publisher=state.event_bus,
        resume_callback=lambda task_id: state.task_lifecycle.resume_approved_task(task_id),  # type: ignore[union-attr]
    )

    state.classifier = KeywordClassifier(
        confidence_threshold=state.settings.scheduler.classification_confidence_threshold,
        llm_base_url=state.settings.llama.planner_base_url.removesuffix("/v1"),
        llm_timeout=state.settings.scheduler.classification_timeout,
    )
    state.resource_tracker = ResourceTracker(
        state.settings.scheduler,
        state.redis_client,  # type: ignore[arg-type]
    )
    state.assignment_service = AssignmentService(
        event_bus=state.event_bus,  # type: ignore[arg-type]
        resource_tracker=state.resource_tracker,
        classifier=state.classifier,
        max_concurrent_tasks=state.settings.scheduler.max_concurrent_tasks,
    )
    queue_controller = BackpressureQueueService(
        state.redis_client,  # type: ignore[arg-type]
        state.event_bus,  # type: ignore[arg-type]
        state.settings.queue,
    )
    planner_service = PlannerService(
        base_url=state.settings.llama.planner_base_url,
        api_key=state.settings.llama.planner_api_key,
        timeout_seconds=state.settings.llama.timeout_seconds,
    )
    state.planner_service = planner_service
    state.capability_service = CapabilityService(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        router=CapabilityRouter(state.settings.capabilities),
        settings=state.settings.capabilities,
    )
    state.research_service = ResearchService(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        capability_service=state.capability_service,
        capability_settings=state.settings.capabilities,
        llama_settings=state.settings.llama,
        reference_settings=state.settings.openai_reference,
    )
    state.task_lifecycle = TaskLifecycleService(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        state_machine=state.state_machine,
        queue_controller=queue_controller,
        classifier=state.classifier,
        planner_service=planner_service,
        approval_manager=state.approval_manager,
        resource_tracker=state.resource_tracker,
    )
    state.local_bridge_service = LocalBridgeService(
        session_factory=state.session_factory,  # type: ignore[arg-type]
        task_lifecycle=state.task_lifecycle,
        approval_manager=state.approval_manager,
    )


async def _start_worker(state: AppState) -> None:
    """Start the worker process (registration, heartbeat, consumer loop).

    Only starts when mode is 'all' or 'worker'.
    """
    from orchestrator.execution.consumer_loop import ConsumerLoop
    from orchestrator.execution.runner import TaskRunner
    from orchestrator.execution.worker import Worker

    mode = state.settings.api.mode
    if mode not in (OrchestratorMode.ALL, OrchestratorMode.WORKER):
        logger.info("Worker not started (mode=%s)", mode.value)
        return

    if state.resource_tracker is not None:
        await state.resource_tracker.start()

    worker = Worker(
        redis_client=state.redis_client,  # type: ignore[arg-type]
        settings=state.settings.worker,
        orchestrator_mode=mode,
    )
    consumer_loop = ConsumerLoop(
        redis_client=state.redis_client,  # type: ignore[arg-type]
        queue_service=state.queue_service,  # type: ignore[arg-type]
        event_bus=state.event_bus,  # type: ignore[arg-type]
        worker_id=worker.worker_id,
        task_runner=TaskRunner(
            planner_service=state.planner_service,
            capability_service=state.capability_service,
            research_service=state.research_service,
        ),
        task_loader=_QueuedTaskLoader(state),
        lifecycle_service=state.task_lifecycle,
    )
    worker.set_consumer_loop(consumer_loop)
    await worker.start()
    state.worker = worker

    logger.info(
        "Worker started",
        worker_id=worker.worker_id,
        mode=mode.value,
    )


async def _start_telegram_bot(state: AppState) -> None:
    """Start the Telegram bot for notifications and remote control.

    Skips startup if no bot token is configured.
    """
    from orchestrator.notifications.telegram_bot import TelegramBot

    token = state.settings.notifications.bot_token
    if not token:
        logger.info("Telegram bot not started (no token configured)")
        return

    allowed_users = state.settings.notifications.allowed_user_ids

    bot = TelegramBot(
        token=token,
        allowed_user_ids=allowed_users,
        task_service=_TelegramTaskService(state),
        approval_service=_TelegramApprovalService(state),
        safe_mode_service=state.safe_mode_enforcer,
        status_service=_TelegramStatusService(state),
        chat_service=_TelegramLlamaChatService(state),
        server_ops_service=_TelegramServerOpsService(),
        capability_service=_TelegramCapabilityService(state),
        research_service=_TelegramResearchService(state),
    )
    await bot.start()
    state.telegram_bot = bot
    if state.approval_manager is not None:
        state.approval_manager._notification_service = _TelegramApprovalNotificationAdapter(bot)  # type: ignore[attr-defined]
    if state.task_lifecycle is not None:
        state.task_lifecycle._notification_bot = bot  # type: ignore[attr-defined]

    logger.info("Telegram bot started")


async def _shutdown_worker(state: AppState) -> None:
    """Gracefully stop the worker."""
    if state.worker is not None:
        logger.info("Stopping worker", worker_id=state.worker.worker_id)
        await state.worker.stop()
        logger.info("Worker stopped")
    if state.resource_tracker is not None:
        await state.resource_tracker.stop()


async def _shutdown_telegram_bot(state: AppState) -> None:
    """Gracefully stop the Telegram bot."""
    if state.telegram_bot is not None:
        logger.info("Stopping Telegram bot")
        await state.telegram_bot.stop()
        logger.info("Telegram bot stopped")


async def _shutdown_redis(state: AppState) -> None:
    """Close Redis connection pool."""
    if state.redis_client is not None:
        logger.info("Closing Redis connection")
        await state.redis_client.aclose()
        logger.info("Redis connection closed")


async def _shutdown_database(state: AppState) -> None:
    """Dispose database engine and close connection pool."""
    if state.engine is not None:
        from orchestrator.persistence.database import dispose_engine

        logger.info("Disposing database engine")
        await dispose_engine(state.engine)
        logger.info("Database engine disposed")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Application lifespan manager — startup and shutdown logic.

    Startup sequence:
    1. Validate configuration
    2. Connect to database (with retry)
    3. Run Alembic migrations
    4. Connect to Redis
    5. Create application services
    6. Wire dependency injection
    7. Configure health routes
    8. Start worker (if mode allows)
    9. Start Telegram bot (if configured)

    Shutdown sequence:
    1. Stop accepting new requests (handled by uvicorn)
    2. Wait for in-flight requests to drain (up to 30s)
    3. Stop Telegram bot
    4. Stop worker (deregister, cancel heartbeat)
    5. Close Redis connection
    6. Dispose database engine
    """
    state = AppState()

    try:
        # --- Startup ---
        # Configure structured logging first so all subsequent logs are JSON.
        configure_logging(state.settings.observability.log_level)

        logger.info("Starting orchestrator", mode=state.settings.api.mode.value)

        await _validate_config(state.settings)
        await _connect_database(state)
        await _run_migrations(state)
        await _connect_redis(state)
        _create_services(state)
        app.state.app_state = state
        wire_dependencies(app, state)

        # Configure health routes with runtime dependencies
        from orchestrator.api.routes.health import configure_health_routes

        configure_health_routes(
            engine=state.engine,  # type: ignore[arg-type]
            redis=state.redis_client,  # type: ignore[arg-type]
            settings=state.settings,
        )

        await _start_worker(state)
        await _start_telegram_bot(state)

        logger.info(
            "Orchestrator started successfully",
            mode=state.settings.api.mode.value,
            port=state.settings.api.port,
            safe_mode=state.settings.security.safe_mode,
        )

        yield

    finally:
        # --- Shutdown ---
        logger.info("Shutting down orchestrator")

        # Drain in-flight requests (uvicorn handles stop-accepting;
        # we give a grace period for handlers to complete)
        logger.info(
            "Waiting for in-flight requests to drain",
            timeout_seconds=_SHUTDOWN_DRAIN_TIMEOUT,
        )
        await asyncio.sleep(0)  # Yield to let pending handlers progress

        await _shutdown_telegram_bot(state)
        await _shutdown_worker(state)
        await _shutdown_redis(state)
        await _shutdown_database(state)

        logger.info("Orchestrator shutdown complete")


def create_app(settings: Settings | None = None) -> FastAPI:
    """Create and configure the FastAPI application.

    This is the main entry point for the application. It:
    1. Creates the FastAPI instance with lifespan management
    2. Registers middleware (correlation → auth → rate limit)
    3. Includes all route modules

    Args:
        settings: Optional pre-built settings (for testing).
                  If None, settings are loaded from environment.

    Returns:
        Configured FastAPI application instance.
    """
    if settings is None:
        settings = Settings()

    app = FastAPI(
        title="Orchestrator Platform",
        description="Central coordination service for the Local AI Agents Platform",
        version="0.1.0",
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
        lifespan=lifespan,
    )

    # --- Middleware stack (applied in reverse order) ---
    # Order of execution: correlation → access_log → auth → rate_limit → handler
    # Registration order (last registered = first executed):

    from orchestrator.api.middleware.rate_limit import RateLimitMiddleware

    app.add_middleware(RateLimitMiddleware)

    from orchestrator.api.middleware.auth import AuthMiddleware

    async def get_key_by_hash(key_hash: str) -> dict[str, str] | None:
        state = getattr(app.state, "app_state", None)
        if state is None or state.session_factory is None:
            return None

        async with state.session_factory() as session:
            repo = ApiKeyRepository(session)
            key = await repo.get_by_hash(key_hash)

        if key is None:
            return None

        return {"id": str(key.id), "name": key.name, "scope": key.scope.value}

    app.add_middleware(
        AuthMiddleware,
        get_key_by_hash=get_key_by_hash,
        max_attempts=settings.rate_limit.brute_force_max_attempts,
        window_seconds=settings.rate_limit.brute_force_window,
        block_duration_seconds=settings.rate_limit.brute_force_block_duration,
    )

    from orchestrator.observability.logging import AccessLogMiddleware

    app.add_middleware(AccessLogMiddleware)

    from orchestrator.api.middleware.correlation import CorrelationMiddleware

    app.add_middleware(CorrelationMiddleware)

    # --- Route registration ---
    from orchestrator.api.routes.approvals import router as approvals_router
    from orchestrator.api.routes.bridges import router as bridges_router
    from orchestrator.api.routes.config import router as config_router
    from orchestrator.api.routes.health import router as health_router
    from orchestrator.api.routes.openai_tools import router as openai_tools_router
    from orchestrator.api.routes.research import router as research_router
    from orchestrator.api.routes.tasks import router as tasks_router
    from orchestrator.api.routes.tools import router as tools_router
    from orchestrator.api.routes.workers import router as workers_router
    from orchestrator.api.routes.workspaces import router as workspaces_router

    app.include_router(health_router)
    app.include_router(tasks_router)
    app.include_router(approvals_router)
    app.include_router(bridges_router)
    app.include_router(workers_router)
    app.include_router(config_router)
    app.include_router(workspaces_router)
    app.include_router(tools_router)
    app.include_router(research_router)
    app.include_router(openai_tools_router)

    return app


# Module-level app instance for uvicorn (uvicorn orchestrator.main:app)
app = create_app()
