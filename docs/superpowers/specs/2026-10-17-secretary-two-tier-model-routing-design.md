# Donna: enrutado de dos niveles entre LFM y modelo de razonamiento

Estado: propuesta detallada para revisión del operador
Fecha: 2026-10-17
Alcance: `lab-secretary`, Telegram, Open WebUI y el perfil local de Qwen

## Objetivo

Conservar LFM como `main`, punto de entrada rápido de Donna y responsable de
interpretar la petición, elegir capacidades permitidas y decidir si el caso
requiere una síntesis más elaborada. Para preguntas de baja complejidad, LFM
responde directamente. Para análisis complejos, LFM entrega al modelo de
razonamiento un paquete estructurado con el plan validado, el contexto útil y
la evidencia ya recuperada de Jira o Granola. El segundo modelo no vuelve a
seleccionar ni ejecutar herramientas.

Telegram y Open WebUI comparten la misma política de enrutado, conectores,
memoria, personalidad, reglas de veracidad y formato de interacción. Las
diferencias se limitan a la presentación que admita cada canal.

## Estado observado

- `lab-secretary` ya actúa como router común y registra `main` y `qwen-eval`.
- Telegram usa `main` (LFM); Open WebUI acepta `main` o `qwen-eval` mediante el
  mismo servicio. La selección explícita de `qwen-eval` sirve para comparar y
  no debe activar una segunda pasada automática.
- El flujo compartido ya tiene memoria reciente por operador/canal/conversación,
  memoria diaria recuperable, capacidades de solo lectura y eventos de
  interacción/progreso.
- Jira REST compila un subconjunto controlado de JQL en código. Granola localiza
  notas y obtiene sus resúmenes; el transcript completo queda fuera de la
  síntesis. Resultados vacíos y errores no se convierten en hechos.
- El perfil local `qwen-eval` apunta a Qwen3-4B y tiene límites de
  memoria/CPU. En el benchmark de diez casos obtuvo la puntuación manual más
  alta (33/40), con 26,75 s de latencia mediana. LFM2.5-1.2B obtuvo 16/40 y
  4,75 s. Es evidencia comparativa limitada, no una garantía de exactitud.
- El benchmark no comparó sistemáticamente el modo de razonamiento activado y
  desactivado; no se debe inferir que habilitarlo mejorará todas las respuestas.

## Alternativas

### A. Escalado en el servicio compartido — recomendada

Extender `lab-secretary` con roles de modelo y una decisión de escalado. Las
herramientas, memoria y políticas siguen en el servicio común; los dos canales
heredan el flujo automáticamente. Ventaja: una fuente de verdad y sin consultas
duplicadas. Coste: dos generaciones secuenciales para peticiones complejas.

### B. Servicio coordinador independiente

Añadir un coordinador separado que invoque modelos y conectores. Se descarta
para esta fase: duplica el enrutado, complica la memoria y agrega otro punto
operativo sin una necesidad demostrada.

### C. Sustituir LFM por una API externa

Simplifica usar un modelo mayor, pero expone contenido laboral a otro proveedor
y añade credenciales, coste y dependencia de red. No se activa en esta fase. La
interfaz de backend podrá admitir más adelante un proveedor OpenAI-compatible,
deshabilitado por defecto y sin datos en la nube salvo activación expresa.

## Diseño propuesto

### Roles y selección

- `main`: LFM actual. Rol por defecto en Telegram y Open WebUI. En casos
  complejos genera el plan estructurado de Donna; en casos sencillos, la
  respuesta final.
- `reasoner`: Qwen3-4B local, respaldado por el alias `qwen-eval` del mismo
  router llama.cpp que sirve LFM (`main`). Ambos modelos se cargan al arrancar
  y permanecen residentes juntos; Qwen solo recibe casos escalados y sintetiza
  el paquete preparado.
- La selección explícita `qwen-eval` en Open WebUI fuerza una llamada directa a
  Qwen para evaluación manual y omite el escalado. Telegram sigue en automático.
- El código dependerá de una interfaz por rol, no de nombres de proveedor. Un
  proveedor externo queda deshabilitado salvo configuración y activación
  explícitas.

### Flujo común

1. El adaptador autentica al operador y normaliza texto de Telegram, texto de
   Open WebUI o una nota de voz transcrita localmente.
2. Se carga la memoria corta del hilo y se recupera memoria duradera pertinente.
   La memoria corta sigue aislada por canal/conversación; la duradera puede
   recuperarse desde ambos. Ninguna memoria sustituye el estado actual de Jira
   o Granola.
3. Las reglas y `main` identifican intención, entidad, fecha y capacidades
   candidatas. `main` devuelve un plan acotado; el router valida capacidades y
   filtros. JQL se compila en código; nunca se ejecuta JQL libre del modelo.
4. El servicio ejecuta como máximo una vez cada capacidad autorizada. Jira REST
   y Granola devuelven resultados tipados con estado, evidencia y motivo de
   vacío/error. Ante entidad ambigua, fallo de fuente o filtro inseguro, Donna
   pregunta o se abstiene; no amplía la búsqueda.
5. Una política local elige respuesta directa o escalado. Motivos: varias
   fuentes que reconciliar, seguimiento de varios pasos, dependencias o fechas,
   contexto recuperado amplio, o petición explícita de análisis detallado. Un
   nombre ambiguo o falta de datos no se arregla llamando a un modelo mayor.
6. En baja complejidad LFM sintetiza la evidencia. En alta, el paquete para
   `reasoner` contiene pregunta original, plan breve de `main`, contexto
   necesario y solo evidencia relevante de conectores. Excluye credenciales,
   transcripts completos, otros usuarios y resultados descartados.
7. `reasoner` redacta en tono Donna, diferenciando hechos, tareas pendientes,
   elementos completados y datos que no constan. No inventa hechos, personas,
   responsables ni fechas. Si contradice la evidencia, la afirmación se omite o
   Donna pide aclaración.
8. La respuesta se guarda en la memoria vigente bajo el canal e hilo correctos.
   Telemetría registra rol/etapa/backend sin contenido ni identificadores
   sensibles.

### Escalado, límites y errores

- La planificación usa un esquema versionado y capacidades registradas. JSON
  inválido, filtros no permitidos o capacidad desconocida fallan cerrados y no
  disparan consultas ni cambios silenciosos de backend.
- La traza usa motivos acotados (`multi_source`, `multi_step`,
  `temporal_constraints`, `long_context`, `explicit_deep_analysis`).
- El razonador tendrá límites configurables de tiempo, contexto, tokens y
  concurrencia. La cola pesada no bloqueará respuestas rápidas ni health.
- Si Qwen falla o vence el plazo, LFM solo entrega un parcial si la evidencia
  basta y puede separar claramente lo pendiente; si no, explica que no terminó.
  Nunca inventa resultados de Jira/Granola como fallback.
- Ningún contenido laboral va a una API externa en esta fase. Una futura
  activación requerirá proveedor explícito, secreto fuera de Git, telemetría
  sin contenido y reglas de qué datos pueden salir del lab.

### Canales, reacciones, progreso y voz

- El router emite eventos comunes: 🔍 búsqueda, ✅ acuerdo y 👍 acción reconocida.
  El emoji comunica interpretación, no éxito de la consulta o ejecución.
- Telegram presenta reacción nativa y aviso breve de fuente/fase. Open WebUI
  presenta el mismo significado en su flujo, conservando SSE. Los adaptadores
  no clasifican intención por separado.
- Respuestas rápidas no llevan coletilla genérica. Tras 30 s por defecto, el
  canal envía como máximo un aviso de la fase actual; no repite el aviso sin
  cambio de fase.
- Voz entrante se transcribe localmente y usa idénticos enrutado, conectores y
  memoria que texto. La respuesta sigue escrita tanto para voz como texto.
- Open WebUI mantiene streaming durante estados y respuesta final. La salida
  interna de planificación no se expone al operador.

### Memoria y veracidad

- Memoria corta: hasta diez intercambios o dos horas por hilo/canal. Seguimientos
  como «¿y quién lo tiene?» reutilizan ese hilo sin mezclar otros.
- Memoria larga: se recupera con fecha, canal y procedencia. Ayuda a resolver
  referencias, pero el estado actual vuelve a comprobarse en Jira/Granola.
- Permisos e identidad se resuelven antes de invocar `reasoner`. La evidencia
  externa se trata como contenido no confiable y no puede cambiar instrucciones
  ni permisos.
- `/new` y `/forget` conservan el significado documentado. No se almacena una
  memoria paralela ni razonamiento privado del modelo.

### Métricas y evaluación

Prometheus distinguirá `model_role`, `phase`, `escalation` y `outcome` con
etiquetas acotadas. Medirá latencia por etapa, errores, fallbacks, escalados y
tokens si están disponibles. Nunca etiquetará texto, nombres, claves Jira, IDs
Granola ni referencias de conversación.

La evaluación usará el banco versionado y casos de secretaria: selección de
fuentes, filtros, fidelidad a evidencia, desambiguación, seguimientos de
conversación, formato, personalidad, latencia rápida y calidad de escalado. Una
respuesta extensa no se considera automáticamente correcta.

## Criterios de aceptación

1. Telegram y Open WebUI usan el mismo `Reply`, política de escalado, memoria y
   conectores.
2. `main` sigue siendo LFM por defecto; preguntas sencillas solo llaman a LFM.
   Las complejas llaman a Qwen con evidencia preparada y cada fuente se consulta
   una sola vez.
3. `qwen-eval` explícito en Open WebUI sigue siendo comparación directa;
   Telegram usa la política automática.
4. Pruebas sintéticas y consultas reales autorizadas a Jira REST y Granola
   verifican cada ruta, cruces, seguimiento temporal/de entidad y vacío/error.
   Los hechos se vinculan a evidencia o se declaran no disponibles.
5. Los seguimientos funcionan dentro del hilo Telegram y del hilo Open WebUI;
   `/new`, expiración y `/forget` respetan sus límites.
6. Texto y voz transcrita equivalentes llegan a la misma lógica y respuesta
   escrita, con la misma evidencia.
7. Reacciones y progreso concuerdan en ambos canales; el trabajo lento informa
   sin muletillas repetidas ni razonamiento interno.
8. Tests cubren JSON inválido, capacidad prohibida, entidad ambigua, fuente vacía
   o caída, timeout, cancelación, streaming y aislamiento de memoria.
9. El despliegue mantiene LFM y Qwen locales en un solo router llama.cpp,
   precarga ambos aliases, limita a dos los modelos residentes y desactiva la
   carga automática bajo demanda. Comprueba generación autenticada por ambos
   aliases y oculta secretos.

## Fuera de alcance

- Escritura en Jira/Confluence, Slack, WhatsApp y nuevas herramientas.
- Envío de contenido de trabajo a una API externa por defecto.
- Entrenar pesos, mutar reglas/persona automáticamente o guardar cadenas
  privadas de razonamiento.
- Asociar personas/clientes mediante fuzzy matching sin ID o alias aprobado.

## Decisiones a confirmar al revisar esta especificación

- Usar Qwen3-4B local como `reasoner` inicial, con posibilidad de reemplazarlo
  después de comparar Qwen3.5-4B en uso real.
- Límite de un trabajo `reasoner` activo a la vez; umbral de progreso retardado
  de 30 s; motivos de escalado enumerados arriba.
- Conservar la selección directa `qwen-eval` para comparación.
