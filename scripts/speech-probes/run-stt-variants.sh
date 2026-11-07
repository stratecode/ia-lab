#!/usr/bin/env bash
M=/srv/ai-lab/lab-speech/models
S=/opt/lab-speech/whisper.cpp/build/bin/whisper-server
[ -f $M/ggml-base-q5_1.bin ] || curl -fL -o $M/ggml-base-q5_1.bin https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base-q5_1.bin
run() {  # name, model, extra flags, prompt
  systemctl stop donna-stt-probe 2>/dev/null
  systemd-run --quiet --unit donna-stt-probe --collect -p Nice=10 $S --host 127.0.0.1 --port 8193 --model $M/$2 --threads 4 --no-gpu --tmp-dir /tmp $3
  for _ in $(seq 1 40); do curl -fs http://127.0.0.1:8193/health >/dev/null 2>&1 && break; sleep 1; done
  echo "== $1"; python3 /srv/ai-lab/donna-eval/stt-variants.py "$4"
  systemctl stop donna-stt-probe
}
PROMPT="Conversación en español con Donna, la secretaria. Palabras: Jira, Granola, Donna, PMI, Valentina, Gonzalo, Gustavo, sprint."
run "small ac0 (actual)" ggml-small-q5_1.bin "" ""
run "small ac768 + vocabulario" ggml-small-q5_1.bin "--audio-ctx 768" "$PROMPT"
run "small ac0 + vocabulario" ggml-small-q5_1.bin "" "$PROMPT"
run "base ac0 + vocabulario" ggml-base-q5_1.bin "" "$PROMPT"
run "base ac768 + vocabulario" ggml-base-q5_1.bin "--audio-ctx 768" "$PROMPT"
