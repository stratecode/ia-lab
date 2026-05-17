"""API routes for Local Agent Bridge registration and polling."""

from __future__ import annotations

import uuid

from fastapi import APIRouter, Depends, HTTPException, status

from orchestrator.api.schemas.bridges import (
    BridgeHeartbeatRequest,
    BridgeListResponse,
    BridgeRegisterRequest,
    BridgeResponse,
    BridgeResultRequest,
    BridgeTaskClaimResponse,
)
from orchestrator.api.schemas.common import ErrorResponse
from orchestrator.local_bridge.service import LocalBridgeService

router = APIRouter(prefix="/bridges", tags=["bridges"])


def _get_local_bridge_service() -> LocalBridgeService:
    raise NotImplementedError("LocalBridgeService dependency not configured")


@router.post(
    "/register",
    response_model=BridgeResponse,
    status_code=status.HTTP_200_OK,
)
async def register_bridge(
    body: BridgeRegisterRequest,
    bridge_service: LocalBridgeService = Depends(_get_local_bridge_service),
) -> BridgeResponse:
    bridge = await bridge_service.register_bridge(
        bridge_id=str(body.bridge_id),
        name=body.name,
        hostname=body.hostname,
        workspace_root=body.workspace_root,
        capabilities=body.capabilities,
        api_key_name=body.api_key_name,
    )
    return BridgeResponse.model_validate(bridge)


@router.post(
    "/{bridge_id}/heartbeat",
    response_model=BridgeResponse,
    responses={404: {"model": ErrorResponse}},
)
async def heartbeat_bridge(
    bridge_id: uuid.UUID,
    body: BridgeHeartbeatRequest,
    bridge_service: LocalBridgeService = Depends(_get_local_bridge_service),
) -> BridgeResponse:
    try:
        bridge = await bridge_service.heartbeat(str(bridge_id), status=body.status)
    except ValueError as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    return BridgeResponse.model_validate(bridge)


@router.get("", response_model=BridgeListResponse)
async def list_bridges(
    bridge_service: LocalBridgeService = Depends(_get_local_bridge_service),
) -> BridgeListResponse:
    items = [BridgeResponse.model_validate(item) for item in await bridge_service.list_bridges()]
    return BridgeListResponse(items=items, total=len(items))


@router.post(
    "/{bridge_id}/claim-next",
    response_model=BridgeTaskClaimResponse | None,
    responses={404: {"model": ErrorResponse}},
)
async def claim_next_task(
    bridge_id: uuid.UUID,
    bridge_service: LocalBridgeService = Depends(_get_local_bridge_service),
) -> BridgeTaskClaimResponse | None:
    try:
        claim = await bridge_service.claim_next_task(str(bridge_id))
    except ValueError as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    if claim is None:
        return None
    return BridgeTaskClaimResponse.model_validate(claim.__dict__)


@router.post(
    "/{bridge_id}/tasks/{task_id}/result",
    status_code=status.HTTP_202_ACCEPTED,
    responses={404: {"model": ErrorResponse}},
)
async def submit_task_result(
    bridge_id: uuid.UUID,
    task_id: uuid.UUID,
    body: BridgeResultRequest,
    bridge_service: LocalBridgeService = Depends(_get_local_bridge_service),
) -> dict[str, str]:
    try:
        await bridge_service.submit_result(
            str(bridge_id),
            str(task_id),
            body.model_dump(mode="json"),
        )
    except ValueError as exc:
        raise HTTPException(status_code=404, detail=str(exc))
    return {"status": "accepted"}
