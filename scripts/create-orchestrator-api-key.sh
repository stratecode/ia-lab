#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SSH_KEY_DEFAULT="${ROOT_DIR}/ssh/lab"
POSTGRES_CONTAINER_DEFAULT="orchestrator-postgres"
POSTGRES_USER_DEFAULT="orchestrator"
POSTGRES_DB_DEFAULT="orchestrator"

target_host="${LAB_HOSTNAME:-}"
target_user="${LAB_USER:-admin}"
ssh_key="${SSH_KEY_DEFAULT}"
postgres_container="${LAB_POSTGRES_CONTAINER:-${POSTGRES_CONTAINER_DEFAULT}}"
postgres_user="${LAB_POSTGRES_USER:-${POSTGRES_USER_DEFAULT}}"
postgres_db="${LAB_POSTGRES_DB:-${POSTGRES_DB_DEFAULT}}"
key_scope="operator"
key_name="manual-operator"
run_local=0
output_json=0
dry_run=0

usage() {
  cat <<'EOF'
Usage:
  scripts/create-orchestrator-api-key.sh [options]

Creates or rotates an orchestrator API key and prints the raw key.

By default:
  - scope: operator
  - name: manual-operator
  - target: remote host from LAB_HOSTNAME over SSH, if available

Options:
  --name NAME              Logical API key name. Default: manual-operator
  --scope SCOPE            readonly | bot | operator | admin. Default: operator
  --host HOST              Remote host to SSH into.
  --user USER              SSH user. Default: admin or LAB_USER.
  --ssh-key PATH           SSH private key. Default: ./ssh/lab
  --postgres-container N   Docker container name. Default: orchestrator-postgres
  --postgres-user USER     PostgreSQL user. Default: orchestrator
  --postgres-db DB         PostgreSQL database. Default: orchestrator
  --local-host             Run against local Docker instead of SSH.
  --json                   Emit JSON to stdout instead of only the raw key.
  --dry-run                Generate key material and show the target command without writing to DB.
  --help                   Show this help.

Examples:
  scripts/create-orchestrator-api-key.sh --name ide-operator
  scripts/create-orchestrator-api-key.sh --local-host --name local-operator
  scripts/create-orchestrator-api-key.sh --name benchmark-operator --json
EOF
}

die() {
  printf '[create-api-key] %s\n' "$*" >&2
  exit 1
}

log() {
  printf '[create-api-key] %s\n' "$*" >&2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name)
      key_name="${2:-}"
      shift 2
      ;;
    --scope)
      key_scope="${2:-}"
      shift 2
      ;;
    --host)
      target_host="${2:-}"
      shift 2
      ;;
    --user)
      target_user="${2:-}"
      shift 2
      ;;
    --ssh-key)
      ssh_key="${2:-}"
      shift 2
      ;;
    --postgres-container)
      postgres_container="${2:-}"
      shift 2
      ;;
    --postgres-user)
      postgres_user="${2:-}"
      shift 2
      ;;
    --postgres-db)
      postgres_db="${2:-}"
      shift 2
      ;;
    --local-host)
      run_local=1
      shift
      ;;
    --json)
      output_json=1
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

case "${key_scope}" in
  readonly|bot|operator|admin)
    ;;
  *)
    die "Invalid scope '${key_scope}'. Use readonly, bot, operator, or admin."
    ;;
esac

[[ -n "${key_name}" ]] || die "Key name cannot be blank."
[[ "${run_local}" -eq 1 || -n "${target_host}" ]] || die "No target host configured. Use --local-host or --host HOST."
[[ "${run_local}" -eq 1 || -n "${target_user}" ]] || die "No SSH user configured. Use --user USER."

RAW_KEY="$(python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(32))
PY
)"

KEY_ID="$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"

KEY_HASH="$(RAW_KEY="${RAW_KEY}" python3 - <<'PY'
import hashlib
import os
print(hashlib.sha256(os.environ["RAW_KEY"].encode()).hexdigest())
PY
)"

SQL="$(cat <<SQL
INSERT INTO api_keys (id, key_hash, name, scope, is_active, created_at)
SELECT '${KEY_ID}'::uuid, '${KEY_HASH}', '${key_name}', '${key_scope}', true, NOW()
WHERE NOT EXISTS (
  SELECT 1 FROM api_keys WHERE name = '${key_name}'
);
UPDATE api_keys
   SET key_hash='${KEY_HASH}',
       scope='${key_scope}',
       is_active=true,
       revoked_at=NULL
 WHERE name='${key_name}'
   AND (
     key_hash <> '${KEY_HASH}'
     OR scope <> '${key_scope}'
     OR is_active <> true
     OR revoked_at IS NOT NULL
   );
SQL
)"

if (( dry_run )); then
  log "dry-run enabled; no database changes will be written"
  log "target_mode=$([[ ${run_local} -eq 1 ]] && printf 'local' || printf 'remote')"
  log "key_name=${key_name} scope=${key_scope}"
  if (( run_local )); then
    log "docker exec -i ${postgres_container} psql -v ON_ERROR_STOP=1 -U ${postgres_user} -d ${postgres_db}"
  else
    log "ssh -i ${ssh_key} ${target_user}@${target_host} docker exec -i ${postgres_container} psql -v ON_ERROR_STOP=1 -U ${postgres_user} -d ${postgres_db}"
  fi
else
  if (( run_local )); then
    docker ps --format '{{.Names}}' | grep -qx "${postgres_container}" || die "postgres container not found locally: ${postgres_container}"
    printf '%s\n' "${SQL}" | docker exec -i "${postgres_container}" psql -v ON_ERROR_STOP=1 -U "${postgres_user}" -d "${postgres_db}" >/dev/null
  else
    [[ -f "${ssh_key}" ]] || die "SSH key not found: ${ssh_key}"
    ssh -i "${ssh_key}" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${target_user}@${target_host}" \
      "export POSTGRES_CONTAINER='${postgres_container}' POSTGRES_USER='${postgres_user}' POSTGRES_DB='${postgres_db}'; /bin/bash -s" <<'SH'
set -euo pipefail
docker ps --format '{{.Names}}' | grep -qx "${POSTGRES_CONTAINER}" || {
  echo "postgres container not found remotely: ${POSTGRES_CONTAINER}" >&2
  exit 1
}
docker exec -i "${POSTGRES_CONTAINER}" psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" >/dev/null
SH
    printf '%s\n' "${SQL}" | ssh -i "${ssh_key}" -o BatchMode=yes -o StrictHostKeyChecking=accept-new "${target_user}@${target_host}" \
      "docker exec -i '${postgres_container}' psql -v ON_ERROR_STOP=1 -U '${postgres_user}' -d '${postgres_db}'" >/dev/null
  fi
fi

if (( output_json )); then
  target_mode="$([[ ${run_local} -eq 1 ]] && printf 'local' || printf 'remote')"
  python3 - <<PY
import json
print(json.dumps({
    "name": ${key_name@Q},
    "scope": ${key_scope@Q},
    "raw_key": ${RAW_KEY@Q},
    "dry_run": ${dry_run},
    "target_mode": ${target_mode@Q},
}))
PY
else
  printf '%s\n' "${RAW_KEY}"
fi
