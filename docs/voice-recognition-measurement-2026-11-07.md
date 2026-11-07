# Reconocimiento de voz con la voz del operador (7 de noviembre de 2026)

Medición de Whisper sobre **seis notas de voz reales** del operador, enviadas por Telegram, contra
las frases exactas que leyó (`benchmarks/voice-real/2026-11-07/references.json`). Se comparó
precisión y tiempo por clip en once configuraciones y después el número de hilos. Los audios y sus
transcripciones se borraron del lab al terminar; en el repositorio solo quedan las frases de
referencia y los resultados.

## Decisión

**Se mantiene `small` q5_1 con vocabulario y se acelera con 8 hilos.** Ningún modelo mayor
compensa, y el vocabulario es la palanca que más pesa.

| Cambio aplicado | Antes | Ahora |
|---|---:|---:|
| Whisper de llamadas y notas cortas (hasta 14 s) | 5,5 s | **3,0 s** |
| Whisper de notas largas | ~11 s | **5,9 s** |
| Precisión | 4,7 % de error | 4,7 % de error (igual) |
| Vocabulario | 10 palabras | 17 palabras, con proyectos y personas |

## Resultados

Frases: «¿Qué tengo pendiente en Jira?», «Dame detalles de la STRAT ciento sesenta y seis.»,
«¿Cómo van Valentina y Gonzalo con sus tareas?», «¿Qué dijo Gustavo en la reunión de Granola de
ayer?», «Dime el estado de la PMI sesenta y siete.» y «Gracias, Donna, eso es todo por hoy.»
Los números se comparan como cifras en ambos lados («ciento sesenta y seis» equivale a «166»).

### Modelos y ajustes

| Configuración | Error de palabras | Palabras clave | Tiempo mediano por clip |
|---|---:|---:|---:|
| small q5_1 · ventana completa · vocabulario [Telegram hoy] | 4.7 % (2/43) | 13/13 | 11.1 s |
| small q5_1 · ventana 768 · vocabulario [llamada hoy] | 4.7 % (2/43) | 13/13 | 5.5 s |
| small q5_1 · ventana completa · SIN vocabulario | 14.0 % (6/43) | 10/13 | 10.7 s |
| small q5_1 · ventana completa · vocabulario · haz 5 | 4.7 % (2/43) | 13/13 | 11.6 s |
| small q5_1 · ventana 768 · vocabulario · haz 5 | 4.7 % (2/43) | 13/13 | 5.8 s |
| small sin cuantizar · ventana completa · vocabulario | 4.7 % (2/43) | 13/13 | 8.5 s |
| base q5_1 · ventana completa · vocabulario | 14.0 % (6/43) | 12/13 | 2.9 s |
| medium q5_0 · ventana completa · vocabulario | 0.0 % (0/43) | 13/13 | 36.0 s |
| medium q5_0 · ventana 768 · vocabulario | 2.3 % (1/43) | 13/13 | 17.9 s |
| turbo q5_0 · ventana completa · vocabulario | 7.0 % (3/43) | 13/13 | 60.9 s |
| turbo q5_0 · ventana 768 · vocabulario | 11.6 % (5/43) | 13/13 | 29.7 s |

### Número de hilos de CPU

| Configuración | Error de palabras | Palabras clave | Tiempo mediano por clip |
|---|---:|---:|---:|
| small q5_1 · ventana 768 · 4 hilos [hoy] | 4.7 % (2/43) | 13/13 | 5.5 s |
| small q5_1 · ventana 768 · 6 hilos | 4.7 % (2/43) | 13/13 | 3.8 s |
| small q5_1 · ventana 768 · 8 hilos | 4.7 % (2/43) | 13/13 | 3.0 s |
| small q5_1 · ventana 768 · 12 hilos | 4.7 % (2/43) | 13/13 | 2.9 s |
| medium q5_0 · ventana 768 · 8 hilos | 2.3 % (1/43) | 13/13 | 9.4 s |
| medium q5_0 · ventana 768 · 12 hilos | 2.3 % (1/43) | 13/13 | 9.2 s |

## Qué dicen los datos

- **El vocabulario importa más que el modelo.** Sin él, el error sube del 4,7 % al 14 % y se pierden
  tres palabras clave: «Jira» sale como «Gira», «Granola» como «gran hora» y «Donna» como «Dona».
- **`small` ya es suficiente.** El único fallo persistente es «Dama detalle de la STRAT…» en lugar de
  «Dame detalles…», que no afecta al enrutado porque la clave sale bien. `medium` lo corrige a costa de
  9-36 s; `base` y `large-v3-turbo` puntúan peor y son o menos precisos o más lentos.
- **La búsqueda en haz de 5 no ayuda** con estas frases, y `small` sin cuantizar tampoco.
- **8 hilos son el punto óptimo.** El equipo tiene 8 núcleos; con 12 la mejora es de un 3 %.
- **La ventana de audio reducida** (`--audio-ctx 768`) ahorra la mitad del tiempo sin perder precisión
  en clips de hasta 14 s; los más largos van a la instancia de ventana completa.

## Incidente durante el ajuste

Con 8 hilos Whisper se cayó en producción («terminate called without an active exception»). La causa
era `TasksMax=64` en su unidad: cada petición arranca hilos de cálculo además del grupo HTTP y con 8 se
superaba el límite. Con `TasksMax=256` quedó estable: 0 reinicios tras tres rondas completas en las dos
instancias. Los valores están en `playbooks/deploy-voice-call.yml` y en los valores por defecto de
`roles/lab_speech`.

## Límites de esta medición

- **Solo seis frases** y una sola voz: una diferencia de una palabra no es significativa. La
  conclusión firme es el efecto del vocabulario y el de los hilos, no la comparación fina entre `small`
  y `medium`.
- Son frases leídas, cortas y sin ruido de fondo; una conversación espontánea, con interrupciones o con
  la voz lejos del micrófono, puede comportarse peor.
- Solo se probó el micrófono del móvil por Telegram. El modo llamada del navegador, con el micrófono del
  ordenador, queda por medir.

## Reproducir

```bash
# en el host, como root, con clips y referencias propias
bash scripts/speech-probes/run-whisper-compare.sh
bash scripts/speech-probes/run-whisper-threads.sh
```
El diagnóstico con audio real se activa con `-e voice_capture=true` en
`playbooks/deploy-voice-call.yml` (guarda los clips 24 h; apagarlo después).
