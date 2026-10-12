#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

bash -n \
  "${ROOT_DIR}/scripts/codex-mode.sh" \
  "${ROOT_DIR}/scripts/codex-lab-exec" \
  "${ROOT_DIR}/scripts/test-codex-mode-guided-helpers.sh"

bash "${ROOT_DIR}/scripts/test-codex-mode-guided-helpers.sh"
python3 "${ROOT_DIR}/scripts/test-secretary-oauth.py"
python3 -m py_compile "${ROOT_DIR}/scripts/connect-secretary.py" "${ROOT_DIR}/scripts/smoke-secretary.py"

docker run --rm \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  golang:1.23 \
  /bin/bash -lc 'export PATH=/usr/local/go/bin:$PATH; export GOTELEMETRY=off; go mod download && go test ./internal/secretary ./cmd/lab-secretary ./internal/llmtelemetry ./cmd/llm-telemetry ./internal/codexlocalgateway ./cmd/codex-local-gateway ./internal/orchestratorgo/... ./cmd/orchestrator-go ./cmd/lab-agent ./cmd/lab-agentd'
