package semanticir

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/semantic"
)

const Version = "semantic-runtime-ir/v1"

type CompileRequest struct {
	Mode       string
	AgentType  string
	Chunks     []domain.ContextChunk
	Policies   []string
	SourceRefs []string
	MaxChars   int
}

func Compile(req CompileRequest) *domain.OperationalIR {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = strings.ToUpper(strings.TrimSpace(req.AgentType))
	}
	ir := &domain.OperationalIR{
		Version:         Version,
		Mode:            mode,
		Confidence:      "unknown",
		Trusted:         []domain.OperationalItem{},
		Invalid:         []domain.OperationalItem{},
		Constraints:     []string{},
		Dependencies:    []string{},
		Policies:        uniqueStrings(req.Policies),
		Flows:           []string{},
		Approvals:       []string{},
		Risks:           []string{},
		ValidationRules: []string{},
		JSONata: domain.JSONataIR{
			Selectors: map[string]string{
				"trusted":      "$.trusted[outcome='trusted']",
				"invalid":      "$.invalid[outcome in ['failed','rejected','invalid']]",
				"source_refs":  "$.source_refs",
				"constraints":  "$.constraints",
				"dependencies": "$.dependencies",
			},
			Conditions: map[string]string{
				"requires_approval": "$count($.approvals) > 0",
				"has_invalid_prior": "$count($.invalid) > 0",
			},
			Projections: map[string]string{
				"execution_context": "{'constraints': $.constraints, 'dependencies': $.dependencies, 'policies': $.policies, 'approvals': $.approvals}",
			},
		},
		SourceRefs: uniqueStrings(req.SourceRefs),
	}
	for _, chunk := range req.Chunks {
		item := buildItem(chunk)
		switch item.Outcome {
		case "trusted":
			ir.Trusted = append(ir.Trusted, item)
		case "failed", "rejected", "invalid":
			ir.Invalid = append(ir.Invalid, item)
		default:
			if item.FailureReason != "" {
				item.Outcome = "failed"
				ir.Invalid = append(ir.Invalid, item)
			} else {
				ir.Trusted = append(ir.Trusted, item)
			}
		}
		ir.Constraints = append(ir.Constraints, stringList(chunk.Metadata["constraints"])...)
		ir.Dependencies = append(ir.Dependencies, stringList(chunk.Metadata["dependencies"])...)
		ir.Approvals = append(ir.Approvals, stringList(chunk.Metadata["approvals"])...)
		ir.Risks = append(ir.Risks, stringList(chunk.Metadata["risks"])...)
		ir.ValidationRules = append(ir.ValidationRules, stringList(chunk.Metadata["validation_rules"])...)
		if rule := validationRuleFromItem(item); rule != "" {
			ir.ValidationRules = append(ir.ValidationRules, rule)
		}
		if flow := flowFromMetadata(chunk.Metadata); flow != "" {
			ir.Flows = append(ir.Flows, flow)
		}
		if approval := approvalFromMetadata(chunk.Metadata); approval != "" {
			ir.Approvals = append(ir.Approvals, approval)
		}
	}
	ir.Constraints = uniqueStrings(ir.Constraints)
	ir.Dependencies = uniqueStrings(ir.Dependencies)
	ir.Policies = uniqueStrings(ir.Policies)
	ir.Flows = uniqueStrings(ir.Flows)
	ir.Approvals = uniqueStrings(ir.Approvals)
	ir.Risks = uniqueStrings(ir.Risks)
	ir.ValidationRules = uniqueStrings(ir.ValidationRules)
	ir.Confidence = aggregateConfidence(ir.Trusted, ir.Invalid)
	if req.MaxChars > 0 {
		trimToBudget(ir, req.MaxChars)
	}
	return ir
}

func buildItem(chunk domain.ContextChunk) domain.OperationalItem {
	meta := semantic.SanitizeMap(chunk.Metadata)
	outcome := stringMetadata(meta, "outcome")
	if outcome == "" {
		outcome = "unknown"
	}
	summary := stringMetadata(meta, "summary")
	if summary == "" {
		summary = firstUsefulLine(chunk.ContentText)
	}
	confidence, hasConfidence := numericMetadata(meta["confidence"])
	var confidencePtr *float64
	if hasConfidence {
		confidencePtr = &confidence
	}
	return domain.OperationalItem{
		SourceRef:     chunk.SourceRef,
		SourceType:    chunk.SourceType,
		SourceID:      chunk.SourceID,
		Outcome:       strings.ToLower(strings.TrimSpace(outcome)),
		Confidence:    confidencePtr,
		Summary:       semantic.TruncateChars(summary, 320),
		AgentType:     stringMetadata(meta, "agent_type"),
		TaskType:      stringMetadata(meta, "task_type"),
		FailureReason: semantic.TruncateChars(stringMetadata(meta, "failure_reason"), 320),
		Metadata:      compactMetadata(meta),
	}
}

func validationRuleFromItem(item domain.OperationalItem) string {
	if item.FailureReason == "" {
		return ""
	}
	return fmt.Sprintf("Avoid repeating %s from %s: %s", item.Outcome, item.SourceRef, item.FailureReason)
}

func flowFromMetadata(meta map[string]any) string {
	agent := stringMetadata(meta, "agent_type")
	target := stringMetadata(meta, "execution_target")
	if agent == "" && target == "" {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("agent=%s target=%s", agent, target))
}

func approvalFromMetadata(meta map[string]any) string {
	value := meta["approval_required"]
	switch typed := value.(type) {
	case bool:
		if typed {
			return "approval_required=true"
		}
	case string:
		if strings.EqualFold(strings.TrimSpace(typed), "true") {
			return "approval_required=true"
		}
	}
	return ""
}

func firstUsefulLine(text string) string {
	for _, line := range strings.Split(semantic.SanitizeText(text), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Metadata:") {
			return line
		}
	}
	return ""
}

func stringMetadata(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	switch typed := meta[key].(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func stringList(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []any:
		for _, item := range typed {
			if trimmed := strings.TrimSpace(fmt.Sprint(item)); trimmed != "" && trimmed != "<nil>" {
				out = append(out, trimmed)
			}
		}
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func numericMetadata(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func compactMetadata(meta map[string]any) map[string]any {
	keys := []string{"phase", "decision", "task_type", "agent_type", "planned_agent", "execution_target", "repo", "validated"}
	out := map[string]any{}
	for _, key := range keys {
		if value, ok := meta[key]; ok {
			out[key] = value
		}
	}
	return out
}

func uniqueStrings(input []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func aggregateConfidence(trusted, invalid []domain.OperationalItem) string {
	total := 0
	high := 0
	for _, item := range append(trusted, invalid...) {
		if item.Confidence == nil {
			continue
		}
		total++
		if *item.Confidence >= 0.8 {
			high++
		}
	}
	switch {
	case total == 0:
		return "unknown"
	case high == total:
		return "high"
	case high > 0:
		return "mixed"
	default:
		return "low"
	}
}

func trimToBudget(ir *domain.OperationalIR, maxChars int) {
	for estimateChars(ir) > maxChars && len(ir.Trusted) > 0 {
		ir.Trusted = ir.Trusted[:len(ir.Trusted)-1]
	}
	for estimateChars(ir) > maxChars && len(ir.Invalid) > 0 {
		ir.Invalid = ir.Invalid[:len(ir.Invalid)-1]
	}
	for estimateChars(ir) > maxChars && len(ir.Risks) > 0 {
		ir.Risks = ir.Risks[:len(ir.Risks)-1]
	}
}

func estimateChars(ir *domain.OperationalIR) int {
	raw, err := json.Marshal(ir)
	if err != nil {
		return 0
	}
	return len(raw)
}
