"""OpenAI-compatible chat surface backed by the Capability Layer."""

from __future__ import annotations

import time
import uuid
from datetime import UTC, datetime
from typing import Any

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel, Field

from orchestrator.config import CapabilitySettings
from orchestrator.research.service import ResearchService, ResearchServiceError

router = APIRouter(prefix="/v1", tags=["openai-tools"])


def _get_research_service() -> ResearchService:
    raise NotImplementedError("ResearchService dependency not configured")


def _get_capability_settings() -> CapabilitySettings:
    raise NotImplementedError("CapabilitySettings dependency not configured")


class OpenAIChatMessage(BaseModel):
    role: str
    content: str | list[dict[str, Any]]


class OpenAIChatRequest(BaseModel):
    model: str
    messages: list[OpenAIChatMessage]
    temperature: float | None = None
    max_tokens: int | None = None


@router.get("/models")
async def list_models(
    settings: CapabilitySettings = Depends(_get_capability_settings),
) -> dict[str, Any]:
    now = int(time.time())
    return {
        "object": "list",
        "data": [
            {
                "id": settings.openai_tools_model_id,
                "object": "model",
                "created": now,
                "owned_by": "orchestrator",
            }
        ],
    }


@router.post("/chat/completions")
async def chat_completions(
    body: OpenAIChatRequest,
    research_service: ResearchService = Depends(_get_research_service),
    settings: CapabilitySettings = Depends(_get_capability_settings),
) -> dict[str, Any]:
    if body.model != settings.openai_tools_model_id:
        raise HTTPException(status_code=400, detail=f"Unsupported model: {body.model}")

    prompt = _extract_prompt(body.messages)
    if not prompt:
        content = (
            "No se detectó una consulta utilizable. Escribe una pregunta, una URL, una ruta de documento o una imagen."
        )
        return _chat_response(body.model, content)
    try:
        result = await research_service.query(prompt, entrypoint="open_webui")
    except ResearchServiceError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    sources = "\n".join(
        f"- {ref.title or ref.uri}: {ref.uri}"
        for ref in result.sources[:5]
        if ref.uri or ref.title
    )
    content = result.answer
    if result.confidence is not None:
        content += f"\n\nConfidence: {result.confidence:.2f}"
    if sources:
        content += f"\n\nSources:\n{sources}"
    return _chat_response(body.model, content)


def _extract_prompt(messages: list[OpenAIChatMessage]) -> str:
    for message in reversed(messages):
        if message.role != "user":
            continue
        if isinstance(message.content, str):
            return message.content.strip()
        parts = []
        for item in message.content:
            if item.get("type") == "text":
                parts.append(str(item.get("text", "")).strip())
        return " ".join(part for part in parts if part).strip()
    return ""


def _chat_response(model: str, content: str) -> dict[str, Any]:
    created = int(datetime.now(UTC).timestamp())
    return {
        "id": f"chatcmpl-{uuid.uuid4().hex}",
        "object": "chat.completion",
        "created": created,
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
    }
