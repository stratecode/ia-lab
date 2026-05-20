#!/usr/bin/env python3
"""
validate_config.py — Pre-deployment validation for llama.cpp multi-instance configuration.

Accepts JSON configuration on stdin, validates resource budgets and constraints,
outputs JSON results to stdout.

Exit codes:
  0 = all validations pass (no warnings, no errors)
  1 = warnings only (thread over-subscription, RAM soft limit, VRAM over-subscription)
  2 = fatal errors (missing fields, context limit violations, RAM hard limit exceeded,
      no always-on instance)

RAM estimation formula:
  estimated_ram_mb = (model_size_mb + kv_cache_mb) * safety_margin
  kv_cache_mb = (ctx_size * parallel * bytes_per_token) / 1024
  MemoryMax = estimated_ram_mb + 512
"""

import json
import sys


# ─── Required fields per instance ────────────────────────────────────────────
REQUIRED_FIELDS = [
    "service_name",
    "port",
    "hf_repo",
    "alias",
    "ctx_size",
    "threads",
    "parallel",
    "n_gpu_layers",
    "api_key",
]


def estimate_instance_ram(model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin):
    """Estimate RAM consumption for a single instance in MB.

    Formula:
      kv_cache_mb = (ctx_size * parallel * bytes_per_token) / 1024
      estimated_ram_mb = (model_size_mb + kv_cache_mb) * safety_margin

    Args:
        model_size_mb: Approximate GGUF model file size in MB.
        ctx_size: Context window size in tokens.
        parallel: Number of concurrent request slots.
        bytes_per_token: Heuristic bytes per token for KV cache estimation.
        safety_margin: Multiplier for safety overhead (e.g. 1.15 = 15% margin).

    Returns:
        Estimated RAM consumption in MB (float).
    """
    kv_cache_mb = (ctx_size * parallel * bytes_per_token) / 1024.0
    estimated_ram_mb = (model_size_mb + kv_cache_mb) * safety_margin
    return estimated_ram_mb


def validate_thread_budget(instances, thread_budget):
    """Validate that total thread allocation does not exceed the budget.

    Args:
        instances: List of instance configuration dicts (each with 'threads' key).
        thread_budget: Maximum allowed total threads.

    Returns:
        Tuple of (ok: bool, total: int, budget: int).
        ok is True when total <= budget.
    """
    total = sum(int(inst.get("threads", 0)) for inst in instances)
    ok = total <= thread_budget
    return (ok, total, thread_budget)


def validate_ram_budget(instances, ram_budget_mb, hard_limit_percent, estimation_factors):
    """Validate total estimated RAM against budget thresholds.

    Thresholds:
      pass: total <= budget
      warn: total > budget AND total <= budget * hard_limit_percent / 100
      fail: total > budget * hard_limit_percent / 100

    Args:
        instances: List of instance configuration dicts.
        ram_budget_mb: Configured RAM budget in MB.
        hard_limit_percent: Percentage of budget that triggers fatal failure (e.g. 110).
        estimation_factors: Dict of per-model-family estimation parameters.

    Returns:
        Tuple of (status: str, total_mb: float, budget_mb: int, excess_mb: float).
        status is one of 'pass', 'warn', 'fail'.
    """
    total_mb = 0.0
    for inst in instances:
        ram = _estimate_ram_for_instance(inst, estimation_factors)
        total_mb += ram

    hard_limit_mb = ram_budget_mb * hard_limit_percent / 100.0
    excess_mb = max(0.0, total_mb - ram_budget_mb)

    if total_mb <= ram_budget_mb:
        status = "pass"
    elif total_mb <= hard_limit_mb:
        status = "warn"
    else:
        status = "fail"

    return (status, total_mb, ram_budget_mb, excess_mb)


def validate_context_limits(instances):
    """Validate that ctx_size does not exceed max_ctx_size for any instance.

    Args:
        instances: List of instance configuration dicts.

    Returns:
        List of violation dicts, each with keys:
          service_name, ctx_size, max_ctx_size.
        Empty list means all instances pass.
    """
    violations = []
    for inst in instances:
        ctx_size = int(inst.get("ctx_size", 0))
        max_ctx_size = int(inst.get("max_ctx_size", 0))
        if max_ctx_size > 0 and ctx_size > max_ctx_size:
            violations.append({
                "service_name": inst.get("service_name", "unknown"),
                "ctx_size": ctx_size,
                "max_ctx_size": max_ctx_size,
            })
    return violations


def validate_vram_budget(instances, vram_total_mb):
    """Validate that total VRAM reservations for GPU-enabled instances fit within budget.

    Only instances with n_gpu_layers > 0 are considered GPU-enabled.

    Args:
        instances: List of instance configuration dicts.
        vram_total_mb: Total available VRAM in MB.

    Returns:
        Tuple of (ok: bool, total_mb: int, available_mb: int).
        ok is True when total <= available.
    """
    total_mb = 0
    for inst in instances:
        if int(inst.get("n_gpu_layers", 0)) > 0:
            total_mb += int(inst.get("vram_reservation_mb", 0))
    ok = total_mb <= vram_total_mb
    return (ok, total_mb, vram_total_mb)


def identify_stale_instances(discovered_services, configured_services):
    """Identify stale instances as the set difference: discovered - configured.

    Handles both bare service names ('llama-cpp-code') and names with
    '.service' suffix ('llama-cpp-code.service'). Comparison is done on
    normalized names (without suffix), but returns names in their original
    discovered format.

    Args:
        discovered_services: List/set of discovered systemd service names
            (may include '.service' suffix).
        configured_services: List/set of configured instance service names
            (bare names without suffix).

    Returns:
        Sorted list of stale service names (in their original discovered format).
    """
    def normalize(name):
        """Strip .service suffix for comparison."""
        if name.endswith(".service"):
            return name[:-8]
        return name

    configured_normalized = set(normalize(s) for s in configured_services)
    stale = [s for s in discovered_services if normalize(s) not in configured_normalized]
    return sorted(stale)


def validate_required_fields(instances):
    """Validate that all required fields are present in each instance.

    Args:
        instances: List of instance configuration dicts.

    Returns:
        List of dicts with keys: service_name, missing_fields.
        Empty list means all instances have all required fields.
    """
    results = []
    for inst in instances:
        name = inst.get("service_name", "unknown")
        missing = [f for f in REQUIRED_FIELDS if f not in inst or inst[f] is None or inst[f] == ""]
        if missing:
            results.append({
                "service_name": name,
                "missing_fields": missing,
            })
    return results


def validate_lifecycle_policy(instances):
    """Validate that at least one instance has instance_lifecycle == 'always-on'.

    Args:
        instances: List of instance configuration dicts.

    Returns:
        Tuple of (ok: bool,).
        ok is True when at least one always-on instance exists.
    """
    has_always_on = any(
        inst.get("instance_lifecycle") == "always-on" for inst in instances
    )
    return (has_always_on,)


# ─── Internal helpers ────────────────────────────────────────────────────────


def _get_model_family(hf_repo):
    """Extract model family from hf_repo string for estimation factor lookup.

    Examples:
      'Qwen/Qwen2.5-Coder-7B-Instruct-GGUF:Q4_K_M' -> 'qwen2.5'
      'nomic-ai/nomic-embed-text-v1.5-GGUF:Q8_0' -> 'nomic'
    """
    repo_lower = (hf_repo or "").lower()
    if "qwen2.5" in repo_lower:
        return "qwen2.5"
    if "nomic" in repo_lower:
        return "nomic"
    if "bge-m3" in repo_lower:
        return "bge"
    if "qwen3-embedding" in repo_lower:
        return "qwen3-embedding"
    return "default"


def _get_quantization(hf_repo):
    """Extract quantization level from hf_repo string.

    Examples:
      'Qwen/Qwen2.5-Coder-7B-Instruct-GGUF:Q4_K_M' -> 'q4'
      'nomic-ai/nomic-embed-text-v1.5-GGUF:Q8_0' -> 'q8'
    """
    repo_lower = (hf_repo or "").lower()
    # Check after the colon for quantization tag
    if ":" in repo_lower:
        tag = repo_lower.split(":")[-1]
        if tag.startswith("q8"):
            return "q8"
        if tag.startswith("q4"):
            return "q4"
        if tag.startswith("q6"):
            return "q4"  # Treat Q6 similar to Q4 for estimation
        if tag.startswith("q5"):
            return "q4"  # Treat Q5 similar to Q4 for estimation
    return "q4"


def _get_bytes_per_token(family_factors, quantization):
    """Get bytes_per_token from estimation factors for a given quantization level."""
    key = "bytes_per_token_{}".format(quantization)
    if key in family_factors:
        return family_factors[key]
    # Fallback: try q4 then q8
    if "bytes_per_token_q4" in family_factors:
        return family_factors["bytes_per_token_q4"]
    if "bytes_per_token_q8" in family_factors:
        return family_factors["bytes_per_token_q8"]
    # Ultimate fallback
    return 0.6


def _estimate_ram_for_instance(inst, estimation_factors):
    """Estimate RAM for a single instance using model family factors.

    If ram_estimation_override_mb is set, use that directly.
    """
    override = inst.get("ram_estimation_override_mb")
    if override is not None and override != "" and override != "null":
        try:
            return float(override)
        except (ValueError, TypeError):
            pass

    model_size_mb = float(inst.get("model_size_mb", 0))
    ctx_size = int(inst.get("ctx_size", 0))
    parallel = int(inst.get("parallel", 1))
    hf_repo = inst.get("hf_repo", "")

    family = _get_model_family(hf_repo)
    quantization = _get_quantization(hf_repo)

    factors = estimation_factors.get(family, estimation_factors.get("default", {}))
    default_factors = estimation_factors.get("default", {})

    safety_margin = factors.get("safety_margin", default_factors.get("safety_margin", 1.20))
    bytes_per_token = _get_bytes_per_token(factors, quantization)

    return estimate_instance_ram(model_size_mb, ctx_size, parallel, bytes_per_token, safety_margin)


# ─── Main entry point ────────────────────────────────────────────────────────


def main():
    """Read JSON config from stdin, run all validations, output JSON results."""
    try:
        config = json.load(sys.stdin)
    except (json.JSONDecodeError, ValueError) as e:
        result = {
            "status": "fail",
            "errors": ["Invalid JSON input: {}".format(str(e))],
            "warnings": [],
            "details": {},
        }
        json.dump(result, sys.stdout, indent=2)
        sys.exit(2)

    instances = config.get("instances", [])
    ram_budget_mb = int(config.get("ram_budget_mb", 22528))
    hard_limit_percent = int(config.get("ram_budget_hard_limit_percent", 110))
    thread_budget = int(config.get("thread_budget", 12))
    vram_total_mb = int(config.get("vram_total_mb", 4096))
    estimation_factors = config.get("ram_estimation_factors", {"default": {"safety_margin": 1.20, "bytes_per_token_q4": 0.6, "bytes_per_token_q8": 1.1}})
    discovered_services = config.get("discovered_services", [])

    errors = []
    warnings = []

    # 1. Validate required fields
    missing_fields = validate_required_fields(instances)
    if missing_fields:
        for entry in missing_fields:
            errors.append(
                "Instance '{}' is missing required fields: {}".format(
                    entry["service_name"], ", ".join(entry["missing_fields"])
                )
            )

    # 2. Validate context limits
    context_violations = validate_context_limits(instances)
    if context_violations:
        for v in context_violations:
            errors.append(
                "Instance '{}': ctx_size ({}) exceeds max_ctx_size ({})".format(
                    v["service_name"], v["ctx_size"], v["max_ctx_size"]
                )
            )

    # 3. Validate lifecycle policy
    lifecycle_ok = validate_lifecycle_policy(instances)
    if not lifecycle_ok[0]:
        errors.append("No instance configured with instance_lifecycle 'always-on'")

    # 4. Validate thread budget
    thread_ok, thread_total, thread_bgt = validate_thread_budget(instances, thread_budget)
    if not thread_ok:
        warnings.append(
            "Thread over-subscription: total {} exceeds budget {}".format(
                thread_total, thread_bgt
            )
        )

    # 5. Validate RAM budget
    ram_status, ram_total, ram_bgt, ram_excess = validate_ram_budget(
        instances, ram_budget_mb, hard_limit_percent, estimation_factors
    )
    if ram_status == "warn":
        warnings.append(
            "RAM budget warning: estimated {:.0f}MB exceeds budget {}MB by {:.0f}MB (within hard limit)".format(
                ram_total, ram_bgt, ram_excess
            )
        )
    elif ram_status == "fail":
        errors.append(
            "RAM budget exceeded: estimated {:.0f}MB exceeds hard limit ({}% of {}MB = {:.0f}MB) by {:.0f}MB".format(
                ram_total, hard_limit_percent, ram_bgt,
                ram_bgt * hard_limit_percent / 100.0,
                ram_total - ram_bgt * hard_limit_percent / 100.0
            )
        )

    # 6. Validate VRAM budget
    vram_ok, vram_total, vram_available = validate_vram_budget(instances, vram_total_mb)
    if not vram_ok:
        warnings.append(
            "VRAM over-subscription: total {}MB exceeds available {}MB".format(
                vram_total, vram_available
            )
        )

    # 7. Identify stale instances
    configured_names = [inst.get("service_name", "") for inst in instances]
    stale = identify_stale_instances(discovered_services, configured_names)

    # Determine overall status
    if errors:
        status = "fail"
    elif warnings:
        status = "warn"
    else:
        status = "pass"

    result = {
        "status": status,
        "errors": errors,
        "warnings": warnings,
        "details": {
            "thread_budget": {
                "ok": thread_ok,
                "total": thread_total,
                "budget": thread_bgt,
            },
            "ram_budget": {
                "status": ram_status,
                "total_mb": round(ram_total, 2),
                "budget_mb": ram_bgt,
                "excess_mb": round(ram_excess, 2),
            },
            "vram_budget": {
                "ok": vram_ok,
                "total_mb": vram_total,
                "available_mb": vram_available,
            },
            "context_violations": context_violations,
            "missing_fields": missing_fields,
            "stale_instances": stale,
            "lifecycle_policy": {
                "ok": lifecycle_ok[0],
            },
        },
    }

    json.dump(result, sys.stdout, indent=2)

    # Exit code based on status
    if status == "fail":
        sys.exit(2)
    elif status == "warn":
        sys.exit(1)
    else:
        sys.exit(0)


if __name__ == "__main__":
    main()
