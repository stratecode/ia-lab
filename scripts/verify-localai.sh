#!/usr/bin/env bash
set -euo pipefail

base_url="${LAB_LOCALAI_BASE_URL:-https://${LAB_LOCALAI_DOMAIN:-localai.stratecode.local}}"
model="${LAB_LOCALAI_DEFAULT_MODEL:-qwen3-4b}"
api_key="${LAB_LOCALAI_API_KEY:-}"
curl_network_args=()
include_tool_call=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-url)
      [[ $# -ge 2 ]] || {
        echo "missing value for --base-url" >&2
        exit 2
      }
      base_url="${2%/}"
      shift 2
      ;;
    --resolve)
      [[ $# -ge 2 ]] || {
        echo "missing value for --resolve" >&2
        exit 2
      }
      curl_network_args+=(--resolve "$2")
      shift 2
      ;;
    --include-tool-call)
      include_tool_call=true
      shift
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

[[ -n "${api_key}" ]] || {
  echo "LAB_LOCALAI_API_KEY is required" >&2
  exit 2
}

command -v curl >/dev/null
command -v jq >/dev/null

curl_tls_args=()
if [[ "${base_url}" == https://* ]]; then
  if [[ -n "${LAB_LOCALAI_CA_CERT:-}" ]]; then
    curl_tls_args=(--cacert "${LAB_LOCALAI_CA_CERT}")
  else
    curl_tls_args=(--insecure)
  fi
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

unauthenticated_status="$(
  curl --silent --show-error \
    "${curl_network_args[@]}" \
    "${curl_tls_args[@]}" \
    --output "${tmp_dir}/unauthenticated.json" \
    --write-out '%{http_code}' \
    "${base_url}/v1/models"
)"
case "${unauthenticated_status}" in
  401|403) echo "authentication: ok" ;;
  *)
    echo "authentication: expected 401 or 403, got ${unauthenticated_status}" >&2
    exit 1
    ;;
esac

curl --silent --show-error --fail-with-body \
  "${curl_network_args[@]}" \
  "${curl_tls_args[@]}" \
  --header "Authorization: Bearer ${api_key}" \
  "${base_url}/readyz" \
  >"${tmp_dir}/ready.json"
echo "readiness: ok"

curl --silent --show-error --fail-with-body \
  "${curl_network_args[@]}" \
  "${curl_tls_args[@]}" \
  --header "Authorization: Bearer ${api_key}" \
  "${base_url}/v1/models" \
  >"${tmp_dir}/models.json"
jq -e --arg model "${model}" \
  '.data | any(.id == $model)' \
  "${tmp_dir}/models.json" >/dev/null
echo "model: ok"

jq -n --arg model "${model}" '{
  model: $model,
  messages: [{
    role: "user",
    content: "/no_think Reply with exactly fixture-ok."
  }],
  max_tokens: 128,
  temperature: 0
}' >"${tmp_dir}/chat-request.json"
curl --silent --show-error --fail-with-body \
  "${curl_network_args[@]}" \
  "${curl_tls_args[@]}" \
  --header "Authorization: Bearer ${api_key}" \
  --header "Content-Type: application/json" \
  --data-binary "@${tmp_dir}/chat-request.json" \
  "${base_url}/v1/chat/completions" \
  >"${tmp_dir}/chat-response.json"
jq -e '.choices[0].message.content | type == "string" and length > 0' \
  "${tmp_dir}/chat-response.json" >/dev/null
echo "chat: ok"

if [[ "${include_tool_call}" != true ]]; then
  echo "tool-call: skipped"
  exit 0
fi

jq -n --arg model "${model}" '{
  model: $model,
  messages: [{
    role: "user",
    content: "/no_think Use get_weather for Madrid. Do not answer without the tool."
  }],
  tools: [{
    type: "function",
    function: {
      name: "get_weather",
      description: "Get weather for a city",
      parameters: {
        type: "object",
        properties: {location: {type: "string"}},
        required: ["location"]
      }
    }
  }],
  tool_choice: "required",
  max_tokens: 128,
  temperature: 0
}' >"${tmp_dir}/tool-request.json"
curl --silent --show-error --fail-with-body \
  "${curl_network_args[@]}" \
  "${curl_tls_args[@]}" \
  --header "Authorization: Bearer ${api_key}" \
  --header "Content-Type: application/json" \
  --data-binary "@${tmp_dir}/tool-request.json" \
  "${base_url}/v1/chat/completions" \
  >"${tmp_dir}/tool-response.json"
jq -e '
  .choices[0].message.tool_calls
  | type == "array"
    and length > 0
    and .[0].function.name == "get_weather"
' "${tmp_dir}/tool-response.json" >/dev/null
echo "tool-call: ok"
