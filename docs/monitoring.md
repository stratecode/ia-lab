# Monitorización, SAI y avisos por Telegram

Rol: `roles/lab_alerting` · Playbook: `playbooks/lab-alerting.yml`

Este documento describe qué hace el rol, cómo aplicarlo y qué cambios **requieren tocar otros roles** (no se hicieron para evitar conflictos). Todo lo que contiene el rol es una copia literal de lo que corre en el host desde el 2026-10-15.

## Qué instala

| Pieza | Dónde | Notas |
|---|---|---|
| Avisos Telegram | `/etc/prometheus/alertmanager.yml`, `alertmanager_templates/telegram.tmpl`, `/etc/prometheus/telegram_bot_token` (640 root:prometheus) | Bot de la secretaria; mensajes en español; latido diario silencioso |
| Reglas | `promreg/lab-ups-alerts.yml`, `lab-extra-alerts.yml`, `lab-recording-rules.yml` | SAI, salud, contenedores/certificados y métricas agregadas |
| SAI | NUT standalone, driver `usbhid-ups` (CyberPower VP700ELCD) | Apagado ordenado con batería ≤10 % o ≤300 s de autonomía |
| Exportadores | `/usr/local/bin/{nut,docker,cert}-textfile-exporter` + timers | Escriben en el textfile collector de node_exporter |
| Dashboard | `grafana/dashboards/ups.json` (uid `lab-ups`) | |
| Clave llama.cpp | `sync-llama-prom-key` + `.path` | Copia la `--api-key` del unit a `credentials_file` de Prometheus |

## Variables

| Variable | Origen | Descripción |
|---|---|---|
| `LAB_TELEGRAM_BOT_TOKEN` | entorno (`.env`) | Mismo token que usa `lab-secretary` |
| `LAB_TELEGRAM_ALLOWED_USERS` | entorno | Se usa el primer id como `chat_id` |
| `vault_nut_upsmon_password` | `group_vars/vault.yml` | Contraseña del usuario `upsmon` de NUT (generar con `openssl rand -hex 16`) |
| `lab_alerting_enable_*` | defaults | Permiten apagar cada pieza |

Ningún secreto está en el repositorio.

## Aplicar

```bash
set -a && source .env && set +a
ansible-playbook playbooks/lab-alerting.yml --check --diff
ansible-playbook playbooks/lab-alerting.yml
# por partes: --tags alertmanager | rules | ups | exporters | llama-key | grafana
```

> El rol **no se ha ejecutado con Ansible** (no hay Ansible en el servidor). Sí se validó: YAML, Jinja, `promtool check rules`, `amtool check-config` sobre la plantilla renderizada (idéntica al fichero del host), `bash -n` y `py_compile` de los scripts, y que los ficheros del rol son byte a byte los del host. Usar `--check --diff` primero.

## Integración con `observability`

La configuración activa de Alertmanager pertenece a `lab_alerting` y se instala en `/etc/prometheus/alertmanager.yml`, que es la ruta por defecto del servicio. `observability` conserva el listener y el job de scrape, pero ya no escribe una segunda configuración en `/etc/alertmanager/alertmanager.yml`.

El job de inferencia se llama `llama-cpp-main`, asigna el `instance` esperado por las reglas y lee la clave desde el fichero sincronizado por `sync-llama-prom-key`:
  ```yaml
  authorization:
    credentials_file: /var/snap/prometheus/current/llama-api-key
  ```
La plantilla también incluye los jobs `llm-telemetry` y `lab-secretary` cuando sus servicios están habilitados. Así, un redeploy de `observability` no elimina esos scrapes ni deja una clave literal obsoleta.

Prometheus (snap) solo carga `/var/snap/prometheus/current/promreg/*.yml`. El rol despliega ahí las reglas de alerta y `lab-recording-rules.yml`; en el árbol del repositorio no hay tareas que recreen `/etc/prometheus/rules/lab-alerts.yml`.

### 4. cAdvisor no funciona en este host
Docker 29 con el almacén de imágenes de containerd: cAdvisor v0.51 y v0.53 fallan con "failed to identify the read-write layer ID" y no exponen `name`. Por eso se usa `docker-textfile-exporter` (`docker_container_*`). Si se actualiza cAdvisor a una versión que lo soporte, se pueden reactivar las reglas de grabación `container_*`.

### Límites y tareas de mantenimiento pendientes
- Datasource de Grafana: apunta a la URL pública (`https://{{ observability_domain }}/prometheus`); mejor una URL local para que los dashboards no dependan de DNS/TLS.
- `orchestrator_pg_backup_last_success_timestamp_seconds` está parada desde julio (orquestador sin uso); no hay alerta a propósito.
- Certificado `lab.stratecode.com-0001` de certbot (caduca 2026-12-27): nginx usa `lab.stratecode.com`. Revocar/borrar el sobrante.
- `cert-textfile-exporter` tiene la lista de dominios fija (`lab`, `monitor`, `chat`, `assistant`); si cambian, editar `files/bin/cert-textfile-exporter`.

## Alertas

SAI (`category=power`, repetición cada 30 min): `UPSOnBattery`, `UPSLowBattery`, `UPSReplaceBattery`, `UPSOverload`, `UPSInputVoltageAbnormal`, `UPSMonitoringDown`.
Salud: `SystemdUnitFailed`, `SmartDiskUnhealthy`, `OOMKillDetected`, `RootFilesystemFillingUp`, `LabSecretaryDown`, `LLMTelemetryDown`, `TextfileCollectorStale`.
Contenedores: `ContainerNotRunning`, `ExpectedContainerMissing` (grafana, open-webui), `ContainerUnhealthy`, `ContainerRestarting`, `ContainerMemoryHigh`, `DockerExporterDown`.
Certificados: `CertExpiringSoon` (<21 d), `CertExpiryCritical` (<7 d), `CertProbeFailed`, `CertMetricsStale`.
Latido: `Watchdog` → un mensaje silencioso cada 24 h; si un día no llega, algo de la cadena (Prometheus, Alertmanager o el servidor) está caído.

## Verificación hecha en el host (2026-10-15)

- Alerta de prueba por la API de Alertmanager → Telegram: recibida.
- Contenedor desechable parado → `ContainerNotRunning` → Telegram: recibido.
- Corte real de red del SAI → `UPSOnBattery` en ~75 s desde el corte (el SAI y NUT tardan ~10-15 s en declarar `OB`; luego 30 s de `for` y 10 s de agrupación) → Telegram: recibido.

## Pendiente de probar
- `UPSLowBattery` y el apagado ordenado por batería baja no se han probado (requeriría agotar la batería).
- Que el playbook aplicado con Ansible deja el host sin cambios (`--check --diff` debe salir sin `changed`).
