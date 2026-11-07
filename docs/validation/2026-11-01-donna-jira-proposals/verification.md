# Donna Jira task proposal verification

Date: 2026-11-01

## Verified locally

- `./scripts/test-orchestrator-go.sh` — passed; includes the secretary route,
  persistent candidate metadata, clarification continuity, Telegram callbacks,
  and Jira action service tests.
- `./scripts/build-orchestrator-go.sh` — final full run passed and built the
  Jira action-service artifact. An earlier run hit a transient failure in the
  unrelated `internal/llmtelemetry/TestStreamPreservesBytesAndMeasuresRealFirstToken`;
  subsequent full runs passed.
- `docker run --rm -v "$PWD:/workspace" -w /workspace python:3.12-slim /bin/sh
  -lc 'pip -q install pydantic && python -m unittest
  roles.open_webui.files.donna_jira_actions_test -v'` — six tests passed.
- `ansible-playbook --syntax-check -i lab, playbooks/deploy-donna-jira-actions.yml`
  — passed.
- `git diff --check` — passed.

The synthetic Open WebUI test starts with a Granola action whose note link is
missing, submits the link as the user's clarification, and verifies that Donna
returns the exact Jira draft marker without calling the language model. Fake
Jira endpoints are used. No Jira credentials were read and no Jira or Granola
provider writes were made. Candidate source metadata is pruned when it is older
than 30 days at secretary startup.

## Not activated

The playbook was not applied and was not run in check mode against the lab host.
The live service needs separately provisioned Jira OAuth and service tokens,
an explicit project/type allowlist, and an administrator to import the Action
and set its valves in Open WebUI. Time tracking remains deferred.

## Live channel smoke test — 2026-11-01

- **Open WebUI:** sent a fictitious, no-write prompt in a temporary chat on
  `chat.stratecode.com` (UI reported v0.12.2). A response arrived, but no Jira
  Action buttons appeared. The model answered generically and listed Jira
  operations outside this feature's supported create/comment scope. Workspace
  showed `Tools 0`. The temporary chat was not saved.
- **Telegram:** sent the same kind of fictitious, no-write request to the live
  Donna bot. It replied, but produced a generic checklist and no Jira inline
  buttons. The test message remains in the Telegram conversation.
- Neither test supplied a Granola candidate notification, so the live
  notification → proposal → discard path could not be exercised. No Jira or
  Granola records were created or changed.

**Result:** both chat transports responded; the Jira proposal feature is not
active in either live channel. End-to-end verification requires deployment of
the action service and Secretary configuration, plus importing and configuring
the Open WebUI Action. Deployment was not performed during this smoke test.
