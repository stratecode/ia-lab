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
    utility_base_url: str = Field(
        default="http://127.0.0.1:8083/v1",
        description="OpenAI-compatible base URL for the utility model",
    )
    utility_api_key: str = Field(
        default="local-utility",
        description="Bearer token for the utility llama.cpp endpoint",
    )
    timeout_seconds: Annotated[float, Field(gt=0)] = Field(
        default=90.0,
        description="Timeout for Telegram-triggered llama.cpp chat requests",
    )


class CapabilitySettings(BaseSettings):
    """Capability Layer configuration."""

    model_config = SettingsConfigDict(env_prefix="LAB_CAPABILITIES_")

    request_timeout_seconds: Annotated[float, Field(gt=0)] = Field(
        default=20.0,
        description="Default outbound timeout for capability HTTP requests",
    )
    max_html_bytes: Annotated[int, Field(gt=0)] = Field(
        default=1_500_000,
        description="Maximum number of HTML bytes to keep per fetch",
    )
    max_document_bytes: Annotated[int, Field(gt=0)] = Field(
        default=20_000_000,
        description="Maximum document size in bytes",
    )
    max_image_bytes: Annotated[int, Field(gt=0)] = Field(
        default=10_000_000,
        description="Maximum image size in bytes",
    )
    max_artifact_text_chars: Annotated[int, Field(gt=0)] = Field(
        default=16000,
        description="Maximum persisted content text per artifact",
    )
    allowed_url_schemes: str = Field(
        default="http,https,file",
        description="Comma-separated list of URL schemes allowed by capability providers",
    )
    allowed_local_roots: str = Field(
        default="/srv/ai-lab,/tmp",
        description="Comma-separated list of local path prefixes allowed for document/image reads",
    )
    planner_allowed_capabilities: str = Field(
        default="web.search,web.fetch,document.read",
        description="Comma-separated capability allowlist for planner tasks",
    )
    coder_allowed_capabilities: str = Field(
        default="",
        description="Comma-separated capability allowlist for coder tasks",
    )
    openai_tools_model_id: str = Field(
        default="orchestrator-tools",
        description="Model identifier exposed by the OpenAI-compatible tool backend",
    )
    research_timeout_seconds: Annotated[float, Field(gt=0)] = Field(
        default=60.0,
        description="End-to-end timeout budget for research queries",
    )
    default_search_fetch_count: Annotated[int, Field(ge=1, le=10)] = Field(
        default=3,
        description="Default number of sources to fetch for search-based research",
    )
    max_search_fetch_count: Annotated[int, Field(ge=1, le=10)] = Field(
        default=5,
        description="Maximum number of sources to fetch for search-based research",
    )
    min_source_content_chars: Annotated[int, Field(ge=100)] = Field(
        default=300,
        description="Minimum cleaned text length for a fetched source to count as useful",
    )
    utility_model_name: str = Field(
        default="utility",
        description="Model name to use when synthesizing research answers",
    )

    @property
    def allowed_url_scheme_list(self) -> list[str]:
        return [item.strip().lower() for item in self.allowed_url_schemes.split(",") if item.strip()]

    @property
    def allowed_local_root_list(self) -> list[str]:
        return [item.strip() for item in self.allowed_local_roots.split(",") if item.strip()]

    @property
    def planner_capability_allowlist(self) -> list[str]:
        return [item.strip() for item in self.planner_allowed_capabilities.split(",") if item.strip()]

    @property
    def coder_capability_allowlist(self) -> list[str]:
        return [item.strip() for item in self.coder_allowed_capabilities.split(",") if item.strip()]


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


class OpenAIReferenceSettings(BaseSettings):
    """Reference-model and judge-model settings for research evaluation."""

    model_config = SettingsConfigDict(env_prefix="LAB_OPENAI_")

    base_url: str = Field(
        default="https://api.openai.com/v1",
        description="Base URL for OpenAI-compatible reference model API",
    )
    reference_api_key: str = Field(
        default="",
        description="API key for generating external reference answers",
    )
    reference_model: str = Field(
        default="gpt-5.4-mini",
        description="Model used to generate the external reference answer",
    )
    judge_model: str = Field(
        default="gpt-5.4-mini",
        description="Model used to judge orchestrator vs reference answers",
    )
    timeout_seconds: Annotated[float, Field(gt=0)] = Field(
        default=60.0,
        description="Timeout for outbound OpenAI reference/judge requests",
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
    capabilities: CapabilitySettings = Field(default_factory=CapabilitySettings)
    security: SecuritySettings = Field(default_factory=SecuritySettings)
    observability: ObservabilitySettings = Field(default_factory=ObservabilitySettings)
    openai_reference: OpenAIReferenceSettings = Field(default_factory=OpenAIReferenceSettings)

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
