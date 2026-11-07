# Donna: intent fallback, source correlation, and conversation style

## Intent

Make Donna understand ambiguous requests more reliably and answer in a natural, compact, easy-to-scan way. When a request involves both meeting notes and Jira, connect facts only when the sources provide a specific, defensible match.

## Goals

- Keep the standalone intent service as the normal interpretation path.
- On an unresolved or failed intent-service call, try Donna's deterministic local routing; invoke the language-model classifier only if the remaining request is genuinely ambiguous.
- If a resolved lookup explicitly asks to compare multiple sources but the intent service returns only one, supplement that result with the deterministic matches for the same utterance, respecting explicit exclusions and source-only requests.
- Keep all source capabilities read-only and reject negated actions without looking up or changing anything.
- In combined-source answers, instruct synthesis to correlate Granola actions and Jira issues only with an explicit shared issue key or a specific match between task, action, and owner; otherwise explain each source separately.
- Make Donna concise by default, answer the asked point first, avoid gratuitous emphasis, and split long Telegram answers at meaningful boundaries.
- Preserve channel/conversation history isolation and existing source citations.

## Non-goals

- No new connector, write capability, or cross-conversation memory.
- No automatic creation or update of Jira issues.
- No deterministic validator for every cross-source relationship stated in free-form model prose; this increment applies evidence rules through synthesis instructions and checks representative live answers.
- No forced multi-message delivery for short answers or non-Telegram channels.

## Design decisions

- The intent microservice remains the first route. Its failure does not become permission to guess: local routing is attempted, then the existing classifier only for ambiguous intent, and unresolved cases ask a concise clarification.
- Correlation guidance belongs in the secretary's synthesis prompt because the intent service must not receive source contents or credentials. The model output is not mechanically checked for each asserted relationship in this increment; source evidence and current Jira state remain authoritative.
- Donna's persona instructions are the shared behavior contract. Telegram adds paragraph/sentence-aware splitting only for long final answers; Open WebUI continues to return one concise response.
- Current history remains isolated by channel and conversation, bounded to the existing retention policy.

## Acceptance

- Tests prove the language-model classifier is called after an ambiguous/unavailable intent-service result and a useful read-only route can still complete.
- A test proves an incomplete single-source intent suggestion cannot suppress a deterministic, explicit Granola-and-Jira comparison.
- Tests prove negated actions do not trigger fallback reads.
- Combined Granola/Jira synthesis receives explicit matching and non-matching correlation rules.
- Persona instructions favor short, direct, lightly formatted answers; Telegram sends multiple messages only when a response is long and splits at meaningful boundaries.
- Secretary Go tests, the repository Go harness, build, and `git diff --check` pass.
