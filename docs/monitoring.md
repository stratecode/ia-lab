# Monitorización, SAI y avisos por Telegram

Rol: `roles/lab_alerting` · Playbook general: `playbooks/lab-alerting.yml` · Reconciliación router: `playbooks/deploy-llama-monitoring.yml`

Este documento describe el estado deseado versionado. El host tuvo drift en la configuración de Prometheus; `playbooks/deploy-llama-monitoring.yml` reconcilia solo los scrapes, las reglas y el sincronizador del router, sin reescribir la configuración de Grafana ni cargar secretos del vault.

## Qué instala

| Pieza | Dónde | Notas |
|---|---|---|
| Avisos Telegram | `/etc/prometheus/alertmanager.yml`, `alertmanager_templates/telegram.tmpl`, `/etc/prometheus/telegram_bot_token` (640 root:prometheus) | Bot de la secretaria; mensajes en español; latido diario silencioso |
| Reglas | `promreg/lab-ups-alerts.yml`, `lab-extra-alerts.yml`, `lab-recording-rules.yml` | SAI, salud, contenedores/certificados y métricas agregadas |
| SAI | NUT standalone, driver `usbhid-ups` (CyberPower VP700ELCD) | Apagado ordenado con batería ≤10 % o ≤300 s de autonomía |
| Exportadores | `/usr/local/bin/{nut,docker,cert}-textfile-exporter` + timers | Escriben en el textfile collector de node_exporter |
| Dashboard | `grafana/dashboards/ups.json` (uid `lab-ups`) | |
| Clave llama.cpp | `sync-llama-prom-key` + `.path` + `.timer` | Copia la clave del unit o de `--api-key-file` al `credentials_file`; reintenta al arranque, al cambiar el unit y cada cinco minutos |

## Variables

| Variable | Origen | Descripción |
|---|---|---|
| `LAB_TELEGRAM_BOT_TOKEN` | entorno (`.env`) | Mismo token que usa `lab-secretary` |
| `LAB_WATCHDOG_TELEGRAM_TOKEN` | entorno (`.env`) | Token del bot Watchdog; se instala para Alertmanager en `/etc/prometheus/watchdog_telegram_bot_token` con modo `0640` y `no_log` |
| `LAB_TELEGRAM_ALLOWED_USERS` | entorno | Se usa el primer id como `chat_id` |
| `vault_nut_upsmon_password` | `group_vars/vault.yml` | Contraseña del usuario `upsmon` de NUT (generar con `openssl rand -hex 16`) |
| `lab_alerting_enable_*` | defaults | Permiten apagar cada pieza |

Ningún secreto está en el repositorio.

Alertmanager envía por defecto las alertas técnicas al bot Watchdog. Donna solo recibe las alertas que incluyan explícitamente la etiqueta `notify_donna="true"`; en ese caso recibe un aviso resumido y Watchdog mantiene el detalle técnico.

Para configurar el token, añade esta línea al `.env` de la raíz del checkout local (ese archivo está ignorado por Git) y sustituye el marcador por el token del bot Watchdog:

```dotenv
LAB_WATCHDOG_TELEGRAM_TOKEN=replace-with-watchdog-bot-token
```

## Aplicar

```bash
set -a && source .env && set +a
ansible-playbook playbooks/lab-alerting.yml --check --diff
ansible-playbook playbooks/lab-alerting.yml
# por partes: --tags alertmanager | rules | ups | exporters | llama-key | grafana

# Reconciliar el router llama.cpp en una única ejecución, sin cargar .env/vault:
LAB_HOSTNAME=192.168.0.85 LAB_USER=stratecode ansible-playbook -i inventory.yml playbooks/deploy-llama-monitoring.yml --check --diff
LAB_HOSTNAME=192.168.0.85 LAB_USER=stratecode ansible-playbook -i inventory.yml playbooks/deploy-llama-monitoring.yml
```

El playbook específico del router usa `--check --diff` antes del despliegue. Las reglas pasan `promtool check rules`; el handler valida la configuración y todas las reglas antes de reiniciar Prometheus.

Las tareas de copia guardan un backup remoto cuando sustituyen un fichero. Para revertir, usa la ruta `backup_file` que devuelve Ansible como `src` de una tarea `copy` con `remote_src: true`, restaura el fichero original y reinicia los servicios afectados mediante Ansible; no edites el host a mano.

## Integración con `observability` y el router llama.cpp

La configuración activa de Alertmanager pertenece a `lab_alerting` y se instala en `/etc/prometheus/alertmanager.yml`, que es la ruta por defecto del servicio. `observability` conserva el listener y el job de scrape, pero ya no escribe una segunda configuración en `/etc/alertmanager/alertmanager.yml`.

El router del puerto 8080 tiene dos scrapes separados: `llama-cpp-main` con `model=main` y `llama-cpp-qwen-eval` con `model=qwen-eval`. Ambos usan el mismo fichero de credenciales y exportan la etiqueta `alias`; el rol reemplaza el fichero completo de Prometheus, quitando jobs obsoletos que apunten al router de evaluación del puerto 8185. `LlamaCppDown` comprueba solo `llama-cpp-main`; el alias opcional `qwen-eval` no dispara una alerta crítica si está descargado.

Los textfiles de node_exporter se leen desde `/var/lib/prometheus/node-exporter` y `honor_labels` conserva los `instance` que escriben los exportadores locales. La clave se sincroniza a `/var/snap/prometheus/current/llama-api-key` con permisos `0640` y propiedad `root:root`; el script no imprime el valor. Admite `--api-key` y `--api-key-file` para usos controlados, y por defecto extrae `--api-key` del unit de llama.cpp.

Las métricas `lab_llama_prom_key_in_sync` y `lab_llama_prom_key_sync_timestamp_seconds` informan del estado y frescura del sincronizador. `LlamaPromKeyOutOfSync` alerta tras diez minutos de fallo; `LlamaPromKeySyncStale` avisa si el timer deja de ejecutarse.

El job principal conserva `instance="llama-cpp-main"`, por lo que las reglas de memoria y cola siguen filtrando el alias principal:
  ```yaml
  authorization:
    credentials_file: /var/snap/prometheus/current/llama-api-key
  ```
La plantilla también incluye los jobs `llm-telemetry` y `lab-secretary` cuando sus servicios están habilitados. Así, el redeploy mantiene el resto de scrapes y no deja una clave literal obsoleta.

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

## Verificación histórica en el host (2026-10-15)

- Alerta de prueba por la API de Alertmanager → Telegram: recibida.
- Contenedor desechable parado → `ContainerNotRunning` → Telegram: recibido.
- Corte real de red del SAI → `UPSOnBattery` en ~75 s desde el corte (el SAI y NUT tardan ~10-15 s en declarar `OB`; luego 30 s de `for` y 10 s de agrupación) → Telegram: recibido.

## Pendiente de probar
- `UPSLowBattery` y el apagado ordenado por batería baja no se han probado (requeriría agotar la batería).
- Que `/metrics?model=qwen-eval` siga respondiendo 200 mientras el alias está descargado. La regla crítica de caída depende solo del job `main`, así que un scrape fallido del alias opcional no genera `LlamaCppDown`.
