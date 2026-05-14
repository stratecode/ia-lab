# WireGuard en el lab

## Qué monta Ansible

- Instala `wireguard`, `wireguard-tools` y `qrencode`.
- Activa reenvío IPv4 en el servidor.
- Genera claves del servidor si no existen.
- Genera un primer cliente llamado `primary`.
- Crea la configuración del servidor en `/etc/wireguard/wg0.conf`.
- Crea la configuración del cliente en `/etc/wireguard/clients/primary.conf`.
- Arranca `wg-quick@wg0`.

## Qué hace falta fuera del servidor

1. Abrir en el router el puerto `51820/udp`.
2. Redirigirlo a la IP LAN del servidor.
3. Instalar WireGuard en el dispositivo cliente.

## Perfil de red

- Subred VPN: `10.66.66.0/24`
- Servidor VPN: `10.66.66.1`
- Cliente inicial: `10.66.66.2`
- Modo: split tunnel

Eso significa que el cliente solo enviará por la VPN:

- `10.66.66.0/24`
- `192.168.0.0/24`

Tu tráfico general de Internet seguirá saliendo por la conexión local del cliente.

## Cómo importar el cliente

En el servidor:

```bash
sudo cat /etc/wireguard/clients/primary.conf
```

O, si quieres QR para móvil:

```bash
sudo qrencode -t ansiutf8 < /etc/wireguard/clients/primary.conf
```

## Cómo comprobar el estado

```bash
sudo systemctl status wg-quick@wg0
sudo wg show
ip addr show wg0
```

## Qué deberías poder hacer al conectarte

- Llegar a `https://<cockpit_domain>`
- Llegar a `https://<observability_domain>`
- Llegar a cualquier servicio de tu LAN que acepte tráfico desde `10.66.66.0/24`

## Si quieres más clientes

Hay que añadir otro bloque en `wireguard_clients` con:

- `name`
- `address`

Y volver a ejecutar:

```bash
ansible-playbook playbooks/bootstrap.yml
```
