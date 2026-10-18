# Reinicios en seco del lab: investigación (19-oct-2026)

El lab se reinicia solo, sin apagado ordenado y sin dejar nada en el registro. Este documento recoge lo que se midió, lo que se descartó, lo que se ha activado para la próxima vez y cómo interpretarlo. **La causa no está determinada.**

## Qué pasa

El exportador de arranque sucio (`lab_boot_previous_clean`) y `last -x` marcan estos cortes sin explicación. El lab vuelve a arrancar solo en ~35 s desde que el BIOS tiene *Restore on AC Power Loss = Power On* (antes se quedaba apagado hasta que alguien lo encendía).

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

## Qué se descartó y con qué

- **Carga, calor o memoria** (siete de los ocho cortes): CPU por debajo del 5 %, 34 a 44 °C, 12 a 27 GiB de RAM libre, sin actividad del modelo y con tensiones normales.
- **Software y kernel:** sin errores del kernel, de máquina (MCE) ni de SATA en ningún arranque; SMART en `PASSED` con 0 sectores dañados. El problema aparece con tres versiones de kernel distintas (7.0.0-30, -34 y -38). No hay cambios de configuración ni despliegues del repo entre el 1 y el 8-oct, ni un denominador común en lo último que hacía el lab antes de cada corte.
- **Un pánico o cuelgue de software:** `kernel.panic = 0` y no hay watchdog (`/dev/watchdog` no existe), así que un pánico dejaría el equipo congelado hasta pulsar el botón. Que vuelva solo en ~35 s apunta a un **reinicio de hardware o a un corte de corriente con arranque automático del BIOS**. Matiz: un fallo duro de la CPU (triple fault) sigue siendo posible.
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
   - **No:** no fue un pánico. Apunta a hardware o a alimentación (o a un fallo duro de la CPU).
2. **Contador 192 del SSD** (`Power-Off_Retract_Count`) antes y después del corte. Primero hay que **calibrarlo**: hacer un reinicio limpio y ver si el 192 no cambia. Si es así, y en el corte sí sube en 1, el SSD vio un apagado brusco de su alimentación. No es una prueba concluyente: la placa puede hacer un reinicio completo que corte los raíles.
3. **Exportador de arranque sucio** (`lab_boot_unclean_total`) para contar los cortes y su hora.
4. Mirar si coincide con alguna operación de Ansible o con actividad en la regleta.

## Decisión de Fran (19-oct)

Si **vuelve a reiniciarse sin explicación** y los registros que ya se recogen no aclaran nada, se hace un **repaso monográfico del BIOS**: qué opciones se tocaron el 18-oct, revertirlas de una en una y observar. Con el ritmo actual (unos 4 cortes en 6 horas) bastan unas 12 horas sin ningún corte para considerar que un cambio funciona. Ese repaso incluye también la comprobación de un BIOS más nuevo del fabricante, `processor.max_cstate=1` o los estados C desactivados en el BIOS, y revertir *Restore on AC Power Loss*.

Quedan fuera por decisión de Fran: enchufar el lab directo a la pared sin SAI y cambiar la fuente de alimentación, hasta que se aborde el repaso del BIOS.

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
