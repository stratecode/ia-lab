"""Polling daemon for the Local Agent Bridge."""

from __future__ import annotations

import argparse
import asyncio
import os
import socket
import uuid

import httpx

from orchestrator.local_bridge.executor import LocalExecutionError, LocalWorkspaceExecutor


class LocalBridgeDaemon:
    """Registers a local workspace and polls the orchestrator for local work."""

    def __init__(
        self,
        *,
        base_url: str,
        api_key: str,
        bridge_id: str,
        workspace_root: str,
        poll_interval: float = 2.0,
        heartbeat_interval: float = 15.0,
        name: str = "lab-agentd",
        hostname: str | None = None,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._bridge_id = bridge_id
        self._workspace_root = os.path.abspath(workspace_root)
        self._poll_interval = poll_interval
        self._heartbeat_interval = heartbeat_interval
        self._name = name
        self._hostname = hostname or socket.gethostname()
        self._executor = LocalWorkspaceExecutor(self._workspace_root)

    @property
    def headers(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self._api_key}"}

    async def run(self) -> None:
        async with httpx.AsyncClient(timeout=30.0) as client:
            await self._register(client)
            last_heartbeat = 0.0
            while True:
                now = asyncio.get_running_loop().time()
                if now - last_heartbeat >= self._heartbeat_interval:
                    await self._heartbeat(client)
                    last_heartbeat = now

                claim = await self._claim_next(client)
                if claim is None:
                    await asyncio.sleep(self._poll_interval)
                    continue

                task_id = claim["task_id"]
                try:
                    result = await self._executor.execute(claim)
                except LocalExecutionError as exc:
                    result = {
                        "status": "error",
                        "summary": "Local execution rejected",
                        "error_message": str(exc),
                        "stderr": str(exc),
                    }
                await self._submit_result(client, task_id, result)

    async def _register(self, client: httpx.AsyncClient) -> None:
        response = await client.post(
            f"{self._base_url}/bridges/register",
            headers=self.headers,
            json={
                "bridge_id": self._bridge_id,
                "name": self._name,
                "hostname": self._hostname,
                "workspace_root": self._workspace_root,
                "capabilities": {
                    "tools": [
                        "read_file",
                        "list_files",
                        "write_file",
                        "apply_patch",
                        "run_command",
                        "git_status",
                        "git_diff",
                        "run_tests",
                    ]
                },
            },
        )
        response.raise_for_status()

    async def _heartbeat(self, client: httpx.AsyncClient) -> None:
        response = await client.post(
            f"{self._base_url}/bridges/{self._bridge_id}/heartbeat",
            headers=self.headers,
            json={"status": "active"},
        )
        response.raise_for_status()

    async def _claim_next(self, client: httpx.AsyncClient) -> dict | None:
        response = await client.post(
            f"{self._base_url}/bridges/{self._bridge_id}/claim-next",
            headers=self.headers,
        )
        response.raise_for_status()
        if not response.content or response.text == "null":
            return None
        return response.json()

    async def _submit_result(self, client: httpx.AsyncClient, task_id: str, result: dict) -> None:
        response = await client.post(
            f"{self._base_url}/bridges/{self._bridge_id}/tasks/{task_id}/result",
            headers=self.headers,
            json=result,
        )
        response.raise_for_status()


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run the Local Agent Bridge daemon")
    parser.add_argument("--base-url", default=os.environ.get("LAB_AGENT_BASE_URL", "http://127.0.0.1:8100"))
    parser.add_argument("--api-key", default=os.environ.get("LAB_AGENT_API_KEY", ""))
    parser.add_argument("--bridge-id", default=os.environ.get("LAB_AGENT_BRIDGE_ID", str(uuid.uuid4())))
    parser.add_argument("--workspace-root", default=os.environ.get("LAB_AGENT_WORKSPACE_ROOT", os.getcwd()))
    parser.add_argument("--poll-interval", type=float, default=float(os.environ.get("LAB_AGENT_POLL_INTERVAL", "2")))
    parser.add_argument("--heartbeat-interval", type=float, default=float(os.environ.get("LAB_AGENT_HEARTBEAT_INTERVAL", "15")))
    parser.add_argument("--name", default=os.environ.get("LAB_AGENT_NAME", "lab-agentd"))
    return parser


def main() -> None:
    args = build_parser().parse_args()
    if not args.api_key:
        raise SystemExit("LAB_AGENT_API_KEY or --api-key is required")
    daemon = LocalBridgeDaemon(
        base_url=args.base_url,
        api_key=args.api_key,
        bridge_id=args.bridge_id,
        workspace_root=args.workspace_root,
        poll_interval=args.poll_interval,
        heartbeat_interval=args.heartbeat_interval,
        name=args.name,
    )
    asyncio.run(daemon.run())


if __name__ == "__main__":
    main()
