#!/usr/bin/env bash
# Run on the lab host as root. Serves each candidate in a side llama-server
# (port 8190, never the production router) and runs the Donna benchmark on it.
set -euo pipefail

BASE=${BASE:-/srv/ai-lab/donna-eval}
OUT=${OUT:-$BASE/results}
MODELS=/srv/ai-lab/llama.cpp/models
SERVER=/opt/llama.cpp/build/bin/llama-server
PORT=8190
CTX=${CTX:-36864}
CONTEXT_SIZES=${CONTEXT_SIZES:-4096,8192,16384,32768}
DEPTHS=${DEPTHS:-0.2,0.8}
HF=https://huggingface.co
THINK_OFF='{"enable_thinking":false}'

# alias | file | download path (empty when already on disk) | chat template kwargs
CANDIDATES=${CANDIDATES:-"
main|LFM2.5-1.2B-Instruct-Q4_0.gguf||
qwen3-4b-2507|Qwen3-4B-Instruct-2507-Q4_K_M.gguf|unsloth/Qwen3-4B-Instruct-2507-GGUF/resolve/main/Qwen3-4B-Instruct-2507-Q4_K_M.gguf|
qwen35-4b|Qwen3.5-4B-Q4_K_M.gguf|unsloth/Qwen3.5-4B-GGUF/resolve/e87f176479d0855a907a41277aca2f8ee7a09523/Qwen3.5-4B-Q4_K_M.gguf|$THINK_OFF
gemma4-e4b|gemma-4-E4B-it-Q4_K_M.gguf|unsloth/gemma-4-E4B-it-GGUF/resolve/bfc15c382204943c3a8fff0c750b94ae2364d7a3/gemma-4-E4B-it-Q4_K_M.gguf|
qwen-eval|Qwen3-4B-Q4_K_M.gguf||$THINK_OFF
"}

mkdir -p "$OUT"
log() { echo "$(date -Is) $*" | tee -a "$OUT/run.log"; }

while IFS='|' read -r alias file path kwargs; do
  [ -z "$alias" ] && continue
  log "== $alias"
  if [ ! -f "$MODELS/$file" ]; then
    log "downloading $file"
    curl -fL --retry 3 -C - -o "$MODELS/$file.part" "$HF/$path"
    mv "$MODELS/$file.part" "$MODELS/$file"
    chown llama-cpp:llama-cpp "$MODELS/$file"
  fi
  sha256sum "$MODELS/$file" | tee -a "$OUT/sha256.txt"

  systemctl stop donna-eval-server 2>/dev/null || true
  systemd-run --quiet --unit donna-eval-server --uid llama-cpp --gid llama-cpp --collect \
    -p Nice=10 -p MemoryMax=16G -p MemorySwapMax=0 -p Environment=HOME=/tmp \
    "$SERVER" --host 127.0.0.1 --port $PORT -m "$MODELS/$file" --alias "$alias" -c "$CTX" \
    --device none -ngl 0 --no-kv-offload --parallel 1 --jinja --threads 8 --threads-batch 8 \
    --no-webui --cache-ram 0
  for _ in $(seq 1 120); do
    curl -fs "http://127.0.0.1:$PORT/health" >/dev/null 2>&1 && break
    sleep 3
  done
  curl -fs "http://127.0.0.1:$PORT/health" >/dev/null || { log "server for $alias did not start"; journalctl -u donna-eval-server -n 20 --no-pager | tee -a "$OUT/run.log"; continue; }

  args=(--base "http://127.0.0.1:$PORT" --alias "$alias" --instructions "$BASE/repo/skills/secretary-core/instructions.md" --out "$OUT/$alias.jsonl" --context-sizes "$CONTEXT_SIZES" --depths "$DEPTHS")
  [ -n "$kwargs" ] && args+=(--template-kwargs "$kwargs")
  nice -n 10 python3 "$BASE/repo/scripts/benchmark-donna-models.py" "${args[@]}" 2>&1 | tee -a "$OUT/run.log" || log "benchmark for $alias failed"
  {
    echo "alias=$alias memory_peak_bytes=$(systemctl show donna-eval-server -p MemoryPeak --value)"
  } | tee -a "$OUT/memory.txt"
  systemctl stop donna-eval-server 2>/dev/null || true
done <<< "$CANDIDATES"
log "all candidates done"
