# Medición continua de llamadas LLM

Diseño aprobado por el operador el 11 de octubre de 2026: medir todas las
conversaciones de Open WebUI sin guardar sus mensajes ni credenciales.

## Recorrido desplegado

`Open WebUI → 127.0.0.1:8081 (llm-telemetry) → 127.0.0.1:8080 (llama.cpp)`.
Prometheus recoge `/metrics` de ambas instancias cada 15 segundos. Grafana:
https://monitor.stratecode.com/d/lab-llm-latency

La pasarela mantiene autenticación, cuerpo, códigos HTTP y streaming. No traduce
la API ni ejecuta herramientas. Solo analiza transitoriamente los eventos SSE
para identificar la primera salida; no registra contenidos, identificadores de
usuario ni claves. Las etiquetas son modelo configurado, endpoint, estado y
resultado; no provienen de mensajes. Solo escucha en loopback. systemd limita su
memoria a 128 MiB y la ejecuta con usuario dinámico. El directorio privado de
estado conserva checkpoints de diagnóstico, descritos abajo.

## Bitácora de cada petición al motor

`/var/lib/llm-telemetry/inference-steps.jsonl` registra, con fsync, la llegada
de cada POST de inferencia, el inicio del reenvío, conexión al motor, cabeceras
y cuerpo enviados, primer byte de respuesta, cabeceras recibidas, primer token
útil del streaming y cierre. Los errores antes de recibir cabeceras también
quedan marcados. Conserva el archivo actual y
cuatro rotaciones de 16 MiB. Solo contiene hora, boot ID, identificador aleatorio
de intento, etapa, resultado, código HTTP, duración y bytes recibidos. No guarda
prompt, respuesta, cuerpo HTTP, URL completa, cabeceras ni credenciales.

Donna transmite el `attempt_id` de su bitácora privada como identificador de
correlación hacia esta pasarela; la pasarela retira la cabecera antes de llamar
al motor. Las peticiones que entren por otro cliente obtienen un ID aleatorio
propio. Para reconstruir un incidente, agrupar ambos JSONL por ese ID y ordenar
por hora y boot ID. Contrastar después con las muestras del registrador de
vuelo, el journal del kernel y los logs nativos de llama.cpp. Una etapa `.start`
sin su `.end` solo identifica dónde dejó de observarse progreso; no demuestra
que esa etapa causara la pérdida de alimentación. La pasarela no puede ver el
interior del motor nativo ni los raíles eléctricos de la placa.

La bitácora de la pasarela cubre `main`. La ruta opcional `qwen-eval` usa otro
backend; mientras esté inactivo, no se declara cubierta ni se redirige.

La sonda temporal de diagnóstico nativo se despliega con
`playbooks/deploy-deep-inference-trace.yml`. Sin modificar el ejecutable de
llama.cpp, observa entradas y salidas de `llama_decode` y
`ggml_backend_sched_graph_compute_async` mediante uprobes. Registra las ocho
primeras llamadas de cada proceso y luego una de cada dieciséis, con PID,
TID, reloj monotónico, duración y resultado; nunca tokens ni prompts. Su
archivo privado es `/var/log/lab-investigation/native-inference-steps.jsonl`
(modo 0600), escrito con `O_DSYNC`. El servicio
`lab-native-inference-trace` vuelve a adjuntarse tras reiniciar el host. La
entrega desde el buffer de BPF al proceso observador es asíncrona: un corte
brusco todavía puede perder el último evento. Esta sonda añade algo de carga
y debe desactivarse tras la investigación si ya no aporta datos.

## Qué se mide

- `lab_llm_requests_total`: llamadas POST a `/v1/chat/completions` y
  `/v1/completions` terminadas, incluidas las fallidas y canceladas.
- `lab_llm_request_duration_seconds`: desde entrada en la pasarela hasta final
  del cuerpo de respuesta, incluida espera en cola. Histograma por resultado.
- `lab_llm_ttft_seconds`: primera salida no vacía de contenido, razonamiento o
  nombre/argumentos de herramienta; un evento con solo role no cuenta. Solo
  streaming. No se inventa un primer token para respuestas sin streaming.
- `lab_llm_requests_in_flight`: llamadas abiertas, también durante prefill/cola.
- Métricas nativas llama.cpp: tokens procesados/generados, tiempo de cada fase
  y llamadas activas/en cola.

Resultados: `ok`, `http_error`, `stream_error` y `cancelled` (estado sintético 499).
Un error HTTP conocido mantiene su código aunque el cliente cierre el cuerpo.
Un stream cortado antes de `[DONE]` cuenta como error. Eventos SSE superiores a
64 KiB se reenvían, pero se omiten del análisis de primer token para limitar
memoria. Los contadores se reinician con el servicio; Prometheus conserva su
histórico y `rate` trata los reinicios.

El panel muestra llamadas/minuto, fallos/minuto, p50/p95 de primer token y duración,
velocidad de ambas fases, llamadas en curso y cola. Sus percentiles se estiman a
partir de buckets y son poco representativos con pocas llamadas. La velocidad de
generación divide tokens por tiempo activo de generación, no por tiempo de reloj.

Todas las llamadas del Open WebUI desplegado pasan por 8081. Los clientes que
usen directamente 8080 lo omiten: para instrumentar nuevos clientes deben usar
`http://127.0.0.1:8081/v1`. El modelo sigue siendo único, `main`.
La etiqueta `llm_telemetry_model_label` debe actualizarse al cambiar sus pesos.

El modelo principal actual es LFM2.5-1.2B-Instruct Q4_0; contexto 16k y
streaming fijado en el perfil de Open WebUI.

Se desactiva la generación automática de títulos además de tags y seguimientos:
el único slot CPU no debe competir con tareas automáticas de la interfaz.

## Reproducción y comprobación

Con el entorno del lab cargado de la forma habitual, sin mostrar sus secretos:

```sh
./scripts/test-orchestrator-go.sh
./scripts/build-orchestrator-go.sh
ansible-playbook playbooks/deploy-llm-telemetry.yml
```

El despliegue principal y el de Open WebUI también instalan la pasarela antes de
cambiar la conexión. `llm_telemetry_enabled: false` devuelve la conexión a 8080;
la retirada del servicio no es automática.

Las pruebas cubren streaming sin buffering, eventos partidos, role vacío,
herramientas, errores HTTP, stream truncado y cancelación. Evidencias y comparación:
`docs/validation/2026-10-11-model-latency/`.

La medición de Open WebUI autenticado incluye personalización y espera en cola,
por lo que no equivale al benchmark directo con un mensaje mínimo. Antes del cambio a LFM, durante esta
validación se observó una inferencia de 8045 tokens de entrada: 242,44 s de prefill
y 12,60 s de generación. No se han almacenado sus mensajes ni atribuido esa
entrada a una causa concreta sin verificarla.
