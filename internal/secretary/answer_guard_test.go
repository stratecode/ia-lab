package secretary

import (
	"strings"
	"testing"
)

func TestSourceAnswerDropsUnaskedAdvice(t *testing.T) {
	sourceAnswer := "**Respuesta:**\nLa reunión acordó explicar dos tablas.\n\n**Próximo paso clave:**\n- Explicar el modelo de datos a Gonzalo (Fran).\n- Cambiar de herramienta.\n- Llamar a otro cliente.\n\n**Fuente:** Nota de reunión."
	got := sourceAnswerForQuestion("¿Qué se acordó sobre el modelo de datos?", sourceAnswer)
	if !strings.Contains(got, "dos tablas") || !strings.Contains(got, "Explicar el modelo de datos") || !strings.Contains(got, "Nota de reunión") || strings.Contains(got, "Cambiar de herramienta") || strings.Contains(got, "Llamar a otro cliente") {
		t.Fatalf("unsupported material survived: %q", got)
	}
	kept := sourceAnswerForQuestion("¿Qué próximos pasos se acordaron?", sourceAnswer)
	if !strings.Contains(kept, "Cambiar de herramienta") {
		t.Fatalf("requested steps were removed: %q", kept)
	}
}
