# llama.cpp router benchmark — 2026-10-16

## Executive result

The isolated llama.cpp router is deployed and serving four resident aliases:
`main`, `qwen-eval`, `lfm-2.6b`, and `qwen25-7b-q4_0`. Prometheus reports all
four scrape targets as healthy and records per-alias token counters. Open WebUI
and `lab-secretary` remain on their previous configuration; this benchmark calls
the loopback router directly.

In the initial three-model run, all three aliases accepted inference requests
at the same time. Under concurrent load, they competed for the router service's
shared eight-CPU quota. Concurrency substantially increased completion latency
and reduced per-model generation throughput. Keeping models resident is useful
for switching without reloads; it does not make CPU inference faster when all
models generate at once.

## Initial three-model baseline

The following baseline measures the original `main`, `qwen-eval`, and
`lfm-2.6b` router configuration before Qwen2.5-7B was added.

## Method

- Host-local, authenticated calls to `127.0.0.1:8185/v1/chat/completions`.
- Same Spanish prompt, temperature 0 and a 192-token output cap for each alias.
- Solo: three requests per model, one at a time, with models already resident.
- Concurrent: two waves; each wave launched one request to each of the three
  aliases at once.
- Measured wall latency and time to first generated token from the streaming API.
  Generation tokens and generation time came from the alias-scoped llama.cpp
  Prometheus counters.
- The harness did not print or retain response text or the router API key.

The solo median is based on three observations per model; the concurrent median
is based on two. Treat these figures as a first operational sample, not a
capacity guarantee.

## Results

| Alias | Solo median latency | Concurrent median latency | Slowdown | Solo generation | Concurrent generation | Median TTFT solo → concurrent |
|---|---:|---:|---:|---:|---:|---:|
| `main` (LFM 1.2B) | 2.39 s | 31.95 s | 13.3× | 48.14 tok/s | 3.55 tok/s | 0.025 s → 0.470 s |
| `qwen-eval` (Qwen3 4B) | 9.93 s | 55.07 s | 5.5× | 13.40 tok/s | 2.50 tok/s | 0.083 s → 1.182 s |
| `lfm-2.6b` (LFM 2.6B) | 9.57 s | 53.57 s | 5.6× | 20.24 tok/s | 3.71 tok/s | 0.073 s → 0.650 s |

For one request to each model, the sum of the solo median latencies was 21.89 s.
In the concurrent waves, the median time until all three finished was 55.07 s.
That corresponds to about 19.9 generated tokens/s for the three serial calls,
versus 7.9 tokens/s for the concurrent set. TTFT was relatively low because the
models were warm and repeated prompts used prefix-cache tokens; total generation
time is the more useful comparison here.

The 2.6B LFM reached the 192-token cap in all runs (`finish_reason=length`), so
those runs measure inference speed but do not establish that it completed the
requested answer. This benchmark does not compare answer quality.

## Four-model follow-up: Qwen2.5 7B Q4_0

After adding the official Qwen2.5-7B-Instruct Q4_0 split GGUF, the same prompt
and output cap were run three times per model individually, then twice with all
four models generating at once. The router and all weights were warm. Medians:

| Alias | Solo median latency | Four-way concurrent median latency | Slowdown | Solo generation | Concurrent generation | Median TTFT solo → concurrent |
|---|---:|---:|---:|---:|---:|---:|
| `main` | 2.37 s | 237.51 s | 100.2× | 47.40 tok/s | 0.47 tok/s | 0.027 s → 1.734 s |
| `qwen-eval` | 9.93 s | 255.04 s | 25.7× | 13.40 tok/s | 0.53 tok/s | 0.081 s → 3.058 s |
| `lfm-2.6b` | 9.60 s | 255.03 s | 26.6× | 20.15 tok/s | 0.76 tok/s | 0.073 s → 1.151 s |
| `qwen25-7b-q4_0` | 23.29 s | 264.69 s | 11.4× | 8.32 tok/s | 0.73 tok/s | 0.125 s → 2.330 s |

The median sum of solo latencies for one request per alias is 55.20 s. The
median time for the four concurrent requests all to finish is 264.69 s. Across
the 627 generated tokens, aggregate throughput falls from about 11.4 tok/s when
the calls run serially to 2.37 tok/s when they run together. The 7B model is
roughly 2.8× slower than Qwen3 4B for this prompt when run solo, and its
192-token responses hit the cap. No quality conclusion follows from this speed
test.

At peak, the router cgroup used about 9.78 GB of its 12 GiB limit and the host
still showed about 14 GiB available. All four `/metrics` `requests_processing`
gauges returned to zero, all four Prometheus targets remained up, and the
production model and secretary services remained active. The host's one-minute
load average peaked at 23.25 during this run. This confirms the current CPU
configuration is heavily oversubscribed when all four aliases generate at once.

## Initial baseline resource and health checks

- Router service stayed active; its cgroup memory was approximately 5.55 GB
  during the test and 5.35 GB after it settled.
- Available host memory remained approximately 18 GB after the run.
- Prometheus showed all three aliases `up`; after completion, all three
  `requests_processing` gauges returned to zero.
- `llama-cpp-main`, `llama-cpp-qwen-eval`, `lab-secretary`, and Prometheus
  remained active.

## Recommendation

Keep multiple models loaded if fast model switching is the goal, but do not
expect concurrent generation to improve responsiveness on this CPU budget. For
Donna, allow only one generation across the router at a time and queue competing
requests. Expose the aliases in Open WebUI only with that concurrency limit;
otherwise a comparison view can make all candidates dramatically slower at once.

The router's per-model routing and `?model=<alias>` Prometheus interface follow
the [official llama.cpp server documentation](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md#using-multiple-models).
