package secretary

import (
	"sort"
	"strings"
	"time"
)

const approvedKnowledgeLimit = 5

func (s *Service) relevantKnowledge(operatorID int64, query string, at time.Time) ([]KnowledgeItem, error) {
	if s.Learning == nil {
		return nil, nil
	}
	items, err := s.Learning.ListKnowledge(operatorID, KnowledgeApproved)
	if err != nil {
		return nil, err
	}
	terms := memoryTerms(query)
	type ranked struct {
		item  KnowledgeItem
		score int
	}
	rankedItems := make([]ranked, 0, len(items))
	for _, item := range items {
		if item.OperatorID != operatorID || item.Status != KnowledgeApproved {
			continue
		}
		if !item.ValidFrom.IsZero() && item.ValidFrom.After(at) {
			continue
		}
		if !item.ValidUntil.IsZero() && !item.ValidUntil.After(at) {
			continue
		}
		text := strings.Join(append([]string{item.Subject, item.Predicate, item.Value}, item.Aliases...), " ")
		for _, value := range item.ExternalRefs {
			text += " " + value
		}
		itemTerms := map[string]bool{}
		for _, term := range memoryTerms(text) {
			itemTerms[term] = true
		}
		score := 0
		for _, term := range terms {
			if itemTerms[term] {
				score++
			}
		}
		if score > 0 {
			rankedItems = append(rankedItems, ranked{item: item, score: score})
		}
	}
	sort.SliceStable(rankedItems, func(i, j int) bool {
		if rankedItems[i].score != rankedItems[j].score {
			return rankedItems[i].score > rankedItems[j].score
		}
		return rankedItems[i].item.UpdatedAt.After(rankedItems[j].item.UpdatedAt)
	})
	if len(rankedItems) > approvedKnowledgeLimit {
		rankedItems = rankedItems[:approvedKnowledgeLimit]
	}
	result := make([]KnowledgeItem, 0, len(rankedItems))
	for _, ranked := range rankedItems {
		result = append(result, ranked.item)
	}
	return result, nil
}

func knowledgeAsMemories(items []KnowledgeItem) []conversationMemory {
	memories := make([]conversationMemory, 0, len(items))
	for _, item := range items {
		at := item.UpdatedAt
		if at.IsZero() {
			at = item.CreatedAt
		}
		memories = append(memories, conversationMemory{At: at, User: item.Subject, Assistant: item.Value, Kind: "approved_knowledge", Sources: append([]EvidenceRef(nil), item.Sources...), ValidFrom: item.ValidFrom, ValidUntil: item.ValidUntil})
	}
	return memories
}
