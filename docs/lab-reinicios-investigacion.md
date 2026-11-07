# Reinicios en seco del lab: investigación (19-oct-2026)

El lab sufre finales abruptos, sin apagado ordenado y sin una causa registrada. Este documento recoge lo que se midió, las hipótesis examinadas y cómo interpretar las pruebas. **La causa física o de firmware no está determinada.** La actualización del 21-oct está al final.

## Qué pasa

El exportador de arranque sucio (`lab_boot_previous_clean`) y `last -x` marcan muchos de estos cortes sin explicación. En el intervalo en que el BIOS tenía *Restore on AC Power Loss = Power On*, el lab volvía a arrancar en ~35 s; después se restauraron los valores de fábrica salvo WOL y ya no puede asumirse ese comportamiento.

| Corte | CPU media | Potencia SoC | Temperatura | Contexto |
|---|---|---|---|---|
| 6-oct 22:42 | 1,1 % | 9,5 W | 43 °C | Fin de 37 días seguidos encendido; arrancó a mano 19 min después |
| 7-oct 03:20 | 1,4 % | 7,2 W | 43 °C | Arrancó a mano por la mañana |
| 13-oct 11:32 | 1,0 % | 8,7 W | 41 °C | |
| 13-oct 17:48 | 0,2 % | 8,3 W | 41 °C | |
| 16-oct 23:00 | **53 %** | **14 W** | **75 °C (pico 81)** | Con carga alta y la CPU frenada: probablemente otra causa, térmica |
| 18-oct 20:31 | 0,3 % | 7,9 W | 34 °C | |
| 18-oct 22:37 | 0,4 % | 7,9 W | 35 °C | |
| 19-oct 00:05 | 1,9 % | 8,2 W | 43 °C | Corría un Ansible del propio equipo |

La potencia es la que declara el SoC, no la de la pared. El 18-oct hubo además finales secos a las 18:17, 20:39 (un arranque de 7 s) y 21:10 que coinciden con pruebas o con el BIOS y no se cuentan.

**Ritmo de cortes por periodo:**

| Periodo | Condiciones | Cortes sin explicación |
|---|---|---|
| 30-ago → 6-oct | Sin SAI, BIOS original | 0 en 37 días |
| 6-oct → 14-oct | Sin SAI, BIOS original | 4 en 8,7 días |
| 14-oct → 18-oct 18:15 | Con SAI, BIOS original | 1 en 4 días (el térmico) |
| Desde el 18-oct 18:15 | Con SAI y **BIOS modificado** | **4 en 6 horas**, todos en reposo |

## Hipótesis examinadas y límites de la evidencia

- **Carga, calor o memoria** (siete de los ocho cortes): CPU por debajo del 5 %, 34 a 44 °C, 12 a 27 GiB de RAM libre, sin actividad del modelo y con tensiones normales.
- **Software y kernel:** no se observaron errores del kernel, de máquina (MCE) ni de SATA antes de los cortes examinados; SMART en `PASSED` con 0 sectores dañados. El problema aparece con tres versiones de kernel distintas (7.0.0-30, -34 y -38). No hay cambios de configuración ni despliegues del repo entre el 1 y el 8-oct, ni un denominador común en lo último que hacía el lab antes de cada corte. Esto reduce la sospecha de una regresión concreta, pero no excluye un fallo de software o firmware.
- **Un pánico o cuelgue de software:** en la configuración anterior a kdump, `kernel.panic = 0` y no había watchdog (`/dev/watchdog` no existe). Los reinicios autónomos de aquel intervalo apuntaban a un **reinicio de hardware o a un corte de corriente con arranque automático del BIOS**. Un fallo duro de CPU, un pánico sin volcado y un cuelgue con reinicio por firmware siguen siendo posibles.
- **El SAI como origen:** se conectó el 14-oct y hay cuatro cortes anteriores. Sus datos tampoco sirven de coartada (239 a 245 V, 0 pasos a batería) porque mide su propia entrada, no lo que llega a la fuente del lab.
- **La regleta o la toma:** la Pi cuelga de la misma regleta, lleva encendida desde las 21:25 con 0 bajos de tensión y no notó los cortes de las 22:37 y las 00:05. Es un indicio, no una prueba: su fuente aguanta cortes muy breves.

## Lo que no se pudo medir

- El contador de ciclos de encendido del SSD sube en **cada** arranque (17 de 17, también en los limpios), así que no distingue un corte de corriente de un reinicio completo de la placa.
- El SSD (KY510 de 1 TB, firmware HT5310B3) tiene `Power-Off_Retract_Count` = 102 sobre 148 ciclos de encendido: casi todos sus arranques vienen de un apagado brusco. Puede incluir apagados manuales.
- Hardware del lab: placa HCAR5000-MI de HC Technology, AMD Ryzen 7 7730U, BIOS AMI 0.33 (25-sep-2025), kernel 7.0.0-38, CPU con gobernador `performance` y *boost* activo.

## Qué se ha activado (solo observacional, 19-oct 00:38)

Rol `lab_crash_diagnostics` y playbook `playbooks/enable-crash-diagnostics.yml`. Son herramientas de observación, con **un único efecto sobre el comportamiento**: con kdump armado, un pánico del kernel guarda un volcado (algo de 1 a 3 GiB en `/var/crash`, hay 781 GB libres) y **reinicia la máquina por sí sola**, en lugar de quedarse congelada como hasta ahora (`kernel.panic = 0`). El exportador solo lee SMART y escribe un fichero pequeño.

1. **kdump armado** (`USE_KDUMP=1`; la memoria de captura de 512 MiB ya estaba reservada). Un pánico del kernel deja un volcado en `/var/crash` y reinicia la máquina.
2. **Exportador SMART del SSD** cada 60 s al textfile de node_exporter (`lab-ssd.prom`): `lab_ssd_smart_attribute_raw{id="9|12|192"}` y `lab_ssd_smart_up`.

**Valores de referencia a las 00:38 del 19-oct:** `Power_On_Hours` = 3464, `Power_Cycle_Count` = 148, `Power-Off_Retract_Count` = 102.

## Cómo interpretarlo tras el próximo corte

1. **¿Hay un volcado en `/var/crash`?**
   - **Sí:** fue un pánico del kernel, un fallo de software. Se analiza con `crash` y el kernel con símbolos.
   - **No:** no se ha demostrado un pánico. Apunta a hardware, alimentación o firmware, pero un fallo temprano o un fallo del mecanismo de captura puede impedir el volcado.
2. **Contador 192 del SSD** (`Power-Off_Retract_Count`) antes y después del corte. Primero hay que **calibrarlo**: hacer un reinicio limpio y ver si el 192 no cambia. Si es así, y en el corte sí sube en 1, el SSD vio un apagado brusco de su alimentación. No es una prueba concluyente: la placa puede hacer un reinicio completo que corte los raíles.
3. **Exportador de arranque sucio** (`lab_boot_unclean_total`) para contar los cortes y su hora.
4. Mirar si coincide con alguna operación de Ansible o con actividad en la regleta.

## Decisión de Fran (19-oct)

Si **vuelve a reiniciarse sin explicación** y los registros que ya se recogen no aclaran nada, se hace un **repaso monográfico del BIOS**: qué opciones se tocaron el 18-oct, revertirlas de una en una y observar. Con el ritmo actual (unos 4 cortes en 6 horas) bastan unas 12 horas sin ningún corte para considerar que un cambio funciona. Ese repaso incluye también la comprobación de un BIOS más nuevo del fabricante, `processor.max_cstate=1` o los estados C desactivados en el BIOS, y revertir *Restore on AC Power Loss*.

Quedan fuera por decisión de Fran: enchufar el lab directo a la pared sin SAI y cambiar la fuente de alimentación, hasta que se aborde el repaso del BIOS.

## Actualización: caída del 20-oct y prueba controlada del 21-oct

El 20-oct el registro del lab acabó abruptamente a las **20:49:41 CEST**. La Pi detectó la pérdida de servicio, envió WOL repetidamente y el lab no terminó un arranque hasta las **23:14:26**, cuando Fran pulsó el botón de encendido. La última muestra previa tenía CPU a **34 °C**, carga 1 min **0,06**, potencia declarada del SoC **5 W**, **26 GiB** de memoria disponible y SAI en línea, con entrada y salida a **238 V** y carga del **5 %**. En los quince minutos anteriores la entrada del SAI estuvo entre **237 y 240 V**, sin paso a batería. El SAI no mide la salida DC del adaptador ni los raíles de la placa. No hay vmcore ni rastro de apagado ordenado.

El 21-oct se hizo un `systemctl poweroff` limpio a las **08:55:53**. El centinela envió WOL a las **08:56:30** y de nuevo a las **08:57:33**. El lab registró un **arranque independiente de cinco segundos**, de **08:56:45 a 08:56:50** (boot ID `85dd31f803ab4ad1934ea9eb982750b4`), quince segundos después del primer WOL. Llegó al kernel y a los primeros servicios de systemd; el registro se interrumpió en seco. Entre los últimos mensajes estaba el inicio del volcado del journal a disco, sin evidencia de que fuese la causa. El sistema no llegó a estar disponible para la Pi. Fran vio el LED de enlace Ethernet encendido, confirmó el SAI activo y finalmente pulsó el botón; el arranque siguiente comenzó a las **08:59:17**. Un reinicio limpio de control a las **09:05:09** arrancó correctamente a las **09:05:25**.

**Interpretación:** la asociación temporal entre WOL y el arranque de cinco segundos es fuerte (fiabilidad estimada **95 %**), aunque no hay una captura eléctrica que pruebe causalidad. Por tanto, la prueba no muestra un WOL que no llega; muestra al menos un arranque tras WOL que muere antes de completar el inicio. La causa de ese segundo corte sigue abierta entre placa/firmware, adaptador DC, almacenamiento y fallo duro del sistema. El LED Ethernet solo prueba alimentación o enlace de la NIC, no estabilidad del resto de la placa. El centinela informa correctamente de que el servicio sigue caído, pero su mensaje posterior «recuperado tras 2 paquetes Wake-on-LAN» atribuye erróneamente el arranque manual al WOL; necesita distinguir entre paquete enviado y arranque causado por él.

El SSD pasó de `Power_Cycle_Count` **160** y `Power-Off_Retract_Count` **111** antes de esta prueba a **162** y **112** tras el arranque de cinco segundos y el arranque manual. El reinicio limpio posterior dejó el contador 192 en **112**. Esto sugiere que el SSD detectó una retirada de alimentación no ordenada durante la prueba (fiabilidad **80 %**); los dos arranques intermedios impiden asignarla con certeza a un instante exacto. El marcador `lab_boot_previous_clean` tampoco detectó el arranque de cinco segundos porque este no llegó a ejecutar el servicio que escribe el marcador; para sucesos tempranos debe consultarse `journalctl --list-boots`.

La etiqueta exterior identifica el equipo como **NiPoGi AM06PRO**, Ryzen 7 7730U, 32 GB RAM, 1 TB SSD y entrada USB-C **19 V / 3,42 A**. La placa se identifica por DMI como **HC Technology HCAR5000-MI** y la pantalla principal de la BIOS muestra AMI **0.33 x64, compilada el 25-sep-2025**. `fwupdmgr get-updates` no ofrece firmware de sistema para ella: solo actualizaciones de las bases de firmas de Secure Boot, que no tienen relación demostrada con estos cortes. La [página oficial de descargas de NiPoGi](https://nipogi.store/pages/drivers-downloads) no anuncia una imagen de BIOS para AM06PRO. No se ha verificado una versión posterior compatible con esta revisión.

Fotos de la BIOS tomadas el 21-oct: `Resume On Lan = Enabled`, `AC Failure Resume = Always Off`, `Global C-state Control = Auto`, `Core Performance Boost = Auto`, `Core Watchdog Timer = Disabled`, ASPM de los puertos PCIe en `Auto`, `SATA Auto Shutdown = Disabled` y `Aggressive SATA Device Sleep` desactivado en los dos puertos. No se ve ErP/Deep Sleep en los menús de alimentación examinados. La Intel I226-V muestra MAC `C8:FF:BF:08:5E:DB`, igual al destino WOL de la Pi. Ningún valor explica por sí solo la pérdida abrupta de alimentación o el arranque de cinco segundos.

El [foro enlazado desde la web oficial de NiPoGi](https://www.nipogi.com/pages/contact) contiene un [testimonio de otro propietario](https://www.minipcunion.com/viewtopic.php?t=5964) con AM06PRO/7730U/HCAR5000-MI y BIOS 0.33 de la misma fecha: relata apagados repentinos y que instalar una compilación **anterior, de diciembre de 2024, también denominada 0.33**, empeoró el problema y rompió Bluetooth. Relata asimismo que desactivar estados C no lo resolvió. Es una experiencia individual no verificada, útil para evitar confundir el número de versión con la fecha o asumir que ese archivo arregla el fallo; no demuestra que este lab tenga la misma causa.

**Siguiente paso de diagnóstico:** observar una caída espontánea o reproducir el arranque fallido temprano, registrando a la vez LED de encendido, pantalla, telemetría eléctrica de la salida DC y tiempos del centinela. Evitar flashear firmware de otro equipo basándose solo en `0.33` o `HCAR5000-MI`.

### Prueba con monitor, 21-oct 09:56–10:00

El lab se apagó limpiamente a las **09:56:29**. La Pi recibió `/encender` por Telegram a las **09:57:57**; el nuevo arranque del lab comenzó a las **09:58:11** y llegó al login de Ubuntu. Fran confirmó el texto en pantalla y SSH respondió antes de las **09:59:38**. `Power_Cycle_Count` pasó de **163** a **164**; `Power-Off_Retract_Count` siguió en **112**. Esto verifica que el WOL manual desde la Pi puede encender el equipo y completar el arranque. **No prueba la recuperación automática del centinela**, porque el comando de Telegram se adelantó a su acción. La Pi quedó sin mantenimiento tras `/reanudar` a las **09:58:34**.

Después de conectar la pantalla, el lab ya no detectó por USB el SAI CyberPower (`lsusb` no lo enumera); `nut-driver@cyberpower` reintenta y `upsc` informa `Driver not connected`. Fran confirmó que el mini PC está temporalmente en el escritorio y desconectado del cable USB de datos del SAI. Esa pérdida de telemetría es esperable en esta ubicación; no hay evidencia de que sea la causa de los apagados anteriores. El centinela puede detectar y ejecutar WOL sin esa telemetría.

### Recuperación automática con monitor, 21-oct 10:06–10:13

Primera prueba automática: apagado ordenado a las **10:06:55**; primer WOL del centinela a las **10:07:36**; arranque de Linux a las **10:07:50**; Pi notificó recuperación a las **10:08:14**. `Power_Cycle_Count` pasó de **164** a **165** y `Power-Off_Retract_Count` siguió en **112**. No se obtuvo vídeo de esta primera secuencia.

Segunda prueba automática, con Fran preparado para grabar: apagado ordenado a las **10:09:34**; primer WOL a las **10:10:19**; único arranque de Linux registrado a las **10:10:33** (boot ID `7c22829034fd4974aaf21e1466c03889`); Pi notificó recuperación a las **10:10:57**. A las **10:12:19** el sistema estaba operativo; `Power_Cycle_Count` pasó de **165** a **166** y `Power-Off_Retract_Count` permaneció en **112**. `last -x` y el journal del intervalo solo muestran este arranque de Linux, sin un segundo boot ID. El vídeo de 90 s aportado por Fran muestra el login inicial, la secuencia de apagado de Ubuntu, el mini PC con LED apagado mientras espera, un único logo AMI y el arranque posterior de Ubuntu. No se ve un segundo apagado después del WOL. La cámara deja de enfocar el LED durante el encendido, así que no descarta un corte brevísimo anterior a Linux; los registros y el contador SMART tampoco lo sugieren. El vídeo termina durante los mensajes de inicio, pero la Pi y la comprobación SSH confirman que el servicio llegó a estar disponible. La primera interpretación de «LED apagado y nuevo POST» se refería al apagado solicitado y al posterior encendido por WOL, no a un reinicio adicional visible.

Estas dos pruebas verifican que el centinela envía WOL y logra recuperar el servicio automáticamente en esta ubicación. No explican el arranque fallido de cinco segundos de las 08:56 ni la caída espontánea del 20-oct. La repetición de pruebas se detuvo tras confirmar recuperación; la causa física o de firmware sigue abierta.

### Prueba temporal de BIOS y carga de CPU, 21-oct 10:26–10:34

Fran cambió únicamente `Advanced > AMD CBS > CPU Common Options > Global C-state Control` de `Auto` a `Disabled`. El lab arrancó a las **10:26:31**; Linux informó `cpuidle/current_driver = none` y no mostró estados `cpuidle` por CPU, coherente con el cambio confirmado por Fran en la BIOS. La versión siguió siendo **0.33**: no se flasheó firmware. El centinela estuvo en mantenimiento durante la edición de BIOS y se reanudó antes de la prueba.

Se ejecutó `openssl speed -multi 16 -seconds 180 sha256` bajo un supervisor temporal que leyó `k10temp/Tctl` cada segundo y habría detenido la carga a **90 °C**. El registro completo quedó en `/tmp/lab-cpu-probe-20261021.csv` en el equipo controlador. La carga duró **180 s** y alcanzó **84,4 °C** sin apagado ni pérdida de servicio. Fran observó LED e imagen continuos. Tras ella, el lab seguía en `running`, no quedaban procesos de carga, la Pi no registró caída y SMART mantuvo `Power_Cycle_Count = 166` y `Power-Off_Retract_Count = 112`. No apareció un volcado nuevo en `/var/crash`.

Esta prueba supera el pico Tctl de **81 °C** anotado en el corte bajo carga del 16-oct y no reproduce un apagado: la temperatura de CPU por sí sola no queda demostrada como causa. Es una carga de cálculo concreta, no el benchmark exacto que Fran recuerda como desencadenante. AMD declara **95 °C Tjmax** para el Ryzen 7 7730U; un corte a 81 °C podría obedecer a otra protección, un punto térmico no medido, alimentación u otro fallo. Desactivar C-states es una prueba de depuración para los cortes en reposo y puede aumentar consumo/temperatura en reposo; no está validado como ajuste permanente.

### Decisión de diagnóstico, 21-oct 10:40

La prueba CPU de 180 s se ejecutó después de desactivar C-states y no tiene una repetición equivalente con `Auto`. Por tanto, no demuestra que el cambio de BIOS haya corregido el fallo ni descarta CPU/controlador de memoria/alimentación bajo otras cargas. La temperatura en reposo volvió a **43,9 °C** a las 10:40; el pico transitorio posterior al arranque no permite atribuir un aumento sostenido al cambio de C-states.

Se propone restaurar **únicamente `Global C-state Control = Auto`** para recuperar la referencia original; esta restauración está pendiente de ejecución física. Mantener `Resume On Lan = Enabled`, `Core Performance Boost = Auto`, ASPM en `Auto`, `AC Failure Resume = Always Off` y el watchdog de BIOS desactivado durante el diagnóstico. No hay una imagen de firmware nueva compatible verificada.

Orden de pruebas: (1) una pasada de `memtester` sobre 16 GiB, con margen para servicios y registro externo; (2) lecturas verificadas de archivos de modelos y carga/descarga repetida, sin escrituras sobre el dispositivo de bloques; (3) inferencia con transiciones repetidas entre reposo y carga, registrando memoria, swap, E/S, temperatura y errores del kernel. Si hay errores de memoria, contrastarlos con una prueba de memoria desde arranque y, cuando proceda, aislar módulos. Si aparece un patrón de fallo reproducible, repetirlo cambiando solo C-states para una comparación A/B. Un intervalo sin fallos aporta evidencia limitada y no confirma por sí solo una reparación.

A las 10:40 el lab estaba operativo y no quedaban procesos `memtester` ni `openssl` activos. `memtester` está instalado; la prueba de RAM aún no se ha ejecutado.

### Nueva caída tras mensaje a Donna, 21-oct ~10:42

Fran informó de un apagado inmediato después de enviar **«Hola»** a Donna por Telegram. Confirmó que **Global C-state Control seguía en Disabled**, que el LED de encendido del mini PC estaba apagado y que los LED Ethernet seguían encendidos. Por tanto, desactivar C-states no evitó esta caída. La relación con el mensaje es temporal y todavía requiere comprobar los registros del servicio y del motor de inferencia.

La Pi detectó la caída a las **10:43:09**, con última temperatura conocida de **41 °C**. A las **10:43:10** registró que había alcanzado el límite de recuperaciones (**2/hora, 4/día**), consumido durante las pruebas anteriores. Esto bloqueó la secuencia inicial, pero una lectura posterior mostró reintentos automáticos desde las **10:44:13**, cada aproximadamente 63 s; por tanto, era incorrecto concluir que no hubo WOL automático en este incidente. El código local permite reintentos en `GAVE_UP` incluso después de alcanzar la cuota. Se envió un único paquete WOL manual de 102 bytes desde la Pi a `C8:FF:BF:08:5E:DB` a las **10:43:50**. A las **10:44:53** el lab seguía inaccesible por SSH. Esto confirma ausencia de recuperación del servicio; hace falta el journal posterior para determinar si hubo un arranque temprano fallido. Se indicó a Fran que pulsara una vez el botón, conservando la BIOS y sin repetir el mensaje hasta recoger evidencia.

La frase del centinela «el SAI estaba en red» no acredita alimentación actual: el mini PC sigue en el escritorio sin el USB de telemetría del SAI.

Fran pulsó el botón y observó que el mini PC se apagaba justo después del chequeo de BIOS; todavía no sabemos si llegó a ejecutar el kernel. En el siguiente intento consiguió entrar y permanecer inicialmente en BIOS/Main. Se pidió observar cinco minutos sin cambiar opciones. A las **10:48:48** se activó mantenimiento de la Pi durante quince minutos para suspender los reintentos mientras se observa el equipo. Resultado de permanencia en BIOS pendiente.

## Cómo revertir

```bash
# kdump: desactivarlo
sudo sed -i 's/^USE_KDUMP=1/USE_KDUMP=0/' /etc/default/kdump-tools && sudo systemctl restart kdump-tools
# exportador del SSD
sudo systemctl disable --now lab-ssd-textfile-exporter.timer
sudo rm -f /etc/systemd/system/lab-ssd-textfile-exporter.{service,timer} /usr/local/bin/lab-ssd-textfile-exporter /var/lib/prometheus/node-exporter/lab-ssd.prom
sudo systemctl daemon-reload
```

## Para volver a aplicarlo

```bash
set -a && source .env && set +a
ansible-playbook playbooks/enable-crash-diagnostics.yml -e lab_crash_diag_enabled=true --check --diff
# y, con aprobación de Fran, el mismo comando sin --check --diff
```

Usa `inventory.yml` (el lab). **Nunca** con `inventory-watchdog.yml`: ese es la Raspberry Pi.

### Arranque con servicios bloqueados, 21-oct 11:17–13:22

Fran confirmó cinco minutos sin apagado en BIOS/Main. Posteriormente apareció la consola GRUB; sus fotografías muestran `$prefix=(hd0,gpt2)/grub`, `grub.cfg` legible, menú oculto y timeout cero. La causa de la consola no está determinada; pulsar Esc durante el arranque también puede influir en cómo se accede a GRUB. No se ha reparado ni reinstalado el cargador.

Se arrancó manualmente el kernel `7.0.0-38-generic` con `systemd.mask=lab-secretary.service systemd.mask=llama-cpp-main.service`. A las 13:22, SSH confirmó uptime de 2 h 04 min, ambas unidades `masked-runtime/inactive`, `cpuidle/current_driver=none`, Tctl 39,4 °C, 27 GiB disponibles y swap sin uso. Esta estabilidad limitada no demuestra que los servicios sean la causa ni descarta hardware. No se cambió C-states de nuevo.

Journal del intervalo 10:40–11:23 guardado fuera del mini PC en `/tmp/lab-investigation-20261021-1322/incident-journal.log` del Mac. Solo aparece un inicio de kernel en ese intervalo, a las 11:17:25; la ausencia de otro registro no excluye un intento temprano sin persistencia. SMART actual: ciclos 168, atributo 192=114; referencia previa 166/112. No se puede asignar cada incremento a un intento concreto.

La línea de kernel introducida contiene erratas en los rangos altos de crashkernel (`64G/128G` y `128G/`); el kernel confirma reserva efectiva de 512 MB, correspondiente al rango actual. No se altera este parámetro durante la siguiente prueba.

A las 13:22 se amplió mantenimiento de Pi una hora. Se prepara activar exclusivamente el motor principal, con Donna bloqueada y sin consultas, para observar carga e inicialización del modelo. Esta prueba aún no se ha iniciado; se espera coordinación física para grabar.

### Registro persistente local, 21-oct 13:30

A petición de Fran se detuvo el controlador de captura del Mac y se instaló instrumentación persistente en el propio lab, sin activar motor ni Donna. Journal ya persistía en disco; se añadió `/etc/systemd/journald.conf.d/90-lab-investigation.conf` con `Storage=persistent`, `SyncIntervalSec=1s`, `SystemMaxUse=4G`, `SystemKeepFree=2G`, `MaxLevelStore=debug`. Esto conserva mensajes emitidos; no hace que cada aplicación emita debug adicional. Se reinició journald, se ejecutaron flush y sync.

Servicio instalado y habilitado `lab-flightrecorder.service`, script `/usr/local/sbin/lab-flightrecorder`. Escribe `/var/log/lab-investigation/metrics.jsonl`, rotación de 32 MiB y siete copias (aprox. 256 MiB en total). Cada segundo registra fecha UTC, boot ID, reloj monotónico, temperaturas, CPU, memoria, vmstat, discos y presión CPU/memoria/E/S; cada cinco segundos añade PID, nombre, ticks CPU y RSS de procesos, sin argumentos ni entorno. Cada muestra se vacía y sincroniza mediante fsync. Arranca de nuevo con el sistema; no trunca registros previos.

Verificación: servicio activo y habilitado, 13 muestras JSON leídas, contador monotónico avanzando, temperaturas y muestras de procesos presentes. `systemd-analyze verify` solo emitió advertencias de CPUAccounting en unidades ajenas preexistentes. No se ha probado recuperación tras un apagado real y no se provocó ninguno. El SSD puede perder datos finales pese a fsync ante pérdida eléctrica. Esta instrumentación añade E/S periódica y puede alterar la reproducción; se mantiene constante en las próximas comparaciones. Kdump informa ready to kdump; no se ha provocado un panic de validación.

Reversión: `sudo systemctl disable --now lab-flightrecorder`; retirar solo su unidad y script, ejecutar daemon-reload; retirar solo `90-lab-investigation.conf` y reiniciar journald. Conservar `/var/log/lab-investigation` para análisis.

### Benchmark de reproducción iniciado, 21-oct 13:33

Con autorización explícita de Fran se inició `lab-model-repro.service` (unidad transitoria, sin arranque automático), ejecutando `/usr/local/sbin/lab-model-repro`. Se encontraron 13 modelos GGUF en el disco; se excluye la segunda parte del Qwen fragmentado como modelo independiente. No se localizó el comando ni las preguntas históricas: es una aproximación, no repetición exacta del benchmark del 16-oct. Diez preguntas sintéticas por modelo, secuencial, contexto 2048, ocho hilos de generación y batch, CPU sin capas GPU, respuestas hasta 128 tokens, 10 s entre modelos, máximo una hora. Servidor aislado en loopback 18185, Donna y servicio principal permanecen bloqueados.

Guardas: parada al alcanzar Tctl 90 °C, fallo de lectura de temperatura o plazo; cgroup MemoryMax=12G y MemorySwapMax=0; RuntimeMaxSec=3700. Cada inicio/fin de fase y cada línea de log del servidor se sincronizan mediante fsync en `/var/log/lab-investigation/benchmark-20261021/`, junto al registrador de métricas activo. Mantenimiento Pi ampliado dos horas desde 13:33. A las 13:33:24 se confirmó primera pregunta de LFM2.5-1.2B activa y Tctl 55,9 °C. Resultado pendiente. Parada manual: `sudo systemctl stop lab-model-repro`.

A las 13:34 el host dejó de responder por SSH; a las 13:34:18 la Pi confirmó dos pings sin respuesta. El último control remoto exitoso situó la prueba en la primera pregunta del LFM2.5-1.2B, después de carga y health correctos. Última temperatura observada por el controlador: 55,9 °C a las 13:33; no es necesariamente el máximo ni la temperatura del fallo. El mantenimiento de Pi seguía vigente y no se enviaron paquetes WOL desde esta sesión. Confirmación física del LED y recuperación de los archivos locales pendientes. No atribuir todavía la pérdida de acceso a un apagado físico sin esa confirmación.

### Recuperación y análisis del corte reproducido, 21-oct 13:48

Fran confirmó LED apagado durante el incidente del benchmark: apagado físico, no solo pérdida de red. Tras encendido manual, kernel arrancó aproximadamente a las 13:43; esta vez sin máscaras de GRUB. `llama-cpp-main` y Donna arrancaron a las 13:43:47 y se detuvieron por SSH a las 13:48:32 para preservar el estado de análisis. El benchmark transitorio no volvió a programarse. El registrador persistente arrancó correctamente.

La evidencia local sobrevivió: 228 muestras JSON del boot `7ecbf6f6-9467-4962-9ffd-02a8b8d32d34`, ninguna línea JSON ilegible en los archivos revisados. Eventos: cuatro respuestas de 128 tokens completas; quinta petición iniciada a las **13:33:39.053872 CEST**. Última línea del servidor a las 13:33:39.078323. Se corrige así la interpretación provisional de caída durante la primera consulta: la última comprobación SSH solo alcanzó a ver esa primera consulta; el benchmark avanzó cuatro respuestas antes del corte.

Métricas: última muestra 13:33:38.266848, Tctl 61,5 °C; máximo registrado 61,75 °C. MemAvailable final 29.049.868 kB (aprox. 27,7 GiB), sin presión de memoria; contadores pswpin, pswpout y oom_kill permanecieron en cero durante el intervalo. No hubo evento de parada de la guarda térmica ni final del benchmark. No se identificaron mensajes de pánico real, error hardware, OOM, E/S ni apagado térmico en el journal del kernel anterior. Las líneas de registro de soporte `drm panic` son mensajes normales de inicialización, no un pánico ocurrido. No hay vmcore; solo archivos auxiliares de kdump del nuevo arranque. SMART: ciclos 169, atributo 192=115 frente a 168/114 antes de la prueba; pendientes, no corregibles y CRC en cero.

Copia preservada en el propio lab: `/var/log/lab-investigation/incident-20261021-133339/`, con métricas del boot anterior, copia del benchmark y journal completo. El registro rodante sigue activo.

Conclusión de fiabilidad alta: esta ejecución de inferencia con LFM2.5-1.2B y ocho hilos coincidió con un apagado físico unos 19 s después de iniciar el servidor, sin Donna activa y con C-states Disabled. No requirió cargar 13 modelos, agotar RAM ni alcanzar la temperatura de CPU de la prueba OpenSSL previa. Inferencia como desencadenante: evidencia fuerte en esta ejecución, pero una sola reproducción no establece determinismo ni identifica componente causal. Alimentación/placa/protecciones/firmware y fallo duro del sistema siguen abiertos. La ausencia de vmcore no los separa. Que el motor haya arrancado y permanecido varios minutos activo tras recuperar tampoco demuestra que inferencia estuviera ejecutándose en ese nuevo arranque.

Siguiente comparación propuesta, aún no ejecutada: mismo modelo, preguntas y configuración, cambiando únicamente los hilos de generación y batch de ocho a uno. Examina dependencia del paralelismo/carga, no demuestra por sí sola un fallo de fuente o de CPU. No volver a ejecutar el benchmark completo de 13 modelos para esta comparación.

### Comparación con un hilo iniciada, 21-oct 14:34

Tras autorización de Fran se inició a las 14:34:47 `lab-model-repro-1thread.service`, unidad transitoria, con el mismo LFM2.5-1.2B, diez preguntas y límites anteriores. Únicos parámetros de carga cambiados: `-t 8 -tb 8` a `-t 1 -tb 1`; se omiten los modelos posteriores para centrar la comparación. Contexto 2048, máximo 128 tokens por consulta, CPU, puerto 18185, MemoryMax=12G, swap prohibida, guarda 90 °C y máximo una hora conservados. Donna y principal inactivos; registrador local activo; cpuidle none; Tctl inicial 41 °C. Pi en mantenimiento una hora desde 14:34.

El script remoto original `/usr/local/sbin/lab-model-repro` apareció con tamaño cero tras el corte (metadatos de creación a las 13:33); no se ha determinado el mecanismo exacto. Se recuperó la copia original del controlador, comprobando sustituciones exactas; la nueva variante se escribió con fsync del archivo y su directorio. El registrador, su unidad y configuración de journald sí conservan contenido y están activos. Evidencia nueva en `/var/log/lab-investigation/benchmark-20261021-1thread/`. Resultado pendiente.

### Resultado de la comparación de un hilo, 21-oct 14:36

La variante de un hilo completó las diez consultas sin error ni apagado. Fin 14:36:13 CEST, duración 86,07 s, unidad Result=success y ExecMainStatus=0. Se recuperaron 86 muestras del intervalo; máximo Tctl **68,375 °C**. Métricas del intervalo preservadas en `benchmark-20261021-1thread/metrics-test.jsonl` para evitar pérdida por rotación. En la ejecución de ocho hilos el máximo registrado fue 61,75 °C, cuatro respuestas completas y corte al comenzar la quinta.

Interpretación: un umbral fijo basado únicamente en Tctl no explica ambos resultados. Evidencia inicial de dependencia del paralelismo/carga o del estado transitorio, todavía no prueba causal ni determinista. Un hilo modifica a la vez el trabajo simultáneo, perfil de consumo y duración de las consultas; no permite distinguir CPU/IMC/RAM, firmware, protecciones de placa ni adaptador. No se modifica BIOS y no se lanza otra repetición sin una incertidumbre concreta.

### Repetición controlada de ocho hilos, 21-oct 14:38

Autorizadas más pruebas por Fran, se repitió exclusivamente LFM2.5-1.2B con las diez preguntas, cambiando generación y batch de uno a ocho hilos, resto de guardas y parámetros iguales. Incertidumbre: reproducibilidad del corte a ocho hilos tras éxito con uno. Tctl inicial 42,875 °C, cpuidle none, Donna y principal inactivos, registrador activo. Inicio 14:38:21 CEST, `lab-model-repro-8thread-repeat.service`, archivos en `/var/log/lab-investigation/benchmark-20261021-8thread-repeat/`. Mantenimiento Pi una hora desde 14:38. Primera comprobación tras 25 s encontró SSH inaccesible; no se lanzó nueva carga ni WOL. Confirmación física y registros pendientes.

### Segundo apagado físico y comparación sin boost, 21-oct 14:41–14:43

Fran confirmó LED apagado en la repetición de ocho hilos y encendió manualmente el equipo. A las 14:41:32 se detuvieron de nuevo Donna y principal, que habían arrancado con Ubuntu; registrador activo. Evidencia del segundo fallo: modelo listo a 14:38:22.128769, primera consulta iniciada a 14:38:22.134697, ninguna respuesta completada, última línea servidor 14:38:22.158319. Solo una muestra de métricas tras iniciar la prueba, a 14:38:21.439361, Tctl 46,625 °C y MemAvailable 29.146.480 kB, swap libre y sin presión de memoria. No representa la temperatura exacta del corte. SMART 170/116 frente a 169/115; sin vmcore. Copia de métricas y journal del boot `be299310-a5c9-45e4-a25f-76483e1d0e6a` preservada dentro de `benchmark-20261021-8thread-repeat/`.

Siguiente incertidumbre: ¿puede tolerar ocho hilos con boost desactivado, separando paralelismo del régimen frecuencia/voltaje/potencia? Se usó interfaz Linux `/sys/devices/system/cpu/cpufreq/boost`, driver amd-pstate-epp activo, sin tocar BIOS. Documentación técnica: https://docs.kernel.org/admin-guide/pm/cpufreq.html y https://docs.kernel.org/admin-guide/pm/amd-pstate.html. Ocho hilos, mismo modelo y preguntas, resto de parámetros conservados. Wrapper temporal guardó estado previo y lo restauró en finally; no se instaló una política de boost permanente.

Prueba sin boost: inicio 14:42:46 CEST, fin 14:43:29, duración 42,59 s. Diez respuestas completas, sin errores ni apagado; systemd Result=success / ExecMainStatus=0. 43 muestras, máximo Tctl 49,75 °C. `boost=0` redujo los límites de políticas de 4.093.151 kHz a 2.000.000 kHz; máximo observado por muestreo 1.997.006 kHz (no captura cada transición instantánea). Se verificó secuencia boost 1→0→1. SMART siguió 170/116. Datos en `/var/log/lab-investigation/benchmark-20261021-8thread-noboost/`, incluye estado de boost, frecuencias y métricas preservadas.

Interpretación: dos apagados con ocho hilos y boost habilitado, una pasada completa de un hilo con boost habilitado y una pasada de ocho hilos sin boost. Evidencia moderada de dependencia del régimen de carga/frecuencia; evidencia insuficiente para identificar adaptador, VRM/placa, firmware o estabilidad de CPU/memoria. La prueba sin boost también cambia duración, temperatura y perfil de transiciones: no aisla un componente físico. No declara solucionados los apagados espontáneos en reposo ni demuestra una causa común con los arranques tempranos fallidos. No se envió WOL durante estos cortes: falta de recuperación automática en mantenimiento no es un nuevo fallo de WOL.

### Inferencia sostenida sin boost, 21-oct 16:06–16:11

Fran autorizó prolongar la prueba sin boost. Preflight 16:05:45: registrador activo, Tctl 41 °C; Donna y principal estaban de nuevo activos, por lo que se detuvieron antes de iniciar la carga para conservar la comparación. Mantenimiento Pi ampliado una hora. Se ejecutó `lab-model-repro-noboost-sustained.service`, unidad transitoria, misma configuración de ocho hilos, contexto 2048 y preguntas sintéticas repetidas durante 300 s; límite systemd 420 s, MemoryMax=12G, sin swap y guarda 90 °C.

Inicio 16:06:00, inferencia terminada 16:11:02, fin tras descarga/espera 16:11:12. **96 consultas completas**, 301,05 s desde model_ready hasta última respuesta, 312,18 s totales. Sin errores ni apagado; Result=success y ExecMainStatus=0. 312 muestras preservadas, máximo Tctl **55,875 °C**. Frecuencia máxima observada por muestreo 1.998.693 kHz. Se verificó boost 1→0→1 y restauración exacta de todos los scaling_max_freq previos. No hay procesos llama-server restantes, Donna y principal inactivos; registrador sigue activo. No se encontraron mensajes kernel de error hardware, OOM, E/S o temperatura crítica durante el intervalo consultado.

SMART al terminar: ciclos 171 y atributo192=116. El ciclo adicional respecto a 170 corresponde al intervalo entre sesiones; no se tomó un nuevo SMART inmediatamente antes de esta prueba, por lo que no se asigna su instante. El contador192 sigue igual al de la recuperación anterior. Los datos de ejecución confirman terminación continua sin reinicio durante esta prueba. Evidencia en `/var/log/lab-investigation/benchmark-20261021-8thread-noboost-sustained/`, con métricas preservadas, eventos, logs del servidor, estado de boost y frecuencias.

Conclusión: se refuerza la asociación entre boost/paralelismo y cortes, sin identificar componente causal. Dos pruebas sin boost completadas, la segunda con cinco minutos de inferencia continua, no garantizan estabilidad prolongada ni explican aún los cortes en reposo o arranques fallidos. El boost fue restaurado para no introducir un ajuste permanente no validado.

### Mayor paralelismo sin boost: 16 hilos, 21-oct 16:13–16:19

Ante petición de aumentar carga se cambió únicamente el número de hilos de generación/batch de ocho a dieciséis, manteniendo boost desactivado y cinco minutos de inferencia continua, modelo, preguntas, contexto y límites. Unidad transitoria `lab-model-repro-16thread-noboost.service`, RuntimeMaxSec=420, MemoryMax=12G y swap prohibida. Tctl de inicio 44,75 °C tras esperar reposo <=45 °C. SMART antes 171/116; registrador activo; Donna y principal detenidos; mantenimiento Pi una hora desde 16:13.

Resultado: **81 consultas completas**, 302,30 s desde model_ready a última respuesta, 313,43 s totales, fin 16:19:11 CEST. Sin errores, sin apagado, Result=success y ExecMainStatus=0. 313 muestras preservadas, máximo Tctl **57,0 °C**. No aparecen mensajes kernel de error hardware, OOM, E/S o temperatura crítica en el intervalo consultado. SMART después 171/116, sin cambio. Boost restaurado 1→0→1 y límites de políticas exactamente iguales a los anteriores. Sin procesos llama-server remanentes; registrador activo, Donna y principal detenidos. Datos en `/var/log/lab-investigation/benchmark-20261021-16thread-noboost-sustained/`.

Comparación: ocho hilos sin boost, cinco minutos, 96 consultas y 55,875 °C; dieciséis hilos sin boost, cinco minutos, 81 consultas y 57 °C. Más hilos no aumentaron el rendimiento de esta carga. Esto refuerza que el paralelismo por sí solo no explica los cortes observados con boost habilitado, pero no descarta un defecto dependiente de frecuencia/voltaje/potencia, firmware, CPU/IMC/RAM o placa/adaptador. No demuestra reparación ni explica cortes en reposo/WOL.

### Pruebas específicas de disco y RAM, 21-oct desde 16:33

Fran pidió priorizar herramientas específicas. Se detuvo la lectura personalizada parcial (no completó dos pasadas; no se declara resultado de integridad global) y se instaló fio 3.41 desde Ubuntu. Se ejecuta suite secuencial, sin cargas de modelos, con memtester 4.7.1 ya instalado. Unidad transitoria `lab-component-tests`, MemoryMax=20G, MemorySwapMax=0, LimitMEMLOCK=infinity, RuntimeMaxSec=1250. Boost conserva valor 1; no se cambia BIOS. Guardas de supervisor: CPU 90 °C, SSD 60 °C y límites por fase. Registros locales en `/var/log/lab-investigation/component-tests-20261021/`, sincronizados cada segundo.

Fio sobre `/dev/sda`, `--readonly`, `--direct=1`, libaio: lectura secuencial de 1 MiB, profundidad16 durante120s; luego lectura aleatoria4KiB, profundidad32 durante120s. Ambas fases exitcode0 y fio error0, cero bytes de escritura. Secuencial leyó35,326GiB a301,38MiB/s; aleatoria6,077GiB y13274,86IOPS. Sin apagado; SSD observado31–34°C durante disco. No es escaneo completo de toda la superficie, verificación de hashes ni prueba de escritura/retención: no descarta todos los fallos del SSD.

RAM: `memtester 16G 1`, reserva y mlock de17.179.869.184bytes confirmados, sin usar /dev/mem. Tiempo máximo900s. Resultados iniciales OK en Stuck Address, Random Value, XOR, SUB, MUL, DIV, OR, AND y Sequential Increment. Block Sequential en curso; pasada todavía no completa. Se consultaron README y README.tests del paquete, fuente primaria local del autor. Memtest86+8.00 está disponible en Ubuntu, pero no instalado ni arrancado en esta sesión.

Resultado RAM acotado: parada prevista a16:52:12 CEST, 902,53s, por límite900s. Se enviaron SIGTERM y se verificó ausencia de procesos memtester/fio restantes. El estado systemd failed/ExecMainStatus=2 procede del supervisor al interrumpir por tiempo; **no es el código2 de un memtester que detectó RAM defectuosa**. Memtester terminó por señal15, no completó la pasada. Log sin FAILURE: OK en Stuck Address, Random Value, XOR, SUB, MUL, DIV, OR, AND, Sequential Increment, Solid Bits, Block Sequential y Checkerboard; Bit Spread en curso al detenerse, otros patrones pendientes. No se descarta RAM.

Se preservaron1137muestras de toda la suite; máximo Tctl73,375°C, sin apagado y boost activo. SMART final171/116, pendientes/no corregibles/CRC0, SSD39°C. Sin nuevos mensajes kernel de error hardware, OOM, E/S o temperatura crítica en el intervalo revisado. Instrumentación continúa activa. Fuente de interpretación memtester: README.tests del paquete; fio: https://fio.readthedocs.io/en/latest/fio_doc.html.

Conclusión: SSD toleró los dos patrones de lectura sin errores reportados (cobertura parcial, sin escritura ni verificación de datos esperados); RAM toleró16GiB de patrones durante15min sin error encontrado, pero pasada incompleta. No equivale a descartar almacenamiento o memoria. Aporta además que una carga específica de memoria con boost habilitado no reprodujo el corte de inferencia en ese intervalo. Antes de nuevas comparaciones de modelos, priorizar una pasada completa de memoria, preferiblemente Memtest86+ fuera de Ubuntu, para mayor cobertura de RAM.

### Preparación de Memtest86+ fuera de Ubuntu, 21-oct 17:12

Fran conectó monitor y teclado tras decidir mover el lab al rack con SAI; ubicación eléctrica actual pendiente de confirmación explícita. Ubuntu accesible a17:12:20, UEFI, SecureBoot disabled ya previamente (no se cambió). Donna y principal detenidos, registrador activo, cpuidle none. Se instaló paquete oficial Ubuntu memtest86+8.00-3, generó entradas de GRUB. Se verificaron binario `/boot/mt86+x64`, entrada x86_64 UEFI ID `memtest86+`, y `grub-script-check` correcto.

Se seleccionó exclusivamente próximo arranque mediante `grub-reboot memtest86+`, verificando next_entry, seguido de journal sync y sync. Mantenimiento Pi ampliado cuatro horas a17:11:31 (hasta21:11 CEST). Se avisó con20s de antelación del reinicio. Objetivo: una pasada completa inicial de RAM fuera de Ubuntu, manteniendo BIOS; monitor necesario para confirmar progreso/errores. No afirmar test arrancado hasta confirmación visual.

### Memtest86+ confirmado visualmente: una pasada completa sin errores

Fran aportó fotografía de Memtest86+v8.00 con banner verde PASS, Status:Pass, contador Pass:1, Errors:0, tiempo1:24:32, memoria31.4GB, SMP16T(PAR), CPU8cores/16threads y dos slots16GB. Parte superior muestra Pass62%, Test9%, Test#9 Modulo20 random pattern: compatible con que la herramienta siguiera otra pasada después de completar la primera; no se contabiliza una segunda pasada completa. Pantalla CLK/Temp1996MHz y77/87°C, sin convertir esa lectura en telemetría Linux ni asumir estado efectivo del boost a partir del reloj mostrado.

Conclusión de fiabilidad alta: al menos una pasada completa de Memtest86+ sin errores detectados en la memoria examinada, equipo encendido al tomar la foto. Reduce sospecha de defectos reproducibles con esos patrones y condiciones; no descarta errores intermitentes, IMC/CPU/placa ni fallos dependientes de carga/frecuencia. El banner PASS no es evidencia de que los apagados estén resueltos. Pendiente salida de Memtest y nuevo acceso a Ubuntu; no se ejecuta otra carga automáticamente.

### Modo de diagnóstico persistente reforzado, 21-oct 18:50

Fran solicitó registrar futuros cortes para consultar tras recuperar Ubuntu. Estado verificado a18:48: journal persistente SyncIntervalSec=1s/4GiB activo, kdump ready, flightrecorder activo, Donna y principal activos. No se provocó carga ni corte. No existe un interruptor genérico LOG_LEVEL de Donna según revisión del código local; no se añadieron cambios al código de aplicación ni se tocaron cambios ajenos. Las métricas existentes permiten observar llamadas en curso y contadores al ser publicados; no constituyen una traza exacta de cada recepción Telegram y una petición subsegundo podría quedar entre muestras.

Se sustituyó atómicamente `/usr/local/sbin/lab-flightrecorder` por esquema2, guardando copia `/usr/local/sbin/lab-flightrecorder.before-debug`; se sincronizaron archivo/directorio. Mantiene recursos y procesos, añade cada segundo: boost, frecuencias de16políticas, temperaturas/potencia expuesta por hwmon, métricas Prometheus de8081/8082 con etiquetas eliminadas y suma por nombre (sin buckets), cgroups de secretary/telemetry/llama (memory.current, memory.events, cpu.stat, pids.current). HTTP local timeout250ms y límite512KiB por endpoint. No se registran cuerpos HTTP, claves, argumentos de procesos ni contenido de conversaciones por esta instrumentación. No se habilitó verbosity debug de llama.cpp porque puede volcar información sensible y alterar carga.

Carencia corregida: motor escribe en `/var/log/llama-cpp/*.log` fuera del journal. El registrador ahora llama fsync sobre esos archivos mediante descriptor readonly, confirmado compatible en Linux, cada iteración. No cambia destino de logs ni reinicia inferencia. Mantiene fsync por muestra, journal por segundo y límites previos32MiB×8. Al iniciar guarda hasta8MiB finales del boot anterior, si están disponibles en metrics activo, en `boot-recovery/<boot-id>.jsonl`, conservando16archivos de recuperación. Esto no garantiza conservar todas las muestras ante un corte o rotación; los incidentes explícitamente preservados no se borran.

Validación empírica: servicio reiniciado activo/enabled; muestras schema2 avanzan; endpoints8081/8082 responden; fsync de logfile principal OK;16frecuencias; cgroups presentes y sin OOM; archivo previo de recuperación creado; marcador journal persistent-debug-recorder-v2-enabled recibido y journal sync ejecutado. Estado final Donna/principal/telemetry activos, boost1, BIOS sin cambios. No se ha forzado otro corte para probar supervivencia del esquema2. Los datos de aplicación están agregados por nombre de métrica: no afirmar correlación exacta de todas las fases.

Reversión del refuerzo: restaurar únicamente la copia before-debug al script y reiniciar lab-flightrecorder; el journal/kdump previos no cambian. Un corte de raíles puede no emitir ningún mensaje; fsync no elimina la posibilidad de pérdida en caché/firmware del SSD.

### Servicios de Donna restaurados, 21-oct 18:53

A petición de Fran se ejecutó enable--now para lab-secretary, llama-cpp-main, llm-telemetry, lab-whisper y lab-piper. Piper estaba deshabilitado/inactivo y quedó habilitado/activo; los demás ya estaban activos. Se verificó router actual de dos modelos, preset `/etc/llama.cpp/models.ini`, models-max2, main=LFM2.5-1.2B y qwen-eval=Qwen3-4B, ambos load-on-startup y estado loaded. Los antiguos llama-cpp-qwen-eval y router-eval independientes permanecen deshabilitados para no duplicar motores. No se cambió selección de modelos ni BIOS/boost.

Verificación: ambos modelos respondieron a una solicitud sintética de hasta4tokens con HTTP200 y3tokens de salida (main0,59s; qwen0,38s), usando credencial existente solo en memoria sin exponerla. Los intentos iniciales sin autorización recibieron401; no se confundieron con un fallo del modelo. Health Donna8082 y OpenWebUI3080 HTTP200. Todos los cinco servicios enabled/active al terminar; flightrecorder activo. No se envió mensaje Telegram desde esta sesión.

### Informes de Claude y reproducción con nueva instrumentación, 21-oct 20:35

Se leyeron los informes de `/srv/ai-lab/coordination/informes/2026-10-21-{claude-debug-apagados,claude-spec-radiografia-fallos,prompt-codex-radiografia}.md`. El informe más reciente indica cambios ya aplicados directamente por Claude. A20:34 se verificaron los seis sysctl de panic a1, nmi_watchdog1, kexec_crash_loaded1, rasdaemon/kdump/flightrecorder activos y watchdog hardware SP5100TCO alimentado por systemd, RuntimeWatchdog30s, Timeleft27s y BOOT-STATUS0 antes de prueba. SHA flightrecorder31ddd0f30a9603c9… y exporter4b82d78474552400… coinciden con informe. Claves GPU/watchdog/UPS presentes en muestras actuales; ras-mc-ctl sin errores hardware registrados, salvo contadores de señales que no se interpretan como MCE. No se modificaron estos ajustes.

Correcciones críticas a informes de Claude: caída14:38 no fue demostrada en reposo: la muestra precedía a primera consulta de inferencia; registro del servidor muestra su inicio y termina acto seguido. Ausencia de vmcore/pstore no descarta un pánico ni demuestra hardware. Un reinicio cercano a30s por sí solo no prueba causalidad del watchdog: contrastar BOOT-STATUS y kernel. Nuevos watchdog/panic cambian comportamiento, no son observación pura. El informe pide codificar seis cambios en rol existente; esa reconciliación de repo queda pendiente y no se presenta como hecha. No se enviaron mensajes a Claude sin autorización explícita de comunicación.

Reproducción justificada: una pasada del mismo LFM2.5-1.2B, ocho hilos generación/batch, diez preguntas, boost1, BIOS igual, ahora con captura de bloqueos/RAS/watchdog activo. Se avisó20s antes. Pi mantenimiento1h (sin WOL automático), Donna y router detenidos temporalmente. Inicio20:35:05CEST, Tctl40,25°C, SMART177/120. Unidad `lab-model-repro-post-radiografia`, RuntimeMaxSec180 y resto de límites originales. Datos en `/var/log/lab-investigation/benchmark-20261021-post-radiografia/`.

Primera comprobación tras20s: SSH timeout. Tras esperar40s más seguía inaccesible; a20:36:36 Pi confirmó ping sin respuesta. Esto no demuestra aún ausencia de POST, reinicio breve o apagado físico. Confirmación del LED y recuperación de registros pendientes. No se encadenaron pruebas ni se envió WOL. Servicios principales habilitados al arranque; no confundir detención temporal para benchmark con deshabilitación permanente.

### Recuperación de la reproducción instrumentada, 21-oct 20:45

Fran indicó equipo nuevamente encendido; Ubuntu boot20:44, acceso20:45:12. El benchmark completócinco respuestas y comenzósexta a20:35:22.792787CEST; última línea servidor20:35:22.858021. Se preservaron23muestras del intervalo20:35:00–20:35:22, último20:35:22.027500, boot`c35ec85c-f9c8-44e3-86fa-3af89c192a61`, en `benchmark-20261021-post-radiografia/metrics-test.jsonl` y journal completo en `journal-boot.log`. MáximoTctl59,875°C, última59,875°C; frecuencias últimas alcanzanaprox4GHz, boost1. Watchdog último stateactive/timeleft17s (previas21,20,19,18), por lo que estaba armado en el boot que falló y aún no había expirado en última muestra. GPUbusy19–20%: actividad, no evidencia de fallo. PSI memoria0, I/O avg10~0,28%, no saturación demostrada. Último texto NUT: nut_up1, OL1/OB0, input/output240V, load5%, batería100%; es telemetría del archivo exportado, sin precisión eléctrica suficiente para descartar transitorios DC.

Sin vmcore ni registros pstore encontrados. RAS sin MCE/AER/erroresdisco registrados; múltiples eventos SIGNAL son señales de procesos, no se presentan como errores de hardware. EDAC no disponible según informe previo: ausencia de errores de memoria RAS no descarta RAM. SMART178/121 frente177/120 antes de prueba. Kernel final solo contiene AppArmor DENIED de herramienta who anterior a carga, sin panic/OOM/MCE/lockup nuevo registrado en tramo final consultado.

Hallazgo de persistencia: después del nuevo arranque `/dev/watchdog0` y `/sys/class/watchdog` no tienen dispositivo. RuntimeWatchdogUSec sigue30s y modules-loadconf contiene sp5100_tco, pero journal de systemd-modules-load confirma `Module sp5100_tco is deny-listed (by kmod)`. `/usr/lib/modprobe.d/blacklist_linux_7.0.0-38-generic.conf` (y versión34) contiene blacklistsp5100_tco junto a otros watchdogs. La activación manual previa no equivalía a una configuración persistente validada. No se modificó blacklist del proveedor ni se reactivó módulo en esta lectura; corrección de persistencia pendiente. No es válido interpretar ausencia de BOOT-STATUS actual como prueba de no actuación del watchdog anterior.

Estado final: Donna, router de modelos, rasdaemon y flightrecorder activos; kexec_crash_loaded1. No se lanzó otra carga. Conclusión: reproducción física referida por operador y fin abrupto de inferencia repetidos, sin diagnóstico causal por software. La instrumentación demuestra condiciones y ausencia de señales capturadas, no excluye pánico no capturado ni identifica placa/CPU/alimentación.

### Nuevo apagado durante despliegue OpenSearch ML, 22-oct 00:33

Fran comunica nuevo apagado y confirma que pulsó él el botón para encenderlo.
Acceso de inspección 00:39 CEST. Boot anterior
`a137e819972c495092e6d0c0b81ff614`, iniciado 21-oct20:44:08; último journal
22-oct00:33:52, última muestra flight recorder **00:33:53.346 CEST**.
Nuevo boot `f62bf9136fe64a018b7189f31651212d`, inicio00:34:23. La Pi envió
WOL00:34:37 y registró recuperación00:34:53: ese envío es posterior al inicio
del kernel, por lo que no explica causalmente el arranque. La confirmación del
operador identifica encendido manual; no se interpreta la separación de30s como
reinicio autónomo del watchdog.

Se preservaron600muestras de los últimos10min, journal completo, kernel anterior,
logs de los tres nodos OpenSearch y su setup ML, logfile previo de llama.cpp,
archivo PCP previo y lecturas SMART/RAS. Trece archivos con manifiesto SHA-256
verificado, permisos0600, en el lab:
`/var/log/lab-investigation/incident-20261022-003353/`.
El intento inicial de journal con boot UUID con guiones fue rechazado; se corrigió
al identificador compacto y se verificó la captura exitosa.

Secuencia relevante:
- Durante el tramo previo hubo despliegue Ansible. Docker inició nodos OpenSearch
  alrededor00:32:10 y el contenedor `opensearch-ml-setup` a00:33:09.
- Setup ML comenzó registro/descarga de
  `huggingface/sentence-transformers/all-MiniLM-L6-v2` a00:33:13.
- Modelo registrado y orden de despliegue00:33:39.118.
- `opensearch-3` registra descarga de bibliotecas nativas PyTorch2.5.1 CPU
  (`libgomp`, `libc10`) a00:33:46.65–46.69.
- Última muestra00:33:53.346, unos14s tras iniciar despliegue y7s tras esos mensajes.
  El log no demuestra que terminase la descarga ni que iniciase inferencia nativa.
- No hay entradas de Donna en el tramo final. Último `telegram_reply_sent`22:09:19.
  En600muestras, cero requests de inferencia en vuelo observados; CPU acumulada
  de llama.cpp aumenta solo13.436microsegundos durante el último minuto.
  Muestreo de1s no excluye una petición más breve entre muestras.

Última muestra: Tctl52,5°C, máximo66,75°C en10min; MemAvailable19.596.120KiB
(18,69GiB), swap completamente libre, PSI memoria avg10=0. OOM/oom_kill0 en
cgroups de secretario, telemetría y llama.cpp; no equivale a medir todos los
contenedores. CPU global útil6,58% e iowait5,05% en último minuto, calculadas por
diferencias de /proc/stat. PSI E/S avg10 some6,22/full6,19%, sin error de E/S
registrado. No había saturación global de CPU/RAM demostrada.

Boost1, última frecuencia muestreada máxima4.035.967kHz. Watchdog activo,
timeleft24s en última muestra; secuencia previa muestra realimentación normal.
Bootstatus actual0. No prueba por sí solo que nunca actuase; la recuperación fue
manual según operador. Esta vez el dispositivo sí existe tras arranque y systemd
anuncia watchdog hardware30s. No se cambiaron ajustes durante esta inspección.

Sin panic/Oops/MCE/lockup/OOM/errores ATA/E/S registrados en la búsqueda kernel;
pstore vacío y sin vmcore. RAS no registra errores de memoria/PCIe/disco; no se
interpreta como exclusión de fallo físico. SMART ciclos179/atributo192=122 frente
178/121 de la recuperación anterior; sectores reasignados/pendientes/incorregibles
0 y CRC0. Atributos privados del fabricante no se interpretan como diagnóstico
sin documentación. Último export NUT online, batería100%, entrada241V/salida242V,
carga13%, sin FSD ni batería baja. No demuestra ausencia de transitorios eléctricos.

Conclusión: nuevo apagado físico referido por el operador, sin diagnóstico causal
capturado. Hay una nueva asociación concreta con inicialización de ML en OpenSearch,
independiente de una consulta a Donna/llama.cpp. No se atribuye el apagado al mensaje
OpenSearch de controlador ausente: ese error coincide con el despliegue, pero no hay
prueba de que pueda apagar el host. La asociación previa boost/carga y esta nueva
secuencia permiten diseñar una prueba de inicialización OpenSearch con boost0/1;
no se ejecutó otra reproducción en esta inspección. No se reiniciaron servicios ni
se modificaron modelos, contenedores, BIOS, boost, credenciales o política watchdog.
Servicios flight recorder, RAS, kdump, Donna y router activos al terminar.


### Corte 22-oct01:09 y activación de bitácora incremental

Durante la preparación local de checkpoints, Fran comunica otro apagado. La
bitácora nueva aún no estaba desplegada. Boot previo f62bf9136fe64a018b7189f31651212d
termina journal01:09:19; última muestra01:09:20.229782CEST, Tctl59,125°C. Nuevo boot
8886f04c83174f41a980259227b24bcd a01:11:47. Fran confirmó equipo encendido; SSH fue
inicialmente muy lento. Antes de desplegar se preservaron journal completo y
últimos10min de métricas en `/var/log/lab-investigation/incident-20261022-011504/`.
No se atribuye causa a este corte sin analizar más los datos preservados.

Se detectó reemplazo concurrente del ejecutable (fb5b5311… frenteee6cb07a… del
cambio de transporte). El modo del proceso seguíawebhook. Se guardó binario/config
privada antes de desplegar el artefacto de bitácora d5955de9…; se comprobó que todas
las fuentes Go del checkout coincidían con el snapshot compilado. Sin alterar
modelos, boost, BIOS o recorder. Registro físico incremental activo, cada checkpoint
fsync, archivos16MiB×5, metadatos sin contenido de conversación. Verificación real
con /status y /jira, más sonda HTTPS de webhook no autorizada, sin inferencia ni
mensajes externos al usuario. Sin errores de escritura. Tests de modelo bloqueado,
salida abrupta de proceso, privacidad y rotación superados; no se forzó otro corte.
Detalles: `docs/validation/2026-10-22-conversation-checkpoints/README.md`.

### Cobertura del límite de inferencia, 23-oct

Se añadió una bitácora privada y sincronizada en la pasarela activa entre Donna
y llama.cpp. Donna transmite un ID aleatorio que permite unir su secuencia de
chat con llegada, reenvío, cabeceras, primer byte, primer token y cierre en la
pasarela. El binario y la unidad están desplegados; una sonda con JSON inválido
y sin credenciales verificó escritura real y HTTP 401, sin ejecutar inferencia.
Los servicios permanecen activos. No se reprodujo un apagado durante la reunión
del operador. El backend opcional Qwen estaba inactivo y no se redirigió. Ver
`docs/validation/2026-10-23-conversation-tracing/README.md`.

### Corte tras primer chat real, 23-oct 09:48

Fran envió un mensaje por Telegram. La bitácora de Donna registra la entrada
autenticada a las 09:48:07, la persistencia de la cola y el avance correcto por
autorización, historial, memoria, conocimiento y preparación del prompt. Seleccionó
el modelo principal y llegó a `model.http.start` a las 09:48:08.472 CEST. La
pasarela de telemetría registró `proxy.request.start` a las 09:48:08.484 y
`proxy.upstream.start` a las 09:48:08.490; no hay cabeceras, primer byte,
primer token ni cierre de esa petición antes del corte. Esto localiza el fallo
durante la solicitud al motor de inferencia, sin identificar todavía la línea
interna de llama.cpp ni la causa física.

El registrador de vuelo continuó hasta las 09:48:11.321 CEST. Su última
muestra marca una petición de inferencia en curso, boost activo, temperatura
CPU 55,75 °C frente a 35 °C antes de la petición, y un aumento de unos ocho
segundos de CPU por segundo en el cgroup de llama.cpp. No aparecen OOM en los
cgroups de Donna, telemetría o llama.cpp, ni presión de memoria; el SAI reportó
alimentación de red (`OL`) y carga del 5 %. El watchdog tenía 28 s restantes
y acababa de ser alimentado. Estos datos acreditan carga nativa intensa;
no miden transitorios en los raíles de alimentación internos.

El journal del boot afectado termina a las 09:48:09 sin apagado ordenado ni
error de kernel en el intervalo final. No hay vmcore ni pstore. Hubo un boot
intermedio de 09:48:50 a 09:48:58 que terminó durante el arranque, y el boot
estable comenzó a las 09:49:53. La cola Telegram reintentó el mismo mensaje
en el boot estable: el modelo devolvió HTTP 502 durante su arranque, Donna envió
un error al usuario y marcó el trabajo como completado. No se provocó otra
inferencia. Evidencia copiada, con permisos privados, a
`/var/log/lab-investigation/incident-20261023-0948/`.

Lectura: la reproducción ya no se limita a una coincidencia genérica con el
chat; coincide con el inicio de cómputo en llama.cpp. Un segmentation fault de
un proceso Go no explica por sí solo el apagado del host ni el segundo fallo
de arranque. Siguen abiertos un fallo nativo/kernel y un problema de energía,
firmware o hardware. El antecedente de corte durante OpenSearch impide atribuir
la causa exclusivamente a llama.cpp.

### Corte con sondas nativas, 23-oct 10:38

Tras desplegar los checkpoints HTTP y las sondas de `llama_decode` y
`ggml_backend_sched_graph_compute_async`, Fran envió otro mensaje por Telegram.
La bitácora de Donna alcanzó `model.http.start` a las 10:38:03.814 CEST. La
pasarela conectó, escribió las cabeceras y terminó de enviar la petición al
motor a las 10:38:03.859; no recibió el primer byte de respuesta. La sonda
nativa registró `decode.enter` a las 10:38:03.869 (PID/TID 4719) y
`compute.enter` a las 10:38:03.880. No hay `compute.exit` ni `decode.exit` de
ese proceso. En el arranque siguiente la misma sonda registró entradas y
salidas de ambos símbolos durante el calentamiento del modelo, lo que verifica
que las sondas estaban operativas. Como bpftrace entrega eventos de forma
asíncrona, la falta de salida no prueba por sí sola la instrucción exacta que
falló; sitúa el último checkpoint duradero dentro de la primera computación
del grafo.

La última muestra del registrador de vuelo del boot afectado es de las
10:38:03.740, 140 ms antes de `compute.enter`: boost=1, sensores a 36,25 y
33 °C, SAI en red con carga reportada del 5 %, watchdog activo con 30 s
restantes y contador OOM de llama.cpp a cero. Ese muestreo es previo al pico
de cómputo, de modo que no permite descartar un transitorio posterior. El
journal termina a las 10:37:56 sin apagado ordenado ni error del kernel; el
siguiente boot comenzó a las 10:39:21. No aparecieron archivos de pstore ni
vmcore. Se guardaron las bitácoras, las métricas, el journal y el log de
llama.cpp en `/var/log/lab-investigation/incident-20261023-1038/`, sólo
accesible por root.

Telegram reintentó el mismo mensaje al arrancar. El motor devolvió HTTP 502
durante la inicialización; Donna notificó el error y completó la cola, sin una
inferencia posterior. El corte nuevo descarta el procesamiento de entrada,
enrutado y serialización de Donna como punto inmediato de fallo. No distingue
entre bug nativo, firmware, placa o alimentación: una caída eléctrica mientras
el proceso calcula deja exactamente esta secuencia de checkpoints. La relación
con boost/carga observada en las pruebas anteriores sigue siendo la pista
operativa más fuerte, sin identificación causal definitiva.

### Mitigación de prueba: boost desactivado, 23-oct

Fran autorizó desactivar boost. Se leyó `1` antes del cambio, con máximo de
política de 4.093.151 kHz. Se escribió `0` en la interfaz global del kernel:
el valor efectivo quedó en `0` y el máximo de política bajó a 2.000.000 kHz.
El gobernador continuó en `performance`. Para conservar el ajuste tras un
reinicio, se instaló y habilitó `lab-disable-cpu-boost.service` mediante
`playbooks/disable-cpu-boost.yml`, ordenada después de `cpu-tuning.service` y
`cpufrequtils.service`. El playbook verificó el valor efectivo; systemd
confirmó la unidad habilitada y activa. Donna, telemetría, llama.cpp y la
sonda nativa permanecieron activos. No se modificaron el modelo ni sus ocho
hilos. La estabilidad bajo un chat real con este ajuste aún no se ha probado.
