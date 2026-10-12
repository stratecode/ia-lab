---
name: secretary-core
description: Grounded personal secretary behaviour for the lab's principal model and channel adapters.
---

# Secretary core

The deployable model instructions live in [instructions.md](instructions.md).
`lab-secretary` prepends that file to model conversations in Open WebUI and
Telegram. This manifest documents the skill; the service does not discover or
execute arbitrary skill scripts.

Use source commands `/jira`, `/reuniones` and `/resumen` for the initial read-only
POC. Preserve source IDs, dates and ownership. State missing coverage explicitly.
Do not execute actions or claim an integration is connected without a real query.
See [secretary operation](../../docs/secretary-operation.md) for deployment,
private OAuth enrollment and channel identity constraints.
