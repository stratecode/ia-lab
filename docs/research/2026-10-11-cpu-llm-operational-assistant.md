# LLM en CPU para el asistente operativo del lab

Fecha: 2026-10-11. Alcance: investigación y pruebas temporales; no despliegue de un nuevo asistente. Prioridad elegida por el operador: diagnosticar el lab y ejecutar acciones controladas. Oráculo y OpenSearch quedan fuera de este proyecto.

## Decisión recomendada

Mantener llama.cpp. Comparar Qwen3.5-4B cuantizado a Q4 con LFM2.5-8B-A1B Q4 para el diagnóstico operativo. Evaluar LFM2.5-1.2B-Instruct como modelo rápido para consultas delimitadas, sin atribuirle autoridad para ejecutar acciones. No aumentar la reserva de VRAM como primer paso.

Fiabilidad estimada, no intervalo estadístico: 95% para especificaciones contrastadas y resultados registrados; 80% para la selección de candidatos; insuficiente evidencia para garantizar fiabilidad operativa de cualquiera de ellos. Dos llamadas correctas a herramientas no equivalen a un asistente validado.

## Hardware y punto de partida

Verificado por SSH: Ryzen 7 7730U, 8 núcleos/16 hilos, AVX2, RAM 30994 MiB, reserva de iGPU 512 MiB. Hay cuatro servicios llama.cpp: code, planner, utility y embeddings. Los cuatro respondieron salud HTTP 200 antes/después de la prueba. Eso verifica disponibilidad, no calidad del diagnóstico.

La memoria reservada para la iGPU procede de la RAM compartida: aumentarla puede permitir cargar más capas si falta capacidad, pero no aumenta por sí sola la capacidad de cálculo o el ancho de banda. Dado el resultado previo comunicado por el operador, conservar CPU puro como referencia y evaluar después CPU+iGPU únicamente mediante comparación reproducible. No se ha modificado BIOS, reserva de memoria ni configuración permanente.

## Motores

| Motor | Evaluación para este lab |
| --- | --- |
| [llama.cpp](https://github.com/ggml-org/llama.cpp) | Primera opción: ya funciona en el lab, admite AVX2, GGUF cuantizados y API. Su [servidor](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md) permite esquemas JSON y herramientas con plantillas apropiadas. |
| [ONNX Runtime GenAI](https://github.com/microsoft/onnxruntime-genai) | Segundo motor para una comparación posterior: CPU x64 y modelos int4. Requiere artefactos ONNX; no reutiliza directamente el GGUF. No hay prueba local de que supere a llama.cpp. |
| [BitNet / bitnet.cpp](https://github.com/microsoft/BitNet) | Interesante para investigar eficiencia extrema. Requiere modelos entrenados para esa representación; no convierte cualquier Qwen en un modelo 1,58 bits equivalente. Las mejoras publicadas no prueban ventaja frente a nuestro Q4 con la misma calidad. |
| [Ollama](https://github.com/ollama/ollama) | Facilita gestión y descarga. Usa llama.cpp; cambiar de envoltorio no demuestra una mejora de rendimiento. |
| [llamafile](https://github.com/mozilla-ai/llamafile) | Portabilidad y distribución en un archivo. No hay una ventaja de velocidad demostrada aquí frente al motor instalado. |
| [vLLM CPU](https://docs.vllm.ai/en/latest/getting_started/installation/cpu/) | Sí soporta CPU; AVX2 tiene funciones limitadas y se recomienda AVX512. Menor prioridad para este 7730U y un asistente con baja concurrencia. |

## Modelos que merece la pena comparar

| Modelo | Función propuesta y límites |
| --- | --- |
| [Qwen3.5-4B](https://huggingface.co/Qwen/Qwen3.5-4B) | Candidato principal para diagnóstico y herramientas. Arquitectura híbrida, multilingüe, Apache 2.0. Empezar con Q4 y contexto 4–8k; desactivar razonamiento mediante la opción de plantilla `enable_thinking=false` cuando corresponda. No está medido en este lab; comprobar compatibilidad de la versión instalada antes de desplegar. |
| [LFM2.5-8B-A1B](https://huggingface.co/LiquidAI/LFM2.5-8B-A1B) | Candidato alternativo eficiente: 8,3B parámetros totales, 1,5B activos. Los pesos totales siguen consumiendo memoria. Español soportado. La cifra publicada de 146 tokens/s procede de un Ryzen AI Max+ 395 y no predice el rendimiento del 7730U. [Fuente del fabricante](https://www.liquid.ai/blog/lfm2-5-8b-a1b). |
| [LFM2.5-1.2B-Instruct](https://huggingface.co/LiquidAI/LFM2.5-1.2B-Instruct) | Muy rápido para extracción y consultas delimitadas. El fabricante no lo recomienda para programación o tareas intensivas de conocimiento. Nuestra prueba detectó un fallo de diagnóstico. [Licencia propia LFM](https://huggingface.co/LiquidAI/LFM2.5-1.2B-Instruct/blob/main/LICENSE), revisar su encaje antes de adoptarlo. |
| [Qwen3-4B-Instruct-2507](https://huggingface.co/Qwen/Qwen3-4B-Instruct-2507) | Control alternativo sin razonamiento, para contrastar coste/precisión si Qwen3.5 no encaja con el motor o la latencia. |
| [Gemma 4 E2B](https://ai.google.dev/gemma/docs/core/model_card_4) | Reservar para una fase con imágenes/audio. E2B tiene 5,1B parámetros totales y 2,3B efectivos: el nombre no implica pesos de solo 2B. |
| [BitNet b1.58 2B 4T](https://huggingface.co/microsoft/bitnet-b1.58-2B-4T) | Experimento separado de consumo. Contexto 4096 y limitaciones fuera del inglés reducen su prioridad para este asistente en español. |

No añadir embeddings nuevos al primer MVP. Recuperación de manuales puede venir después; [Qwen3-Embedding-0.6B](https://huggingface.co/Qwen/Qwen3-Embedding-0.6B) es un candidato multilingüe para esa fase.

## Medición real, CPU puro

Servidor temporal escuchando solo en 127.0.0.1:18089, una petición simultánea, contexto 2048, sin capas GPU y sin KV en GPU. Warmup excluido; tres repeticiones por configuración, 96 tokens generados, caché de prefijo desactivada. Servicios normales permanecieron activos; no es un benchmark aislado. Medianas:

| Modelo / cuantización | Hilos | Prefill tokens/s | Generación tokens/s | Petición completa s | RSS MiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| Qwen2.5-1.5B / Q4_K_M | 4 | 108,29 | 33,55 | 4,557 | 1921,4 |
| Qwen2.5-1.5B / Q4_K_M | 8 | 117,00 | 33,03 | 4,524 | 1922,9 |
| LFM2.5-1.2B / Q4_0 | 4 | 157,16 | 47,60 | 3,291 | 1359,6 |
| LFM2.5-1.2B / Q4_0 | 8 | 240,68 | 47,61 | 2,849 | 1360,5 |

LFM genera aproximadamente 42% más tokens/s y usa 29% menos RSS en la comparación de 4 hilos. Distintos modelos, cuantizaciones y tokenizadores: no atribuir la diferencia solo a la arquitectura ni convertirla directamente en palabras/s. El texto de entrada fue igual, pero produjo 183 tokens Qwen y 200 LFM. Ocho hilos mejoraron principalmente el prefill de LFM, no su generación; añadir hilos no garantiza acelerar todo.

### Calidad y herramientas

- Extracción solicitando JSON por texto: Qwen añadió markdown; LFM devolvió JSON, pero `action:null` en lugar del valor solicitado `none`.
- Pregunta sobre si HTTP 200 prueba calidad: Qwen distinguió disponibilidad y calidad; LFM respondió que los datos sugerían que el asistente funcionaba bien. Fallo relevante para diagnóstico.
- Selección de herramientas pidiendo JSON por texto: ambos produjeron argumentos inadecuados.
- Con `tools` declaradas en `/v1/chat/completions`, ambos devolvieron `service_status` y la unidad correcta para planner y code: 2/2 por modelo. Las llamadas no se ejecutaron; se comprobó la salida estructurada real de la API.

Usar herramientas declaradas y validar argumentos en el backend. Un esquema válido no asegura una decisión correcta. El modelo pequeño no ha superado una evaluación suficiente para gestionar el lab por sí solo.

## Funcionalidad propuesta

Primer ciclo útil: consultar estado de unidades permitidas, CPU/RAM/disco, métricas y un número limitado de líneas de logs; resumir evidencias con hora y fuente; explicar causas probables y reconocer datos ausentes. Para reinicios: proponer unidad y motivo, exigir autorización, ejecutar una acción fija del backend, verificar salud posterior y registrar resultado.

El ejecutor debe imponer permisos y lista de unidades, independientemente de lo que genere el LLM. No dar al modelo una herramienta de shell arbitrario ni sudo general. Tratar instrucciones dentro de logs como datos. Mantener el flujo y auditoría de iniciativas del repositorio como referencia, pero verificar el encaje con `docs/architecture/master-plan.md` antes de implementarlo: el control plane no está activo ahora.

Un trabajo de inferencia prioritario a la vez durante el primer MVP. Más servidores escuchando no implican más capacidad de CPU. Contexto corto, datos filtrados, respuestas limitadas y carga bajo demanda son hipótesis a medir, no optimizaciones ya desplegadas.

## Próxima validación antes de elegir modelo

Comparar Qwen3.5-4B y LFM2.5-8B-A1B con la misma batería de 30–40 situaciones en español: servicio caído, salud engañosa, logs incompletos, memoria alta, instrucciones maliciosas en logs, argumentos inválidos y acciones no autorizadas. Medir elección de herramienta, argumentos, fundamentación, latencia p50/p95, RSS y temperatura, con 4/8 hilos y Q4/Q5 cuando proceda.

Criterios propuestos: al menos 95% de decisiones correctas en esa batería y cero ejecuciones no autorizadas, garantizadas por el ejecutor y sus pruebas. Una batería pequeña no representa garantía universal. No probar reinicios reales fuera de unidades de prueba autorizadas.

## Evidencia y reproducción

En [evidence/2026-10-11-cpu](evidence/2026-10-11-cpu/): `results.json`, `native-tools.json`, `probe.py`, `tools-probe.py`. Son sondas de investigación específicas del host y sus rutas; no un harness de producción. Ejecutadas mediante `sudo -n python3 -` por SSH. Los modelos y logs quedan en `/srv/ai-lab/llama.cpp/research/2026-10-11-cpu/`; el GGUF oficial está fijado por revisión y su SHA256 consta en el JSON. Cada proceso temporal se cerró; puerto 18089 cerrado al terminar. No se han sustituido modelos de los servicios.
