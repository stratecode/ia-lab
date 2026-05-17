"""Planner execution and structured plan validation for Phase 5A."""

from __future__ import annotations

import json
from dataclasses import dataclass

import httpx
from pydantic import BaseModel, Field, ValidationError

from orchestrator.state_machine.transitions import AgentType, Priority


class PlannedSubtask(BaseModel):
    title: str = Field(..., min_length=1, max_length=200)
    description: str = Field(..., min_length=1, max_length=5000)
    assigned_agent: AgentType
    priority: Priority = Priority.NORMAL
    requires_approval: bool = False


class PlannerOutput(BaseModel):
    summary: str = Field(..., min_length=1, max_length=2000)
    plan: str = Field(..., min_length=1, max_length=10000)
    subtasks: list[PlannedSubtask] = Field(default_factory=list)


@dataclass(frozen=True)
class PlannerExecutionResult:
    summary: str
    plan: str
    subtasks: list[PlannedSubtask]
    raw_response: str


class PlannerOutputError(Exception):
    """Raised when the planner returns invalid structured output."""


class PlannerService:
    """Calls the planner model and validates a structured JSON plan."""

    def __init__(
        self,
        base_url: str,
        api_key: str,
        timeout_seconds: float = 90.0,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._api_key = api_key
        self._timeout_seconds = timeout_seconds

    async def create_plan(
        self,
        description: str,
        metadata: dict | None = None,
    ) -> PlannerExecutionResult:
        headers = {"Content-Type": "application/json"}
        if self._api_key:
            headers["Authorization"] = f"Bearer {self._api_key}"

        metadata_json = json.dumps(metadata or {}, ensure_ascii=True)
        capability_context = metadata.get("capability_context") if metadata else None
        capability_context_text = ""
        if capability_context:
            capability_context_text = "\n\nContexto adicional de capacidades:\n" + "\n\n".join(
                str(item)[:2400] for item in capability_context[:5]
            )
        prompt = (
            "Eres un planner operativo para un orquestador multiagente.\n"
            "Devuelve SOLO JSON valido con esta forma exacta:\n"
            "{"
            '"summary":"...",'
            '"plan":"...",'
            '"subtasks":[{"title":"...","description":"...","assigned_agent":"coder","priority":"normal","requires_approval":false}]'
            "}\n"
            "Reglas:\n"
            "- assigned_agent solo puede ser 'coder' o 'planner'\n"
            "- No incluyas markdown ni texto fuera del JSON\n"
            "- Crea subtareas ejecutables y concretas\n"
            "- Usa requires_approval=true solo si la subtarea parece destructiva o sensible\n\n"
            f"Objetivo:\n{description}\n\n"
            f"Metadata:\n{metadata_json}"
            f"{capability_context_text}"
        )
        payload = {
            "model": "planner",
            "messages": [{"role": "user", "content": prompt}],
            "temperature": 0.1,
            "max_tokens": 1600,
            "response_format": {"type": "json_object"},
        }

        async with httpx.AsyncClient(timeout=self._timeout_seconds) as client:
            response = await client.post(
                f"{self._base_url}/chat/completions",
                json=payload,
                headers=headers,
            )
            response.raise_for_status()

        content = (
            response.json()
            .get("choices", [{}])[0]
            .get("message", {})
            .get("content", "")
            .strip()
        )
        if not content:
            raise PlannerOutputError("planner returned empty content")

        try:
            data = json.loads(content)
            parsed = PlannerOutput.model_validate(data)
        except (json.JSONDecodeError, ValidationError) as exc:
            raise PlannerOutputError(f"invalid planner output: {exc}") from exc

        for subtask in parsed.subtasks:
            if subtask.assigned_agent not in {AgentType.CODER, AgentType.PLANNER}:
                raise PlannerOutputError(
                    f"unsupported assigned_agent in subtask: {subtask.assigned_agent}"
                )

        return PlannerExecutionResult(
            summary=parsed.summary,
            plan=parsed.plan,
            subtasks=parsed.subtasks,
            raw_response=content,
        )
