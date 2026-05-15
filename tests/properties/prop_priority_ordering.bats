#!/usr/bin/env bats
# Feature: agent-runtime, Property 11: Priority Queue Ordering
# Validates: Requirements 29.1, 29.2, 29.4

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 11: critical > high > normal > low — strict ordering between all levels" {
    # Define the priority levels in descending order
    local -a priorities=("critical" "high" "normal" "low")

    # For every pair where i < j, priorities[i] should be "higher" than priorities[j]
    for ((i = 0; i < ${#priorities[@]}; i++)); do
        for ((j = i + 1; j < ${#priorities[@]}; j++)); do
            local higher="${priorities[$i]}"
            local lower="${priorities[$j]}"

            # higher vs lower should return "higher"
            run compare_priority "$higher" "$lower"
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: non-zero exit for compare_priority('$higher', '$lower')" >&2
                return 1
            }
            [[ "$output" == "higher" ]] || {
                echo "FAILED: expected 'higher' for compare_priority('$higher', '$lower') but got '$output'" >&2
                return 1
            }

            # lower vs higher should return "lower"
            run compare_priority "$lower" "$higher"
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: non-zero exit for compare_priority('$lower', '$higher')" >&2
                return 1
            }
            [[ "$output" == "lower" ]] || {
                echo "FAILED: expected 'lower' for compare_priority('$lower', '$higher') but got '$output'" >&2
                return 1
            }
        done
    done
}

@test "Property 11: same priority returns equal — FIFO within same level" {
    local -a priorities=("critical" "high" "normal" "low")

    for priority in "${priorities[@]}"; do
        run compare_priority "$priority" "$priority"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: non-zero exit for compare_priority('$priority', '$priority')" >&2
            return 1
        }
        [[ "$output" == "equal" ]] || {
            echo "FAILED: expected 'equal' for compare_priority('$priority', '$priority') but got '$output'" >&2
            return 1
        }
    done
}

@test "Property 11: transitivity — if a > b and b > c then a > c" {
    local -a priorities=("critical" "high" "normal" "low")

    for ((a = 0; a < ${#priorities[@]}; a++)); do
        for ((b = a + 1; b < ${#priorities[@]}; b++)); do
            for ((c = b + 1; c < ${#priorities[@]}; c++)); do
                local pa="${priorities[$a]}"
                local pb="${priorities[$b]}"
                local pc="${priorities[$c]}"

                # pa > pb
                run compare_priority "$pa" "$pb"
                [[ "$output" == "higher" ]] || {
                    echo "FAILED: transitivity broken — expected '$pa' > '$pb' but got '$output'" >&2
                    return 1
                }

                # pb > pc
                run compare_priority "$pb" "$pc"
                [[ "$output" == "higher" ]] || {
                    echo "FAILED: transitivity broken — expected '$pb' > '$pc' but got '$output'" >&2
                    return 1
                }

                # pa > pc (transitive)
                run compare_priority "$pa" "$pc"
                [[ "$output" == "higher" ]] || {
                    echo "FAILED: transitivity broken — expected '$pa' > '$pc' (transitive) but got '$output'" >&2
                    return 1
                }
            done
        done
    done
}

@test "Property 11: antisymmetry — if a > b then b < a, always" {
    local -a priorities=("critical" "high" "normal" "low")

    for ((i = 0; i < ${#priorities[@]}; i++)); do
        for ((j = 0; j < ${#priorities[@]}; j++)); do
            local pa="${priorities[$i]}"
            local pb="${priorities[$j]}"

            run compare_priority "$pa" "$pb"
            local forward="$output"

            run compare_priority "$pb" "$pa"
            local reverse="$output"

            if [[ "$forward" == "higher" ]]; then
                [[ "$reverse" == "lower" ]] || {
                    echo "FAILED: antisymmetry broken — compare('$pa','$pb')='higher' but compare('$pb','$pa')='$reverse'" >&2
                    return 1
                }
            elif [[ "$forward" == "lower" ]]; then
                [[ "$reverse" == "higher" ]] || {
                    echo "FAILED: antisymmetry broken — compare('$pa','$pb')='lower' but compare('$pb','$pa')='$reverse'" >&2
                    return 1
                }
            elif [[ "$forward" == "equal" ]]; then
                [[ "$reverse" == "equal" ]] || {
                    echo "FAILED: antisymmetry broken — compare('$pa','$pb')='equal' but compare('$pb','$pa')='$reverse'" >&2
                    return 1
                }
            fi
        done
    done
}

@test "Property 11: random priority pairs — ordering holds for 100+ random comparisons" {
    local -a priorities=("critical" "high" "normal" "low")
    local -A priority_rank=( [critical]=4 [high]=3 [normal]=2 [low]=1 )

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local pa pb
        pa=$(random_choice "${priorities[@]}")
        pb=$(random_choice "${priorities[@]}")

        local rank_a="${priority_rank[$pa]}"
        local rank_b="${priority_rank[$pb]}"

        # Determine expected result
        local expected
        if (( rank_a > rank_b )); then
            expected="higher"
        elif (( rank_a < rank_b )); then
            expected="lower"
        else
            expected="equal"
        fi

        run compare_priority "$pa" "$pb"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: non-zero exit (iteration: $i, a='$pa', b='$pb')" >&2
            return 1
        }
        [[ "$output" == "$expected" ]] || {
            echo "FAILED: expected '$expected' for compare_priority('$pa', '$pb') but got '$output' (iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 11: FIFO semantics — equal priority means arrival order decides (compare returns equal)" {
    # When two tasks have the same priority, compare_priority returns "equal"
    # which signals the queue to use FIFO (arrival time) as tiebreaker.
    # We verify this by checking that for any priority level, comparing it
    # with itself always yields "equal" across many iterations.
    local -a priorities=("critical" "high" "normal" "low")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local p
        p=$(random_choice "${priorities[@]}")

        run compare_priority "$p" "$p"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: non-zero exit for same-priority comparison (iteration: $i, priority='$p')" >&2
            return 1
        }
        [[ "$output" == "equal" ]] || {
            echo "FAILED: same priority '$p' should return 'equal' but got '$output' (iteration: $i)" >&2
            return 1
        }
    done
}
