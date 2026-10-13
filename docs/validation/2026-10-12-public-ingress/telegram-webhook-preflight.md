# Telegram webhook ingress preflight

Date: 2026-10-13

## Passed

- `assistant.stratecode.com` resolves through the Route53 CNAME to the same
  public IPv4 currently reported by the lab host.
- The lab's Nginx configuration passes `nginx -t` and listens on IPv4 TCP 443.
- A loopback HTTPS GET to `/telegram/webhook` verifies the certificate and
  returns 403, confirming the exact route is present and rejects the wrong
  method.
- The playbook contract tests, Go secretary tests and Ansible syntax checks pass.
- `lab-secretary` is deployed in polling mode; Telegram has not been switched.
- The router rule is reported as WAN IPv4 TCP 443 to `192.168.0.85:443`.
- The lab egress address and Route53 target both resolve to `213.37.7.25`.

## Blocked external acceptance

Five external TCP 443 checks timed out from fifteen nodes across Canada,
Hungary, Iran, Germany, Hong Kong, Russia, Indonesia, Kazakhstan, Vietnam,
Cyprus, Moldova, Romania, Austria and Poland:
[probe 1](https://check-host.net/check-report/50a77205k407),
[probe 2](https://check-host.net/check-report/50a7f0e4k7d0),
[probe 3](https://check-host.net/check-report/50a89effk288),
[probe 4](https://check-host.net/check-report/50a8a49fk4ad),
[probe 5](https://check-host.net/check-report/50a8aef0k782). During the last
probe, a host capture filtered to TCP SYN packets for `192.168.0.85:443` from
outside `192.168.0.0/16` recorded zero packets. This places the failure before
the lab host; the router's configured forward is not delivering the probes,
or an upstream network is filtering them. DNS and public egress match, and a
local HTTPS request verifies TLS, so the Nginx vhost and certificate are
working inside the lab.

No webhook was registered with Telegram. Keep polling active until an external
probe can complete TLS and receive the expected method/path responses. After
that, use `playbooks/enable-telegram-webhook.yml`; to roll back, use
`playbooks/restore-telegram-polling.yml`. This record contains no bot token,
webhook secret, message body, or account identifier.
