#!/usr/bin/env bats
# Feature: agent-runtime, Property 10: Token Budget Enforcement
# Validates: Requirements 28.1, 28.2

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 10: No violation when all metrics are within limits" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens

        # Generate random limits (positive)
        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # Generate usage values strictly within limits
        prompt_tokens=$(random_number 0 $((max_prompt - 1)))
        completion_tokens=$(random_number 0 $((max_completion - 1)))
        total_tokens=$(random_number 0 $((max_total - 1)))

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected no violation but got exit=$status" >&2
            echo "  prompt=$prompt_tokens/$max_prompt completion=$completion_tokens/$max_completion total=$total_tokens/$max_total" >&2
            echo "  output: $output" >&2
            return 1
        }
    done
}

@test "Property 10: Violation flagged when prompt tokens exceed limit" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens

        # Generate random limits
        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # Prompt tokens exceed limit; others within
        prompt_tokens=$(random_number $((max_prompt + 1)) $((max_prompt + 10000)))
        completion_tokens=$(random_number 0 $((max_completion - 1)))
        total_tokens=$(random_number 0 $((max_total - 1)))

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): expected violation (exit=1) but got exit=$status" >&2
            echo "  prompt=$prompt_tokens/$max_prompt" >&2
            return 1
        }
        [[ "$output" == *"budget_exceeded"* ]] || {
            echo "FAILED (iteration $i): expected 'budget_exceeded' in output but got: $output" >&2
            return 1
        }
        [[ "$output" == *"prompt tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'prompt tokens' in output but got: $output" >&2
            return 1
        }
    done
}

@test "Property 10: Violation flagged when completion tokens exceed limit" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens

        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # Completion tokens exceed limit; others within
        prompt_tokens=$(random_number 0 $((max_prompt - 1)))
        completion_tokens=$(random_number $((max_completion + 1)) $((max_completion + 10000)))
        total_tokens=$(random_number 0 $((max_total - 1)))

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): expected violation (exit=1) but got exit=$status" >&2
            echo "  completion=$completion_tokens/$max_completion" >&2
            return 1
        }
        [[ "$output" == *"budget_exceeded"* ]] || {
            echo "FAILED (iteration $i): expected 'budget_exceeded' in output but got: $output" >&2
            return 1
        }
        [[ "$output" == *"completion tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'completion tokens' in output but got: $output" >&2
            return 1
        }
    done
}

@test "Property 10: Violation flagged when total tokens exceed limit" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens

        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # Total tokens exceed limit; others within
        prompt_tokens=$(random_number 0 $((max_prompt - 1)))
        completion_tokens=$(random_number 0 $((max_completion - 1)))
        total_tokens=$(random_number $((max_total + 1)) $((max_total + 10000)))

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): expected violation (exit=1) but got exit=$status" >&2
            echo "  total=$total_tokens/$max_total" >&2
            return 1
        }
        [[ "$output" == *"budget_exceeded"* ]] || {
            echo "FAILED (iteration $i): expected 'budget_exceeded' in output but got: $output" >&2
            return 1
        }
        [[ "$output" == *"total tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'total tokens' in output but got: $output" >&2
            return 1
        }
    done
}

@test "Property 10: Zero limit means unlimited — no violation regardless of usage" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local prompt_tokens completion_tokens total_tokens

        # Generate arbitrarily large usage values
        prompt_tokens=$(random_number 1 999999)
        completion_tokens=$(random_number 1 999999)
        total_tokens=$(random_number 1 999999)

        # All limits set to 0 (unlimited)
        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" 0 0 0
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): expected no violation with zero limits but got exit=$status" >&2
            echo "  prompt=$prompt_tokens completion=$completion_tokens total=$total_tokens" >&2
            echo "  output: $output" >&2
            return 1
        }
    done
}

@test "Property 10: Multiple metrics can exceed simultaneously — all violations reported" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens

        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # All metrics exceed their limits
        prompt_tokens=$(random_number $((max_prompt + 1)) $((max_prompt + 10000)))
        completion_tokens=$(random_number $((max_completion + 1)) $((max_completion + 10000)))
        total_tokens=$(random_number $((max_total + 1)) $((max_total + 10000)))

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): expected violation (exit=1) but got exit=$status" >&2
            return 1
        }
        # All three violations should be reported
        [[ "$output" == *"prompt tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'prompt tokens' violation in output" >&2
            return 1
        }
        [[ "$output" == *"completion tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'completion tokens' violation in output" >&2
            return 1
        }
        [[ "$output" == *"total tokens"* ]] || {
            echo "FAILED (iteration $i): expected 'total tokens' violation in output" >&2
            return 1
        }
    done
}

@test "Property 10: Violation iff any metric exceeds its limit — random combinations" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local max_prompt max_completion max_total
        local prompt_tokens completion_tokens total_tokens
        local expect_violation=0

        # Generate random limits (positive)
        max_prompt=$(random_number 100 50000)
        max_completion=$(random_number 100 50000)
        max_total=$(random_number 200 100000)

        # Randomly decide whether each metric exceeds its limit
        if (( RANDOM % 2 == 0 )); then
            prompt_tokens=$(random_number $((max_prompt + 1)) $((max_prompt + 10000)))
            expect_violation=1
        else
            prompt_tokens=$(random_number 0 "$max_prompt")
        fi

        if (( RANDOM % 2 == 0 )); then
            completion_tokens=$(random_number $((max_completion + 1)) $((max_completion + 10000)))
            expect_violation=1
        else
            completion_tokens=$(random_number 0 "$max_completion")
        fi

        if (( RANDOM % 2 == 0 )); then
            total_tokens=$(random_number $((max_total + 1)) $((max_total + 10000)))
            expect_violation=1
        else
            total_tokens=$(random_number 0 "$max_total")
        fi

        run check_token_budget "$prompt_tokens" "$completion_tokens" "$total_tokens" \
            "$max_prompt" "$max_completion" "$max_total"

        if (( expect_violation )); then
            [[ "$status" -eq 1 ]] || {
                echo "FAILED (iteration $i): expected violation but got exit=$status" >&2
                echo "  prompt=$prompt_tokens/$max_prompt completion=$completion_tokens/$max_completion total=$total_tokens/$max_total" >&2
                return 1
            }
            [[ "$output" == *"budget_exceeded"* ]] || {
                echo "FAILED (iteration $i): expected 'budget_exceeded' in output" >&2
                return 1
            }
        else
            [[ "$status" -eq 0 ]] || {
                echo "FAILED (iteration $i): expected no violation but got exit=$status" >&2
                echo "  prompt=$prompt_tokens/$max_prompt completion=$completion_tokens/$max_completion total=$total_tokens/$max_total" >&2
                echo "  output: $output" >&2
                return 1
            }
        fi
    done
}
