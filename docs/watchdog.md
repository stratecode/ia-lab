# Vigilante del lab con Raspberry Pi

Rol: `roles/lab_watchdog` · Playbook: `playbooks/lab-watchdog.yml` · Pruebas: `tests/lab_watchdog/`

Una Raspberry Pi **sin Internet** (solo ve la LAN del lab) vigila el estado del lab desde fuera y, si el lab se apaga (por ejemplo por sobrecalentamiento), lo enciende con **Wake-on-LAN**, comprueba que arranca y, **cuando el lab vuelve, le entrega el aviso** (cuándo detectó la caída y que lo encendió) para que lo envíe él a Telegram. Requiere tres cambios en el lab: `docs/watchdog-lado-lab.md`. Todo corre **en la Raspberry, no en el lab**: si el lab cae, el vigilante sigue vivo.

## Qué hace y qué no hace
| Hace | No hace |
|---|---|
| Comprueba cada 30 s: ping, SSH (22), HTTPS (443) y la telemetría de Prometheus (temperatura, SAI, uptime) | **No entra por SSH** al lab ni tiene credenciales suyas |
| Registra caídas, encendidos y recuperaciones y los **entrega al lab** (Alertmanager, por la LAN) en cuanto responde; el lab los manda a Telegram | No reinicia servicios ni toca nada del lab |
| Envía Wake-on-LAN con un protocolo de reintentos y límites | **Wake-on-LAN solo enciende un equipo apagado**, no uno colgado |
| Opcional: ciclo de corriente con un enchufe inteligente (API local) | No usa servicios en la nube para encender el lab |
| Distingue "el lab cayó" de "mi red falló" y no actúa si no hay red | No decide solo si el problema es térmico: espera más si venía caliente |

> **Qué pasó el 16-oct:** el lab se paró sin apagado limpio con 81 °C. El kernel no tiene zonas térmicas ni puntos críticos configurados (`/sys/class/thermal` vacío, `thermald` inactivo), así que ante un sobrecalentamiento corta el hardware. Por eso el vigilante asume un corte brusco y espera más antes de encender si el último dato era alto.

## Arquitectura
```
 Raspberry Pi (lab-watchdog)                       Lab (192.168.0.85)
 ┌──────────────────────────────┐   ping / TCP 22,443   ┌──────────────────────────┐
 │ bucle cada 30 s              │ ───────────────────▶ │ nginx (LAN permitida)     │
 │  - sondas  - máquina estados │   HTTPS (SNI monitor) │  └ /prometheus/api (solo    │
 │  - cola de avisos → lab      │ ───────────────────▶ │     lectura)               │
 │  - Wake-on-LAN (UDP 9 y 7)   │   paquete mágico      │ NIC enp3s0 (igc) WoL "g"  │
 │  - enchufe (opcional)        │ ───────────────────▶ └──────────────────────────┘
 └──────────────────────────────┘
```

## Requisitos
**Hardware y red**
- Raspberry Pi con **Ethernet** en el **mismo segmento de red** que el lab (el paquete mágico es un broadcast de capa 2: no cruza VLAN ni red de invitados). Modelo exacto por confirmar.
- Tarjeta microSD de calidad (≥ 8 GB) y fuente de 5 V adecuada. Conectada al **mismo SAI** que el lab (decidido). Si el router o el switch no están en el SAI, un corte los apaga y la Pi no ve nada.
- **Sin Internet:** la Pi no instala paquetes (usa `python3` y `ping` que ya trae Raspberry Pi OS Lite) y toma la **hora del lab** (chrony en el lab).
- En el router: **reserva DHCP** para el lab (MAC `c8:ff:bf:08:5e:db` → `192.168.0.85`) y para la Pi.
- Enchufe inteligente: **no** hace falta por ahora (el rol lo soporta si algún día se quiere).

**BIOS del lab (Fran confirma que ya está hecho; no se ha podido verificar desde el sistema)**
- Activar **Wake on LAN / Power On by PCI-E**.
- **Desactivar ErP/EuP** si existe: con ErP activo, el WoL desde apagado no funciona.
- **Restore on AC Power Loss = Power On** (necesario para el enchufe y para los cortes de luz).

**Telegram:** no hace falta bot ni token en la Pi. Los avisos salen por el Alertmanager del lab con el bot actual.

## Puesta en marcha (por fases)
0. **Lado del lab** (Codex, con aprobación de Fran): `docs/watchdog-lado-lab.md` (nginx, ruta de Alertmanager, chrony). Sin esto la Pi detecta y enciende, pero no puede entregar los avisos.
1. **Grabar la Pi** con Raspberry Pi Imager: Raspberry Pi OS Lite, nombre `lab-watchdog`, SSH **solo con clave**, sin Wi-Fi, zona horaria `Europe/Madrid`.
2. **Preparar el inventario** (separado): `cp inventory-watchdog.example.yml inventory-watchdog.yml` y rellenarlo. **No añadir la Pi a `inventory.yml`**: los playbooks con `hosts: all` la aprovisionarían como si fuera el lab.
3. **Aplicar el rol en simulación** (`lab_watchdog_dry_run: true`, que es el valor por defecto):
   ```bash
   set -a && source .env && set +a
   ansible-playbook -i inventory-watchdog.yml playbooks/lab-watchdog.yml --check --diff
   ansible-playbook -i inventory-watchdog.yml playbooks/lab-watchdog.yml
   ```
4. **Observar 24-48 h** sin actuar: debe llegar el aviso de arranque y el latido diario, y no deben salir falsas alarmas.
5. **Pasar a modo real** (`lab_watchdog_dry_run: false`) solo tras las pruebas T0-T4.
6. **Enchufe inteligente** (opcional): configurar `lab_watchdog_plug_type` y `lab_watchdog_plug_host` tras la prueba T6.

## Comportamiento
| Situación | Qué hace |
|---|---|
| La puerta de enlace no responde | Estado `NETWORK_DOWN`: **no juzga al lab, no enciende nada**. Avisa tras 2 ciclos y al volver |
| El lab no responde 3 ciclos (~90 s) | `DOWN`: aviso con la **última temperatura y estado del SAI** conocidos |
| Espera de gracia (5 min; 25 min si venía a ≥ 80 °C; el doble si reincide) | No actúa: cubre reinicios y despliegues previstos |
| Fin de la espera | **Wake-on-LAN nº 1** → espera 4 min |
| Sigue caído | **Wake-on-LAN nº 2** → espera 4 min |
| Sigue caído y hay enchufe | **Ciclo de corriente** → espera 4 min |
| Sigue caído | `GAVE_UP`: aviso crítico y recordatorio cada hora; **no vuelve a actuar** |
| Límites | 2 secuencias/hora y 4/día; si se superan, solo avisa |
| Mantenimiento (`maintenance N`) | Silencia avisos y **pausa las acciones**; al terminar, si sigue caído, avisa |
| Lab caído o inalcanzable | Los avisos quedan en una cola local (máx. 50) y salen al volver el lab, con la hora real del suceso ("enviado con retraso; ocurrió a las HH:MM") |

## Mensajes de Telegram (resumen)
`Vigilante arrancado` · `El lab no responde desde las HH:MM; última temperatura …; SAI …` · `Enviado paquete Wake-on-LAN nº N` · `Lab recuperado tras … después de N min` · `Límite de intentos alcanzado` · `El lab sigue caído … necesita intervención` · `Sin red local` / `Red local recuperada` · `Latido diario` (09:00).
Los mensajes los redacta la Pi y los entrega el lab, así que **llegan tras el arranque del lab**, no en el momento de la caída.

## Manejo (en la Pi, como el usuario del servicio)
```bash
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json status        # estado guardado
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json once          # una lectura ahora
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json maintenance 30   # pausar 30 min (0 = reanudar)
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json reset         # borrar historial de intentos
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json test-notify   # evento de prueba hacia el lab
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json test-wake --really   # UN paquete real
sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json test-plug     # ¿responde el enchufe? (no lo conmuta)
journalctl -u lab-watchdog -f                                                 # registro
```
**Antes de reiniciar o parar el lab a propósito**, ejecuta `maintenance 30`; si no, el vigilante lo tratará como una caída.

## Indicador local (LED, zumbador y botón)
Avisa **en la propia Pi**, sin depender del lab, del router ni de Telegram: es la única vía cuando el lab no arranca y el aviso diferido no puede salir. Está **desactivado por defecto** (`lab_watchdog_indicator_enabled: false`). Lo maneja un hilo propio del vigilante y **nunca lo tumba**: sin GPIO, o si falla, se desactiva y reintenta cada 5 min.

Montaje (numeración BCM, ajustable con `lab_watchdog_indicator_pin_*`): zumbador activo de 5 V por un transistor NPN en GPIO17 (pin 11), LED verde en GPIO27 (pin 13), LED rojo en GPIO22 (pin 15), pulsador a GND en GPIO23 (pin 16). Esquema y lista de piezas: `docs/watchdog-contexto-sesion-pi.md`, sección 4.

| Estado del vigilante | Verde | Rojo | Zumbador |
|---|---|---|---|
| Lab UP | fijo | apagado | silencio |
| SUSPECT / DEGRADED | parpadeo lento | apagado | silencio |
| DOWN (espera de gracia) | apagado | parpadeo lento | silencio |
| WoL enviado (esperando arranque) | apagado | fijo | 1 pitido corto por cada WoL o ciclo de enchufe |
| Recuperado | fijo | apagado | 2 pitidos cortos |
| GAVE_UP (rendido) | apagado | parpadeo rápido | 3 pitidos largos, **repetidos cada 10 min hasta pulsar el botón** |
| NETWORK_DOWN (sin red local) | alterna con rojo | alterna | silencio |
| Mantenimiento | parpadeo muy lento, ambos a la vez | | silencio |
| Arranque del servicio | ambos LED 1,5 s | | 1 pitido |

- **Horas de silencio:** de 23:00 a 08:00 no suenan los pitidos de WoL ni de "recuperado". `GAVE_UP` suena siempre. Se cambian con `lab_watchdog_indicator_quiet_start/_end` (`""` en ambos las desactiva).
- **Botón:** silencia el pitido en curso y, en `GAVE_UP`, las repeticiones. El LED rojo sigue parpadeando; si el lab vuelve a caer y se rinde otra vez, suena de nuevo. El botón solo se consulta mientras algo suena.
- **Salidas seguras:** al parar el servicio (`systemctl stop`) y, si lo matan, con `ExecStopPost`, el zumbador y los LED quedan apagados.
- **Requisitos en la Pi:** `pinctrl` (viene con Raspberry Pi OS) y el grupo `gpio`. El rol añade al usuario del servicio a ese grupo y abre en el unit solo `/dev/gpiomem*` y `/dev/gpiochip0`; con el indicador desactivado el unit queda como antes.
- **Prueba de hardware (TH1/TH2):** con el servicio parado, `sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json test-indicator [--seconds 5]` recorre cada fila de la tabla y termina esperando el botón.

## Plan de pruebas
**Automáticas (sin hardware):** `python3 -m unittest discover -s tests/lab_watchdog -v` → 56 pruebas con reloj, red, entrega al lab, Telegram, Wake-on-LAN, enchufe y GPIO simulados; ningún socket real. Incluyen la **repetición del incidente del 16-oct** (también con el indicador: qué se ve y se oye en cada fase). Las pruebas detectan averías introducidas a propósito (límite horario, modo simulación, comprobación de red, espera por calor, formato del paquete, silencio nocturno, botón, apagado de salidas, fallo de backend).

**Con hardware** (en este orden; pararse ante el primer fallo):
| # | Prueba | Esperado |
|---|---|---|
| T0 | BIOS y router según los requisitos | Reservas DHCP hechas; WoL activo y ErP desactivado |
| T1 | `sudo poweroff` en el lab y paquete mágico desde otro equipo (`wakeonlan c8:ff:bf:08:5e:db`) | Arranca en < 3 min |
| T2 | (Con enchufe) apagar el lab, cortar y devolver la corriente | Arranca solo (*Restore on AC Power Loss*) |
| T3 | Modo simulación 24-48 h | Latido diario, sin falsas alarmas |
| T4 | Simulación: `maintenance 0`, `sudo poweroff` en el lab | Aviso de caída en ~90 s; a los 5 min "[SIMULACIÓN] habría enviado WoL"; al encenderlo a mano, "recuperado" |
| T5 | Modo real: repetir T4 | WoL real; arranca; "recuperado tras 1 paquete" |
| T6 | `test-plug`; luego, con el lab apagado limpio, `test-plug --cycle --really` | El enchufe responde; el lab arranca tras el ciclo |
| T7 | Reiniciar brevemente el router con el lab encendido | "Sin red local" y "Red local recuperada"; **ningún WoL** |
| T8 | Quitar y devolver la corriente a la Pi | Servicio activo al arrancar; estado conservado |
| T9 | Con la Pi en simulación: `sudo poweroff` en el lab, esperar ~6 min, encenderlo a mano | Al volver el lab llegan a Telegram la caída (con su hora real) y la recuperación |
| TH1 | `test-indicator` con el hardware montado | Cada fila de la tabla del indicador se ve y se oye como se describe |
| TH2 | En `test-indicator`, pulsar el botón durante el paso `GAVE_UP` | El zumbador se silencia y el paso termina con "botón: silenciado" |
| TH3 | Con el servicio activo, quitar el cable de red de la Pi | LED alternando verde/rojo (`NETWORK_DOWN`), **ningún WoL** y sin pitidos |

No se recomienda provocar un sobrecalentamiento real: el caso se cubre con la prueba de repetición del incidente.

## Seguridad
- La Pi **no tiene credenciales del lab ni de Telegram**: lee métricas de Prometheus (solo lectura, LAN), envía paquetes mágicos y puede **publicar alertas** en un único endpoint de Alertmanager (solo desde su IP). Si se compromete, puede encender el lab, leer métricas y mandar avisos falsos; nada más.
- Por defecto no hay ningún secreto en la Pi. Si algún día se usa Telegram directo, el token iría en `/etc/lab-watchdog/secrets.env` (0640 `root:labwatch`), nunca en git; una prueba comprueba que los errores de envío no lo filtran.
- El servicio corre con un usuario sin sesión, sin privilegios (solo `CAP_NET_RAW` para `ping`) y con la protección de `systemd` activada.
- La Pi tiene su propio **watchdog de hardware**: si se cuelga, se reinicia sola.

## Limitaciones conocidas
- **Wake-on-LAN no revive un equipo colgado**; para eso hace falta el enchufe inteligente.
- Si el router o el switch caen y no están en el SAI, la Pi no puede ver ni avisar. La prueba T7 verifica que, en ese caso, no actúa.
- **Aviso diferido:** si el lab no arranca, el aviso no sale nunca (la Pi no tiene otra vía). Un `GAVE_UP` queda solo en la cola y en `journalctl`. En local lo cubre el **indicador** (LED y zumbador, con repetición cada 10 min hasta pulsar el botón). Alternativas futuras para avisar fuera de casa: un módem SMS o un canal por Internet por otra red.
- Si el lab sigue en el mismo estado tras 2 Wake-on-LAN (y un ciclo, si hay enchufe), el vigilante **se rinde** y avisa: necesita una persona.
- El nombre `monitor.stratecode.com` no resuelve en la LAN (el router no lo conoce): la Pi se conecta por IP y presenta el nombre para validar el certificado.
- La Pi no tiene reloj propio y toma la hora del lab. Si arranca con el lab caído, la fecha puede ser antigua: la telemetría TLS y la entrega fallan hasta resincronizar, pero la vigilancia por ping/TCP y el Wake-on-LAN **siguen funcionando**.
