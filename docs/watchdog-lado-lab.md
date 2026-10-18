# Lado del lab para el vigilante de la Raspberry

La Raspberry **no tiene Internet**: solo ve el lab. Por eso el lab hace tres cosas por ella. Son cambios **en el host**, así que van por playbook de Codex, con una línea previa en el buzón y la aprobación de Fran. Este documento es la propuesta; nada de esto está aplicado.

Sea `PI_IP` la IP reservada de la Pi (192.168.0.PP).

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

## 3. Dar la hora a la Pi (chrony)
La Pi no tiene reloj ni Internet; sin fecha correcta, TLS falla y los avisos salen con hora errónea. En el lab (`/etc/chrony/conf.d/lab-watchdog.conf`):
```
allow PI_IP/32
```
y abrir UDP 123 solo desde `PI_IP` si hay cortafuegos (`ufw allow from PI_IP to any port 123 proto udp`). Comprobar desde la Pi con `chronyc`/`timedatectl timesync-status`.

## Flujo completo
1. El lab se apaga (p. ej. por calor). La Pi lo detecta (ping y puertos 22/443 sin respuesta ~90 s), espera la gracia y manda Wake-on-LAN.
2. La Pi no puede avisar todavía: el mensaje queda en su cola local.
3. El lab arranca; la Pi lo ve vivo y encola "lab recuperado".
4. La Pi envía a Alertmanager, por la LAN, los avisos retenidos: "no respondía desde las HH:MM; última temperatura …", "WoL enviado", "recuperado tras N min". Alertmanager los manda a Telegram.

## Prueba
Con la Pi en simulación: `sudo poweroff` en el lab, esperar ~6 min y encenderlo a mano; deben llegar a Telegram los mensajes de caída y de recuperación con la hora real de la caída. Un fallo de este tramo no pone en riesgo el WoL: este solo depende de la LAN.

## Verificado y no verificado
Verificado (solo lectura): Alertmanager escucha en `127.0.0.1:9093` y **no** está expuesto por nginx; chrony está activo y **no** sirve a la LAN. Sin verificar: que el cortafuegos permita UDP 123, y todo lo anterior hasta que se aplique.
