# LocalAI Parallel Deployment Design

Date: 2026-07-29

## Objective

Add LocalAI to the StrateCode lab as an optional, reversible evaluation service
without removing, replacing, or reconfiguring the four existing `llama.cpp`
instances.

The deployment must support:

- local access from the server;
- private access from the configured LAN and WireGuard networks;
- HTTPS access through `localai.stratecode.local`;
- authenticated API and web access;
- repeatable installation through the main Ansible bootstrap;
- explicit health, persistence, security, and idempotency verification.

## Scope

This change covers the LocalAI runtime, its persistent storage, Nginx and TLS
integration, configuration variables, bootstrap wiring, documentation, and
deployment verification.

It does not:

- remove or modify any `llama.cpp` service, model, port, or resource policy;
- route the orchestrator, Codex gateway, OpenClaw, or other consumers through
  LocalAI;
- expose the LocalAI backend port directly outside the host;
- promise AMD acceleration before runtime compatibility is verified;
- install image, video, speech, or other heavyweight model families during the
  initial evaluation;
- make LocalAI part of the canonical product architecture.

## Selected Approach

Deploy LocalAI as a version-pinned container managed by a dedicated Ansible
role.

This approach is preferred over a native binary because it isolates LocalAI's
backend dependencies and makes removal and version changes predictable. It is
preferred over embedding LocalAI in the orchestrator deployment because the
current goal is infrastructure evaluation, not a product-runtime migration.

## Architecture

### Runtime

The `localai` role will manage one LocalAI container with:

- an explicitly pinned image version;
- a restart policy suitable for host reboots;
- persistent host directories or named volumes for models, configuration, and
  application data;
- a loopback-only port binding;
- an Ansible-managed configuration and environment file that contains no
  plaintext secret values committed to Git;
- resource limits compatible with the existing `llama.cpp` services.

LocalAI is an additional consumer of server resources. The initial deployment
must therefore use a small CPU-capable model and conservative limits. GPU
acceleration is enabled only after the playbook detects a supported AMD/ROCm
path and a live inference request proves that the selected backend works.
Failure to validate GPU acceleration must fall back to the documented CPU
evaluation path rather than disrupt the deployment.

### Network and TLS

LocalAI will listen on a new, configurable loopback address and port. The
default port must not collide with the existing `llama.cpp` ports `8080`,
`8082`, `8083`, and `8084`, or with known retired application ports.

Nginx will terminate TLS for:

`https://localai.stratecode.local`

The server block will proxy to the loopback backend, support streaming
responses, use appropriate inference timeouts, and pass the headers required by
the LocalAI web and API surfaces.

Access to this virtual host will be restricted to the configured LAN CIDR,
WireGuard subnet, and loopback. The raw LocalAI port will not listen on a LAN,
VPN, or public interface.

The role will create or reuse the repository's existing local-certificate
pattern. The operator remains responsible for resolving
`localai.stratecode.local` to the lab address through the client hosts file or
private DNS and for trusting the local certificate authority or certificate on
the client.

### Authentication

Anonymous LocalAI access is forbidden. Authentication configuration will be
enabled by default.

Secrets will be supplied through the existing environment/vault mechanism.
They will not be written to `.env.example`, logs, task output, or repository
files. Example configuration will contain placeholders only.

The verification path must prove both sides of the boundary:

- an unauthenticated API request is rejected;
- an authenticated API request succeeds.

If LocalAI's multi-user authentication cannot be bootstrapped
non-interactively for the pinned version, the initial deployment will use a
strong API key as the bounded evaluation fallback. That fallback grants
administrative authority and must therefore remain confined to LAN and
WireGuard access.

## Ansible Integration

The implementation will introduce:

- a dedicated `roles/localai` role;
- LocalAI defaults and environment lookups in the existing variable layer;
- `LAB_LOCALAI_ENABLED`, defaulting to `false`;
- configurable image version, backend address, backend port, domain, storage
  paths, authentication, and resource limits;
- an opt-in LocalAI role entry in `playbooks/bootstrap.yml`;
- handlers for container and Nginx reconciliation;
- an explicit interaction with
  `playbooks/cleanup-localai-baseline.yml`.

The baseline cleanup playbook will treat LocalAI as absent only when its cleanup
mode is requested. It must preserve all existing `llama.cpp`, WireGuard, and
observability invariants.

Running bootstrap with LocalAI disabled must produce no LocalAI runtime changes.
Running it enabled must converge on the declared LocalAI state without altering
the `llama.cpp` units.

## Model Evaluation

The first model will be small, openly licensed, CPU-capable, and trained for
tool calling. Its exact identifier will be configurable rather than hard-coded
into the role's task logic.

The initial acceptance test covers:

- model availability;
- a basic chat completion;
- structured tool-call output from a fixed request;
- response streaming where supported;
- persistence across a LocalAI container restart.

The test does not authorize executing arbitrary model-requested commands. Tool
execution remains the responsibility of a separately governed consumer or MCP
server.

## Observability

The role will expose a bounded health signal and, where supported by the pinned
LocalAI release, metrics for the existing Prometheus deployment.

The initial integration must provide:

- container state;
- LocalAI health or readiness status;
- Nginx HTTPS availability;
- a clear distinction between runtime readiness and model readiness.

No alert or dashboard will claim inference health based only on the container
being alive.

## Error Handling and Rollback

The role will fail before modifying Nginx when required secrets, storage paths,
or image-version inputs are invalid.

Nginx configuration will be validated before reload. A failed validation must
leave the currently active configuration serving traffic.

The runtime must not start when its configured port collides with a managed
service. A failed LocalAI health check must not stop or restart any
`llama.cpp` unit.

Rollback consists of disabling LocalAI and running the appropriate cleanup
path. Runtime resources and Nginx exposure are removed, while persistent model
data is retained unless a separate destructive-retention action is explicitly
authorized.

## Verification

Repository-level verification:

1. Add contract tests before production role changes.
2. Observe the new tests fail for the missing LocalAI behavior.
3. Implement the smallest role and bootstrap changes that satisfy them.
4. Run the affected contract tests.
5. Run `ansible-playbook playbooks/bootstrap.yml --syntax-check`.
6. Run `bash -n` for any new shell scripts.
7. Run `git diff --check`.

Live-host verification:

1. Record the active/enabled state and health of all four `llama.cpp` services.
2. Apply bootstrap with LocalAI enabled.
3. Confirm LocalAI binds only to loopback.
4. Confirm the HTTPS domain is reachable from LAN and WireGuard.
5. Confirm requests from outside the allowed networks are denied where a
   suitable test path is available.
6. Confirm unauthenticated API access fails.
7. Confirm authenticated health, model listing, chat, and tool-call requests
   succeed.
8. Restart the container and confirm model/configuration persistence.
9. Confirm every original `llama.cpp` service remains active and healthy.
10. Apply the playbook again and require `changed=0` and `failed=0`.

AMD acceleration is reported as validated only when backend logs, device
visibility, and a successful inference request all agree. Otherwise the result
is explicitly CPU-only.

## Acceptance Criteria

The work is complete when:

- LocalAI installation is fully represented in Ansible and opt-in by default;
- `localai.stratecode.local` serves LocalAI over valid configured TLS from LAN
  and WireGuard clients after local name resolution is configured;
- all LocalAI access requires authentication;
- the backend port is loopback-only;
- one small tool-capable model passes chat and structured tool-call tests;
- configuration and model data survive a container restart;
- all four `llama.cpp` services remain unchanged and healthy;
- repository checks and the live idempotency run pass;
- documentation describes enablement, access, certificate trust, validation,
  rollback, and known AMD limitations.
