"""Research orchestration and evaluation service."""

from __future__ import annotations

import json
import re
from datetime import UTC, datetime
from typing import Any
from urllib.parse import urlparse
from uuid import UUID

import httpx
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker

from orchestrator.capabilities.interfaces import SourceRef
from orchestrator.capabilities.service import CapabilityService
from orchestrator.config import CapabilitySettings, LlamaChatSettings, OpenAIReferenceSettings
from orchestrator.persistence.models import EvaluationDatasetItem, EvaluationRun, ResearchRun
from orchestrator.persistence.repositories.evaluation_dataset_items import (
    EvaluationDatasetItemRepository,
)
from orchestrator.persistence.repositories.evaluation_runs import EvaluationRunRepository
from orchestrator.persistence.repositories.research_runs import ResearchRunRepository
from orchestrator.persistence.repositories.artifacts import ArtifactRepository
from orchestrator.research.interfaces import (
    EvaluationDatasetItemRecord,
    EvaluationJudgeResult,
    EvaluationRunRecord,
    ResearchIntent,
    ResearchQueryResult,
    ResearchRunRecord,
)


class ResearchServiceError(Exception):
    """Raised when the research service cannot complete a request."""


class ResearchService:
    """Orchestrate multi-step research and evaluation flows."""

    def __init__(
        self,
        session_factory: async_sessionmaker[AsyncSession],
        capability_service: CapabilityService,
        capability_settings: CapabilitySettings,
        llama_settings: LlamaChatSettings,
        reference_settings: OpenAIReferenceSettings,
    ) -> None:
        self._session_factory = session_factory
        self._capability_service = capability_service
        self._capability_settings = capability_settings
        self._llama_settings = llama_settings
        self._reference_settings = reference_settings

    async def query(
        self,
        query: str,
        *,
        task_id: str | None = None,
        entrypoint: str = "api",
        agent_type=None,
        allowed_capabilities: list[str] | None = None,
        mode_hint: ResearchIntent | None = None,
        evaluate_against_reference: bool = False,
    ) -> ResearchQueryResult:
        normalized_query = query.strip()
        if not normalized_query:
            raise ResearchServiceError("query is required")

        intent = mode_hint or self.classify_intent(normalized_query)
        selected_capabilities = self._resolve_capabilities(intent, allowed_capabilities)
        if not selected_capabilities:
            raise ResearchServiceError("No capabilities available for this research query")

        task_uuid = UUID(task_id) if task_id else None
        invocations = []
        sources: list[SourceRef] = []

        if intent == "search_answer":
            search_record = await self._capability_service.invoke(
                "web.search",
                {"query": normalized_query},
                task_id=task_id,
                agent_type=agent_type,
                entrypoint=entrypoint,
                allowed_capabilities=selected_capabilities,
            )
            invocations.append(search_record)
            sources.extend(search_record.source_refs)
            results = list(search_record.output_payload.get("results") or [])
            fetch_count = self.select_fetch_count(normalized_query, results)
            if "web.fetch" in selected_capabilities:
                for item in results[:fetch_count]:
                    url = str(item.get("url") or "").strip()
                    if not url:
                        continue
                    fetch_record = await self._capability_service.invoke(
                        "web.fetch",
                        {"url": url},
                        task_id=task_id,
                        agent_type=agent_type,
                        entrypoint=entrypoint,
                        allowed_capabilities=selected_capabilities,
                    )
                    invocations.append(fetch_record)
                    if len(str(fetch_record.output_payload.get("content_text") or "")) >= self._capability_settings.min_source_content_chars:
                        sources.extend(fetch_record.source_refs)
        elif intent == "url_summary":
            url = self._extract_first_url(normalized_query)
            if not url:
                raise ResearchServiceError("No URL found in the query")
            record = await self._capability_service.invoke(
                "web.fetch",
                {"url": url},
                task_id=task_id,
                agent_type=agent_type,
                entrypoint=entrypoint,
                allowed_capabilities=selected_capabilities,
            )
            invocations.append(record)
            sources.extend(record.source_refs)
        elif intent == "document_qa":
            location = self._extract_location(normalized_query)
            if not location:
                raise ResearchServiceError("No document path or URL found in the query")
            record = await self._capability_service.invoke(
                "document.read",
                {"location": location},
                task_id=task_id,
                agent_type=agent_type,
                entrypoint=entrypoint,
                allowed_capabilities=selected_capabilities,
            )
            invocations.append(record)
            sources.extend(record.source_refs)
        elif intent == "image_qa":
            location = self._extract_location(normalized_query)
            if not location:
                raise ResearchServiceError("No image path or URL found in the query")
            record = await self._capability_service.invoke(
                "image.analyze",
                {"location": location},
                task_id=task_id,
                agent_type=agent_type,
                entrypoint=entrypoint,
                allowed_capabilities=selected_capabilities,
            )
            invocations.append(record)
            sources.extend(record.source_refs)
        else:  # pragma: no cover
            raise ResearchServiceError(f"Unsupported intent: {intent}")

        source_blocks = await self._build_source_blocks(invocations)
        answer, confidence, synthesis_metadata = await self._synthesize_answer(
            query=normalized_query,
            intent=intent,
            source_blocks=source_blocks,
        )

        artifact_ids = []
        invocation_ids = []
        for invocation in invocations:
            invocation_ids.append(str(invocation.id))
            artifact_ids.extend(invocation.artifact_ids)

        run = await self._store_research_run(
            task_id=task_uuid,
            entrypoint=entrypoint,
            query=normalized_query,
            intent=intent,
            selected_capabilities=selected_capabilities,
            final_answer=answer,
            confidence=confidence,
            source_artifact_ids=artifact_ids,
            tool_invocation_ids=invocation_ids,
            metadata=synthesis_metadata,
        )

        evaluation = None
        if evaluate_against_reference:
            evaluation = await self.evaluate_query(run.id)

        return ResearchQueryResult(
            research_run=run,
            answer=answer,
            confidence=confidence,
            sources=sources[:10],
            tool_invocation_ids=invocation_ids,
            evaluation=evaluation,
        )

    def classify_intent(self, query: str) -> ResearchIntent:
        lowered = query.lower()
        url = self._extract_first_url(query)
        location = self._extract_location(query)
        if url:
            if self._looks_like_image(url, lowered):
                return "image_qa"
            if self._looks_like_document(url, lowered):
                return "document_qa"
            return "url_summary"
        if location:
            if self._looks_like_image(location, lowered):
                return "image_qa"
            if self._looks_like_document(location, lowered):
                return "document_qa"
        return "search_answer"

    def select_fetch_count(self, query: str, results: list[dict[str, Any]]) -> int:
        target = self._capability_settings.default_search_fetch_count
        lowered = query.lower()
        comparative_terms = (" vs ", " compare", "compar", "mejor", "best", "tradeoff", "pros", "contras", "diferencia")
        domains = {
            urlparse(str(item.get("url") or "")).netloc
            for item in results
            if item.get("url")
        }
        avg_snippet_len = 0.0
        snippets = [str(item.get("snippet") or "") for item in results if item.get("snippet")]
        if snippets:
            avg_snippet_len = sum(len(item) for item in snippets) / len(snippets)
        if any(term in lowered for term in comparative_terms):
            target = self._capability_settings.max_search_fetch_count
        elif len(domains) < 3 or avg_snippet_len < 40:
            target = self._capability_settings.max_search_fetch_count
        return max(1, min(target, self._capability_settings.max_search_fetch_count, len(results) or 1))

    async def get_research_run(self, research_run_id: UUID) -> ResearchRunRecord | None:
        async with self._session_factory() as session:
            repo = ResearchRunRepository(session)
            record = await repo.get_by_id(research_run_id)
        if record is None:
            return None
        return self._serialize_research_run(record)

    async def create_reference(self, research_run_id: UUID) -> EvaluationRunRecord:
        if not self._reference_settings.reference_api_key:
            raise ResearchServiceError("OpenAI reference API key is not configured")
        research_run = await self._require_research_run(research_run_id)
        reference_answer = await self._call_openai_chat(
            model=self._reference_settings.reference_model,
            system_prompt=(
                "Eres un asistente de investigación riguroso. Responde a la pregunta del usuario "
                "con una respuesta clara y útil. Si faltan datos, dilo."
            ),
            user_prompt=research_run.query,
        )
        async with self._session_factory() as session:
            async with session.begin():
                repo = EvaluationRunRepository(session)
                record = await repo.create(
                    research_run_id=research_run_id,
                    reference_provider="openai",
                    reference_model=self._reference_settings.reference_model,
                    reference_answer=reference_answer,
                    judge_model=self._reference_settings.judge_model,
                )
            await session.refresh(record)
        return self._serialize_evaluation_run(record)

    async def judge_evaluation(self, evaluation_run_id: UUID) -> EvaluationRunRecord:
        if not self._reference_settings.reference_api_key:
            raise ResearchServiceError("OpenAI reference API key is not configured")
        evaluation_run, research_run = await self._require_evaluation_with_research(evaluation_run_id)
        if not evaluation_run.reference_answer:
            raise ResearchServiceError("Evaluation run has no reference answer")

        verdict = await self._judge_answers(research_run, evaluation_run.reference_answer)
        async with self._session_factory() as session:
            async with session.begin():
                eval_repo = EvaluationRunRepository(session)
                dataset_repo = EvaluationDatasetItemRepository(session)
                record = await eval_repo.get_by_id(evaluation_run_id)
                assert record is not None
                record.judge_model = self._reference_settings.judge_model
                record.judge_verdict = verdict.model_dump(mode="json")
                record.judge_scores = {
                    "accuracy_score": verdict.accuracy_score,
                    "coverage_score": verdict.coverage_score,
                    "source_use_score": verdict.source_use_score,
                    "usefulness_score": verdict.usefulness_score,
                    "hallucination_risk_score": verdict.hallucination_risk_score,
                }
                record.winner = verdict.winner
                await dataset_repo.create(
                    research_run_id=research_run.id,
                    evaluation_run_id=record.id,
                    query=research_run.query,
                    orchestrator_answer=research_run.final_answer,
                    reference_answer=evaluation_run.reference_answer,
                    sources=await self._load_dataset_sources(research_run),
                    scores=record.judge_scores,
                    winner=verdict.winner,
                    metadata={"judge_reasoning": verdict.reasoning},
                )
            await session.refresh(record)
        return self._serialize_evaluation_run(record)

    async def evaluate_query(self, research_run_id: UUID) -> EvaluationRunRecord:
        evaluation = await self.create_reference(research_run_id)
        return await self.judge_evaluation(evaluation.id)

    async def get_evaluation_run(self, evaluation_run_id: UUID) -> EvaluationRunRecord | None:
        async with self._session_factory() as session:
            repo = EvaluationRunRepository(session)
            record = await repo.get_by_id(evaluation_run_id)
        if record is None:
            return None
        return self._serialize_evaluation_run(record)

    async def list_dataset_items(self, limit: int = 100) -> list[EvaluationDatasetItemRecord]:
        async with self._session_factory() as session:
            repo = EvaluationDatasetItemRepository(session)
            items = await repo.list_recent(limit=limit)
        return [self._serialize_dataset_item(item) for item in items]

    async def _store_research_run(
        self,
        *,
        task_id: UUID | None,
        entrypoint: str,
        query: str,
        intent: ResearchIntent,
        selected_capabilities: list[str],
        final_answer: str,
        confidence: float | None,
        source_artifact_ids: list[str],
        tool_invocation_ids: list[str],
        metadata: dict[str, Any],
    ) -> ResearchRunRecord:
        async with self._session_factory() as session:
            async with session.begin():
                repo = ResearchRunRepository(session)
                record = await repo.create(
                    task_id=task_id,
                    entrypoint=entrypoint,
                    query=query,
                    intent_type=intent,
                    selected_capabilities=selected_capabilities,
                    final_answer=final_answer,
                    confidence=confidence,
                    source_artifact_ids=source_artifact_ids,
                    tool_invocation_ids=tool_invocation_ids,
                    metadata=metadata,
                )
            await session.refresh(record)
        return self._serialize_research_run(record)

    async def _build_source_blocks(self, invocations) -> list[dict[str, Any]]:
        blocks: list[dict[str, Any]] = []
        for invocation in invocations:
            for ref in invocation.source_refs[:5]:
                payload = invocation.output_payload or {}
                content = str(payload.get("content_text") or payload.get("summary") or "")[:4000]
                blocks.append(
                    {
                        "title": ref.title or ref.uri or invocation.capability,
                        "uri": ref.uri,
                        "kind": ref.kind,
                        "content": content,
                    }
                )
        return blocks

    async def _synthesize_answer(
        self,
        *,
        query: str,
        intent: ResearchIntent,
        source_blocks: list[dict[str, Any]],
    ) -> tuple[str, float | None, dict[str, Any]]:
        sources_text = "\n\n".join(
            f"[{idx}] {block['title']}\nURL: {block.get('uri') or 'n/a'}\n{block['content'][:2400]}"
            for idx, block in enumerate(source_blocks[:5], start=1)
        )
        prompt = (
            "Eres un research assistant riguroso. Responde directamente a la consulta usando solo las fuentes "
            "proporcionadas. Si faltan datos o hay contradicciones, dilo con claridad. Devuelve SOLO JSON válido "
            'con esta forma: {"answer":"...","confidence":0.0,"limitations":["..."]}. '
            f"Intent: {intent}\n\n"
            f"Consulta:\n{query}\n\n"
            f"Fuentes:\n{sources_text}"
        )
        content = await self._call_llama_utility(prompt)
        try:
            data = self._extract_json_object(content)
            answer = str(data.get("answer") or "").strip()
            confidence = data.get("confidence")
            limitations = data.get("limitations") or []
        except Exception:
            answer = content.strip()
            confidence = None
            limitations = []
        if not answer:
            raise ResearchServiceError("Synthesis returned an empty answer")
        if limitations:
            clean_limitations = [
                str(item).strip()
                for item in limitations
                if str(item).strip()
            ]
            if clean_limitations:
                answer = (
                    f"{answer}\n\nLimitations:\n"
                    + "\n".join(f"- {item}" for item in clean_limitations[:5])
                )
        return answer, float(confidence) if confidence is not None else None, {
            "limitations": limitations,
            "source_count": len(source_blocks),
        }

    def _extract_json_object(self, content: str) -> dict[str, Any]:
        raw = content.strip()
        if raw.startswith("```"):
            fenced = re.match(r"^```(?:json)?\s*(.*?)\s*```$", raw, re.DOTALL | re.IGNORECASE)
            if fenced:
                raw = fenced.group(1).strip()
        return json.loads(raw)

    async def _call_llama_utility(self, prompt: str) -> str:
        headers = {"Content-Type": "application/json"}
        if self._llama_settings.utility_api_key:
            headers["Authorization"] = f"Bearer {self._llama_settings.utility_api_key}"
        payload = {
            "model": self._capability_settings.utility_model_name,
            "messages": [{"role": "user", "content": prompt}],
            "temperature": 0.1,
            "max_tokens": 1800,
            "response_format": {"type": "json_object"},
        }
        async with httpx.AsyncClient(timeout=self._llama_settings.timeout_seconds) as client:
            response = await client.post(
                f"{self._llama_settings.utility_base_url.rstrip('/')}/chat/completions",
                json=payload,
                headers=headers,
            )
            response.raise_for_status()
        return (
            response.json()
            .get("choices", [{}])[0]
            .get("message", {})
            .get("content", "")
            .strip()
        )

    async def _call_openai_chat(
        self,
        *,
        model: str,
        system_prompt: str,
        user_prompt: str,
        response_format: dict[str, Any] | None = None,
    ) -> str:
        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self._reference_settings.reference_api_key}",
        }
        payload: dict[str, Any] = {
            "model": model,
            "messages": [
                {"role": "system", "content": system_prompt},
                {"role": "user", "content": user_prompt},
            ],
            "temperature": 0.1,
            "max_tokens": 1800,
        }
        if response_format is not None:
            payload["response_format"] = response_format
        async with httpx.AsyncClient(timeout=self._reference_settings.timeout_seconds) as client:
            response = await client.post(
                f"{self._reference_settings.base_url.rstrip('/')}/chat/completions",
                json=payload,
                headers=headers,
            )
            response.raise_for_status()
        return (
            response.json()
            .get("choices", [{}])[0]
            .get("message", {})
            .get("content", "")
            .strip()
        )

    async def _judge_answers(
        self,
        research_run: ResearchRunRecord,
        reference_answer: str,
    ) -> EvaluationJudgeResult:
        sources = await self._load_dataset_sources(research_run)
        prompt = (
            "Compara dos respuestas a la misma pregunta. Devuelve SOLO JSON válido con: "
            '{"accuracy_score":0.0,"coverage_score":0.0,"source_use_score":0.0,'
            '"usefulness_score":0.0,"hallucination_risk_score":0.0,'
            '"winner":"orchestrator|reference|tie","reasoning":"..."}.\n\n'
            f"Pregunta:\n{research_run.query}\n\n"
            f"Respuesta orquestador:\n{research_run.final_answer}\n\n"
            f"Respuesta referencia:\n{reference_answer}\n\n"
            f"Fuentes del orquestador:\n{json.dumps(sources, ensure_ascii=False)[:7000]}"
        )
        content = await self._call_openai_chat(
            model=self._reference_settings.judge_model,
            system_prompt="Eres un evaluador estricto de respuestas de investigación.",
            user_prompt=prompt,
            response_format={"type": "json_object"},
        )
        try:
            data = json.loads(content)
            return EvaluationJudgeResult.model_validate(data)
        except Exception as exc:  # pragma: no cover - depends on external model
            raise ResearchServiceError(f"Judge returned invalid JSON: {exc}") from exc

    def _resolve_capabilities(
        self,
        intent: ResearchIntent,
        requested: list[str] | None,
    ) -> list[str]:
        default_allowed = {
            "search_answer": ["web.search", "web.fetch"],
            "url_summary": ["web.fetch"],
            "document_qa": ["document.read"],
            "image_qa": ["image.analyze"],
        }[intent]
        if not requested:
            return default_allowed
        requested_set = set(requested)
        return [cap for cap in default_allowed if cap in requested_set]

    def _extract_first_url(self, query: str) -> str | None:
        match = re.search(r"https?://\S+", query)
        return match.group(0).rstrip(".,)") if match else None

    def _extract_location(self, query: str) -> str | None:
        url = self._extract_first_url(query)
        if url:
            return url
        match = re.search(r"(/[^ \n]+|\./[^ \n]+|~\/[^ \n]+)", query)
        return match.group(0).rstrip(".,)") if match else None

    def _looks_like_document(self, value: str, lowered: str) -> bool:
        return any(value.lower().endswith(ext) for ext in (".pdf", ".docx", ".md", ".txt")) or "documento" in lowered or "document" in lowered or "pdf" in lowered

    def _looks_like_image(self, value: str, lowered: str) -> bool:
        return any(value.lower().endswith(ext) for ext in (".png", ".jpg", ".jpeg", ".gif", ".webp")) or "imagen" in lowered or "image" in lowered or "ocr" in lowered

    async def _require_research_run(self, research_run_id: UUID) -> ResearchRunRecord:
        run = await self.get_research_run(research_run_id)
        if run is None:
            raise ResearchServiceError(f"Research run {research_run_id} not found")
        return run

    async def _require_evaluation_with_research(
        self, evaluation_run_id: UUID
    ) -> tuple[EvaluationRunRecord, ResearchRunRecord]:
        evaluation = await self.get_evaluation_run(evaluation_run_id)
        if evaluation is None:
            raise ResearchServiceError(f"Evaluation run {evaluation_run_id} not found")
        research = await self._require_research_run(evaluation.research_run_id)
        return evaluation, research

    async def _load_dataset_sources(self, research_run: ResearchRunRecord) -> list[dict[str, Any]]:
        artifact_uuids = [UUID(item) for item in research_run.source_artifact_ids[:10]]
        async with self._session_factory() as session:
            repo = ArtifactRepository(session)
            items = await repo.list_by_ids(artifact_uuids)
        return [
            {
                "artifact_id": str(item.id),
                "artifact_type": item.artifact_type,
                "title": item.title,
                "uri": item.uri,
                "media_type": item.media_type,
            }
            for item in items
        ]

    def _serialize_research_run(self, record: ResearchRun) -> ResearchRunRecord:
        return ResearchRunRecord(
            id=record.id,
            task_id=record.task_id,
            entrypoint=record.entrypoint,
            query=record.query,
            intent_type=record.intent_type,  # type: ignore[arg-type]
            selected_capabilities=list(record.selected_capabilities or []),
            final_answer=record.final_answer,
            confidence=record.confidence,
            source_artifact_ids=list(record.source_artifact_ids or []),
            tool_invocation_ids=list(record.tool_invocation_ids or []),
            metadata=record.metadata_ or {},
            created_at=record.created_at,
        )

    def _serialize_evaluation_run(self, record: EvaluationRun) -> EvaluationRunRecord:
        return EvaluationRunRecord(
            id=record.id,
            research_run_id=record.research_run_id,
            reference_provider=record.reference_provider,
            reference_model=record.reference_model,
            reference_answer=record.reference_answer,
            judge_model=record.judge_model,
            judge_verdict=record.judge_verdict or {},
            judge_scores=record.judge_scores or {},
            winner=record.winner,
            created_at=record.created_at,
        )

    def _serialize_dataset_item(
        self, record: EvaluationDatasetItem
    ) -> EvaluationDatasetItemRecord:
        return EvaluationDatasetItemRecord(
            id=record.id,
            research_run_id=record.research_run_id,
            evaluation_run_id=record.evaluation_run_id,
            query=record.query,
            orchestrator_answer=record.orchestrator_answer,
            reference_answer=record.reference_answer,
            sources=list(record.sources or []),
            scores=record.scores or {},
            winner=record.winner,
            metadata=record.metadata_ or {},
            created_at=record.created_at,
        )
