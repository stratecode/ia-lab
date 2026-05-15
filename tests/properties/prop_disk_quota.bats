#!/usr/bin/env bats
# Feature: agent-runtime, Property 9: Disk Quota Enforcement
# Validates: Requirements 22.1, 22.2, 22.3, 22.4

setup() {
    TESTS_DIR="$(cd "$(dirname "$BATS_TEST_FILENAME")" && pwd)"
    source "${TESTS_DIR}/test_helper.bash"
}

@test "Property 9: quota_exceeded if and only if size > limit (workspace)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local size limit
        size=$(random_number 1 100000)
        limit=$(random_number 1 100000)

        run check_disk_quota "$size" 0 0 "$limit" 0 0

        if (( size > limit )); then
            [[ "$status" -eq 1 ]] || {
                echo "FAILED: workspace size $size > limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
            [[ "$output" == *"quota_exceeded"* ]] || {
                echo "FAILED: workspace size $size > limit $limit but output missing 'quota_exceeded' (iteration: $i)" >&2
                return 1
            }
        else
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: workspace size $size <= limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
        fi
    done
}

@test "Property 9: quota_exceeded if and only if size > limit (artifact)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local size limit
        size=$(random_number 1 100000)
        limit=$(random_number 1 100000)

        run check_disk_quota 0 "$size" 0 0 "$limit" 0

        if (( size > limit )); then
            [[ "$status" -eq 1 ]] || {
                echo "FAILED: artifact size $size > limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
            [[ "$output" == *"quota_exceeded"* ]] || {
                echo "FAILED: artifact size $size > limit $limit but output missing 'quota_exceeded' (iteration: $i)" >&2
                return 1
            }
        else
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: artifact size $size <= limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
        fi
    done
}

@test "Property 9: quota_exceeded if and only if size > limit (log)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local size limit
        size=$(random_number 1 100000)
        limit=$(random_number 1 100000)

        run check_disk_quota 0 0 "$size" 0 0 "$limit"

        if (( size > limit )); then
            [[ "$status" -eq 1 ]] || {
                echo "FAILED: log size $size > limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
            [[ "$output" == *"quota_exceeded"* ]] || {
                echo "FAILED: log size $size > limit $limit but output missing 'quota_exceeded' (iteration: $i)" >&2
                return 1
            }
        else
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: log size $size <= limit $limit but exit code was $status (iteration: $i)" >&2
                return 1
            }
        fi
    done
}

@test "Property 9: checks apply independently for workspace, artifact, and log" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local ws_size art_size log_size ws_limit art_limit log_limit
        ws_size=$(random_number 1 100000)
        art_size=$(random_number 1 100000)
        log_size=$(random_number 1 100000)
        ws_limit=$(random_number 1 100000)
        art_limit=$(random_number 1 100000)
        log_limit=$(random_number 1 100000)

        run check_disk_quota "$ws_size" "$art_size" "$log_size" "$ws_limit" "$art_limit" "$log_limit"

        local ws_exceeded=0 art_exceeded=0 log_exceeded=0
        (( ws_size > ws_limit )) && ws_exceeded=1
        (( art_size > art_limit )) && art_exceeded=1
        (( log_size > log_limit )) && log_exceeded=1

        local any_exceeded=$(( ws_exceeded || art_exceeded || log_exceeded ))

        if (( any_exceeded )); then
            [[ "$status" -eq 1 ]] || {
                echo "FAILED: at least one quota exceeded (ws:$ws_size/$ws_limit art:$art_size/$art_limit log:$log_size/$log_limit) but exit code was $status (iteration: $i)" >&2
                return 1
            }
        else
            [[ "$status" -eq 0 ]] || {
                echo "FAILED: no quota exceeded (ws:$ws_size/$ws_limit art:$art_size/$art_limit log:$log_size/$log_limit) but exit code was $status (iteration: $i)" >&2
                return 1
            }
        fi

        # Verify independent reporting: each exceeded quota should be mentioned
        if (( ws_exceeded )); then
            [[ "$output" == *"workspace size"* ]] || {
                echo "FAILED: workspace exceeded but not reported in output (iteration: $i)" >&2
                return 1
            }
        fi
        if (( art_exceeded )); then
            [[ "$output" == *"artifact size"* ]] || {
                echo "FAILED: artifact exceeded but not reported in output (iteration: $i)" >&2
                return 1
            }
        fi
        if (( log_exceeded )); then
            [[ "$output" == *"log size"* ]] || {
                echo "FAILED: log exceeded but not reported in output (iteration: $i)" >&2
                return 1
            }
        fi
    done
}

@test "Property 9: limit of 0 means unlimited (no enforcement)" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local ws_size art_size log_size
        ws_size=$(random_number 1 999999999)
        art_size=$(random_number 1 999999999)
        log_size=$(random_number 1 999999999)

        # All limits set to 0 means unlimited
        run check_disk_quota "$ws_size" "$art_size" "$log_size" 0 0 0

        [[ "$status" -eq 0 ]] || {
            echo "FAILED: all limits 0 (unlimited) but exit code was $status for sizes ws:$ws_size art:$art_size log:$log_size (iteration: $i)" >&2
            return 1
        }
        [[ -z "$output" ]] || {
            echo "FAILED: all limits 0 (unlimited) but got output: '$output' (iteration: $i)" >&2
            return 1
        }
    done
}

@test "Property 9: size equal to limit does not trigger quota_exceeded" {
    for i in $(seq 1 "$PROPERTY_ITERATIONS"); do
        local limit
        limit=$(random_number 1 100000)

        # Size exactly equals limit — should NOT exceed
        run check_disk_quota "$limit" "$limit" "$limit" "$limit" "$limit" "$limit"

        [[ "$status" -eq 0 ]] || {
            echo "FAILED: size == limit ($limit) should not exceed but exit code was $status (iteration: $i)" >&2
            return 1
        }
    done
}
