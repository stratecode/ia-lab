"""Capability Layer v1 router and provider implementations."""

from __future__ import annotations

import io
import mimetypes
import re
import time
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, unquote, urlparse

import httpx
from bs4 import BeautifulSoup
from docx import Document as DocxDocument
from PIL import Image
from pypdf import PdfReader

from orchestrator.capabilities.interfaces import (
    ArtifactPayload,
    CapabilityExecutionResult,
    CapabilityInvocationContext,
    CapabilityName,
    SourceRef,
)
from orchestrator.config import CapabilitySettings

try:
    import pytesseract
except Exception:  # pragma: no cover - optional runtime dependency
    pytesseract = None


class CapabilityError(Exception):
    """Raised when capability execution fails."""


class CapabilityBlockedError(CapabilityError):
    """Raised when a capability input is denied by policy."""


class CapabilityRouter:
    """Routes capability invocations to concrete provider implementations."""

    _CAPABILITIES: dict[str, str] = {
        "web.search": "Search the web and return result links with snippets.",
        "web.fetch": "Fetch and clean the main text content of a URL.",
        "document.read": "Read PDF, DOCX, Markdown, or text documents.",
        "image.analyze": "Inspect an image, returning metadata and OCR when available.",
    }

    def __init__(self, settings: CapabilitySettings) -> None:
        self._settings = settings

    def list_capabilities(self) -> list[dict[str, str]]:
        return [
            {"name": name, "description": description}
            for name, description in self._CAPABILITIES.items()
        ]

    async def execute(
        self,
        capability: CapabilityName,
        payload: dict[str, Any],
        context: CapabilityInvocationContext,
    ) -> CapabilityExecutionResult:
        self._enforce_allowed(capability, context)
        started = time.monotonic()
        try:
            if capability == "web.search":
                result = await self._web_search(str(payload.get("query") or "").strip())
            elif capability == "web.fetch":
                result = await self._web_fetch(str(payload.get("url") or "").strip())
            elif capability == "document.read":
                result = await self._document_read(str(payload.get("location") or "").strip())
            elif capability == "image.analyze":
                result = await self._image_analyze(str(payload.get("location") or "").strip())
            else:  # pragma: no cover - guarded by typing and caller
                raise CapabilityError(f"Unsupported capability: {capability}")
        except CapabilityBlockedError as exc:
            return CapabilityExecutionResult(
                capability=capability,
                status="blocked",
                summary=str(exc),
                output={},
                error_message=str(exc),
                duration_ms=int((time.monotonic() - started) * 1000),
            )
        except httpx.TimeoutException as exc:
            return CapabilityExecutionResult(
                capability=capability,
                status="timeout",
                summary=f"{capability} timed out",
                output={},
                error_message=str(exc),
                duration_ms=int((time.monotonic() - started) * 1000),
            )
        except Exception as exc:
            return CapabilityExecutionResult(
                capability=capability,
                status="error",
                summary=f"{capability} failed",
                output={},
                error_message=str(exc),
                duration_ms=int((time.monotonic() - started) * 1000),
            )

        result.duration_ms = int((time.monotonic() - started) * 1000)
        return result

    def _enforce_allowed(
        self,
        capability: CapabilityName,
        context: CapabilityInvocationContext,
    ) -> None:
        if context.allowed_capabilities is None:
            return
        if capability not in set(context.allowed_capabilities):
            raise CapabilityBlockedError(
                f"Capability '{capability}' is not allowed in this context"
            )

    async def _web_search(self, query: str) -> CapabilityExecutionResult:
        if not query:
            raise CapabilityError("query is required")
        results = await self._fetch_search_results(query)
        if not results:
            raise CapabilityError("no search results found")
        refs = [
            SourceRef(
                title=item["title"],
                uri=item["url"],
                snippet=item.get("snippet"),
                kind="web_search_result",
            )
            for item in results
        ]
        summary_lines = [f"{idx}. {item['title']} — {item['url']}" for idx, item in enumerate(results, start=1)]
        artifact = ArtifactPayload(
            artifact_type="search_results",
            title=f"Search results for: {query}",
            uri=None,
            media_type="application/json",
            content_text="\n".join(summary_lines)[: self._settings.max_artifact_text_chars],
            metadata={"query": query, "results": results},
        )
        return CapabilityExecutionResult(
            capability="web.search",
            status="success",
            summary="\n".join(summary_lines[:5]),
            output={"query": query, "results": results},
            source_refs=refs,
            artifacts=[artifact],
        )

    async def _web_fetch(self, url: str) -> CapabilityExecutionResult:
        if not url:
            raise CapabilityError("url is required")
        resource = await self._load_resource(url, max_bytes=self._settings.max_html_bytes)
        text, title = self._clean_html(resource["bytes"], resource["media_type"])
        text = text[: self._settings.max_artifact_text_chars]
        summary = f"{title or resource['uri']}\n\n{text[:1200].strip()}"
        ref = SourceRef(
            title=title or resource["uri"],
            uri=resource["uri"],
            snippet=text[:300],
            kind="web_page",
        )
        artifact = ArtifactPayload(
            artifact_type="web_page",
            title=title,
            uri=resource["uri"],
            media_type=resource["media_type"],
            content_text=text,
            metadata={"title": title, "content_length": len(text)},
        )
        return CapabilityExecutionResult(
            capability="web.fetch",
            status="success",
            summary=summary.strip(),
            output={"title": title, "content_text": text},
            source_refs=[ref],
            artifacts=[artifact],
        )

    async def _document_read(self, location: str) -> CapabilityExecutionResult:
        if not location:
            raise CapabilityError("location is required")
        resource = await self._load_resource(location, max_bytes=self._settings.max_document_bytes)
        text, sections = self._extract_document(resource["bytes"], resource["uri"], resource["media_type"])
        text = text[: self._settings.max_artifact_text_chars]
        ref = SourceRef(
            title=Path(urlparse(resource["uri"]).path).name or resource["uri"],
            uri=resource["uri"],
            snippet=text[:300],
            kind="document",
            metadata={"sections": sections[:10]},
        )
        artifact = ArtifactPayload(
            artifact_type="document_text",
            title=ref.title,
            uri=resource["uri"],
            media_type=resource["media_type"],
            content_text=text,
            metadata={"sections": sections[:20]},
        )
        summary = f"{ref.title}\n\n{text[:1200].strip()}"
        return CapabilityExecutionResult(
            capability="document.read",
            status="success",
            summary=summary.strip(),
            output={"content_text": text, "sections": sections},
            source_refs=[ref],
            artifacts=[artifact],
        )

    async def _image_analyze(self, location: str) -> CapabilityExecutionResult:
        if not location:
            raise CapabilityError("location is required")
        resource = await self._load_resource(location, max_bytes=self._settings.max_image_bytes)
        image = Image.open(io.BytesIO(resource["bytes"]))
        ocr_text = ""
        if pytesseract is not None:
            try:
                ocr_text = (pytesseract.image_to_string(image) or "").strip()
            except Exception:
                ocr_text = ""
        summary_parts = [
            f"Image: {image.format or 'unknown'}",
            f"Size: {image.width}x{image.height}",
            f"Mode: {image.mode}",
        ]
        if ocr_text:
            summary_parts.append(f"OCR: {ocr_text[:600]}")
        else:
            summary_parts.append("OCR: not available or no text detected")
        summary = "\n".join(summary_parts)
        artifact = ArtifactPayload(
            artifact_type="image_analysis",
            title=Path(urlparse(resource["uri"]).path).name or resource["uri"],
            uri=resource["uri"],
            media_type=resource["media_type"] or Image.MIME.get(image.format or "", "image/*"),
            content_text=ocr_text[: self._settings.max_artifact_text_chars] if ocr_text else None,
            metadata={
                "width": image.width,
                "height": image.height,
                "mode": image.mode,
                "format": image.format,
                "ocr_available": bool(pytesseract is not None),
            },
        )
        ref = SourceRef(
            title=artifact.title,
            uri=resource["uri"],
            snippet=ocr_text[:300] if ocr_text else None,
            kind="image",
            metadata=artifact.metadata,
        )
        return CapabilityExecutionResult(
            capability="image.analyze",
            status="success",
            summary=summary,
            output={**artifact.metadata, "ocr_text": ocr_text},
            source_refs=[ref],
            artifacts=[artifact],
        )

    async def _fetch_search_results(self, query: str) -> list[dict[str, str]]:
        async with httpx.AsyncClient(timeout=self._settings.request_timeout_seconds, follow_redirects=True) as client:
            response = await client.post(
                "https://html.duckduckgo.com/html/",
                data={"q": query},
                headers={"User-Agent": "Mozilla/5.0 CapabilityRouter/1.0"},
            )
            response.raise_for_status()
        soup = BeautifulSoup(response.text, "html.parser")
        return self._parse_search_html(soup)[:5]

    def _parse_search_html(self, soup: BeautifulSoup) -> list[dict[str, str]]:
        results: list[dict[str, str]] = []
        seen: set[str] = set()
        for anchor in soup.find_all("a", href=True):
            title = " ".join(anchor.get_text(" ", strip=True).split())
            if not title:
                continue
            url = self._extract_search_result_url(anchor["href"])
            if not url or url in seen:
                continue
            parsed = urlparse(url)
            if parsed.scheme not in {"http", "https"}:
                continue
            snippet = ""
            parent = anchor.find_parent()
            if parent is not None:
                parent_text = " ".join(parent.get_text(" ", strip=True).split())
                snippet = parent_text.replace(title, "", 1).strip()[:240]
            seen.add(url)
            results.append({"title": title[:240], "url": url, "snippet": snippet})
        return results

    def _extract_search_result_url(self, href: str) -> str | None:
        if href.startswith("//"):
            href = f"https:{href}"
        parsed = urlparse(href)
        if parsed.netloc.endswith("duckduckgo.com") and parsed.path.startswith("/l/"):
            target = parse_qs(parsed.query).get("uddg", [None])[0]
            return unquote(target) if target else None
        if parsed.scheme in {"http", "https"}:
            return href
        return None

    async def _load_resource(self, location: str, *, max_bytes: int) -> dict[str, Any]:
        parsed = urlparse(location)
        scheme = parsed.scheme.lower()
        if scheme in {"http", "https"}:
            return await self._load_remote_resource(location, max_bytes=max_bytes)
        if scheme == "file":
            return self._load_local_resource(unquote(parsed.path), max_bytes=max_bytes)
        if scheme == "":
            return self._load_local_resource(location, max_bytes=max_bytes)
        raise CapabilityBlockedError(f"URL scheme '{scheme}' is not allowed")

    async def _load_remote_resource(self, url: str, *, max_bytes: int) -> dict[str, Any]:
        parsed = urlparse(url)
        if parsed.scheme not in self._settings.allowed_url_scheme_list:
            raise CapabilityBlockedError(f"URL scheme '{parsed.scheme}' is not allowed")
        async with httpx.AsyncClient(timeout=self._settings.request_timeout_seconds, follow_redirects=True) as client:
            response = await client.get(
                url,
                headers={"User-Agent": "Mozilla/5.0 CapabilityRouter/1.0"},
            )
            response.raise_for_status()
        content = response.content[:max_bytes]
        return {
            "uri": str(response.url),
            "bytes": content,
            "media_type": response.headers.get("content-type", "").split(";")[0] or mimetypes.guess_type(str(response.url))[0] or "application/octet-stream",
        }

    def _load_local_resource(self, location: str, *, max_bytes: int) -> dict[str, Any]:
        path = Path(location).expanduser().resolve()
        allowed = [Path(root).expanduser().resolve() for root in self._settings.allowed_local_root_list]
        if not any(root == path or root in path.parents for root in allowed):
            raise CapabilityBlockedError(f"Local path '{path}' is outside allowed roots")
        if not path.exists() or not path.is_file():
            raise CapabilityError(f"Local file not found: {path}")
        data = path.read_bytes()[:max_bytes]
        return {
            "uri": path.as_uri(),
            "bytes": data,
            "media_type": mimetypes.guess_type(str(path))[0] or "application/octet-stream",
        }

    def _clean_html(self, content: bytes, media_type: str) -> tuple[str, str | None]:
        text = content.decode("utf-8", errors="replace")
        if "html" not in media_type and not text.lstrip().startswith("<"):
            cleaned = self._collapse_whitespace(text)
            return cleaned, None
        soup = BeautifulSoup(text, "html.parser")
        title = soup.title.get_text(strip=True) if soup.title else None
        for tag in soup(["script", "style", "noscript"]):
            tag.decompose()
        body_text = soup.get_text("\n", strip=True)
        return self._collapse_whitespace(body_text), title

    def _extract_document(
        self,
        content: bytes,
        uri: str,
        media_type: str,
    ) -> tuple[str, list[str]]:
        suffix = Path(urlparse(uri).path).suffix.lower()
        if suffix == ".pdf" or media_type == "application/pdf":
            return self._read_pdf(content)
        if suffix == ".docx" or media_type in {
            "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
            "application/msword",
        }:
            return self._read_docx(content)
        text = content.decode("utf-8", errors="replace")
        sections = self._extract_text_sections(text)
        return self._collapse_whitespace(text), sections

    def _read_pdf(self, content: bytes) -> tuple[str, list[str]]:
        reader = PdfReader(io.BytesIO(content))
        pages = [page.extract_text() or "" for page in reader.pages]
        text = "\n\n".join(page.strip() for page in pages if page.strip())
        return self._collapse_whitespace(text), [f"page_{idx}" for idx, page in enumerate(pages, start=1) if page.strip()]

    def _read_docx(self, content: bytes) -> tuple[str, list[str]]:
        doc = DocxDocument(io.BytesIO(content))
        paragraphs = [p.text.strip() for p in doc.paragraphs if p.text.strip()]
        sections = [p for p in paragraphs if len(p) < 120][:20]
        return self._collapse_whitespace("\n".join(paragraphs)), sections

    def _extract_text_sections(self, text: str) -> list[str]:
        sections: list[str] = []
        for line in text.splitlines():
            normalized = line.strip()
            if not normalized:
                continue
            if normalized.startswith("#"):
                sections.append(normalized.lstrip("# ").strip())
            elif re.match(r"^[A-Z0-9][A-Z0-9 _-]{3,80}$", normalized):
                sections.append(normalized)
            if len(sections) >= 20:
                break
        return sections

    def _collapse_whitespace(self, value: str) -> str:
        return re.sub(r"\n{3,}", "\n\n", re.sub(r"[ \t]+", " ", value)).strip()
