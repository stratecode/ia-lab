# Qwen3 evaluation beside the principal model

This is a temporary, reproducible CPU evaluation profile. It keeps the
production `llama-cpp-main` service and Open WebUI connection unchanged.

## Pinned artifact

- Model: official `Qwen/Qwen3-4B-GGUF`, `Qwen3-4B-Q4_K_M.gguf`.
- Revision: `bc640142c66e1fdd12af0bd68f40445458f3869b`.
- Size: 2,497,280,256 bytes.
- SHA-256: `7485fe6f11af29433bc51cab58009521f205840f5b4ae3a32fa7f92e8534fdf5`.
- Runtime: existing pinned llama.cpp build, CPU only, loopback port 8184.
- Alias: `qwen-eval`; context 8,192; six inference threads; one slot.
- Resource guardrails: 6 GiB MemoryMax and a 6 CPU cores quota. The model
  generation thread settings are eight for LFM and six for Qwen; this is a
  configuration budget, not a hard aggregate CPU cap, since the principal
  service retains its existing scheduling policy.
- Lifecycle: started for the experiment, but disabled for automatic startup.
- Data directory: `/srv/ai-lab/llama.cpp/models`; no Open WebUI or secretary
  routing changes are made.

The official Qwen model page lists the GGUF artifact and its hash, and the Qwen
documentation describes its official GGUFs and llama.cpp support:
[Qwen3-4B GGUF](https://huggingface.co/Qwen/Qwen3-4B-GGUF/blob/bc640142c66e1fdd12af0bd68f40445458f3869b/Qwen3-4B-Q4_K_M.gguf),
[Qwen llama.cpp guide](https://github.com/QwenLM/Qwen3/blob/main/docs/source/run_locally/llama.cpp.md).

## Deploy and verify

```bash
LAB_HOSTNAME=stratecode.local LAB_USER=stratecode \
  ansible-playbook playbooks/run-qwen3-eval.yml
```

The playbook checks the host profile, downloads with checksum verification,
starts a separate loopback-only systemd service, and verifies model-list and
Spanish-generation requests. It does not alter the main service, read or deploy
API tokens, change Open WebUI, remove retired model paths, or edit persistent
principal-model configuration.

To stop the Qwen process while keeping its verified weights:

```bash
ansible -i inventory.yml lab -b -m ansible.builtin.systemd \
  -a 'name=llama-cpp-qwen-eval state=stopped enabled=false' \
  -e LAB_HOSTNAME=stratecode.local -e LAB_USER=stratecode
```

To start it again without changing the principal model:

```bash
ansible -i inventory.yml lab -b -m ansible.builtin.systemd \
  -a 'name=llama-cpp-qwen-eval state=started enabled=false' \
  -e LAB_HOSTNAME=stratecode.local -e LAB_USER=stratecode
```

Both APIs are intended to be benchmarked with the same bounded Jira-intent
cases. Keep results separate from production telemetry: the experimental API is
not connected to Open WebUI or the secretary.

## Initial live preflight (2026-10-15)

- The official Qwen file downloaded on the lab and matched the pinned SHA-256.
- `llama-cpp-qwen-eval` is active on loopback; `llama-cpp-main` remained active.
- Qwen's cgroup reported about 3.0 GiB in use after model load, under its 6 GiB
  limit. Its service is disabled for automatic startup.
- A three-request synthetic extraction batch returned JSON in 6.14, 11.78 and
  16.31 seconds. Requests queued on the Qwen server's single slot. It captured
  Jira project, assignee and date in the direct Jira example, but mislabeled a
  Granola-only question as `both`, and one null assignee came back as the string
  `"null"`. Treat this as a smoke probe, not a quality score.
- The direct LFM comparison did not run: the lab rejected the no-secret test
  request with HTTP 401. The existing LFM credential was deliberately not read
  from its protected configuration. Rerun the paired benchmark only through an
  operator-approved credential injection path.
