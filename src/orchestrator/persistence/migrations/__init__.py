"""Alembic database migrations.

Provides a programmatic interface to run pending migrations on application
startup. If migration fails, an exception is raised so the caller can
refuse to start the application (Requirement 5.4).
"""

from __future__ import annotations

import logging
from pathlib import Path

from alembic import command
from alembic.config import Config
from sqlalchemy import pool
from sqlalchemy.engine import Connection
from sqlalchemy.ext.asyncio import async_engine_from_config

from orchestrator.config import DatabaseSettings

logger = logging.getLogger(__name__)

# Directory containing this package (the migrations directory)
_MIGRATIONS_DIR = Path(__file__).parent

# Project root where alembic.ini lives
_PROJECT_ROOT = _MIGRATIONS_DIR.parents[3]


def _build_alembic_config(database_url: str) -> Config:
    """Build an Alembic Config object with the correct paths and URL.

    Args:
        database_url: The async database DSN.

    Returns:
        Configured Alembic Config instance.
    """
    ini_path = _PROJECT_ROOT / "alembic.ini"
    alembic_cfg = Config(str(ini_path))
    alembic_cfg.set_main_option("script_location", str(_MIGRATIONS_DIR))
    alembic_cfg.set_main_option("sqlalchemy.url", database_url)
    return alembic_cfg


async def run_migrations(settings: DatabaseSettings | None = None) -> None:
    """Run all pending Alembic migrations programmatically.

    This function is intended to be called during application startup.
    It applies any pending migrations to bring the database schema up to
    the latest revision.

    If migration fails for any reason, the exception propagates to the
    caller, which should refuse to start the application.

    Args:
        settings: Optional DatabaseSettings. If not provided, settings are
                  loaded from environment variables.

    Raises:
        Exception: Any error during migration (connection failure, SQL error,
                   migration script error) propagates unchanged.
    """
    if settings is None:
        settings = DatabaseSettings()

    database_url = settings.dsn
    alembic_cfg = _build_alembic_config(database_url)

    logger.info("Running database migrations", extra={"dsn_host": settings.host})

    # Use an async engine to run migrations
    engine_config = {"sqlalchemy.url": database_url}
    connectable = async_engine_from_config(
        engine_config,
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )

    def _do_upgrade(connection: Connection) -> None:
        """Execute the Alembic upgrade within a sync connection."""
        alembic_cfg.attributes["connection"] = connection
        command.upgrade(alembic_cfg, "head")

    async with connectable.connect() as connection:
        await connection.run_sync(_do_upgrade)

    await connectable.dispose()

    logger.info("Database migrations completed successfully")
