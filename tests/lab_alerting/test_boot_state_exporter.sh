#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EXPORTER="${ROOT_DIR}/roles/lab_alerting/files/bin/lab-boot-state-exporter"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

STATE_DIR="${TMP_DIR}/state"
TEXTFILE_DIR="${TMP_DIR}/textfile"
METRICS="${TEXTFILE_DIR}/lab-boot.prom"

metric() {
  awk -v name="$1" '$1 == name { print $2 }' "${METRICS}"
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

"${EXPORTER}" --initialize --state-dir "${STATE_DIR}" --textfile-dir "${TEXTFILE_DIR}"
"${EXPORTER}" --collect --state-dir "${STATE_DIR}" --textfile-dir "${TEXTFILE_DIR}" --boot-id boot-first --now 1000
[[ "$(metric lab_boot_previous_clean)" == "1" ]] || fail "first boot must be clean"
[[ "$(metric lab_boot_unclean_total)" == "0" ]] || fail "first boot must not increment the unclean counter"

touch "${STATE_DIR}/clean-shutdown"
"${EXPORTER}" --collect --state-dir "${STATE_DIR}" --textfile-dir "${TEXTFILE_DIR}" --boot-id boot-clean --now 2000
[[ "$(metric lab_boot_previous_clean)" == "1" ]] || fail "a present clean-shutdown marker must produce clean=1"
[[ "$(metric lab_boot_unclean_total)" == "0" ]] || fail "a clean shutdown must not increment the unclean counter"

"${EXPORTER}" --collect --state-dir "${STATE_DIR}" --textfile-dir "${TEXTFILE_DIR}" --boot-id boot-unclean --now 3000
[[ "$(metric lab_boot_previous_clean)" == "0" ]] || fail "a missing clean-shutdown marker must produce clean=0"
[[ "$(metric lab_boot_unclean_total)" == "1" ]] || fail "an unclean boot must increment the counter"
[[ "$(metric lab_boot_unclean_last_timestamp_seconds)" == "3000" ]] || fail "an unclean boot must record its timestamp"

"${EXPORTER}" --collect --state-dir "${STATE_DIR}" --textfile-dir "${TEXTFILE_DIR}" --boot-id boot-unclean --now 4000
[[ "$(metric lab_boot_unclean_total)" == "1" ]] || fail "collect must be idempotent within one boot"
[[ "$(metric lab_boot_unclean_last_timestamp_seconds)" == "3000" ]] || fail "a repeat collect must retain the last unclean timestamp"

echo "lab boot state exporter tests passed"
