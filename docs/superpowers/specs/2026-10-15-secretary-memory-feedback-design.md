# Secretaria: memoria, feedback y evaluación

Estado: propuesta de diseño para revisión del operador
Fecha: 2026-10-15
Alcance: `lab-secretary`, su enrutador común y los canales conectados

## Objetivo

La secretaria debe aprovechar el contexto y las correcciones verificadas para
mejorar futuras decisiones, sin entrenar automáticamente los pesos del modelo
ni convertir una respuesta errónea en conocimiento. El sistema debe poder
explicar qué fuente consultó, recuperar recuerdos pertinentes y demostrar con
pruebas cuándo una corrección mejora el resultado.

El primer alcance cubre Telegram y Open WebUI. Otros canales deben reutilizar el
mismo contrato de conversación, enrutado, memoria y feedback al incorporarse.
Las capacidades externas siguen siendo de solo lectura.

## Estado actual observado

- El historial de corto plazo está aislado por operador, canal y conversación;
  conserva hasta diez intercambios o dos horas.
- La memoria conversacional duradera se guarda por día en JSONL en el servidor y
  permite recuperar contexto principalmente con palabras clave. `/forget` borra
  la memoria del operador.
- El enrutador comparte capacidades de lectura. Granola aporta resúmenes de
  reuniones; Rovo busca en Jira y Confluence. La correlación entre reuniones,
  proyectos y personas aún no está implementada.
- Los eventos de interacción que presentan 🔍, ✅, 👍 y el avance de una consulta
  son transitorios. No registran el resultado del enrutado ni una valoración de
  la respuesta.
- El principal problema operativo observado es que se puede seleccionar una
  fuente incorrecta o sintetizar datos ajenos a la evidencia. Un modelo más
  capaz, por sí solo, no corrige esos fallos.

## Alternativas consideradas

### A. Extender los archivos locales existentes — recomendada para la primera fase

Añadir registros JSONL estructurados para trazas, feedback y conocimiento
aprobado. Definir contratos de almacenamiento para que la implementación pueda
migrar a otro backend sin cambiar canales ni conectores.

Ventajas: coherente con el almacenamiento actual, pocas dependencias y adecuado
para un único operador y un volumen bajo. Inconvenientes: las consultas con
múltiples relaciones y un volumen alto exigirían migración posterior.

### B. Introducir una base SQL ahora

Guardar trazas, entidades, relaciones y feedback en una base relacional local o
PostgreSQL. Mejora las consultas estructuradas y la integridad referencial, pero
añade una dependencia operativa y de despliegue que aún no se justifica por el
volumen actual.

### C. Añadir de inicio RAG vectorial y base de grafos

Permite búsquedas semánticas y recorridos complejos entre personas, clientes,
proyectos y reuniones. Aumenta servicios, mantenimiento y puntos de fallo antes
de tener una evaluación que demuestre que la búsqueda léxica es insuficiente.

La propuesta elige A y deja interfaces y referencias externas estables para que
una migración posterior a B o C no acople el servicio a un proveedor concreto.

## Diseño propuesto

### Flujo común

1. El adaptador autentica al operador y asigna canal y conversación.
2. La secretaria recupera el contexto corto de esa conversación y, cuando
   procede, recuerdos aprobados por fecha, entidad y relevancia.
3. El enrutador decide una o varias capacidades de lectura. Las preguntas sobre
   acciones derivadas de reuniones pueden requerir Granola y Rovo; la ambigüedad
   de fuente o entidad no se resuelve inventando una asociación.
4. Los conectores devuelven evidencia con identificador, sistema de origen,
   fecha y enlace cuando esté disponible. El estado actual de tareas se
   consulta en la fuente autoritativa; la memoria no sustituye Jira/Rovo ni
   Granola.
5. El sintetizador recibe solo la evidencia recuperada y debe distinguir hechos,
   ausencias y errores de acceso. Cada afirmación factual importante conserva su
   referencia a la fuente.
6. La conversación se guarda usando el mecanismo existente. En paralelo se
   registra una traza de decisión sin duplicar el contenido de la conversación
   o las transcripciones completas.
7. El operador puede valorar o corregir esa respuesta. La corrección queda como
   candidata hasta ser contrastada o aprobada explícitamente.

Los eventos de presentación de canal siguen siendo neutrales y efímeros; el
registro de evaluación se produce en el servicio compartido, no en el adaptador.

### Registros persistentes

La primera versión añade tres tipos de registro al directorio privado de estado
del servicio:

- **Interaction trace:** ID aleatorio de interacción, fecha, canal,
  fingerprint no reversible de conversación, clase de intención, capacidades
  seleccionadas, estado por capacidad (`selected`, `success`, `empty`, `error`),
  latencias, número de resultados, IDs/enlaces de evidencia y versión de
  enrutador/modelo. No incluye tokens, contenido completo de proveedores ni
  copias de prompts y respuestas.
- **Feedback event:** ID de interacción, etiqueta (`correct`, `incorrect`,
  `wrong_source`, `missing_source`, `wrong_entity`, `missing_fact`, `style`) y
  corrección opcional. Una corrección es texto sensible y queda protegida como
  el historial actual.
- **Knowledge item:** tipo (`preference`, `fact`, `entity_alias`, `relationship`,
  `procedure`), sujeto y predicado estructurados, valor, referencias de fuente,
  fechas de validez, confianza y estado (`candidate`, `approved`, `rejected`).

El almacenamiento será JSONL versionado bajo `StateDir`, con directorios `0700`
y archivos `0600`, escrituras atómicas y rotación por fecha. Cada tipo tendrá
una interfaz de repositorio para poder sustituir JSONL por SQL sin modificar el
flujo de conversación.

### Memoria y recuperación

- El historial reciente y el archivo diario existentes siguen siendo memoria
  episódica. No se vuelve a almacenar el transcript completo de una reunión en
  la memoria de la secretaria.
- El historial episódico sigue disponible como contexto conversacional con su
  fecha y canal; no se presenta por sí solo como hecho verificado. Solo los
  elementos de conocimiento aprobados pueden influir como hechos persistentes.
  Los candidatos pendientes pueden mostrarse para revisión, pero no se inyectan
  como hechos en respuestas.
- El orden inicial de recuperación es: filtro de operador y permisos, fecha,
  IDs/alias exactos de entidad, coincidencia léxica y recencia. La búsqueda
  vectorial se añade únicamente si la evaluación descubre fallos repetidos de
  sinónimos o paráfrasis.
- La recuperación de hechos históricos incluye fecha y fuente. No se presenta
  una decisión antigua como estado actual sin volver a consultar la fuente
  externa.

### Registro de clientes, proyectos y personas

El primer registro de entidades no será una base de grafos. Usará IDs internos
estables y referencias externas:

- proyecto: clave/ID de Jira y referencias opcionales a espacio de Confluence,
  carpeta o reunión de Granola;
- persona: `accountId` de Atlassian y correo exacto cuando esté disponible,
  asociados a nombres y alias revisados;
- cliente: ID de CRM si existe; si no, ID interno y alias aprobados por el
  operador.

Cada relación tendrá procedencia y confianza. La coincidencia exacta por ID o
correo puede resolverse automáticamente. Un nombre ambiguo o una coincidencia
difusa pasa a aclaración/candidato; no se fusionan personas solo por compartir
nombre.

### Feedback y ciclo de mejora

El contrato común de feedback admitirá valoración positiva/negativa y motivos
estructurados; los canales podrán presentarlo como comando, botón o reacción.
Una corrección podrá indicar la fuente correcta, entidad, hecho omitido o
redacción esperada. La interacción no se toma como feedback por el mero hecho de
que el operador no responda.

El flujo de aprendizaje será supervisado:

1. Capturar una corrección asociada a la traza.
2. Contrastar los hechos contra la fuente, o pedir aprobación si la fuente no
   puede verificarse.
3. Guardar una memoria aprobada cuando sea reutilizable; de lo contrario,
   convertir el caso en ejemplo de evaluación.
4. Ejecutar la suite antes de cambiar reglas, prompts o recuperación.
5. Publicar un cambio solo si mejora los casos fallidos sin degradar los casos
   que ya pasaban.

No habrá actualizaciones automáticas de pesos, prompts de sistema ni reglas
productivas desde texto libre del operador. Fine-tuning o reinforcement learning
quedan fuera del MVP y requieren un conjunto curado, métricas y aprobación
específica.

### Evaluación y observabilidad

El repositorio incluirá fixtures sintéticos o redactados y casos de regresión
para:

- consulta exclusiva de Granola;
- consulta exclusiva de Jira/Rovo;
- consulta que debe cruzar Granola y Rovo;
- aliases ambiguos de persona/cliente/proyecto;
- fuente vacía, desconectada o con error;
- recuerdo temporal como “la semana pasada” frente a estado actual.

Cada caso fijará la intención y fuentes esperadas, referencias de evidencia,
hechos que deben aparecer y afirmaciones que no deben inventarse. La verificación
medirá selección de fuente, cobertura de evidencia, exactitud de tareas,
responsables y fechas, abstención ante datos ausentes y latencia.

Prometheus recibirá solo contadores y duraciones de baja cardinalidad
(`channel`, `intent`, `capability`, `outcome`). Nunca tendrá texto, nombres,
IDs de conversación, URLs particulares, claves de Jira ni IDs de Granola como
labels.

### Privacidad, retención y borrado

- Los datos permanecen en el servidor del lab; ejemplos en Git serán sintéticos
  o redactados. El registro de entidades reales y las correcciones no se
  versionan en Git.
- Los payloads completos de Granola/Rovo no se guardan en la traza. Se conservan
  únicamente referencias y los campos mínimos necesarios para la memoria
  aprobada.
- `/forget` debe borrar historial corto/largo, trazas y feedback vinculados al
  operador, junto con candidatos y recuerdos aprobados. Las métricas agregadas
  no identificables no se pueden vincular de nuevo al operador.
- Se propone retener trazas operativas 90 días, candidatos sin resolver 30 días
  y recuerdos aprobados hasta `/forget` o su fecha de caducidad. Estos plazos
  deben quedar configurables y confirmarse al revisar esta especificación.

## Fases de implementación

1. **Contratos y evaluación base:** definir IDs, interfaces, fixtures anonimizados
   y pruebas para el comportamiento actual y los fallos observados.
2. **Trazas mínimas:** capturar selección de capacidad, resultado, latencia y
   referencia de evidencia para ambos canales; añadir métricas sin contenido.
3. **Feedback supervisado:** añadir operación de feedback común, asociarla a la
   última interacción y exponer consulta/revisión/borrado de candidatos.
4. **Memoria aprobada y entidades:** guardar aliases, hechos y relaciones con
   procedencia; recuperar solo memoria aprobada y mantener estado actual ligado
   al conector.
5. **Validación y despliegue:** ejecutar suite de regresión, canario de solo
   lectura con consultas reales autorizadas, comprobar `/forget`, permisos de
   archivos, rollback y documentación operativa.

Cada fase debe quedar en Git y pasar el harness Go del repositorio. No se activa
escritura en Jira ni se conectan nuevos canales como parte de este alcance.

## Criterios de aceptación del diseño

- Telegram y Open WebUI usan el mismo contrato de memoria, trazas y feedback.
- Una traza identifica la decisión y las fuentes sin guardar payloads completos.
- Una corrección no cambia producción hasta verificarse o aprobarse.
- Los hechos actuales se vuelven a consultar en la fuente autoritativa.
- Las entidades ambiguas no se enlazan como hechos.
- Los casos de evaluación cubren rutas de una y varias fuentes y fallos de
  proveedor.
- `/forget` elimina todos los registros identificables y las métricas no
  contienen contenido ni etiquetas de alta cardinalidad.

## Decisiones abiertas para la revisión

1. Confirmar los plazos propuestos de 90 días para trazas y 30 días para
   candidatos no resueltos.
2. Confirmar si las correcciones aprobadas pueden conservarse hasta `/forget`,
   o si también deben tener caducidad por defecto.
3. Confirmar el mecanismo inicial de feedback en Telegram/Open WebUI; el contrato
   será común aunque la presentación varíe por canal.
