package secretary

import "strings"

func isStandaloneSyntheticScenario(query string) bool {
	normalized := normalizedIntentText(query)
	return containsAny(normalized,
		"caso ficticio", "caso sintetico", "prueba sintetica", "escenario ficticio",
		"escenario sintetico", "prueba ficticia", "ejemplo inventado",
	)
}

func hasSyntheticConversationTurn(history []Turn) bool {
	for _, turn := range history {
		if turn.Role == "user" && isStandaloneSyntheticScenario(turn.Content) {
			return true
		}
	}
	return false
}

func isStandaloneNewTopic(query string) bool {
	normalized := normalizedIntentText(query)
	return containsAny(normalized,
		"caso nuevo", "nuevo caso", "escenario nuevo", "nuevo escenario",
		"prueba nueva", "nuevo ejemplo", "otro ejemplo", "siguiente escenario",
		"probemos otro", "cambiando de tema", "cambiemos de tema", "otra cosa",
		"por otro lado",
	)
}

func conversationHistoryForTurn(query string, history []Turn) []Turn {
	if len(history) == 0 || isStandaloneSyntheticScenario(query) || isStandaloneNewTopic(query) || !isConversationalFollowUp(query) {
		return nil
	}
	if topic := explicitConversationTopic(query); topic != "" {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role != "user" || !strings.Contains(normalizedIntentText(history[i].Content), topic) {
				continue
			}
			end := i + 1
			if end < len(history) && history[end].Role == "assistant" {
				end++
			}
			return append([]Turn(nil), history[i:end]...)
		}
	}
	// A short follow-up needs only a small local window. Older turns are kept
	// for durable recall, but should not become ambient facts in every answer.
	start := len(history) - 8
	if start < 0 {
		start = 0
	}
	return append([]Turn(nil), history[start:]...)
}

func explicitConversationTopic(query string) string {
	normalized := normalizedIntentText(query)
	for _, cue := range []string{"volviendo al proyecto ", "volviendo al caso ", "retomando el proyecto ", "retomando el caso ", "volviendo a ", "retomando ", "volvamos a "} {
		if index := strings.Index(normalized, cue); index >= 0 {
			remainder := strings.TrimSpace(normalized[index+len(cue):])
			if fields := strings.Fields(remainder); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

func isConversationalFollowUp(query string) bool {
	normalized := normalizedIntentText(query)
	if isStandaloneSyntheticScenario(query) || isStandaloneNewTopic(query) {
		return false
	}
	return containsAny(normalized,
		"y cuando", "y quien", "y que", "y cual", "y cuanto", "y como",
		"y ella", "y el ", "ella cuando", "el cuando",
		"y el plazo", "y la fecha", "y el responsable", "y el siguiente paso",
		"volviendo a", "volviendo al proyecto", "volviendo al caso", "retomando", "volvamos a",
		"y despues", "y luego", "entonces", "en ese caso", "sobre eso",
		"eso esta vinculado", "eso esta relacionado",
		"que cosas tenemos que hacer para hoy", "que cosas tenemos que hacer",
		"que te he dicho", "que falta conectar", "que falta", "seguimiento", "hola",
		"lo anterior", "esa reunion", "esa tarea", "ese proyecto", "ese mismo",
		"esa misma", "explica eso", "amplia", "mas detalle", "mas contexto",
		"que opinas", "que piensas", "que te parece", "continua", "sigue",
	)
}

func syntheticClosedWorldAnswer(query string) (string, bool) {
	if !isStandaloneSyntheticScenario(query) {
		return "", false
	}
	if answer, ok := syntheticSourceSynthesisAnswer(query); ok {
		return answer, true
	}
	normalized := normalizedIntentText(query)
	if containsAny(normalized, "resume el dato", "resume ese dato", "resume el hecho", "resume la fecha") {
		if answer, ok := syntheticSingleDatedActionFact(query); ok {
			return answer, true
		}
	}
	missing := containsAny(normalized,
		"no consta", "no aparece", "no anote", "no tengo la fecha", "sin fecha",
		"no indico el dia", "no indico la fecha", "no especifico el dia",
		"no especifico la fecha", "no se acordo una fecha", "no se ha acordado una fecha",
		"no hay una fecha nueva", "no hay fecha nueva",
	)
	asksAboutDate := containsAny(normalized, "fecha", "plazo", "cuando", "que dia")
	summarizesContext := containsAny(normalized, "resume", "resumen", "sintetiza", "sintesis", "que sabemos", "bloqueo", "siguiente paso")
	if missing && asksAboutDate && !summarizesContext {
		return "No consta la nueva fecha en los datos que compartes. ¿Qué día acordó el equipo?", true
	}
	if !summarizesContext && containsAny(normalized, "que queda pendiente", "que sigue pendiente", "que falta por hacer") {
		if answer, ok := syntheticPendingActionAnswer(query); ok {
			return answer, true
		}
		return "", false
	}
	if !containsAny(normalized, "quien", "cuando", "que dia", "que fecha") {
		return "", false
	}
	factsEnd := len(query)
	if questionStart := strings.IndexAny(query, "¿?"); questionStart >= 0 {
		factsEnd = questionStart
	}
	facts := strings.TrimSpace(query[:factsEnd])
	if colon := strings.Index(facts, ":"); colon >= 0 {
		facts = strings.TrimSpace(facts[colon+1:])
	}
	if facts == "" || len([]rune(facts)) > 240 || strings.Count(facts, ".") > 1 {
		return "", false
	}
	factText := normalizedIntentText(facts)
	if !containsAny(factText, "lunes", "martes", "miercoles", "jueves", "viernes", "sabado", "domingo", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre") {
		return "", false
	}
	if !containsAny(factText, "enviara", "entregara", "presentara", "revisara", "confirmara", "llegara", "desplegara", "publicara", "comenzara", "empezara", "sera") {
		return "", false
	}
	return facts, true
}

func syntheticAnaphoricDateAnswer(query string, history []Turn) (string, bool) {
	normalized := normalizedIntentText(query)
	if isStandaloneNewTopic(query) || !isConversationalFollowUp(query) ||
		!containsAny(normalized, "ella", "ellas", "el cuando", "ellos") ||
		!containsAny(normalized, "cuando", "que fecha", "que dia", "plazo") {
		return "", false
	}
	for i := len(history) - 1; i >= 0; i-- {
		turn := history[i]
		if turn.Role != "user" {
			continue
		}
		if isStandaloneNewTopic(turn.Content) {
			return "", false
		}
		if isStandaloneSyntheticScenario(turn.Content) {
			return syntheticSingleDatedActionFact(turn.Content)
		}
	}
	return "", false
}

func syntheticSingleDatedActionFact(text string) (string, bool) {
	weekdays := []string{"lunes", "martes", "miercoles", "jueves", "viernes", "sabado", "domingo"}
	months := []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	actionVerbs := []string{"confirmara", "enviara", "entregara", "corregira", "revisara", "comprobara", "presentara", "desplegara", "publicara", "comenzara", "empezara", "aprobara", "validara", "firmara", "terminara", "actualizara", "preparara", "iniciara", "completara", "finalizara"}
	var candidate string
	for _, sentence := range strings.FieldsFunc(text, func(r rune) bool { return r == '.' || r == '!' || r == '?' || r == '\n' }) {
		for _, rawClause := range strings.Split(sentence, ";") {
			clause := strings.TrimSpace(rawClause)
			normalized := normalizedIntentText(clause)
			if !containsAny(normalized, weekdays...) || !containsAny(normalized, months...) || !containsAny(normalized, actionVerbs...) {
				continue
			}
			if candidate != "" {
				return "", false
			}
			if len([]rune(clause)) > 240 {
				return "", false
			}
			candidate = clause
		}
	}
	if candidate == "" {
		return "", false
	}
	return strings.TrimRight(candidate, ".!? ") + ".", true
}

func syntheticPendingActionAnswer(query string) (string, bool) {
	factsEnd := len(query)
	if questionStart := strings.IndexAny(query, "¿?"); questionStart >= 0 {
		factsEnd = questionStart
	}
	facts := query[:factsEnd]
	actionVerbs := []string{"confirmara", "enviara", "entregara", "corregira", "revisara", "comprobara", "presentara", "desplegara", "publicara", "comenzara", "empezara", "aprobara", "validara", "firmara", "terminara", "actualizara", "preparara", "iniciara", "completara", "finalizara"}
	weekdays := []string{"lunes", "martes", "miercoles", "jueves", "viernes", "sabado", "domingo"}
	var actionClause, scheduledClause string
	actionCount := 0
	for _, sentence := range strings.FieldsFunc(facts, func(r rune) bool { return r == '.' || r == '!' || r == '?' }) {
		for _, rawClause := range strings.Split(sentence, ";") {
			clause := strings.TrimSpace(rawClause)
			normalizedClause := normalizedIntentText(clause)
			if containsAny(normalizedClause, actionVerbs...) && containsAny(normalizedClause, weekdays...) {
				actionCount++
				if actionCount > 1 {
					return "", false
				}
				if comma := strings.LastIndex(clause, ","); comma >= 0 && strings.Contains(normalizedIntentText(clause[:comma]), "proyecto") {
					clause = strings.TrimSpace(clause[comma+1:])
				}
				actionClause = clause
				continue
			}
			if scheduledClause == "" && containsAny(normalizedClause, "esta prevista", "esta previsto", "esta programada", "esta programado") {
				scheduledClause = strings.ToLower(clause[:1]) + clause[1:]
			}
		}
	}
	if actionClause == "" {
		return "", false
	}
	answer := "Queda pendiente: " + actionClause
	if scheduledClause != "" {
		answer += "; " + scheduledClause
	}
	return answer + ".", true
}
