package memory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

const (
	DefaultPrecedentLimit    = 5
	DefaultPerClassLimit     = 2
	DefaultChunkSummaryChars = 180
	DefaultPacketBudgetChars = 2400
)

func NormalizeMemoryClass(raw string) string {
	switch strings.TrimSpace(raw) {
	case domain.MemoryClassRepoSpecific,
		domain.MemoryClassTechnologySimilar,
		domain.MemoryClassPatternSimilar,
		domain.MemoryClassNegativeGuardrail,
		domain.MemoryClassRecentExecution,
		domain.MemoryClassPlanningContext:
		return strings.TrimSpace(raw)
	default:
		return domain.MemoryClassRepoSpecific
	}
}

func CompactText(text string, maxChars int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = DefaultChunkSummaryChars
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return strings.TrimSpace(string(runes[:maxChars-3])) + "..."
}

func BuildSelectionReason(className string, score *float64) string {
	className = NormalizeMemoryClass(className)
	label := strings.ReplaceAll(className, "_", " ")
	if score != nil {
		return fmt.Sprintf("selected as %s evidence (score %.2f)", label, *score)
	}
	return fmt.Sprintf("selected as %s evidence", label)
}

func SelectRetrievalHits(chunks []domain.ContextChunk, limit, perClassLimit int) ([]domain.RetrievalHit, map[string]string, []string) {
	if limit <= 0 {
		limit = DefaultPrecedentLimit
	}
	if perClassLimit <= 0 {
		perClassLimit = DefaultPerClassLimit
	}
	type candidate struct {
		chunk    domain.ContextChunk
		score    float64
		priority int
	}
	candidates := make([]candidate, 0, len(chunks))
	for _, chunk := range chunks {
		className := chunk.MemoryClass
		if className == "" && chunk.Metadata != nil {
			className = strings.TrimSpace(asString(chunk.Metadata["memory_class"]))
		}
		className = NormalizeMemoryClass(className)
		if className == domain.MemoryClassNegativeGuardrail {
			continue
		}
		score := -1.0
		if chunk.Score != nil {
			score = *chunk.Score
		}
		candidates = append(candidates, candidate{
			chunk:    chunk,
			score:    score,
			priority: memoryClassPriority(className),
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			if candidates[i].priority == candidates[j].priority {
				return strings.TrimSpace(candidates[i].chunk.SourceRef) < strings.TrimSpace(candidates[j].chunk.SourceRef)
			}
			return candidates[i].priority < candidates[j].priority
		}
		return candidates[i].score > candidates[j].score
	})

	perClass := map[string]int{}
	seenRefs := map[string]struct{}{}
	hits := make([]domain.RetrievalHit, 0, min(limit, len(candidates)))
	compactSummaries := map[string]string{}
	why := make([]string, 0, limit)
	for _, item := range candidates {
		className := item.chunk.MemoryClass
		if className == "" && item.chunk.Metadata != nil {
			className = strings.TrimSpace(asString(item.chunk.Metadata["memory_class"]))
		}
		className = NormalizeMemoryClass(className)
		sourceRef := strings.TrimSpace(item.chunk.SourceRef)
		if sourceRef == "" {
			sourceRef = strings.TrimSpace(item.chunk.SourceType) + ":" + strings.TrimSpace(item.chunk.SourceID)
		}
		if sourceRef == ":" {
			continue
		}
		if _, exists := seenRefs[sourceRef]; exists {
			continue
		}
		if perClass[className] >= perClassLimit {
			continue
		}
		summary := CompactText(item.chunk.ContentText, DefaultChunkSummaryChars)
		reason := BuildSelectionReason(className, item.chunk.Score)
		hits = append(hits, domain.RetrievalHit{
			SourceRef:       sourceRef,
			SourceType:      strings.TrimSpace(item.chunk.SourceType),
			SourceID:        strings.TrimSpace(item.chunk.SourceID),
			Score:           item.chunk.Score,
			MemoryClass:     className,
			InitiativeID:    item.chunk.InitiativeID,
			TaskID:          item.chunk.TaskID,
			ArtifactID:      item.chunk.ArtifactID,
			Summary:         summary,
			SelectionReason: reason,
		})
		perClass[className]++
		seenRefs[sourceRef] = struct{}{}
		if _, ok := compactSummaries[className]; !ok {
			compactSummaries[className] = summary
		}
		why = append(why, fmt.Sprintf("%s: %s", sourceRef, reason))
		if len(hits) >= limit {
			break
		}
	}
	if len(hits) == 0 {
		return nil, nil, nil
	}
	return hits, compactSummaries, why
}

func memoryClassPriority(className string) int {
	switch NormalizeMemoryClass(className) {
	case domain.MemoryClassRepoSpecific:
		return 0
	case domain.MemoryClassRecentExecution:
		return 1
	case domain.MemoryClassPatternSimilar:
		return 2
	case domain.MemoryClassTechnologySimilar:
		return 3
	case domain.MemoryClassPlanningContext:
		return 4
	default:
		return 10
	}
}

func asString(value any) string {
	if raw, ok := value.(string); ok {
		return raw
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
