package initiative

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

type ExecutionPolicy struct {
	WorkspaceRoot         string
	Scope                 string
	AllowedModes          []string
	ApprovalRequiredModes []string
}

func ValidateRequirementsPayload(payload map[string]any) error {
	requiredStrings := []string{"objective"}
	requiredLists := []string{"scope", "out_of_scope", "constraints", "risks", "acceptance_criteria", "open_questions", "assumptions"}
	for _, key := range requiredStrings {
		if strings.TrimSpace(asString(payload[key])) == "" {
			return fmt.Errorf("requirements payload missing %s", key)
		}
	}
	for _, key := range requiredLists {
		if len(normalizedStringSlice(payload[key])) == 0 {
			return fmt.Errorf("requirements payload missing non-empty %s", key)
		}
	}
	return nil
}

func ValidateDesignPayload(payload map[string]any) error {
	requiredStrings := []string{"architecture"}
	requiredValues := []string{"components", "interfaces", "data_model", "testing_strategy", "technical_risks", "pending_decisions"}
	for _, key := range requiredStrings {
		if strings.TrimSpace(asString(payload[key])) == "" {
			return fmt.Errorf("design payload missing %s", key)
		}
	}
	for _, key := range requiredValues {
		if isEmptyStructuredValue(payload[key]) {
			return fmt.Errorf("design payload missing non-empty %s", key)
		}
	}
	return nil
}

func ValidateExecutionPlanPayload(payload map[string]any) error {
	epics := normalizedMapSlice(payload["epics"])
	if len(epics) == 0 {
		return fmt.Errorf("execution plan missing epics")
	}
	for epicIdx, epic := range epics {
		if strings.TrimSpace(asString(epic["name"])) == "" {
			return fmt.Errorf("execution plan epic %d missing name", epicIdx)
		}
		tasks := normalizedMapSlice(epic["tasks"])
		if len(tasks) == 0 {
			return fmt.Errorf("execution plan epic %q has no tasks", asString(epic["name"]))
		}
		for taskIdx, task := range tasks {
			if strings.TrimSpace(asString(task["title"])) == "" {
				return fmt.Errorf("execution plan epic %q task %d missing title", asString(epic["name"]), taskIdx)
			}
			if strings.TrimSpace(asString(task["description"])) == "" {
				return fmt.Errorf("execution plan task %q missing description", asString(task["title"]))
			}
			agent := strings.TrimSpace(asString(task["suggested_agent"]))
			switch agent {
			case string(domain.AgentTypePlanner), string(domain.AgentTypeResearcher), string(domain.AgentTypeCoder), string(domain.AgentTypeReviewer):
			default:
				return fmt.Errorf("execution plan task %q has invalid suggested_agent %q", asString(task["title"]), agent)
			}
			mode := strings.TrimSpace(asString(task["execution_mode"]))
			if !domain.IsRecognizedTaskLaunchMode(mode) {
				return fmt.Errorf("execution plan task %q has invalid execution_mode %q", asString(task["title"]), mode)
			}
			target := strings.TrimSpace(asString(task["execution_target"]))
			if target != string(domain.ExecutionTargetLocal) && target != string(domain.ExecutionTargetRemote) {
				return fmt.Errorf("execution plan task %q has invalid execution_target %q", asString(task["title"]), target)
			}
			if strings.TrimSpace(asString(task["definition_of_done"])) == "" {
				return fmt.Errorf("execution plan task %q missing definition_of_done", asString(task["title"]))
			}
		}
	}
	return nil
}

func ResolveExecutionPolicy(orchestratorWorkspaceRoot, workspaceRoot string) ExecutionPolicy {
	orchestratorWorkspaceRoot = filepath.Clean(strings.TrimSpace(orchestratorWorkspaceRoot))
	workspaceRoot = filepath.Clean(strings.TrimSpace(workspaceRoot))
	policy := ExecutionPolicy{
		WorkspaceRoot:         workspaceRoot,
		ApprovalRequiredModes: []string{},
	}
	if workspaceRoot != "" && orchestratorWorkspaceRoot != "" &&
		(workspaceRoot == orchestratorWorkspaceRoot || strings.HasPrefix(workspaceRoot, orchestratorWorkspaceRoot+string(filepath.Separator))) {
		policy.Scope = "orchestrator_remote"
		policy.AllowedModes = []string{domain.TaskLaunchModeManual, domain.TaskLaunchModeAgentRemote}
		return policy
	}
	policy.Scope = "local_bridge"
	policy.AllowedModes = []string{domain.TaskLaunchModeManual, domain.TaskLaunchModeAgentLocal}
	return policy
}

func (p ExecutionPolicy) AllowsMode(mode string) bool {
	for _, candidate := range p.AllowedModes {
		if strings.TrimSpace(candidate) == strings.TrimSpace(mode) {
			return true
		}
	}
	return false
}

func ValidateTaskLaunchAgainstPolicy(policy ExecutionPolicy, link domain.InitiativeTaskLinkResponse, mode string) error {
	mode = strings.TrimSpace(mode)
	if !policy.AllowsMode(mode) {
		return fmt.Errorf("execution mode %q is not allowed for workspace scope %s", mode, policy.Scope)
	}
	if mode != domain.TaskLaunchModeManual && asBool(link.Task.Metadata["approval_required"]) {
		return fmt.Errorf("task %s requires manual approval before automatic launch", link.TaskID)
	}
	return nil
}

func SummarizeStructuredDiff(previous, next map[string]any) string {
	if len(previous) == 0 {
		return "initial version"
	}
	added := []string{}
	changed := []string{}
	removed := []string{}
	seen := map[string]struct{}{}
	for key := range previous {
		seen[key] = struct{}{}
	}
	for key := range next {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		prev, prevOK := previous[key]
		curr, currOK := next[key]
		switch {
		case !prevOK && currOK:
			added = append(added, key)
		case prevOK && !currOK:
			removed = append(removed, key)
		default:
			prevRaw, _ := json.Marshal(prev)
			currRaw, _ := json.Marshal(curr)
			if string(prevRaw) != string(currRaw) {
				changed = append(changed, key)
			}
		}
	}
	parts := make([]string, 0, 3)
	if len(changed) > 0 {
		parts = append(parts, "changed: "+strings.Join(changed, ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed: "+strings.Join(removed, ", "))
	}
	if len(parts) == 0 {
		return "no semantic changes"
	}
	return strings.Join(parts, " | ")
}

func normalizedStringSlice(value any) []string {
	out := []string{}
	switch items := value.(type) {
	case []string:
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	case []any:
		for _, item := range items {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

func isEmptyStructuredValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []string:
		return len(normalizedStringSlice(typed)) == 0
	case []any:
		return len(normalizedStringSlice(typed)) == 0 && len(normalizedMapSlice(typed)) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		raw := strings.TrimSpace(fmt.Sprint(typed))
		return raw == "" || raw == "<nil>"
	}
}

func asBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}
