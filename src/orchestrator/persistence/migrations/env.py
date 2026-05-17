"""Alembic environment configuration with async migration support.

This module configures Alembic to run migrations using asyncpg (async driver).
The database URL is sourced from the orchestrator's DatabaseSettings.
"""

from __future__ import annotations

import asyncio
from logging.config import fileConfig

from alembic import context
from sqlalchemy import pool
from sqlalchemy.engine import Connection
from sqlalchemy.ext.asyncio import async_engine_from_config

from orchestrator.config import DatabaseSettings
from orchestrator.persistence.models import Base

# Alembic Config object — provides access to values in alembic.ini
config = context.config

# Set up Python logging from the config file
if config.config_file_name is not None:
    fileConfig(config.config_file_name)

# Target metadata for autogenerate support
target_metadata = Base.metadata


def _get_database_url() -> str:
    """Resolve the async database URL from environment-based settings."""
    settings = DatabaseSettings()
    return settings.dsn


def run_migrations_offline() -> None:
    """Run migrations in 'offline' mode.

    Configures the context with just a URL and not an Engine.
    Calls to context.execute() emit the given string to the script output.
    """
    url = _get_database_url()
    context.configure(
        url=url,
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={"paramstyle": "named"},
    )

    with context.begin_transaction():
        context.run_migrations()


def do_run_migrations(connection: Connection) -> None:
    """Execute migrations within a synchronous connection context."""
    context.configure(connection=connection, target_metadata=target_metadata)

    with context.begin_transaction():
        context.run_migrations()


async def run_async_migrations() -> None:
    """Run migrations in 'online' mode using an async engine.

    Creates an async engine from the resolved database URL, then
    runs migrations synchronously within the async connection.
    """
    configuration = config.get_section(config.config_ini_section, {})
    configuration["sqlalchemy.url"] = _get_database_url()

    connectable = async_engine_from_config(
        configuration,
        prefix="sqlalchemy.",
        poolclass=pool.NullPool,
    )

    async with connectable.connect() as connection:
        await connection.run_sync(do_run_migrations)

    await connectable.dispose()


def run_migrations_online() -> None:
    """Run migrations in 'online' mode (async wrapper or connection-based).

    If a connection is passed via config attributes (programmatic usage from
    run_migrations()), use it directly. Otherwise, create an async engine
    and run migrations through it.
    """
    connectable = config.attributes.get("connection", None)

    if connectable is not None:
        # Programmatic invocation — connection already provided
        do_run_migrations(connectable)
    else:
        # CLI invocation — create async engine
        asyncio.run(run_async_migrations())


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
