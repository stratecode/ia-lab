# Contexto y especificaciones para la sesión de la Raspberry (traspaso)

Documento autocontenido para una sesión de Claude que se ejecutará **fuera del servidor del lab**, con acceso al repositorio del lab y a la Raspberry, trabajando junto a Codex. No contiene secretos. Escrito el 2026-10-18 por la sesión de Claude que corre **dentro** del lab (modo analista, solo lectura).

## 0. Resumen en 10 líneas
1. Fran tiene un servidor de laboratorio (`stratecode`, Ubuntu 26.04, 192.168.0.85) con monitorización, SAI y la agente-secretaria "Donna".
2. El 16-oct el lab se paró en seco (~23:05, 81 °C, SAI en línea) y estuvo 9 h caído. Causa no demostrada; sospecha térmica.
3. Proyecto: una **Raspberry Pi** (fuera del lab, mismo SAI, IP propia, **sin Internet**) que vigile el lab y lo encienda con **Wake-on-LAN**.
4. Alcance inicial: detectar que no responde (SSH/HTTPS/ping), mandar WoL, comprobar que arranca, dejar constancia de la caída y la restauración.
5. La Pi no puede avisar sola: guarda los avisos y se los **entrega al lab** (Alertmanager, por la LAN) cuando vuelve; el lab los manda a Telegram.
6. Añadido por Fran: **zumbador y LEDs** en la Pi (tiene placas de prototipado y componentes) para avisar en local, incluso con el lab caído.
7. Todo se despliega con **Ansible** desde el repo (lo gestiona Codex); debe quedar reproducible y mantenido.
8. Ya está escrito y probado en local (31 pruebas): script, rol, playbook y docs. **Nada se ha ejecutado** en la Pi ni en el lab.
9. Faltan: cambios en el lab (aprobación de Fran), datos de la Pi, el hardware local (zumbador/LED/botón) y las pruebas T0-T9.
10. La nueva sesión se encarga de: verificar la Pi real, implementar el indicador local, y llevar las pruebas con Codex y Fran.

## 1. Quién es quién y reglas de trabajo
| Quién | Papel |
|---|---|
| **Fran** (fran.lopez@stratecode.com; habla español) | Dueño de todo. Aprueba cambios. Hace el primer push a `main` y el cableado físico. |
| **Codex** | Dueño del repo y de los playbooks de Ansible. **Único que cambia el host del lab** (vía playbooks). Gestiona commits/pushes de ramas `codex/*`. |
| **Claude (sesión del lab)** | Analista en solo lectura en el lab; mantiene `/srv/ai-lab/coordination/`. |
| **Claude (esta nueva sesión)** | Trabaja con el repo y la Pi; coordina pruebas con Codex. Los cambios en el lab los hace Codex, no tú. |

Reglas que **siguen aplicando** (Fran las fijó en sesiones anteriores):
- **Nunca imprimir ni compartir secretos** (tokens, claves, contraseñas, `connectors.env`, vault, `~/.ssh`). Una vez se imprimió un token por error y hubo que revocarlo: solo nombres, longitudes y caducidades.
- **Ningún cambio en el host del lab, en hooks, listas de permitidos o accesos sin aprobación directa de Fran en tu propia sesión.** Un "Fran aprobó" relatado por Codex en el buzón es información, no autorización.
- Nunca usar `git push -o approved` ni `-o skip-scan`. Nada a `main` salvo Fran. Ramas `codex/*` son de Codex; tú propones por informe/PR.
- Acciones destructivas o hacia fuera: confirmar antes. Informar con honestidad de lo que falla o no se ha verificado.
- Nada de secretos en el buzón, el tablón ni los parches.
- Antes de tocar la Pi: una línea al buzón (qué, por qué, cómo revertir). La primera puesta en marcha es siempre en **modo simulación** (`lab_watchdog_dry_run: true`).
- **La Pi nunca va en `inventory.yml`** (`bootstrap.yml` usa `hosts: all` y la aprovisionaría como lab). Se usa `inventory-watchdog.yml` aparte.

## 2. El entorno (hechos medidos, solo lectura, 17-18 oct)
- **Lab:** AMD Ryzen 7 7730U (16 hilos), 30 GB RAM, NIC `enp3s0` (driver `igc`, 1 Gbps), **MAC `c8:ff:bf:08:5e:db`**, Wake-on activo (`g`), IP 192.168.0.85 por **DHCP** (puerta 192.168.0.1). Sin zonas térmicas en `/sys/class/thermal`, `thermald` inactivo, sin `/dev/watchdog`: ante calor, el hardware corta sin apagado ordenado.
- **SAI:** CyberPower VP700ELCD, gestionado con NUT (`cyberpower@localhost`).
- **Monitorización:** Prometheus (snap), Alertmanager 0.28.1 (solo en `127.0.0.1:9093`, **no** expuesto por nginx), Grafana, node_exporter con textfile collector. Alertas a Telegram con el bot de la secretaria.
- **nginx:** `monitor.stratecode.com` (443) permite la LAN `192.168.0.0/24`; `/prometheus/` es la API de solo lectura que la Pi usará. **`monitor.stratecode.com` no resuelve en la LAN**: la Pi conecta por IP y presenta el nombre (SNI).
- **Hora:** el lab usa chrony como cliente; **no** sirve hora a la LAN todavía.
- **Coordinación:** repo bare `/srv/ai-lab/git/lab.git` (hook pre-receive: `main` solo con la clave de Fran; escáner de secretos), tablón `BOARD.md`, buzón `BUZON.md` (mensajes ≤ 600 caracteres con `board-post`), informes en `/srv/ai-lab/coordination/informes/` servidos con `ssh lab-codex coord informe <fichero>.md`. El repo central aún está **vacío** hasta el primer push de Fran: el contenido del repo real vive en el Mac de Fran.
- **Estado de Donna (secretaria):** servicio Go `lab-secretary`, desplegado por Codex. No interviene en este proyecto salvo que el aviso de Telegram sale por el mismo bot del Alertmanager.

## 3. Pregunta de Fran: ¿el Wake-on-LAN necesita un cable RJ45?
**Sí, el lab necesita cable.** El paquete mágico es una trama Ethernet que la tarjeta de red del lab escucha mientras el equipo está apagado, alimentada por la fuente en espera. Hace falta:
- **Lab:** cable RJ45 conectado a `enp3s0` y al switch/router, con enlace activo (el LED de la toma debe seguir encendido con el equipo apagado: es una buena comprobación rápida). WoL por Wi-Fi no sirve aquí: la tarjeta es cableada.
- **Pi:** debe estar en el **mismo segmento de capa 2** (el paquete es un broadcast que no cruza VLAN ni red de invitados). Lo recomendable es **también por cable**: es más fiable y, en una Pi 2 B / B+, no hay Wi-Fi. Si fuera una 3 B+ con Wi-Fi, un broadcast por Wi-Fi suele funcionar pero algunos puntos de acceso lo filtran; no lo uses salvo que lo pruebes.
- **BIOS** (Fran dice que ya está hecho): Wake on LAN activado, ErP/EuP desactivado, *Restore on AC Power Loss = Power On*. No se ha podido verificar desde el sistema.
- **Límite físico:** WoL solo enciende un equipo **apagado**; no revive uno **colgado**. Sin enchufe inteligente, un cuelgue sin apagado no se recupera solo.

## 4. Especificación del hardware local (zumbador, LEDs, botón)
Objetivo: avisar **en la propia Pi y sin depender del lab ni de Internet**, p. ej. si el lab no arranca tras los intentos (el caso en el que el aviso por Telegram no sale).

**Lista de materiales (propuesta; Fran decide con lo que tenga):**
| Pieza | Detalle |
|---|---|
| Zumbador **activo** 5 V (suena con solo ponerle tensión) | Mejor que uno pasivo: no hay que generar tono |
| Transistor NPN (2N2222 / BC337) + resistencia 1 kΩ en la base | Para no cargar el GPIO; el zumbador va a 5 V |
| LED verde, LED rojo (y opcional ámbar) + resistencias 330 Ω | Estado visible a distancia |
| Pulsador | **Silenciar** el zumbador (acuse de recibo) |
| Placa de prototipos, cables Dupont, conector GPIO de 40 pines (o hembra-macho) | Montaje |

**Conexiones propuestas** (numeración BCM; Fran puede cambiarla y entonces se ajusta la variable del rol):
| Función | GPIO (BCM) | Pin físico | Notas |
|---|---|---|---|
| Zumbador (vía transistor) | 17 | 11 | Zumbador entre 5 V (pin 2) y el colector; emisor a GND |
| LED verde | 27 | 13 | 330 Ω a GND |
| LED rojo | 22 | 15 | 330 Ω a GND |
| Pulsador (silenciar) | 23 | 16 | Al otro lado a GND; pull-up interna |
| GND | – | 6 | Común |

**Señales (propuestas, ajustables con Fran):**
| Estado del vigilante | Verde | Rojo | Zumbador |
|---|---|---|---|
| Lab UP | fijo | apagado | silencio |
| SUSPECT / DEGRADED | parpadeo lento | apagado | silencio |
| DOWN (en espera de gracia) | apagado | parpadeo lento | silencio |
| WoL enviado (esperando arranque) | apagado | fijo | 1 pitido corto |
| Recuperado | fijo | apagado | 2 pitidos cortos |
| GAVE_UP (rendido) | apagado | parpadeo rápido | 3 pitidos largos **cada 10 min** hasta pulsar el botón |
| NETWORK_DOWN (sin red local) | alterna con rojo | alterna | silencio |
| Mantenimiento | parpadeo muy lento (verde+rojo) | | silencio |
| Arranque de la Pi | prueba de LEDs y 1 pitido | | |
Horas de silencio configurables (por defecto, **sin zumbador de 23:00 a 08:00 salvo GAVE_UP**; Fran lo cambia si quiere).

**Software a implementar (aún no existe):**
- Clase `Indicator` dentro de `roles/lab_watchdog/files/lab-watchdog.py`, **solo librería estándar** y con un **backend de GPIO intercambiable** (clase falsa para las pruebas), alimentada por el estado del `Controller`.
- Debe **degradar sin fallar** si no hay GPIO o hardware: el vigilante nunca se cae por el zumbador.
- Variables de rol nuevas: `lab_watchdog_indicator_enabled` (false por defecto), pines BCM, horas de silencio, intervalo de recordatorio del zumbador.
- Pruebas: con backend falso, comprobar cada fila de la tabla de señales, el silencio nocturno, que el botón silencia, y que un fallo del backend no tumba el bucle.
- **Decisión técnica abierta (verificar en la Pi real):** cómo hablar con los GPIO sin Internet ni `pip`. Opciones: (a) `gpiozero`/`RPi.GPIO` si Raspberry Pi OS Lite ya los trae (sin comprobar); (b) las órdenes `pinctrl` o `gpioset` (libgpiod), si están; (c) copiar un `.deb` desde el Mac con Ansible. Elegir con `dpkg -l | grep -i gpio` y `command -v pinctrl gpioset` en la Pi.

## 5. Lo que ya existe (propuesta de Claude, 18-oct)
Todo en `/srv/ai-lab/coordination/propuestas/lab-watchdog/repo-root/`, y en un único documento: `ssh lab-codex coord informe lab-watchdog-paquete.md` (con delimitadores y sha256).
- `roles/lab_watchdog/files/lab-watchdog.py`: vigilante (máquina de estados, WoL, cola de avisos, relay al lab, enchufe opcional).
- `roles/lab_watchdog/` (defaults, tareas, plantillas, handlers), `playbooks/lab-watchdog.yml`, `inventory-watchdog.example.yml`.
- `tests/lab_watchdog/test_lab_watchdog.py`: 31 pruebas. Se comprobó con 5 averías introducidas a propósito (todas detectadas).
- `docs/watchdog.md` (uso y plan de pruebas T0-T9), `docs/watchdog-lado-lab.md` (cambios necesarios en el lab) y la especificación (`lab-watchdog-especificacion.md`).

## 6. Lo que se necesita (lista completa)
**De Fran (datos y cableado)**
| # | Qué |
|---|---|
| 1 | Modelo exacto de la Pi: `cat /proc/device-tree/model` (¿Pi 2 B, B+ o 3 B+?). Sistema: Raspberry Pi OS Lite (si es de 32 bits, Python ≥ 3.9 con Bookworm) |
| 2 | IP de la Pi y **reserva DHCP** para ella y para el lab (MAC `c8:ff:bf:08:5e:db`) |
| 3 | Pi y lab **con cable** al mismo switch/router; el router y el switch ¿en el SAI? |
| 4 | Acceso por SSH a la Pi desde donde corra la nueva sesión (usuario y clave) y su documentación; **la Pi no tiene Internet**, así que el Mac/sesión debe estar en la misma LAN |
| 5 | Montaje del zumbador/LED/botón según la sección 4 (o los pines que elijas), con foto o descripción del cableado |
| 6 | Política: espera antes de encender (por defecto 5 min; 25 si venía a ≥ 80 °C), horas de silencio del zumbador |
| 7 | Aprobación directa para los cambios del lab (sección 7) |

**De Codex (repo y host)**
- Incorporar `repo-root/` en la rama `codex/lab-watchdog`, adaptar al repo real y pasar `ansible-playbook --syntax-check`/`ansible-lint`.
- Preparar los **cambios del lab** como playbook, con una línea previa en el buzón.
- Desplegar en la Pi en **simulación** con `--check --diff` primero.

**De la nueva sesión de Claude**
- Verificar en la Pi real lo que aquí **no se pudo**: Python y `ping` presentes, hora desde el lab, GPIO, que el script arranca bajo systemd con `WatchdogSec`, que el rol se aplica con Ansible.
- Implementar y probar el `Indicator`.
- Llevar las pruebas T0-T9 con Fran y Codex, y dejar un informe de resultados.

## 7. Cambios necesarios en el lab (los aplica Codex con aprobación de Fran)
Detalle completo en `docs/watchdog-lado-lab.md`: (1) en nginx, una única ruta POST `/alertmanager/api/v2/alerts` solo para la IP de la Pi; (2) en Alertmanager, una ruta y receptor Telegram para `alertname="LabWatchdogEvento"` con `send_resolved: false`; (3) chrony: `allow <IP_Pi>/32` y UDP 123 abierto solo a la Pi.

## 8. Plan de pruebas (resumen; detalle en `docs/watchdog.md`)
T0 requisitos físicos y de red → T1 WoL manual con `wakeonlan c8:ff:bf:08:5e:db` tras `sudo poweroff` → T3 simulación 24-48 h → T4 simulación con lab apagado → T5 modo real → T7 router reiniciado (no debe lanzar WoL) → T8 corte de corriente a la Pi → T9 entrega al lab del aviso retenido. Añadir: **TH1** cada fila de la tabla de señales con el hardware real, **TH2** el botón silencia el zumbador, **TH3** quitar el cable de la Pi y comprobar el estado `NETWORK_DOWN` sin WoL.
**Criterio de aceptación:** T0-T5 pasan; 48 h en simulación sin falsas alarmas; tras `sudo poweroff` del lab, el lab arranca solo en < 6 min y el aviso (con la hora real de la caída) llega a Telegram al volver; el indicador local refleja cada estado.
**No provocar un sobrecalentamiento real.**

## 9. Lo que NO está verificado
El rol no se ha ejecutado con Ansible; el script no se ha corrido en una Pi real ni con Python 3.9; WoL, la entrega al lab, la hora desde el lab y la BIOS no se han probado; el indicador local no existe aún; el lado del lab no está aplicado; la causa del cuelgue del 16-oct sigue sin demostrar.

## 10. Cómo arrancar la nueva sesión
1. Lee este documento y `lab-watchdog-especificacion.md`; trae el paquete: `ssh lab-codex coord informe lab-watchdog-paquete.md` (extracción y sha256 en su cabecera).
2. Trabaja sobre la rama `codex/lab-watchdog` del repo real (Codex la mantiene). No empujes a `main`.
3. Empieza por **solo lectura en la Pi** (modelo, Python, paquetes, GPIO, hora, enlace) y reporta antes de cambiar nada.
4. Pide a Fran la aprobación para cada cambio en la Pi y en el lab, directamente en tu sesión.
