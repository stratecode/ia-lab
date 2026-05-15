#!/usr/bin/env bats
# Feature: agent-runtime, Property 8: Failure Classification Mapping
# Validates: Requirements 27.1, 27.2, 27.3, 27.4, 27.5, 30.1, 30.4

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 8: all retryable failure types map to 'retryable' with retry_eligible=true" {
    local retryable_types=("inference_failure" "queue_timeout" "timeout")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Pick a random retryable failure type
        local failure_type
        failure_type=$(random_choice "${retryable_types[@]}")

        run classify_failure "$failure_type"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: classify_failure '$failure_type' returned non-zero exit code $status (iteration: $i)" >&2
            return 1
        }

        # Parse output: line 1 = classification, line 2 = retry_eligible
        local classification
        local retry_eligible
        classification=$(echo "$output" | sed -n '1p')
        retry_eligible=$(echo "$output" | sed -n '2p')

        [[ "$classification" == "retryable" ]] || {
            echo "FAILED: failure type '$failure_type' classified as '$classification' instead of 'retryable' (iteration: $i)" >&2
            return 1
        }

        [[ "$retry_eligible" == "true" ]] || {
            echo "FAILED: failure type '$failure_type' has retry_eligible='$retry_eligible' instead of 'true' (iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 8: all non-retryable failure types map to 'non_retryable' with retry_eligible=false" {
    local non_retryable_types=("validation_failure" "permission_violation" "repository_error" "scope_violation" "quota_exceeded" "sandbox_failure")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Pick a random non-retryable failure type
        local failure_type
        failure_type=$(random_choice "${non_retryable_types[@]}")

        run classify_failure "$failure_type"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: classify_failure '$failure_type' returned non-zero exit code $status (iteration: $i)" >&2
            return 1
        }

        # Parse output: line 1 = classification, line 2 = retry_eligible
        local classification
        local retry_eligible
        classification=$(echo "$output" | sed -n '1p')
        retry_eligible=$(echo "$output" | sed -n '2p')

        [[ "$classification" == "non_retryable" ]] || {
            echo "FAILED: failure type '$failure_type' classified as '$classification' instead of 'non_retryable' (iteration: $i)" >&2
            return 1
        }

        [[ "$retry_eligible" == "false" ]] || {
            echo "FAILED: failure type '$failure_type' has retry_eligible='$retry_eligible' instead of 'false' (iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 8: classification is deterministic — same input always produces same output" {
    local all_types=("inference_failure" "queue_timeout" "timeout" "validation_failure" "permission_violation" "repository_error" "scope_violation" "quota_exceeded" "sandbox_failure")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Pick a random failure type
        local failure_type
        failure_type=$(random_choice "${all_types[@]}")

        # Call twice and compare
        run classify_failure "$failure_type"
        local first_output="$output"
        local first_status="$status"

        run classify_failure "$failure_type"
        local second_output="$output"
        local second_status="$status"

        [[ "$first_status" -eq "$second_status" ]] || {
            echo "FAILED: classify_failure '$failure_type' returned different exit codes: $first_status vs $second_status (iteration: $i)" >&2
            return 1
        }

        [[ "$first_output" == "$second_output" ]] || {
            echo "FAILED: classify_failure '$failure_type' returned different outputs (iteration: $i)" >&2
            echo "  First:  '$first_output'" >&2
            echo "  Second: '$second_output'" >&2
            return 1
        }
    done
}

@test "Property 8: retry_eligible boolean always matches classification" {
    local all_types=("inference_failure" "queue_timeout" "timeout" "validation_failure" "permission_violation" "repository_error" "scope_violation" "quota_exceeded" "sandbox_failure")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Pick a random failure type
        local failure_type
        failure_type=$(random_choice "${all_types[@]}")

        run classify_failure "$failure_type"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: classify_failure '$failure_type' returned non-zero exit code $status (iteration: $i)" >&2
            return 1
        }

        local classification
        local retry_eligible
        classification=$(echo "$output" | sed -n '1p')
        retry_eligible=$(echo "$output" | sed -n '2p')

        # retry_eligible must be "true" iff classification is "retryable"
        if [[ "$classification" == "retryable" ]]; then
            [[ "$retry_eligible" == "true" ]] || {
                echo "FAILED: classification='retryable' but retry_eligible='$retry_eligible' for '$failure_type' (iteration: $i)" >&2
                return 1
            }
        elif [[ "$classification" == "non_retryable" ]]; then
            [[ "$retry_eligible" == "false" ]] || {
                echo "FAILED: classification='non_retryable' but retry_eligible='$retry_eligible' for '$failure_type' (iteration: $i)" >&2
                return 1
            }
        else
            echo "FAILED: unexpected classification '$classification' for '$failure_type' (iteration: $i)" >&2
            return 1
        fi
    done
}

@test "Property 8: unknown failure types return non-zero exit code" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        # Generate random strings that are NOT valid failure types
        local unknown_type
        unknown_type="unknown_$(random_hex 8)"

        run classify_failure "$unknown_type"
        [[ "$status" -ne 0 ]] || {
            echo "FAILED: classify_failure '$unknown_type' returned exit code 0 for unknown type (iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 8: exhaustive check — every defined failure type is classified correctly" {
    # This test verifies the complete mapping exhaustively (not random)
    local -A expected_classification=(
        [inference_failure]="retryable"
        [queue_timeout]="retryable"
        [timeout]="retryable"
        [validation_failure]="non_retryable"
        [permission_violation]="non_retryable"
        [repository_error]="non_retryable"
        [scope_violation]="non_retryable"
        [quota_exceeded]="non_retryable"
        [sandbox_failure]="non_retryable"
    )
    local -A expected_retry_eligible=(
        [inference_failure]="true"
        [queue_timeout]="true"
        [timeout]="true"
        [validation_failure]="false"
        [permission_violation]="false"
        [repository_error]="false"
        [scope_violation]="false"
        [quota_exceeded]="false"
        [sandbox_failure]="false"
    )

    for failure_type in "${!expected_classification[@]}"; do
        run classify_failure "$failure_type"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: classify_failure '$failure_type' returned non-zero exit code $status" >&2
            return 1
        }

        local classification
        local retry_eligible
        classification=$(echo "$output" | sed -n '1p')
        retry_eligible=$(echo "$output" | sed -n '2p')

        [[ "$classification" == "${expected_classification[$failure_type]}" ]] || {
            echo "FAILED: '$failure_type' classified as '$classification', expected '${expected_classification[$failure_type]}'" >&2
            return 1
        }

        [[ "$retry_eligible" == "${expected_retry_eligible[$failure_type]}" ]] || {
            echo "FAILED: '$failure_type' retry_eligible='$retry_eligible', expected '${expected_retry_eligible[$failure_type]}'" >&2
            return 1
        }
    done
}
