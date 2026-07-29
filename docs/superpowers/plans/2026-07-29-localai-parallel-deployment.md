# LocalAI Parallel Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install an authenticated LocalAI evaluation service through Ansible, reachable at `https://localai.stratecode.local` from LAN and WireGuard, without changing the existing `llama.cpp` services.

**Architecture:** A dedicated Ansible role runs a pinned LocalAI container bound only to loopback and places an authenticated, network-restricted Nginx TLS proxy in front of it. Bootstrap enables the role explicitly, while cleanup removes only the runtime exposure and retains model data.

**Tech Stack:** Ansible, Docker CLI, Nginx, OpenSSL, LocalAI container API, Bats, Python 3 contract helpers.

## Global Constraints

- Do not remove, replace, restart, or reconfigure any of the four `llama.cpp` services.
- Do not touch secrets in `.env`, `group_vars/vault.yml`, or `ssh/`.
- Default `LAB_LOCALAI_ENABLED` to `false`.
- Bind the LocalAI backend only to `127.0.0.1`.
- Permit HTTPS clients only from loopback, the configured LAN CIDR, Docker internal CIDR, and WireGuard subnet.
- Use an explicitly pinned LocalAI image tag; never use `latest`.
- Use a small CPU-capable, tool-calling model for initial validation.
- Treat AMD/ROCm acceleration as unverified until device visibility, backend logs, and inference all succeed.
- Preserve LocalAI model data during ordinary disablement and cleanup.
- Run the repository's real Ansible and shell verification paths before completion.
- Preserve all unrelated dirty-worktree changes.

---

## File Map

- `tests/properties/prop_localai_ansible_contract.bats` — executable deployment-contract tests.
- `tests/fixtures/localai-role.yml` — local check-mode fixture for the role.
- `group_vars/all.yml` — environment-backed LocalAI configuration.
- `.env.example` — non-secret operator inputs and placeholders.
- `roles/localai/tasks/main.yml` — validation, storage, container, certificate, Nginx, health, and model readiness.
- `roles/localai/handlers/main.yml` — safe Nginx validation/reload and LocalAI restart.
- `roles/localai/templates/localai.env.j2` — root-readable runtime environment.
- `roles/localai/templates/localai-nginx.conf.j2` — private TLS reverse proxy.
- `playbooks/bootstrap.yml` — opt-in role entry.
- `playbooks/cleanup-localai-baseline.yml` — remove LocalAI runtime exposure while retaining data.
- `scripts/verify-localai.sh` — authenticated live acceptance checks without printing credentials.
- `docs/localai.md` — installation, access, hosts-file, certificate, validation, AMD status, and rollback runbook.
- `README.md` — concise link and enablement summary.

### Task 1: Configuration and Bootstrap Contract

**Files:**
- Create: `tests/properties/prop_localai_ansible_contract.bats`
- Modify: `group_vars/all.yml`
- Modify: `.env.example`
- Modify: `playbooks/bootstrap.yml`

**Interfaces:**
- Consumes: existing `server_lan_cidr`, `docker_internal_cidr`, `wireguard_enabled`, and `wireguard_subnet` variables.
- Produces: `localai_enabled`, `localai_image`, `localai_bind_address`, `localai_port`, `localai_domain`, `localai_api_key`, storage-path, resource-limit, and model variables consumed by `roles/localai`.

- [ ] **Step 1: Write the failing configuration contract**

Create `tests/properties/prop_localai_ansible_contract.bats`:

```bash
#!/usr/bin/env bats

setup() {
  repo_root="$(cd "$(dirname "$BATS_TEST_FILENAME")/../.." && pwd)"
}

@test "LocalAI defaults are opt-in pinned and loopback-only" {
  run python3 - "$repo_root" <<'PY'
from pathlib import Path
import sys

root = Path(sys.argv[1])
vars_text = (root / "group_vars/all.yml").read_text()
env_text = (root / ".env.example").read_text()
bootstrap = (root / "playbooks/bootstrap.yml").read_text()

required = [
    "localai_enabled:",
    "localai_image:",
    "localai_bind_address:",
    "localai_port:",
    "localai_domain:",
    "localai_api_key:",
    "localai_models_dir:",
    "localai_config_dir:",
    "localai_data_dir:",
    "localai_default_model:",
]
missing = [value for value in required if value not in vars_text]
if missing:
    raise SystemExit("missing vars: " + ", ".join(missing))
if "LAB_LOCALAI_ENABLED') | default('false', true) | bool" not in vars_text:
    raise SystemExit("LocalAI must default disabled")
if "LAB_LOCALAI_BIND_ADDRESS') | default('127.0.0.1', true)" not in vars_text:
    raise SystemExit("LocalAI must default to loopback")
image_line = next(line for line in vars_text.splitlines() if line.startswith("localai_image:"))
if ":latest" in image_line:
    raise SystemExit("LocalAI image must be pinned")
if "name: localai" not in bootstrap:
    raise SystemExit("bootstrap missing localai role")
if "when: localai_enabled" not in bootstrap:
    raise SystemExit("bootstrap LocalAI role must be opt-in")
if "LAB_LOCALAI_API_KEY=replace-with-random-hex" not in env_text:
    raise SystemExit("example API key placeholder missing")
PY
  [ "$status" -eq 0 ]
}
```

- [ ] **Step 2: Run the contract and verify RED**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: FAIL with `missing vars` because LocalAI configuration does not exist.

- [ ] **Step 3: Add the minimum environment-backed variables**

Add a focused LocalAI section to `group_vars/all.yml`. Use these names and
defaults:

```yaml
localai_enabled: "{{ lookup('env', 'LAB_LOCALAI_ENABLED') | default('false', true) | bool }}"
localai_image: "{{ lookup('env', 'LAB_LOCALAI_IMAGE') | default('localai/localai:v4.7.1', true) }}"
localai_container_name: localai
localai_bind_address: "{{ lookup('env', 'LAB_LOCALAI_BIND_ADDRESS') | default('127.0.0.1', true) }}"
localai_port: "{{ lookup('env', 'LAB_LOCALAI_PORT') | default('8181', true) | int }}"
localai_domain: "{{ lookup('env', 'LAB_LOCALAI_DOMAIN') | default('localai.stratecode.local', true) }}"
localai_api_key: "{{ lookup('env', 'LAB_LOCALAI_API_KEY') | default('', true) }}"
localai_root_dir: /srv/ai-lab/localai
localai_models_dir: "{{ localai_root_dir }}/models"
localai_config_dir: "{{ localai_root_dir }}/config"
localai_data_dir: "{{ localai_root_dir }}/data"
localai_env_file: "{{ localai_config_dir }}/localai.env"
localai_ssl_certificate: "/etc/ssl/certs/{{ localai_domain }}.crt"
localai_ssl_certificate_key: "/etc/ssl/private/{{ localai_domain }}.key"
localai_default_model: "{{ lookup('env', 'LAB_LOCALAI_DEFAULT_MODEL') | default('qwen3-4b', true) }}"
localai_memory_limit: "{{ lookup('env', 'LAB_LOCALAI_MEMORY_LIMIT') | default('6g', true) }}"
localai_cpu_limit: "{{ lookup('env', 'LAB_LOCALAI_CPU_LIMIT') | default('4.0', true) }}"
```

The `v4.7.1` version is the explicit versioned image documented by LocalAI on
2026-07-29. Confirm the manifest is available for the server architecture before
deployment and record that evidence in `docs/localai.md`.

Add matching placeholders to `.env.example`; never add a real key.

- [ ] **Step 4: Add the opt-in bootstrap entry**

Add immediately after the `llama_cpp` role:

```yaml
    - name: Install LocalAI evaluation runtime
      ansible.builtin.import_role:
        name: localai
      when: localai_enabled
```

- [ ] **Step 5: Run the contract and verify GREEN**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: `1 test, 0 failures`.

- [ ] **Step 6: Commit the configuration contract**

```bash
git add tests/properties/prop_localai_ansible_contract.bats group_vars/all.yml .env.example playbooks/bootstrap.yml
git commit -m "feat: define opt-in LocalAI deployment"
```

### Task 2: Isolated LocalAI Runtime

**Files:**
- Modify: `tests/properties/prop_localai_ansible_contract.bats`
- Create: `roles/localai/tasks/main.yml`
- Create: `roles/localai/handlers/main.yml`
- Create: `roles/localai/templates/localai.env.j2`

**Interfaces:**
- Consumes: variables defined in Task 1 and the host Docker CLI installed by the server baseline.
- Produces: container `localai`, loopback endpoint `http://127.0.0.1:8181`, and persistent model/config/data directories.

- [ ] **Step 1: Add a failing executable runtime contract**

Append a Bats test that renders the role in Ansible check mode against localhost:

```bash
@test "LocalAI role validates secrets and renders a loopback container" {
  run bash -lc "cd '$repo_root' && ansible-playbook \
    -i 'localhost,' -c local \
    tests/fixtures/localai-role.yml \
    --check \
    -e localai_api_key=test-only-key"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Validate LocalAI configuration"* ]]
  [[ "$output" == *"Install LocalAI runtime environment"* ]]
  [[ "$output" == *"Reconcile LocalAI container"* ]]
}
```

Create `tests/fixtures/localai-role.yml` with `become: false`, temporary paths
under `/tmp/localai-ansible-contract`, and `localai_enabled: true`. Override the
container task during check mode so it validates the exact argument list but
does not require a running Docker daemon.

- [ ] **Step 2: Run the runtime contract and verify RED**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: FAIL because `roles/localai/tasks/main.yml` does not exist.

- [ ] **Step 3: Implement validation and persistent directories**

In `roles/localai/tasks/main.yml`, assert:

```yaml
- name: Validate LocalAI configuration
  ansible.builtin.assert:
    that:
      - localai_api_key | length >= 24
      - localai_bind_address == '127.0.0.1'
      - localai_port not in [8080, 8082, 8083, 8084, 8100, 8180, 8221, 8222, 18789]
      - localai_image is not search(':latest$')
      - localai_domain | length > 0
    fail_msg: LocalAI requires a strong API key, loopback binding, pinned image, unique port, and domain.
```

Create the root, models, config, and data directories with root ownership and
mode `0750`. Do not remove them when `localai_enabled` becomes false.

- [ ] **Step 4: Render a root-readable runtime environment**

Create `roles/localai/templates/localai.env.j2`:

```dotenv
LOCALAI_ADDRESS=0.0.0.0:8080
LOCALAI_API_KEY={{ localai_api_key }}
LOCALAI_MODELS_PATH=/models
LOCALAI_CONFIG_DIR=/config
LOCALAI_DATA_PATH=/data
```

Install it with mode `0600` and `no_log: true`.

- [ ] **Step 5: Reconcile the pinned container**

Use `ansible.builtin.command` with an argv list, not a shell string:

```yaml
- name: Reconcile LocalAI container
  ansible.builtin.command:
    argv:
      - docker
      - run
      - --detach
      - --name
      - "{{ localai_container_name }}"
      - --restart
      - unless-stopped
      - --env-file
      - "{{ localai_env_file }}"
      - --publish
      - "{{ localai_bind_address }}:{{ localai_port }}:8080"
      - --volume
      - "{{ localai_models_dir }}:/models"
      - --volume
      - "{{ localai_config_dir }}:/config"
      - --volume
      - "{{ localai_data_dir }}:/data"
      - --memory
      - "{{ localai_memory_limit }}"
      - --cpus
      - "{{ localai_cpu_limit }}"
      - "{{ localai_image }}"
```

Precede it with read-only container/image inspection. Remove and recreate the
container only when the inspected image, port binding, environment-file
checksum, mounts, or limits differ. Mark unchanged containers with
`changed_when: false`. Do not use `docker rm -v`; persistent host directories
must survive.

- [ ] **Step 6: Add bounded runtime readiness**

Poll `http://127.0.0.1:{{ localai_port }}/readyz` with the API key for at most
180 seconds. If the pinned release exposes a different documented readiness
endpoint, encode that exact endpoint in `localai_readiness_path` and test it.
Failure must mention LocalAI only and must not notify a `llama.cpp` handler.

- [ ] **Step 7: Run the runtime contract and syntax check**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
ansible-playbook playbooks/bootstrap.yml --syntax-check
```

Expected: all Bats tests pass and Ansible exits `0`.

- [ ] **Step 8: Commit the isolated runtime**

```bash
git add tests/properties/prop_localai_ansible_contract.bats tests/fixtures/localai-role.yml roles/localai
git commit -m "feat: provision isolated LocalAI runtime"
```

### Task 3: Private HTTPS and Cleanup

**Files:**
- Modify: `tests/properties/prop_localai_ansible_contract.bats`
- Create: `roles/localai/templates/localai-nginx.conf.j2`
- Modify: `roles/localai/tasks/main.yml`
- Modify: `roles/localai/handlers/main.yml`
- Modify: `playbooks/cleanup-localai-baseline.yml`

**Interfaces:**
- Consumes: LocalAI loopback endpoint from Task 2 and network CIDRs from global variables.
- Produces: `https://localai.stratecode.local`, a self-signed SAN certificate, and a cleanup path that retains data.

- [ ] **Step 1: Add a failing Nginx and cleanup behavior contract**

Append a test that renders `localai-nginx.conf.j2` with Ansible's `template`
lookup and asserts literal rendered behavior:

```python
assert "server_name localai.stratecode.local;" in rendered
assert "proxy_pass http://127.0.0.1:8181;" in rendered
assert "allow 192.168.1.0/24;" in rendered
assert "allow 10.66.66.0/24;" in rendered
assert "deny all;" in rendered
assert "proxy_buffering off;" in rendered
assert "proxy_read_timeout 300s;" in rendered
```

Also parse `playbooks/cleanup-localai-baseline.yml` and require the LocalAI
container and Nginx site to be absent while `/srv/ai-lab/localai/models` is not
listed in any destructive runtime-path loop.

- [ ] **Step 2: Run the HTTPS contract and verify RED**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: FAIL because the Nginx template and cleanup declarations are absent.

- [ ] **Step 3: Add certificate and Nginx tasks**

Generate an RSA 4096 self-signed certificate with:

```text
CN=localai.stratecode.local
subjectAltName=DNS:localai.stratecode.local
```

Use `creates:` for idempotency. Install and enable
`/etc/nginx/sites-available/localai.conf`. Validate `nginx -t` before reload.

Create `roles/localai/templates/localai-nginx.conf.j2` with HTTP-to-HTTPS
redirect and:

```nginx
allow 127.0.0.1;
allow ::1;
allow {{ server_lan_cidr }};
allow {{ docker_internal_cidr }};
{% if wireguard_enabled %}
allow {{ wireguard_subnet }};
{% endif %}
deny all;

location / {
    proxy_pass http://{{ localai_bind_address }}:{{ localai_port }};
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_http_version 1.1;
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_cache off;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
    proxy_connect_timeout 60s;
    gzip off;
    add_header X-Accel-Buffering no always;
}
```

- [ ] **Step 4: Add cleanup without data deletion**

Extend `playbooks/cleanup-localai-baseline.yml` to:

- stop and remove only the `localai` container;
- remove `/etc/nginx/sites-enabled/localai.conf`;
- remove `/etc/nginx/sites-available/localai.conf`;
- validate and reload Nginx;
- verify the backend port is closed;
- leave `/srv/ai-lab/localai/models`, configuration, certificates, and data
  untouched.

Guard this behavior with a clear cleanup variable so the evaluation baseline can
either retain a running LocalAI evaluation or reconcile it absent intentionally.

- [ ] **Step 5: Run contracts and syntax validation**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
ansible-playbook playbooks/bootstrap.yml --syntax-check
ansible-playbook playbooks/cleanup-localai-baseline.yml --syntax-check
```

Expected: all commands exit `0`.

- [ ] **Step 6: Commit HTTPS and cleanup**

```bash
git add tests/properties/prop_localai_ansible_contract.bats roles/localai playbooks/cleanup-localai-baseline.yml
git commit -m "feat: expose LocalAI through private TLS"
```

### Task 4: Live Verification and Operator Runbook

**Files:**
- Create: `scripts/verify-localai.sh`
- Create: `docs/localai.md`
- Modify: `README.md`
- Modify: `tests/properties/prop_localai_ansible_contract.bats`

**Interfaces:**
- Consumes: `LAB_LOCALAI_API_KEY`, `LAB_LOCALAI_DOMAIN`, and the deployed HTTPS/API surfaces.
- Produces: reproducible acceptance evidence and operator instructions.

- [ ] **Step 1: Write a failing verifier self-test**

Add a Bats test that runs the verifier against a temporary local HTTP fixture
which implements:

- `401` without `Authorization`;
- `200` for `/readyz` with the test key;
- a model list containing the configured model;
- a chat response;
- a structured tool-call response.

The fixture must record requests but never print the key. The test fails before
the verifier exists.

- [ ] **Step 2: Run the verifier contract and verify RED**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: FAIL with `scripts/verify-localai.sh: No such file`.

- [ ] **Step 3: Implement the verifier**

Create `scripts/verify-localai.sh` with `set -euo pipefail`. Require the key from
the environment, use `curl --fail-with-body`, and verify:

1. unauthenticated `/v1/models` is rejected;
2. authenticated readiness succeeds;
3. the configured model appears in `/v1/models`;
4. `/v1/chat/completions` returns assistant content;
5. a fixed weather-tool request returns a structured `tool_calls` array.

Accept `--base-url` so the same script can target the local fixture and the real
HTTPS domain. Never enable shell tracing and never echo the authorization
header.

- [ ] **Step 4: Run shell and contract verification**

Run:

```bash
bash -n scripts/verify-localai.sh
bats tests/properties/prop_localai_ansible_contract.bats
```

Expected: both commands exit `0`.

- [ ] **Step 5: Write the operator documentation**

Document in `docs/localai.md`:

- enablement variables;
- selected pinned version and model;
- `hosts` entry using the lab LAN or WireGuard IP;
- certificate retrieval and trust;
- API-key handling;
- local, LAN, WireGuard, and HTTPS verification;
- model installation;
- explicit CPU/AMD validation status;
- disabling and cleanup;
- retained data;
- no change to `llama.cpp`.

Link the runbook from `README.md`.

- [ ] **Step 6: Run full repository verification**

Run:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
ansible-playbook playbooks/bootstrap.yml --syntax-check
ansible-playbook playbooks/cleanup-localai-baseline.yml --syntax-check
bash -n scripts/verify-localai.sh
git diff --check
```

If bridge, orchestrator, or gateway behavior was changed unexpectedly, also run
`./scripts/test-orchestrator-go.sh`. Otherwise record that it is out of scope
because this feature changes only infrastructure deployment.

- [ ] **Step 7: Apply to the live host**

Load the existing inventory environment without printing it. Record:

```bash
systemctl is-active llama-cpp-code.service
systemctl is-active llama-cpp-planner.service
systemctl is-active llama-cpp-utility.service
systemctl is-active llama-cpp-embeddings.service
```

Run bootstrap with `LAB_LOCALAI_ENABLED=true`, then:

```bash
ss -ltnp
docker inspect localai
scripts/verify-localai.sh --base-url https://localai.stratecode.local
```

Use the existing certificate-aware curl mechanism. Do not put the key into shell
history; load `LAB_LOCALAI_API_KEY` from the existing environment/vault flow
before invoking the verifier.

- [ ] **Step 8: Verify persistence, coexistence, and idempotency**

Restart only the LocalAI container. Re-run the verifier and confirm the model
remains available. Re-check all four `llama.cpp` services and their HTTP health
endpoints. Apply bootstrap a second time and require `changed=0`, `failed=0`.

- [ ] **Step 9: Commit documentation and verification**

```bash
git add scripts/verify-localai.sh docs/localai.md README.md tests/properties/prop_localai_ansible_contract.bats
git commit -m "docs: add LocalAI operations and verification"
```

## Final Review

- [ ] Re-read `docs/superpowers/specs/2026-07-29-localai-parallel-deployment-design.md`.
- [ ] Confirm every acceptance criterion has fresh evidence.
- [ ] Confirm `git status --short` contains no accidental user-owned files.
- [ ] Confirm every task-specific commit excludes pre-existing unrelated edits.
- [ ] Record exact LocalAI version, model, CPU/GPU mode, test counts, live URLs,
  idempotency recap, and any unverified external-client access.
