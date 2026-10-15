package secretary

import (
	"strings"
	"testing"
)

func TestExplicitAssignmentAnswerKeepsOwnersAndFiltersRequestedActions(t *testing.T) {
	const source = `# Resumen

Cambios acordados:
- Cambiar Shop a Producto.
- Añadir un mapa de puntos de venta.

# Próximos Pasos
- **Enviar imágenes adaptadas a Fran para la home y profesionales** (Paula, Alba)
  Incluir la foto con los dos productos para el primer slide y las imágenes del shooting organizadas por página.
- **Revisar textos de la página en inglés y castellano** (Iris)
  Confirmar que no falta ni sobra contenido.
- **Aplicar cambios acordados y tener la web lista para el viernes** (Fran)
  Renombrar shop, quitar precios, animar el muñeco, fusionar home con "sobre nosotros" y subir imágenes definitivas.
- **Investigar posible collab con Impress** (Alba)
  Explorar si hay encaje de marca para una colaboración.`
	const question = "¿Quién debía enviar las imágenes y qué tareas tenía Fran para dejar la web lista?"

	answer, ok := explicitAssignmentAnswer(question, source, []EvidenceRef{{Provider: "Granola", ID: "meeting-1", URL: "https://notes.granola.ai/d/meeting-1"}})
	if !ok {
		t.Fatal("expected to extract explicit next-step assignments")
	}
	for _, expected := range []string{
		"Paula, Alba",
		"Enviar imágenes adaptadas a Fran",
		"Fran",
		"tener la web lista para el viernes",
		"https://notes.granola.ai/d/meeting-1",
	} {
		if !strings.Contains(answer, expected) {
			t.Errorf("answer omitted %q: %s", expected, answer)
		}
	}
	for _, excluded := range []string{"Iris", "mapa de puntos", "collab con Impress"} {
		if strings.Contains(answer, excluded) {
			t.Errorf("answer included unrelated assignment %q: %s", excluded, answer)
		}
	}
}

func TestExplicitAssignmentAnswerDoesNotInventFromNarrative(t *testing.T) {
	if answer, ok := explicitAssignmentAnswer("¿Qué tareas tenía Fran?", "Fran tenía que dejar la web lista pronto.", nil); ok {
		t.Fatalf("narrative without explicit assignment structure was treated as a task list: %q", answer)
	}
}

func TestExplicitAssignmentAnswerAcceptsNotesAfterFormattingIsStripped(t *testing.T) {
	const question = "En la reunión de Stonecare del 14 de octubre, ¿quién debía enviar las imágenes y qué tareas tenía Fran para dejar la web lista?"
	const notes = `Próximos Pasos
- Enviar imágenes adaptadas a Fran para la home y profesionales (Paula, Alba)
  Incluir la foto con los dos productos para el primer slide.
- Aplicar cambios acordados y tener la web lista para el viernes (Fran)`

	answer, ok := explicitAssignmentAnswer(question, notes, nil)
	if !ok {
		t.Fatal("expected assignments to be extracted after Markdown formatting was stripped")
	}
	if !strings.Contains(answer, "Paula, Alba: Enviar imágenes") || strings.Contains(answer, "Fran: Enviar imágenes") || !strings.Contains(answer, "Fran: Aplicar cambios") {
		t.Fatalf("unexpected assignment attribution: %s", answer)
	}
}

func TestExplicitAssignmentAnswerExtractsTasksFromOwnerSection(t *testing.T) {
	const notes = `# Proyectos Fran
- Enviar la configuración de Active Directory a Minicor antes del viernes.
- Corregir el XLS de Amplify con error 404 durante la mañana.

# Proyectos Gonzalo
- Terminar la importación de localizaciones antes de revisar incidencias.`
	answer, ok := explicitAssignmentAnswer("¿Qué tareas tiene Fran y qué plazo aparece?", notes, nil)
	if !ok {
		t.Fatal("expected tasks under a person-specific project heading to be extracted")
	}
	for _, expected := range []string{"Fran: Enviar la configuración", "antes del viernes", "Fran: Corregir el XLS de Amplify", "durante la mañana"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("answer omitted %q: %s", expected, answer)
		}
	}
	if strings.Contains(answer, "Gonzalo") || strings.Contains(answer, "importación de localizaciones") {
		t.Fatalf("answer included another owner's task: %s", answer)
	}
}

func TestExplicitAssignmentAnswerExtractsOwnerPrefix(t *testing.T) {
	const notes = `# Próximos Pasos
- Fran: Subir los procedimientos de inventario durante la mañana.
- Paula: Enviar las imágenes adaptadas para la web.`
	answer, ok := explicitAssignmentAnswer("¿Qué debe hacer Fran?", notes, nil)
	if !ok {
		t.Fatal("expected an explicit owner prefix to be extracted")
	}
	if !strings.Contains(answer, "Fran: Subir los procedimientos de inventario") || strings.Contains(answer, "Paula:") {
		t.Fatalf("unexpected owner attribution: %s", answer)
	}
}
