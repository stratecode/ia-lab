# llama.cpp multi-model router evaluation

This evaluation runs one additional `llama-server` in router mode on
`127.0.0.1:8185`. It preloads four aliases and keeps the current production
`llama-cpp-main`, `llama-cpp-qwen-eval`, `lab-secretary`, and Open WebUI
configuration untouched. The experimental endpoint is authenticated and is
not exposed outside the host.

| Alias | Model | Startup state | Context | Threads |
|---|---|---:|---:|---:|
| `main` | LFM2.5 1.2B Instruct Q4_0 | loaded | 16,384 | 8 |
| `qwen-eval` | Qwen3 4B Q4_K_M | loaded | 8,192 | 6 |
| `lfm-2.6b` | LFM2.5 2.6B Q4_K_M | loaded | 8,192 | 6 |
| `qwen25-7b-q4_0` | Qwen2.5 7B Instruct Q4_0 | loaded | 8,192 | 6 |

The router caps itself at four models, disables implicit model autoloading,
uses CPU only, and has a 12 GiB systemd memory limit. The additional LFM file is
downloaded from a pinned Hugging Face revision and checked against its SHA-256
before use. Qwen2.5 is the official two-shard GGUF, pinned to a separate
revision and checksummed per shard. On the lab host, the preflight observed
30.3 GiB total RAM and 18 GiB available; those values are a snapshot, not a
capacity guarantee.
The service keeps `ProtectHome=true` and uses its systemd state directory for
Hugging Face and shader caches.

## Deploy and verify

```bash
LAB_HOSTNAME=stratecode.local LAB_USER=stratecode \
  ansible-playbook playbooks/run-llama-router-eval.yml
```

The playbook creates a separate random API key on the lab host and keeps it in
root/service-owned files. It never places that key in Git, a command-line
argument or task output. It checks the four model statuses, sends one short
generation (up to 512 output tokens, enough for LFM 2.6B to finish its thought
and produce visible text) through each alias, and verifies the alias-scoped
`/metrics?model=<alias>` endpoints.

The OpenAI-compatible API is available at
`http://127.0.0.1:8185/v1/chat/completions`. Requests select their backend with
the usual `model` field (`main`, `qwen-eval`, `lfm-2.6b`, or
`qwen25-7b-q4_0`). For a manual
loopback probe on the host, use the existing authenticated local tooling; do
not place the API key in shell history or command-line arguments.

## Prometheus and Grafana

`roles/observability` adds four authenticated scrape jobs, each querying the
router with its own `model` parameter. This preserves per-alias time series
for `llamacpp:prompt_tokens_total`, `llamacpp:tokens_predicted_total`, prompt
and generation throughput, and the processing/deferred request gauges. The
LLM latency dashboard now compares prompt and generation throughput for the
four router aliases. These metrics describe llama.cpp activity, not answer
quality; the secretary's existing application-level metrics remain separate.

Apply the scrape configuration only after reviewing its dry-run diff:

```bash
LAB_HOSTNAME=stratecode.local LAB_USER=stratecode \
  ansible-playbook --check --diff playbooks/deploy-observability.yml
```

The router can be stopped without changing the existing endpoints:

```bash
ansible -i inventory.yml lab -b -m ansible.builtin.systemd \
  -a 'name=llama-cpp-router-eval state=stopped enabled=false' \
  -e LAB_HOSTNAME=stratecode.local -e LAB_USER=stratecode
```

## Pinned model artifact

- Repository: `LiquidAI/LFM2.5-2.6B-GGUF`.
- Revision: `e7caca5d835a3901a8e0d63e94009429bafafdfc`.
- File: `LFM2.5-2.6B-Q4_K_M.gguf` (1,674,455,040 bytes).
- SHA-256: `02a8b7e17487d326e46d68ce0ba24211e1b80a14c4cd0597fa73c1cd697f52ed`.
- Source: [official model artifact](https://huggingface.co/LiquidAI/LFM2.5-2.6B-GGUF/blob/e7caca5d835a3901a8e0d63e94009429bafafdfc/LFM2.5-2.6B-Q4_K_M.gguf).
- Qwen repository: `Qwen/Qwen2.5-7B-Instruct-GGUF`.
- Qwen revision: `bb5d59e06d9551d752d08b292a50eb208b07ab1f`.
- Shard 1: `qwen2.5-7b-instruct-q4_0-00001-of-00002.gguf`, SHA-256 `09cda9ec3aff8b5d3a760ddd16d26351e2dbf01f5c4ad4d05e18ac666708a6a2`.
- Shard 2: `qwen2.5-7b-instruct-q4_0-00002-of-00002.gguf`, SHA-256 `db92b5c30da42fbfa33af1c501f4be6fcba1c85348d879d794b475154c0fe564`.
- Source: [official Qwen2.5-7B GGUF repository](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct-GGUF/tree/bb5d59e06d9551d752d08b292a50eb208b07ab1f); llama.cpp loads a split GGUF using its first shard, as shown in [its split-model test](https://github.com/ggml-org/llama.cpp/blob/master/tools/gguf-split/tests.sh).
