#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LAB_EXEC="$ROOT_DIR/scripts/codex-lab-exec"
ENV_FILE="${VERIFY_CODEX_LAB_ENV_FILE:-$ROOT_DIR/.env}"
CODEX_HOME_DIR="${VERIFY_CODEX_LAB_CODEX_HOME:-$HOME/.codex-lab}"
TRIALS="${VERIFY_CODEX_LAB_TRIALS:-3}"
TRIAL_RETRIES="${VERIFY_CODEX_LAB_TRIAL_RETRIES:-2}"
ARTIFACT_DIR="${VERIFY_CODEX_LAB_ARTIFACT_DIR:-$ROOT_DIR/tmp/verify-codex-lab-remote}"
REAL_REPO=""
KEEP_REAL_MARKER="false"
KEEP_REAL_WORKTREE="false"

usage() {
  cat <<'EOF'
Usage:
  scripts/verify-codex-lab-remote.sh [--real-repo PATH] [--keep-real-marker] [--keep-real-worktree]

Runs repeated end-to-end checks proving that Codex talks to the remote lab model
while editing a local repository checkout.

What it verifies on each temp-repo trial:
  - the wrapper reaches provider `lab-codex-gateway`
  - Codex reports the target repo as `workdir`
  - the agent creates the requested file in the local repo
  - the newest transcript under `~/.codex-lab/sessions` records the same repo as `cwd`

Optional:
  --real-repo PATH      Run one additional smoke test against a real local repo.
  --keep-real-marker    Keep the marker file written in the real repo.
  --keep-real-worktree  Keep the temporary detached worktree used for tracked-file verification.

Environment:
  VERIFY_CODEX_LAB_TRIALS      default: 3
  VERIFY_CODEX_LAB_TRIAL_RETRIES default: 2
  VERIFY_CODEX_LAB_CODEX_HOME  default: ~/.codex-lab
  VERIFY_CODEX_LAB_ENV_FILE    default: ./.env
  VERIFY_CODEX_LAB_ARTIFACT_DIR default: ./tmp/verify-codex-lab-remote
EOF
}

die() {
  printf '%s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --real-repo)
      REAL_REPO="$2"
      shift 2
      ;;
    --keep-real-marker)
      KEEP_REAL_MARKER="true"
      shift
      ;;
    --keep-real-worktree)
      KEEP_REAL_WORKTREE="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1"
      ;;
  esac
done

require_command git
require_command python3
[[ -x "$LAB_EXEC" ]] || die "Missing executable launcher: $LAB_EXEC"
[[ -f "$ENV_FILE" ]] || die "Missing env file: $ENV_FILE"
[[ -d "$CODEX_HOME_DIR" ]] || die "Missing lab CODEX_HOME: $CODEX_HOME_DIR"
mkdir -p "$ARTIFACT_DIR"

run_trial_once() {
  local repo_dir="$1"
  local marker_name="$2"
  local marker_value="$3"
  local capture_file="$4"
  local sessions_before session_after

  sessions_before="$(find "$CODEX_HOME_DIR/sessions" -type f 2>/dev/null | sort)"
  "$LAB_EXEC" --project-dir "$repo_dir" --prompt "Create a file named ${marker_name} at repository root with exactly this single line: ${marker_value}. Do not modify any other file." \
    >"$capture_file" 2>&1

  [[ -f "$repo_dir/$marker_name" ]] || die "Marker file missing: $repo_dir/$marker_name"
  [[ "$(tr -d '\r' < "$repo_dir/$marker_name")" == "$marker_value" ]] || die "Marker content mismatch in $repo_dir/$marker_name"

  grep -F "provider: lab-codex-gateway" "$capture_file" >/dev/null || die "Missing lab provider evidence in $capture_file"
  grep -F "workdir: $repo_dir" "$capture_file" >/dev/null || die "Missing workdir evidence in $capture_file"

  session_after="$(resolve_session_transcript "$capture_file")"
  validate_transcript "$repo_dir" "$capture_file" "$session_after" "$sessions_before"

  printf '%s\n' "$session_after"
}

resolve_session_transcript() {
  local capture_file="$1"
  local session_id
  session_id="$(sed -n 's/^session id: //p' "$capture_file" | head -n 1)"
  [[ -n "$session_id" ]] || die "Missing session id in $capture_file"
  find "$CODEX_HOME_DIR/sessions" -type f 2>/dev/null | rg "$session_id" | head -n 1
}

validate_transcript() {
  local repo_dir="$1"
  local capture_file="$2"
  local session_after="$3"
  local sessions_before="$4"
  [[ -n "$session_after" ]] || die "No transcript found for capture $capture_file"
  if [[ -n "$sessions_before" ]] && grep -Fqx "$session_after" <<<"$sessions_before"; then
    die "No new transcript detected for trial"
  fi
  grep -F "\"cwd\":\"$repo_dir\"" "$session_after" >/dev/null || die "Transcript cwd mismatch in $session_after"
  grep -F "\"model_provider\":\"lab-codex-gateway\"" "$session_after" >/dev/null || die "Transcript provider mismatch in $session_after"
}

run_trial() {
  local repo_dir="$1"
  local marker_name="$2"
  local marker_value="$3"
  local capture_file="$4"
  local attempt=1
  local trial_capture transcript
  while [[ "$attempt" -le "$TRIAL_RETRIES" ]]; do
    trial_capture="${capture_file%.log}.attempt-${attempt}.log"
    if transcript="$(run_trial_once "$repo_dir" "$marker_name" "$marker_value" "$trial_capture")"; then
      cp "$trial_capture" "$capture_file"
      printf '%s\n' "$transcript"
      return 0
    fi
    printf 'trial-retry=%s/%s capture=%s\n' "$attempt" "$TRIAL_RETRIES" "$trial_capture" >&2
    attempt=$((attempt + 1))
  done
  die "Trial failed after ${TRIAL_RETRIES} attempts; see ${capture_file%.log}.attempt-*.log"
}

cleanup_temp_repo() {
  local repo_dir="$1"
  rm -rf "$repo_dir"
}

create_temp_repo() {
  local repo_dir
  repo_dir="$(mktemp -d /tmp/codex-lab-verify.XXXXXX)"
  git -C "$repo_dir" init >/dev/null
  printf '# verification repo\n' >"$repo_dir/README.md"
  git -C "$repo_dir" add README.md >/dev/null
  git -C "$repo_dir" commit -m 'init' >/dev/null
  printf '%s\n' "$repo_dir"
}

verify_temp_trials() {
  local index=1
  local repo_dir marker_name marker_value capture_file transcript
  while [[ "$index" -le "$TRIALS" ]]; do
    repo_dir="$(create_temp_repo)"
    marker_name="AGENT_REMOTE_TRIAL_${index}.txt"
    marker_value="trial-${index}-remote-local-ok"
    capture_file="$ARTIFACT_DIR/temp-trial-${index}.log"
    transcript="$(run_trial "$repo_dir" "$marker_name" "$marker_value" "$capture_file")"
    printf 'temp-trial=%s repo=%s transcript=%s\n' "$index" "$repo_dir" "$transcript"
    printf '  marker=%s content=%s\n' "$marker_name" "$marker_value"
    printf '  capture=%s\n' "$capture_file"
    cleanup_temp_repo "$repo_dir"
    index=$((index + 1))
  done
}

verify_real_repo() {
  local repo_dir="$1"
  local marker_name marker_value capture_file transcript
  [[ -d "$repo_dir" ]] || die "Real repo does not exist: $repo_dir"
  git -C "$repo_dir" rev-parse --show-toplevel >/dev/null || die "Not a git repo: $repo_dir"

  marker_name="AGENT_REMOTE_REAL_REPO_CHECK.txt"
  marker_value="real-repo-remote-local-ok"
  capture_file="$ARTIFACT_DIR/real-repo.log"
  transcript="$(run_trial "$repo_dir" "$marker_name" "$marker_value" "$capture_file")"

  printf 'real-repo=%s transcript=%s\n' "$repo_dir" "$transcript"
  printf '  marker=%s content=%s\n' "$marker_name" "$marker_value"
  printf '  capture=%s\n' "$capture_file"

  if [[ "$KEEP_REAL_MARKER" != "true" ]]; then
    rm -f "$repo_dir/$marker_name"
    printf '  cleanup=removed-marker\n'
  else
    printf '  cleanup=kept-marker\n'
  fi

  verify_real_repo_tracked_worktree "$repo_dir"
}

verify_real_repo_tracked_worktree() {
  local repo_dir="$1"
  local worktree_dir capture_file transcript sessions_before attempt attempt_capture
  local marker_line='<!-- codex-lab-remote-worktree-check -->'
  local readme_path
  readme_path="$repo_dir/README.md"
  [[ -f "$readme_path" ]] || die "Missing tracked README for worktree verification: $readme_path"

  worktree_dir="$(mktemp -d /tmp/codex-lab-real-worktree.XXXXXX)"
  rm -rf "$worktree_dir"
  git -C "$repo_dir" worktree add --detach "$worktree_dir" HEAD >/dev/null
  capture_file="$ARTIFACT_DIR/real-repo-worktree.log"
  attempt=1
  while [[ "$attempt" -le "$TRIAL_RETRIES" ]]; do
    git -C "$worktree_dir" checkout -- README.md >/dev/null 2>&1 || true
    attempt_capture="${capture_file%.log}.attempt-${attempt}.log"
    sessions_before="$(find "$CODEX_HOME_DIR/sessions" -type f 2>/dev/null | sort)"
    if "$LAB_EXEC" --project-dir "$worktree_dir" --prompt "Append exactly this single line to README.md as a new final line: ${marker_line}. Do not modify any other file. After editing, run git status --short and report the changed file." \
      >"$attempt_capture" 2>&1; then
      transcript="$(resolve_session_transcript "$attempt_capture")"
      validate_transcript "$worktree_dir" "$attempt_capture" "$transcript" "$sessions_before"
      grep -F "provider: lab-codex-gateway" "$attempt_capture" >/dev/null || die "Missing lab provider evidence in $attempt_capture"
      grep -F "workdir: $worktree_dir" "$attempt_capture" >/dev/null || die "Missing workdir evidence in $attempt_capture"
      tail -n 1 "$worktree_dir/README.md" | grep -Fx "$marker_line" >/dev/null || die "Tracked-file marker missing from $worktree_dir/README.md"
      [[ "$(git -C "$worktree_dir" diff --name-only)" == "README.md" ]] || die "Unexpected tracked-file diff in $worktree_dir"
      cp "$attempt_capture" "$capture_file"
      break
    fi
    printf 'tracked-trial-retry=%s/%s capture=%s\n' "$attempt" "$TRIAL_RETRIES" "$attempt_capture" >&2
    attempt=$((attempt + 1))
  done
  [[ "$attempt" -le "$TRIAL_RETRIES" ]] || die "Tracked-file trial failed after ${TRIAL_RETRIES} attempts; see ${capture_file%.log}.attempt-*.log"

  printf 'real-repo-worktree=%s transcript=%s\n' "$worktree_dir" "$transcript"
  printf '  changed-file=README.md\n'
  printf '  marker-line=%s\n' "$marker_line"
  printf '  capture=%s\n' "$capture_file"

  if [[ "$KEEP_REAL_WORKTREE" == "true" ]]; then
    printf '  cleanup=kept-worktree\n'
    return
  fi

  git -C "$repo_dir" worktree remove --force "$worktree_dir" >/dev/null
  printf '  cleanup=removed-worktree\n'
}

printf 'verify-codex-lab-remote start\n'
printf '  codex_home=%s\n' "$CODEX_HOME_DIR"
printf '  trials=%s\n' "$TRIALS"
verify_temp_trials
if [[ -n "$REAL_REPO" ]]; then
  verify_real_repo "$REAL_REPO"
fi
printf 'verify-codex-lab-remote done\n'
