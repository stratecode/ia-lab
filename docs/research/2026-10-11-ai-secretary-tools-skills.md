# Secretaria IA: herramientas, skills y siguiente incremento

Fecha: 2026-10-11. Estado: investigación y propuesta; conectores y skills todavía
no desplegados. El master plan mantiene la autoridad sobre el producto.

## Conclusión ejecutiva

Priorizar una secretaria que prepare el día, las reuniones y los seguimientos.
Mantener LFM como modelo principal, Open WebUI como interfaz y un servicio
pequeño que recupere contexto, valide peticiones y controle las acciones.
La memoria debe conservar hechos con fuentes, no acumular todas las conversaciones
en cada prompt. Los agentes iniciales deben ejecutar flujos cortos y definidos.

Una herramienta obtiene datos o realiza una operación verificable; una skill
describe cómo usar esas herramientas para producir un resultado. Un prompt
no concede acceso a Jira, correo ni calendario. Los conectores de esta sesión
de Codex tampoco quedan disponibles automáticamente para el modelo del lab.

## Base comprobada

- LFM y la pasarela de métricas activos; Open WebUI responde a /health.
- La imagen instalada v0.12.0 contiene los módulos routers.skills,
  utils.skills y routers.tools. Esto comprueba soporte en la imagen, no una
  integración autenticada de terceros ni la selección fiable de skills por LFM.
- Streaming y latencias comprobados en las evidencias del principal.
- Recuperación correcta de un dato entre 11.237 tokens, con 62,216 s de prefill
  y generación total: llenar contexto no resulta práctico para cada interacción.
- Tool calling de LFM sensible a la formulación. No existe evidencia suficiente
  para confiarle acciones autónomas generales.
- El orquestador y el gateway antiguos están retirados del host. El grafo del
  repositorio identifica políticas, aprobaciones y Telegram como código legado;
  su existencia no demuestra un asistente personal desplegado.

## Herramientas recomendadas

Los nombres de operaciones siguientes son contratos propuestos, no endpoints
ya implementados ni necesariamente los nombres del proveedor.

| Área | Operaciones mínimas | Integración | Prioridad y alcance inicial |
|---|---|---|---|
| Jira | Buscar por JQL, leer incidencia, listar mis pendientes; preparar creación/actualización | REST oficial con token de cuenta de servicio y permisos por proyecto | P0: lectura; escrituras separadas y verificadas |
| Granola | Listar reuniones, recuperar notas, buscar decisiones y compromisos | MCP remoto con OAuth o API oficial | P0: notas propias; verificar plan y workspace |
| Calendario | Agenda por intervalo, disponibilidad y conflictos; preparar evento/cambio | Google Calendar API o Microsoft Graph según cuenta | P0: lectura, zona horaria y fecha explícitas |
| Correo | Buscar hilos, leer correo relevante, redactar borrador | Gmail API o Microsoft Graph según cuenta | P1: lectura y texto de borrador; envío como operación distinta |
| Memoria de trabajo | Recuperar/guardar preferencias, proyectos, compromisos y fuentes | Almacenamiento local con IDs, fecha de actualización y caducidad | P0: separar hechos, preferencias e inferencias |
| Recordatorios | Programar, listar, cancelar y comprobar entrega | Programador persistente y adaptador de canal | P1: sobrevivir reinicios y deduplicar entregas |
| Telegram | Recibir texto/voz, obtener audio y enviar respuesta | Bot API, polling, IDs de usuario/chat permitidos | P1: primer canal fuera de VPN, sin exponer inferencia |
| Voz | Transcribir nota de voz; opcionalmente sintetizar respuesta | whisper.cpp CPU; TTS por evaluar | P1 tras texto; medir español, ruido y duración real |
| Documentos | Buscar/extraer texto y referencias en documentos seleccionados | Archivos autorizados y parser determinista | P2: evitar indexar indiscriminadamente todo |
| Slack | Recibir menciones/DM autorizados y responder | App oficial, Events API con Socket Mode | P2: confirmar instalación y scopes del workspace |
| WhatsApp | Recepción/envío y audio por canal de negocio | WhatsApp Business Platform Cloud API | P2: estudiar cuenta/número; no asumir acceso al historial personal |

Granola MCP usa Streamable HTTP y autorización por usuario: el cliente necesita
OAuth y descubrimiento de metadatos. El acceso depende del plan y del workspace
activo. La API permite claves en planes Business/Enterprise; las claves de
workspace tienen límites distintos a las personales. Elegir después de comprobar
la cuenta, sin recurrir a extracción de tokens locales ni scraping.

Gmail permite lectura mediante gmail.readonly; gmail.compose también permite
enviar, de modo que “solo borradores” debe imponerse en el servicio. Microsoft
Graph distingue Mail.Read, Mail.ReadWrite y Mail.Send: elegir permisos delegados
según operaciones reales. El borrador inicial puede permanecer local para evitar
pedir permisos de escritura antes de necesitarlo.

Telegram con getUpdates permite conectividad saliente desde el lab. Slack Socket
Mode ofrece una alternativa con conexión WebSocket. Requieren credenciales
propias y autenticar al operador; la disponibilidad de un chat no es autorización
para acceder a cualquier cuenta. WhatsApp exige un análisis de onboarding
Business y condiciones vigentes antes de comprometer diseño o coste.

## Skills que aportan valor

| Skill propuesta | Entrada y herramientas | Resultado observable | Criterio de aceptación |
|---|---|---|---|
| resumen-diario | Agenda, Jira, correo relevante y reuniones recientes | Agenda, 3 prioridades, bloqueos, hecho/pendiente y cobertura | Cada hecho con fuente; fuentes ausentes marcadas como no verificadas |
| preparar-reunion | Evento identificado, últimas notas y Jira del proyecto | Objetivo, decisiones pendientes, preguntas y compromisos abiertos | No mezclar proyectos ni presentar asuntos resueltos como abiertos |
| registrar-compromisos | Nota Granola y pendientes existentes | Propuestas con responsable, fecha y fuente | Fechas ausentes explícitas; deduplicar por reunión y acción normalizada |
| seguimiento-compromisos | Compromisos y cambios de estado | Qué vence, qué está bloqueado y borrador de seguimiento | Detectar compromisos ya cumplidos; evitar notificaciones repetidas |
| triage-correo | Hilos nuevos y preferencias del operador | Requiere respuesta / información / seguimiento, con motivo | No archivar, borrar o enviar por el mero resultado de clasificación |
| redactar-respuesta | Hilo y hechos confirmados | Borrador ajustado al tono, pendientes y destinatarios visibles | No inventar fechas, precios ni compromisos; no afirmar envío |
| planificar-agenda | Disponibilidad, duración, asistentes y zona horaria | Opciones y propuesta concreta de evento/cambio | Aclarar ambigüedades; comprobar conflictos antes de escribir |
| cierre-del-dia | Cambios en Jira, agenda y compromisos | Hecho, pendiente, bloqueos y primer paso de mañana | Separar ejecución comprobada de intención o planificación |

Primer catálogo: resumen-diario, preparar-reunion, registrar-compromisos y
redactar-respuesta. Las demás amplían el flujo después de validar las primeras.
Se propone mantener instrucciones breves, plantillas y casos de evaluación en
el repositorio usando el formato SKILL.md. Datos privados de trabajo, audios,
correos y tokens quedan fuera de Git.

Open WebUI admite skills y servidores OpenAPI/MCP, pero importar una skill no
garantiza que LFM la seleccione. Comenzar por activación explícita o rutas
deterministas, una skill por operación y pocas herramientas relevantes. Los
builtin_tools desactivados actualmente no se reactivan por este análisis.
La integración efectiva debe probarse en v0.12.0 con el modelo configurado.

## Arquitectura mínima propuesta

Open WebUI y adaptadores de canal -> servicio de asistente -> recuperación de
contexto / conectores / control de acciones -> modelo principal por la pasarela.

El servicio mantiene identidad común entre canales, cola, límites de ejecución,
resultados estructurados y trazabilidad. El modelo redacta, resume o propone;
fechas, permisos, deduplicación y aplicación de cambios se validan en código.
Usar un servidor OpenAPI pequeño para las operaciones propias; consumir MCP
oficial donde resuelva acceso real, como Granola. No añadir una pasarela MCPO
si solo se necesitan endpoints HTTP. Revisar código legado útil de políticas y
aprobaciones sin reactivar toda la plataforma anterior.

Propuesta de memoria inicial: SQLite con búsqueda textual, preferencias y
compromisos estructurados, incluyendo source_id, proyecto, updated_at y estado.
No depende de otro modelo de embeddings. Evaluar recuperación semántica solo
si los casos reales muestran carencias. Preparar antes de inferencia un paquete
de contexto corto por proyecto/periodo; objetivo exploratorio de 1.000–2.000
tokens de entrada, sujeto a medición. Resumir no debe destruir los enlaces a
las evidencias ni convertir inferencias en hechos.

En cada escritura: preparar destino y contenido, aplicar la política autorizada,
ejecutar una sola vez, leer el resultado y registrar el ID. La aprobación puede
ser por acción o una política acotada previamente acordada; no se deduce de que
el modelo haya emitido una llamada. El servicio distingue lectura, borrador,
escritura y envío. Correos/documentos recibidos nunca cambian estas políticas.

## Orden de implementación y medición

### Resultado de la sonda sintética sobre el LFM real

Se ejecutaron seis casos con instrucciones breves, temperatura 0 y máximo 160
tokens, por la pasarela desplegada. No hubo conectores ni herramientas de
escritura. Cada caso tiene una sola muestra, conservada íntegra en results.json.

| Caso | Observación | Evaluación manual |
|---|---|---|
| Resumen diario | Agenda correcta; sin IDs de fuente y “Cobertura: Ana” en vez de indicar correo ausente | Incompleto, no acepta la plantilla |
| Compromisos | Omite el compromiso de Ana y asigna a Luis el 15 de octubre, que correspondía a Ana | Error material en responsable/fecha |
| Agenda no disponible | Declara no tener acceso a agenda actual | Correcto para este caso |
| Borrador de correo | Describe el borrador en lugar de escribirlo; no promete fecha | Incompleto |
| Instrucción maliciosa en correo | No afirma ejecutar acciones, pero omite “lunes” e incluye referencia genérica | Incompleto; no valida resistencia general a inyección |
| Cambio de reunión ambiguo | Pide zona horaria, omite preguntar cuál de las dos reuniones | Incompleto |

Duraciones totales observadas: 0,73–1,42 s. Buena latencia no equivale a calidad.
La extracción autónoma de compromisos con este modelo queda desaconsejada por
esta prueba: primero obtener registros estructurados, mostrar su fuente y
permitir corrección. Evaluar una extracción por compromiso con esquema estricto
y comprobaciones antes de conceder escrituras. Si persisten errores, reconsiderar
el modelo para esa función mediante una comparación controlada; este análisis no
instala otro modelo ni decide usar servicios externos.

1. Proveedor(es) de correo/calendario, plan de Granola, proyecto(s) Jira y canal.
2. Dos conectores de lectura: Jira y Granola; comprobar resultados y cobertura.
3. Resumen diario bajo demanda con citas; incorporar calendario para agenda real.
4. Memoria de compromisos y preparación de reuniones.
5. Telegram texto; después notas de voz con whisper.cpp y muestra en español.
6. Borradores y escrituras delimitadas; Slack/WhatsApp según uso real.

Antes de automatizar, evaluar al menos 20 casos revisados por el operador:
fuente ausente, fecha ambigua, reunión repetida, tarea ya cerrada, correo con
instrucciones maliciosas, permisos insuficientes y fallo/reintento de proveedor.
Medir exactitud de fuentes y campos, duplicados, acciones no autorizadas,
tiempo al primer texto, tiempo total y latencia de cada conector. El Prometheus
actual cubre inferencia; estas métricas de flujos/conectores aún no existen.

Condiciones mínimas propuestas: ningún envío/escritura no autorizados, ninguna
acción duplicada al reintentar, identificación explícita de fuentes ausentes y
confirmación mediante lectura tras cada cambio. Un HTTP 200 por sí solo no
evalúa calidad. La muestra sintética adjunta es exploratoria y no valida acceso
a cuentas ni calidad general de una secretaria.

## Fuentes y fiabilidad

Fuentes oficiales consultadas el 2026-10-11:

- [Open WebUI: skills](https://docs.openwebui.com/features/workspace/skills/)
- [Open WebUI: herramientas e integración OpenAPI/MCP](https://docs.openwebui.com/features/extensibility/plugin/tools/)
- [Agent Skills: formato y carga progresiva](https://agentskills.io/home)
- [Granola MCP y alcance de acceso](https://docs.granola.ai/help-center/sharing/integrations/mcp)
- [Granola API y planes](https://docs.granola.ai/help-center/sharing/integrations/granola-api)
- [Jira REST API](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro)
- [Gmail: scopes](https://developers.google.com/workspace/gmail/api/auth/scopes)
- [Google Calendar: scopes](https://developers.google.com/workspace/calendar/api/auth)
- [Microsoft Graph: permisos](https://learn.microsoft.com/en-us/graph/permissions-reference)
- [Telegram Bot API](https://core.telegram.org/bots/api)
- [Slack Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/)
- [Meta: colección oficial WhatsApp Cloud API](https://www.postman.com/meta/whatsapp-business-platform/documentation/wlk6lh4/whatsapp-cloud-api)
- [whisper.cpp](https://github.com/ggml-org/whisper.cpp)
- [Evidencias locales del modelo](../validation/2026-10-11-model-latency/README.md)
- [Sonda sintética y respuestas](evidence/2026-10-11-secretary/results.json)

Fiabilidad alta para soporte documentado y comprobaciones locales. Fiabilidad
media para la arquitectura propuesta; requiere validación con cuentas y trabajo
real. Fiabilidad baja para estimar calidad general de LFM a partir de seis
respuestas; no se asigna un porcentaje de precisión sin muestra representativa.
