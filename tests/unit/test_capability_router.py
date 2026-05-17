from __future__ import annotations

from bs4 import BeautifulSoup
from PIL import Image

from orchestrator.capabilities.interfaces import CapabilityInvocationContext
from orchestrator.capabilities.router import CapabilityRouter
from orchestrator.config import CapabilitySettings


def test_parse_search_html_extracts_urls() -> None:
    router = CapabilityRouter(CapabilitySettings())
    soup = BeautifulSoup(
        """
        <html><body>
          <a href="https://example.com/article">Example Article</a>
          <a href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fdocs.example.com%2Fguide">Docs Guide</a>
        </body></html>
        """,
        "html.parser",
    )

    results = router._parse_search_html(soup)

    assert [item["url"] for item in results] == [
        "https://example.com/article",
        "https://docs.example.com/guide",
    ]


async def test_document_read_local_markdown(tmp_path) -> None:
    doc = tmp_path / "notes.md"
    doc.write_text("# Title\n\nBody paragraph\n\n## Details\nMore text", encoding="utf-8")
    settings = CapabilitySettings(
        allowed_local_roots=str(tmp_path),
    )
    router = CapabilityRouter(settings)

    result = await router.execute(
        "document.read",
        {"location": str(doc)},
        context=CapabilityInvocationContext(entrypoint="test"),
    )

    assert result.status == "success"
    assert "Title" in result.summary
    assert result.artifacts[0].artifact_type == "document_text"


async def test_image_analyze_local_png_without_ocr(tmp_path, monkeypatch) -> None:
    image_path = tmp_path / "sample.png"
    image = Image.new("RGB", (64, 32), color="white")
    image.save(image_path)
    settings = CapabilitySettings(
        allowed_local_roots=str(tmp_path),
    )
    router = CapabilityRouter(settings)
    monkeypatch.setattr("orchestrator.capabilities.router.pytesseract", None)

    result = await router.execute(
        "image.analyze",
        {"location": str(image_path)},
        context=CapabilityInvocationContext(entrypoint="test"),
    )

    assert result.status == "success"
    assert "64x32" in result.summary
    assert result.artifacts[0].metadata["ocr_available"] is False
