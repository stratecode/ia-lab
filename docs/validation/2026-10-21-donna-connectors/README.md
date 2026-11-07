# Recuperación de conectores de Donna, 21-oct-2026

## Fallo reproducido

La comprobación autenticada `/resumen` por HTTP local de Donna devolvía Jira y
`Granola: no disponible`. Las comprobaciones directas de identidad Jira,
inicialización MCP Granola y Telegram getMe devolvieron HTTP 200, sin mostrar
credenciales ni contenido privado.

El esquema real de `list_meetings` de Granola declara `cursor` obligatorio,
tipo string. Su descripción indica cursor vacío para la primera página.
`GranolaResult` rechazaba cualquier argumento obligatorio antes de llamar la
herramienta. Se añade `cursor=""` y se acepta un argumento obligatorio solo si
el conector lo proporciona. Se mantiene rechazo de argumentos desconocidos.

## Validación

- Nueva regresión `TestGranolaListHandlesRequiredFirstPageCursor`: falló antes
  del cambio con `Granola list_meetings schema needs explicit arguments`;
  después pasa, incluido el rechazo de un argumento desconocido.
- `./scripts/test-orchestrator-go.sh`: completado, exit 0.
- `./scripts/build-orchestrator-go.sh`: completado, exit 0, desde copia aislada
  de HEAD 4064fa0 más los cambios de connectors.go y connectors_test.go.
  Las modificaciones locales previas de Telegram no se incluyeron en el despliegue.
- Binario desplegado con sustitución atómica y copia de reversión:
  `/usr/local/bin/lab-secretary.before-connector-fix-20261021`.
- SHA-256 desplegado verificado:
  `0b617a22b6b2fbc7431f6159f0ff4343325f143c124166242e7a08ada4b4cc69`.
- Solo se reinició lab-secretary, a las 20:55:47 CEST; health HTTP 200.
- `/resumen` autenticado después del cambio: HTTP 200 en 2,44 s, ambas fuentes
  presentes, notas Granola presentes y sin marcadores de no disponible.
  No se conserva el contenido privado en este informe.

## Telegram y límites

Antes del reinicio había errores periódicos `Telegram transport unavailable`.
El proceso nuevo registra `telegram_ready`. No se envió ningún mensaje a
Telegram desde esta sesión; la validación de entrega en ese canal requiere
una consulta real del operador. No se atribuye una causa definitiva al error
transitorio de transporte. Jira ya respondió por Donna antes de este cambio:
no se afirma haber corregido un fallo de Jira que no se reprodujo.

Tras 81 segundos de ejecución del proceso nuevo: `telegram_ready` presente,
cero `telegram_poll_error`; los servicios de Donna, modelos, voz, telemetría
y flight recorder siguen activos.

Los modelos y la instrumentación de apagados no se modificaron. No se tocó
ningún secreto de .env, vault o ssh ni se cambiaron credenciales de conectores.

## Consulta de tareas por personas (21 octubre, continuación)

Reproducción: `Hola Donna, me resumes como van Valentina y Gonzalo con sus tareas?`.
La búsqueda genérica compilaba `text ~ "hola" AND text ~ "donna" AND text ~ "resumes" AND text ~ "van"`;
no llegaba a los nombres. La prueba nueva falló con ese JQL antes de corregirlo.

Se reconocen peticiones explícitas de tareas por responsable, se resuelven los
nombres mediante Jira REST `user/search` y se consulta cada accountId por separado.
Los nombres ambiguos no producen búsquedas de incidencias. Se conserva prioridad
para claves de incidencia y consultas personales. Respuesta determinista con
estados verificados, agrupados por persona; hasta diez incidencias por persona,
ordenadas por actualización. Es una muestra, no un recuento total ni una medición
histórica de avance. Consulta vacía y fallo HTTP tienen resultados distintos.

Validación:
- Regresión del texto exacto, dos responsables, ambigüedad, consultas personales,
  clave de incidencia, nombre Emilia, cero incidencias y HTTP 403.
- `./scripts/test-orchestrator-go.sh`: correcto.
- `scripts/build-orchestrator-go.sh` en staging limpio de HEAD con solo los cambios
  de conectores de esta sesión: correcto; preserva modificaciones ajenas de Telegram.
- `git diff --check`: correcto.
- Servicio real: misma pregunta por `/v1/chat/completions` local autenticado,
  HTTP 200 en 1,71 s, ambas identidades y veinte incidencias presentes. No se
  conserva el contenido de las incidencias en este informe.

Binario desplegado SHA-256:
`83bac40cd2dd9648f18e15068128e844145c8420973ff0a698b589273da93515`.
Respaldo: `/usr/local/bin/lab-secretary.before-jira-people-fix-20261021`.
Solo reiniciado `lab-secretary`. No se ha enviado un mensaje por Telegram: queda
sin verificar la entrega de esta respuesta en ese canal.

Comprobación posterior: secretario, router de modelos y flight recorder activos.
Telegram registra `telegram_ready` y un `telegram_poll_error` de transporte desde
el reinicio; la corrección JQL no se presenta como arreglo del transporte Telegram.
