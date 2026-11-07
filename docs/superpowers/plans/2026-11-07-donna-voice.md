# Hablar con Donna por voz: qué hay, qué falta y en qué orden

Estado: desglose del 7 de noviembre de 2026, sin implementar. Se apoya en mediciones
hechas en el lab ese mismo día.

## Objetivo

Poder hablar con Donna y oírla responder, sin escribir. Todo local (sin servicios de voz
en la nube), con el mismo carácter, memoria y permisos que por texto.

## Lo que ya existe

| Pieza | Estado |
|---|---|
| **Reconocimiento (STT)** | `whisper.cpp` v1.9.5 como `lab-whisper` en `127.0.0.1:8083`, modelo `small` q5_1, 4 hilos. Funciona: Telegram ya acepta notas de voz de hasta 120 s. |
| **Síntesis (TTS)** | Piper `es_ES-sharvard-medium` como `lab-piper` en `127.0.0.1:8084`. Desplegado y activo, pero la salida de voz de Telegram está desactivada (`lab_secretary_voice_enabled = false`). |
| **Código** | `internal/secretary/speech.go` ya tiene `Transcribe`, `Synthesize` y la conversión con ffmpeg (OGG/Opus ↔ WAV). |
| **Entrada** | Telegram (webhook, con lista de permitidos) y Open WebUI (`v0.12.2`, solo administrador). |
| **Carácter y respuestas cortas** | Hechos esta semana: respuestas de unas 2 frases y resúmenes compactos, lo que ayuda mucho a la voz. |

Lo que falta es un **canal de voz directo**: hoy la voz entra por Telegram y sale como texto.

## Mediciones en el lab (CPU Ryzen 7 7730U, sin GPU)

Voz sintetizada con Piper y reconocida después, para aislar la latencia. **El audio es
limpio y sin ruido, así que la precisión es optimista**; hace falta repetirlo con voz real.

### Síntesis (Piper)
| Texto | Audio | Tiempo | Factor (menor es mejor) |
|---|---:|---:|---:|
| ~2 s | 1,5 s | 0,6-1,2 s | 0,4-0,8 |
| ~8 s | 8,9 s | 2,9 s | 0,33 |
| ~20 s | 17,3 s | 5,4-6,3 s | 0,31-0,37 |

Sintetiza más rápido de lo que se reproduce, así que por frases se puede empezar a hablar
casi de inmediato (~1 s) y mantener la delantera.

### Reconocimiento (Whisper), la mayor palanca
| Configuración | Frase corta | Frase de ~10 s | Frase de ~17 s | Notas |
|---|---:|---:|---:|---|
| `small`, actual (ventana completa) | 5,4 s | 5,9 s | 6,5 s | coste casi fijo: procesa 30 s siempre |
| `small` + `--audio-ctx 768` | 2,9 s | 3,3 s | 3,7 s | mitad de tiempo, misma precisión aquí |
| `base` + `--audio-ctx 768` | **0,8 s** | **1,0 s** | **1,1 s** | casi igual de preciso en estas frases |

Con un prompt de vocabulario («Jira, Granola, Donna, PMI, nombres del equipo»), `base`
acertó «Jira» y `small` siguió oyendo «Fira». Es una mejora gratuita.

## Presupuesto de latencia de un turno hablado

Pregunta de unos 5 s. Tiempo hasta oír el primer sonido de la respuesta:

| Tramo | Hoy | Optimizado |
|---|---:|---:|
| Detectar que has terminado de hablar | 0,7-1 s | 0,5-0,7 s |
| Transcribir | 5,5 s | 1-3 s |
| Donna (ruta compacta de Jira, o LLM con la caché de prefijo) | 3-8 s | 3-6 s |
| Primera frase hablada (Piper) | 1,2 s | 1,2 s |
| Red y conversión de audio | 0,3 s | 0,3 s |
| **Total** | **~11-16 s** | **~6-11 s** |

Con un **acuse inmediato** («Dame un segundo»), grabado una vez, la espera se siente
más corta aunque el total no cambie.

**Conclusión:** una conversación fluida y bidireccional (menos de 2-3 s) no es posible
con esta CPU y un LLM de 1,2 B. Una conversación tipo *walkie-talkie* de 6-11 s, sí.
Para la fluidez completa haría falta otro hardware (GPU) o modelos más pequeños y menos
precisos. Los modelos de 4B ya descartados empeorarían el LLM de 14 s a más de un minuto.

## Opciones de interfaz

| Opción | Qué es | Esfuerzo | Encaja si… |
|---|---|---|---|
| **A. Telegram con respuesta hablada** | Activar la salida de voz que ya existe: Donna contesta con una nota de voz. | **~0,5-1 día** | Quieres hablar desde el móvil, estés donde estés. Es asíncrono por naturaleza. |
| **B. Modo llamada de Open WebUI** | El navegador detecta tu voz, y Donna contesta hablando, frase a frase. | **~2 días** | Quieres hablar desde el ordenador, con micrófono y altavoz. Casi una llamada. |
| **C. Cliente propio** (aplicación web o pulsar-para-hablar en el Mac) | Un cliente ligero que usa los mismos puntos de entrada de voz. | ~2-4 días | Quieres un atajo de teclado para hablar sin abrir el navegador. |
| **D. Llamada telefónica o WhatsApp de voz** | Telefonía SIP o canal de terceros. | Alto y con coste | Más adelante, si de verdad lo necesitas fuera del móvil y del ordenador. |

La **opción B** es la que más se parece a «hablar con Donna directamente». Open WebUI ya
trae la detección de voz, el micrófono y la reproducción por frases. Solo necesita dos
puntos de entrada compatibles con OpenAI que apunten a nuestros servicios:

- **Voz a texto:** `whisper-server` admite `--inference-path`, así que puede responder en
  `/v1/audio/transcriptions` sin código nuevo.
- **Texto a voz:** Piper no habla ese formato; hace falta un traductor pequeño. Lo más
  sensato es añadir `/v1/audio/speech` a `lab-secretary`, que ya tiene el cliente de Piper,
  ffmpeg y la autenticación, y de paso normaliza el texto para hablarlo.

## Qué hace falta, por componente

1. **Texto pensado para hablarse** (imprescindible, lo más específico de Donna).
   Quitar Markdown y emojis; leer «PMI-67» como «P M I sesenta y siete», fechas, cifras y
   siglas; no leer URLs; frases cortas y como mucho dos. El resumen compacto de Jira ya
   encaja, pero hay que añadir una versión hablada. Es código determinista y testeable.
2. **Reconocimiento afinado.** `--audio-ctx` (o `base`), prompt de vocabulario y, si hace
   falta, detección de voz de Silero para recortar silencios. Medir con voz real.
3. **Síntesis por frases y acuses pregrabados.** Enviar cada frase a Piper según llega del
   modelo, y tener un puñado de acuses y de «voy a mirar Jira» ya sintetizados.
4. **Puntos de entrada de voz con autenticación.** `/v1/audio/transcriptions` y
   `/v1/audio/speech`, con el mismo permiso que el chat (administrador mapeado al operador
   único) y sin guardar audio ni transcripciones, como ya se hace en Telegram.
5. **Turnos de palabra.** Detección de fin de frase (en el navegador, la trae Open WebUI),
   posibilidad de interrumpir a Donna y un modo de pulsar para hablar como alternativa.
6. **Recursos.** Whisper usa 4 hilos y el LLM 8, de un total de 16. Los tramos van en serie,
   así que no compiten salvo que hables mientras Donna está procesando; hay que serializar.
7. **Evaluación con voz real.** Unas 20 grabaciones tuyas, con tu micrófono, ruido
   ambiente y los nombres del equipo: tasa de error de palabras y latencia de principio a
   fin (mediana y p95). Sin esto la elección de modelo de voz es a ciegas.
8. **Privacidad y seguridad.** Todo local; HTTPS ya disponible (`chat.stratecode.com`);
   permisos de micrófono del navegador; sin retención de audio.

## Orden recomendado

| Fase | Contenido | Resultado |
|---|---|---|
| **1** (~1 día) | Texto hablado + STT afinado + salida de voz en Telegram. | Hablas a Donna por Telegram y te contesta con voz. Se puede usar ya. |
| **2** (~2 días) | Puntos de entrada de voz en `lab-secretary` + modo llamada de Open WebUI. | Conversación desde el navegador. |
| **3** (~2-4 días) | Cliente de pulsar para hablar en el Mac, interrupciones y evaluación con voz real. | Atajo de teclado y datos para elegir modelo. |
| **4** | Voz de Donna más cuidada (otra voz de Piper u otro motor), reconocimiento en continuo y, si procede, hardware con GPU. | Fluidez de conversación. |

## Decisiones que necesito de ti

1. **¿Dónde quieres hablar con ella?** Móvil (A), ordenador en el navegador (B), atajo en el
   Mac (C), o varios. Cambia el orden, no la base técnica.
2. **¿Qué voz?** Sharvard medium (la actual) con acento de España y varios locutores, u
   otra. Se puede escuchar antes de decidir.
3. **¿Cuánto tolera tu uso una espera de 6-11 s?** Si lo que quieres es una charla fluida,
   el límite es el hardware y conviene saberlo antes de invertir.
4. **¿Quieres que Donna hable por defecto,** o solo cuando le hables por voz?

## Reproducir las mediciones

```bash
# en el host, como root
python3 scripts/speech-probes/tts-stt-latency.py          # Piper y Whisper actuales
bash scripts/speech-probes/run-stt-variants.sh            # small / base, audio-ctx, vocabulario
```
