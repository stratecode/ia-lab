# Telegram webhook ingress validation

Date: 2026-10-13

## Current state

- `assistant.stratecode.com` resolves through Route53 to `213.37.7.25`, which
  matches the router WAN IPv4 and the lab's public egress address. There is no
  published AAAA record and no `/etc/hosts` override for this hostname.
- The router has an enabled `Assistant HTTPS` forward: public TCP 443 to
  `192.168.0.85:443`. It remained present after reloading the router page.
- Nginx validates HTTPS locally and listens on IPv4 TCP 443. External HTTPS
  GET probes to `/telegram/webhook` returned 403, the expected response because
  the endpoint accepts POST only. Nginx logged `GET /telegram/webhook 403`, and
  a host capture observed an external source reaching `192.168.0.85:443`.
- `playbooks/enable-telegram-webhook.yml` passed its external-ingress gate and
  completed successfully. Its private-secret checks rejected the wrong secret,
  accepted and deduplicated a synthetic unauthorized update, and Telegram
  accepted the webhook URL without dropping pending updates.
- `playbooks/check-telegram.yml` reports `stratecode_bot` valid, webhook
  configured, and zero pending updates.
- The activation playbook keeps voice enabled. `lab-secretary`, `lab-whisper`,
  and `lab-piper` are active; the deployed secretary environment has
  `LAB_SECRETARY_VOICE_ENABLED=true`.

External probe evidence after adding the router rule:

- [TCP 443 probe](https://check-host.net/check-report/50aa4a6dk8b): two of
  three nodes connected; one timed out.
- [HTTPS route probe 1](https://check-host.net/check-report/50aa53bfk558):
  three of three nodes received HTTP 403 from `213.37.7.25`.
- [HTTPS route probe 2](https://check-host.net/check-report/50aa58f5k91c):
  three of three nodes received HTTP 403 from `213.37.7.25`.
- [HTTPS route probe 3](https://check-host.net/check-report/50aa6a9dk650):
  two nodes received HTTP 403; one timed out.

## Remaining live check

The transport, TLS route and Telegram webhook registration are verified. One
real update from an authorized Telegram chat is still needed to confirm a full
Telegram-to-secretary delivery. This record contains no bot token, webhook
secret, message body, or account identifier.

## Before the router rule was added

Five external TCP 443 checks timed out from fifteen nodes across several
countries:
[probe 1](https://check-host.net/check-report/50a77205k407),
[probe 2](https://check-host.net/check-report/50a7f0e4k7d0),
[probe 3](https://check-host.net/check-report/50a89effk288),
[probe 4](https://check-host.net/check-report/50a8a49fk4ad),
[probe 5](https://check-host.net/check-report/50a8aef0k782),
[probe 6](https://check-host.net/check-report/50a8fb52k422). Host captures
recorded zero external SYN packets. Router inspection showed the WAN IP was
correct but the TCP 443 forward was absent; only Teredo UDP and WireGuard UDP
51820 were configured. This explains the prior failure.
