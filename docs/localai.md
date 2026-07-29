# LocalAI Evaluation Runtime

LocalAI is an optional evaluation service installed alongside the four existing
`llama.cpp` instances. It does not replace them or become part of the canonical
orchestrator architecture.

## Deployment

The role uses the version-pinned `localai/localai:v4.7.1` image and installs the
small, CPU-capable `qwen3-4b` gallery model by default. Model, configuration,
and application data persist under `/srv/ai-lab/localai`.

Configure `.env` without committing the real API key:

```bash
LAB_LOCALAI_ENABLED=true
LAB_LOCALAI_IMAGE=localai/localai:v4.7.1
LAB_LOCALAI_BIND_ADDRESS=127.0.0.1
LAB_LOCALAI_PORT=8181
LAB_LOCALAI_DOMAIN=localai.stratecode.local
LAB_LOCALAI_API_KEY=<random-secret-with-at-least-24-characters>
LAB_LOCALAI_DEFAULT_MODEL=qwen3-4b
LAB_LOCALAI_MEMORY_LIMIT=6g
LAB_LOCALAI_CPU_LIMIT=4.0
```

Load the environment and apply the main playbook:

```bash
set -a
source .env
set +a
ansible-playbook playbooks/bootstrap.yml --syntax-check
ansible-playbook playbooks/bootstrap.yml
```

The first run downloads the pinned container image and model. Later runs reuse
the persistent model directory.

## Access

The backend listens only on `127.0.0.1:8181`. Nginx exposes:

```text
https://localai.stratecode.local
```

Nginx permits loopback, the configured LAN, the Docker internal network, and
WireGuard. Other source networks receive `403`.

Add the appropriate lab address on each client:

```text
192.168.1.100 localai.stratecode.local
```

Use the real LAN address when connected locally or the reachable lab address
when connected through WireGuard.

The role creates:

- `/etc/ssl/certs/localai.stratecode.local.crt`
- `/etc/ssl/private/localai.stratecode.local.key`

Copy and trust only the certificate, never the private key. Until the
certificate is trusted, browsers will show a local-certificate warning. The
verification script accepts `LAB_LOCALAI_CA_CERT=/path/to/certificate.crt`;
without it, the script uses `curl --insecure` for this bounded self-signed
evaluation.

## Authentication

All LocalAI API and web access requires `LAB_LOCALAI_API_KEY`. The key is stored
in the root-readable runtime environment file and is never printed by the
verifier.

Simple API-key mode grants broad administrative access. Keep the endpoint
limited to LAN and WireGuard. Do not forward TCP ports `8181`, `80`, or `443`
from the public Internet for this service.

## Verification

Repository checks:

```bash
bats tests/properties/prop_localai_ansible_contract.bats
ansible-playbook playbooks/bootstrap.yml --syntax-check
ansible-playbook playbooks/cleanup-localai-baseline.yml --syntax-check
bash -n scripts/verify-localai.sh
git diff --check
```

Live API acceptance:

```bash
set -a
source .env
set +a
scripts/verify-localai.sh --base-url https://localai.stratecode.local
```

The verifier proves:

- anonymous API access is rejected;
- authenticated readiness succeeds;
- the configured model is present;
- chat completion returns content;
- a fixed tool request returns structured `tool_calls`.

Also inspect the listening socket and container:

```bash
ss -ltnp | grep ':8181'
docker inspect localai
```

The expected socket is `127.0.0.1:8181`, never `0.0.0.0:8181`.

## Coexistence with llama.cpp

Before and after deployment:

```bash
systemctl is-active llama-cpp-code.service
systemctl is-active llama-cpp-planner.service
systemctl is-active llama-cpp-utility.service
systemctl is-active llama-cpp-embeddings.service
```

LocalAI uses a separate port, container, storage tree, limits, certificate, and
Nginx site. Its handlers do not notify or restart `llama.cpp`.

## AMD and ROCm

The initial profile is CPU-only. AMD acceleration remains unverified until all
of the following agree:

- the host GPU architecture is supported by the selected LocalAI image;
- the device is visible inside the container;
- LocalAI loads the intended accelerated backend;
- a real inference request succeeds;
- the existing `llama.cpp` services stay within their resource budgets.

Do not infer GPU acceleration merely from a healthy container or installed ROCm
packages.

## Disable and cleanup

Set `LAB_LOCALAI_ENABLED=false` to stop managing LocalAI in bootstrap. This does
not delete data or automatically remove the running evaluation container.

To remove the container and Nginx exposure explicitly:

```bash
export LAB_LOCALAI_CLEANUP_ENABLED=true
ansible-playbook playbooks/cleanup-localai-baseline.yml
```

Cleanup retains `/srv/ai-lab/localai`, certificates, configuration, and model
data. Deleting retained data or secrets requires a separate explicit retention
decision.

After any deployment or cleanup change, apply the same playbook a second time
and require `changed=0` and `failed=0`.
