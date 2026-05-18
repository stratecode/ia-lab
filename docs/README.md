# Documentation Index

This index lists all documentation artifacts for the Local AI Agents Platform, grouped by subdirectory. Each entry links to the document file and displays its title.

## Architecture

Platform architecture, design decisions, and operational constraints.

- [Architecture Overview](architecture/overview.md)
- [Task Types Taxonomy](architecture/task-types.md)
- [Operational Limits](architecture/operational-limits.md)
- [Resource Scheduling](architecture/resource-scheduling.md)
- [Workspace Isolation](architecture/workspace-isolation.md)
- [Governance Framework](architecture/governance.md)

### Architecture Decision Records

- [0001. Use Nginx Over Traefik as Reverse Proxy](architecture/adr/0001-use-nginx-over-traefik.md)

## Agents

Agent definitions, responsibilities, and interaction patterns.

- [Agent Catalog](agents/catalog.md)

## Flows

Interaction flows and communication patterns between agents and the orchestrator.

- [Task Execution Flow](flows/task-execution.md)
- [Multi-Agent Collaboration Flow](flows/multi-agent-collaboration.md)
- [Error Handling Flow](flows/error-handling.md)
- [Human Overrides Flow](flows/human-overrides.md)

## Security

Permission boundaries and approval models.

- [Permissions Model](security/permissions.md)
- [Approval Model](security/approval-model.md)

## Events

Event taxonomy, schemas, and severity classification.

- [Event Taxonomy](events/taxonomy.md)
- [Event Schemas](events/schemas.md)
- [Event Severity Levels](events/severity-levels.md)

## Models

Model registry and inference configuration.

- [Model Registry](models/registry.md)

## Tools

Tool registry and MCP integrations.

- [Tool Registry](tools/registry.md)

## General

- [Getting Started](getting-started.md)
- [System Usage Guide](system-usage.md)
- [Orchestrator Go Shadow](orchestrator-go-shadow.md)
- [Orchestrator Redeploy Runbook](orchestrator-redeploy.md)
- [Server Baseline](server-baseline.md)
- [WireGuard](wireguard.md)

## Revision History

| Date | Author | Change Description |
|------|--------|--------------------|
| 2025-07-14 | Platform Architect | Initial documentation index skeleton creation |
| 2025-07-15 | Platform Architect | Final index update with all documents, verified links and headings |
