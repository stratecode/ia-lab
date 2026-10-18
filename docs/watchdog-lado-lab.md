# Lado del lab para el vigilante de la Raspberry

La Raspberry tiene conectividad Wi-Fi y envía sus avisos directamente a Telegram; el relay del lab es solo un respaldo. La configuración del relay está versionada y desactivada por defecto (`lab_watchdog_relay_enabled: false`); habilitarla cambia el host. Codex debe ejecutar el playbook con `--check --diff`, compartir el diff y esperar la aprobación directa de Fran antes de aplicar.

Playbook: `playbooks/deploy-watchdog-relay.yml`. El relay expone solo el POST de Alertmanager a la IP de la Pi y enruta `LabWatchdogEvento` al bot Telegram existente. Chrony queda separado y desactivado: la Pi usa el router como fuente principal de NTP. Por defecto el rol no toca nada.

Variables necesarias para preparar la revisión:

```yaml
lab_watchdog_relay_enabled: true
lab_watchdog_pi_ip: "192.168.0.200"  # reserva DHCP de la Pi
lab_watchdog_relay_chrony_enabled: false  # la Pi usa el router como NTP principal
lab_watchdog_manage_ufw: false       # activar solo si UFW está gestionado y necesita UDP/123
```

No actives estas variables en `group_vars/all.yml`. Para revisar juntas el relay y el exportador de apagado no limpio, pásalas solo a esa ejecución:

```bash
ansible-playbook -i inventory.yml playbooks/deploy-watchdog-relay.yml \
  -e lab_watchdog_relay_enabled=true \
  -e lab_watchdog_pi_ip=192.168.0.200 \
  -e lab_watchdog_relay_chrony_enabled=false \
  -e lab_alerting_enable_boot_exporter=true \
  --check --diff
```

La misma orden sin `--check --diff` solo se ejecuta después de aprobación explícita de Fran.

Sea `PI_IP` la IP reservada de la Pi (`192.168.0.200`).

## 1. Recibir los avisos de la Pi (nginx, solo la Pi, solo POST)
En el `server` de `monitor.stratecode.com` (443), que ya limita a la LAN, añadir **antes** de `location /`:
```nginx
    location = /alertmanager/api/v2/alerts {
        allow PI_IP;
        allow 127.0.0.1;
        deny all;
        limit_except POST { deny all; }
        client_max_body_size 16k;
        proxy_pass http://127.0.0.1:9093/api/v2/alerts;
    }
```
Solo expone ese único endpoint de Alertmanager (no el panel, ni los silencios, ni la lectura de alertas). `nginx -t` antes de recargar.

## 2. Convertir esos avisos en un mensaje de Telegram (Alertmanager)
La Pi envía una alerta `LabWatchdogEvento` por evento (etiquetas `severity`, `event_id`; texto completo en `annotations.description`, con la hora de detección). Añadir en `alertmanager.yml`:
- una **ruta** con `matchers: [alertname="LabWatchdogEvento"]`, `group_by: [event_id]`, `group_wait: 0s`, `repeat_interval: 24h`, antes de las rutas genéricas;
- un **receptor** Telegram con el mismo bot y chat actuales, `send_resolved: false` y `message: '{{ (index .Alerts 0).Annotations.description }}'`.
Con `send_resolved: false` no llega un segundo mensaje cuando caduca la alerta. Validar con `amtool check-config` y una alerta de prueba enviada desde el propio lab.

## 3. Dar la hora a la Pi (chrony, opcional)
La Pi usa el router como fuente principal de hora y el lab como alternativa. No habilites el servidor chrony del lab para el relay normal. Solo si se pide expresamente, pasa `lab_watchdog_relay_chrony_enabled=true`; la tarea UFW sigue requiriendo además `lab_watchdog_manage_ufw=true`.

En el lab (`/etc/chrony/conf.d/lab-watchdog.conf`):
```
allow PI_IP/32
```
y abrir UDP 123 solo desde `PI_IP` si hay cortafuegos (`ufw allow from PI_IP to any port 123 proto udp`). Comprobar desde la Pi con `chronyc`/`timedatectl timesync-status`.

## 4. Métrica del último apagado no limpio (opt-in)

`lab_alerting_enable_boot_exporter` está desactivado por defecto. Al habilitarlo, una unidad persistente deja la marca de apagado limpio y un servicio de arranque la consume una sola vez por boot. El exportador mantiene `lab_boot_previous_clean`, el contador persistente `lab_boot_unclean_total` y `lab_boot_unclean_last_timestamp_seconds` en el directorio textfile de node_exporter. La primera instalación inicializa una marca limpia; un apagado no limpio genera `LabApagadoSucio` con severidad `warning` durante una hora. La alerta usa la ruta Telegram existente de Alertmanager.

Prueba reproducible sin tocar el host: `bash tests/lab_alerting/test_boot_state_exporter.sh`. Comprueba primera instalación limpia, marca presente, marca ausente con contador y timestamp, e idempotencia dentro del mismo arranque.

## Flujo completo
1. El lab se apaga (p. ej. por calor). La Pi lo detecta (ping y puertos 22/443 sin respuesta ~90 s), espera la gracia y manda Wake-on-LAN.
2. La Pi no puede avisar todavía: el mensaje queda en su cola local.
3. El lab arranca; la Pi lo ve vivo y encola "lab recuperado".
4. La Pi envía a Alertmanager, por la LAN, los avisos retenidos: "no respondía desde las HH:MM; última temperatura …", "WoL enviado", "recuperado tras N min". Alertmanager los manda a Telegram.

## Prueba
Con la Pi en simulación: `sudo poweroff` en el lab, esperar ~6 min y encenderlo a mano; deben llegar a Telegram los mensajes de caída y de recuperación con la hora real de la caída. Un fallo de este tramo no pone en riesgo el WoL: este solo depende de la LAN.

## Verificado y no verificado
Verificado tras el despliegue del 19-oct-2026: `nginx -t` pasa; Nginx, Alertmanager, Prometheus, el timer de `lab-boot-state-exporter` y el servicio `lab-boot-clean-marker` están activos. La métrica inicial indica `lab_boot_previous_clean=1` y `lab_boot_unclean_total=0`, y Prometheus tiene cargada `LabApagadoSucio`. Un GET al endpoint desde loopback devuelve 403 por la restricción de método (no redirige a Grafana). Pendiente: confirmación desde la Pi de que el endpoint ya no redirige; no se ha enviado un POST de prueba porque generaría una notificación real.

Chrony sigue sin habilitarse y UFW no se modificó. La prueba de POST desde la IP de la Pi y el aviso de Telegram quedan sin verificar hasta que Fran autorice enviar una notificación de prueba.
