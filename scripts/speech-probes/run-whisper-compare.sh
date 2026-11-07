#!/usr/bin/env bash
# Run on the lab host as root. Scores several Whisper models and settings on the
# clips in $CLIPS against the sentences in refs-real-voice.json.
set -uo pipefail
BASE=${BASE:-/srv/ai-lab/donna-eval}
CLIPS=${CLIPS:-$BASE/real}
OUT=${OUT:-$BASE/whisper-compare.jsonl}
M=/srv/ai-lab/lab-speech/models
S=/opt/lab-speech/whisper.cpp/build/bin/whisper-server
REV=5359861c739e955e79d9a303bcbc70fb988958b1
HF=https://huggingface.co/ggerganov/whisper.cpp/resolve/$REV
PROMPT="Conversación en español con Donna, la secretaria personal de Fran. Palabras: Jira, Granola, Donna, PMI, STRAT, sprint, backlog, Valentina, Gonzalo, Gustavo."

fetch() {  # file sha256
  [ -f "$M/$1" ] && [ "$(sha256sum "$M/$1" | cut -d' ' -f1)" = "$2" ] && return 0
  curl -fL --retry 3 -o "$M/$1.part" "$HF/$1" && [ "$(sha256sum "$M/$1.part" | cut -d' ' -f1)" = "$2" ] && mv "$M/$1.part" "$M/$1" \
    || { echo "download or checksum failed for $1"; rm -f "$M/$1.part"; return 1; }
}
fetch ggml-medium-q5_0.bin 19fea4b380c3a618ec4723c3eef2eb785ffba0d0538cf43f8f235e7b3b34220f
fetch ggml-large-v3-turbo-q5_0.bin 394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2
fetch ggml-small.bin 1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b
fetch ggml-base-q5_1.bin 422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898

: > "$OUT"
run() {  # label | model | flags | prompt(1/0)
  local label="$1" model="$2" flags="$3" usePrompt="$4"
  systemctl stop donna-whisper-compare 2>/dev/null
  systemd-run --quiet --unit donna-whisper-compare --collect -p Nice=10 $S --host 127.0.0.1 --port 8193 --model "$M/$model" --threads 4 --no-gpu --tmp-dir /tmp $flags
  for _ in $(seq 1 90); do curl -fs http://127.0.0.1:8193/health >/dev/null 2>&1 && break; sleep 1; done
  local args=(--label "$label" --clips "$CLIPS" --refs "$BASE/refs-real-voice.json")
  [ "$usePrompt" = 1 ] && args+=(--prompt "$PROMPT")
  python3 "$BASE/compare-whisper.py" "${args[@]}" >> "$OUT" \
    || echo "{\"label\": \"$label\", \"error\": true}" >> "$OUT"
  systemctl stop donna-whisper-compare
  echo "$(date +%T) done: $label"
}
run "small q5_1 · ventana completa · vocabulario [Telegram hoy]" ggml-small-q5_1.bin "" 1
run "small q5_1 · ventana 768 · vocabulario [llamada hoy]" ggml-small-q5_1.bin "--audio-ctx 768" 1
run "small q5_1 · ventana completa · SIN vocabulario" ggml-small-q5_1.bin "" 0
run "small q5_1 · ventana completa · vocabulario · haz 5" ggml-small-q5_1.bin "--beam-size 5 --best-of 5" 1
run "small q5_1 · ventana 768 · vocabulario · haz 5" ggml-small-q5_1.bin "--audio-ctx 768 --beam-size 5 --best-of 5" 1
run "small sin cuantizar · ventana completa · vocabulario" ggml-small.bin "" 1
run "base q5_1 · ventana completa · vocabulario" ggml-base-q5_1.bin "" 1
run "medium q5_0 · ventana completa · vocabulario" ggml-medium-q5_0.bin "" 1
run "medium q5_0 · ventana 768 · vocabulario" ggml-medium-q5_0.bin "--audio-ctx 768" 1
run "turbo q5_0 · ventana completa · vocabulario" ggml-large-v3-turbo-q5_0.bin "" 1
run "turbo q5_0 · ventana 768 · vocabulario" ggml-large-v3-turbo-q5_0.bin "--audio-ctx 768" 1
echo "all configurations done"
