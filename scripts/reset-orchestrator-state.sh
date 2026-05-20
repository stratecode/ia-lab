#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SSH_KEY_DEFAULT="${ROOT_DIR}/ssh/lab"
TUI_STATE_DEFAULT="${HOME}/.config/lab-agent/tui.json"
WORKSPACE_ROOT_DEFAULT="/srv/ai-lab/orchestrator/workspaces"
POSTGRES_CONTAINER_DEFAULT="orchestrator-postgres"
POSTGRES_USER_DEFAULT="orchestrator"
POSTGRES_DB_DEFAULT="orchestrator"

target_host="${LAB_HOSTNAME:-}"
target_user="${LAB_USER:-admin}"
ssh_key="${SSH_KEY_DEFAULT}"
tui_state_path="${TUI_STATE_DEFAULT}"
workspace_root="${WORKSPACE_ROOT_DEFAULT}"
postgres_container="${POSTGRES_CONTAINER_DEFAULT}"
postgres_user="${POSTGRES_USER_DEFAULT}"
postgres_db="${POSTGRES_DB_DEFAULT}"

reset_ui=1
reset_db=1
reset_workspaces=1
reset_api_keys=0
backup_db=1
run_remote=1
run_local=0

usage() {
  cat <<'EOF'
Usage:
  scripts/reset-orchestrator-state.sh [options]

Resets the lab operator state without dropping the schema.

What it clears by default:
  - local TUI state (~/.config/lab-agent/tui.json)
  - orchestrator operational tables (tasks, approvals, initiatives, artifacts, bridges, research, audit log)
  - generated orchestrator workspaces on the target host

What it preserves by default:
  - PostgreSQL schema and migrations
  - API keys (Telegram/Open WebUI stay alive)

Options:
  --host HOST              Remote host to ssh into.
  --user USER              SSH user. Default: admin or LAB_USER.
  --ssh-key PATH           SSH private key. Default: ./ssh/lab
  --workspace-root PATH    Orchestrator workspace root. Default: /srv/ai-lab/orchestrator/workspaces
  --postgres-container N   Docker container name. Default: orchestrator-postgres
  --postgres-user USER     PostgreSQL user. Default: orchestrator
  --postgres-db DB         PostgreSQL database. Default: orchestrator
  --local-host             Run DB/workspace reset on this machine instead of over SSH.
  --local-ui-only          Only remove local TUI state.
  --keep-workspaces        Do not delete generated workspaces.
  --skip-backup            Do not create a DB backup before truncating tables.
  --nuclear                Also wipe api_keys.
  --help                   Show this help.

Examples:
  scripts/reset-orchestrator-state.sh --host lab.stratecode.com
  scripts/reset-orchestrator-state.sh --local-host --keep-workspaces
  scripts/reset-orchestrator-state.sh --local-ui-only
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
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
    --workspace-root)
      workspace_root="${2:-}"
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
      run_remote=0
      run_local=1
      shift
      ;;
    --local-ui-only)
      reset_db=0
      reset_workspaces=0
      shift
      ;;
    --keep-workspaces)
      reset_workspaces=0
      shift
      ;;
    --skip-backup)
      backup_db=0
      shift
      ;;
    --nuclear)
      reset_api_keys=1
      shift
      ;;
    --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

log() {
  printf '[reset] %s\n' "$*"
}

die() {
  printf '[reset] %s\n' "$*" >&2
  exit 1
}

if (( reset_ui )); then
  if [[ -f "${tui_state_path}" ]]; then
    rm -f "${tui_state_path}"
    log "Removed local TUI state: ${tui_state_path}"
  else
    log "Local TUI state already absent: ${tui_state_path}"
  fi
fi

if (( ! reset_db )) && (( ! reset_workspaces )); then
  log "Nothing else requested."
  exit 0
fi

sql="$(cat <<'SQL'
BEGIN;

TRUNCATE TABLE
  initiative_task_links,
  initiative_phase_reviews,
  initiatives,
  approvals,
  state_transitions,
  evaluation_dataset_items,
  evaluation_runs,
  research_runs,
  artifacts,
  tool_invocations,
  local_bridges,
  tasks,
  audit_log
RESTART IDENTITY CASCADE;
SQL
)"

if (( reset_api_keys )); then
  sql+=$'\nTRUNCATE TABLE api_keys RESTART IDENTITY CASCADE;'
fi
sql+=$'\nCOMMIT;'

remote_script="$(cat <<'BASH'
set -euo pipefail

postgres_container="$1"
postgres_user="$2"
postgres_db="$3"
workspace_root="$4"
do_backup="$5"
do_reset_db="$6"
do_reset_workspaces="$7"

timestamp="$(date +%Y%m%d-%H%M%S)"

if ! docker ps --format '{{.Names}}' | grep -qx "${postgres_container}"; then
  echo "postgres container not found: ${postgres_container}" >&2
  exit 1
fi

if [[ "${do_backup}" == "1" && "${do_reset_db}" == "1" ]]; then
  backup_path="/tmp/orchestrator-reset-backup-${timestamp}.sql.gz"
  docker exec -i "${postgres_container}" pg_dump -U "${postgres_user}" -d "${postgres_db}" | gzip -c > "${backup_path}"
  echo "backup=${backup_path}"
fi

if [[ "${do_reset_db}" == "1" ]]; then
  docker exec -i "${postgres_container}" psql -v ON_ERROR_STOP=1 -U "${postgres_user}" -d "${postgres_db}" >/dev/null
fi

if [[ "${do_reset_workspaces}" == "1" ]]; then
  mkdir -p "${workspace_root}"
  find "${workspace_root}" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  echo "workspaces_cleared=${workspace_root}"
fi
BASH
)"

run_sql_local() {
  printf '%s\n' "${sql}" | docker exec -i "${postgres_container}" psql -v ON_ERROR_STOP=1 -U "${postgres_user}" -d "${postgres_db}"
}

run_sql_remote() {
  local host_spec="${target_user}@${target_host}"
  ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "${ssh_key}" "${host_spec}" \
    "docker exec -i '${postgres_container}' psql -v ON_ERROR_STOP=1 -U '${postgres_user}' -d '${postgres_db}'" <<EOF
${sql}
EOF
}

if (( run_local )); then
  if (( backup_db )) && (( reset_db )); then
    backup_path="/tmp/orchestrator-reset-backup-$(date +%Y%m%d-%H%M%S).sql.gz"
    docker exec -i "${postgres_container}" pg_dump -U "${postgres_user}" -d "${postgres_db}" | gzip -c > "${backup_path}"
    log "Database backup written to ${backup_path}"
  fi
  if (( reset_db )); then
    run_sql_local >/dev/null
    log "Operational PostgreSQL tables truncated"
  fi
  if (( reset_workspaces )); then
    mkdir -p "${workspace_root}"
    find "${workspace_root}" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
    log "Workspace root cleared: ${workspace_root}"
  fi
  exit 0
fi

[[ -n "${target_host}" ]] || die "Missing remote host. Use --host or set LAB_HOSTNAME."
[[ -f "${ssh_key}" ]] || die "SSH key not found: ${ssh_key}"

host_spec="${target_user}@${target_host}"
log "Checking SSH access to ${host_spec}"
ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "${ssh_key}" "${host_spec}" "hostname" >/dev/null

if (( backup_db )) || (( reset_workspaces )); then
  ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "${ssh_key}" "${host_spec}" \
    "bash -s -- '${postgres_container}' '${postgres_user}' '${postgres_db}' '${workspace_root}' '${backup_db}' '${reset_db}' '${reset_workspaces}'" <<EOF
${remote_script}
EOF
fi

if (( reset_db )); then
  run_sql_remote >/dev/null
  log "Operational PostgreSQL tables truncated on ${host_spec}"
fi
if (( reset_workspaces )); then
  log "Workspace root cleared on ${host_spec}: ${workspace_root}"
fi
if (( reset_api_keys )); then
  log "Nuclear mode used: api_keys were wiped too"
else
  log "API keys preserved"
fi
