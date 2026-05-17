"""Async database engine, session factory, and connection management.

Provides:
- Async SQLAlchemy engine with connection pooling
- Session factory via async_sessionmaker
- get_session async context manager for FastAPI dependency injection
- Connection retry with exponential backoff and jitter
- Health check for database connectivity
- Graceful engine disposal for shutdown
"""

from __future__ import annotations

import asyncio
import logging
import random
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager

from sqlalchemy.ext.asyncio import (
    AsyncEngine,
    AsyncSession,
    async_sessionmaker,
    create_async_engine,
)
from sqlalchemy import text

from orchestrator.config import DatabaseSettings

logger = logging.getLogger(__name__)

# Retry configuration constants
_INITIAL_BACKOFF_S = 1.0
_MAX_BACKOFF_S = 30.0
_MAX_RETRIES = 10
_JITTER_FACTOR = 0.2  # ±20%


def _compute_backoff(attempt: int) -> float:
    """Compute backoff delay with exponential growth and ±20% jitter.

    Args:
        attempt: Zero-based retry attempt number.

    Returns:
        Delay in seconds with jitter applied.
    """
    base_delay = min(_INITIAL_BACKOFF_S * (2**attempt), _MAX_BACKOFF_S)
    jitter = base_delay * _JITTER_FACTOR * (2 * random.random() - 1)
    return base_delay + jitter


def create_engine(settings: DatabaseSettings) -> AsyncEngine:
    """Create an async SQLAlchemy engine with connection pool configuration.

    Pool settings are sourced from the DatabaseSettings object:
    - pool_size: Number of persistent connections (default 5)
    - max_overflow: Extra connections beyond pool_size (default 10)
    - pool_timeout: Seconds to wait for a connection (default 30)

    Args:
        settings: Database configuration from the Settings object.

    Returns:
        Configured AsyncEngine instance.
    """
    engine = create_async_engine(
        settings.dsn,
        pool_size=settings.pool_size,
        max_overflow=settings.max_overflow,
        pool_timeout=settings.pool_timeout,
        pool_pre_ping=True,
        echo=False,
    )
    logger.info(
        "Database engine created",
        extra={
            "host": settings.host,
            "port": settings.port,
            "db": settings.db,
            "pool_size": settings.pool_size,
            "max_overflow": settings.max_overflow,
            "pool_timeout": settings.pool_timeout,
        },
    )
    return engine


def create_session_factory(engine: AsyncEngine) -> async_sessionmaker[AsyncSession]:
    """Create an async session factory bound to the given engine.

    Sessions are configured with:
    - expire_on_commit=False for detached object access after commit
    - No autoflush or autocommit (explicit transaction management)

    Args:
        engine: The async engine to bind sessions to.

    Returns:
        Configured async_sessionmaker instance.
    """
    return async_sessionmaker(
        bind=engine,
        class_=AsyncSession,
        expire_on_commit=False,
    )


@asynccontextmanager
async def get_session(
    session_factory: async_sessionmaker[AsyncSession],
) -> AsyncGenerator[AsyncSession, None]:
    """Async context manager providing a database session.

    Intended for use as a FastAPI dependency. Commits on successful exit,
    rolls back on exception.

    Args:
        session_factory: The session factory to create sessions from.

    Yields:
        An AsyncSession instance.
    """
    async with session_factory() as session:
        try:
            yield session
            await session.commit()
        except Exception:
            await session.rollback()
            raise


async def check_connectivity(engine: AsyncEngine) -> bool:
    """Check database connectivity by executing a simple query.

    Useful for health check endpoints. Returns True if the database
    is reachable, False otherwise.

    Args:
        engine: The async engine to check.

    Returns:
        True if the database responds, False on any error.
    """
    try:
        async with engine.connect() as conn:
            await conn.execute(text("SELECT 1"))
        return True
    except Exception as exc:
        logger.warning("Database connectivity check failed", extra={"error": str(exc)})
        return False


async def connect_with_retry(engine: AsyncEngine) -> None:
    """Attempt to connect to the database with exponential backoff.

    Retries up to MAX_RETRIES times with exponential backoff starting
    at 1s, capped at 30s, with ±20% jitter on each delay.

    Args:
        engine: The async engine to verify connectivity for.

    Raises:
        ConnectionError: If all retry attempts are exhausted.
    """
    for attempt in range(_MAX_RETRIES):
        try:
            async with engine.connect() as conn:
                await conn.execute(text("SELECT 1"))
            logger.info("Database connection established", extra={"attempt": attempt + 1})
            return
        except Exception as exc:
            if attempt == _MAX_RETRIES - 1:
                logger.error(
                    "Database connection failed after max retries",
                    extra={"max_retries": _MAX_RETRIES, "error": str(exc)},
                )
                raise ConnectionError(
                    f"Failed to connect to database after {_MAX_RETRIES} attempts: {exc}"
                ) from exc

            delay = _compute_backoff(attempt)
            logger.warning(
                "Database connection attempt failed, retrying",
                extra={
                    "attempt": attempt + 1,
                    "max_retries": _MAX_RETRIES,
                    "delay_s": round(delay, 2),
                    "error": str(exc),
                },
            )
            await asyncio.sleep(delay)


async def dispose_engine(engine: AsyncEngine) -> None:
    """Dispose the engine and close all pooled connections.

    Should be called during application shutdown for graceful cleanup.

    Args:
        engine: The async engine to dispose.
    """
    await engine.dispose()
    logger.info("Database engine disposed")
