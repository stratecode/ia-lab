"""Pydantic Settings configuration model for the Orchestrator Platform.

All configuration is loaded from environment variables with the LAB_ prefix.
Settings are grouped into logical sections matching the platform's modules.
Sensitive fields are redacted when logging the active configuration on startup.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Annotated

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class OrchestratorMode(StrEnum):
    """Deployment mode for the orchestrator process."""

    ALL = "all"
    API = "api"
    WORKER = "worker"


class LogLevel(StrEnum):
    """Supported log levels."""

    DEBUG = "DEBUG"
    INFO = "INFO"
    WARNING = "WARNING"
    ERROR = "ERROR"
    CRITICAL = "CRITICAL"


# ---------------------------------------------------------------------------
# Settings Groups
# ---------------------------------------------------------------------------


class DatabaseSettings(BaseSettings):
    """PostgreSQL connection settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_POSTGRES_")

    host: str = Field(default="localhost", description="PostgreSQL host")
    port: Annotated[int, Field(ge=1024, le=65535)] = Field(
        default=5432, description="PostgreSQL port"
    )
    db: str = Field(default="orchestrator", description="Database name")
    user: str = Field(default="orchestrator", description="Database user")
    password: str = Field(default="", description="Database password")
    pool_size: Annotated[int, Field(ge=1)] = Field(
        default=5, description="Connection pool size"
    )
    max_overflow: Annotated[int, Field(ge=0)] = Field(
        default=10, description="Max overflow connections beyond pool_size"
    )
    pool_timeout: Annotated[float, Field(gt=0)] = Field(
        default=30.0, description="Seconds to wait for a connection from the pool"
    )

    @property
    def dsn(self) -> str:
        """Build the async PostgreSQL DSN."""
        return (
            f"postgresql+asyncpg://{self.user}:{self.password}"
            f"@{self.host}:{self.port}/{self.db}"
        )


class RedisSettings(BaseSettings):
    """Redis connection settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_REDIS_")

    url: str = Field(
        default="redis://localhost:6379/0", description="Redis connection URL"
    )


class ApiSettings(BaseSettings):
    """API server settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_ORCHESTRATOR_")

    port: Annotated[int, Field(ge=1024, le=65535)] = Field(
        default=8100, description="API server port"
    )
    mode: OrchestratorMode = Field(
        default=OrchestratorMode.ALL,
        description="Process mode: all, api, or worker",
    )


class WorkerSettings(BaseSettings):
    """Worker process settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_WORKER_")

    heartbeat_interval: Annotated[int, Field(gt=0)] = Field(
        default=30, description="Heartbeat interval in seconds"
    )
    heartbeat_ttl_multiplier: Annotated[float, Field(gt=0)] = Field(
        default=3.5,
        description="Multiplier for heartbeat interval to determine TTL",
    )


class SchedulerSettings(BaseSettings):
    """Scheduler and classification settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_SCHEDULER_")

    max_concurrent_tasks: Annotated[int, Field(ge=1)] = Field(
        default=3, description="Maximum concurrent tasks across all agents"
    )
    classification_confidence_threshold: Annotated[float, Field(gt=0, le=1)] = Field(
        default=0.8,
        description="Minimum confidence for keyword classification before LLM fallback",
    )
    classification_timeout: Annotated[float, Field(gt=0)] = Field(
        default=10.0, description="LLM classification timeout in seconds"
    )
    resource_poll_interval: Annotated[float, Field(gt=0)] = Field(
        default=30.0, description="Resource availability poll interval in seconds"
    )


class QueueSettings(BaseSettings):
    """Task queue settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_QUEUE_")

    max_global: Annotated[int, Field(ge=1)] = Field(
        default=200, description="Maximum total queue depth across all agents"
    )
    max_per_agent: Annotated[int, Field(ge=1)] = Field(
        default=50, description="Maximum queue depth per agent type"
    )
    dead_letter_timeout: Annotated[int, Field(gt=0)] = Field(
        default=3600,
        description="Seconds before unprocessed tasks move to dead letter queue",
    )


class RateLimitSettings(BaseSettings):
    """Rate limiting settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_RATE_LIMIT_")

    per_key: Annotated[int, Field(ge=1)] = Field(
        default=20, description="Maximum tasks per minute per API key"
    )
    brute_force_max_attempts: Annotated[int, Field(ge=1)] = Field(
        default=10, description="Max failed auth attempts before blocking"
    )
    brute_force_window: Annotated[int, Field(gt=0)] = Field(
        default=60, description="Window in seconds for brute force detection"
    )
    brute_force_block_duration: Annotated[int, Field(gt=0)] = Field(
        default=300, description="Block duration in seconds after brute force detection"
    )


class NotificationSettings(BaseSettings):
    """Telegram notification settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_TELEGRAM_")

    bot_token: str = Field(default="", description="Telegram Bot API token")
    allowed_users: str = Field(
        default="",
        description="Comma-separated list of allowed Telegram user IDs",
    )

    @property
    def allowed_user_ids(self) -> list[int]:
        """Parse the comma-separated user IDs into a list of integers."""
        if not self.allowed_users.strip():
            return []
        return [int(uid.strip()) for uid in self.allowed_users.split(",") if uid.strip()]


class LlamaChatSettings(BaseSettings):
    """Local llama.cpp chat endpoints used by Telegram commands."""

    model_config = SettingsConfigDict(env_prefix="LAB_LLAMA_")

    code_base_url: str = Field(
        default="http://127.0.0.1:8080/v1",
        description="OpenAI-compatible base URL for the coder model",
    )
    code_api_key: str = Field(
        default="local-code",
        description="Bearer token for the coder llama.cpp endpoint",
    )
    planner_base_url: str = Field(
        default="http://127.0.0.1:8082/v1",
        description="OpenAI-compatible base URL for the planner model",
    )
    planner_api_key: str = Field(
        default="local-planner",
        description="Bearer token for the planner llama.cpp endpoint",
    )
    timeout_seconds: Annotated[float, Field(gt=0)] = Field(
        default=90.0,
        description="Timeout for Telegram-triggered llama.cpp chat requests",
    )


class SecuritySettings(BaseSettings):
    """Security and safe mode settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_ORCHESTRATOR_")

    safe_mode: bool = Field(
        default=True,
        description="Enable safe mode (blocks destructive operations)",
    )
    workspace_retention_hours: Annotated[int, Field(gt=0)] = Field(
        default=24,
        description="Hours to retain completed task workspaces before cleanup",
    )


class ObservabilitySettings(BaseSettings):
    """Observability and logging settings."""

    model_config = SettingsConfigDict(env_prefix="LAB_ORCHESTRATOR_")

    log_level: LogLevel = Field(
        default=LogLevel.INFO, description="Application log level"
    )


# ---------------------------------------------------------------------------
# Root Settings
# ---------------------------------------------------------------------------

# Fields considered sensitive for redaction
_SENSITIVE_KEYWORDS = frozenset({"password", "token", "secret", "api_key"})
_REDACTED = "***REDACTED***"


class Settings(BaseSettings):
    """Root configuration aggregating all settings groups.

    Each group is loaded independently from its own env prefix.
    Use `Settings()` to load all configuration from the environment.
    """

    model_config = SettingsConfigDict(
        env_prefix="LAB_ORCHESTRATOR_",
        env_nested_delimiter="__",
    )

    database: DatabaseSettings = Field(default_factory=DatabaseSettings)
    redis: RedisSettings = Field(default_factory=RedisSettings)
    api: ApiSettings = Field(default_factory=ApiSettings)
    worker: WorkerSettings = Field(default_factory=WorkerSettings)
    scheduler: SchedulerSettings = Field(default_factory=SchedulerSettings)
    queue: QueueSettings = Field(default_factory=QueueSettings)
    rate_limit: RateLimitSettings = Field(default_factory=RateLimitSettings)
    notifications: NotificationSettings = Field(default_factory=NotificationSettings)
    llama: LlamaChatSettings = Field(default_factory=LlamaChatSettings)
    security: SecuritySettings = Field(default_factory=SecuritySettings)
    observability: ObservabilitySettings = Field(default_factory=ObservabilitySettings)

    def redacted_dict(self) -> dict:
        """Return configuration as a nested dict with sensitive values redacted.

        Any field whose name contains 'password', 'token', or 'secret'
        (case-insensitive) is replaced with '***REDACTED***'.
        This is intended for logging the active configuration on startup.
        """
        return _redact_dict(self.model_dump())


def _redact_dict(data: dict) -> dict:
    """Recursively redact sensitive fields in a configuration dictionary."""
    result = {}
    for key, value in data.items():
        if isinstance(value, dict):
            result[key] = _redact_dict(value)
        elif any(keyword in key.lower() for keyword in _SENSITIVE_KEYWORDS):
            result[key] = _REDACTED
        else:
            result[key] = value
    return result
