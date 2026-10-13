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
- The router WAN IPv4 is `213.37.7.25`, matching DNS and lab egress. Its
  current port-forward table contains Teredo UDP and WireGuard UDP 51820, but
  no TCP 443 rule for `192.168.0.85`.
- The lab egress address and Route53 target both resolve to `213.37.7.25`.
- `assistant.stratecode.com` has no `/etc/hosts` override on the Mac. The macOS
  resolver, the local DNS resolver, Cloudflare and Google DNS all resolve it to
  `213.37.7.25`; no AAAA record is published. Other lab subdomains do have
  local overrides, but not this callback domain.

## Blocked external acceptance

Five external TCP 443 checks timed out from fifteen nodes across Canada,
Hungary, Iran, Germany, Hong Kong, Russia, Indonesia, Kazakhstan, Vietnam,
Cyprus, Moldova, Romania, Austria and Poland:
[probe 1](https://check-host.net/check-report/50a77205k407),
[probe 2](https://check-host.net/check-report/50a7f0e4k7d0),
[probe 3](https://check-host.net/check-report/50a89effk288),
[probe 4](https://check-host.net/check-report/50a8a49fk4ad),
[probe 5](https://check-host.net/check-report/50a8aef0k782),
[probe 6](https://check-host.net/check-report/50a8fb52k422). During the last
two probes, host captures filtered to TCP SYN packets for `192.168.0.85:443`
from outside `192.168.0.0/16` recorded zero packets. The inspected router
table has no TCP 443 forward, which explains why probes do not reach the lab.
The Mac's direct request to the public address also failed, but that result is
not conclusive because LAN hairpin NAT may be unsupported. DNS and public
egress match, and a local HTTPS request verifies TLS, so the Nginx vhost and
certificate are working inside the lab. If external probes still fail after
the forward is added, inspect ISP filtering next.

No webhook was registered with Telegram. Keep polling active until an external
probe can complete TLS and receive the expected method/path responses. After
that, use `playbooks/enable-telegram-webhook.yml`; to roll back, use
`playbooks/restore-telegram-polling.yml`. This record contains no bot token,
webhook secret, message body, or account identifier.
