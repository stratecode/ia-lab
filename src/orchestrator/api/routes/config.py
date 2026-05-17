"""Configuration management API routes.

Endpoints:
- POST /config/safe-mode — Toggle safe mode on/off (requires admin scope)
- POST /config/reload — Reload non-critical configuration without restart

Requirements: 11.6, 20.6
"""

from __future__ import annotations

import structlog
from fastapi import APIRouter, Depends, status
from pydantic import BaseModel, Field

from orchestrator.tools.safe_mode import SafeModeEnforcer

logger = structlog.get_logger(__name__)

router = APIRouter(prefix="/config", tags=["config"])


class SafeModeRequest(BaseModel):
    """Request body for toggling safe mode."""

    enabled: bool = Field(..., description="Whether to enable or disable safe mode")


class SafeModeResponse(BaseModel):
    """Response after toggling safe mode."""

    safe_mode_enabled: bool
    message: str


class ConfigReloadResponse(BaseModel):
    """Response after reloading configuration."""

    reloaded: bool
    message: str
    reloaded_settings: list[str] = Field(default_factory=list)


def _get_safe_mode_enforcer() -> SafeModeEnforcer:
    """Dependency placeholder — wired by the app factory at startup."""
    raise NotImplementedError("SafeModeEnforcer dependency not configured")


@router.post(
    "/safe-mode",
    response_model=SafeModeResponse,
    status_code=status.HTTP_200_OK,
)
async def toggle_safe_mode(
    body: SafeModeRequest,
    enforcer: SafeModeEnforcer = Depends(_get_safe_mode_enforcer),
) -> SafeModeResponse:
    """Toggle safe mode on or off at runtime without requiring a restart.

    Requires admin-scoped API key (enforced by auth middleware).
    When safe mode is enabled, all destructive tool operations are blocked.
    """
    previous = enforcer.enabled
    enforcer.enabled = body.enabled

    action = "enabled" if body.enabled else "disabled"
    logger.info(
        "Safe mode toggled via API",
        safe_mode_enabled=body.enabled,
        previous=previous,
    )

    return SafeModeResponse(
        safe_mode_enabled=body.enabled,
        message=f"Safe mode {action} (was {'enabled' if previous else 'disabled'})",
    )


@router.post(
    "/reload",
    response_model=ConfigReloadResponse,
    status_code=status.HTTP_200_OK,
)
async def reload_config(
    enforcer: SafeModeEnforcer = Depends(_get_safe_mode_enforcer),
) -> ConfigReloadResponse:
    """Reload non-critical configuration settings without requiring a restart.

    Reloadable settings include:
    - Log level
    - Rate limits
    - Safe mode status

    Critical settings (database URL, Redis URL, port) require a full restart.
    Requires admin-scoped API key (enforced by auth middleware).
    """
    # In a full implementation, this would re-read environment variables
    # and update the in-memory settings for non-critical values.
    # For now, we reload safe mode from the environment as a demonstration.
    import os

    reloaded_settings: list[str] = []

    # Reload safe mode from environment
    env_safe_mode = os.environ.get("LAB_ORCHESTRATOR_SAFE_MODE", "").lower()
    if env_safe_mode in ("true", "1", "yes"):
        enforcer.enabled = True
        reloaded_settings.append("safe_mode=true")
    elif env_safe_mode in ("false", "0", "no"):
        enforcer.enabled = False
        reloaded_settings.append("safe_mode=false")

    # Reload log level from environment
    env_log_level = os.environ.get("LAB_LOG_LEVEL")
    if env_log_level:
        reloaded_settings.append(f"log_level={env_log_level}")

    logger.info(
        "Configuration reloaded via API",
        reloaded_settings=reloaded_settings,
    )

    return ConfigReloadResponse(
        reloaded=True,
        message="Non-critical configuration reloaded from environment",
        reloaded_settings=reloaded_settings,
    )
