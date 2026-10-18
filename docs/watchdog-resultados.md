# Vigilante del lab: resultados de las pruebas (18-oct-2026)

Informe de la sesión que desplegó y probó el vigilante en la Raspberry Pi. Complementa `docs/watchdog.md` (uso y plan de pruebas) y `docs/watchdog-contexto-sesion-pi.md` (traspaso). No contiene secretos: del token y del chat de Telegram solo se cita que existen.

## Resumen

- La Pi (Raspberry Pi 3 B+, Raspberry Pi OS Lite 32 bits, Trixie, conectada por **Wi-Fi** de forma definitiva) vigila el lab, lo **enciende con Wake-on-LAN en cuanto confirma la caída** y avisa **directamente a Telegram** con un bot propio, sin pasar por el lab.
- Probado con el lab apagado de verdad: la Pi detectó la caída, mandó el WoL y el lab volvió a responder en **~3 min** desde que se apagó.
- Se pueden dar órdenes a la Pi desde Telegram (`/estado`, `/encender`, `/mantenimiento`, `/reanudar`, `/silenciar`, `/ayuda`), solo desde el chat autorizado y sin abrir ningún puerto. Verificado por Fran.
- El desarrollo encontró y corrigió **seis problemas** (apartado "Hallazgos"), entre ellos que la Pi tenía la hora 11 min atrasada y que el registro persistente no se guardaba.
- El lab se cayó por su cuenta a las 20:31 sin dejar rastro en el registro, a 34 °C y con el SAI en línea. Apunta a la **alimentación aguas abajo del SAI**; la causa no está demostrada (apartado "Caída de las 20:31").
- Pendientes: fuente de alimentación nueva y aplicar los cambios preparados del lado del lab (relay de respaldo y métrica de apagado sucio), sujeto a revisión del `--check --diff` y aprobación de Fran.

## Configuración desplegada

Valores del despliegue actual (el inventario `inventory-watchdog.yml` está fuera de git; los valores por defecto del rol son más conservadores y están en `docs/watchdog.md`).

| Ajuste | Valor | Efecto |
|---|---|---|
| Modo | real (`dry_run: false`) | Manda WoL de verdad |
| Sondeo / caída confirmada | 15 s / 2 fallos | Confirma la caída en ~30 s |
| Espera de gracia y por calor | 0 / 0 | Actúa en el mismo ciclo |
| Ventana de arranque | 60 s | WoL nº 2 al minuto |
| Rendición | En el 2.º WoL (sin enchufe) | Alarma local y aviso crítico |
| Reintentos | Un WoL por minuto, sin límite | Hasta que el lab responda |
| Avisos | Telegram directo, relay de respaldo | Relay del lab aplicado el 19-oct; pendiente validar desde la Pi sin enviar una alerta de prueba |
| Órdenes por Telegram | Activas (`/estado`, `/encender`, `/mantenimiento N`, `/reanudar`, `/silenciar`, `/ayuda`) | Un solo chat privado, órdenes de más de 5 min ignoradas, sin comando que toque el lab |
| Caducidad de la cola de avisos | 6 h | Evita ráfagas de noticias viejas |
| Hora | `systemd-timesyncd`: router, luego lab; intervalo máximo 64 s | La Pi no tiene reloj propio |
| Registro del sistema | Persistente, tope 50 MB | Permite diagnosticar reinicios |
| Panel | Botón (GPIO3), pitido de arranque, 5 pitidos de apagado, LED azul "listo" (GPIO24), LED testigo de 3,3 V | Ver `docs/watchdog.md` |

## Pruebas

| Prueba | Resultado | Evidencia |
|---|---|---|
| **T0** BIOS y requisitos | OK | BIOS revisada por Fran (WoL activo); reservas DHCP del lab y de la Pi hechas por Fran |
| **T1** WoL manual desde otro equipo | OK | Desde el Mac, tras corregir el BIOS |
| WoL desde la Pi por Wi-Fi | OK | Arrancó el lab a los 62 s y en la prueba real (T5). No arrancó la primera vez, antes de revisar el BIOS, ni con el primer paquete de la caída de las 20:35 (el lab probablemente estaba sin corriente) |
| **T3** Simulación 24-48 h | **No hecha** | Se pasó a modo real tras la prueba T4 |
| **T4** Simulación con el lab apagado | OK | Caída detectada y recuperación registrada |
| **T5** Modo real con el lab apagado | OK | Caída ~19:25; WoL nº 1 a las 19:27:51; SSH del lab a las 19:28:21 (~3 min en total, con la política anterior de espera) |
| **T7** Reinicio del router | No hecha | |
| **T8** Corte de corriente a la Pi | No hecha (parcial) | La Pi se apagó y encendió varias veces con el botón y por reinicio; no se cortó la corriente |
| **T9** Entrega de avisos atrasados | Sustituida | Ahora los avisos salen directos a Telegram; el relay del lab queda como respaldo |
| **TH1** Señales del indicador | Parcial | Rojo fijo y 1 pitido al enviar WoL, pitido de arranque, 5 pitidos de apagado, LED azul: confirmados por Fran. El recorrido completo de `test-indicator` no se ha ejecutado |
| **TH2** El botón silencia el zumbador | Parcial | Lectura del botón verificada (`hi` en reposo, `lo` al pulsar); el silenciado del `GAVE_UP` solo está probado con GPIO simulado |
| **TH3** Quitar la red de la Pi | No aplica | La Pi va por Wi-Fi; habría que probar con el punto de acceso |
| **TH4** Pitido de arranque | OK | La unidad corre a los ~24 s del arranque; Fran lo oyó |
| **TH5** LED de estado y LED "listo" | OK | GPIO24 en alto mientras el servicio cicla; se apaga al parar el servicio |
| **TH6** Botón: apagar y encender | OK | Verificado por Fran en el rack: apagado ordenado con los 5 pitidos y arranque al volver a pulsar |
| **TH8** 5 pitidos antes de apagar | OK | Probado parando su unidad; el apagado real quedó sin registro por el fallo del journal (ya corregido) |
| Telegram directo | OK | El aviso en cola salió tras pulsar Start en el bot; la cola quedó en 0 |
| Órdenes por Telegram | OK | Fran probó todas desde su Telegram; `/estado` respondió con el estado real. El registro de la Pi muestra cada orden recibida y ningún error |
| Pruebas automáticas | OK | 118 pruebas con reloj, red, GPIO, Telegram y relay simulados; ningún socket real. Incluyen la seguridad de las órdenes (chat ajeno, grupo, órdenes caducadas, reinicio sin repetir órdenes) y la coherencia del diseño de la placa |

## Hallazgos y correcciones

1. **Colisión de variable.** `group_vars/all.yml` definía `lab_watchdog_relay_enabled` para el lado del lab y anulaba el valor de la Pi: la Pi no habría entregado ningún aviso. Renombrada a `lab_watchdog_notify_relay_enabled`.
2. **Directorios de systemd inexistentes** (`timesyncd.conf.d`, `system.conf.d`): la primera ejecución real fallaba sin cambiar nada y el modo `--check` no lo detectaba.
3. **Copia de seguridad de `config.txt`:** `/boot/firmware` es FAT y no admite los caracteres del nombre automático. Ahora se guarda una copia con nombre propio la primera vez.
4. **La hora de la Pi iba 11 min atrasada.** No tiene reloj propio y arrancaba con la hora de cuando se apagó; además `timesyncd` subía su intervalo a 8 min tras los fallos con el lab. El lab no podía ser la fuente principal del vigilante del lab. Ahora la primera fuente es el router (responde NTP en la LAN) y el intervalo máximo es 64 s.
5. **Registro persistente que no se guardaba:** Raspberry Pi OS trae `Storage=volatile` en un fichero que se leía después del nuestro. Corregido con el nombre `90-lab-watchdog.conf`.
6. **Política de espera demasiado prudente** para el objetivo del proyecto (lab apagado el menor tiempo posible). Se rediseñó: sin gracia, WoL inmediato, rendición en el 2.º paquete y reintento cada minuto.

Además se añadió validación estricta de tipos en la configuración (un `"false"` de texto era verdadero para Python) y el error de Telegram muestra el código HTTP sin exponer el token.

## Caída de las 20:31

Analizada en solo lectura desde el lab.

- El registro termina de golpe a las 20:31:18, en mitad del funcionamiento normal (`last -x` lo marca como `crash`): sin apagado ordenado, sin aviso del kernel.
- Temperatura de 34 °C; SAI en línea con 241 V, 100 % de batería y 5 % de carga, sin eventos.
- El WoL nº 1 (~20:35) no arrancó nada. El nº 2 (~20:39) arrancó el lab, que **murió de nuevo a los 7 s**, otra vez sin mensajes; volvió a arrancar a las 20:40:27.
- Misma firma que el incidente del 16-oct (parada en seco con el SAI en línea). **Hipótesis, sin demostrar:** interrupción de la alimentación entre el SAI y el equipo (cable, fuente o contacto). Explicaría que el WoL no funcionara (sin corriente en reposo la tarjeta de red no escucha) y el arranque de 7 s.
- **Consecuencia:** ante un corte total de alimentación al lab el WoL no puede ayudar, y un enchufe inteligente tampoco si el fallo está aguas abajo de él.
- **Decisión de Fran: no se investiga más.** La causa queda sin determinar; la métrica de apagado sucio (ver pendientes) dejaría constancia si se repite.

## Pendientes, por prioridad

1. **Fuente de alimentación nueva** para la Pi (la cambia Fran) y comprobar que `throttled` sigue en `0x0`. En el banco registraba bajo voltaje (`0x50005`); en el rack, 0 avisos en más de una hora.
2. **Lado del lab**: relay de Alertmanager y métrica de apagado no limpio aplicados el 19-oct. Nginx, Alertmanager, Prometheus, timer y exportador están activos; falta que la Pi confirme el acceso al endpoint. No se ha enviado una alerta artificial a Telegram.
3. **Clave SSH propia para la Pi** (hoy acepta la misma que el lab) y unos pasos escritos para reconstruirla si falla la tarjeta.
4. Pruebas T7, T8 y TH3 (con el punto de acceso), y el recorrido completo de `test-indicator`.
5. **Placa a medida del panel** (`docs/hardware/panel-pcb.md`): el circuito está listo, pero falta dibujarla en un programa de PCB, medir zumbador, LED y pulsadores y pedirla. No se ha pasado ERC ni DRC.
6. **Sensores de temperatura (lab y rack) y módulo SIM**: previstos en el hardware (bornera de 1-Wire y cabecera de la SIM), sin soporte en el software todavía.

## Limitaciones conocidas

- **WoL solo enciende un equipo apagado y alimentado:** no revive un lab colgado ni uno sin corriente.
- **La Pi depende del Wi-Fi y del router.** Si el router cae y no está en el SAI, la Pi no ve ni avisa; con la Pi apagada, nadie vigila.
- **Apagar la Pi con el botón apaga también el vigilante** hasta que se vuelva a pulsar.
- **Un solo canal directo:** si falla Internet, el aviso espera en la cola (caduca a las 6 h) y sale por el relay del lab si estuviera aplicado.
- La tarjeta microSD es el punto débil de una Pi encendida durante meses; conviene tener otra grabada de repuesto.

## Operación rápida

Como el usuario del servicio, en la Pi (`sudo -u labwatch lab-watchdog -c /etc/lab-watchdog/config.json …`):

| Orden | Para qué |
|---|---|
| `status` | Estado guardado |
| `maintenance 30` | Pausar 30 min antes de reiniciar o parar el lab a propósito (si no, cuenta como caída y gasta intentos) |
| `test-notify` | Mensaje de prueba por los canales configurados |
| `test-indicator` | Recorrer LED y zumbador (parar antes el servicio) |
| `purge-outbox` | Vaciar la cola de avisos (con el servicio parado) |

Despliegue desde el Mac: `set -a && source .env && set +a` y después `ansible-playbook -i inventory-watchdog.yml playbooks/lab-watchdog.yml`, siempre con `--check --diff` antes.
