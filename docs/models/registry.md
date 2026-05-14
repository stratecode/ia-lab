# Model Registry

Centralized registry of all LLM and embedding models available on the platform, enabling agents to discover models and the system to validate model assignments.

## Overview

All inference is served locally via llama.cpp with Vulkan backend on AMD GPU hardware. Models are based on the Qwen family and quantized for optimal VRAM usage. Each model is assigned to one category and maps to specific agent types from the [Agent Catalog](../agents/catalog.md).

## Operational Status Definitions

| Status | Description |
|--------|-------------|
| **experimental** | Model is under evaluation; not recommended for production workloads. May be removed or replaced without notice. |
| **stable** | Model is validated and approved for production use. Changes follow the governance ADR process. |
| **deprecated** | Model is scheduled for removal. A replacement model identifier is provided. A `model.deprecated` System_Event is emitted containing the deprecated model identifier, new status, and replacement model identifier. |

## Model Categories

Each model is classified into exactly one category:

| Category | Purpose | Typical Size |
|----------|---------|--------------|
| **coding** | Code generation, completion, refactoring, and review | 8B parameters |
| **reasoning** | Task planning, decomposition, research, and analysis | 8B parameters |
| **utility** | Lightweight operations: summarization, formatting, infrastructure scripts | 4B parameters |
| **embedding** | Text vectorization for RAG, semantic search, and similarity | Varies |

## Model Entries

### qwen2.5-coder-8b

| Field | Value |
|-------|-------|
| **Identifier** | `qwen2.5-coder-8b` |
| **Provider** | llama.cpp |
| **Quantization** | Q4_K_M |
| **Context Window** | 32768 tokens |
| **Category** | coding |
| **Endpoint** | `http://localhost:8081/v1` |
| **VRAM Usage** | 6144 MB |
| **Supported Agents** | coder, reviewer |
| **Max Concurrent** | 2 |
| **Fallback Models** | `qwen2.5-utility-4b` |
| **Status** | stable |

---

### qwen2.5-planner-8b

| Field | Value |
|-------|-------|
| **Identifier** | `qwen2.5-planner-8b` |
| **Provider** | llama.cpp |
| **Quantization** | Q4_K_M |
| **Context Window** | 32768 tokens |
| **Category** | reasoning |
| **Endpoint** | `http://localhost:8082/v1` |
| **VRAM Usage** | 6144 MB |
| **Supported Agents** | planner, researcher |
| **Max Concurrent** | 2 |
| **Fallback Models** | `qwen2.5-utility-4b` |
| **Status** | stable |

---

### qwen2.5-utility-4b

| Field | Value |
|-------|-------|
| **Identifier** | `qwen2.5-utility-4b` |
| **Provider** | llama.cpp |
| **Quantization** | Q4_K_M |
| **Context Window** | 16384 tokens |
| **Category** | utility |
| **Endpoint** | `http://localhost:8083/v1` |
| **VRAM Usage** | 3072 MB |
| **Supported Agents** | infra |
| **Max Concurrent** | 4 |
| **Fallback Models** | — |
| **Status** | stable |

---

### qwen2.5-embedding

| Field | Value |
|-------|-------|
| **Identifier** | `qwen2.5-embedding` |
| **Provider** | llama.cpp |
| **Quantization** | F16 |
| **Context Window** | 8192 tokens |
| **Category** | embedding |
| **Endpoint** | `http://localhost:8084/v1` |
| **VRAM Usage** | 2048 MB |
| **Supported Agents** | planner, coder, reviewer, researcher |
| **Max Concurrent** | 4 |
| **Fallback Models** | — |
| **Status** | stable |

## Model Assignment Validation

When an agent is assigned a model whose `supported_agents` list does not include that agent's type, the platform rejects the assignment and emits a `model.assignment-validation-failed` System_Event containing:

- Agent identifier
- Agent type
- Requested model identifier
- Timestamp

## Deprecation Policy

When a model status transitions to `deprecated`, the platform emits a `model.deprecated` System_Event containing:

- Deprecated model identifier
- New status (`deprecated`)
- Replacement model identifier (or empty if no replacement exists)

The registry entry is updated to indicate the recommended replacement model identifier.

## Related Documents

- [Agent Catalog](../agents/catalog.md) — defines agent types referenced in supported_agents lists
- [Resource Scheduling](../architecture/resource-scheduling.md) — defines model allocation and scheduling policies per agent
- [Event Taxonomy](../events/taxonomy.md) — defines System_Event types emitted on status changes and validation failures

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial model registry with 4 model entries |
