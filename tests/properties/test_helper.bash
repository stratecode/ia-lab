#!/usr/bin/env bash
# test_helper.bash — Property test harness for aider-task-lib.sh
# Provides random input generators and assertion helpers for property-based testing.

# ─── Configuration ───────────────────────────────────────────────────────────
PROPERTY_ITERATIONS=100

# ─── Source the library under test ───────────────────────────────────────────
HELPER_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$HELPER_DIR/../.." && pwd)"
LIB_PATH="${PROJECT_ROOT}/roles/aider/files/aider-task-lib.sh"

if [[ ! -f "$LIB_PATH" ]]; then
    echo "ERROR: aider-task-lib.sh not found at $LIB_PATH" >&2
    exit 1
fi

source "$LIB_PATH"

# ─── Mock environment variables ─────────────────────────────────────────────
# Set up mock environment so library functions can be tested in isolation
export AIDER_REPOS_DIR="${BATS_TEST_TMPDIR:-/tmp}/mock_repos"
export AIDER_PROTECTED_BRANCHES="main,master,develop"
export AIDER_WORKSPACE_ROOT="${BATS_TEST_TMPDIR:-/tmp}/mock_workspaces"
export AIDER_LOG_DIR="${BATS_TEST_TMPDIR:-/tmp}/mock_logs"
export AIDER_LOCK_DIR="${BATS_TEST_TMPDIR:-/tmp}/mock_locks"
export AIDER_TASK_TIMEOUT=600
export AIDER_TEST_TIMEOUT=300
export AIDER_MAX_CONCURRENT=2
export AIDER_QUEUE_TIMEOUT=300
export AIDER_MAX_WORKSPACE_DISK=10737418240
export AIDER_MAX_WORKSPACE_SIZE=1073741824
export AIDER_EDIT_FORMAT=whole
export AIDER_MAP_TOKENS=1024
export AIDER_MAX_CHAT_HISTORY_TOKENS=2048
export AIDER_MODEL_NAME=coder
export AIDER_DEFAULT_MODE=autonomous
export AIDER_API_BASE="http://127.0.0.1:8080/v1"
export AIDER_API_KEY="local-code"

# ─── Random Generators ───────────────────────────────────────────────────────

# random_string(max_len)
# Generates random strings including unicode, special chars, spaces.
# Arguments:
#   $1 - max_len: maximum length of the string (default: 64)
random_string() {
    local max_len="${1:-64}"
    local len=$(( (RANDOM % max_len) + 1 ))
    local charset='abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 !@#$%^&*()_+-=[]{}|;:,.<>?/~`'
    local special_chars=('ñ' 'ü' 'é' 'ß' '中' '日' '🚀' '→' '∞' '½' 'ø' 'å')
    local result=""
    local i

    for ((i = 0; i < len; i++)); do
        # 80% chance of normal charset, 20% chance of special/unicode
        if (( RANDOM % 5 == 0 )); then
            local idx=$(( RANDOM % ${#special_chars[@]} ))
            result+="${special_chars[$idx]}"
        else
            local idx=$(( RANDOM % ${#charset} ))
            result+="${charset:$idx:1}"
        fi
    done

    printf '%s' "$result"
}

# random_uuid()
# Generates valid UUID v4 format strings.
random_uuid() {
    # UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
    # where y is one of 8, 9, a, b
    local hex_chars="0123456789abcdef"
    local variant_chars="89ab"
    local uuid=""
    local i

    # 8 hex chars
    for ((i = 0; i < 8; i++)); do
        uuid+="${hex_chars:$(( RANDOM % 16 )):1}"
    done
    uuid+="-"
    # 4 hex chars
    for ((i = 0; i < 4; i++)); do
        uuid+="${hex_chars:$(( RANDOM % 16 )):1}"
    done
    uuid+="-4"
    # 3 hex chars (version nibble is 4)
    for ((i = 0; i < 3; i++)); do
        uuid+="${hex_chars:$(( RANDOM % 16 )):1}"
    done
    uuid+="-"
    # variant nibble (8, 9, a, or b) + 3 hex chars
    uuid+="${variant_chars:$(( RANDOM % 4 )):1}"
    for ((i = 0; i < 3; i++)); do
        uuid+="${hex_chars:$(( RANDOM % 16 )):1}"
    done
    uuid+="-"
    # 12 hex chars
    for ((i = 0; i < 12; i++)); do
        uuid+="${hex_chars:$(( RANDOM % 16 )):1}"
    done

    printf '%s' "$uuid"
}

# random_invalid_uuid()
# Generates strings that are NOT valid UUID v4.
random_invalid_uuid() {
    local strategy=$(( RANDOM % 7 ))
    local hex_chars="0123456789abcdef"

    case $strategy in
        0)
            # Wrong version nibble (not 4)
            local versions="012356789abcdef"
            local ver="${versions:$(( RANDOM % 15 )):1}"
            printf '%s-%s-%s%s-%s%s-%s' \
                "$(random_hex 8)" "$(random_hex 4)" "$ver" "$(random_hex 3)" \
                "${hex_chars:$(( RANDOM % 16 )):1}" "$(random_hex 3)" "$(random_hex 12)"
            ;;
        1)
            # Wrong variant nibble (not 8/9/a/b)
            local bad_variants="01234567cdef"
            local var="${bad_variants:$(( RANDOM % 12 )):1}"
            printf '%s-%s-4%s-%s%s-%s' \
                "$(random_hex 8)" "$(random_hex 4)" "$(random_hex 3)" \
                "$var" "$(random_hex 3)" "$(random_hex 12)"
            ;;
        2)
            # Too short
            printf '%s-%s' "$(random_hex 4)" "$(random_hex 4)"
            ;;
        3)
            # No hyphens
            printf '%s' "$(random_hex 32)"
            ;;
        4)
            # Contains invalid characters
            printf '%s-%s-4%s-%s%s-%s' \
                "$(random_hex 6)zz" "$(random_hex 4)" "$(random_hex 3)" \
                "8" "$(random_hex 3)" "$(random_hex 12)"
            ;;
        5)
            # Empty string
            printf ''
            ;;
        6)
            # Random garbage
            random_string 20
            ;;
    esac
}

# random_hex(count)
# Helper: generates count random hex characters.
random_hex() {
    local count="${1:-8}"
    local hex_chars="0123456789abcdef"
    local result=""
    local i
    for ((i = 0; i < count; i++)); do
        result+="${hex_chars:$(( RANDOM % 16 )):1}"
    done
    printf '%s' "$result"
}

# random_file_path(depth)
# Generates random file paths with varying depth.
# Arguments:
#   $1 - depth: maximum directory depth (default: random 1-5)
random_file_path() {
    local max_depth="${1:-$(( (RANDOM % 5) + 1 ))}"
    local depth=$(( (RANDOM % max_depth) + 1 ))
    local path=""
    local dir_names=("src" "lib" "tests" "docs" "config" "utils" "helpers" "modules" "pkg" "internal")
    local extensions=(".py" ".sh" ".js" ".ts" ".yml" ".json" ".md" ".txt" ".cfg" ".toml")
    local i

    for ((i = 0; i < depth; i++)); do
        local dir_idx=$(( RANDOM % ${#dir_names[@]} ))
        path+="${dir_names[$dir_idx]}/"
    done

    # Add filename
    local name_len=$(( (RANDOM % 12) + 3 ))
    local name=""
    local chars="abcdefghijklmnopqrstuvwxyz0123456789_-"
    for ((i = 0; i < name_len; i++)); do
        name+="${chars:$(( RANDOM % ${#chars} )):1}"
    done

    local ext_idx=$(( RANDOM % ${#extensions[@]} ))
    path+="${name}${extensions[$ext_idx]}"

    printf '%s' "$path"
}

# random_number(min, max)
# Generates random integers in range [min, max].
# Arguments:
#   $1 - min: minimum value (default: 0)
#   $2 - max: maximum value (default: 1000)
random_number() {
    local min="${1:-0}"
    local max="${2:-1000}"
    local range=$(( max - min + 1 ))

    if (( range <= 0 )); then
        printf '%d' "$min"
        return
    fi

    printf '%d' $(( (RANDOM % range) + min ))
}

# random_choice(array_elements...)
# Picks random element from arguments.
# Usage: random_choice "a" "b" "c"
random_choice() {
    local -a items=("$@")
    local count=${#items[@]}

    if (( count == 0 )); then
        return 1
    fi

    local idx=$(( RANDOM % count ))
    printf '%s' "${items[$idx]}"
}

# random_json_string()
# Generates random JSON-like strings (valid and invalid JSON).
random_json_string() {
    local strategy=$(( RANDOM % 5 ))

    case $strategy in
        0)
            # Valid JSON object with random fields
            local key1="field_$(random_hex 4)"
            local val1="value_$(random_hex 6)"
            printf '{"'%s'":"'%s'"}' "$key1" "$val1"
            ;;
        1)
            # Valid JSON with nested object
            printf '{"name":"test_%s","nested":{"id":%d}}' "$(random_hex 4)" "$(random_number 1 9999)"
            ;;
        2)
            # Invalid JSON (missing closing brace)
            printf '{"broken":"value_%s"' "$(random_hex 4)"
            ;;
        3)
            # Valid JSON array
            printf '["%s","%s","%s"]' "$(random_hex 4)" "$(random_hex 4)" "$(random_hex 4)"
            ;;
        4)
            # Plain string (not JSON)
            printf 'not-json-%s' "$(random_hex 8)"
            ;;
    esac
}

# ─── Assertion Helpers ───────────────────────────────────────────────────────

# assert_matches_regex(value, regex)
# Asserts value matches the given regex pattern.
assert_matches_regex() {
    local value="$1"
    local regex="$2"

    if [[ ! "$value" =~ $regex ]]; then
        echo "ASSERTION FAILED: expected '$value' to match regex '$regex'" >&2
        return 1
    fi
    return 0
}

# assert_not_matches_regex(value, regex)
# Asserts value does NOT match the given regex pattern.
assert_not_matches_regex() {
    local value="$1"
    local regex="$2"

    if [[ "$value" =~ $regex ]]; then
        echo "ASSERTION FAILED: expected '$value' NOT to match regex '$regex'" >&2
        return 1
    fi
    return 0
}

# assert_exit_code(expected, actual)
# Asserts exit codes match.
assert_exit_code() {
    local expected="$1"
    local actual="$2"

    if [[ "$expected" != "$actual" ]]; then
        echo "ASSERTION FAILED: expected exit code $expected, got $actual" >&2
        return 1
    fi
    return 0
}
