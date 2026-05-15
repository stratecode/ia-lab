#!/usr/bin/env bats
# Feature: agent-runtime, Property 13: Task Metadata Validation
# Validates: Requirements 26.2, 26.5

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

# ─── Generators specific to metadata validation ─────────────────────────────

# Generates a valid metadata JSON with all fields valid
generate_valid_metadata() {
    local correlation_id execution_id task_type priority risk_level
    local task_types=("coding" "refactor" "bugfix" "test" "documentation")
    local priorities=("low" "normal" "high" "critical")
    local risk_levels=("low" "medium" "high")

    correlation_id=$(random_uuid)
    execution_id=$(random_uuid)
    task_type=$(random_choice "${task_types[@]}")
    priority=$(random_choice "${priorities[@]}")
    risk_level=$(random_choice "${risk_levels[@]}")

    printf '{"correlation_id":"%s","execution_id":"%s","task_type":"%s","priority":"%s","risk_level":"%s"}' \
        "$correlation_id" "$execution_id" "$task_type" "$priority" "$risk_level"
}

# Generates a valid metadata JSON with a random subset of fields
generate_partial_valid_metadata() {
    local fields=()
    local task_types=("coding" "refactor" "bugfix" "test" "documentation")
    local priorities=("low" "normal" "high" "critical")
    local risk_levels=("low" "medium" "high")

    # Randomly include each field (50% chance)
    if (( RANDOM % 2 == 0 )); then
        fields+=("\"correlation_id\":\"$(random_uuid)\"")
    fi
    if (( RANDOM % 2 == 0 )); then
        fields+=("\"execution_id\":\"$(random_uuid)\"")
    fi
    if (( RANDOM % 2 == 0 )); then
        fields+=("\"task_type\":\"$(random_choice "${task_types[@]}")\"")
    fi
    if (( RANDOM % 2 == 0 )); then
        fields+=("\"priority\":\"$(random_choice "${priorities[@]}")\"")
    fi
    if (( RANDOM % 2 == 0 )); then
        fields+=("\"risk_level\":\"$(random_choice "${risk_levels[@]}")\"")
    fi

    # Join fields with commas
    local json="{"
    local first=1
    for field in "${fields[@]}"; do
        if (( first )); then
            json+="$field"
            first=0
        else
            json+=",$field"
        fi
    done
    json+="}"

    printf '%s' "$json"
}

# Generates metadata with an invalid correlation_id
generate_invalid_correlation_id() {
    local bad_uuid
    bad_uuid=$(random_invalid_uuid)
    printf '{"correlation_id":"%s"}' "$bad_uuid"
}

# Generates metadata with an invalid execution_id
generate_invalid_execution_id() {
    local bad_uuid
    bad_uuid=$(random_invalid_uuid)
    printf '{"execution_id":"%s"}' "$bad_uuid"
}

# Generates metadata with an invalid task_type
generate_invalid_task_type() {
    local invalid_types=("code" "fix" "refactoring" "testing" "docs" "deploy" "build" "unknown" "CODING" "Refactor")
    local bad_type
    bad_type=$(random_choice "${invalid_types[@]}")
    printf '{"task_type":"%s"}' "$bad_type"
}

# Generates metadata with an invalid priority
generate_invalid_priority() {
    local invalid_priorities=("urgent" "medium" "highest" "lowest" "none" "CRITICAL" "High" "p1" "p2")
    local bad_priority
    bad_priority=$(random_choice "${invalid_priorities[@]}")
    printf '{"priority":"%s"}' "$bad_priority"
}

# Generates metadata with an invalid risk_level
generate_invalid_risk_level() {
    local invalid_risks=("critical" "none" "minimal" "extreme" "LOW" "Medium" "HIGH" "very_high" "moderate")
    local bad_risk
    bad_risk=$(random_choice "${invalid_risks[@]}")
    printf '{"risk_level":"%s"}' "$bad_risk"
}

# ─── Property Tests ─────────────────────────────────────────────────────────

@test "Property 13: Valid metadata with all fields is accepted" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_valid_metadata)

        run validate_metadata "$json"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): valid metadata rejected: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Valid metadata with partial fields is accepted" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_partial_valid_metadata)

        run validate_metadata "$json"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): partial valid metadata rejected: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Empty JSON object is accepted (no fields to validate)" {
    run validate_metadata '{}'
    [[ "$status" -eq 0 ]]
}

@test "Property 13: Invalid correlation_id is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_invalid_correlation_id)

        # Skip empty correlation_id (that's valid — means field not provided meaningfully)
        local cid
        cid=$(printf '%s' "$json" | jq -r '.correlation_id // empty')
        [[ -n "$cid" ]] || continue

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): invalid correlation_id accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"correlation_id"* ]] || {
            echo "FAILED (iteration $i): error message doesn't mention correlation_id: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Invalid execution_id is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_invalid_execution_id)

        # Skip empty execution_id
        local eid
        eid=$(printf '%s' "$json" | jq -r '.execution_id // empty')
        [[ -n "$eid" ]] || continue

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): invalid execution_id accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"execution_id"* ]] || {
            echo "FAILED (iteration $i): error message doesn't mention execution_id: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Invalid task_type is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_invalid_task_type)

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): invalid task_type accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"task_type"* ]] || {
            echo "FAILED (iteration $i): error message doesn't mention task_type: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Invalid priority is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_invalid_priority)

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): invalid priority accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"priority"* ]] || {
            echo "FAILED (iteration $i): error message doesn't mention priority: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Invalid risk_level is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local json
        json=$(generate_invalid_risk_level)

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): invalid risk_level accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"risk_level"* ]] || {
            echo "FAILED (iteration $i): error message doesn't mention risk_level: $output" >&2
            return 1
        }
    done
}

@test "Property 13: Invalid JSON is rejected" {
    local invalid_jsons=(
        'not-json-at-all'
        '{"broken": "value"'
        '{key: value}'
        '['
        '{"unclosed'
        '}{bad'
    )

    for json in "${invalid_jsons[@]}"; do
        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED: invalid JSON accepted: '$json'" >&2
            echo "Output: $output" >&2
            return 1
        }
        [[ "$output" == *"invalid JSON"* ]] || {
            echo "FAILED: error message doesn't mention 'invalid JSON': $output" >&2
            return 1
        }
    done
}

@test "Property 13: Mixed valid/invalid — one bad field causes rejection" {
    local task_types=("coding" "refactor" "bugfix" "test" "documentation")
    local priorities=("low" "normal" "high" "critical")
    local risk_levels=("low" "medium" "high")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local strategy=$(( RANDOM % 5 ))
        local json

        case $strategy in
            0)
                # Valid everything except correlation_id
                json=$(printf '{"correlation_id":"not-a-uuid-%s","execution_id":"%s","task_type":"%s","priority":"%s","risk_level":"%s"}' \
                    "$(random_hex 4)" "$(random_uuid)" \
                    "$(random_choice "${task_types[@]}")" \
                    "$(random_choice "${priorities[@]}")" \
                    "$(random_choice "${risk_levels[@]}")")
                ;;
            1)
                # Valid everything except execution_id
                json=$(printf '{"correlation_id":"%s","execution_id":"bad-exec-%s","task_type":"%s","priority":"%s","risk_level":"%s"}' \
                    "$(random_uuid)" "$(random_hex 4)" \
                    "$(random_choice "${task_types[@]}")" \
                    "$(random_choice "${priorities[@]}")" \
                    "$(random_choice "${risk_levels[@]}")")
                ;;
            2)
                # Valid everything except task_type
                json=$(printf '{"correlation_id":"%s","execution_id":"%s","task_type":"invalid_type","priority":"%s","risk_level":"%s"}' \
                    "$(random_uuid)" "$(random_uuid)" \
                    "$(random_choice "${priorities[@]}")" \
                    "$(random_choice "${risk_levels[@]}")")
                ;;
            3)
                # Valid everything except priority
                json=$(printf '{"correlation_id":"%s","execution_id":"%s","task_type":"%s","priority":"invalid_prio","risk_level":"%s"}' \
                    "$(random_uuid)" "$(random_uuid)" \
                    "$(random_choice "${task_types[@]}")" \
                    "$(random_choice "${risk_levels[@]}")")
                ;;
            4)
                # Valid everything except risk_level
                json=$(printf '{"correlation_id":"%s","execution_id":"%s","task_type":"%s","priority":"%s","risk_level":"invalid_risk"}' \
                    "$(random_uuid)" "$(random_uuid)" \
                    "$(random_choice "${task_types[@]}")" \
                    "$(random_choice "${priorities[@]}")")
                ;;
        esac

        run validate_metadata "$json"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i, strategy $strategy): metadata with one invalid field accepted: $json" >&2
            echo "Output: $output" >&2
            return 1
        }
    done
}
