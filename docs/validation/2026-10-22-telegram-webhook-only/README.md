# Donna: recepción exclusiva por webhook

22 de octubre de 2026. Solicitud explícita del operador: activar webhook y retirar
el transporte anterior sin fallback. Alcance: bot de Donna en lab-secretary.
La Pi y el orquestador retirado son sistemas separados; no se modificaron.

Cambios:
- Modos admitidos: webhook (predeterminado) y disabled. El valor retirado se rechaza.
- Eliminado Bot.Run y el consumidor getUpdates, junto a su checkpoint de offset.
- Worker con cola persistente, deduplicación y reintento; planificador de timers
  arranca junto al worker. No hay conmutación automática de transporte.
- Actualizados valores Ansible, ejemplo de entorno y documentación; eliminado
  el playbook de restauración del modo retirado. Evidencia histórica conservada.

Verificación:
- Regresión antes del cambio: falla tanto el valor predeterminado como el rechazo
  del modo antiguo. Después pasa.
- Pruebas del worker, autorización, persistencia, deduplicación y recuperación tras
  reinicio; prueba adicional de rechazo de usuarios/grupos antes de ejecutar.
- `./scripts/test-orchestrator-go.sh`: correcto.
- `scripts/build-orchestrator-go.sh` sobre snapshot aislado del checkout: correcto.
- YAML de tareas, playbook de activación y group_vars: parseo correcto.
- `git diff --check`: correcto.
- No se alteraron los cambios preexistentes ajenos a esta migración.

Despliegue:
- Binario SHA-256:
  `ee6cb07ab23dd2ce42cbefadc85064d6be8772c4609b3a0304d7b2853af6d1fa`.
- El binario anterior coincidía con el artefacto local antes de cambiarlo; el script
  aborta si cambia de forma concurrente. Copia y sustitución atómica del binario.
- Modo webhook en entorno y drop-in con el archivo privado del secreto existente.
  No se imprimieron credenciales ni se editaron .env, vault o ssh.
- Solo se reinició lab-secretary. Modelos e instrumentación de apagados intactos.
- Prueba contra URL pública desde el Mac con validación TLS: secreto incorrecto
  401; actualización sintética de usuario no autorizado 200; repetición 200.
  El almacén CA por defecto de Python del Mac falló; se usó certifi con validación
  TLS habilitada, sin omitir verificación de certificados.
- Registro setWebhook con drop_pending_updates=false y max_connections=4.
- getWebhookInfo: URL correcta, pending_update_count=0, sin error informado.
- Estado offset retirado del directorio activo y archivado con permisos0600.
  Respaldo privado: /var/lib/lab-investigation/webhook-only-20261022/.

Límites: la prueba pública se hizo desde el Mac; no se presenta como sonda desde
una segunda red externa. El registro de Telegram confirma configuración, no prueba
por sí solo la entrega desde sus servidores. No se envió mensaje al operador desde
esta sesión. La siguiente consulta real del operador verifica el recorrido completo.
Los tests sí ejercitan aceptación HTTP, procesamiento y envío con API simulada.
Entrega at-least-once: un corte después de enviar y antes de completar puede
ocasionar respuesta duplicada; no se promete exactamente una vez.

Protección frente a artefactos concurrentes: se detectaron compilaciones ajenas
modificando el binario genérico de dist durante esta migración. No se sobrescribió.
El binario de este despliegue se conserva también como
`dist/lab-secretary-webhook-only-linux-amd64`. El rol de despliegue ahora rechaza
antes de copiar cualquier binario que aún incluya la API de recepción retirada.
Prueba empírica: rechaza el artefacto genérico concurrente y acepta el binario
exclusivo de webhook. Un siguiente despliegue debe reconstruir el artefacto genérico
con las fuentes actuales; no permite volver a introducir el consumidor retirado.

Verificación posterior al registro: URL correcta, cola de Telegram0, sin error
informado, proceso en webhook y SHA-256 desplegado conservado. La actualización
sintética figura completada, no se envió respuesta al usuario de prueba.
Se observó además una actualización real en la cola local (distinta de la sonda),
recibida después de activar webhook; no se copió su contenido privado.

Confirmación de extremo a extremo posterior: la actualización real recibida tras
activar webhook pasó de pending a completed; el journal del proceso registra
`telegram_reply_sent=1`, cero telegram_send_error, model_reply_error y
telegram_queue_error. Cola local pending0/completed2 (sonda y actualización real).
Por tanto ya existe evidencia de recepción real, procesamiento y envío aceptado por
Telegram con este transporte; la limitación anterior de entrega pendiente queda
superada para esa actualización. No se inspeccionó ni reprodujo su contenido.
Donna, router y flight recorder permanecen activos.
