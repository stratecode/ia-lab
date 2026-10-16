# LLM capability benchmark — 2026-10-16

## Executive decision

Keep `main` (LFM2.5 1.2B) for low-latency requests and `qwen-eval` (Qwen3 4B)
as the stronger general-purpose model. This is the best two-model operating
pair in this sample: `main` completed the ten cases in 38.4 seconds total,
while Qwen3 scored 34/40 and completed them in 172.8 seconds.

Qwen3 and LFM2.5 2.6B tied at 34/40. Qwen3 is the more practical second model:
it was about 2.8 times faster across the suite, handled the ambiguous-person
case more safely, and used far fewer generated tokens. LFM2.5 2.6B performed
well on code, arithmetic and long-context retrieval, but spent a large amount
of output on internal reasoning and still guessed a person's identity in the
ambiguous case. Qwen2.5-7B Q4_0 scored 26/40 and was slower than Qwen3, so this
run does not support keeping it ahead of Qwen3.

This is a recommendation, not a deployment change. All four aliases remain
loaded in the isolated evaluation router; production `lab-secretary` and
Open WebUI were not changed.

## Results

| Model alias | Manual quality | Median end-to-end latency | Total for 10 cases | Median generated throughput | Generated tokens per case, avg. | Quality points / elapsed second |
|---|---:|---:|---:|---:|---:|---:|
| `main` — LFM2.5 1.2B | 19/40 (47.5%) | 2.71 s | 38.4 s | 47.03 tok/s | 138 | 0.49 |
| `qwen-eval` — Qwen3 4B | **34/40 (85%)** | 14.37 s | 172.8 s | 12.92 tok/s | 145 | **0.20** |
| `lfm-2.6b` — LFM2.5 2.6B | **34/40 (85%)** | 41.68 s | 476.3 s | 19.70 tok/s* | 875* | 0.07 |
| `qwen25-7b-q4_0` — Qwen2.5 7B | 26/40 (65%) | 23.90 s | 282.6 s | 8.24 tok/s | 178 | 0.09 |

`quality points / elapsed second` is the manual score divided by the sum of
latencies for the ten requests. It is a rough comparison index, not a universal
model metric. The token throughput uses the API's completion-token count. For
LFM2.5 2.6B this includes its reasoning tokens, so its 19.70 tok/s and 875
tokens/case overstate user-visible output efficiency; the run counted 27,758
reasoning characters, without retaining their contents. Its median final
response latency was 41.68 seconds, versus 14.37 seconds for Qwen3.

| Case | Topic | `main` | `qwen-eval` | `lfm-2.6b` | `qwen25-7b-q4_0` |
|---|---|---:|---:|---:|---:|
| 01 | Code generation and edge cases | 1 | 4 | 4 | 3 |
| 02 | Constraint logic | 3 | 4 | 4 | 0 |
| 03 | Multi-step arithmetic | 2 | 4 | 4 | 4 |
| 04 | Action extraction and JSON | 2 | 2 | 3 | 2 |
| 05 | Long-context retrieval | 1 | 4 | 4 | 3 |
| 06 | Follow-up conversation context | 0 | 3 | 3 | 3 |
| 07 | Strict JSON schema | 4 | 4 | 4 | 4 |
| 08 | Similar-person disambiguation | 0 | 3 | 1 | 1 |
| 09 | Grounded executive synthesis | 4 | 3 | 3 | 3 |
| 10 | Writing for two audiences | 2 | 3 | 4 | 3 |

## What the results say

- **Fastest:** `main`, by a wide margin. It is not safe as the only assistant
  model in this test: it miscalculated a time conversion, mixed conversation
  context, guessed a person's identity, and returned malformed JSON once.
- **Best quality/latency balance:** `qwen-eval`. It led or tied in seven cases,
  answered the ambiguity case without assigning the task to the wrong person,
  and was much faster than the other high-scoring model.
- **Best code and grounded long-context results:** `qwen-eval` and `lfm-2.6b`
  both scored 4/4 on those cases. LFM2.5 2.6B used 1,459 completion tokens for
  code and took 78.2 seconds; Qwen3 used 250 tokens and took 24.8 seconds. The
  generated code was syntax-checked, not executed.
- **Weakest logic result:** Qwen2.5-7B assigned Alba to 11:00 despite the prompt
  forbidding it, then claimed the answer satisfied the constraint.
- **Weakest uncertainty handling:** `main`, LFM2.5 2.6B and Qwen2.5-7B all
  chose Ana Pereda despite insufficient evidence. Qwen3 recognized the
  ambiguity but could have asked a direct clarification question.
- **Structured data:** the strict-schema test passed for all four. In the
  action-extraction case, `main` produced malformed JSON; Qwen3 and Qwen2.5
  invented the year 2023 and omitted known statuses. Seven of eight JSON-only
  responses parsed successfully.

## Test design and method

The fixed, synthetic suite is in
[`benchmarks/llm-capabilities/v1/cases.json`](../benchmarks/llm-capabilities/v1/cases.json).
It covers code, constraint logic, arithmetic, extraction, long context,
multi-turn follow-up, strict JSON, uncertainty/person identity, executive
synthesis, and audience adaptation. It contains no Jira, Granola, customer, or
employee data.

Each case ran once on each model: 40 serial requests total. The alias order
rotated between cases. All used the same Spanish system prompt, temperature 0,
seed `20261016`, a 1,536-token completion ceiling and disabled prompt caching.
The high ceiling was selected after a lower-cap pilot showed LFM2.5 2.6B
exhausting its output budget in reasoning before producing an answer. The final
results therefore compare complete visible answers where possible; one LFM
ambiguity response still reached the ceiling. Reasoning content was counted by
characters but not saved. The runner records visible response text, status,
latency, first generated token, token usage and finish reason in
[`benchmarks/llm-capabilities/results-2026-10-16.jsonl`](../benchmarks/llm-capabilities/results-2026-10-16.jsonl).

Scores use the per-case rubric and a manual 0–4 scale: 0 wrong/unusable, 1
major errors, 2 partly correct with an important omission, 3 correct with
minor omissions, and 4 fully correct, grounded and format-compliant. They are
human judgments, not an LLM-as-judge score. Full case scores and reasons are in
[`benchmarks/llm-capabilities/v1/scores-2026-10-16.json`](../benchmarks/llm-capabilities/v1/scores-2026-10-16.json).

The context tests send prior turns or a synthetic register in the request. They
measure context following, **not** Donna's persistent memory, RAG, connector
selection, Jira/Granola API retrieval, or tool use. Those need a separate
end-to-end suite.

The four models were resident in one router during the run, so memory is a
shared service cost and cannot be attributed per model. At the end of the run,
the router used about 9.45 GiB, the host had 13.47 GiB available, and the router
remained active. All 40 requests returned HTTP 200; the benchmark made no
concurrent calls and changed no production service.

## Reproducing the run

Run the benchmark on the lab host so the API key remains in its protected file:

```bash
sudo python3 scripts/benchmark-llm-capabilities.py \
  --models main,qwen-eval,lfm-2.6b,qwen25-7b-q4_0 \
  --api-key-file /etc/llama-cpp/router-api-key \
  --output results.jsonl
```

The runner is generic for future OpenAI-compatible model aliases. Add a new
alias to the `--models` list and keep the suite version fixed for direct
comparisons.

## References

- [Liquid AI LFM2.5-2.6B model card](https://huggingface.co/LiquidAI/LFM2.5-2.6B) — describes the model as a reasoning model that thinks before answering.
- [llama.cpp server options](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) — documents reasoning formats and streaming response behavior.
