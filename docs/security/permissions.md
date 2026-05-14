# Permissions Model

Defines the resource access boundaries for each agent type in the Local AI Agents Platform, enforcing a deny-by-default policy where agents have no access unless explicitly granted.

## Security Policy

### Deny-by-Default

All agents operate under a **deny-by-default** policy. An agent has zero filesystem, shell, Docker, Git, or network access unless an explicit allow-list entry grants it. This applies universally:

- Registered agents receive only the permissions defined in their allow-list below
- Unregistered agents (any agent type not defined in this document) receive **deny-all** with zero granted permissions until an administrator configures an explicit allow-list entry

### Access Types

Each permission entry is categorized as one of four binary access types, independently granted or denied per resource:

| Access Type | Description |
|-------------|-------------|
| **read** | Retrieve or inspect resource content without modification |
| **write** | Create, modify, or delete resource content |
| **execute** | Run commands, scripts, or container operations |
| **network** | Communicate with external endpoints over HTTP/HTTPS |

## Permission Entries

### Planner

```yaml
permission:
  agent_type: planner
  resources:
    filesystem:
      - path_pattern: "/workspace/*/plans/**"
        access: [read]
      - path_pattern: "/workspace/*/tasks/**"
        access: [read]
    shell:
      []  # No shell access
    docker:
      scope: none
    git:
      access: none
    network:
      []  # No network access
```

**Summary:** The Planner agent has read-only access to plan and task files within workspaces. It cannot execute commands, access Docker, Git, or external networks.

---

### Coder

```yaml
permission:
  agent_type: coder
  resources:
    filesystem:
      - path_pattern: "/workspace/*/src/**"
        access: [read, write]
      - path_pattern: "/workspace/*/tests/**"
        access: [read, write]
      - path_pattern: "/workspace/*/docs/**"
        access: [read, write]
      - path_pattern: "/workspace/*/config/**"
        access: [read]
      - path_pattern: "/workspace/*/.gitignore"
        access: [read, write]
      - path_pattern: "/workspace/*/package.json"
        access: [read, write]
      - path_pattern: "/workspace/*/requirements.txt"
        access: [read, write]
    shell:
      - command_pattern: "npm *"
        access: [execute]
      - command_pattern: "pip *"
        access: [execute]
      - command_pattern: "python -m pytest *"
        access: [execute]
      - command_pattern: "make *"
        access: [execute]
      - command_pattern: "cargo *"
        access: [execute]
    docker:
      scope: sandboxed
    git:
      access: read-write
    network:
      - endpoint_pattern: "https://registry.npmjs.org/**"
        methods: [GET]
      - endpoint_pattern: "https://pypi.org/**"
        methods: [GET]
```

**Summary:** The Coder agent has read/write access to source, test, and documentation files within workspaces. It can execute build and test commands, operate Docker in sandboxed mode (no host network, limited volume mounts), and has read-write Git access. Network access is limited to package registries (read-only).

---

### Reviewer

```yaml
permission:
  agent_type: reviewer
  resources:
    filesystem:
      - path_pattern: "/workspace/*/src/**"
        access: [read]
      - path_pattern: "/workspace/*/tests/**"
        access: [read]
      - path_pattern: "/workspace/*/docs/**"
        access: [read]
      - path_pattern: "/workspace/*/config/**"
        access: [read]
    shell:
      []  # No shell access
    docker:
      scope: none
    git:
      access: read
    network:
      []  # No network access
```

**Summary:** The Reviewer agent has read-only access to source, test, documentation, and configuration files. It can read Git history (diff, log, status) but cannot modify files, execute commands, or access Docker or external networks.

---

### Infra

```yaml
permission:
  agent_type: infra
  resources:
    filesystem:
      - path_pattern: "/workspace/*/infra/**"
        access: [read, write]
      - path_pattern: "/workspace/*/docker-compose*.yml"
        access: [read, write]
      - path_pattern: "/workspace/*/Dockerfile*"
        access: [read, write]
      - path_pattern: "/workspace/*/.env.example"
        access: [read, write]
      - path_pattern: "/workspace/*/config/**"
        access: [read, write]
      - path_pattern: "/workspace/*/src/**"
        access: [read]
    shell:
      - command_pattern: "docker *"
        access: [execute]
      - command_pattern: "docker-compose *"
        access: [execute]
      - command_pattern: "systemctl status *"
        access: [execute]
      - command_pattern: "ansible-playbook *"
        access: [execute]
      - command_pattern: "nginx -t"
        access: [execute]
    docker:
      scope: full
    git:
      access: read-write
    network:
      - endpoint_pattern: "https://registry.hub.docker.com/**"
        methods: [GET]
      - endpoint_pattern: "https://ghcr.io/**"
        methods: [GET]
```

**Summary:** The Infra agent has read/write access to infrastructure files, Docker configurations, and environment templates. It can execute Docker, systemctl, Ansible, and Nginx commands. It has full Docker scope and read-write Git access. Network access is limited to container registries (read-only).

---

### Researcher

```yaml
permission:
  agent_type: researcher
  resources:
    filesystem:
      - path_pattern: "/workspace/*/docs/**"
        access: [read]
      - path_pattern: "/workspace/*/README.md"
        access: [read]
    shell:
      []  # No shell access
    docker:
      scope: none
    git:
      access: none
    network:
      - endpoint_pattern: "https://*.wikipedia.org/**"
        methods: [GET]
      - endpoint_pattern: "https://api.github.com/**"
        methods: [GET]
      - endpoint_pattern: "https://docs.*/**"
        methods: [GET]
      - endpoint_pattern: "https://stackoverflow.com/**"
        methods: [GET]
```

**Summary:** The Researcher agent has read-only access to documentation files. It cannot execute commands, access Docker, or modify Git. It has network access limited to read-only queries against knowledge sources (Wikipedia, GitHub API, documentation sites, Stack Overflow).

---

## Authorization Summary Matrix

| Resource | Planner | Coder | Reviewer | Infra | Researcher |
|----------|---------|-------|----------|-------|------------|
| Filesystem (read) | ✓ (plans, tasks) | ✓ (workspace) | ✓ (workspace) | ✓ (infra, config) | ✓ (docs) |
| Filesystem (write) | ✗ | ✓ (src, tests, docs) | ✗ | ✓ (infra, docker) | ✗ |
| Shell (execute) | ✗ | ✓ (build/test) | ✗ | ✓ (docker, system) | ✗ |
| Docker | none | sandboxed | none | full | none |
| Git | none | read-write | read | read-write | none |
| Network | ✗ | ✓ (registries) | ✗ | ✓ (registries) | ✓ (knowledge) |

## Security Violation Handling

When an agent attempts an operation outside its granted permissions, the platform enforces the following behavior:

1. **Deny the operation** — the requested action is not executed
2. **Preserve agent state** — the agent's current state remains unmodified
3. **Emit a security violation System_Event** containing:

```yaml
event:
  event_type: "security.violation"
  severity: WARNING
  category: security
  payload:
    agent_identifier: string    # Agent type and instance ID
    denied_operation: string    # Operation type attempted (read, write, execute, network)
    target_resource: string     # Resource path, command, or endpoint that was denied
    timestamp: string           # ISO 8601 with timezone
    reason: string              # "permission_denied" | "unregistered_agent"
```

This event is routed to the monitoring system for alerting and audit logging. Repeated violations from the same agent may trigger escalation per the [Event Severity Levels](../events/severity-levels.md).

## Unregistered Agent Policy

Any agent type not explicitly defined in this document is treated as **unregistered** and receives:

- **Filesystem:** deny-all (no path patterns granted)
- **Shell:** deny-all (no commands permitted)
- **Docker:** scope `none`
- **Git:** access `none`
- **Network:** deny-all (no endpoints permitted)

An administrator must add an explicit permission entry to this document before an unregistered agent can perform any operation. Until then, all access attempts emit a `security.violation` System_Event with reason `unregistered_agent`.

## Related Documents

- [Agent Catalog](../agents/catalog.md) — defines agent types and their boundary constraints
- [Tool Registry](../tools/registry.md) — defines tools available to each agent and their authorization lists
- [Event Severity Levels](../events/severity-levels.md) — severity classification for security violation events

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial permissions model with 5 agent types, deny-by-default policy |
