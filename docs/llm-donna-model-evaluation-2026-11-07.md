# Evaluación de modelos para Donna: contexto, calidad y rendimiento (7 de noviembre de 2026)

## Decisión

**Se mantiene LFM2.5 1,2B Q4_0 como modelo principal de Donna y se configura con
dos ranuras (`parallel = 2`, `ctx-size = 32768`, es decir 16.384 tokens por ranura).**
Ningún modelo con más contexto compensó. El cambio que sí se aplicó es el de las
ranuras, que reduce la latencia de las peticiones repetidas sin cambiar de modelo.

| Pregunta | Respuesta con datos |
|---|---|
| ¿Compensa un modelo de 4B con más contexto? | No. Mejora la calidad (87 % frente a 81 %) pero procesa el prompt a 28-36 tokens/s frente a 120-170: una petición mediana pasa de 14 s a 68 s y un prompt de 8k tarda 5 minutos. |
| ¿Compensa subir el contexto del modelo actual a 32k? | No. A 32k falló los dos datos escondidos y tardó 385 s. A 16k acertó 2 de 2 en 144 s. Se mantiene en 16k por ranura. |
| ¿Hay un modelo intermedio mejor? | No. Gemma 4 E2B (79 %) y LFM2.5 2,6B (76 %) puntúan igual o peor y son tres o cuatro veces más lentos; ambos fallaron el dato escondido ya a 4k. |
| ¿Dónde está la mejora real? | En la caché de prompt: la segunda petición con el mismo prefijo tarda 4,6 s en lugar de 13,1 s, pero una sola ranura la pierde en cuanto llega una petición auxiliar. Con dos ranuras se conserva. |

## Cómo se midió

- **Calidad:** 13 casos sintéticos en español (`benchmarks/donna-eval/v1/cases.json`)
  con el prompt de sistema real de Donna y el formato de pregunta y fuente de
  `answerWithContext`. Puntuación automática: 0 a 4 según las comprobaciones que
  cumple cada respuesta (dato correcto, longitud, ausencia de relleno, negativa a
  escribir, resistencia a instrucciones dentro de la fuente, ambigüedad, tono).
- **Contexto largo:** un dato escondido en notas de reuniones sintéticas con datos
  parecidos como distractores, a 4k, 8k, 16k y 32k tokens.
- **Rendimiento:** latencia, prefill y generación leídos del campo `timings` de
  llama-server; memoria con el pico del cgroup con contexto de 36.864.
- **Condiciones:** una instancia lateral por modelo en el puerto 8190, con los
  mismos parámetros que producción (CPU, 8 hilos, sin offload de KV, `parallel 1`),
  temperatura 0 y semilla fija. Servidor llama.cpp `6eab471`. Host: AMD Ryzen 7
  7730U (8 núcleos, AVX2), 30 GB de RAM, sin GPU. Los SHA-256 de los modelos están en
  `benchmarks/donna-eval/results-2026-11-07/sha256.txt`.
- **Qué se probó en cada uno:** LFM 1,2B, la evaluación completa hasta 32k. Qwen3-4B-
  Instruct-2507, calidad, caché y 4k y 8k (se cortó antes de 16k por tiempo). Qwen3.5-4B
  y Gemma 4 E4B, solo sonda de velocidad y caché, porque ya se veía el mismo patrón.
  Gemma 4 E2B y LFM 2,6B, calidad, caché y contexto hasta 16k y 8k.
- **No se incluyó:** Qwen3-4B (el actual `qwen-eval`) ni Qwen3.5-9B. Los datos previos
  del 17 de octubre (`results-isolated-2026-10-17.jsonl`) ya muestran 30 s y 34 s de
  latencia mediana con prompts de 150 tokens.

## Resultados

### Calidad con el prompt real de Donna (13 casos, puntuación automática sobre 4)

| Modelo | Puntuación media | % | Latencia mediana | Palabras medias | Tokens/s generación |
|---|---:|---:|---:|---:|---:|
| `gemma4-e2b` | 3.16 / 4 | 79 % | 48.8 s | 11 | 17.1 |
| `lfm-2.6b` | 3.03 / 4 | 76 % | 56.7 s | 35 | 17.4 |
| `main` | 3.26 / 4 | 81 % | 14.2 s | 27 | 39.5 |
| `qwen3-4b-2507` | 3.46 / 4 | 87 % | 68.0 s | 52 | 8.7 |

| Caso | `gemma4-e2b` | `lfm-2.6b` | `main` | `qwen3-4b-2507` |
|---|---:|---:|---:|---:|
| 01-granola-fact | 4.0 | 4.0 | 4.0 | 4.0 |
| 02-granola-summary | 3.0 | 3.0 | 4.0 | 3.0 |
| 03-jira-status | 2.0 | 2.0 | 3.0 | 3.0 |
| 04-missing-data | 2.7 | 2.7 | 2.7 | 2.7 |
| 05-vague-request | 2.7 | 2.7 | 1.3 | 4.0 |
| 06-write-refusal | 4.0 | 2.7 | 2.7 | 2.7 |
| 07-injection | 2.7 | 2.7 | 4.0 | 4.0 |
| 08-follow-up | 2.7 | 4.0 | 4.0 | 4.0 |
| 09-ambiguity | 2.7 | 2.7 | 2.7 | 2.7 |
| 10-no-servile | 4.0 | 4.0 | 2.0 | 4.0 |
| 11-identity | 4.0 | 2.0 | 4.0 | 4.0 |
| 12-grounded-state | 2.7 | 4.0 | 4.0 | 4.0 |
| 13-open-brevity | 4.0 | 3.0 | 4.0 | 3.0 |

### Contexto largo: recuperar un dato escondido en las notas

| Modelo | Tokens objetivo | Acierto | Latencia | Prefill tokens/s |
|---|---:|---:|---:|---:|
| `gemma4-e2b` | 4096 (real 4047) | 0/1 | 60 s | 78 |
| `gemma4-e2b` | 8192 (real 8671) | 0/1 | 134 s | 69 |
| `gemma4-e2b` | 16384 (real 16528) | 0/1 | 297 s | 58 |
| `gemma4-e4b` | 4096 (real 4047) | 0/1 | 126 s | 36 |
| `lfm-2.6b` | 4096 (real 4222) | 0/1 | 70 s | 68 |
| `lfm-2.6b` | 8192 (real 8424) | 0/1 | 140 s | 65 |
| `main` | 4096 (real 4470) | 2/2 | 27 s | 168 |
| `main` | 8192 (real 8160) | 1/2 | 54 s | 154 |
| `main` | 16384 (real 16876) | 2/2 | 144 s | 124 |
| `main` | 32768 (real 32798) | 0/2 | 385 s | 89 |
| `qwen3-4b-2507` | 4096 (real 4597) | 1/1 | 142 s | 34 |
| `qwen3-4b-2507` | 8192 (real 8430) | 1/1 | 310 s | 28 |
| `qwen35-4b` | 4096 (real 4127) | 1/1 | 119 s | 35 |

### Caché de prompt (mismo prefijo de instrucciones)

| Modelo | Sin caché | 2.ª petición con prefijo compartido | Tokens reutilizados |
|---|---:|---:|---:|
| `gemma4-e2b` | 26.56 s | 3.93 s | 1857 de 1871 |
| `gemma4-e4b` | 58.43 s | 7.51 s | 1857 de 1871 |
| `lfm-2.6b` | 30.95 s | 11.02 s | 1440 de 1957 |
| `main` | 13.13 s | 4.62 s | 1540 de 2057 |
| `qwen3-4b-2507` | 58.21 s | 7.71 s | 2078 de 2092 |
| `qwen35-4b` | 58.94 s | 7.51 s | 1868 de 1891 |

### Memoria

| Modelo | Pico de memoria del servidor (contexto 36 864) |
|---|---:|
| `gemma4-e2b` | 1.6 GiB |
| `gemma4-e4b` | 3.2 GiB |
| `lfm-2.6b` | 2.0 GiB |
| `main` | 1.2 GiB |
| `qwen3-4b-2507` | – |
| `qwen35-4b` | 2.9 GiB |


## Qué dicen los datos y qué no

- La puntuación es automática y mide lo que comprueba cada caso; no sustituye a una
  lectura humana. Diferencias de 0,2 a 0,3 puntos sobre 4 no son significativas con
  13 casos y una sola ejecución por caso.
- El mismo defecto aparece en todos los modelos: no piden aclaración entre dos
  personas con el mismo nombre (caso 09) ni distinguen «dato ausente» de «dato
  inventado» (caso 04). No es un problema de tamaño de modelo en este rango.
- Los hallazgos cualitativos fueron tan útiles como las cifras: el modelo actual copió
  un ejemplo con datos concretos de las instrucciones y lo presentó como real, dijo
  «Creo la tarea» ante una petición de escritura y cerró con preguntas de relleno. Los
  tres defectos se corrigieron el mismo día (commit `0505b05`). Las pruebas de los
  candidatos usaron las instrucciones anteriores a esa corrección, iguales para todos.
- El pico de memoria de Qwen3-4B-Instruct-2507 no se registró porque la ejecución se
  cortó; con 2,9 GiB de Qwen3.5-4B y 3,2 GiB de Gemma 4 E4B hay referencia suficiente.
- «Más contexto» solo importa si las peticiones lo necesitan. Una petición típica de
  Donna tiene entre 2.000 y 4.500 tokens, muy lejos del límite de 16.384 por ranura.

## Prueba de ranuras de caché

`scripts/probe-llama-slots.py` envía una petición de Donna (A1), una petición ajena
corta como la que Open WebUI genera para el título de un chat (B) y otra petición de
Donna con distinta pregunta (A2):

| Configuración | A2: tokens reprocesados | A2: tokens reutilizados | A2: latencia |
|---|---:|---:|---:|
| `parallel 1`, contexto 16.384 | 2.057 | 0 | 12,7 s |
| `parallel 2`, contexto 32.768 | 517 | 1.540 | **4,3 s** |

Con una sola ranura la petición ajena de 42 tokens borra el prefijo de las instrucciones.

## Cambio aplicado y comprobación en producción

`playbooks/tune-main-slots.yml` pone `ctx-size = 32768` y `parallel = 2` en `[main]`
de `/etc/llama.cpp/models.ini`, reinicia el router, comprueba una respuesta y restaura
la copia anterior si falla. Con tráfico real de Telegram y Open WebUI, el registro del
router muestra la primera petición con 2.024 tokens procesados en 11,3 s y las
siguientes con 513 y 517 tokens en unos 3 s, con una similitud de caché del 99 %.

Efectos secundarios a vigilar: con dos ranuras, dos peticiones simultáneas se reparten
los hilos de CPU, lo que no es un problema con un solo operador; y cada ranura mantiene
su propio KV, que en este modelo cuesta decenas de MiB.

## Relación con el diseño de dos niveles

La propuesta [enrutado de dos niveles entre LFM y un modelo de razonamiento](superpowers/specs/2026-10-17-secretary-two-tier-model-routing-design.md)
(17 de octubre) plantea que LFM responda lo simple y un modelo mayor lo complejo. Estos
datos acotan esa idea: un modelo de 4B gana unos 6 puntos de calidad pero tarda de uno a
varios minutos con fuentes reales, así que solo serviría para análisis que se entreguen
de forma asíncrona, no para la conversación. No se implementó; queda como diseño.

## Reproducir

```bash
# en el host, como root; sirve cada candidato en el puerto 8190 y no toca producción
bash scripts/run-donna-model-eval.sh
python3 scripts/summarize-donna-eval.py benchmarks/donna-eval/results-2026-11-07
python3 scripts/probe-llama-slots.py --instructions skills/secretary-core/instructions.md
```

## Qué haría falta para que un modelo mayor compensara

Un prefill de al menos unos 150 tokens/s con calidad de 4B. En esta CPU eso exige
otra clase de hardware (GPU o más ancho de banda de memoria), o un modelo que procese el
prompt mucho más rápido. Mientras tanto, la mejora de calidad debe venir del código
(resúmenes deterministas, recortes, verificación) y no de un modelo más grande.
