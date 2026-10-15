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

func TestExplicitAssignmentAnswerInheritsOwnerFromProject(t *testing.T) {
	const notes = `# Proyectos Fran
- SiteWatchman: revisar exportación de inventario.

# Próximos Pasos
- Corregir la regla XLS de SiteWatchman antes del viernes.
- Revisar textos de la web (Iris).`
	answer, ok := explicitAssignmentAnswer("¿Qué tareas tiene Fran y qué plazo aparece?", notes, nil)
	if !ok {
		t.Fatal("expected a next step to inherit its matching project owner")
	}
	for _, expected := range []string{"Fran: Corregir la regla XLS", "antes del viernes"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("answer omitted %q: %s", expected, answer)
		}
	}
	if strings.Contains(answer, "Iris:") || strings.Contains(answer, "Revisar textos") {
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

func TestExplicitAssignmentAnswerExtractsOwnerBulletsWithoutHeading(t *testing.T) {
	const notes = `- Fran: Subir los procedimientos de inventario durante la mañana.
- Paula: Enviar las imágenes adaptadas para la web.`
	answer, ok := explicitAssignmentAnswer("¿Qué debe hacer Fran?", notes, nil)
	if !ok {
		t.Fatal("expected owner-labelled task bullets to be extracted without a section heading")
	}
	if !strings.Contains(answer, "Fran: Subir los procedimientos") || strings.Contains(answer, "Paula:") {
		t.Fatalf("unexpected owner attribution: %s", answer)
	}
}

func TestExplicitAssignmentAnswerMapsNextStepsToProjectOwners(t *testing.T) {
	const notes = `# Proyectos Alex
- ClienteX: revisión de presentación y seguridad.
  - Pendiente: enviar correos con configuración técnica a la responsable.
- SistemaY (Gonzalo): revisar importación de inventario.
  - Falta corregir regla de descarga XLS que da 404 en producción.
  - Subir unos 15 procedimientos de inventario durante la mañana.

# Próximos Pasos
- Enviar correos de configuración a ClienteX.
- Corregir la regla de descarga XLS en SistemaY.
- Subir inventario de procedimientos en SistemaY, objetivo dejarlo listo durante la mañana.
- Blindar la migración de Payroll para producción el lunes 19 de octubre (Valentina).
- Atacar incidencias sin ubicación tras terminar importación (Gonzalo).`
	answer, ok := explicitAssignmentAnswer("¿Qué tareas tiene Alex y qué plazos aparecen?", notes, nil)
	if !ok {
		t.Fatal("expected project next steps to inherit their explicit project owner")
	}
	for _, expected := range []string{"Alex: Enviar correos", "Alex: Corregir la regla", "Alex: Subir inventario", "durante la mañana"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("answer omitted %q: %s", expected, answer)
		}
	}
	for _, excluded := range []string{"Gonzalo:", "Valentina:", "ClienteX: revisión", "Pendiente: enviar"} {
		if strings.Contains(answer, excluded) {
			t.Errorf("answer included a project update or another owner's task %q: %s", excluded, answer)
		}
	}
}

func TestExplicitAssignmentAnswerOwnerFilterExcludesOtherOwnersMatchingDateTerms(t *testing.T) {
	const notes = `# Proyectos Fran
- SiteWatchman: corregir la regla de descarga XLS.

# Próximos Pasos
- Corregir la regla XLS de SiteWatchman antes del viernes.
- Blindar migración Payroll para producción el lunes 19 de octubre (Valentina).`
	answer, ok := explicitAssignmentAnswer("¿Qué tareas quedaron a cargo de Fran en la reunión del 13 de octubre y qué plazos se mencionaron?", notes, nil)
	if !ok {
		t.Fatal("expected Fran's assigned action to be extracted")
	}
	if !strings.Contains(answer, "Fran: Corregir la regla XLS") || !strings.Contains(answer, "antes del viernes") {
		t.Fatalf("answer omitted Fran's action or deadline: %s", answer)
	}
	if strings.Contains(answer, "Valentina") || strings.Contains(answer, "Payroll") {
		t.Fatalf("answer leaked another owner's task because it shared the requested date: %s", answer)
	}
}

func TestExplicitAssignmentAnswerUsesProjectTermsToNarrowOwnerActions(t *testing.T) {
	const notes = `# Proyectos Fran
- Minicor: preparar configuración de Active Directory.
- SiteWatchman: corregir la regla de descarga XLS.

# Próximos Pasos
- Enviar correos de configuración de Active Directory a Minicor.
- Corregir la regla XLS de SiteWatchman antes del viernes.
- Subir inventario de procedimientos en SiteWatchman durante la mañana.`
	answer, ok := explicitAssignmentAnswer("Según la reunión del 13 de octubre, ¿qué debía enviar Fran a la responsable de seguridad de Minicor?", notes, nil)
	if !ok {
		t.Fatal("expected the Minicor assignment to be extracted")
	}
	if !strings.Contains(answer, "Enviar correos de configuración") || !strings.Contains(answer, "Minicor") {
		t.Fatalf("answer omitted the requested Minicor action: %s", answer)
	}
	if strings.Contains(answer, "SiteWatchman") {
		t.Fatalf("answer included unrelated SiteWatchman actions: %s", answer)
	}
}

func TestExplicitAssignmentAnswerReturnsAllOwnersWhenAskedToGroupActions(t *testing.T) {
	const notes = `# Proyectos Fran
- Minicor: preparar configuración de Active Directory.

# Próximos Pasos
- Enviar correos de configuración de Active Directory a Minicor.
- Blindar migración Payroll para producción el lunes 19 de octubre (Valentina).`
	answer, ok := explicitAssignmentAnswer("¿Qué tareas se acordaron en la reunión del 13 de octubre, agrupadas por responsable?", notes, nil)
	if !ok {
		t.Fatal("expected a broad grouped task question to use structured assignments")
	}
	for _, expected := range []string{"Fran: Enviar correos", "Valentina: Blindar migración Payroll"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("grouped answer omitted %q: %s", expected, answer)
		}
	}
}

func TestExplicitAssignmentAnswerExtractsUploadQuestionWithDeadline(t *testing.T) {
	const notes = `# Proyectos Fran
- SiteWatchman: subir unos 15-16 procedimientos de inventario recibidos el viernes.

# Próximos Pasos
- Fran: Subir inventario de procedimientos en SiteWatchman.
	Unos 15-16 procedimientos recibidos el viernes, objetivo dejarlo listo durante la mañana.
- Fran: Arreglar regla de Amplify para descarga de ficheros XLS en SiteWatchman.`
	answer, ok := explicitAssignmentAnswer("Según Granola, ¿qué tenía que subir Fran a SiteWatchman el 13 de octubre y para cuándo?", notes, nil)
	if !ok {
		t.Fatal("expected the upload task question to use structured assignments")
	}
	for _, expected := range []string{"Fran: Subir inventario", "durante la mañana"} {
		if !strings.Contains(answer, expected) {
			t.Errorf("structured answer omitted %q: %s", expected, answer)
		}
	}
	for _, unrelated := range []string{"incidencias sin ubicación", "Arreglar regla de Amplify"} {
		if strings.Contains(answer, unrelated) {
			t.Fatalf("structured answer included unrelated task %q: %s", unrelated, answer)
		}
	}
}
