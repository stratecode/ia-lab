#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

docker run --rm \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go mod download && go test ./internal/orchestratorgo/... ./cmd/orchestrator-go && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/orchestrator-go-linux-amd64 ./cmd/orchestrator-go'
