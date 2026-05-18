#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

docker run --rm \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go mod download && go test ./internal/orchestratorgo/... ./cmd/orchestrator-go ./cmd/lab-agent ./cmd/lab-agentd'
