#!/usr/bin/env python3
"""Exercise local Piper -> FFmpeg -> Whisper without retaining speech data."""

from __future__ import annotations

import ipaddress
import json
import os
import re
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request


PHRASE = "Esta es una prueba de voz del laboratorio. Buenos días."
REQUIRED_WORDS = ("buenos", "prueba", "voz", "laboratorio")
WHISPER_VERSION = "whisper.cpp v1.9.5"
PIPER_VERSION = "piper-tts 1.8.0"


def loopback_base(env_name: str, default: str) -> str:
    value = os.environ.get(env_name, default).rstrip("/")
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme != "http" or not parsed.hostname or parsed.username or parsed.password:
        raise ValueError(f"{env_name} must be a plain HTTP loopback URL")
    try:
        loopback = ipaddress.ip_address(parsed.hostname).is_loopback
    except ValueError:
        loopback = parsed.hostname.lower() == "localhost"
    if not loopback:
        raise ValueError(f"{env_name} must point to loopback")
    return value


def post(url: str, payload: bytes, content_type: str, timeout: int = 180) -> bytes:
    request = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": content_type},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            if response.status != 200:
                raise RuntimeError("local speech service returned a non-200 status")
            return response.read(50_000_001)
    except (urllib.error.URLError, TimeoutError) as exc:
        raise RuntimeError("local speech service request failed") from exc


def multipart_wav(wav: bytes) -> tuple[bytes, str]:
    boundary = "lab-voice-smoke-boundary"
    fields = (
        ("response_format", "json"),
        ("temperature", "0"),
        ("language", "es"),
    )
    chunks = []
    for name, value in fields:
        chunks.append(
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"\r\n\r\n{value}\r\n".encode()
        )
    chunks.append(
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"voice.wav\"\r\nContent-Type: audio/wav\r\n\r\n".encode()
    )
    chunks.extend((wav, f"\r\n--{boundary}--\r\n".encode()))
    return b"".join(chunks), f"multipart/form-data; boundary={boundary}"


def main() -> int:
    piper = loopback_base("LAB_PIPER_URL", "http://127.0.0.1:8084")
    whisper = loopback_base("LAB_WHISPER_URL", "http://127.0.0.1:8083")

    total_started = time.perf_counter()
    started = total_started
    wav = post(
        f"{piper}/synthesize",
        json.dumps({"text": PHRASE}, ensure_ascii=False).encode(),
        "application/json",
    )
    piper_seconds = time.perf_counter() - started
    if len(wav) > 50_000_000 or not (wav[:4] == b"RIFF" and wav[8:12] == b"WAVE"):
        raise RuntimeError("Piper response is not a bounded WAV file")

    started = time.perf_counter()
    converted = subprocess.run(
        ["/usr/bin/ffmpeg", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-vn", "-c:a", "libopus", "-b:a", "32k", "-f", "ogg", "pipe:1"],
        input=wav,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
        timeout=90,
    )
    ffmpeg_seconds = time.perf_counter() - started
    ogg = converted.stdout
    if converted.returncode or not ogg.startswith(b"OggS") or len(ogg) > 50_000_000:
        raise RuntimeError("FFmpeg did not produce bounded OGG/Opus audio")

    body, content_type = multipart_wav(wav)
    started = time.perf_counter()
    response = post(f"{whisper}/inference", body, content_type)
    whisper_seconds = time.perf_counter() - started
    if len(response) > 1_048_576:
        raise RuntimeError("Whisper response exceeded the expected size")
    result = json.loads(response)
    transcript = result.get("text", "")
    if not isinstance(transcript, str):
        raise RuntimeError("Whisper response did not contain text")
    words = set(re.findall(r"[a-záéíóúüñ]+", transcript.lower()))
    missing = [word for word in REQUIRED_WORDS if word not in words]
    if missing:
        raise RuntimeError(
            "Spanish speech smoke test missed expected markers: " + ",".join(missing)
        )

    print(f"Whisper: {WHISPER_VERSION}")
    print(f"Piper: {PIPER_VERSION}")
    print("Result: PASS (speech content withheld)")
    print(f"Piper synthesis: {piper_seconds:.3f}s")
    print(f"FFmpeg OGG/Opus: {ffmpeg_seconds:.3f}s")
    print(f"Whisper transcription: {whisper_seconds:.3f}s")
    print(f"Total: {time.perf_counter() - total_started:.3f}s")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, RuntimeError, json.JSONDecodeError, subprocess.TimeoutExpired) as exc:
        print(f"Result: FAIL ({exc})")
        raise SystemExit(1)
