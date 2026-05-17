"""API key repository — key lookup, creation, revocation, and usage tracking."""

from __future__ import annotations

import uuid
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import ApiKey
from orchestrator.state_machine.transitions import ApiKeyScope


class ApiKeyRepository:
    """Data access layer for ApiKey entities."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def get_by_hash(self, key_hash: str) -> ApiKey | None:
        """Look up an active API key by its SHA-256 hash.

        Only returns keys where is_active is True.

        Args:
            key_hash: The SHA-256 hash of the API key.

        Returns:
            The ApiKey instance or None if not found or inactive.
        """
        stmt = select(ApiKey).where(
            ApiKey.key_hash == key_hash,
            ApiKey.is_active == True,  # noqa: E712
        )
        result = await self._session.execute(stmt)
        return result.scalar_one_or_none()

    async def create(
        self,
        key_hash: str,
        name: str,
        scope: ApiKeyScope,
    ) -> ApiKey:
        """Create a new API key record.

        Args:
            key_hash: The SHA-256 hash of the raw key.
            name: Human-readable name for the key.
            scope: Authorization scope for the key.

        Returns:
            The newly created ApiKey instance.
        """
        api_key = ApiKey(
            key_hash=key_hash,
            name=name,
            scope=scope,
        )
        self._session.add(api_key)
        await self._session.flush()
        return api_key

    async def revoke(self, key_id: uuid.UUID) -> ApiKey | None:
        """Revoke an API key by setting is_active=False and revoked_at.

        Args:
            key_id: The UUID of the key to revoke.

        Returns:
            The updated ApiKey instance, or None if not found.
        """
        api_key = await self._session.get(ApiKey, key_id)
        if api_key is None:
            return None
        api_key.is_active = False
        api_key.revoked_at = datetime.now(timezone.utc)
        await self._session.flush()
        return api_key

    async def update_last_used(self, key_id: uuid.UUID) -> None:
        """Update the last_used_at timestamp for an API key.

        Args:
            key_id: The UUID of the key to update.
        """
        api_key = await self._session.get(ApiKey, key_id)
        if api_key is not None:
            api_key.last_used_at = datetime.now(timezone.utc)
            await self._session.flush()
