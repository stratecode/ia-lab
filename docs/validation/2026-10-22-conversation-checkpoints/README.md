# Bitácora local incremental de conversaciones

Solicitada por el operador para acotar el paso alcanzado antes de los apagados.
Estado actual: desplegado y verificado en el lab después de recuperar acceso.

Archivo en el lab: `/var/lib/lab-secretary/diagnostics/conversation-steps.jsonl`.
Rotación: 16MiB por archivo, cuatro copias anteriores, hasta80MiB nominales.
Directorio0700 y archivos0600. Cada registro JSONL se escribe y fsync antes de
continuar; se sincronizan también directorios para persistir creación y rotación.
Los fallos de escritura emiten `conversation_diagnostic_write_error` al journal;
no se ocultan ni se convierten en fallos de modelo, y no bloquean la conversación.

Campos: hora UTC, boot_id, trace_id, attempt_id aleatorio, secuencia del logger,
tiempo desde inicio del intento, evento, resultado, duración, estado HTTP y bytes.
No se guardan texto, prompt, respuesta, URL, cabeceras, claves ni mensajes de error
libres. Las solicitudes Telegram usan `telegram-<update_id>`; recepción y worker
pueden tener distintos attempt_id, conservando la correlación de actualización.
Cada reintento crea otro intento. WebUI y llamadas al servicio también se registran.

Puntos instrumentados:
- Webhook autenticado, persistencia de cola y acuse; worker, autorización y cierre
  de la actualización tras envío.
- Petición al servicio y espera de su mutex, historial, routing, clasificación
  opcional, memoria y conocimiento aprobado.
- Comandos de fuentes, capacidades, Jira (identificación y consulta), MCP Granola
  (initialize, initialized, tools/list y tools/call), renovación OAuth y lectura web.
- Preparación del prompt, modelo seleccionado, inicio/fin de solicitud, espera HTTP,
  recepción de cabeceras, fin de lectura del cuerpo y primer fragmento streaming.
- Descarga, conversión y transcripción del audio; guardado de conversación;
  llamadas Telegram, incluido cada bloque sendMessage.

Ampliación posterior: la pasarela `llm-telemetry` guarda checkpoints propios
antes y después de reenviar la petición a llama.cpp. Donna envía su `attempt_id`
aleatorio a esa pasarela, de modo que las dos bitácoras puedan unirse sin guardar
el contenido de la conversación. Véase `docs/llm-telemetry-operation.md`. Los
checkpoints de la pasarela terminan en el límite HTTP del motor; los logs nativos
de llama.cpp y el registrador de vuelo siguen siendo fuentes separadas.

Pruebas:
- Rojo: una solicitud bloqueada en el modelo no tenía archivo de pasos disponible.
- Verde: checkpoint model.request.start presente mientras el modelo sigue bloqueado;
  al terminar aparecen cabeceras, cuerpo, cierre de modelo, persistencia y envío.
- Verifica correlación por mensaje/intento, permisos y ausencia de los sentinelas
  privados de entrada, salida, token de modelo, token de bot y URL.
- Proceso hijo termina con os.Exit(23) sin ejecutar defer: sobrevive el inicio de
  la operación sin cierre. Tras reapertura se crea un attempt_id distinto.
- Rotación: cuatro copias más el archivo actual, tamaño limitado, JSON válido.
- `./scripts/test-orchestrator-go.sh`: correcto.
- `scripts/build-orchestrator-go.sh` en snapshot aislado del checkout: correcto.

Binario preparado SHA-256:
`d5955de9c636a303b52619c0b6e61da29f3513c418f412e51137f23daf08903f`.
No se sobrescribió dist genérico, que tenía compilaciones concurrentes.

Límites: la prueba de salida abrupta de un proceso no equivale a retirar físicamente
alimentación. Fsync reduce pérdida, pero no garantiza el último instante frente a
caché/firmware del SSD. Un paso abierto acota el último progreso registrado; no
atribuye la causa al componente indicado. La instrumentación introduce E/S síncrona
y puede alterar tiempos; debe cruzarse con flight recorder, kernel y otras cargas.
En webhook no se registra el cuerpo ni solicitudes con secreto inválido; el primer
checkpoint correlacionado se escribe después de validar/decodificar el update.

## Despliegue y comprobación real

Recuperación del host: nuevo boot `8886f04c83174f41a980259227b24bcd`, inicio
01:11:47 CEST. El boot anterior terminó con journal01:09:19 y última muestra
01:09:20.229782, Tctl59,125°C. Este corte ocurrió antes de desplegar esta bitácora.
Se preservó journal completo y métricas de los últimos10min en
`/var/log/lab-investigation/incident-20261022-011504/` antes de cambiar el binario.

Se detectó que otro despliegue había sustituido el binario exclusivo de webhook
por SHA-256 `fb5b5311c5a8e384ca38c00981ed8d55a371f5d825859187e10c6c65ae132228`.
El modo activo seguía siendo webhook. Se guardó ese ejecutable y configuración
privada en el incidente y se desplegó el nuevo binario con sustitución atómica.
Las fuentes Go del checkout coincidían exactamente con el snapshot compilado.
Se mantuvo modo webhook y su EnvironmentFile de secreto existente. Solo se reinició
lab-secretary; sin cambios en modelos, boost, BIOS ni flight recorder.
Artefacto independiente local: `dist/lab-secretary-conversation-logs-linux-amd64`.

Verificación real:
- `/status` HTTP200 en0,084s; `/jira` HTTP200 en0,866s. No se imprimió ni conservó
  el contenido de las respuestas en este informe. Cero peticiones al modelo en
  esta comprobación. Veinte checkpoints iniciales, con boot_id/trace_id/attempt_id.
- Etapas reales: servicio/mutex, historial, comando, petición Jira HTTP/cabeceras/
  cuerpo, guardado de conversación y cierre. Archivo con permisos0600.
- Prueba pública HTTPS del webhook: secreto incorrecto401, update sintético de
  identidad no autorizada200, duplicado200. Correlación completa de recepción,
  persistencia, autorización rechazada y queue.complete.end. Tres intentos
  distinguibles: dos entregas HTTP y un procesamiento de worker. No se enviaron
  mensajes al usuario de prueba ni se llamó al modelo.
- Journal del proceso: cero conversation_diagnostic_write_error y cero
  conversation_diagnostic_event_error. Log físico8654bytes tras las comprobaciones.

No se provocó otro apagado físico para verificar supervivencia. La prueba de
salida abrupta del proceso permanece diferenciada de la evidencia en el host.
