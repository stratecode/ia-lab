"""FastAPI dependency injection wiring.

Provides dependency factories for route handlers. Each dependency is a
callable that returns the appropriate service instance, configured during
app startup via the AppState container.

The pattern:
1. Route modules define placeholder dependency functions (raise NotImplementedError)
2. The app factory creates real service instances during lifespan startup
3. app.dependency_overrides maps placeholders → real factories

This module provides the centralized wiring logic used by the app factory.

Validates: Requirements 1.8, 1.9
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from redis.asyncio import Redis
from sqlalchemy.ext.asyncio import AsyncEngine, AsyncSession, async_sessionmaker

from orchestrator.approvals.manager import ApprovalManager
from orchestrator.config import SecuritySettings, Settings
from orchestrator.execution.worker import Worker
from orchestrator.notifications.telegram_bot import TelegramBot
from orchestrator.queue.event_bus import RedisEventBus
from orchestrator.queue.redis_queue import RedisQueueService
from orchestrator.state_machine.engine import StateMachineEngine
from orchestrator.tools.safe_mode import SafeModeEnforcer


@dataclass
class AppState:
    """Container holding all initialized service instances.

    Created during app lifespan startup and used to wire FastAPI
    dependency overrides. Provides a single point of access to all
    shared resources for the dependency injection layer.
    """

    settings: Settings = field(default_factory=Settings)
    engine: AsyncEngine | None = None
    session_factory: async_sessionmaker[AsyncSession] | None = None
    redis_client: Redis | None = None
    event_bus: RedisEventBus | None = None
    queue_service: RedisQueueService | None = None
    state_machine: StateMachineEngine | None = None
    approval_manager: ApprovalManager | None = None
    safe_mode_enforcer: SafeModeEnforcer | None = None
    worker: Worker | None = None
    telegram_bot: TelegramBot | None = None


def wire_dependencies(app: Any, state: AppState) -> None:
    """Wire all FastAPI dependency overrides from the AppState.

    Maps each route module's placeholder dependency function to a
    real factory that returns the initialized service instance.

    Args:
        app: The FastAPI application instance.
        state: The initialized AppState with all service instances.
    """
    from orchestrator.api.routes.approvals import (
        _get_approval_manager,
        _get_session_factory as _approvals_get_session_factory,
    )
    from orchestrator.api.routes.config import _get_safe_mode_enforcer
    from orchestrator.api.routes.tasks import (
        _get_session_factory as _tasks_get_session_factory,
        _get_state_engine,
    )
    from orchestrator.api.routes.workers import _get_redis
    from orchestrator.api.routes.workspaces import (
        _get_security_settings,
        _get_session_factory as _workspaces_get_session_factory,
    )

    # Session factory dependencies
    if state.session_factory is not None:

        async def provide_session_factory() -> async_sessionmaker[AsyncSession]:
            return state.session_factory  # type: ignore[return-value]

        app.dependency_overrides[_tasks_get_session_factory] = provide_session_factory
        app.dependency_overrides[_approvals_get_session_factory] = provide_session_factory
        app.dependency_overrides[_workspaces_get_session_factory] = provide_session_factory

    # State machine engine
    if state.state_machine is not None:

        async def provide_state_engine() -> StateMachineEngine:
            return state.state_machine  # type: ignore[return-value]

        app.dependency_overrides[_get_state_engine] = provide_state_engine

    # Approval manager
    if state.approval_manager is not None:

        def provide_approval_manager() -> ApprovalManager:
            return state.approval_manager  # type: ignore[return-value]

        app.dependency_overrides[_get_approval_manager] = provide_approval_manager

    # Redis client
    if state.redis_client is not None:

        def provide_redis() -> Redis:
            return state.redis_client  # type: ignore[return-value]

        app.dependency_overrides[_get_redis] = provide_redis

    # Safe mode enforcer
    if state.safe_mode_enforcer is not None:

        def provide_safe_mode_enforcer() -> SafeModeEnforcer:
            return state.safe_mode_enforcer  # type: ignore[return-value]

        app.dependency_overrides[_get_safe_mode_enforcer] = provide_safe_mode_enforcer

    # Security settings
    def provide_security_settings() -> SecuritySettings:
        return state.settings.security

    app.dependency_overrides[_get_security_settings] = provide_security_settings
