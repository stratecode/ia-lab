#!/usr/bin/env python3
"""Scan staged Git blobs using the central lab pre-receive patterns.

Diagnostics intentionally contain only the path and finding class, never matched
content. This is a convenience guard; the central hook remains authoritative.
"""

from __future__ import annotations

import re
import subprocess
import sys

MAX_BYTES = 1_048_576
PATTERNS = (
    ("private-key-marker", re.compile(rb"-----BEGIN ([A-Z]+ )?PRIVATE KEY-----")),
    ("telegram-bot-token", re.compile(rb"(^|[^0-9])[0-9]{8,10}:[A-Za-z0-9_-]{35}($|[^A-Za-z0-9_-])")),
    ("api-key", re.compile(rb"AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{40,}|sk-[A-Za-z0-9]{32,}")),
    ("jwt", re.compile(rb"eyJ[A-Za-z0-9_-]{15,}\.eyJ[A-Za-z0-9_-]{15,}\.")),
)


def git(*args: str) -> bytes:
    return subprocess.check_output(["git", *args])


def staged_paths() -> list[bytes]:
    raw = git("diff", "--cached", "--diff-filter=AMR", "--name-status", "-z", "-M")
    fields = [field for field in raw.split(b"\0") if field]
    paths: list[bytes] = []
    index = 0
    while index < len(fields):
        status = fields[index]
        index += 1
        if status.startswith((b"R", b"C")):
            # Scan the destination of a rename/copy; its source was already in Git.
            if index + 1 >= len(fields):
                break
            paths.append(fields[index + 1])
            index += 2
        else:
            if index >= len(fields):
                break
            paths.append(fields[index])
            index += 1
    return paths


def blob_for(path: bytes) -> bytes:
    return subprocess.check_output(["git", "show", b":" + path])


def check_path(path: bytes) -> list[str]:
    name = path.decode("utf-8", "replace")
    findings: list[str] = []
    base = name.rsplit("/", 1)[-1]
    if base in {".env", ".env.local"}:
        findings.append("environment-file")
    if name == "ssh/lab" or base in {"id_rsa", "id_ed25519"} or base.endswith((".kdbx", ".p12", ".pfx")):
        findings.append("credential-file")

    data = blob_for(path)
    if len(data) > MAX_BYTES:
        return findings
    for kind, pattern in PATTERNS:
        if pattern.search(data):
            findings.append(kind)
    if base == "vault.yml":
        first_line = data.splitlines()[0] if data.splitlines() else b""
        if not first_line.startswith(b"$ANSIBLE_VAULT"):
            findings.append("unencrypted-vault")
    return findings


def main() -> int:
    try:
        paths = staged_paths()
        failures = [(path.decode("utf-8", "replace"), kind)
                    for path in paths for kind in check_path(path)]
    except subprocess.CalledProcessError as exc:
        print(f"secret scan failed: git exited {exc.returncode}", file=sys.stderr)
        return 2
    except OSError as exc:
        print(f"secret scan failed: {exc.__class__.__name__}", file=sys.stderr)
        return 2

    if failures:
        for path, kind in failures:
            print(f"REJECTED: {path}: {kind}", file=sys.stderr)
        print("Commit blocked by staged secret scan; review locally and remove sensitive data.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
