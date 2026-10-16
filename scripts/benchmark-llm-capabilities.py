#!/usr/bin/env python3
"""Run a versioned capability suite against OpenAI-compatible chat models."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import sys
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


DEFAULT_CASES = Path(__file__).resolve().parents[1] / "benchmarks/llm-capabilities/v1/cases.json"


def parse_stream(lines, started_at, clock=time.monotonic):
    """Collect OpenAI-compatible SSE content, usage, finish reason and TTFT."""
    content = []
    reasoning_characters = 0
    usage = {}
    finish_reason = None
    first_token_at = None

    for raw_line in lines:
        line = raw_line.decode("utf-8", errors="replace") if isinstance(raw_line, bytes) else raw_line
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if not data or data == "[DONE]":
            continue
        try:
            event = json.loads(data)
        except json.JSONDecodeError:
            continue
        usage = event.get("usage") or usage
        for choice in event.get("choices", []):
            delta = choice.get("delta") or {}
            reasoning = delta.get("reasoning_content")
            if reasoning:
                reasoning_characters += len(reasoning)
                if first_token_at is None:
                    first_token_at = clock()
            text = delta.get("content")
            if text:
                if first_token_at is None:
                    first_token_at = clock()
                content.append(text)
            if choice.get("finish_reason"):
                finish_reason = choice["finish_reason"]

    return {
        "response": "".join(content),
        "reasoning_characters": reasoning_characters,
        "finish_reason": finish_reason,
        "ttft_seconds": round(first_token_at - started_at, 4) if first_token_at else None,
        "prompt_tokens": usage.get("prompt_tokens"),
        "completion_tokens": usage.get("completion_tokens"),
    }


def load_cases(path):
    data = json.loads(Path(path).read_text(encoding="utf-8"))
    if not data.get("version") or not isinstance(data.get("cases"), list):
        raise ValueError("case file must contain version and cases")
    if len(data["cases"]) != 10:
        raise ValueError(f"expected exactly 10 cases, got {len(data['cases'])}")
    ids = [case.get("id") for case in data["cases"]]
    if not all(ids) or len(ids) != len(set(ids)):
        raise ValueError("case ids must be present and unique")
    for case in data["cases"]:
        if not case.get("messages") or not case.get("rubric"):
            raise ValueError(f"case {case.get('id')} is missing messages or rubric")
    return data


def model_order(models, case_index):
    """Rotate which alias starts each case to reduce order and thermal bias."""
    if not models:
        return []
    offset = case_index % len(models)
    return models[offset:] + models[:offset]


def run_case(endpoint, api_key, dataset, case, model, timeout):
    messages = [{"role": "system", "content": dataset["system_prompt"]}, *case["messages"]]
    payload = {
        "model": model,
        "messages": messages,
        "temperature": 0,
        "seed": 20261016,
        "max_tokens": case.get("max_tokens", 256),
        "stream": True,
        "stream_options": {"include_usage": True},
        "cache_prompt": False,
    }
    request = Request(
        endpoint,
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"},
        method="POST",
    )
    start = time.monotonic()
    result = {
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "suite_version": dataset["version"],
        "case_id": case["id"],
        "category": case["category"],
        "model": model,
    }
    try:
        with urlopen(request, timeout=timeout) as response:
            parsed = parse_stream(response, start)
            result.update(parsed)
            result["http_status"] = response.status
    except HTTPError as error:
        result.update({"http_status": error.code, "error": f"HTTP {error.code} {error.reason}"})
    except (URLError, TimeoutError, OSError) as error:
        result.update({"http_status": None, "error": type(error).__name__})
    result["latency_seconds"] = round(time.monotonic() - start, 4)
    if result.get("completion_tokens") and result.get("ttft_seconds") is not None:
        generation_seconds = max(result["latency_seconds"] - result["ttft_seconds"], 0.001)
        result["tokens_per_second"] = round(result["completion_tokens"] / generation_seconds, 3)
    result.setdefault("response", "")
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cases", default=str(DEFAULT_CASES))
    parser.add_argument("--endpoint", default="http://127.0.0.1:8185/v1/chat/completions")
    parser.add_argument("--models", required=True, help="comma-separated model aliases, tested serially")
    parser.add_argument("--api-key-file", default="/etc/llama-cpp/router-api-key")
    parser.add_argument("--output", default="-", help="JSONL destination; '-' writes results to stdout")
    parser.add_argument("--append", action="store_true", help="append to an existing JSONL file")
    parser.add_argument("--timeout", type=int, default=600)
    args = parser.parse_args(argv)

    dataset = load_cases(args.cases)
    models = [model.strip() for model in args.models.split(",") if model.strip()]
    if not models:
        parser.error("provide at least one model alias")
    api_key = os.environ.get("LLAMA_ROUTER_API_KEY")
    if not api_key:
        api_key = Path(args.api_key_file).read_text(encoding="utf-8").strip()
    if not api_key:
        parser.error("API key is empty")

    output = sys.stdout if args.output == "-" else open(args.output, "a" if args.append else "w", encoding="utf-8")
    try:
        for case_index, case in enumerate(dataset["cases"]):
            for model in model_order(models, case_index):
                result = run_case(args.endpoint, api_key, dataset, case, model, args.timeout)
                output.write(json.dumps(result, ensure_ascii=False) + "\n")
                output.flush()
                print(
                    f"{model} {case['id']}: HTTP {result.get('http_status')} "
                    f"{result['latency_seconds']:.2f}s",
                    file=sys.stderr,
                    flush=True,
                )
    finally:
        if output is not sys.stdout:
            output.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
