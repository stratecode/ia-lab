# Architecture Overview

Describes the layered architecture of the Local AI Agents Platform, defining each system layer's responsibility, constituent components, and boundaries with adjacent layers.

## System Layers

The platform is organized into five distinct layers, each with a clear responsibility boundary. Communication flows downward through the stack, with events propagating upward for observability and control.

### Interface Layer

| Attribute | Description |
|-----------|-------------|
| **Responsibility** | Receives user requests from external channels, normalizes input into a common command format, and delivers responses back to users. Acts as the sole entry point for human interaction with the platform. |
| **Components** | Telegram Bot, Open WebUI, TUI/CLI Local Bridge |
| **Upper Boundary** | External users and third-party messaging platforms |
| **Lower Boundary** | Orchestration Layer — passes normalized requests to the Go orchestrator via HTTP or bridge APIs |

**Component Details:**

- **Telegram Bot** — Listens for commands and messages via Telegram Bot API long-polling; translates operator messages into structured task or initiative requests
- **Open WebUI** — Browser-based conversational UI connected to local models and the orchestrator-compatible backend
- **TUI/CLI Local Bridge** — Primary operator cockpit for initiative flow, selective launch, approvals, and local execution bridge management

---

### Orchestration Layer

| Attribute | Description |
|-----------|-------------|
| **Responsibility** | Routes incoming requests to the appropriate agent, manages task lifecycle state, enforces [operational limits](operational-limits.md), coordinates multi-agent workflows, and handles [approval gates](../security/approval-model.md). |
| **Components** | Go Orchestrator API, Embedded Worker Runtime, Redis Task Queue, PostgreSQL Persistence, Capability Sidecars |
| **Upper Boundary** | Interface Layer — receives normalized requests via HTTP/webhook |
| **Lower Boundary** | Agent Layer — dispatches tasks to agents via Redis Task Queue |

**Component Details:**

- **Go Orchestrator API** — Core control plane managing task and initiative state machines, [permission enforcement](../security/permissions.md), scheduling decisions per [resource scheduling](resource-scheduling.md), research, approvals, and event-facing persistence
- **Embedded Worker Runtime** — Claims remote planner, researcher, coder, and reviewer tasks, executes supported flows, and reconciles task trees
- **Redis Task Queue** — Priority-based message broker decoupling orchestration from agent execution; supports priority-first, FIFO-within-priority scheduling
- **PostgreSQL Persistence** — System of record for tasks, initiatives, approvals, artifacts, evaluations, and bridge state
- **Capability Sidecars** — Narrow Python services for document and image processing where the tooling still justifies it

---

### Agent Layer

| Attribute | Description |
|-----------|-------------|
| **Responsibility** | Executes specialized tasks within defined [permission boundaries](../security/permissions.md). Each agent operates autonomously within its domain scope, consuming tasks from the queue and producing structured results. See the [Agent Catalog](../agents/catalog.md) for full definitions. |
| **Components** | Planner Agent, Coder Agent, Reviewer Agent, Infra Agent, Researcher Agent |
| **Upper Boundary** | Orchestration Layer — receives task assignments via Redis Task Queue |
| **Lower Boundary** | Inference Layer — sends prompts to llama.cpp model instances and receives completions |

**Component Details:**

- **Planner Agent** — Decomposes high-level requests into structured task plans and coordinates multi-agent workflows
- **Coder Agent** — Generates, modifies, and refactors code within isolated [workspaces](workspace-isolation.md)
- **Reviewer Agent** — Analyzes code quality, identifies issues, and produces review feedback
- **Infra Agent** — Manages infrastructure configuration, Docker operations, and deployment tasks
- **Researcher Agent** — Gathers information, summarizes documentation, and provides context for other agents

---

### Inference Layer

| Attribute | Description |
|-----------|-------------|
| **Responsibility** | Provides local LLM inference capabilities via llama.cpp instances. Manages model loading/unloading, GPU memory allocation, and concurrent request handling. See the [Model Registry](../models/registry.md) for available models. |
| **Components** | llama.cpp Code (8B), llama.cpp Planner (8B), llama.cpp Utility (4B), llama.cpp Embeddings |
| **Upper Boundary** | Agent Layer — receives inference requests via OpenAI-compatible HTTP API |
| **Lower Boundary** | Infrastructure Layer — runs on Docker containers with GPU passthrough (Vulkan backend) |

**Component Details:**

- **llama.cpp Code (8B)** — Serves the Coder and Reviewer agents with a code-specialized 8B parameter model
- **llama.cpp Planner (8B)** — Serves the Planner and Researcher agents with a reasoning-specialized 8B parameter model
- **llama.cpp Utility (4B)** — Serves the Infra agent and lightweight utility tasks with a 4B parameter model
- **llama.cpp Embeddings** — Provides vector embeddings for semantic search and context retrieval

---

### Infrastructure Layer

| Attribute | Description |
|-----------|-------------|
| **Responsibility** | Provides the foundational runtime environment including containerization, networking, persistent storage, monitoring, and observability. All upper layers depend on infrastructure services. |
| **Components** | Docker, Nginx, Prometheus, Grafana, Redis, Git Repos |
| **Upper Boundary** | Inference Layer — provides container runtime, GPU access, and networking |
| **Lower Boundary** | Physical hardware (Ubuntu Server, AMD GPU with Vulkan, LVM storage) |

**Component Details:**

- **Docker** — Container runtime for all services; provides isolation and reproducible deployments
- **Nginx** — Reverse proxy with TLS termination (Let's Encrypt); routes external traffic to internal services
- **Prometheus** — Metrics collection and alerting; scrapes all service endpoints
- **Grafana** — Visualization dashboards for system and agent metrics
- **Redis** — In-memory data store serving as task queue and ephemeral state cache
- **Git Repos** — Version-controlled repositories for code, configuration, and documentation

---

## Diagrams

### Component Relationships

```mermaid
graph TB
    subgraph "Interface Layer"
        TELEGRAM[Telegram Bot]
        WEBUI[Open WebUI]
        TUI[TUI/CLI Bridge]
    end

    subgraph "Orchestration Layer"
        GOAPI[Go Orchestrator API]
        GOWORKER[Embedded Worker Runtime]
        QUEUE[Redis Task Queue]
        POSTGRES[PostgreSQL]
        SIDECARS[Python Sidecars]
    end

    subgraph "Agent Layer"
        PLANNER[Planner Agent]
        CODER[Coder Agent]
        REVIEWER[Reviewer Agent]
        INFRA_AGENT[Infra Agent]
        RESEARCHER[Researcher Agent]
    end

    subgraph "Inference Layer"
        LLAMA_CODE[llama.cpp Code 8B]
        LLAMA_PLAN[llama.cpp Planner 8B]
        LLAMA_UTIL[llama.cpp Utility 4B]
        LLAMA_EMB[llama.cpp Embeddings]
    end

    subgraph "Infrastructure Layer"
        DOCKER[Docker]
        NGINX[Nginx]
        PROMETHEUS[Prometheus]
        GRAFANA[Grafana]
        REDIS[Redis]
        GIT[Git Repos]
    end

    TELEGRAM --> GOAPI
    WEBUI --> GOAPI
    TUI --> GOAPI
    GOAPI --> QUEUE
    GOAPI --> POSTGRES
    GOAPI --> SIDECARS
    GOWORKER --> QUEUE
    QUEUE --> PLANNER
    QUEUE --> CODER
    QUEUE --> REVIEWER
    QUEUE --> INFRA_AGENT
    QUEUE --> RESEARCHER

    PLANNER --> LLAMA_PLAN
    CODER --> LLAMA_CODE
    REVIEWER --> LLAMA_CODE
    INFRA_AGENT --> LLAMA_UTIL
    RESEARCHER --> LLAMA_PLAN

    CODER --> GIT
    INFRA_AGENT --> DOCKER
    GOAPI --> REDIS
    PROMETHEUS --> GRAFANA
```

### Data Flows Between Layers

```mermaid
sequenceDiagram
    participant IL as Interface Layer
    participant OL as Orchestration Layer
    participant AL as Agent Layer
    participant INF as Inference Layer
    participant INFRA as Infrastructure Layer

    IL->>OL: Normalized request (HTTP/webhook)
    OL->>OL: Classify task type, check permissions
    OL->>AL: Task assignment (Redis queue message)
    AL->>INF: Inference request (OpenAI-compatible API)
    INF->>INFRA: GPU compute (Vulkan), model I/O
    INFRA-->>INF: Completion tokens
    INF-->>AL: Model response
    AL->>INFRA: Tool execution (filesystem, Git, Docker)
    AL-->>OL: Task result (Redis queue message)
    OL-->>IL: Formatted response (HTTP)

    Note over OL,AL: Events emitted at each state transition
    OL->>INFRA: task.created event (Prometheus metrics)
    AL->>INFRA: agent.invoked / agent.responded events
```

### Deployment Topology

```mermaid
graph TB
    subgraph "Ubuntu Server Host"
        subgraph "Nginx Reverse Proxy"
            NGINX_SVC[Nginx + TLS]
        end

        subgraph "Docker Containers"
            GRAFANA_C[Grafana Container]
            POSTGRES_C[PostgreSQL Container]
        end

        subgraph "Systemd Services"
            LLAMA_SVC1[llama-code.service]
            LLAMA_SVC2[llama-planner.service]
            LLAMA_SVC3[llama-utility.service]
            LLAMA_SVC4[llama-embeddings.service]
            ORCHGO_SVC[orchestrator.service]
            DOCS_SIDECAR[orchestrator-cap-docs.service]
            IMAGES_SIDECAR[orchestrator-cap-images.service]
            REDIS_SVC[redis.service]
            PROM_SVC[prometheus.service]
        end

        subgraph "Storage"
            LVM[LVM Volumes]
            GIT_REPOS[Git Repositories]
            MODELS[Model Files GGUF]
        end

        subgraph "GPU"
            VULKAN[AMD GPU - Vulkan Backend]
        end
    end

    subgraph "External"
        USERS[Users]
        TELEGRAM_API[Telegram API]
        ROUTE53[AWS Route53 DDNS]
    end

    USERS --> NGINX_SVC
    NGINX_SVC --> GRAFANA_C
    NGINX_SVC --> ORCHGO_SVC
    ORCHGO_SVC --> REDIS_SVC
    ORCHGO_SVC --> POSTGRES_C
    ORCHGO_SVC --> DOCS_SIDECAR
    ORCHGO_SVC --> IMAGES_SIDECAR
    ORCHGO_SVC --> LLAMA_SVC1
    ORCHGO_SVC --> LLAMA_SVC2
    ORCHGO_SVC --> LLAMA_SVC3
    LLAMA_SVC1 --> VULKAN
    LLAMA_SVC2 --> VULKAN
    LLAMA_SVC3 --> VULKAN
    LLAMA_SVC4 --> VULKAN
    LLAMA_SVC1 --> MODELS
    LLAMA_SVC2 --> MODELS
    LLAMA_SVC3 --> MODELS
    LLAMA_SVC4 --> MODELS
    PROM_SVC --> GRAFANA_C
    ROUTE53 -.-> NGINX_SVC
```

## Layer Interaction Summary

| Source Layer | Target Layer | Protocol | Payload |
|-------------|-------------|----------|---------|
| Interface → Orchestration | HTTP/Webhook | Normalized task request (JSON) |
| Orchestration → Agent | Redis Queue | Task assignment message with metadata |
| Agent → Inference | HTTP (OpenAI-compatible) | Prompt with context and parameters |
| Agent → Infrastructure | Filesystem/Shell/Docker API | Tool invocations per [permissions](../security/permissions.md) |
| All Layers → Infrastructure | Event bus | [System events](../events/taxonomy.md) per [event schemas](../events/schemas.md) |

## Related Documents

- [Agent Catalog](../agents/catalog.md) — Defines all agent types, responsibilities, boundaries, and inter-agent communication patterns
- [Permissions Model](../security/permissions.md) — Specifies resource access boundaries for each agent type
- [Event Taxonomy](../events/taxonomy.md) — Classifies all system events by category and defines producer-consumer relationships
- [Event Schemas](../events/schemas.md) — Defines the formal structure of system event payloads
- [Task Types](task-types.md) — Categorizes work items and defines lifecycle state machines
- [Operational Limits](operational-limits.md) — Specifies token budgets, timeouts, and retry policies per task type
- [Resource Scheduling](resource-scheduling.md) — Defines model allocation and GPU memory management policies
- [Model Registry](../models/registry.md) — Catalogs available LLM and embedding models with their configurations
- [Tool Registry](../tools/registry.md) — Lists all tools available to agents with authorization rules

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial architecture overview with 5 layers and 3 Mermaid diagrams |
