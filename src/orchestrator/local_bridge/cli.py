"""Operational CLI for the Local Agent Bridge."""

from __future__ import annotations

import argparse
import asyncio
import os
import socket
import uuid

import httpx


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Operate the Local Agent Bridge")
    sub = parser.add_subparsers(dest="command", required=True)

    common = argparse.ArgumentParser(add_help=False)
    common.add_argument("--base-url", default=os.environ.get("LAB_AGENT_BASE_URL", "http://127.0.0.1:8100"))
    common.add_argument("--api-key", default=os.environ.get("LAB_AGENT_API_KEY", ""))
    common.add_argument("--bridge-id", default=os.environ.get("LAB_AGENT_BRIDGE_ID", str(uuid.uuid4())))
    common.add_argument("--workspace-root", default=os.environ.get("LAB_AGENT_WORKSPACE_ROOT", os.getcwd()))
    common.add_argument("--name", default=os.environ.get("LAB_AGENT_NAME", "lab-agentd"))

    sub.add_parser("register", parents=[common], help="Register the current workspace bridge")
    sub.add_parser("heartbeat", parents=[common], help="Send a bridge heartbeat")
    sub.add_parser("list", parents=[common], help="List active bridges")
    sub.add_parser("smoke", parents=[common], help="Register + heartbeat + claim once")
    return parser


async def _request(args, method: str, path: str, json_body: dict | None = None) -> httpx.Response:
    if not args.api_key:
        raise SystemExit("LAB_AGENT_API_KEY or --api-key is required")
    async with httpx.AsyncClient(timeout=20.0) as client:
        response = await client.request(
            method,
            f"{args.base_url.rstrip('/')}{path}",
            headers={"Authorization": f"Bearer {args.api_key}"},
            json=json_body,
        )
        response.raise_for_status()
        return response


async def _run(args) -> None:
    hostname = socket.gethostname()
    if args.command == "register":
        response = await _request(
            args,
            "POST",
            "/bridges/register",
            {
                "bridge_id": args.bridge_id,
                "name": args.name,
                "hostname": hostname,
                "workspace_root": os.path.abspath(args.workspace_root),
                "capabilities": {"tools": ["read_file", "list_files", "write_file", "apply_patch", "run_command", "git_status", "git_diff", "run_tests"]},
            },
        )
        print(response.text)
        return
    if args.command == "heartbeat":
        response = await _request(args, "POST", f"/bridges/{args.bridge_id}/heartbeat", {"status": "active"})
        print(response.text)
        return
    if args.command == "list":
        response = await _request(args, "GET", "/bridges")
        print(response.text)
        return
    if args.command == "smoke":
        await _request(
            args,
            "POST",
            "/bridges/register",
            {
                "bridge_id": args.bridge_id,
                "name": args.name,
                "hostname": hostname,
                "workspace_root": os.path.abspath(args.workspace_root),
                "capabilities": {"tools": ["read_file", "list_files", "write_file", "apply_patch", "run_command", "git_status", "git_diff", "run_tests"]},
            },
        )
        await _request(args, "POST", f"/bridges/{args.bridge_id}/heartbeat", {"status": "active"})
        response = await _request(args, "POST", f"/bridges/{args.bridge_id}/claim-next")
        print(response.text)


def main() -> None:
    args = build_parser().parse_args()
    asyncio.run(_run(args))


if __name__ == "__main__":
    main()
