from __future__ import annotations

import pytest

from orchestrator.main import _TelegramServerOpsService


@pytest.mark.asyncio
async def test_server_ops_rejects_unsupported_actions() -> None:
    service = _TelegramServerOpsService()

    assert "no disponible" in await service.run("logs")
    assert "no disponible" in await service.run("restart")
    assert "no soportada" in await service.run("made-up")


@pytest.mark.asyncio
async def test_server_ops_status_aggregates_health_and_disk(monkeypatch: pytest.MonkeyPatch) -> None:
    service = _TelegramServerOpsService()

    async def fake_run(command: list[str], timeout: float) -> str:
        if command[:2] == ["curl", "-sf"]:
            return '{"status":"healthy"}\n'
        return "Filesystem Size Used Avail Use% Mounted on\n/dev/root 10G 1G 9G 10% /\n"

    monkeypatch.setattr(service, "_run", fake_run)

    result = await service.run("status")

    assert "HEALTH" in result
    assert '"status":"healthy"' in result
    assert "DISK" in result
