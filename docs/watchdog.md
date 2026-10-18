# Vigilante del lab con Raspberry Pi

Rol: `roles/lab_watchdog` · Playbook: `playbooks/lab-watchdog.yml` · Pruebas: `tests/lab_watchdog/`

Resultados de las pruebas, hallazgos y pendientes: `docs/watchdog-resultados.md`.

Una Raspberry Pi por Wi-Fi vigila el estado del lab desde fuera y, si el lab se apaga (por ejemplo por sobrecalentamiento), lo enciende con **Wake-on-LAN**, comprueba que arranca y envía el aviso directamente a Telegram. El relay del lab es un canal de respaldo opcional; chrony en el lab no es necesario porque la Pi usa el router como NTP principal. Ver `docs/watchdog-lado-lab.md`. Todo corre **en la Raspberry, no en el lab**: si el lab cae, el vigilante sigue vivo.

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
- **Sin Internet:** la Pi no instala paquetes (usa `python3` y `ping` que ya trae Raspberry Pi OS Lite). **La hora** la toma de la LAN con `systemd-timesyncd` (`lab_watchdog_ntp_servers`): primero **el router**, que contesta NTP en la propia LAN, y el lab de segundo. El lab no puede ser la fuente principal: la Pi tiene que poder corregir su reloj justo cuando el lab está caído. Se limita el intervalo de consulta (`lab_watchdog_ntp_poll_max_seconds`, 64 s) porque `timesyncd` lo aumenta hasta 8 min tras fallos y tardaría en recuperarse. La Pi no tiene reloj propio y, tras estar apagada, arranca con la hora que tenía al apagarse: sin esto los avisos salen con hora equivocada y falla el TLS.
- En el router: **reserva DHCP** para el lab (MAC `c8:ff:bf:08:5e:db` → `192.168.0.85`) y para la Pi.
- Enchufe inteligente: **no** hace falta por ahora (el rol lo soporta si algún día se quiere).

**BIOS del lab (Fran confirma que ya está hecho; no se ha podido verificar desde el sistema)**
- Activar **Wake on LAN / Power On by PCI-E**.
- **Desactivar ErP/EuP** si existe: con ErP activo, el WoL desde apagado no funciona.
- **Restore on AC Power Loss = Power On** (necesario para el enchufe y para los cortes de luz).

**Avisos (dos canales, en este orden):** 1) **Telegram directo** desde la Pi con un **bot propio** (`lab_watchdog_telegram_enabled`; nunca el de Donna): llega al instante aunque el lab esté caído, que es cuando más importa; 2) el **relay del lab** (Alertmanager, `docs/watchdog-lado-lab.md`) como respaldo si Telegram falla. Si ninguno está disponible, los avisos esperan en una cola local y **caducan a las 6 h** (`lab_watchdog_outbox_max_age_seconds`) para que no salgan de golpe como noticias viejas. Hace falta la hora correcta (TLS): ver `lab_watchdog_ntp_servers`. Para activar Telegram directo: token del bot en `LAB_WATCHDOG_TELEGRAM_TOKEN` (en el `.env`, nunca en git) y `LAB_TELEGRAM_ALLOWED_USERS` como chat de destino.

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
Los valores de la tabla son los **por defecto**; el despliegue actual los ajusta para encender el lab cuanto antes (columna "Despliegue actual").

| Situación | Por defecto | Despliegue actual |
|---|---|---|
| La puerta de enlace no responde | Estado `NETWORK_DOWN`: **no juzga al lab, no enciende nada**. Avisa tras 2 ciclos y al volver | igual |
| Confirmar la caída (`down_after_failures`, sondeo `poll_seconds`) | 3 ciclos de 30 s (~90 s): `DOWN`, con la **última temperatura y estado del SAI** | 2 ciclos de 15 s (~30 s) |
| Espera de gracia (`grace_seconds`; `cooldown_seconds` si venía a ≥ 80 °C; el doble si reincide) | 5 min (+20 min si venía caliente) | **0**: actúa en ese mismo ciclo |
| **Wake-on-LAN nº 1** → ventana de arranque (`boot_window_seconds`) | 4 min | 1 min |
| Sigue caído: **Wake-on-LAN nº 2** | tras la ventana | tras la ventana; con `give_up_at_second_wake` salta aquí el `GAVE_UP` |
| Sigue caído y hay enchufe | **Ciclo de corriente** → espera | (sin enchufe) |
| `GAVE_UP` (rendido) | Aviso crítico, 3 pitidos largos cada 10 min hasta pulsar el botón, recordatorio cada hora; **no vuelve a actuar** | Lo mismo, pero **sigue mandando un WoL cada `retry_seconds` (1 min)** hasta que el lab responda |
| Límites | 2 secuencias/hora y 4/día; si se superan, no inicia otra | Los **reintentos** tras rendirse **ignoran** estos límites |
| Mantenimiento (`maintenance N`) | Silencia avisos y **pausa las acciones**; al terminar, si sigue caído, avisa | igual |
| Lab caído o inalcanzable | Los avisos quedan en una cola local (máx. 50) y salen al volver el lab, con la hora real del suceso | igual |

Los reintentos mandan un solo mensaje cuando empiezan ("empiezo a reintentar") y después solo el recordatorio horario, para no inundar Telegram con un mensaje por minuto; cada reintento queda en el registro del sistema. Un paquete mágico enviado a un lab que ya está encendido no hace nada, así que insistir no tiene coste.

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

Esquema, lista de conexiones y lista de materiales para una **placa a medida** que sustituya a la de prototipos: `docs/hardware/panel-pcb.md`.

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
| Arranque del servicio | ambos LED 1,5 s | | 1 pitido (solo si no hay pitido de arranque, ver "Panel de la Pi") |

- **Horas de silencio:** de 23:00 a 08:00 no suenan los pitidos de WoL ni de "recuperado". `GAVE_UP` suena siempre. Se cambian con `lab_watchdog_indicator_quiet_start/_end` (`""` en ambos las desactiva).
- **Botón:** silencia el pitido en curso y, en `GAVE_UP`, las repeticiones. El LED rojo sigue parpadeando; si el lab vuelve a caer y se rinde otra vez, suena de nuevo. El botón solo se consulta mientras algo suena.
- **Salidas seguras:** al parar el servicio (`systemctl stop`) y, si lo matan, con `ExecStopPost`, el zumbador y los LED quedan apagados.
- **Requisitos en la Pi:** `pinctrl` (viene con Raspberry Pi OS) y el grupo `gpio`. El rol añade al usuario del servicio a ese grupo y abre en el unit solo `/dev/gpiomem*` y `/dev/gpiochip0`; con el indicador desactivado el unit queda como antes.
- **Prueba de hardware (TH1/TH2):** con el servicio parado, `sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json test-indicator [--seconds 5]` recorre cada fila de la tabla y termina esperando el botón.

## Panel de la Pi (botón de encendido, LED de estado y pitido de arranque)
Tres piezas independientes, todas **desactivadas por defecto**. Las dos primeras son overlays del kernel que el rol declara en un bloque gestionado al final de `/boot/firmware/config.txt` (con copia de seguridad) y necesitan **un reinicio de la Pi**; la tercera es una unidad de systemd.

| Pieza | Variable | Montaje | Qué hace |
|---|---|---|---|
| Botón de encendido | `lab_watchdog_power_button_enabled` | Pulsador entre **GPIO3 (pin 5)** y GND (pin 6) | Con la Pi apagada (`halt`) la enciende. Con la Pi en marcha pide un apagado ordenado (overlay `gpio-shutdown`). Solo GPIO3 puede despertar una Pi apagada |
| LED de estado | `lab_watchdog_status_led_enabled`, `_pin` (24), `_trigger` (`default-on`) | GPIO24 (pin 18) → resistencia → LED → GND | Lo gobierna el kernel desde el arranque (overlay `gpio-led`): **encendido fijo mientras Linux funciona** y apagado cuando la Pi está apagada. `heartbeat` lo cambia por un doble destello que se para si el kernel se cuelga |
| LED azul "Pi lista" (alternativa al LED de estado del kernel) | `lab_watchdog_indicator_pin_ready` (p. ej. 24) | GPIO24 (pin 18) → resistencia → LED → GND | Lo gobierna **el propio vigilante**, no el kernel: se enciende cuando el servicio está ciclando de verdad y **se apaga si se queda parado** (más de 90 s sin informar) o al parar el servicio. Si se usa, desactiva `lab_watchdog_status_led_enabled` (es el mismo pin; el rol lo comprueba) |
| Aviso de apagado | `lab_watchdog_shutdown_beep_enabled`, `_count` (5), `_seconds` (0,25) | El zumbador del indicador | 5 pitidos de 0,25 s **antes de apagar o reiniciar la Pi**, sea cual sea la causa (botón, SSH, Ansible). Es una unidad propia (`lab-watchdog-shutdown-beep.service`) que systemd detiene en el apagado; reiniciar solo el vigilante en un despliegue no la dispara. Ignora las horas de silencio: responde a una acción deliberada. Se detiene antes que el vigilante, así que después de los pitidos se apagan los LED |
| LED testigo de corriente | (sin software) | Pin 1 (3,3 V) → resistencia → LED → GND | Encendido siempre que la placa tenga corriente, **también con la Pi apagada con el botón**. Distingue "apagada pero alimentada" de "sin corriente" |
| Pitido de arranque | `lab_watchdog_boot_beep_enabled`, `_seconds` (0,15) | El zumbador del indicador (GPIO17) | Un pitido corto muy pronto en el arranque, como el del POST de un PC. El arranque completo tarda más de un minuto y el vigilante entra casi al final, así que va en su propia unidad (`lab-watchdog-boot-beep.service`) |

- **Resistencia según el color del LED.** Un LED azul o blanco necesita unos 3 V, y con 3,3 V y 330 Ω pasa casi nada de corriente (~1 mA): se ve muy tenue. Para LED azules o blancos usa **100 Ω** (unos 3 mA, seguro para el pin); para rojo, verde o amarillo valen los 330 Ω. En el pin 1 y el pin 2, que están juntos, **el 1 es 3,3 V y el 2 es 5 V**: no los confundas.
- **Registro del sistema persistente** (`lab_watchdog_persistent_journal`, activado, con tope `lab_watchdog_journal_max_use: 50M`). Por defecto el registro de systemd se pierde al reiniciar, y sin él no se puede saber después por qué se reinició la Pi ni si un apagado fue limpio. Se consulta con `journalctl -b -1` (arranque anterior).
- **Apagar con el botón apaga también el vigilante.** Mientras la Pi esté apagada nadie vigila el lab. Para volver a la normalidad hay que pulsar el botón otra vez; el vigilante arranca solo con la Pi.
- **El botón no despierta tras un corte de luz** (sin corriente no hay nada que despertar): en ese caso la Pi arranca sola al volver la corriente.
- **El pitido de arranque ignora las horas de silencio**: a esas alturas del arranque el reloj no es fiable (la Pi no tiene reloj propio). Cuando está activado, el pitido de inicio del servicio se desactiva solo (`indicator.startup_beep: false`), para no oír dos seguidos; el test de LED del servicio se mantiene.
- Tras aplicar el rol con alguna de las dos primeras piezas, la Pi pide **un reinicio** (`sudo reboot`). Si se desactivan las dos, el bloque se elimina del fichero.

## Órdenes por Telegram
Desde el chat autorizado puedes hablar con el bot de la Pi (`lab_watchdog_telegram_commands_enabled`, **desactivado por defecto**; necesita Telegram directo activo).

| Orden | Qué hace |
|---|---|
| `/estado` | Situación del lab (responde, temperatura, SAI, o caído desde cuándo y en qué fase), modo del vigilante, mantenimiento activo y avisos pendientes |
| `/encender` | Manda un Wake-on-LAN ahora. Si el lab responde no hace nada; `/encender forzar` lo envía igualmente. Máximo uno cada 30 s. En modo simulación no envía nada |
| `/mantenimiento N` | Pausa avisos y acciones N minutos (1-720), como `maintenance N` en la Pi. `/reanudar` vuelve antes |
| `/silenciar` | Calla el zumbador y la alarma de "rendido", como el botón |
| `/ayuda` | La lista |

**Seguridad.** Funciona por *long polling*: la Pi pregunta a Telegram, **no abre ningún puerto**.
- Solo responde a **un chat privado** cuyo identificador es el configurado; cualquier otro recibe silencio (ni siquiera sabe que el bot escucha). Un grupo con el mismo identificador tampoco vale.
- Ignora órdenes de más de 5 min, y al arrancar **descarta lo que estuviera en cola**, de modo que un reinicio nunca repite una orden vieja. Cada orden se confirma a Telegram antes de ejecutarse, así que no puede ejecutarse dos veces.
- **No hay ninguna orden para apagar o tocar el lab**: la Pi no tiene credenciales suyas. Lo peor que puede hacer quien controle ese chat es mandar un Wake-on-LAN (inofensivo), pausar el vigilante o callar el zumbador; por eso la cuenta de Telegram debe tener verificación en dos pasos.
- Se necesita Internet: sin él la orden no llega, y la Pi lo reintenta sola con esperas crecientes.

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
| TH4 | `sudo systemctl start lab-watchdog-boot-beep`; después un arranque real | Un pitido corto al iniciar la unidad y otro unos segundos después de dar corriente a la Pi |
| TH5 | Tras el reinicio con el overlay `gpio-led` | El LED de estado se enciende en cuanto arranca el kernel y queda fijo mientras la Pi funciona; el LED testigo de 3,3 V está encendido siempre que hay corriente |
| TH7 | Con el LED "Pi lista": reiniciar solo el servicio (`sudo systemctl restart lab-watchdog`) y, aparte, pararlo (`stop`) | Reiniciar: el LED se apaga un instante y vuelve (sin pitidos). Parar: el LED se apaga y no vuelve hasta arrancarlo |
| TH8 | `sudo systemctl stop lab-watchdog-shutdown-beep`; después `start` para dejarla armada | 5 pitidos de 0,25 s al detenerla |
| TH6 | Pulsar el botón con la Pi en marcha; esperar el apagado; pulsarlo otra vez | Apagado ordenado: el LED de estado se apaga, el testigo de 3,3 V sigue encendido y `journalctl -b -1` muestra el apagado (necesita el registro persistente). Al pulsar otra vez, la Pi arranca y el vigilante vuelve a estar activo |

No se recomienda provocar un sobrecalentamiento real: el caso se cubre con la prueba de repetición del incidente.

## Seguridad
- La Pi **no tiene credenciales del lab**: lee métricas de Prometheus (solo lectura, LAN), envía paquetes mágicos y, si el relay está aplicado, puede **publicar alertas** en un único endpoint de Alertmanager (solo desde su IP). Con Telegram directo guarda además **el token de un bot dedicado** que solo sirve para enviar mensajes a tu chat. Si la Pi se compromete, puede encender el lab, leer métricas y mandar avisos falsos; nada más.
- Sin Telegram directo no hay ningún secreto en la Pi. Con él, el token va en `/etc/lab-watchdog/secrets.env` (0640 `root:labwatch`), nunca en git; una prueba comprueba que los errores de envío no lo filtran.
- El servicio corre con un usuario sin sesión, sin privilegios (solo `CAP_NET_RAW` para `ping`) y con la protección de `systemd` activada.
- La Pi tiene su propio **watchdog de hardware**: si se cuelga, se reinicia sola.

## Limitaciones conocidas
- **Wake-on-LAN no revive un equipo colgado**; para eso hace falta el enchufe inteligente.
- Si el router o el switch caen y no están en el SAI, la Pi no puede ver ni avisar. La prueba T7 verifica que, en ese caso, no actúa.
- **Aviso diferido:** si el lab no arranca, el aviso no sale nunca (la Pi no tiene otra vía). Un `GAVE_UP` queda solo en la cola y en `journalctl`. En local lo cubre el **indicador** (LED y zumbador, con repetición cada 10 min hasta pulsar el botón). Alternativas futuras para avisar fuera de casa: un módem SMS o un canal por Internet por otra red.
- Si el lab sigue en el mismo estado tras 2 Wake-on-LAN (y un ciclo, si hay enchufe), el vigilante **se rinde** y avisa: necesita una persona.
- El nombre `monitor.stratecode.com` no resuelve en la LAN (el router no lo conoce): la Pi se conecta por IP y presenta el nombre para validar el certificado.
- La Pi no tiene reloj propio y toma la hora del lab. Si arranca con el lab caído, la fecha puede ser antigua: la telemetría TLS y la entrega fallan hasta resincronizar, pero la vigilancia por ping/TCP y el Wake-on-LAN **siguen funcionando**.
