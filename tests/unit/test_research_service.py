from __future__ import annotations

from orchestrator.config import CapabilitySettings, LlamaChatSettings, OpenAIReferenceSettings
from orchestrator.research.service import ResearchService


def _build_service() -> ResearchService:
    return ResearchService(
        session_factory=None,  # type: ignore[arg-type]
        capability_service=None,  # type: ignore[arg-type]
        capability_settings=CapabilitySettings(),
        llama_settings=LlamaChatSettings(),
        reference_settings=OpenAIReferenceSettings(),
    )


def test_classify_intent_detects_url_document_and_image() -> None:
    service = _build_service()

    assert service.classify_intent("resume https://example.com/page") == "url_summary"
    assert service.classify_intent("analiza ./captura.png") == "image_qa"
    assert service.classify_intent("lee /tmp/spec.pdf y resume") == "document_qa"
    assert service.classify_intent("últimas novedades de postgres") == "search_answer"


def test_select_fetch_count_scales_for_comparative_or_low_diversity_queries() -> None:
    service = _build_service()

    sparse_results = [
        {"url": "https://example.com/a", "snippet": "short"},
        {"url": "https://example.com/b", "snippet": "tiny"},
        {"url": "https://example.com/c", "snippet": "mini"},
    ]
    assert service.select_fetch_count("compare redis vs nats", sparse_results) == 3

    richer_results = [
        {"url": "https://a.example.com/post", "snippet": "x" * 80},
        {"url": "https://b.example.com/post", "snippet": "y" * 90},
        {"url": "https://c.example.com/post", "snippet": "z" * 85},
        {"url": "https://d.example.com/post", "snippet": "w" * 88},
    ]
    assert service.select_fetch_count("estado actual de fastapi", richer_results) == 3


def test_extract_json_object_accepts_fenced_json() -> None:
    service = _build_service()

    data = service._extract_json_object(
        """```json
        {"answer":"ok","confidence":0.7,"limitations":["x"]}
        ```"""
    )

    assert data["answer"] == "ok"
    assert data["confidence"] == 0.7
