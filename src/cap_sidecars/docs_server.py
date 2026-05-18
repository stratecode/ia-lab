from __future__ import annotations

from pathlib import Path
from urllib.parse import urlparse

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field

from cap_sidecars.common import SidecarBlockedError, SidecarError, extract_document, load_resource

app = FastAPI(title="cap-docs", version="0.1.0")


class DocumentReadRequest(BaseModel):
    location: str = Field(..., min_length=1, max_length=4096)
    max_bytes: int = Field(default=20_000_000, gt=0)
    max_chars: int = Field(default=16_000, gt=0)
    allowed_url_schemes: list[str] = Field(default_factory=lambda: ["http", "https", "file"])
    allowed_local_roots: list[str] = Field(default_factory=lambda: ["/srv/ai-lab", "/tmp"])


@app.post("/document/read")
async def document_read(body: DocumentReadRequest) -> dict:
    try:
        resource = await load_resource(
            body.location,
            max_bytes=body.max_bytes,
            allowed_url_schemes=body.allowed_url_schemes,
            allowed_local_roots=body.allowed_local_roots,
        )
        text, sections = extract_document(resource["bytes"], resource["uri"], resource["media_type"])
        text = text[: body.max_chars]
        title = Path(urlparse(resource["uri"]).path).name or resource["uri"]
        return {
            "status": "success",
            "summary": f"{title}\n\n{text[:1200].strip()}".strip(),
            "output": {"content_text": text, "sections": sections},
            "source_refs": [
                {
                    "title": title,
                    "uri": resource["uri"],
                    "kind": "document",
                    "snippet": text[:300],
                    "metadata": {"sections": sections[:10]},
                }
            ],
            "artifacts": [
                {
                    "artifact_type": "document_text",
                    "title": title,
                    "uri": resource["uri"],
                    "media_type": resource["media_type"],
                    "content_text": text,
                    "metadata": {"sections": sections[:20]},
                }
            ],
        }
    except SidecarBlockedError as exc:
        raise HTTPException(status_code=403, detail=str(exc)) from exc
    except SidecarError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except Exception as exc:  # pragma: no cover
        raise HTTPException(status_code=500, detail=str(exc)) from exc

