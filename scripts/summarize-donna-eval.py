#!/usr/bin/env python3
"""Summarise the JSONL files written by benchmark-donna-models.py as Markdown."""

import argparse
import json
import statistics
from collections import defaultdict
from pathlib import Path


def load(directory):
    rows = defaultdict(list)
    for path in sorted(directory.glob("*.jsonl")):
        for line in path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError:
                continue  # a partially written line while the run is still going
            if "alias" in record:
                rows[record["alias"]].append(record)
    return rows


def median(values):
    values = [v for v in values if v is not None]
    return statistics.median(values) if values else None


def fmt(value, digits=1, suffix=""):
    return "–" if value is None else f"{value:.{digits}f}{suffix}"


def memory(directory):
    result = {}
    path = directory / "memory.txt"
    if path.exists():
        for line in path.read_text().splitlines():
            parts = dict(item.split("=", 1) for item in line.split() if "=" in item)
            if parts.get("memory_peak_bytes", "").isdigit():
                result[parts["alias"]] = int(parts["memory_peak_bytes"]) / 2**30
    return result


def summarise(directory):
    rows = load(directory)
    peaks = memory(directory)
    out = ["## Calidad con el prompt real de Donna (13 casos, puntuación automática sobre 4)", "",
           "| Modelo | Puntuación media | % | Latencia mediana | Palabras medias | Tokens/s generación |", "|---|---:|---:|---:|---:|---:|"]
    quality = {}
    for alias, records in rows.items():
        q = [r for r in records if r["suite"] == "quality"]
        if not q:
            continue
        mean = statistics.mean(r["score"] for r in q)
        quality[alias] = q
        out.append(f"| `{alias}` | {mean:.2f} / 4 | {100 * mean / 4:.0f} % | {fmt(median(r['latency_s'] for r in q), 1, ' s')} | "
                   f"{fmt(statistics.mean(len(r['response'].split()) for r in q), 0)} | {fmt(median(r['predicted_tps'] for r in q))} |")
    cases = sorted({r["case_id"] for q in quality.values() for r in q})
    out += ["", "| Caso | " + " | ".join(f"`{a}`" for a in quality) + " |", "|---|" + "---:|" * len(quality)]
    for case in cases:
        cells = []
        for alias, q in quality.items():
            match = [r["score"] for r in q if r["case_id"] == case]
            cells.append(fmt(match[0], 1) if match else "–")
        out.append(f"| {case} | " + " | ".join(cells) + " |")

    out += ["", "## Contexto largo: recuperar un dato escondido en las notas", "",
            "| Modelo | Tokens objetivo | Acierto | Latencia | Prefill tokens/s |", "|---|---:|---:|---:|---:|"]
    for alias, records in rows.items():
        by_size = defaultdict(list)
        for r in records:
            if r["suite"] == "context" and "target_tokens" in r and "correct" in r:
                by_size[r["target_tokens"]].append(r)
        for size in sorted(by_size):
            rs = by_size[size]
            out.append(f"| `{alias}` | {size} (real {rs[0]['prompt_tokens']}) | {sum(r['correct'] for r in rs)}/{len(rs)} | "
                       f"{fmt(median(r['latency_s'] for r in rs), 0, ' s')} | {fmt(median(r['prompt_tps'] for r in rs), 0)} |")
        stopped = [r for r in records if r["suite"] == "context" and "stopped_after_seconds" in r]
        for r in stopped:
            out.append(f"| `{alias}` | >{r['target_tokens']} | no probado | parado tras {r['stopped_after_seconds']} s | – |")

    out += ["", "## Caché de prompt (mismo prefijo de instrucciones)", "",
            "| Modelo | Sin caché | 2.ª petición con prefijo compartido | Tokens reutilizados |", "|---|---:|---:|---:|"]
    for alias, records in rows.items():
        cache = {r["label"]: r for r in records if r["suite"] == "cache"}
        if len(cache) == 3:
            warm = cache["second_shared_prefix"]
            out.append(f"| `{alias}` | {cache['cold_no_cache']['latency_s']} s | {warm['latency_s']} s | {warm['cache_n']} de {warm['prompt_tokens']} |")

    out += ["", "## Memoria", "", "| Modelo | Pico de memoria del servidor (contexto 36 864) |", "|---|---:|"]
    for alias in rows:
        out.append(f"| `{alias}` | {fmt(peaks.get(alias), 1, ' GiB')} |")
    return "\n".join(out)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("directory", type=Path)
    print(summarise(parser.parse_args().directory))
