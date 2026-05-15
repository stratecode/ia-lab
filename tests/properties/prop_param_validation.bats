#!/usr/bin/env bats
# Feature: agent-runtime, Property 2: Parameter Validation Rejects Invalid Inputs
#
# **Validates: Requirements 5.3, 5.8**
#
# Property: For any parameter set where the task-id is not a valid UUID v4,
# or the repository name does not match any entry in aider_repositories,
# or the description exceeds 10,000 characters, or any required parameter
# is missing, the Wrapper Script SHALL exit with a non-zero code and set
# task status to "failed".

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
    # Create mock repo directory for valid repo tests
    mkdir -p "${AIDER_REPOS_DIR}/test-repo"
}

# ─── validate_uuid rejects invalid UUIDs ─────────────────────────────────────

@test "Property 2: validate_uuid rejects all randomly generated invalid UUIDs (100 iterations)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local invalid_uuid
        invalid_uuid=$(random_invalid_uuid)

        run validate_uuid "$invalid_uuid"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED: validate_uuid accepted invalid UUID '$invalid_uuid' (iteration: $i)" >&2
            return 1
        }
    done
}

# ─── validate_uuid accepts valid UUIDs ────────────────────────────────────────

@test "Property 2: validate_uuid accepts all randomly generated valid UUIDs (100 iterations)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local valid_uuid
        valid_uuid=$(random_uuid)

        run validate_uuid "$valid_uuid"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: validate_uuid rejected valid UUID '$valid_uuid' (iteration: $i)" >&2
            return 1
        }
    done
}

# ─── validate_params rejects invalid UUIDs ────────────────────────────────────

@test "Property 2: validate_params rejects invalid UUIDs with non-zero exit and validation_error output (100 iterations)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local invalid_uuid
        invalid_uuid=$(random_invalid_uuid)

        run validate_params "$invalid_uuid" "test-repo" "valid description"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED: validate_params accepted invalid UUID '$invalid_uuid' (iteration: $i)" >&2
            return 1
        }
        [[ "$output" == *"validation_error"* ]] || {
            echo "FAILED: validate_params output missing 'validation_error' for invalid UUID '$invalid_uuid' (iteration: $i, output: '$output')" >&2
            return 1
        }
    done
}

# ─── validate_params rejects unknown repository names ─────────────────────────

@test "Property 2: validate_params rejects unknown repo names with non-zero exit and validation_error output (100 iterations)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local unknown_repo
        unknown_repo="nonexistent-$(random_hex 8)"
        local valid_uuid
        valid_uuid=$(random_uuid)

        run validate_params "$valid_uuid" "$unknown_repo" "valid description"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED: validate_params accepted unknown repo '$unknown_repo' (iteration: $i)" >&2
            return 1
        }
        [[ "$output" == *"validation_error"* ]] || {
            echo "FAILED: validate_params output missing 'validation_error' for unknown repo '$unknown_repo' (iteration: $i, output: '$output')" >&2
            return 1
        }
    done
}

# ─── validate_params rejects oversized descriptions ───────────────────────────

@test "Property 2: validate_params rejects descriptions exceeding 10000 characters (100 iterations)" {
    local valid_uuid
    valid_uuid=$(random_uuid)

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate a string >10000 chars (between 10001 and 15000)
        local extra_len
        extra_len=$(( (RANDOM % 5000) + 10001 ))
        local oversized=""
        # Build string using repeated pattern for efficiency
        local pattern="abcdefghij"
        local repeats=$(( extra_len / 10 + 1 ))
        local j
        for ((j = 0; j < repeats; j++)); do
            oversized+="$pattern"
        done
        oversized="${oversized:0:$extra_len}"

        run validate_params "$valid_uuid" "test-repo" "$oversized"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED: validate_params accepted oversized description (length: ${#oversized}, iteration: $i)" >&2
            return 1
        }
        [[ "$output" == *"validation_error"* ]] || {
            echo "FAILED: validate_params output missing 'validation_error' for oversized description (iteration: $i, output: '$output')" >&2
            return 1
        }
    done
}

# ─── validate_params rejects missing task-id ──────────────────────────────────

@test "Property 2: validate_params rejects missing task-id with non-zero exit and validation_error output" {
    run validate_params "" "test-repo" "valid description"
    [[ "$status" -ne 0 ]] || {
        echo "FAILED: validate_params accepted empty task-id" >&2
        return 1
    }
    [[ "$output" == *"validation_error"* ]] || {
        echo "FAILED: validate_params output missing 'validation_error' for empty task-id (output: '$output')" >&2
        return 1
    }
}

# ─── validate_params rejects missing repo name ────────────────────────────────

@test "Property 2: validate_params rejects missing repo name with non-zero exit and validation_error output" {
    local valid_uuid
    valid_uuid=$(random_uuid)

    run validate_params "$valid_uuid" "" "valid description"
    [[ "$status" -ne 0 ]] || {
        echo "FAILED: validate_params accepted empty repo name" >&2
        return 1
    }
    [[ "$output" == *"validation_error"* ]] || {
        echo "FAILED: validate_params output missing 'validation_error' for empty repo name (output: '$output')" >&2
        return 1
    }
}

# ─── validate_params rejects missing description ──────────────────────────────

@test "Property 2: validate_params rejects missing description with non-zero exit and validation_error output" {
    local valid_uuid
    valid_uuid=$(random_uuid)

    run validate_params "$valid_uuid" "test-repo" ""
    [[ "$status" -ne 0 ]] || {
        echo "FAILED: validate_params accepted empty description" >&2
        return 1
    }
    [[ "$output" == *"validation_error"* ]] || {
        echo "FAILED: validate_params output missing 'validation_error' for empty description (output: '$output')" >&2
        return 1
    }
}

# ─── validate_params accepts valid inputs ─────────────────────────────────────

@test "Property 2: validate_params accepts valid inputs (valid UUID, existing repo, description ≤10000 chars) (100 iterations)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local valid_uuid
        valid_uuid=$(random_uuid)
        # Generate a valid description (non-empty, ≤10000 chars)
        local desc_len
        desc_len=$(( (RANDOM % 200) + 1 ))
        local valid_desc
        valid_desc=$(random_string "$desc_len")

        run validate_params "$valid_uuid" "test-repo" "$valid_desc"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: validate_params rejected valid inputs (uuid: '$valid_uuid', desc_len: ${#valid_desc}, iteration: $i, output: '$output')" >&2
            return 1
        }
    done
}
