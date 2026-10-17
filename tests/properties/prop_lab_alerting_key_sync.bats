#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
  test_tmp="$BATS_TEST_TMPDIR/key-sync"
  mkdir -p "$test_tmp/bin"
  script="$repo_root/roles/lab_alerting/files/bin/sync-llama-prom-key"
  export LLAMA_PROM_CREDENTIALS_FILE="$test_tmp/prometheus-key"
  export LLAMA_PROM_METRIC_FILE="$test_tmp/metrics/lab-llama-prom-key.prom"
  export LLAMA_PROM_SYSTEMD_SERVICE="test-llama.service"
  export PATH="$test_tmp/bin:$PATH"
}

@test "inline API key is written atomically with restricted mode and no output" {
  key='never-print-this-llama-key'
  run "$script" --api-key "$key"
  [ "$status" -eq 0 ]
  [[ "$output" != *"$key"* ]]
  [ "$(cat "$LLAMA_PROM_CREDENTIALS_FILE")" = "$key" ]
  grep -q '^lab_llama_prom_key_in_sync 1$' "$LLAMA_PROM_METRIC_FILE"
  run python3 - "$LLAMA_PROM_CREDENTIALS_FILE" <<'PY'
import os, stat, sys
mode = stat.S_IMODE(os.stat(sys.argv[1]).st_mode)
if mode != 0o640:
    raise SystemExit(f"expected mode 0640, got {mode:o}")
PY
  [ "$status" -eq 0 ]
}

@test "API key file input is supported without printing its contents" {
  key='file-input-secret-that-must-not-appear'
  printf '%s\n' "$key" > "$test_tmp/source-key"
  run "$script" --api-key-file "$test_tmp/source-key"
  [ "$status" -eq 0 ]
  [[ "$output" != *"$key"* ]]
  [ "$(cat "$LLAMA_PROM_CREDENTIALS_FILE")" = "$key" ]
  grep -q '^lab_llama_prom_key_in_sync 1$' "$LLAMA_PROM_METRIC_FILE"
}

@test "systemd key extraction keeps the secret out of stdout and stderr" {
  key='unit-secret-that-must-not-appear'
  cat > "$test_tmp/bin/systemctl" <<'SH'
#!/bin/sh
if [ "$1" = cat ]; then
  printf '[Service]\nExecStart=/usr/bin/llama-server --api-key %s --metrics\n' "$TEST_SYSTEMD_KEY"
fi
SH
  chmod +x "$test_tmp/bin/systemctl"
  export TEST_SYSTEMD_KEY="$key"
  run "$script"
  [ "$status" -eq 0 ]
  [[ "$output" != *"$key"* ]]
  [ "$(cat "$LLAMA_PROM_CREDENTIALS_FILE")" = "$key" ]
  grep -q '^lab_llama_prom_key_in_sync 1$' "$LLAMA_PROM_METRIC_FILE"
}

@test "a missing key records zero and fails without exposing credentials" {
  cat > "$test_tmp/bin/systemctl" <<'SH'
#!/bin/sh
exit 0
SH
  chmod +x "$test_tmp/bin/systemctl"
  run "$script"
  [ "$status" -ne 0 ]
  [[ "$output" != *"never-print-this-llama-key"* ]]
  grep -q '^lab_llama_prom_key_in_sync 0$' "$LLAMA_PROM_METRIC_FILE"
  grep -q '^lab_llama_prom_key_sync_timestamp_seconds [0-9][0-9]*$' "$LLAMA_PROM_METRIC_FILE"
}

@test "conflicting key input flags fail safely and mark the sync out of sync" {
  printf 'file-secret\n' > "$test_tmp/source-key"
  run "$script" --api-key inline-secret --api-key-file "$test_tmp/source-key"
  [ "$status" -ne 0 ]
  [[ "$output" != *"inline-secret"* ]]
  [[ "$output" != *"file-secret"* ]]
  grep -q '^lab_llama_prom_key_in_sync 0$' "$LLAMA_PROM_METRIC_FILE"
}
