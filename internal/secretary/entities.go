package secretary

import "strings"

// ResolveEntity only resolves exact external references or operator-approved aliases.
// Shared display names and pending aliases stay ambiguous by design.
func ResolveEntity(query string, entityType EntityType, items []KnowledgeItem) EntityResolution {
	needle := normalizeEntityAlias(query)
	if needle == "" {
		return EntityResolution{Status: EntityNotFound}
	}
	var exact, aliases, pending []KnowledgeItem
	for _, item := range items {
		if item.EntityType != entityType {
			continue
		}
		if item.Status == KnowledgeApproved {
			for _, external := range item.ExternalRefs {
				if strings.TrimSpace(external) != "" && strings.EqualFold(strings.TrimSpace(external), strings.TrimSpace(query)) {
					exact = appendUniqueKnowledge(exact, item)
					break
				}
			}
			if item.Type == KnowledgeEntityAlias && aliasesContain(item.Aliases, needle) {
				aliases = appendUniqueKnowledge(aliases, item)
			}
		} else if item.Status == KnowledgeCandidate && item.Type == KnowledgeEntityAlias && aliasesContain(item.Aliases, needle) {
			pending = appendUniqueKnowledge(pending, item)
		}
	}
	if len(exact) == 1 {
		return EntityResolution{Status: EntityResolved, Item: knowledgePointer(exact[0])}
	}
	if len(exact) > 1 {
		return ambiguousEntities(exact)
	}
	if len(aliases) == 1 {
		return EntityResolution{Status: EntityResolved, Item: knowledgePointer(aliases[0])}
	}
	if len(aliases) > 1 {
		return ambiguousEntities(aliases)
	}
	if len(pending) > 0 {
		return ambiguousEntities(pending)
	}
	return EntityResolution{Status: EntityNotFound}
}

func normalizeEntityAlias(value string) string {
	return strings.TrimSpace(normalizedIntentText(value))
}

func aliasesContain(aliases []string, needle string) bool {
	for _, alias := range aliases {
		if normalizeEntityAlias(alias) == needle {
			return true
		}
	}
	return false
}

func appendUniqueKnowledge(items []KnowledgeItem, item KnowledgeItem) []KnowledgeItem {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func knowledgePointer(item KnowledgeItem) *KnowledgeItem { return &item }

func ambiguousEntities(items []KnowledgeItem) EntityResolution {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.ID != "" {
			ids = append(ids, item.ID)
		}
	}
	return EntityResolution{Status: EntityAmbiguous, CandidateIDs: ids}
}
