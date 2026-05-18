from __future__ import annotations

import io
import mimetypes
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

import httpx
from docx import Document as DocxDocument
from PIL import Image
from pypdf import PdfReader

try:
    import pytesseract
except Exception:  # pragma: no cover
    pytesseract = None


class SidecarError(Exception):
    pass


class SidecarBlockedError(SidecarError):
    pass


def normalize_uri(location: str) -> str:
    parsed = urlparse(location)
    if parsed.scheme in {"http", "https", "file"}:
        return location
    path = Path(location).expanduser().resolve()
    return path.as_uri()


def enforce_allowed(uri: str, allowed_url_schemes: list[str], allowed_local_roots: list[str]) -> None:
    parsed = urlparse(uri)
    scheme = (parsed.scheme or "file").lower()
    if scheme not in {item.lower() for item in allowed_url_schemes}:
        raise SidecarBlockedError(f"URI scheme '{scheme}' is not allowed")
    if scheme == "file":
        path = Path(parsed.path).resolve()
        roots = [Path(root).expanduser().resolve() for root in allowed_local_roots]
        if roots and not any(path == root or root in path.parents for root in roots):
            raise SidecarBlockedError(f"Path '{path}' is outside allowed_local_roots")


async def load_resource(
    location: str,
    *,
    max_bytes: int,
    allowed_url_schemes: list[str],
    allowed_local_roots: list[str],
    timeout_seconds: float = 20.0,
) -> dict[str, Any]:
    uri = normalize_uri(location)
    enforce_allowed(uri, allowed_url_schemes, allowed_local_roots)
    parsed = urlparse(uri)
    if parsed.scheme in {"http", "https"}:
        async with httpx.AsyncClient(timeout=timeout_seconds, follow_redirects=True) as client:
            response = await client.get(uri)
            response.raise_for_status()
            body = response.content[:max_bytes]
            media_type = response.headers.get("content-type", "").split(";", 1)[0].strip()
            return {"uri": uri, "media_type": media_type, "bytes": body}

    path = Path(parsed.path).resolve()
    if not path.exists():
        raise SidecarError(f"Resource not found: {path}")
    body = path.read_bytes()[:max_bytes]
    media_type = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    return {"uri": uri, "media_type": media_type, "bytes": body}


def extract_document(body: bytes, uri: str, media_type: str) -> tuple[str, list[str]]:
    path = Path(urlparse(uri).path)
    suffix = path.suffix.lower()
    if suffix == ".pdf" or media_type == "application/pdf":
        reader = PdfReader(io.BytesIO(body))
        pages = [page.extract_text() or "" for page in reader.pages]
        sections = [f"Page {idx + 1}" for idx in range(len(pages))]
        return "\n\n".join(page.strip() for page in pages if page.strip()), sections
    if suffix == ".docx" or media_type == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
        document = DocxDocument(io.BytesIO(body))
        paragraphs = [paragraph.text.strip() for paragraph in document.paragraphs if paragraph.text.strip()]
        sections = paragraphs[:10]
        return "\n".join(paragraphs), sections
    text = body.decode("utf-8", errors="replace")
    sections = []
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            sections.append(stripped.lstrip("# ").strip())
        elif len(sections) < 10 and len(stripped) < 120:
            sections.append(stripped)
        if len(sections) >= 10:
            break
    return text, sections


def analyze_image_bytes(body: bytes) -> tuple[dict[str, Any], str]:
    image = Image.open(io.BytesIO(body))
    ocr_text = ""
    if pytesseract is not None:
        try:
            ocr_text = (pytesseract.image_to_string(image) or "").strip()
        except Exception:
            ocr_text = ""
    metadata = {
        "width": image.width,
        "height": image.height,
        "mode": image.mode,
        "format": image.format,
        "ocr_available": bool(pytesseract is not None),
    }
    return metadata, ocr_text

