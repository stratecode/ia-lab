# Donna grounded retrieval repair

**Requested:** Apply the agreed correctness improvements, compare behavior with repeatable evidence, and document the result. Preserve all pre-existing working-tree changes. Do not deploy or enable Jira writes as part of this local repair.

## Scope and order

1. Scope durable conversation recall to the authenticated channel and conversation by default; retain same-chat recall across multiple turns.
2. Make Jira result coverage explicit. A bounded first page must not be described as a complete result set; distinguish empty, partial, and unavailable outcomes.
3. Prevent write-success claims while no write operation and receipt exist. An explicit mutation request must stop before read-only tools or model synthesis.
4. Keep source-backed facts in deterministic renderers where available; allow synthesis only for narrowly validated cross-source comparison. Document remaining limits where full claim verification is not feasible.
5. Run focused secretary tests, the repository Go harness, build/contract checks affected by the changes, compare against the 100-case live baseline where possible, and record each result here.

## Existing baseline

- The accepted live Open WebUI rerun is **13/100 PASS**, as recorded in `docs/validation/2026-10-28-donna-100-readonly/README.md` (100 cases against the deployed artifact; read-only Jira and Granola oracle evidence; no Jira writes).
- Initial local verification on 2026-10-28: `./scripts/test-orchestrator-go.sh` passed `internal/secretary` and `cmd/lab-secretary`, but the whole harness exited 1 because `internal/llmtelemetry/TestStreamPreservesBytesAndMeasuresRealFirstToken` failed with `stream changed`. This is outside this task's code scope and must remain visible in the final report unless independently resolved by an authorized follow-up.
- Host `go` is not installed; use the repository Docker harness for Go checks.

## Execution ledger

- [x] Record test-first regressions for the behavior changes addressed (memory scope, action gating, Jira pagination/partial-empty rendering, and direct Granola answers).
- [x] Implement conversation-scope isolation and verify same-chat recall remains available.
- [x] Implement bounded Jira pagination (up to 10 pages) and distinguish empty, partial, complete, and unavailable outcomes.
- [x] Gate mutation requests before model or connector calls while no Jira write capability exists. A successful write receipt is not possible until a write tool is deliberately added.
- [x] Keep single-source Granola summaries direct and cited; validate quoted cross-source comparisons and otherwise separate facts.
- [x] Run focused and repository-level verification; all package and build checks pass (details in the validation report).
- [x] Update the validation report with local results and limits. The 100-case live rerun is not safe from this mixed worktree: deploying it would also ship unrelated uncommitted changes.
- [x] Review the task-scoped diff and run `git diff --check`; pre-existing unrelated changes remain untouched.

## Local implementation results (2026-10-28)

- Durable recall now requires exact channel and conversation scope. Same-chat recall remains supported; Telegram/Open WebUI and separate Open WebUI chats do not share remembered dialogue.
- Jira search follows `nextPageToken`, deduplicates repeated issues, and stops after ten pages. A missing continuation token before `isLast`, a repeated cursor, or the cap marks the result partial. Partial zero-result searches cannot be reported as complete “no matches.”
- Recognized mutation requests stop before read-only retrieval and model synthesis. Donna states she has read-only access; there is no write or success receipt path enabled.
- Granola's query-generated text is not used as the answer. Cited note summaries come from `get_meetings` and are returned with note links. Cross-source linking retains the exact-quote validator.
- Locally verified with `go test ./internal/secretary -count=1`, `./scripts/test-orchestrator-go.sh`, and `./scripts/build-orchestrator-go.sh` in the repository Docker Go 1.23 environment. All passed after updating two stale tests that expected a read-only lookup after an unsupported write request.
- These are local synthetic/regression checks. The deployed baseline remains 13/100 until the repaired artifact is deployed and the approved live matrix is run again. No production data was mutated and no deployment was performed.
- Remaining correctness limit: ordinary multi-source model synthesis does not have general claim-by-claim entailment verification. The docs state this explicitly; expand deterministic renderers/validators before relying on arbitrary synthesized cross-source facts.
