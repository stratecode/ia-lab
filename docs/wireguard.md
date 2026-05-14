# WireGuard

## What Ansible configures

- Installs `wireguard`, `wireguard-tools`, and `qrencode`.
- Enables IPv4 forwarding on the server.
- Generates server keys if they don't exist.
- Generates an initial client named `primary`.
- Creates the server configuration at `/etc/wireguard/wg0.conf`.
- Creates the client configuration at `/etc/wireguard/clients/primary.conf`.
- Starts `wg-quick@wg0`.

## What's needed outside the server

1. Open port `51820/udp` on the router.
2. Forward it to the server's LAN IP.
3. Install WireGuard on the client device.

## Network profile

- VPN subnet: `10.66.66.0/24`
- VPN server: `10.66.66.1`
- Initial client: `10.66.66.2`
- Mode: split tunnel

This means the client will only route through the VPN:

- `10.66.66.0/24`
- `192.168.0.0/24`

General internet traffic will continue to use the client's local connection.

## Importing the client profile

On the server:

```bash
sudo cat /etc/wireguard/clients/primary.conf
```

Or, for a mobile QR code:

```bash
sudo qrencode -t ansiutf8 < /etc/wireguard/clients/primary.conf
```

## Checking status

```bash
sudo systemctl status wg-quick@wg0
sudo wg show
ip addr show wg0
```

## What you should be able to reach once connected

- `https://<cockpit_domain>`
- `https://<observability_domain>`
- Any LAN service that accepts traffic from `10.66.66.0/24`

## Adding more clients

Add another entry to `wireguard_clients` with:

- `name`
- `address`

Then re-run:

```bash
ansible-playbook playbooks/bootstrap.yml
```
