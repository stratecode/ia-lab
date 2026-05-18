from __future__ import annotations

from pathlib import Path
from urllib.parse import urlparse

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from cap_sidecars.common import SidecarBlockedError, SidecarError, analyze_image_bytes, load_resource

app = FastAPI(title="cap-images", version="0.1.0")


class ImageAnalyzeRequest(BaseModel):
    location: str = Field(..., min_length=1, max_length=4096)
    max_bytes: int = Field(default=10_000_000, gt=0)
    max_chars: int = Field(default=16_000, gt=0)
    allowed_url_schemes: list[str] = Field(default_factory=lambda: ["http", "https", "file"])
    allowed_local_roots: list[str] = Field(default_factory=lambda: ["/srv/ai-lab", "/tmp"])


@app.post("/image/analyze")
async def image_analyze(body: ImageAnalyzeRequest) -> dict:
    try:
        resource = await load_resource(
            body.location,
            max_bytes=body.max_bytes,
            allowed_url_schemes=body.allowed_url_schemes,
            allowed_local_roots=body.allowed_local_roots,
        )
        metadata, ocr_text = analyze_image_bytes(resource["bytes"])
        title = Path(urlparse(resource["uri"]).path).name or resource["uri"]
        summary_lines = [
            f"Image: {metadata.get('format') or 'unknown'}",
            f"Size: {metadata.get('width')}x{metadata.get('height')}",
            f"Mode: {metadata.get('mode')}",
        ]
        if ocr_text:
            summary_lines.append(f"OCR: {ocr_text[:600]}")
        else:
            summary_lines.append("OCR: not available or no text detected")
        return {
            "status": "success",
            "summary": "\n".join(summary_lines),
            "output": {"ocr_text": ocr_text[: body.max_chars], "metadata": metadata},
            "source_refs": [
                {
                    "title": title,
                    "uri": resource["uri"],
                    "kind": "image",
                    "snippet": ocr_text[:300] if ocr_text else None,
                    "metadata": metadata,
                }
            ],
            "artifacts": [
                {
                    "artifact_type": "image_analysis",
                    "title": title,
                    "uri": resource["uri"],
                    "media_type": resource["media_type"],
                    "content_text": ocr_text[: body.max_chars] if ocr_text else None,
                    "metadata": metadata,
                }
            ],
        }
    except SidecarBlockedError as exc:
        raise HTTPException(status_code=403, detail=str(exc)) from exc
    except SidecarError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:  # pragma: no cover
        raise HTTPException(status_code=500, detail=str(exc)) from exc
