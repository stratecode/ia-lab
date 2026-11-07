#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$repo_root/dist"
docker run --rm \
  -v "$repo_root:/workspace" \
  -w /workspace \
  -e GOOS=linux -e GOARCH=amd64 -e CGO_ENABLED=0 \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go build -trimpath -o dist/donna-identity-linux-amd64 ./cmd/donna-identity'
