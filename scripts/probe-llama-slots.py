#!/usr/bin/env python3
"""Does an unrelated request evict Donna's cached prefix? Run against one llama-server.

Sends A1 (Donna prompt), B (an unrelated prompt, like Open WebUI's title
generation) and A2 (Donna prompt, different question). The prompt_n of A2 is the
number of tokens that had to be processed again; a small value means the
prefix survived B.
"""
import argparse
import importlib.util
import json
from pathlib import Path

spec = importlib.util.spec_from_file_location("bench", Path(__file__).with_name("benchmark-donna-models.py"))
bench = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bench)

parser = argparse.ArgumentParser()
parser.add_argument("--base", default="http://127.0.0.1:8190")
parser.add_argument("--alias", default="probe")
parser.add_argument("--instructions", required=True, type=Path)
parser.add_argument("--template-kwargs", type=json.loads, default=None)
args = parser.parse_args()
instructions = args.instructions.read_text(encoding="utf-8")


def run(label, messages):
    result = bench.chat(args.base, args.alias, messages, max_tokens=48, cache_prompt=True, template_kwargs=args.template_kwargs)
    print(json.dumps({"label": label, "latency_s": result["latency_s"], "prompt_tokens": result["prompt_tokens"],
                      "reprocessed": result["prompt_n"], "reused": result["cache_n"]}), flush=True)


run("A1 donna", bench.donna_messages(instructions, "¿Qué es un spike en desarrollo de software?"))
run("B unrelated", [{"role": "system", "content": "Genera un título de tres palabras para la conversación."},
                    {"role": "user", "content": "Hablamos de planificación de sprints y de estimaciones del equipo."}])
run("A2 donna", bench.donna_messages(instructions, "¿Qué diferencia hay entre un sprint y un spike?"))
