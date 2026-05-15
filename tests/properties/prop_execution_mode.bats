#!/usr/bin/env bats
# Feature: agent-runtime, Property 5: Execution Mode Precedence
# Validates: Requirements 6.1, 19.5, 19.6, 19.7

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 5: CLI mode overrides all — when CLI is set, it always wins regardless of repo/global" {
    local modes=("autonomous" "supervised" "dry-run")

    for cli_mode in "${modes[@]}"; do
        for repo_mode in "${modes[@]}" ""; do
            for global_mode in "${modes[@]}"; do
                run resolve_execution_mode "$cli_mode" "$repo_mode" "$global_mode"
                [[ "$status" -eq 0 ]] || {
                    echo "FAILED: non-zero exit for cli='$cli_mode' repo='$repo_mode' global='$global_mode'" >&2
                    return 1
                }
                [[ "$output" == "$cli_mode" ]] || {
                    echo "FAILED: expected '$cli_mode' but got '$output' (cli='$cli_mode' repo='$repo_mode' global='$global_mode')" >&2
                    return 1
                }
            done
        done
    done
}

@test "Property 5: Repo mode overrides global — when CLI is empty but repo is set, repo wins" {
    local modes=("autonomous" "supervised" "dry-run")

    for repo_mode in "${modes[@]}"; do
        for global_mode in "${modes[@]}"; do
            run resolve_execution_mode "" "$repo_mode" "$global_mode"
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: non-zero exit for cli='' repo='$repo_mode' global='$global_mode'" >&2
                return 1
            }
            [[ "$output" == "$repo_mode" ]] || {
                echo "FAILED: expected '$repo_mode' but got '$output' (cli='' repo='$repo_mode' global='$global_mode')" >&2
                return 1
            }
        done
    done
}

@test "Property 5: Global is fallback — when CLI and repo are empty, global is used" {
    local modes=("autonomous" "supervised" "dry-run")

    for global_mode in "${modes[@]}"; do
        run resolve_execution_mode "" "" "$global_mode"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: non-zero exit for cli='' repo='' global='$global_mode'" >&2
            return 1
        }
        [[ "$output" == "$global_mode" ]] || {
            echo "FAILED: expected '$global_mode' but got '$output' (cli='' repo='' global='$global_mode')" >&2
            return 1
        }
    done
}

@test "Property 5: All levels empty — returns error when no mode is set at any level" {
    run resolve_execution_mode "" "" ""
    [[ "$status" -eq 1 ]] || {
        echo "FAILED: expected exit code 1 when all levels empty, got $status" >&2
        return 1
    }
    [[ "$output" == *"no execution mode resolved"* ]] || {
        echo "FAILED: expected error message about no mode resolved, got '$output'" >&2
        return 1
    }
}

@test "Property 5: Invalid mode at winning level — returns error for unrecognized modes" {
    local invalid_modes=("auto" "manual" "AUTONOMOUS" "Supervised" "dryrun" "dry_run" "none" "")
    local valid_modes=("autonomous" "supervised" "dry-run")

    for invalid in "${invalid_modes[@]}"; do
        [[ -z "$invalid" ]] && continue
        # Invalid at CLI level (highest precedence)
        run resolve_execution_mode "$invalid" "autonomous" "supervised"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED: expected exit 1 for invalid CLI mode '$invalid', got $status" >&2
            return 1
        }
        [[ "$output" == *"invalid execution mode"* ]] || {
            echo "FAILED: expected invalid mode error for '$invalid', got '$output'" >&2
            return 1
        }
    done

    for invalid in "${invalid_modes[@]}"; do
        [[ -z "$invalid" ]] && continue
        # Invalid at repo level (CLI empty)
        run resolve_execution_mode "" "$invalid" "autonomous"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED: expected exit 1 for invalid repo mode '$invalid', got $status" >&2
            return 1
        }
    done

    for invalid in "${invalid_modes[@]}"; do
        [[ -z "$invalid" ]] && continue
        # Invalid at global level (CLI and repo empty)
        run resolve_execution_mode "" "" "$invalid"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED: expected exit 1 for invalid global mode '$invalid', got $status" >&2
            return 1
        }
    done
}

@test "Property 5: Random mode combinations — precedence CLI > repo > global holds for 100+ iterations" {
    local modes=("autonomous" "supervised" "dry-run")
    local all_options=("autonomous" "supervised" "dry-run" "")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local cli_mode repo_mode global_mode expected

        # Pick random values (including "" for unset)
        cli_mode=$(random_choice "${all_options[@]}")
        repo_mode=$(random_choice "${all_options[@]}")
        # Global must be a valid mode (at least one level should resolve)
        global_mode=$(random_choice "${modes[@]}")

        # Determine expected result based on precedence
        if [[ -n "$cli_mode" ]]; then
            expected="$cli_mode"
        elif [[ -n "$repo_mode" ]]; then
            expected="$repo_mode"
        else
            expected="$global_mode"
        fi

        run resolve_execution_mode "$cli_mode" "$repo_mode" "$global_mode"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED: non-zero exit (iteration: $i, cli='$cli_mode' repo='$repo_mode' global='$global_mode')" >&2
            return 1
        }
        [[ "$output" == "$expected" ]] || {
            echo "FAILED: expected '$expected' but got '$output' (iteration: $i, cli='$cli_mode' repo='$repo_mode' global='$global_mode')" >&2
            return 1
        }
    done
}

@test "Property 5: Random combinations with invalid modes — invalid at winning level always errors" {
    local valid_modes=("autonomous" "supervised" "dry-run")
    local invalid_modes=("auto" "manual" "AUTONOMOUS" "Supervised" "dryrun" "dry_run" "none" "unknown" "exec")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local cli_mode repo_mode global_mode
        local invalid_mode

        invalid_mode=$(random_choice "${invalid_modes[@]}")

        # Strategy: randomly place the invalid mode at the winning level
        local strategy=$(( RANDOM % 3 ))

        case $strategy in
            0)
                # Invalid at CLI level — always wins, so always errors
                cli_mode="$invalid_mode"
                repo_mode=$(random_choice "${valid_modes[@]}" "")
                global_mode=$(random_choice "${valid_modes[@]}")
                ;;
            1)
                # Invalid at repo level, CLI empty — repo wins, so errors
                cli_mode=""
                repo_mode="$invalid_mode"
                global_mode=$(random_choice "${valid_modes[@]}")
                ;;
            2)
                # Invalid at global level, CLI and repo empty — global wins, so errors
                cli_mode=""
                repo_mode=""
                global_mode="$invalid_mode"
                ;;
        esac

        run resolve_execution_mode "$cli_mode" "$repo_mode" "$global_mode"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED: expected exit 1 for invalid mode (iteration: $i, strategy=$strategy, cli='$cli_mode' repo='$repo_mode' global='$global_mode')" >&2
            return 1
        }
        [[ "$output" == *"invalid execution mode"* ]] || {
            echo "FAILED: expected 'invalid execution mode' error (iteration: $i, got '$output')" >&2
            return 1
        }
    done
}
