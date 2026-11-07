# Trazas duraderas del chat a la inferencia — 23-oct-2026

## Objetivo y alcance

Preparar el recorrido activo de Telegram y Open WebUI para identificar el último
paso observable tras otro corte del lab. No se forzó inferencia ni se provocó un
apagado mientras el operador estaba en su reunión. El backend opcional
`qwen-eval` estaba inactivo y no respondía en su puerto; no se cambió su ruta.

## Recorrido instrumentado

La bitácora ya existente de Donna registra la entrada autenticada, cola de
Telegram, autorización, servicio, mutex, historial, routing, conectores,
preparación del prompt, petición HTTP, cabeceras, cuerpo, respuesta y envío. El
nuevo límite de pasarela registra la llegada del POST, inicio del reenvío,
cabeceras del motor, primer byte, primer token útil si hay streaming y cierre,
incluidos los fallos HTTP y de transporte. Ambos JSONL usan el mismo identificador
aleatorio de intento; la cabecera de correlación se retira antes de llama.cpp.
Los registros no contienen cuerpos, credenciales ni texto conversacional.

Fuentes en el host:

- `/var/lib/lab-secretary/diagnostics/conversation-steps.jsonl`
- `/var/lib/llm-telemetry/inference-steps.jsonl`
- `/var/log/lab-investigation/metrics.jsonl` y logs nativos de llama.cpp
- journal del sistema y kernel, pstore y kdump si existen

El último checkpoint no es una causa demostrada: un corte físico puede impedir
registrar el siguiente evento o perder la última escritura incluso con fsync.
Para interpretar el incidente, unir los eventos por `attempt_id` de Donna y
`trace_id` de la pasarela, y cruzarlos por boot ID y hora con flight recorder,
journal y logs nativos.

## Verificación

- `./scripts/test-orchestrator-go.sh`: correcto. Se comprobó la correlación
  entre el ID del log de Donna y la cabecera del modelo, y el orden y privacidad
  de los checkpoints de la pasarela.
- `./scripts/build-orchestrator-go.sh`: correcto; incluye todas las pruebas Go
  y produjo ambos binarios Linux.
- `ansible-playbook ... playbooks/deploy-conversation-diagnostics.yml --syntax-check`:
  correcto.
- Playbook de despliegue estrecho: terminó sin fallos. Comparó previamente los
  hashes de los binarios activos para detectar reemplazos concurrentes;
  instaló Donna y la pasarela, activó el directorio privado de trazas y comprobó
  `/health` de ambos servicios sin inferencia.
- Sonda real sin credenciales con JSON inválido contra la pasarela: HTTP 401,
  sin inferencia. El archivo privado se creó con modo `0600` y contiene llegada,
  reenvío, cabeceras de error, primer byte y cierre. La segunda sonda verificó
  que las cabeceras HTTP 401 quedan marcadas como error. Donna y la pasarela
  permanecieron activas.
- `git diff --check`: correcto al cierre.

Después de esta verificación inicial, el primer chat real por Telegram apagó el
host. La secuencia y la recuperación están en
`docs/lab-reinicios-investigacion.md`, apartado «Corte tras primer chat real».

## Refuerzo para la siguiente reproducción

El incidente llegó a `proxy.upstream.start`, y el grupo de llama.cpp acumuló
unos ocho segundos de CPU por segundo hasta el corte. El log nativo rotado
`llama-cpp-main.log.1` termina con 789 bytes nulos y no conserva el detalle
de esa solicitud; se guardó copia privada junto a las otras pruebas en
`/var/log/lab-investigation/incident-20261023-0948/`.

La pasarela registra ahora conexión, cabeceras y petición completa enviadas,
primer byte de respuesta y resultado del transporte. La sonda BPF registra
entrada/salida de `llama_decode` y del cálculo ggml en un JSONL con escritura
sincronizada por evento. Ambos binarios y servicios están desplegados; no se
cambió el modelo, boost, BIOS ni el ejecutable nativo.

Validación sin inferencia: arnés Go y build correctos; compilación Python y
`bpftrace --dry-run` correctas; playbook real y pasada posterior `--check`
correctos; servicios activos. Una solicitud con credencial deliberadamente
inválida devolvió HTTP 401 y produjo todos los nuevos checkpoints HTTP, sin
decodificar tokens. La sonda escribió `observer.ready` tras adjuntarse.
Todavía falta comprobar sus eventos `decode` y `compute` con la próxima
inferencia real. Registrar la hora, canal y estado físico del LED, sin
guardar el contenido del mensaje en este informe.
