# Public callback and Telegram ingress design

**Status:** design approved in conversation; awaiting written-spec review  
**Date:** 2026-10-12

> **Current-state note (2026-10-19):** this is a historical ingress design.
> Jira OAuth enrollment and its callback route were retired when Jira moved to
> direct REST/JQL with a private service token. Only Granola OAuth remains.

## Goal

Provide a stable public HTTPS origin for the secretary's Granola OAuth
callbacks and Telegram webhook while keeping the browser UI and internal model
services private. Use the existing home router, lab Nginx, and Route53 dynamic
DNS rather than adding a cloud relay.

## Current state and constraints

- Route53 DDNS updates `vpn-lab.stratecode.com` to the router's current IPv4
  address. The router reports the same public address, `213.37.7.25`.
- The lab is `192.168.0.85`; Nginx listens on TCP 443. Three external TCP probes
  timed out while control probes to Google HTTPS succeeded. The router's UPnP
  interface reports no TCP 443 mapping. A manually configured router rule or ISP
  filtering is not ruled out.
- `assistant.stratecode.com` returned no A, AAAA, CNAME, MX, TXT, or CAA records
  in the DNS checks performed for this design. Verify its Route53 zone state
  again before creating records.
- Existing Nginx vhosts for chat, cockpit, and monitoring enforce LAN/VPN access
  lists. Open WebUI is not to become public. The secretary listens only on
  `127.0.0.1:8082`.
- The OAuth helper runs on the operator Mac, binds callbacks to loopback, uses
  PKCE S256 and state validation, and stores tokens outside the repository. The
  lab SSH daemon permits TCP forwarding while keeping remote forwarded ports on
  loopback (`GatewayPorts no`).
- Telegram currently uses `getUpdates` polling. Its updates are restricted to
  private chats and an explicit user allowlist. Jira and Granola commands are
  read-only.

## Selected architecture

Use `assistant.stratecode.com` as a CNAME to `vpn-lab.stratecode.com`, so the
existing DDNS updater continues to own the changing A record. Route only
external IPv4 TCP 443 from the router to `192.168.0.85:443`; reserve the lab's
LAN address so the forwarding target remains stable. Do not use UPnP and do not
forward ports 80, 22, or any application port.

Nginx will have a dedicated HTTPS vhost for `assistant.stratecode.com`. Obtain
and renew its certificate through the existing Route53 DNS-01 Certbot setup, so
public port 80 is unnecessary. The vhost will reject unknown hosts and expose
only these exact paths:

| Public path | Method | Private destination | Purpose |
|---|---|---|---|
| `/telegram/webhook` | POST | `127.0.0.1:8082` | Telegram updates |
| `/oauth/granola/callback` | GET | loopback-forwarded port `18766`, path `/callback` | One Granola OAuth enrollment |

All other paths and methods return a non-success response. Existing chat,
Cockpit, and monitoring vhosts retain their current access controls. The new
vhost does not proxy to Open WebUI, the model, telemetry, or any administrative
interface.

## OAuth callback flow

The operator starts the existing Granola helper on the Mac and opens the
authorization URL in a browser. The helper registers the exact HTTPS callback
URI and continues to bind its local handler only to
`127.0.0.1`. During the short enrollment window, an SSH reverse forward from the
Mac maps the lab loopback port (`18766`) to the Mac's
loopback listener. Nginx forwards only the matching public callback path to that
loopback port. The SSH listener is never bound to a LAN or public interface.

Preserve PKCE, state checks, provider-host allowlists, token validation, and the
existing private token destination. OAuth callback access logs must record only
the normalized URI path and status, never `$request` or query arguments, since
authorization codes and state arrive in the query string. Do not log callback
bodies, codes, tokens, or provider error bodies.

Before treating Granola as connected, verify the provider accepts the
exact custom redirect URI during dynamic client registration and consent. If a
provider rejects it, stop that provider's enrollment and use only a
provider-supported callback pattern; do not weaken PKCE, state, endpoint
validation, or redirect allowlists. Telegram ingress can proceed independently.

## Telegram webhook flow

After the public TLS route passes its external checks, stop polling and
configure Telegram's webhook URL as
`https://assistant.stratecode.com/telegram/webhook`. Use Telegram's
`X-Telegram-Bot-Api-Secret-Token` header with a random secret stored only in
root-protected host state and the secretary's private runtime configuration.
Compare it in constant time. Reject missing or invalid secrets before parsing
or processing the update. Do not put the bot token or webhook secret in the
URL.

The secretary will have an explicit mutually exclusive Telegram mode:
`disabled`, `polling`, or `webhook`. Preserve `polling` as the current deployed
mode until the ingress acceptance checks pass. In `webhook` mode the service
starts the callback handler and worker but never calls `getUpdates`; in
`polling` mode it never accepts webhook updates. The handler accepts bounded
JSON POSTs, persists an update to a durable queue, deduplicates by `update_id`,
and returns success only after durable acceptance. A worker processes queued
updates through the existing allowlist and read-only secretary flow.
Processing and replies are at-least-once across a crash; a crash after Telegram
accepts a reply but before local completion is recorded can produce a duplicate
reply. Do not claim exactly-once delivery.

Switch from polling only after the route, secret validation, queue persistence,
and a synthetic update pass. Stop polling first, deploy `webhook` mode, then
call `setWebhook` with `drop_pending_updates=false` and verify
`getWebhookInfo`. Preserve pending updates. Telegram polling and webhooks are
mutually exclusive. Keep a rollback procedure that calls `deleteWebhook` with
pending updates preserved, deploys `polling` mode, and resumes from the
persisted offset. Telegram retains pending updates for at most 24 hours.

## Security and operations

- Router forwarding is the public network boundary; forward only TCP 443 to the
  reserved lab address. Keep SSH private to the LAN/VPN.
- Keep Nginx as the only public listener. Bind secretary, model, telemetry, and
  callback listeners to loopback. Retain systemd hardening and existing chat
  access lists.
- Limit the Telegram route to POST and a bounded request body. Verify the
  Telegram secret header in the application; do not rely on source IP ranges as
  the sole authentication method.
- Use a sanitized access-log format for callback paths that excludes query
  strings. Do not log Telegram update bodies or secrets.
- Keep generated webhook secret, OAuth tokens, connector state, `.env`, vault,
  and SSH key material out of Git and deployment output. Do not alter existing
  secrets to implement this design.
- Do not configure IPv6 ingress as part of this increment. Verify that the
  router has no independent IPv6 exposure before acceptance.
- No AWS compute, load balancer, tunnel service, or additional recurring cloud
  charge is introduced. AWS remains limited to Route53 DNS and DNS-01 challenge
  access already used by the lab.

## Acceptance checks

1. Confirm the hostname is available in the authoritative Route53 zone and
   resolves through the existing DDNS name to the router's current IPv4.
2. Confirm the certificate covers `assistant.stratecode.com`, is trusted, and
   renews through DNS-01 without opening port 80.
3. Configure the router's manual TCP 443 forward to `192.168.0.85:443`, then
   verify reachability from outside the LAN. If it still times out, diagnose
   router policy versus ISP filtering before proceeding.
4. From an external network, verify the new vhost's exact routes, method and
   default-host rejection. Confirm `https://chat.stratecode.com` remains
   inaccessible outside its existing LAN/VPN allowlist and that model and
   telemetry ports are not reachable publicly.
5. Verify a wrong Telegram secret is rejected without queuing an update; verify
   a valid synthetic update is durably accepted once and duplicate `update_id`
   values are not processed twice.
6. Verify both OAuth helpers receive the callback through the SSH reverse
   forward, reject invalid state, and keep codes out of logs. Complete Jira and
   Granola consent separately; report a provider as connected only after a
   successful read-only query.
7. Verify Telegram webhook metadata, deliver a real allowlisted private
   message/reply, and test rollback to polling without dropping pending updates.

## Rollback

Remove the router's TCP 443 forwarding rule and the assistant DNS record, revert
the dedicated Nginx vhost and certificate SAN change, then call Telegram's
`deleteWebhook` with pending updates preserved and resume polling. Retain the
private queue and existing polling offset until recovery is verified. Revoke
any OAuth client/token state created for an enrollment that is abandoned.

## Out of scope

Public Open WebUI, public Cockpit/monitoring, WhatsApp, Slack, voice processing,
general-purpose APIs, model ports, and AWS-hosted ingress are not part of this
increment.

## Primary references

- [Telegram webhook guide](https://core.telegram.org/bots/webhooks) and
  [Telegram bot FAQ](https://core.telegram.org/bots/faq): HTTPS/IPv4/443,
  webhook versus polling, and webhook setup/rollback behavior.
- Existing repository contracts: `roles/monitor/tasks/main.yml`,
  `roles/lab_secretary/tasks/main.yml`, `scripts/connect-secretary.py`,
  `internal/secretary/telegram.go`, and `docs/secretary-operation.md`.
