# Donna: respuestas breves — evidencia del 7 de noviembre de 2026

Verificación en producción tras desplegar `lab-secretary` (commit `162cff0` y
el revisor de longitud posterior), con mensajes de prueba enviados a Telegram y
a Open WebUI. Los datos de Jira son los reales del operador; no se guardan aquí.

| Entrada | Canal | Resultado |
|---|---|---|
| `/jira` | Telegram | Resumen de tres frases (cuentas por estado, tres destacadas, vencidas) en lugar de diez incidencias en tres mensajes |
| «gracias Donna» | Telegram | «Para eso estoy.» sin pregunta final |
| «dime como van Valentina y Gonzalo» | Telegram | Una línea por persona con cifras de Jira; antes, dos párrafos genéricos |
| «qué tengo pendiente en Jira» | Open WebUI | Mismo resumen compacto |
| «¿Qué es un spike…?» | Open WebUI | Primera respuesta del modelo demasiado larga, regenerada una vez: 25 palabras |
| «¿Qué diferencia hay entre un sprint y un spike?» | Telegram | Dos frases |

Métricas del host tras las pruebas: `lab_secretary_answer_adjustments_total
{action="regenerated"}` = 1, `lab_secretary_model_tokens_total` registra
prompt y completion, y `lab_secretary_response_words` registra la longitud por
intención (25 palabras en la respuesta general).

## Defectos encontrados y corregidos durante la prueba

- `offersExternalWrite` leía «puedo» y un verbo de escritura en cualquier parte
  de la respuesta. Un estado de Jira con «no puedo decir si van en plazo» y un
  título que empieza por «Crear» se bloqueaba con «No he aplicado ningún
  cambio externo». Ahora exige el verbo junto a la oferta.
- «cómo van <Nombre>» no llegaba a Jira por persona.

## Sin verificar todavía

- Memoria semántica con `bge-m3`: solo tiene sentido si las métricas muestran
  fallos léxicos reales; todavía no hay datos.
- Optimización de prompts con DSPy: requiere un conjunto etiquetado de
  `/feedback` que aún no existe.
- No hay una puntuación formal de naturalidad; la mejora se comprobó con casos
  concretos y con tests de longitud (`answer_contract_test.go`).
