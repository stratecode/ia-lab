"""Repository for registered local bridges."""

from __future__ import annotations

import uuid
from datetime import datetime, timezone

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from orchestrator.persistence.models import LocalBridge


class LocalBridgeRepository:
    """Persistence helpers for local bridge registration and heartbeat."""

    def __init__(self, session: AsyncSession) -> None:
        self._session = session

    async def upsert(
        self,
        bridge_id: uuid.UUID,
        *,
        name: str,
        hostname: str,
        workspace_root: str,
        capabilities: dict | None = None,
        api_key_name: str | None = None,
        status: str = "active",
    ) -> LocalBridge:
        bridge = await self._session.get(LocalBridge, bridge_id)
        now = datetime.now(timezone.utc)
        if bridge is None:
            bridge = LocalBridge(
                id=bridge_id,
                name=name,
                hostname=hostname,
                workspace_root=workspace_root,
                capabilities=capabilities or {},
                api_key_name=api_key_name,
                status=status,
                last_heartbeat=now,
            )
            self._session.add(bridge)
        else:
            bridge.name = name
            bridge.hostname = hostname
            bridge.workspace_root = workspace_root
            bridge.capabilities = capabilities or {}
            bridge.api_key_name = api_key_name
            bridge.status = status
            bridge.last_heartbeat = now
        await self._session.flush()
        return bridge

    async def get_by_id(self, bridge_id: uuid.UUID) -> LocalBridge | None:
        return await self._session.get(LocalBridge, bridge_id)

    async def touch_heartbeat(self, bridge_id: uuid.UUID, status: str = "active") -> LocalBridge | None:
        bridge = await self._session.get(LocalBridge, bridge_id)
        if bridge is None:
            return None
        bridge.status = status
        bridge.last_heartbeat = datetime.now(timezone.utc)
        await self._session.flush()
        return bridge

    async def list_active(self) -> list[LocalBridge]:
        stmt = (
            select(LocalBridge)
            .where(LocalBridge.status == "active")
            .order_by(LocalBridge.updated_at.desc(), LocalBridge.id.asc())
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())
