# Validación del servicio de interpretación, 27 de octubre de 2026

## Contrato y seguridad

- 23 casos sintéticos de intención en el corpus; pruebas Go en Docker y cliente
  Python real contra el proceso HTTP: correctos.
- Cliente sin token: HTTP 401. Capacidades desconocidas: rechazadas en Donna.
  Petición de cambio: ninguna herramienta de escritura registrada ni llamada.
- Servicio y Donna activos en el host; `/health` correcto. `/metrics` contiene
  solo contadores y duración agregada, sin texto de peticiones.

## Contraste con fuentes conectadas

- Jira: una incidencia conocida conserva estado `Waiting`, persona asignada y
  ausencia de vencimiento. Donna devuelve los campos de Jira directamente para
  una clave exacta, sin síntesis del modelo, y añade «No he cambiado nada» al
  pedir el cierre. Se observó lo mismo en respuesta normal y streaming.
- Granola: una daily reciente contenía un acuerdo sobre explicar dos tablas
  del modelo de datos. El enrutado anterior pidió aclarar la fuente; el nuevo
  consultó Granola y respondió sobre ese acuerdo. Una primera síntesis añadió
  próximos pasos ajenos; se ajustaron instrucciones y se filtraron apartados
  de consejo no solicitados. La repetición conservó la fuente y el hecho
  relevante sin esos apartados.
- Una petición de recuerdo sobre una conversación no registrada respondió que
  no había memoria relacionada; no se trató como orden de ejecución.
- Las consultas generales de vacaciones y de definición no llamaron a Jira ni
  Granola. La respuesta libre del modelo todavía puede ofrecer sugerencias;
  la fiabilidad de ese contenido se evalúa aparte del enrutado.

## Límite de la evidencia

La evaluación cubre estos canarios y el corpus, no todas las formulaciones.
La clasificación opcional por modelo está desactivada en el host mientras no
haya evaluación independiente de precisión y latencia. No se guardaron notas,
transcripciones, tokens ni respuestas privadas en este directorio.
