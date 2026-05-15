#!/usr/bin/env bats
# Feature: agent-runtime, Property 7: Branch Protection Enforcement
# Validates: Requirements 10.1, 10.2

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

# ─── Helper generators ───────────────────────────────────────────────────────

# random_branch_slug()
# Generates a random valid branch slug segment (lowercase alphanumeric + hyphens)
random_branch_slug() {
    local max_len="${1:-30}"
    local len=$(( (RANDOM % max_len) + 1 ))
    local chars="abcdefghijklmnopqrstuvwxyz0123456789-"
    local result=""
    local i

    for ((i = 0; i < len; i++)); do
        result+="${chars:$(( RANDOM % ${#chars} )):1}"
    done

    # Ensure no leading/trailing hyphens
    result="${result#-}"
    result="${result%-}"

    # Fallback if empty
    if [[ -z "$result" ]]; then
        result="task"
    fi

    printf '%s' "$result"
}

# random_valid_agent_branch()
# Generates a branch name with agent/ prefix that is NOT in the protected list
random_valid_agent_branch() {
    local slug
    slug="$(random_branch_slug 20)"
    local task_id
    task_id="$(random_hex 8)"
    printf 'agent/%s/%s' "$task_id" "$slug"
}

# random_non_agent_branch()
# Generates a branch name WITHOUT the agent/ prefix
random_non_agent_branch() {
    local prefixes=("feature/" "bugfix/" "hotfix/" "release/" "" "main" "master" "develop" "test/")
    local prefix
    prefix=$(random_choice "${prefixes[@]}")
    local slug
    slug="$(random_branch_slug 15)"
    printf '%s%s' "$prefix" "$slug"
}

# ─── Property Tests ──────────────────────────────────────────────────────────

@test "Property 7: Branches with agent/ prefix and not in protected list are accepted" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local branch
        branch="$(random_valid_agent_branch)"

        run validate_branch_name "$branch"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): branch '$branch' should be accepted (has agent/ prefix, not protected), got status=$status output='$output'" >&2
            return 1
        }
        [[ -z "$output" ]] || {
            echo "FAILED (iteration $i): accepted branch should produce no output, got '$output'" >&2
            return 1
        }
    done
}

@test "Property 7: Branches without agent/ prefix are rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local branch
        branch="$(random_non_agent_branch)"

        # Ensure the branch does NOT start with agent/
        if [[ "$branch" == agent/* ]]; then
            continue
        fi

        run validate_branch_name "$branch"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): branch '$branch' should be rejected (no agent/ prefix), got status=$status" >&2
            return 1
        }
        [[ "$output" == *"branch_error"* ]] || {
            echo "FAILED (iteration $i): expected 'branch_error' in output for '$branch', got '$output'" >&2
            return 1
        }
        [[ "$output" == *'does not start with required prefix "agent/"'* ]] || {
            echo "FAILED (iteration $i): expected prefix error message for '$branch', got '$output'" >&2
            return 1
        }
    done
}

@test "Property 7: Protected branch names are rejected even with agent/ prefix if they match exactly" {
    # The protected branches list is checked against the FULL branch name.
    # Default: main,master,develop
    # So "agent/main" != "main" — it would NOT be rejected by the protected check.
    # Only exact matches like the branch literally being "main" would be rejected,
    # but "main" would already fail the agent/ prefix check.
    # This test verifies that if we add "agent/protected" to the protected list,
    # it gets rejected.
    local protected_entries=("agent/secret" "agent/deploy" "agent/release")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local entry
        entry=$(random_choice "${protected_entries[@]}")

        # Set custom protected branches that include agent/-prefixed entries
        export AIDER_PROTECTED_BRANCHES="main,master,develop,${entry}"

        run validate_branch_name "$entry"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): branch '$entry' should be rejected (in protected list), got status=$status" >&2
            return 1
        }
        [[ "$output" == *"branch_error"* ]] || {
            echo "FAILED (iteration $i): expected 'branch_error' in output for protected branch '$entry', got '$output'" >&2
            return 1
        }
        [[ "$output" == *"protected branches list"* ]] || {
            echo "FAILED (iteration $i): expected 'protected branches list' message for '$entry', got '$output'" >&2
            return 1
        }
    done

    # Restore default
    export AIDER_PROTECTED_BRANCHES="main,master,develop"
}

@test "Property 7: Default protected branches (main, master, develop) are rejected" {
    local protected=("main" "master" "develop")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local branch
        branch=$(random_choice "${protected[@]}")

        run validate_branch_name "$branch"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): branch '$branch' should be rejected (no agent/ prefix and/or protected), got status=$status" >&2
            return 1
        }
        # These fail because they don't have agent/ prefix
        [[ "$output" == *"branch_error"* ]] || {
            echo "FAILED (iteration $i): expected 'branch_error' in output for '$branch', got '$output'" >&2
            return 1
        }
    done
}

@test "Property 7: Random branch names — accept only agent/ prefix AND not in protected list" {
    local all_branches=()
    local protected_branches_arr=("main" "master" "develop")

    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local branch
        local strategy=$(( RANDOM % 4 ))

        case $strategy in
            0)
                # Valid: agent/ prefix, not protected
                branch="$(random_valid_agent_branch)"
                ;;
            1)
                # Invalid: no agent/ prefix
                branch="$(random_non_agent_branch)"
                # Make sure it doesn't accidentally start with agent/
                while [[ "$branch" == agent/* ]]; do
                    branch="$(random_non_agent_branch)"
                done
                ;;
            2)
                # Invalid: bare protected branch name (no agent/ prefix)
                branch=$(random_choice "${protected_branches_arr[@]}")
                ;;
            3)
                # Valid: agent/ prefix with protected name as part of path
                # e.g., "agent/abc123/main-feature" — this should be accepted
                # because the full branch name doesn't match "main" exactly
                local protected
                protected=$(random_choice "${protected_branches_arr[@]}")
                branch="agent/$(random_hex 6)/${protected}-$(random_branch_slug 8)"
                ;;
        esac

        run validate_branch_name "$branch"

        # Determine expected result
        local should_accept=0

        # Must start with agent/
        if [[ "$branch" != agent/* ]]; then
            should_accept=1
        fi

        # Must not be in protected list (exact match)
        if [[ "$should_accept" -eq 0 ]]; then
            local IFS=','
            local protected
            for protected in $AIDER_PROTECTED_BRANCHES; do
                protected="${protected#"${protected%%[![:space:]]*}"}"
                protected="${protected%"${protected##*[![:space:]]}"}"
                if [[ "$branch" == "$protected" ]]; then
                    should_accept=1
                    break
                fi
            done
        fi

        if [[ "$should_accept" -eq 0 ]]; then
            # Should be accepted
            [[ "$status" -eq 0 ]] || {
                echo "FAILED (iteration $i): branch '$branch' should be accepted, got status=$status output='$output'" >&2
                return 1
            }
        else
            # Should be rejected
            [[ "$status" -eq 1 ]] || {
                echo "FAILED (iteration $i): branch '$branch' should be rejected, got status=$status" >&2
                return 1
            }
            [[ "$output" == *"branch_error"* ]] || {
                echo "FAILED (iteration $i): expected 'branch_error' for rejected branch '$branch', got '$output'" >&2
                return 1
            }
        fi
    done
}

@test "Property 7: Empty branch name is rejected" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        run validate_branch_name ""
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): empty branch name should be rejected, got status=$status" >&2
            return 1
        }
        [[ "$output" == *"branch_error"* ]] || {
            echo "FAILED (iteration $i): expected 'branch_error' for empty branch, got '$output'" >&2
            return 1
        }
    done
}

@test "Property 7: Custom protected branches list is enforced" {
    # Test with a custom protected branches list that includes agent/-prefixed entries
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local custom_protected="agent/release-v1,agent/hotfix-prod,staging"
        export AIDER_PROTECTED_BRANCHES="$custom_protected"

        # These should be rejected (exact match in protected list)
        local rejected_branches=("agent/release-v1" "agent/hotfix-prod")
        local rejected
        rejected=$(random_choice "${rejected_branches[@]}")

        run validate_branch_name "$rejected"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): branch '$rejected' should be rejected (custom protected list), got status=$status" >&2
            return 1
        }

        # "staging" should be rejected because it lacks agent/ prefix
        run validate_branch_name "staging"
        [[ "$status" -eq 1 ]] || {
            echo "FAILED (iteration $i): 'staging' should be rejected (no agent/ prefix), got status=$status" >&2
            return 1
        }

        # A valid agent/ branch not in the custom list should be accepted
        local valid_branch="agent/$(random_hex 8)/my-feature"
        run validate_branch_name "$valid_branch"
        [[ "$status" -eq 0 ]] || {
            echo "FAILED (iteration $i): branch '$valid_branch' should be accepted (not in custom protected list), got status=$status output='$output'" >&2
            return 1
        }
    done

    # Restore default
    export AIDER_PROTECTED_BRANCHES="main,master,develop"
}
