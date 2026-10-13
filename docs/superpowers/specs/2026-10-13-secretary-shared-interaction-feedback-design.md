# Secretary shared interaction feedback design

**Status:** Draft — awaiting operator review  
**Date:** 2026-10-13

## Goal

Make the secretary's acknowledgement and progress behavior consistent across every connected channel. The shared router interprets what the operator wants and emits channel-neutral interaction events. Each channel adapter renders those events using its own native affordances while preserving the same meaning and truthful status.

The user should be able to tell, without a generic acknowledgement, whether the secretary understood a request as a lookup, agrees with an explicit statement, or understood an instruction as an action. When the work needs an external source, the secretary should say which source it is checking, then return the result when the work finishes.

## Scope and current constraints

- Active channels are Telegram and OpenWebUI. Future Slack and WhatsApp adapters should consume the same event contract without duplicating intent or routing rules.
- Telegram can react to the operator's original message. OpenWebUI has no equivalent reaction on the submitted message, so it should render the semantic emoji and progress copy in its assistant response stream.
- Current connector capabilities are read-only. This change must not add write access to Jira, Granola, or any other service.
- Keep the existing 30-second delayed progress threshold. Emit one delayed update only if work is still running; do not repeat the same stage.
- Preserve current voice behavior: voice input may be transcribed, but the response and progress remain written.
- Do not change memory, RAG, connector OAuth, or channel authentication in this iteration.

## Semantics

The shared interaction meaning has four values:

| Meaning | Operator-facing signal | When it applies |
| --- | --- | --- |
| `lookup` | 🔍 | The request asks to retrieve, inspect, or verify information. |
| `agreement` | ✅ | The secretary has recognized an explicit confirmation or a position it agrees with. This does not mean a task completed. |
| `action` | 👍 | The request is an instruction for the secretary to carry out. This acknowledges the intent; it does not claim authorization or completion. |
| `neutral` | No emoji | Conversation is ordinary, ambiguous, or not well represented by the other meanings. |

Choose `neutral` when confidence is insufficient; do not emit an emoji merely to acknowledge every message. Intent classification should reuse the existing local rules and existing model fallback for ambiguity. Do not add a separate model call solely to choose an emoji.

## Shared event flow

The secretary core owns intent, policy, source selection, and progress state. It emits typed events without Telegram method names or channel-specific markup:

1. `intent_recognized(meaning)` after the request has been interpreted.
2. `lookup_started(source)` immediately before calling a read capability.
3. `action_recognized(action)` when an instruction is understood.
4. `action_started(tool)` only when a registered write capability is authorized and actually begins. No write capability is available today, so this event cannot occur in the current deployment.
5. `work_delayed(stage)` once after the configured 30-second threshold if work remains active.
6. `completed` or `blocked(reason)` after the operation returns.

The router sends source progress only after it has selected the source and is about to call it. Example: `¿Qué tareas importantes tengo para hoy?` produces 🔍, then `Déjame revisar Granola.` when the Granola read begins, then a concise answer grounded in the returned notes.

An instruction such as `Cierra la tarea STRAT-42 para Acme` produces 👍 to acknowledge the requested action. With the current read-only Jira connector, the router may look up the task if a Jira read capability matches, but must say that it can only consult the task and has not closed it. It must not emit `action_started`, say that it is applying a change, or report success. If no lookup or write capability is available, it should explain that directly rather than announce work it cannot perform.

## Channel rendering

Channel adapters render the same event meaning and source copy:

- **Telegram:** use `setMessageReaction` on the authorized incoming message for 🔍, ✅, or 👍. Use one active reaction per message. Send a concise text message when a source lookup or supported action actually starts, and a final text answer when complete. Reaction API failure must not stop the request or suppress its answer.
- **OpenWebUI:** include the same semantic emoji and contextual source update in the assistant response stream, then stream the final answer in the same chat. Preserve the existing streaming response contract and identity checks.
- **Future channels:** Slack and WhatsApp adapters map the event to a native reaction when supported; otherwise render the same emoji with the corresponding short text. They must not implement their own intent classifier.

Suggested shared Spanish copy:

- Lookup: `Déjame revisar {source}.`
- Action recognized but unavailable: `He entendido que quieres {action}. Ahora mismo solo puedo consultar {source}; no puedo aplicar ese cambio.`
- Supported action starting: `Voy a {action} en {source}.`
- Delayed work: a varied, concise message naming the current stage, sent once after 30 seconds.

The final response remains the substantive result. A reaction or progress message never substitutes for the result and is not a success signal by itself.

## Error handling and privacy

- Reject unauthorized Telegram messages before emitting events, reacting, downloading media, or calling model/connectors.
- A failed reaction is a presentation failure only; continue routing and answer normally.
- A connector failure becomes `blocked(reason)` and receives a clear final response. Never imply that information was verified when the connector failed.
- Do not log user text, task identifiers, prompts, connector payloads, chat identifiers, or reaction content in metrics. Event metrics may use bounded labels for channel, meaning, event type, and outcome.
- Do not persist interaction events separately; existing conversation history behavior remains authoritative.

## Validation criteria

1. Shared core tests cover lookup, agreement, action, neutral, ambiguous intent, source selection, delayed progress, and blocked read-only actions without depending on Telegram.
2. Telegram adapter tests verify the three reaction mappings, target message ID, source-specific progress copy, one-reaction payload shape, and continued final response when `setMessageReaction` fails.
3. OpenWebUI tests verify matching emoji and source progress in the existing streamed response, with final content and identity enforcement preserved.
4. A read-only action request never produces a claim that it changed an external system. A future write capability can emit `action_started` only after policy authorization.
5. The repository's Go test harness passes. Deployment smoke tests verify only the secretary service changed if implementation requires deployment.

## Primary source

- [Telegram Bot API — `setMessageReaction`](https://core.telegram.org/bots/api#setmessagereaction): reaction payload, target message, and bot reaction limit.
