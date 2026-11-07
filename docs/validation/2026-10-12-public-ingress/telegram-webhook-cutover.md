# Telegram webhook ingress check — 2026-10-12

## Result

The ingress configuration is deployed, but Telegram webhook delivery is not
activated. `lab-secretary` remains in `polling` mode. The public endpoint could
not establish TCP 443 from the operator network, so it is not safe to switch
Telegram away from polling yet.

## Evidence

- Route53 resolves `assistant.stratecode.com` through the existing DDNS CNAME.
- The ingress deployment completed with no Ansible failures. Certbot issued or
  renewed the DNS-01 certificate with the assistant hostname in its SANs.
- A private-network HTTPS probe verified the certificate (`ssl_verify_result=0`).
  `POST /telegram/webhook` without Telegram's secret returned 401 from the
  secretary authentication middleware; `/` returned 404. The service was in
  polling mode during this probe, so this verifies the vhost/proxy reachability,
  not webhook handler acceptance.
- A public HTTPS probe failed before TLS with curl error 7 (TCP connection
  refused) on port 443. Router forwarding or upstream filtering still needs
  investigation from a genuinely external network.
- Telegram preflight: bot valid, webhook unset, zero pending updates. The host's
  `/etc/lab-secretary/secretary.env` reports `LAB_SECRETARY_TELEGRAM_MODE=polling`.
- Running `enable-telegram-webhook.yml` without the explicit reachability
  verification flag stopped at its first assertion and made no changes.

## Checks

- `./scripts/test-orchestrator-go.sh` — passed.
- `bats tests/properties/prop_public_telegram_ingress_contract.bats tests/properties/prop_telegram_webhook_playbooks.bats` — passed.
- Ingress, enable, and rollback Ansible syntax checks — passed.

## Remaining acceptance

Forward IPv4 TCP 443 on the router to the lab's HTTPS listener, verify the exact
endpoint and certificate from outside the LAN/VPN, then use
`playbooks/enable-telegram-webhook.yml`. Send an actual private Telegram message
and confirm the secretary reply. Keep polling if any external check fails.
