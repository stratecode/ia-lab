#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${CODEX_BOOTSTRAP_ENV_FILE:-$ROOT_DIR/.env}"
OUTPUT_DIR="${CODEX_BOOTSTRAP_OUTPUT_DIR:-$HOME/.codex-lab}"
CONFIG_FILE="$OUTPUT_DIR/config.toml"
RUNTIME_ENV_FILE="$OUTPUT_DIR/env"
DEFAULT_KEY_FILE="${HOME}/.config/stratecode-lab/codex_gateway_api_key"

usage() {
  cat <<'EOF'
Usage: scripts/bootstrap-codex-home.sh [--env-file PATH] [--output-dir PATH]

Creates a dedicated CODEX_HOME configured to use the lab Codex gateway.

Environment override precedence:
1. Existing shell environment
2. Values loaded from --env-file / .env

Recognized inputs:
- CODEX_GATEWAY_BASE_URL
- CODEX_GATEWAY_API_KEY
- CODEX_GATEWAY_MODEL
- LAB_CODEX_GATEWAY_DOMAIN
- LAB_CODEX_GATEWAY_API_KEY
- ~/.config/stratecode-lab/codex_gateway_api_key
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file)
      ENV_FILE="$2"
      shift 2
      ;;
    --output-dir)
      OUTPUT_DIR="$2"
      CONFIG_FILE="$OUTPUT_DIR/config.toml"
      RUNTIME_ENV_FILE="$OUTPUT_DIR/env"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

gateway_base_url="${CODEX_GATEWAY_BASE_URL:-}"
gateway_api_key="${CODEX_GATEWAY_API_KEY:-${LAB_CODEX_GATEWAY_API_KEY:-}}"
gateway_model="${CODEX_GATEWAY_MODEL:-${LAB_CODEX_GATEWAY_MODEL:-qwen-local-code}}"
gateway_domain="${LAB_CODEX_GATEWAY_DOMAIN:-}"

if [[ -z "$gateway_api_key" && -f "$DEFAULT_KEY_FILE" ]]; then
  gateway_api_key="$(tr -d '[:space:]' < "$DEFAULT_KEY_FILE")"
fi

if [[ -z "$gateway_base_url" ]]; then
  if [[ -n "$gateway_domain" && "$gateway_domain" != "codex.example.com" ]]; then
    gateway_base_url="https://${gateway_domain}/v1"
  else
    gateway_base_url="http://127.0.0.1:8180/v1"
  fi
fi

if [[ -z "$gateway_api_key" || "$gateway_api_key" == "change-me-codex-gateway-key" ]]; then
  printf 'Missing Codex gateway API key. Set CODEX_GATEWAY_API_KEY, LAB_CODEX_GATEWAY_API_KEY, or create %s.\n' "$DEFAULT_KEY_FILE" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"
chmod 700 "$OUTPUT_DIR"

cat >"$CONFIG_FILE" <<EOF
model = "${gateway_model}"
model_provider = "lab-codex-gateway"

[model_providers.lab-codex-gateway]
name = "Lab Codex Gateway"
base_url = "${gateway_base_url}"
env_key = "CODEX_GATEWAY_API_KEY"
wire_api = "responses"
EOF

cat >"$RUNTIME_ENV_FILE" <<EOF
CODEX_GATEWAY_API_KEY=${gateway_api_key}
EOF

chmod 600 "$CONFIG_FILE" "$RUNTIME_ENV_FILE"

cat <<EOF
Created:
- $CONFIG_FILE
- $RUNTIME_ENV_FILE

Next:
  export CODEX_HOME="$OUTPUT_DIR"
  set -a && source "$RUNTIME_ENV_FILE" && set +a
  codex
EOF
