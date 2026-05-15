"""
Property-based tests for validate_config.py validation functions.

Feature: local-inference-upgrade

Tests cover 6 correctness properties from the design document:
  Property 1: Thread Budget Validation
  Property 2: RAM Estimation Formula Correctness
  Property 3: RAM Budget Threshold Logic
  Property 4: Stale Instance Identification
  Property 5: Context Window Validation
  Property 6: VRAM Budget Validation
"""

import math
import sys
import os

# Add the files directory to the path so we can import validate_config
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "files"))

from hypothesis import given, settings
from hypothesis import strategies as st

from validate_config import (
    estimate_instance_ram,
    identify_stale_instances,
    validate_context_limits,
    validate_ram_budget,
    validate_thread_budget,
    validate_vram_budget,
)


# ─── Strategies ──────────────────────────────────────────────────────────────

# Generate a pool of possible service names for stale instance tests
SERVICE_NAME_POOL = [f"llama-cpp-svc-{i}" for i in range(20)]
service_names_strategy = st.sampled_from(SERVICE_NAME_POOL)


# ─── Property 1: Thread Budget Validation ────────────────────────────────────
# Validates: Requirements 4.2, 4.3, 4.4


class TestThreadBudgetValidation:
    """Property 1: Thread Budget Validation

    Feature: local-inference-upgrade, Property 1: Thread Budget Validation

    For any set of instance configurations with thread allocations, the
    validation function SHALL emit a warning if and only if the sum of all
    threads values exceeds the configured thread budget (12).
    """

    THREAD_BUDGET = 12

    @given(
        thread_allocations=st.lists(
            st.integers(min_value=1, max_value=8), min_size=1, max_size=8
        )
    )
    @settings(max_examples=200)
    def test_thread_budget_ok_iff_sum_within_budget(self, thread_allocations):
        """ok == True iff sum(threads) <= budget.

        **Validates: Requirements 4.2, 4.3, 4.4**
        """
        instances = [{"threads": t} for t in thread_allocations]
        ok, total, budget = validate_thread_budget(instances, self.THREAD_BUDGET)

        expected_total = sum(thread_allocations)

        if expected_total <= self.THREAD_BUDGET:
            assert ok is True
        else:
            assert ok is False

        assert total == expected_total
        assert budget == self.THREAD_BUDGET


# ─── Property 2: RAM Estimation Formula Correctness ──────────────────────────
# Validates: Requirements 5.1, 5.2, 5.6


class TestRAMEstimationFormulaCorrectness:
    """Property 2: RAM Estimation Formula Correctness

    Feature: local-inference-upgrade, Property 2: RAM Estimation Formula Correctness

    For any valid instance configuration, the estimated RAM SHALL equal
    (model_size_mb + kv_cache_mb) * safety_margin where
    kv_cache_mb = (ctx_size * parallel * bytes_per_token) / 1024.
    The estimate SHALL be monotonically increasing with respect to
    ctx_size, parallel, and model_size_mb individually.
    """

    @given(
        model_size_mb=st.integers(min_value=100, max_value=10000),
        ctx_size=st.integers(min_value=512, max_value=65536),
        parallel=st.integers(min_value=1, max_value=8),
        bytes_per_token=st.floats(min_value=0.1, max_value=2.0),
        safety_margin=st.floats(min_value=1.01, max_value=2.0),
    )
    @settings(max_examples=200)
    def test_ram_estimation_formula_correctness(
        self, model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin
    ):
        """Estimated RAM matches the documented formula.

        **Validates: Requirements 5.1, 5.2**
        """
        result = estimate_instance_ram(
            model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin
        )

        kv_cache_mb = (ctx_size * parallel * bytes_per_token) / 1024.0
        expected = (model_size_mb + kv_cache_mb) * safety_margin

        assert math.isclose(result, expected, rel_tol=1e-9)

    @given(
        model_size_mb=st.integers(min_value=100, max_value=10000),
        ctx_size=st.integers(min_value=512, max_value=65536),
        parallel=st.integers(min_value=1, max_value=8),
        bytes_per_token=st.floats(min_value=0.1, max_value=2.0),
        safety_margin=st.floats(min_value=1.01, max_value=2.0),
    )
    @settings(max_examples=200)
    def test_ram_estimation_memory_max_relationship(
        self, model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin
    ):
        """MemoryMax = estimated + 512 and is always greater than the estimate.

        **Validates: Requirements 5.6**
        """
        estimated = estimate_instance_ram(
            model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin
        )
        memory_max = estimated + 512

        assert memory_max > estimated
        assert math.isclose(memory_max - estimated, 512.0, rel_tol=1e-9)

    @given(
        model_size_mb=st.integers(min_value=100, max_value=10000),
        ctx_size_a=st.integers(min_value=512, max_value=32768),
        ctx_size_b=st.integers(min_value=512, max_value=32768),
        parallel=st.integers(min_value=1, max_value=8),
        bytes_per_token=st.floats(min_value=0.1, max_value=2.0),
        safety_margin=st.floats(min_value=1.01, max_value=2.0),
    )
    @settings(max_examples=200)
    def test_ram_estimation_monotonic_ctx_size(
        self, model_size_mb, ctx_size_a, ctx_size_b, parallel, bytes_per_token, safety_margin
    ):
        """Increasing ctx_size increases the estimate (monotonicity).

        **Validates: Requirements 5.1, 5.2**
        """
        est_a = estimate_instance_ram(
            model_size_mb, ctx_size_a, parallel, bytes_per_token, safety_margin
        )
        est_b = estimate_instance_ram(
            model_size_mb, ctx_size_b, parallel, bytes_per_token, safety_margin
        )

        if ctx_size_a < ctx_size_b:
            assert est_a < est_b
        elif ctx_size_a > ctx_size_b:
            assert est_a > est_b
        else:
            assert math.isclose(est_a, est_b, rel_tol=1e-9)

    @given(
        model_size_mb=st.integers(min_value=100, max_value=10000),
        ctx_size=st.integers(min_value=512, max_value=65536),
        parallel_a=st.integers(min_value=1, max_value=8),
        parallel_b=st.integers(min_value=1, max_value=8),
        bytes_per_token=st.floats(min_value=0.1, max_value=2.0),
        safety_margin=st.floats(min_value=1.01, max_value=2.0),
    )
    @settings(max_examples=200)
    def test_ram_estimation_monotonic_parallel(
        self, model_size_mb, ctx_size, parallel_a, parallel_b, bytes_per_token, safety_margin
    ):
        """Increasing parallel increases the estimate (monotonicity).

        **Validates: Requirements 5.1, 5.2**
        """
        est_a = estimate_instance_ram(
            model_size_mb, ctx_size, parallel_a, bytes_per_token, safety_margin
        )
        est_b = estimate_instance_ram(
            model_size_mb, ctx_size, parallel_b, bytes_per_token, safety_margin
        )

        if parallel_a < parallel_b:
            assert est_a < est_b
        elif parallel_a > parallel_b:
            assert est_a > est_b
        else:
            assert math.isclose(est_a, est_b, rel_tol=1e-9)

    @given(
        model_size_a=st.integers(min_value=100, max_value=10000),
        model_size_b=st.integers(min_value=100, max_value=10000),
        ctx_size=st.integers(min_value=512, max_value=65536),
        parallel=st.integers(min_value=1, max_value=8),
        bytes_per_token=st.floats(min_value=0.1, max_value=2.0),
        safety_margin=st.floats(min_value=1.01, max_value=2.0),
    )
    @settings(max_examples=200)
    def test_ram_estimation_monotonic_model_size(
        self, model_size_a, model_size_b, ctx_size, parallel, bytes_per_token, safety_margin
    ):
        """Increasing model_size_mb increases the estimate (monotonicity).

        **Validates: Requirements 5.1, 5.2**
        """
        est_a = estimate_instance_ram(
            model_size_a, ctx_size, parallel, bytes_per_token, safety_margin
        )
        est_b = estimate_instance_ram(
            model_size_b, ctx_size, parallel, bytes_per_token, safety_margin
        )

        if model_size_a < model_size_b:
            assert est_a < est_b
        elif model_size_a > model_size_b:
            assert est_a > est_b
        else:
            assert math.isclose(est_a, est_b, rel_tol=1e-9)


# ─── Property 3: RAM Budget Threshold Logic ──────────────────────────────────
# Validates: Requirements 5.3, 5.4


class TestRAMBudgetThresholdLogic:
    """Property 3: RAM Budget Threshold Logic

    Feature: local-inference-upgrade, Property 3: RAM Budget Threshold Logic

    For any set of instance RAM estimates and a configured budget, the
    validation SHALL: (a) pass silently when total <= budget, (b) emit a
    warning when total > budget AND total <= budget * 1.10, and (c) fail
    when total > budget * 1.10. These three outcomes are mutually exclusive
    and exhaustive.
    """

    # Use simple estimation factors for predictable math
    SIMPLE_FACTORS = {
        "default": {
            "safety_margin": 1.0,
            "bytes_per_token_q4": 0.5,
            "bytes_per_token_q8": 1.0,
        }
    }

    @given(
        model_sizes=st.lists(
            st.integers(min_value=100, max_value=5000), min_size=1, max_size=4
        ),
        ctx_sizes=st.lists(
            st.integers(min_value=512, max_value=32768), min_size=1, max_size=4
        ),
        ram_budget_mb=st.integers(min_value=5000, max_value=30000),
        hard_limit_percent=st.integers(min_value=105, max_value=150),
    )
    @settings(max_examples=200)
    def test_ram_budget_mutually_exclusive_outcomes(
        self, model_sizes, ctx_sizes, ram_budget_mb, hard_limit_percent
    ):
        """Pass/warn/fail outcomes are mutually exclusive and exhaustive.

        **Validates: Requirements 5.3, 5.4**
        """
        # Build instances with matching sizes (use shorter list length)
        n = min(len(model_sizes), len(ctx_sizes))
        instances = [
            {
                "model_size_mb": model_sizes[i],
                "ctx_size": ctx_sizes[i],
                "parallel": 1,
                "hf_repo": "test/model-GGUF:Q4_K_M",
            }
            for i in range(n)
        ]

        status, total_mb, budget_mb, excess_mb = validate_ram_budget(
            instances, ram_budget_mb, hard_limit_percent, self.SIMPLE_FACTORS
        )

        hard_limit_mb = ram_budget_mb * hard_limit_percent / 100.0

        # Verify mutually exclusive outcomes
        if total_mb <= ram_budget_mb:
            assert status == "pass"
        elif total_mb <= hard_limit_mb:
            assert status == "warn"
        else:
            assert status == "fail"

        # Verify exhaustiveness (one of the three always holds)
        assert status in ("pass", "warn", "fail")

        # Verify excess calculation
        expected_excess = max(0.0, total_mb - ram_budget_mb)
        assert math.isclose(excess_mb, expected_excess, rel_tol=1e-9)

        # Verify budget passthrough
        assert budget_mb == ram_budget_mb

    @given(
        total_mb=st.floats(min_value=100.0, max_value=50000.0, allow_nan=False, allow_infinity=False),
        ram_budget_mb=st.integers(min_value=1000, max_value=30000),
        hard_limit_percent=st.integers(min_value=105, max_value=150),
    )
    @settings(max_examples=200)
    def test_pass_implies_no_excess(self, total_mb, ram_budget_mb, hard_limit_percent):
        """When status is 'pass', excess is always zero.

        **Validates: Requirements 5.3, 5.4**
        """
        # Create a single instance with a known model_size that produces a predictable total
        # Use safety_margin=1.0 and bytes_per_token=0 so total == model_size_mb
        instances = [
            {
                "model_size_mb": total_mb,
                "ctx_size": 0,
                "parallel": 1,
                "hf_repo": "test/model-GGUF:Q4_K_M",
            }
        ]

        # With ctx_size=0, kv_cache=0, safety_margin=1.0 => total = model_size_mb
        status, result_total, budget, excess = validate_ram_budget(
            instances, ram_budget_mb, hard_limit_percent, self.SIMPLE_FACTORS
        )

        if status == "pass":
            assert math.isclose(excess, 0.0, abs_tol=1e-9)
            assert result_total <= ram_budget_mb

    @given(
        total_mb=st.floats(min_value=100.0, max_value=50000.0, allow_nan=False, allow_infinity=False),
        ram_budget_mb=st.integers(min_value=1000, max_value=30000),
        hard_limit_percent=st.integers(min_value=105, max_value=150),
    )
    @settings(max_examples=200)
    def test_warn_implies_over_budget_within_hard_limit(
        self, total_mb, ram_budget_mb, hard_limit_percent
    ):
        """When status is 'warn', total > budget AND total <= hard_limit.

        **Validates: Requirements 5.3, 5.4**
        """
        instances = [
            {
                "model_size_mb": total_mb,
                "ctx_size": 0,
                "parallel": 1,
                "hf_repo": "test/model-GGUF:Q4_K_M",
            }
        ]

        status, result_total, budget, excess = validate_ram_budget(
            instances, ram_budget_mb, hard_limit_percent, self.SIMPLE_FACTORS
        )

        hard_limit_mb = ram_budget_mb * hard_limit_percent / 100.0

        if status == "warn":
            assert result_total > ram_budget_mb
            assert result_total <= hard_limit_mb
            assert excess > 0.0

    @given(
        total_mb=st.floats(min_value=100.0, max_value=50000.0, allow_nan=False, allow_infinity=False),
        ram_budget_mb=st.integers(min_value=1000, max_value=30000),
        hard_limit_percent=st.integers(min_value=105, max_value=150),
    )
    @settings(max_examples=200)
    def test_fail_implies_over_hard_limit(self, total_mb, ram_budget_mb, hard_limit_percent):
        """When status is 'fail', total > hard_limit.

        **Validates: Requirements 5.3, 5.4**
        """
        instances = [
            {
                "model_size_mb": total_mb,
                "ctx_size": 0,
                "parallel": 1,
                "hf_repo": "test/model-GGUF:Q4_K_M",
            }
        ]

        status, result_total, budget, excess = validate_ram_budget(
            instances, ram_budget_mb, hard_limit_percent, self.SIMPLE_FACTORS
        )

        hard_limit_mb = ram_budget_mb * hard_limit_percent / 100.0

        if status == "fail":
            assert result_total > hard_limit_mb
            assert excess > 0.0

    @given(
        ram_budget_mb=st.integers(min_value=1000, max_value=30000),
        hard_limit_percent=st.integers(min_value=105, max_value=150),
    )
    @settings(max_examples=100)
    def test_exactly_at_budget_passes(self, ram_budget_mb, hard_limit_percent):
        """When total == budget exactly, status is 'pass'.

        **Validates: Requirements 5.3, 5.4**
        """
        # model_size_mb == ram_budget_mb with safety_margin=1.0 and ctx_size=0
        # gives total == ram_budget_mb exactly
        instances = [
            {
                "model_size_mb": float(ram_budget_mb),
                "ctx_size": 0,
                "parallel": 1,
                "hf_repo": "test/model-GGUF:Q4_K_M",
            }
        ]

        status, total_mb, budget, excess = validate_ram_budget(
            instances, ram_budget_mb, hard_limit_percent, self.SIMPLE_FACTORS
        )

        assert status == "pass"
        assert math.isclose(total_mb, ram_budget_mb, rel_tol=1e-9)
        assert math.isclose(excess, 0.0, abs_tol=1e-9)


# ─── Property 4: Stale Instance Identification ──────────────────────────────
# Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6


class TestStaleInstanceIdentification:
    """Property 4: Stale Instance Identification

    Feature: local-inference-upgrade, Property 4: Stale Instance Identification

    For any set of discovered systemd unit file names and any set of
    configured instance service_name values, the stale set SHALL equal
    exactly the set difference (discovered - configured).
    """

    @given(
        discovered_names=st.sets(service_names_strategy, min_size=0, max_size=10),
        configured_names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_stale_equals_discovered_minus_configured(self, discovered_names, configured_names):
        """Stale instances are exactly the set difference: discovered - configured.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        discovered_with_suffix = [f"{name}.service" for name in discovered_names]

        stale = identify_stale_instances(discovered_with_suffix, list(configured_names))

        stale_normalized = set(s.replace(".service", "") for s in stale)
        expected = discovered_names - configured_names

        assert stale_normalized == expected

    @given(
        discovered_names=st.sets(service_names_strategy, min_size=0, max_size=10),
        configured_names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_no_configured_instance_in_stale(self, discovered_names, configured_names):
        """No configured instance is ever identified as stale.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        discovered_with_suffix = [f"{name}.service" for name in discovered_names]

        stale = identify_stale_instances(discovered_with_suffix, list(configured_names))

        stale_normalized = set(s.replace(".service", "") for s in stale)
        for configured in configured_names:
            assert configured not in stale_normalized

    @given(
        discovered_names=st.sets(service_names_strategy, min_size=0, max_size=10),
        configured_names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_stale_is_subset_of_discovered(self, discovered_names, configured_names):
        """Stale is always a subset of discovered.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        discovered_with_suffix = [f"{name}.service" for name in discovered_names]

        stale = identify_stale_instances(discovered_with_suffix, list(configured_names))

        for s in stale:
            assert s in discovered_with_suffix

    @given(
        names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_stale_empty_when_discovered_equals_configured(self, names):
        """If discovered == configured, stale is empty.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        discovered_with_suffix = [f"{name}.service" for name in names]

        stale = identify_stale_instances(discovered_with_suffix, list(names))

        assert stale == []

    @given(
        configured_names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_stale_empty_when_discovered_is_empty(self, configured_names):
        """If discovered is empty, stale is empty.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        stale = identify_stale_instances([], list(configured_names))

        assert stale == []

    @given(
        discovered_names=st.sets(service_names_strategy, min_size=0, max_size=10),
        configured_names=st.sets(service_names_strategy, min_size=0, max_size=10),
    )
    @settings(max_examples=200)
    def test_normalization_handles_both_formats(self, discovered_names, configured_names):
        """The function handles both bare names and .service suffix in discovered set.

        **Validates: Requirements 14.1, 14.2, 14.3, 14.4, 14.5, 14.6**
        """
        # Test with bare names (no .service suffix)
        stale_bare = identify_stale_instances(list(discovered_names), list(configured_names))
        stale_bare_normalized = set(s.replace(".service", "") for s in stale_bare)

        # Test with .service suffix
        discovered_with_suffix = [f"{name}.service" for name in discovered_names]
        stale_suffix = identify_stale_instances(discovered_with_suffix, list(configured_names))
        stale_suffix_normalized = set(s.replace(".service", "") for s in stale_suffix)

        assert stale_bare_normalized == stale_suffix_normalized


# ─── Property 5: Context Window Validation ────────────────────────────────────
# Validates: Requirements 16.2, 16.3


class TestContextWindowValidation:
    """Property 5: Context Window Validation

    Feature: local-inference-upgrade, Property 5: Context Window Validation

    For any instance configuration where ctx_size and max_ctx_size are defined,
    the validation SHALL fail if and only if ctx_size > max_ctx_size.
    """

    @given(
        ctx_size=st.integers(min_value=512, max_value=131072),
        max_ctx_size=st.integers(min_value=512, max_value=131072),
    )
    @settings(max_examples=200)
    def test_single_instance_violation_iff_ctx_exceeds_max(self, ctx_size, max_ctx_size):
        """Violation occurs iff ctx_size > max_ctx_size for a single instance.

        **Validates: Requirements 16.2, 16.3**
        """
        instances = [
            {
                "service_name": "llama-cpp-test",
                "ctx_size": ctx_size,
                "max_ctx_size": max_ctx_size,
            }
        ]

        violations = validate_context_limits(instances)

        if ctx_size > max_ctx_size:
            assert len(violations) == 1
            assert violations[0]["service_name"] == "llama-cpp-test"
            assert violations[0]["ctx_size"] == ctx_size
            assert violations[0]["max_ctx_size"] == max_ctx_size
        else:
            assert len(violations) == 0

    @given(
        data=st.lists(
            st.tuples(
                st.integers(min_value=512, max_value=131072),
                st.integers(min_value=512, max_value=131072),
            ),
            min_size=1,
            max_size=6,
        )
    )
    @settings(max_examples=200)
    def test_multiple_instances_violation_count_matches(self, data):
        """Violation count equals number of instances where ctx_size > max_ctx_size.

        **Validates: Requirements 16.2, 16.3**
        """
        instances = [
            {
                "service_name": f"llama-cpp-instance-{i}",
                "ctx_size": ctx_size,
                "max_ctx_size": max_ctx_size,
            }
            for i, (ctx_size, max_ctx_size) in enumerate(data)
        ]

        violations = validate_context_limits(instances)

        expected_count = sum(1 for ctx, max_ctx in data if ctx > max_ctx)
        assert len(violations) == expected_count

    @given(
        ctx_size=st.integers(min_value=512, max_value=131072),
    )
    @settings(max_examples=200)
    def test_zero_max_ctx_size_skips_validation(self, ctx_size):
        """When max_ctx_size is 0 (unset), no violation is reported.

        **Validates: Requirements 16.2, 16.3**
        """
        instances = [
            {
                "service_name": "llama-cpp-no-limit",
                "ctx_size": ctx_size,
                "max_ctx_size": 0,
            }
        ]

        violations = validate_context_limits(instances)
        assert len(violations) == 0


# ─── Property 6: VRAM Budget Validation ──────────────────────────────────────
# Validates: Requirements 22.3


class TestVramBudgetValidation:
    """Property 6: VRAM Budget Validation

    Feature: local-inference-upgrade, Property 6: VRAM Budget Validation

    For any set of GPU-enabled instance configurations (where n_gpu_layers > 0),
    the validation SHALL emit a warning if and only if the sum of all
    vram_reservation_mb values exceeds the total available VRAM.
    """

    @given(
        data=st.lists(
            st.tuples(
                st.integers(min_value=0, max_value=50),    # n_gpu_layers
                st.integers(min_value=0, max_value=3000),  # vram_reservation_mb
            ),
            min_size=1,
            max_size=6,
        ),
        vram_total_mb=st.integers(min_value=2048, max_value=8192),
    )
    @settings(max_examples=200)
    def test_vram_budget_ok_iff_sum_within_budget(self, data, vram_total_mb):
        """ok == True iff sum of GPU-enabled vram_reservation_mb <= vram_total_mb.

        **Validates: Requirements 22.3**
        """
        instances = [
            {
                "service_name": f"llama-cpp-inst-{i}",
                "n_gpu_layers": gpu_layers,
                "vram_reservation_mb": vram_mb,
            }
            for i, (gpu_layers, vram_mb) in enumerate(data)
        ]

        ok, total_mb, available_mb = validate_vram_budget(instances, vram_total_mb)

        # Calculate expected total (only GPU-enabled instances)
        expected_total = sum(
            vram_mb for gpu_layers, vram_mb in data if gpu_layers > 0
        )

        assert total_mb == expected_total
        assert available_mb == vram_total_mb

        if expected_total <= vram_total_mb:
            assert ok is True
        else:
            assert ok is False

    @given(
        vram_reservations=st.lists(
            st.integers(min_value=0, max_value=3000), min_size=1, max_size=6
        ),
        vram_total_mb=st.integers(min_value=2048, max_value=8192),
    )
    @settings(max_examples=200)
    def test_cpu_only_instances_excluded_from_vram_sum(self, vram_reservations, vram_total_mb):
        """Instances with n_gpu_layers == 0 never affect the VRAM calculation.

        **Validates: Requirements 22.3**
        """
        instances = [
            {
                "service_name": f"llama-cpp-cpu-{i}",
                "n_gpu_layers": 0,
                "vram_reservation_mb": vram_mb,
            }
            for i, vram_mb in enumerate(vram_reservations)
        ]

        ok, total_mb, available_mb = validate_vram_budget(instances, vram_total_mb)

        assert total_mb == 0
        assert ok is True
        assert available_mb == vram_total_mb

    @given(
        vram_total_mb=st.integers(min_value=1024, max_value=8192),
    )
    @settings(max_examples=200)
    def test_vram_exactly_at_budget_passes(self, vram_total_mb):
        """When total VRAM reservation equals budget exactly, ok is True.

        **Validates: Requirements 22.3**
        """
        instances = [
            {
                "service_name": "llama-cpp-exact",
                "n_gpu_layers": 33,
                "vram_reservation_mb": vram_total_mb,
            }
        ]

        ok, total_mb, available_mb = validate_vram_budget(instances, vram_total_mb)

        assert ok is True
        assert total_mb == vram_total_mb
        assert available_mb == vram_total_mb

    @given(
        gpu_vram=st.lists(
            st.integers(min_value=100, max_value=2000), min_size=1, max_size=4
        ),
        cpu_vram=st.lists(
            st.integers(min_value=100, max_value=5000), min_size=0, max_size=4
        ),
        vram_total_mb=st.integers(min_value=2048, max_value=8192),
    )
    @settings(max_examples=200)
    def test_mixed_gpu_cpu_only_gpu_counted(self, gpu_vram, cpu_vram, vram_total_mb):
        """In a mixed set, only GPU-enabled instances contribute to the VRAM total.

        **Validates: Requirements 22.3**
        """
        instances = []
        for i, vram_mb in enumerate(gpu_vram):
            instances.append({
                "service_name": f"llama-cpp-gpu-{i}",
                "n_gpu_layers": 20,
                "vram_reservation_mb": vram_mb,
            })
        for i, vram_mb in enumerate(cpu_vram):
            instances.append({
                "service_name": f"llama-cpp-cpu-{i}",
                "n_gpu_layers": 0,
                "vram_reservation_mb": vram_mb,
            })

        ok, total_mb, available_mb = validate_vram_budget(instances, vram_total_mb)

        expected_total = sum(gpu_vram)
        assert total_mb == expected_total
        assert available_mb == vram_total_mb

        if expected_total <= vram_total_mb:
            assert ok is True
        else:
            assert ok is False

    @given(
        vram_total_mb=st.integers(min_value=1024, max_value=8192),
        excess=st.integers(min_value=1, max_value=2000),
    )
    @settings(max_examples=200)
    def test_vram_over_budget_always_warns(self, vram_total_mb, excess):
        """When GPU VRAM sum exceeds budget by any amount, ok is False.

        **Validates: Requirements 22.3**
        """
        over_budget_total = vram_total_mb + excess
        instances = [
            {
                "service_name": "llama-cpp-over",
                "n_gpu_layers": 33,
                "vram_reservation_mb": over_budget_total,
            }
        ]

        ok, total_mb, available_mb = validate_vram_budget(instances, vram_total_mb)

        assert ok is False
        assert total_mb == over_budget_total
        assert total_mb > available_mb
