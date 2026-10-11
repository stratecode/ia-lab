# Modelos CPU: comparación real y telemetría

Medido en el lab Ryzen 7 7730U, llama.cpp, CPU, 8 hilos y contexto configurado a 16k.
Mismos cuatro mensajes en español, tres repeticiones por modelo, temperatura 0,
salida máxima 128 tokens y `cache_prompt=false`. No se fuerza igual longitud:
se conserva la respuesta real de cada modelo. Los tokenizadores son distintos.

| Modelo | Agenda: primer token / total | Resumen: primer token / total | Contexto: primer token / total |
|---|---:|---:|---:|
| Qwen3.5-4B-Q4_K_M | 1.82 s / 3.10 s | 2.25 s / 7.68 s | 19.35 s / 19.96 s |
| Qwen2.5-7B-Instruct-Q4_K_M | 2.52 s / 4.45 s | 3.00 s / 7.10 s | 45.77 s / 46.74 s |
| LFM2.5-1.2B-Q4_0 | 0.15 s / 0.42 s | 0.20 s / 0.85 s | 4.26 s / 4.53 s |

Son medianas, no percentiles de producción. Qwen2.5-7B generó unos 8 tokens/s
en mensajes cortos y ocupó alrededor de 8,5 GiB RSS con contexto reservado a 16k.
LFM generó unos 49–52 tokens/s en estos casos. La reserva VRAM no se cambió.

Calidad observada: los tres recuperan la agenda, resumen las tareas y rechazan
concluir calidad/rendimiento a partir de HTTP 200. Qwen3.5 y Qwen2.5 devuelven
exactamente ALAMEDA-73; LFM añade una frase y markdown pese a pedir solo la clave.
Cuatro casos no constituyen una evaluación general de un asistente ni de agentes.
En pruebas previas LFM falló otras formulaciones de grounding y JSON; mantener
esas evidencias y validar acciones antes de habilitar automatización.

Limitaciones de la medición: host en uso, sin afinidad ni control de temperatura.
Qwen3.5 tuvo esperas adicionales de unos 23 y 30 segundos en dos llamadas,
separadas del prefill registrado por el motor; no se han borrado esos outliers.
Una primera pasada de Qwen2.5 coincidió con inferencia de producción y se
descartó; se repitió después de esperar a que el principal estuviera libre.
El contexto Qwen2.5 varió entre 31,06 y 46,80 s; no extrapolar esa cifra a todo
tamaño de entrada. Las mediciones de Open WebUI incluyen cola y personalización.

Decisión posterior del operador: probar LFM como único principal. Se desplegó
el GGUF oficial Q4_0 fijado a revisión y SHA256, se retiraron los pesos Qwen y
se activó streaming. Las instancias comparativas y sus pesos son temporales.

La pasarela aprobada mide todas las llamadas de Open WebUI en 8081; Prometheus
recoge sus histogramas y Grafana muestra el panel Lab · LLM: llamadas y latencia.
Detalles: [operación](../../llm-telemetry-operation.md).

Fuentes primarias:
- [Qwen2.5-7B y arquitectura/contexto](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct)
- [GGUF oficial Qwen2.5-7B](https://huggingface.co/Qwen/Qwen2.5-7B-Instruct-GGUF)
- [LFM2.5-1.2B](https://huggingface.co/LiquidAI/LFM2.5-1.2B-Instruct)
- JSON, scripts y registros de despliegue de esta carpeta.

Fiabilidad: alta para las observaciones registradas; media para extrapolar
rendimiento y baja para estimar calidad general a partir de esta muestra.

## Verificación del principal LFM y streaming

Despliegue final: `deploy-lfm-final.txt` (85 tareas correctas, 0 fallos).
`lfm-principal-smoke.json` verifica conversación (0,572 s, 49,3 tokens/s),
streaming, llamada estructurada a `service_status` y recuperación exacta
ALAMEDA-73 entre 11.237 tokens: 62,216 s, principalmente prefill.
La llamada a herramientas depende de la formulación: el primer prompt
“Consulta con la herramienta…” no produjo tool_calls; la formulación
“Comprueba si llama-cpp-main está funcionando” sí. No habilita acciones autónomas.

`lfm-live-open-webui.json`: llamada SSE autenticada a la API real de Open WebUI,
primer texto en 2,173 s y respuesta completa en 2,368 s. Incluye personalización
existente y no crea historial. `lfm-live-telemetry.json`: proxy SSE con primer
texto en 0,073 s y total 0,364 s para saludo breve; respuesta no streaming y
401 comprobadas. Son pruebas puntuales, no una garantía de latencia.
`lfm-prometheus-grafana.json` verifica recogida de métricas y consultas del panel.

Streaming configurado tanto en valores predeterminados como en el modelo y
la preferencia UI del administrador mediante API soportada. La comprobación
visual del navegador integrado quedó bloqueada por un 401 de /api/config en
el contenedor temporal; no se considera una validación visual del renderizado.
El contenedor sin autenticación y sus datos se eliminan tras la prueba;
producción conserva autenticación. Evidencia: `probe-cleanup.txt`.
