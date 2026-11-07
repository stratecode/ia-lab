#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
mkdir -p "${DIST_DIR}"

docker run --rm \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go test ./internal/donnagranola ./cmd/donna-granola-events && go test ./internal/secretary ./cmd/lab-secretary -run "TestGranola|TestOpenWebUIReceivesOnlyItsPendingGranolaNotification" -count=1 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/donna-granola-events-linux-amd64 ./cmd/donna-granola-events && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/lab-secretary-linux-amd64 ./cmd/lab-secretary'
