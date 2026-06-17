# Codex Remote Verification Audit

Date: 2026-06-17

## Scope

Objective under audit:

- prove that Codex works remotely against the lab model while operating on the user's local repository
- run exhaustive verification against the real repository path `/Users/fran.lopez/Development/smartfm/api`
- determine whether a literal `100%` guarantee is supportable from current evidence

This audit only uses artifacts captured inside this repository workspace.

Rebuild the consolidated summary with:

```bash
./scripts/summarize-codex-remote-verification.py > tmp/verify-codex-lab-remote-summary.json
```

## Requirements Matrix

### Requirement 1

Codex must use the lab-hosted provider instead of an OpenAI/ChatGPT-backed provider.

Evidence:

- `tmp/verify-codex-lab-remote/real-repo.log`
- `tmp/verify-codex-lab-remote/real-repo-worktree.log`
- soak artifacts under `tmp/verify-codex-lab-remote-soak/`

Observed:

- logs record `provider: lab-codex-gateway`
- transcripts record `model_provider":"lab-codex-gateway"`

Audit result:

- proved

### Requirement 2

Codex must operate on the exact local repo path provided by the operator.

Evidence:

- `tmp/verify-codex-lab-remote/real-repo.log`
- `tmp/verify-codex-lab-remote-soak/20260616-212631/run-1/stdout.log`
- `tmp/verify-codex-lab-remote-soak/20260616-221331/run-25/stdout.log`

Observed:

- logs record `workdir: /Users/fran.lopez/Development/smartfm/api`
- transcripts record `cwd":"\/Users\/fran.lopez\/Development\/smartfm\/api"`

Audit result:

- proved

### Requirement 3

Codex must apply a real write in the provided local repository.

Evidence:

- `tmp/verify-codex-lab-remote/real-repo.log`
- repeated real-repo sections in soak run stdout logs

Observed:

- command execution wrote `AGENT_REMOTE_REAL_REPO_CHECK.txt` to the real repo path
- the harness verified exact marker contents and then removed the file

Audit result:

- proved

### Requirement 4

Codex must be able to modify a tracked file from the same repository without confusing the workspace.

Evidence:

- `tmp/verify-codex-lab-remote/real-repo-worktree.log`
- repeated worktree sections in soak run stdout logs

Observed:

- Codex appended `<!-- codex-lab-remote-worktree-check -->` to `README.md`
- the harness verified the diff was limited to `README.md`
- the harness removed the detached worktree afterward

Audit result:

- proved

### Requirement 5

The behavior must hold under repeated execution, not only in one happy-path run.

Evidence:

- `tmp/verify-codex-lab-remote-soak/20260616-212631/report.json`
- `tmp/verify-codex-lab-remote-soak/20260616-215803/report.json`
- `tmp/verify-codex-lab-remote-soak/20260616-221331/report.json`

Observed:

- run set A: `10/10` passed with `retries_per_trial=3`
- run set B: `5/5` passed with `retries_per_trial=1`
- run set C: `25/25` passed with `retries_per_trial=3`
- aggregate recent sample: `40/40` passed, `0` failed

Audit result:

- proved

### Requirement 6

The real repository must not be left dirty after the verification workflow.

Evidence:

- end-of-run checks captured in prior sessions
- current workspace-side audit notes recorded an empty `git status --short` for the real repo during the final soak review

Observed:

- no residual marker file is expected
- detached worktrees were removed

Audit result:

- proved from captured checks, but not re-runnable now from this restricted workspace

### Requirement 7

A literal `100%` future guarantee must be supportable from evidence.

Evidence:

- all available soak reports
- known architecture: external Codex client, network transport, local model, non-deterministic runtime

Observed:

- the empirical sample is extremely strong
- the system still depends on components outside this repository's control
- no finite test sample can prove impossibility of future failure for this kind of distributed non-deterministic system

Audit result:

- not provable

## Quantitative Summary

- strong verifier on real repo: passed
- tracked-file worktree verifier: passed
- soak with retries: `10/10`
- soak without retries: `5/5`
- long soak with retries: `25/25`
- aggregate recent cycles: `40/40`
- aggregate recent failures: `0`
- consolidated machine-readable summary: `tmp/verify-codex-lab-remote-summary.json`

## Interpretation

What the evidence supports:

- Codex is operating through the lab gateway, not through an OpenAI fallback path
- Codex can write to the operator's exact local repository path
- Codex can modify tracked repository content in a controlled detached worktree derived from that same repo
- the workflow is stable under repeated execution, including a sample without retry assistance

What the evidence does not support:

- a mathematical guarantee that failure probability is zero
- a guarantee that future client, network, or model behavior can never regress

## Final Audit Decision

Operational conclusion:

- the system is verified to a very high confidence level and is fit for practical use as a remote-local Codex workflow

Literal guarantee conclusion:

- a strict `100%` guarantee is not supportable from current evidence or from the nature of the system itself

Recommended acceptance criterion:

- accept the workflow as validated if it maintains `>= 99.5%` success on repeated real-repo soak verification with clean-repo postconditions and transcript/provider consistency
