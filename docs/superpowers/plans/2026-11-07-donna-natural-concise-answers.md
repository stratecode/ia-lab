# Donna: respuestas naturales, correctas y breves

Plan del 7 de noviembre de 2026. Estado: fases 1 a 4 implementadas y desplegadas el 7 de noviembre de 2026
(evidencia en `docs/validation/2026-11-07-donna-concise-answers.md`). Pendientes:
conjunto de oro formal, memoria semántica y optimización con DSPy, que
dependen de datos que aún no existen.
Objetivo del operador: que Donna (Telegram y Open WebUI) responda como una
persona que conoce el contexto, con información correcta y útil, y que lo que
cabe en un párrafo de dos líneas no ocupe tres pantallas. Cuando la versión
mejorada sea sólida se añadirán más herramientas y una interfaz de voz; este
plan las deja fuera, pero no las bloquea.

## Evidencia (conversación real de Telegram, 7 de noviembre)

| Entrada | Respuesta de Donna | Problema |
|---|---|---|
| `/jira` | Diez incidencias en tres mensajes, cada una con estado, responsable y «Vencimiento: sin vencimiento». El texto de cabecera dice «hasta 5». | Volcado literal del conector. No resume, no prioriza, repite campos vacíos y contradice su propia cabecera. |
| «dime cómo van Valentina y Gonzalo» | Dos párrafos genéricos («ambos han participado en actividades…») sin una sola incidencia ni fecha. | Sin información útil. Parece salir del historial, no de Jira. |
| «gracias Donna» | «Entendido. Estoy lista para seguir… ¿Qué información o acción necesitas a continuación?» | Robótico. Un agradecimiento no necesita una pregunta. |
| Resúmenes de Granola | Notas enteras repartidas en varios mensajes, truncadas con «…». | Contradice la instrucción «no los vuelques enteros». |
| Mensaje con DNI y clave | «El DNI mencionado corresponde a una persona…» | Frase vacía; el modelo rellena sin aportar. |

## Diagnóstico

1. **Las instrucciones ya piden brevedad** (`skills/secretary-core/instructions.md`),
   pero nada la hace cumplir. Es una petición a un modelo de 1,2B, que la ignora.
2. **Hay una vía que ni pasa por el modelo.** En `answerFromCapabilities`, cuando
   hay una sola fuente con `directAnswer`, se devuelve el texto del conector tal
   cual. Ahí nace el volcado de `/jira` y de las listas de Granola.
3. **La síntesis depende de un modelo pequeño** con `max_tokens=512` y sin
   comprobación posterior de longitud, fundamento ni tono.
4. **No hay capa conversacional.** Un agradecimiento, un saludo o un «vale» pasan
   por el mismo pipeline de consulta y reciben una respuesta de plantilla.
5. **Tampoco se mide.** No hay métrica de longitud, utilidad ni naturalidad, así
   que no se puede saber si un cambio mejora o empeora.

## Qué tomar de OpenJarvis (código leído en `~/.openjarvis/src`)

| Idea en OpenJarvis | Dónde | Cómo se aplica a Donna |
|---|---|---|
| Límite explícito de palabras y estructura fija por tipo de salida («STRICT LIMIT: 200 words») | `agents/morning_digest.py` | Contrato de formato por intención, comprobado en código. |
| Autoevaluación y regeneración: un evaluador puntúa y, si baja de 7/10, se pide una revisión | `morning_digest.py` + `DigestEvaluator` | Crítico en dos niveles: reglas deterministas siempre, juez LLM opcional. |
| Constructor de prompt con prefijo congelado y sufijo dinámico, secciones inspeccionables | `prompt/builder.py` | Sustituir la concatenación de cadenas de `answerWithContext` por secciones nombradas y testeables. |
| Juez de trazas (LLM-as-judge, puntuación 0 a 1) | `learning/optimize/feedback/judge.py` | Puntuar el conjunto de oro y las conversaciones reales aprobadas. |
| Dataset y scorer «personales» para optimización | `learning/optimize/personal/` | Convertir `/feedback` y `/aprueba` en dataset etiquetado. |
| Guarda de bucles de herramientas | `agents/loop_guard.py` | Útil cuando se añadan herramientas; hoy no hace falta. |
| Guardrails, auditoría y telemetría | `security/`, `telemetry/` | Los guardrails ya están hechos y verificados. Telemetría pendiente. |

Lo que **no** se copia: ReAct con elección libre de herramientas (un 1,2B es
poco fiable), aprendizaje por refuerzo del router (un solo operador no da
volumen) y ejecución de código arbitrario.

## Principios de diseño

- **Respuesta primero, detalle bajo demanda.** Máximo dos líneas por defecto,
  con una oferta explícita de ampliar solo si hay más que contar.
- **El código decide la forma; el modelo, solo el lenguaje.** Todo lo que se
  pueda calcular (contar, agrupar, ordenar por vencimiento, detectar bloqueos)
  se calcula en Go. El modelo recibe hechos ya cocinados.
- **Una mala respuesta corta es peor que una larga.** La brevedad nunca debe
  esconder datos: se resume y se indica cuánto se omite.
- **Los datos reales mandan.** Sin evidencia de la fuente, Donna lo dice en una
  frase en vez de rellenar.

## Fases

### Fase 0: medir antes de tocar (1 día)
- Congelar un conjunto de oro de 40 a 60 conversaciones, partiendo de las
  100 preguntas del plan `2026-10-28-donna-readonly-acceptance-100.md` y de
  los casos de la tabla de evidencia. Datos sintéticos o redactados.
- Métricas nuevas, sin contenido de usuario: palabras de la respuesta por
  intención, número de mensajes de Telegram por respuesta, ratio de
  respuestas con volcado literal. Se suman a `voice_metrics.go`.
- Línea base: ejecutar el conjunto contra la versión actual y guardar el
  resultado. Sin esto, ninguna fase siguiente es demostrable.

### Fase 1: renderizadores compactos para las vías directas (2 a 3 días)
Mayor impacto con menor riesgo, y no necesita más modelo.
- `/jira` y consultas de tareas: agrupar por estado, contar, destacar lo
  vencido, bloqueado o que vence en dos días, y mostrar como máximo tres
  incidencias. Ejemplo de salida objetivo: «Tienes 10 abiertas: 1 en curso
  (PMI-160), 2 bloqueadas (COC-99, COC-110) y 7 por hacer. Ninguna tiene
  fecha límite. ¿Detallo las bloqueadas?»
- Omitir campos vacíos («sin vencimiento») y respetar el límite que se anuncia.
- Granola: ya existe `wantsExecutiveMeetingSummary`; extender para que la vía
  directa nunca devuelva una nota entera, sino conclusión, decisiones y
  acciones.
- Criterio de aceptación: ninguna respuesta de Jira o Granola supera el
  tope de palabras por intención en el conjunto de oro, sin perder la clave
  de incidencia ni la fecha cuando existen.

### Fase 2: contrato de forma y crítico determinista (2 días)
- Tabla de contratos por intención (tarea, reunión, estado de persona,
  conversación): máximo de palabras y de mensajes, y qué hechos son obligatorios.
- Después de generar, un comprobador en Go valida longitud, ausencia de
  frases de relleno («Aclara lo que necesitas…», «¿Qué información necesitas
  a continuación?»), presencia de los hechos obligatorios y coherencia de
  cifras con la fuente. Si falla, una sola regeneración con la instrucción
  concreta de qué corregir (patrón de `morning_digest.py`); si vuelve a
  fallar, se usa el renderizador determinista de la fase 1.
- Sustituir la concatenación de `answerWithContext` por secciones nombradas
  (patrón `SystemPromptBuilder`) para poder probar cada una por separado.

### Fase 3: capa conversacional (1 a 2 días)
- Clasificar cortesías, confirmaciones y despedidas antes del pipeline de
  consulta y responder en una línea natural sin pregunta final.
- Variación controlada: un pequeño conjunto de fórmulas por situación, para no
  repetir siempre «Entendido».
- Preguntas aclaratorias solo cuando falte un dato y siempre con la pregunta
  concreta.

### Fase 4: corrección de contenido (3 a 4 días)
- «¿Cómo van Valentina y Gonzalo?»: verificar por qué no se enruta a Jira por
  persona como describe `secretary-operation.md`; si se enruta, el
  renderizador debe producir un estado por persona con cifras reales.
- Probar `qwen-eval` solo para la síntesis final, con el modelo principal para el
  resto, y comparar con el conjunto de oro. Decisión basada en la puntuación,
  no en la impresión.
- Cuestión abierta: latencia en CPU con un modelo mayor. Medirla antes de decidir.

### Fase 5: aprendizaje supervisado y optimización (después de 1 a 4)
- Convertir `/feedback bien|mal|corrige` y los candidatos aprobados en dataset.
- Juez de trazas (patrón `judge.py`) para puntuar el conjunto de oro y detectar
  regresiones de estilo. DSPy sobre prompts con revisión humana antes de
  desplegar; no se mutan prompts de producción automáticamente.
- Recuperación semántica de memoria con `bge-m3` solo si las métricas de la
  fase 0 muestran fallos léxicos reales.

### Fase 6: telemetría de coste y energía
- Tokens, latencia y, si el host lo permite, potencia por respuesta, sobre
  `llm-telemetry`. Sirve para decidir cuándo compensa un modelo mayor.

## Después de la versión mejorada (fuera de este plan)
- **Más herramientas:** cada una entra como capacidad registrada con su
  renderizador compacto y su contrato de forma, nunca como volcado.
- **Voz:** el contrato de forma ya contempla frases cortas sin viñetas. Piper
  (salida) y la transcripción local (entrada) existen; falta enlazar un canal
  de voz bidireccional y adaptar los renderizadores a texto hablado.

## Verificación

- `./scripts/test-orchestrator-go.sh` para todo cambio en `internal/secretary`.
- El conjunto de oro se ejecuta antes y después de cada fase y los resultados se
  guardan en `docs/validation/`.
- Prueba real en Telegram y Open WebUI por el navegador, con datos sintéticos,
  y lectura de métricas en el host, como en la verificación de los guardrails.
- Cada despliegue con `playbooks/deploy-secretary-runtime.yml`, que ya tiene
  rollback si falla la comprobación de salud.

## Riesgos

- Un renderizador demasiado agresivo puede ocultar una incidencia importante:
  siempre se indica cuántas se omiten y se ofrece ampliar.
- Un crítico basado en reglas puede rechazar respuestas correctas: se calibra
  con el conjunto de oro y se registra la tasa de regeneración.
- Un modelo mayor puede mejorar el estilo y empeorar la latencia en CPU.
- El árbol de trabajo tiene cambios previos sin commitear en
  `cmd/lab-secretary/main.go`; conviene aislarlos antes de empezar.
