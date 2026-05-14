# Documentation Index

This index lists all documentation artifacts for the Local AI Agents Platform, grouped by subdirectory. Each entry links to the document file and displays its title.

## Architecture

Platform architecture, design decisions, and operational constraints.

- [Architecture Overview](architecture/overview.md) — Architecture Overview
- [Task Types Taxonomy](architecture/task-types.md) — Task Types Taxonomy
- [Operational Limits](architecture/operational-limits.md) — Operational Limits
- [Resource Scheduling](architecture/resource-scheduling.md) — Resource Scheduling and Model Allocation
- [Workspace Isolation](architecture/workspace-isolation.md) — Workspace Isolation
- [Governance Framework](architecture/governance.md) — Governance Framework

### Architecture Decision Records

- [ADR-0001: Use Nginx over Traefik](architecture/adr/0001-use-nginx-over-traefik.md) — ADR-0001: Use Nginx over Traefik

## Agents

Agent definitions, responsibilities, and interaction patterns.

- [Agent Catalog](agents/catalog.md) — Agent Catalog

## Flows

Interaction flows and communication patterns between agents and the orchestrator.

- [Task Execution Flow](flows/task-execution.md) — Task Execution Flow
- [Multi-Agent Collaboration](flows/multi-agent-collaboration.md) — Multi-Agent Collaboration Flow
- [Error Handling Flow](flows/error-handling.md) — Error Handling Flow
- [Human Overrides](flows/human-overrides.md) — Human Override Controls

## Security

Permission boundaries and approval models.

- [Permissions Model](security/permissions.md) — Permissions Model
- [Approval Model](security/approval-model.md) — Approval Model

## Events

Event taxonomy, schemas, and severity classification.

- [Event Taxonomy](events/taxonomy.md) — Event Taxonomy
- [Event Schemas](events/schemas.md) — Event Schemas
- [Severity Levels](events/severity-levels.md) — Event Severity Classification

## Models

Model registry and inference configuration.

- [Model Registry](models/registry.md) — Model Registry

## Tools

Tool registry and MCP integrations.

- [Tool Registry](tools/registry.md) — Tool Registry

## General

- [Getting Started](getting-started.md) — Getting Started
- [Server Baseline](server-baseline.md) — Server Baseline
- [WireGuard](wireguard.md) — WireGuard

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial documentation index skeleton creation |
