# Donna Granola follow-ups

## What it does

`donna-granola-events` receives only signed `note.generated` callbacks for the
Granola `personal` scope. It persists and deduplicates each event before
returning `202`, then retrieves the note summary asynchronously through the
fixed Granola API host. It does not request or store transcripts, call Jira, or
create tasks.

The generated summary is delivered to the authorized private Telegram chat. The
same message is retained for the allowlisted operator and shown in that
operator's next authenticated Open WebUI conversation. Donna receives that
notification as conversation context before answering and retains it in the
private conversation history for later follow-ups. Acknowledging it in Open
WebUI does not remove the Telegram message. Telegram delivery is at-least-once
if the process stops after Telegram accepts a message but before the local
delivery marker is saved.

Donna labels actions copied from the summary's explicit `Acciones`, `Actions`,
`Action items`, `Next steps`, `Siguientes pasos`, `Próximos pasos`,
`Siguientes acciones`, or `Próximas acciones` section as Jira candidates. The
message says that no issue was created. It marks an absent note date or owner
as absent; it does not infer one.

## Granola prerequisites and enrollment

Granola documents webhooks for Business and Enterprise. Use a personal API key
that can access the `personal` scope. That scope includes notes you own, notes
shared directly with you, and notes in private folders shared with you. A
workspace API key cannot subscribe to this personal scope. The webhook URL must
be publicly reachable over HTTPS.

1. Set `LAB_GRANOLA_API_KEY`, `LAB_GRANOLA_OPERATOR_ID`, and
   `LAB_GRANOLA_NOTIFICATION_TOKEN` in the private, untracked `.env`. Leave
   `LAB_GRANOLA_WEBHOOK_SECRET` blank for this first deployment.
2. Build and deploy the service with
   `scripts/build-donna-granola-events.sh` and
   `ansible-playbook playbooks/deploy-donna-granola-events.yml`. With no
   signing secret yet, the public callback returns `503` while health remains
   available; this makes Granola retry rather than treating the endpoint as a
   permanently invalid `4xx`.
3. Confirm that
   `https://assistant.stratecode.com/webhooks/granola` reaches the lab's exact
   HTTPS route. The service itself listens only on `127.0.0.1:8085`.
4. In Granola, create a webhook for this URL with scope `personal` and event
   `note.generated` only. Do not leave the event list empty: Granola's default
   is to subscribe to every event. Do not add `public`, `workspace`,
   `note.edited`, or `note.access_granted` for this first increment.
5. Save the returned `whsec_...` signing secret immediately. Granola shows it
   only when the endpoint is created. Put it in the private, untracked `.env`
   as `LAB_GRANOLA_WEBHOOK_SECRET`.
6. Re-source private environment variables, then rerun the deployment playbook
   immediately. The callback stops returning `503` once the signing secret is
   loaded.
7. Create a new test note after deployment. Verify one
   Telegram message, then verify that the matching Open WebUI operator sees
   its pending notification on their next authenticated conversation.
   The playbook installs the service and exact Nginx route, and restarts only
   the secretary, Granola event service and Nginx when their files change.

Do not create the webhook before the service and public route are ready. If the
Granola account does not show the required plan or scope, stop at deployment;
the software cannot widen account permissions.

## Settings

| Variable | Use |
|---|---|
| `LAB_GRANOLA_API_KEY` | Read the note summary from Granola. |
| `LAB_GRANOLA_WEBHOOK_SECRET` | Verify Standard Webhooks signatures. |
| `LAB_GRANOLA_NOTIFICATION_TOKEN` | Authenticate event-service calls to the loopback secretary endpoint. |
| `LAB_GRANOLA_OPERATOR_ID` | Select the one allowlisted operator and private Telegram chat. |

All four values belong in private configuration only. Ansible tasks that write
them are marked `no_log`, and the generated environment files are mode `0600`.
The event queue is in the service's private systemd state directory. The
secretary retains formatted notifications by operator until the next
authenticated Open WebUI conversation consumes them.

## Health, failure, and recovery

- Local health: `curl --fail http://127.0.0.1:8085/healthz`.
- Nginx forwards only `POST /webhooks/granola`; its default state is `404`.
- Bad signatures and timestamps are rejected before the queue is touched.
- Granola API 404/409/425 and temporary failures use bounded exponential
  retries. Exhausted or permanent failures are kept in the private `failed`
  queue directory for inspection. `journalctl -u donna-granola-events` records
  retry and failure event IDs with bounded categories and attempt numbers;
  note contents and credentials are not logged.
- Completed event IDs remain on disk for restart-safe deduplication.
- If the event reaches the queue but no message appears, inspect
  `journalctl -u donna-granola-events` and
  `journalctl -u lab-secretary`; never print either service's environment file.
- Granola retries deliveries for up to four days and may disable an endpoint
  after repeated failures. After restoring the receiver, re-enable it in
  Granola's webhook settings.

## Rollback

1. Disable or delete the endpoint in Granola to stop incoming events.
2. Replace `/etc/nginx/snippets/donna-granola-webhook.conf` with `return 404;`
   and run `nginx -t && systemctl reload nginx`.
3. Stop and disable `donna-granola-events`.
4. Remove the secretary drop-in
   `/etc/systemd/system/lab-secretary.service.d/granola-notifications.conf`
   and `/etc/lab-secretary/granola-notification.env`, then run
   `systemctl daemon-reload && systemctl restart lab-secretary`.
5. Remove the event service environment file and revoke the Granola API key and
   webhook endpoint. Preserve or securely remove the private queue according
   to the operator's retention decision.

## Validation boundary

Automated checks use synthetic notes, signatures, and a Telegram test server.
They prove the local code path, not the Granola account plan, API-key scope,
public DNS/TLS path, or delivery of a real meeting. Those require the operator's
account-side enrollment and a real test event.

Sources: [Granola webhooks](https://docs.granola.ai/webhooks.md),
[Get Note API](https://docs.granola.ai/api-reference/get-note.md),
[Create Webhook Endpoint API](https://docs.granola.ai/api-reference/create-webhook-endpoint.md).
