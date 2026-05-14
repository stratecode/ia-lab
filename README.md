# StrateCode Lab

Infraestructura de homelab gestionada con Ansible. Provisiona y configura un servidor Ubuntu como plataforma de desarrollo con IA local, VPN, observabilidad y hardening básico.

## Requisitos previos

- Ansible (core + `community.general`)
- Acceso SSH al host con la clave en `ssh/lab`
- Fichero de contraseña del vault en `~/.config/stratecode-lab/ansible-vault-pass`
- Perfil AWS `route53-dns` configurado en el host para DDNS y certificados Let's Encrypt
- Fichero `.env` con la configuración del entorno (copiar de `.env.example`)

## Inicio rápido

```bash
# Copiar y rellenar la configuración del entorno
cp .env.example .env
# Editar .env con tus valores

# Cargar variables de entorno
set -a && source .env && set +a

# Comprobar conectividad
ansible all -m ping

# Ejecutar el playbook completo
ansible-playbook playbooks/bootstrap.yml

# Dry-run con diff
ansible-playbook playbooks/bootstrap.yml --check --diff

# Ejecutar un solo role
ansible-playbook playbooks/bootstrap.yml --tags wireguard
```

## Arquitectura

Un único host Ubuntu Server en una LAN doméstica con GPU AMD (Vulkan).

```
┌─────────────────────────────────────────────────────────┐
│  lab host                                               │
│                                                         │
│  ┌──────────┐  ┌──────────┐  ┌────────────────────┐    │
│  │ llama.cpp│  │ WireGuard│  │ Nginx reverse proxy │    │
│  │ :8080    │  │ :51820   │  │ :443 / :80          │    │
│  │ :8081    │  └──────────┘  └────────────────────┘    │
│  └──────────┘                                           │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │Prometheus│  │ Grafana  │  │Alertmgr  │             │
│  │ :9090    │  │ :3000    │  │ :9093    │             │
│  └──────────┘  └──────────┘  └──────────┘             │
│  ┌──────────┐  ┌──────────┐                            │
│  │ Cockpit  │  │ fail2ban │                            │
│  │ :9091    │  └──────────┘                            │
│  └──────────┘                                           │
└─────────────────────────────────────────────────────────┘
```

## Roles

Los roles se aplican en orden de dependencia:

| # | Role | Descripción |
|---|------|-------------|
| 1 | `server_baseline` | Paquetes base, Docker, fail2ban, LVM, red, node_exporter |
| 2 | `route53_ddns` | Actualización dinámica de DNS en Route53 (systemd timer) |
| 3 | `wireguard` | Servidor VPN WireGuard + perfiles de cliente |
| 4 | `llama_cpp` | Compilación de llama.cpp con Vulkan, servicios systemd |
| 5 | `monitor` | Cockpit + Nginx reverse proxy + TLS (Let's Encrypt) |
| 6 | `observability` | Prometheus, Grafana (Docker), Alertmanager, reglas de alerta |

## Estructura del proyecto

```
.
├── ansible.cfg              # Configuración de Ansible
├── inventory.yml            # Inventario (host único)
├── .env.example             # Plantilla de variables de entorno
├── .env                     # Variables de entorno reales (excluido de git)
├── group_vars/
│   ├── all.yml              # Variables globales (lee de env vars)
│   └── vault.yml            # Secretos cifrados (ansible-vault)
├── host_vars/
│   └── lab.yml              # Overrides del host (lee de env vars)
├── playbooks/
│   └── bootstrap.yml        # Playbook principal
├── roles/                   # Un directorio por role (tasks + handlers)
├── docs/                    # Documentación por subsistema
└── ssh/                     # Par de claves SSH (excluido de git)
```

## Servicios expuestos

| URL | Servicio | Acceso |
|-----|----------|--------|
| `https://<cockpit_domain>` | Cockpit | LAN + VPN |
| `https://<observability_domain>` | Grafana | LAN + VPN |
| `https://<observability_domain>/prometheus/` | Prometheus | LAN + VPN |
| `http://127.0.0.1:8080/v1` | llama.cpp (code) | localhost |
| `http://127.0.0.1:8081/v1` | llama.cpp (chat) | localhost |

Los dominios se configuran en `group_vars/all.yml` (`cockpit_domain`, `observability_domain`).

## VPN (WireGuard)

Split tunnel que enruta solo el tráfico hacia la subred VPN (`10.66.66.0/24`) y la LAN (`192.168.0.0/24`).

- Puerto: `51820/udp` (requiere port forward en el router)
- Endpoint dinámico: configurado en `route53_ddns_record_name` (actualizado por DDNS)
- Perfiles de cliente generados en `/etc/wireguard/clients/`

Más detalles en [docs/wireguard.md](docs/wireguard.md).

## IA local (llama.cpp)

Dos instancias con backend Vulkan (AMD GPU):

| Instancia | Puerto | Modelo | Uso |
|-----------|--------|--------|-----|
| `llama-cpp-code` | 8080 | Qwen2.5-Coder-3B-Instruct (Q4_K_M) | Asistencia de código |
| `llama-cpp-chat` | 8081 | Qwen2.5-Coder-3B-Instruct (Q4_K_M) | Chat / orquestación |

API compatible con OpenAI (`/v1/chat/completions`, `/v1/completions`).

## Observabilidad

- **Prometheus** — métricas del sistema vía node_exporter
- **Grafana** — dashboards (Docker, puerto 3000)
- **Alertmanager** — alertas a Slack (opcional, requiere webhook en vault)
- **PCP** — Performance Co-Pilot para métricas avanzadas
- **Cockpit** — consola web de administración

Alertas configuradas: nodo caído, CPU alta, memoria alta, disco lleno, presión I/O, temperatura elevada.

## Gestión de secretos

```bash
# Ver secretos
ansible-vault view group_vars/vault.yml

# Editar secretos
ansible-vault edit group_vars/vault.yml
```

Variables sensibles en el vault:
- `vault_grafana_admin_password`
- `vault_alertmanager_slack_webhook_url`
- Credenciales AWS para Route53
- Claves de WireGuard

## Documentación adicional

- [Server Baseline](docs/server-baseline.md) — detalle de la configuración base
- [WireGuard](docs/wireguard.md) — configuración VPN y guía de clientes
