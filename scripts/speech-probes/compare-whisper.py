#!/usr/bin/env python3
"""Score one Whisper server on recorded clips against their known sentences.

Prints one JSON line per configuration with the transcripts, the word error rate
and whether the words that matter for routing survived. Numbers are compared as
digits on both sides ("ciento sesenta y seis" equals "166"), so that writing a
number differently is not counted as a mistake.
"""
import argparse
import json
import re
import statistics
import time
import unicodedata
import urllib.request
import uuid
from pathlib import Path

UNITS = {"cero": 0, "un": 1, "uno": 1, "una": 1, "dos": 2, "tres": 3, "cuatro": 4, "cinco": 5, "seis": 6, "siete": 7, "ocho": 8, "nueve": 9, "diez": 10,
         "once": 11, "doce": 12, "trece": 13, "catorce": 14, "quince": 15, "dieciseis": 16, "diecisiete": 17, "dieciocho": 18, "diecinueve": 19, "veinte": 20,
         "veintiuno": 21, "veintidos": 22, "veintitres": 23, "veinticuatro": 24, "veinticinco": 25, "veintiseis": 26, "veintisiete": 27, "veintiocho": 28, "veintinueve": 29}
TENS = {"treinta": 30, "cuarenta": 40, "cincuenta": 50, "sesenta": 60, "setenta": 70, "ochenta": 80, "noventa": 90}
HUNDREDS = {"cien": 100, "ciento": 100, "doscientos": 200, "trescientos": 300, "cuatrocientos": 400, "quinientos": 500, "seiscientos": 600,
            "setecientos": 700, "ochocientos": 800, "novecientos": 900}


def strip_accents(text):
    return "".join(c for c in unicodedata.normalize("NFD", text) if unicodedata.category(c) != "Mn")


def canonical(text):
    """Lowercase words, no accents or punctuation, Spanish number words as digits."""
    text = strip_accents(text.lower()).replace("-", " ")
    words = re.sub(r"[^a-z0-9 ]", " ", text).split()
    out, i = [], 0
    while i < len(words):
        w = words[i]
        if w in UNITS or w in TENS or w in HUNDREDS:
            value, j = 0, i
            while j < len(words):
                x = words[j]
                if x in HUNDREDS:
                    value += HUNDREDS[x]
                elif x in TENS:
                    value += TENS[x]
                elif x in UNITS:
                    value += UNITS[x]
                elif x == "y" and j + 1 < len(words) and words[j + 1] in UNITS and 30 <= value % 100 and value % 10 == 0:
                    pass
                else:
                    break
                j += 1
            out.append(str(value))
            i = j
        else:
            out.append(w)
            i += 1
    # "60 y 7" -> 67
    merged, i = [], 0
    while i < len(out):
        if i + 2 < len(out) and out[i].isdigit() and out[i + 1] == "y" and out[i + 2].isdigit() and int(out[i]) % 10 == 0 and 20 <= int(out[i]) < 100 and 1 <= int(out[i + 2]) <= 9:
            merged.append(str(int(out[i]) + int(out[i + 2])))
            i += 3
        else:
            merged.append(out[i])
            i += 1
    return merged


def edit_distance(a, b):
    previous = list(range(len(b) + 1))
    for i, x in enumerate(a, 1):
        current = [i]
        for j, y in enumerate(b, 1):
            current.append(min(previous[j] + 1, current[j - 1] + 1, previous[j - 1] + (x != y)))
        previous = current
    return previous[-1]


def transcribe(url, wav, prompt):
    boundary = uuid.uuid4().hex
    fields = {"response_format": "verbose_json", "temperature": "0", "language": "es"}
    if prompt:
        fields["prompt"] = prompt
    body = b""
    for key, value in fields.items():
        body += f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode()
    body += f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="v.wav"\r\nContent-Type: audio/wav\r\n\r\n'.encode() + wav + f"\r\n--{boundary}--\r\n".encode()
    request = urllib.request.Request(url + "/inference", data=body, headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    started = time.monotonic()
    data = json.load(urllib.request.urlopen(request, timeout=600))
    elapsed = time.monotonic() - started
    text = re.sub(r"\[[^\]]*\]", " ", data.get("text", ""))
    segments = data.get("segments", [])
    logprob = sum(s.get("avg_logprob", 0) for s in segments) / len(segments) if segments else 0.0
    return " ".join(text.split()), elapsed, logprob


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--url", default="http://127.0.0.1:8193")
    parser.add_argument("--label", required=True)
    parser.add_argument("--clips", required=True, type=Path)
    parser.add_argument("--refs", required=True, type=Path)
    parser.add_argument("--prompt", default="")
    parser.add_argument("--passes", type=int, default=2)
    args = parser.parse_args()
    refs = json.loads(args.refs.read_text(encoding="utf-8"))
    results, errors, total_words, latencies = [], 0, 0, []
    critical_ok = critical_total = 0
    for name, ref in refs.items():
        wav = (args.clips / f"{name}.wav").read_bytes()
        times, text, logprob = [], "", 0.0
        for _ in range(args.passes):
            text, elapsed, logprob = transcribe(args.url, wav, args.prompt)
            times.append(elapsed)
        got, want = canonical(text), canonical(ref["text"])
        distance = edit_distance(want, got)
        errors += distance
        total_words += len(want)
        latencies.append(min(times[1:] or times))
        hits = [w for w in ref["critical"] if w in got]
        critical_ok += len(hits)
        critical_total += len(ref["critical"])
        results.append({"clip": name, "heard": text, "errors": distance, "words": len(want), "seconds": round(times[-1], 2), "logprob": round(logprob, 2),
                        "missed": [w for w in ref["critical"] if w not in got]})
    print(json.dumps({"label": args.label, "wer": round(errors / total_words, 3), "errors": errors, "words": total_words,
                      "critical": f"{critical_ok}/{critical_total}", "median_s": round(statistics.median(latencies), 2),
                      "max_s": round(max(latencies), 2), "clips": results}, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
