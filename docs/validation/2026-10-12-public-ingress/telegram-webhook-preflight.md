# Telegram webhook ingress preflight

Date: 2026-10-13

## Passed

- `assistant.stratecode.com` resolves through the Route53 CNAME to the same
  public IPv4 currently reported by the lab host.
- The lab's Nginx configuration passes `nginx -t` and listens on IPv4 TCP 443.
- A loopback HTTPS GET to `/telegram/webhook` returns 403, confirming the exact
  route is present and rejects the wrong method.
- The playbook contract tests, Go secretary tests and Ansible syntax checks pass.
- `lab-secretary` is deployed in polling mode; Telegram has not been switched.

## Blocked external acceptance

Two external TCP 443 checks timed out from six nodes across Canada, Hungary,
Iran, Germany, Hong Kong and Russia:
[first probe](https://check-host.net/check-report/50a77205k407),
[latest probe](https://check-host.net/check-report/50a7f0e4k7d0). The Mac's
direct request also failed, but that result alone may be affected by NAT
loopback. DNS and the lab's public egress address matched, Nginx listens on
0.0.0.0:443, and the host input chain has ACCEPT policy, so the remaining
unverified boundary is the router forward or upstream ISP path.

No webhook was registered with Telegram. Keep polling active until an external
probe can complete TLS and receive the expected method/path responses. After
that, use `playbooks/enable-telegram-webhook.yml`; to roll back, use
`playbooks/restore-telegram-polling.yml`. This record contains no bot token,
webhook secret, message body, or account identifier.
