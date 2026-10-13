# Memoria por conversación y por día

## Objetivo

Dar continuidad a cada conversación sin mezclar canales y permitir recuperar conversaciones anteriores por tema y fecha, manteniendo el almacenamiento local de la secretaria.

## Diseño

- El contexto de corto plazo se separa por operador, canal y conversación. Conserva hasta diez intercambios y un máximo de dos horas; los límites de tamaño actuales siguen aplicándose.
- Telegram usa el identificador de chat y Open WebUI usa `X-OpenWebUI-Chat-Id`. Las API públicas existentes conservan wrappers para no romper consumidores y pruebas.
- Cada intercambio se archiva en JSONL diario, con fecha local de Madrid, canal, huella de conversación y pregunta/respuesta. Las escrituras son append-only; `/forget` borra todo el archivo nuevo y antiguo para el operador.
- El recuperador identifica periodos comunes (hoy, ayer, esta semana y semana pasada), abre únicamente sus particiones y clasifica con búsqueda léxica local. Sin embeddings en esta fase: medir precisión y cobertura antes de añadir índice o modelo.
- El archivo JSONL histórico se mantiene legible en búsquedas sin fecha durante la transición. No se reescribe ni elimina al migrar.
- El aviso de demora de Telegram pasa a 30 segundos por defecto en código y despliegue.

## Pasos y validación

1. Añadir pruebas que fallen para diez intercambios / dos horas, aislamiento por conversación, metadatos y partición diaria, recuperación por «la semana pasada», compatibilidad con historial antiguo, `/forget` y retraso de 30 segundos.
2. Implementar persistencia/recuperación y pasar los tests del paquete `internal/secretary`.
3. Actualizar configuración y documentación operativa.
4. Ejecutar harness Go del repositorio y build de distribución; revisar diff y estado Git.
5. Commit y push a `main`; desplegar solo `lab-secretary` y verificar servicio y artefacto.

## Criterios de aceptación

- Los mensajes de dos chats distintos no aparecen en el contexto del otro.
- Una conversación conserva hasta diez turnos de ida y vuelta dentro de dos horas y descarta los más antiguos.
- Las conversaciones se almacenan por día y se recuperan por fecha y tema sin cargar todas las particiones.
- Los historiales existentes siguen disponibles y `/forget` elimina todos los datos conversacionales del operador.
- El aviso de demora no aparece antes de 30 segundos por defecto y sigue siendo configurable.
