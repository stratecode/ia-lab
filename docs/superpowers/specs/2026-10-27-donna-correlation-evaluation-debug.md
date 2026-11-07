# Donna: verified correlation, conversation regressions, and routing diagnostics

## Intent

Make cross-source relationship claims mechanically checkable, preserve observed conversation behavior in repeatable tests, and make debug traces explain routing decisions without storing conversation content.

## Design

- For explicit Granola/Jira comparisons, request short source facts as exact quotes and a proposed shared phrase from the model. Validate each quote as a literal substring of the corresponding connector response and validate every evidence ID against that response's evidence references.
- Render validated source facts with provider citations. Render a relationship only when the proposed shared phrase is present in both original source responses; otherwise say that no verifiable link was found. Never render model-authored relationship prose.
- Keep the normal synthesis path for single-source requests and other multi-source combinations.
- Add a same-conversation, three-turn regression that covers meeting context, an unrelated Jira lookup, and an anaphoric cross-source follow-up.
- Add bounded route decision and fallback-reason fields to the private interaction trace. Add corresponding content-free events to the `LAB_DEBUG_ENABLED` conversation log. Do not store utterances, prompts, answers, connector payloads, raw errors, URLs, or credentials.
- Keep the existing `LAB_DEBUG_ENABLED` default-off behavior and existing learning retention. New trace fields are additive and optional.

## Acceptance

- Fabricated quotes, unknown evidence IDs, or phrases present in only one source cannot yield a cross-source link.
- Exact shared evidence produces a link with source references; nonmatching evidence remains separated.
- Regression tests cover the observed three-turn path and source exclusions.
- Route diagnostics identify intent-service use, local fallback, model classification, and bounded fallback categories without user text.
- Secretary tests, the Go harness, build, Ansible syntax checks for changed playbooks, and `git diff --check` pass.
