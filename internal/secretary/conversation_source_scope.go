package secretary

import (
	"strings"
)

func conversationSourceExclusions(query string, history []Turn, capabilities []ReadCapability) map[string]struct{} {
	if !isConversationalFollowUp(query) || isStandaloneNewTopic(query) {
		return nil
	}

	var exclusions map[string]struct{}
	for i := len(history) - 1; i >= 0; i-- {
		turn := history[i]
		if turn.Role != "user" {
			continue
		}
		if isStandaloneNewTopic(turn.Content) {
			return nil
		}
		exclusions = explicitSourceExclusions(turn.Content, capabilities)
		if len(exclusions) > 0 {
			break
		}
		if isStandaloneSyntheticScenario(turn.Content) {
			return nil
		}
	}
	if len(exclusions) == 0 {
		return nil
	}

	for capability := range exclusions {
		if explicitlyRequestsCapabilityRead(query, capability) {
			delete(exclusions, capability)
		}
	}
	return exclusions
}

func explicitSourceExclusions(query string, capabilities []ReadCapability) map[string]struct{} {
	exclusions := make(map[string]struct{})
	if explicitlyExcludesExternalSources(query) {
		for _, capability := range capabilities {
			if capability.Name != "" && capability.Read != nil {
				exclusions[strings.ToLower(strings.TrimSpace(capability.Name))] = struct{}{}
			}
		}
		return exclusions
	}
	for _, capability := range capabilities {
		if capability.Name != "" && capability.Read != nil && (explicitlyExcludedCapability(query, capability.Name) || excludedByNegatedConjunction(query, capability.Name)) {
			exclusions[strings.ToLower(strings.TrimSpace(capability.Name))] = struct{}{}
		}
	}
	return exclusions
}

func explicitlyExcludesExternalSources(query string) bool {
	normalized := normalizedIntentText(query)
	return containsAny(normalized,
		"no uses datos externos", "no utilices datos externos", "sin datos externos",
		"ni uses datos externos", "ni utilices datos externos",
		"no uses fuentes externas", "no consultes fuentes externas", "sin fuentes externas",
		"ni uses fuentes externas", "ni consultes fuentes externas",
		"no uses fuentes reales", "no consultes fuentes reales", "sin fuentes reales",
	)
}

func excludedByNegatedConjunction(query, capability string) bool {
	normalized := normalizedIntentText(query)
	name := strings.ToLower(strings.TrimSpace(capability))
	return strings.Contains(normalized, " ni "+name) || strings.Contains(normalized, " ni "+name+" ")
}

func explicitlyRequestsCapabilityRead(query, capability string) bool {
	if explicitlyExcludedCapability(query, capability) {
		return false
	}
	if strings.EqualFold(explicitlyRequestedCapabilityOnly(query), capability) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(capability), "Jira") && explicitJiraVerificationFollowUp(query) {
		return true
	}

	normalized := normalizedIntentText(query)
	name := strings.ToLower(strings.TrimSpace(capability))
	if !strings.Contains(normalized, name) {
		return false
	}
	return containsAny(normalized,
		"consulta "+name, "consultar "+name, "busca en "+name, "buscar en "+name,
		"verifica en "+name, "verificar en "+name, "comprueba en "+name,
		"revisa "+name, "revisa en "+name, "confirma en "+name,
		"que dice "+name, "que pone "+name, "que consta en "+name,
		"estado de "+name, "estado actual de "+name,
	)
}

func scopeContainsCapability(scope map[string]struct{}, capability string) bool {
	_, ok := scope[strings.ToLower(strings.TrimSpace(capability))]
	return ok
}

func scopeBlocksAllCapabilities(scope map[string]struct{}, capabilities []ReadCapability) bool {
	count := 0
	for _, capability := range capabilities {
		if capability.Name == "" || capability.Read == nil {
			continue
		}
		count++
		if !scopeContainsCapability(scope, capability.Name) {
			return false
		}
	}
	return count > 0
}

func filterByConversationSourceScope(selected []ReadCapability, scope map[string]struct{}) []ReadCapability {
	if len(scope) == 0 || len(selected) == 0 {
		return selected
	}
	filtered := make([]ReadCapability, 0, len(selected))
	for _, capability := range selected {
		if scopeContainsCapability(scope, capability.Name) {
			continue
		}
		filtered = append(filtered, capability)
	}
	return filtered
}
