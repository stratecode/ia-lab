package codexlocalgateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	echoWritePattern    = regexp.MustCompile(`(?m)(?:^|[;&\n]\s*)echo\s+(.+?)\s*>\s*([^\s;&]+)`)
	pythonWritePattern  = regexp.MustCompile(`Path\(["']([^"']+)["']\)\.write_text\(["']((?:\\.|[^"'])*)["']\)`)
	prefixedToolPattern = regexp.MustCompile(`(?s)^\s*([A-Za-z_][A-Za-z0-9_-]*)\s*:?\s+(\{.*\})\s*$`)
)

func flattenInput(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("input is required")
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil
	}

	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", errors.New("input must be a string or text array")
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		text := textFromValue(value)
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func inputToChatMessages(raw json.RawMessage) ([]chatMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("input is required")
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if strings.TrimSpace(asString) == "" {
			return nil, errors.New("input must contain text")
		}
		return []chatMessage{{Role: "user", Content: asString}}, nil
	}

	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, errors.New("input must be a string or text array")
	}
	messages := make([]chatMessage, 0, len(values))
	textParts := make([]string, 0, len(values))
	flushText := func() {
		if len(textParts) == 0 {
			return
		}
		content := strings.Join(textParts, "\n")
		if strings.TrimSpace(content) != "" {
			messages = append(messages, chatMessage{Role: "user", Content: content})
		}
		textParts = textParts[:0]
	}
	for _, value := range values {
		if mapped, ok := value.(map[string]any); ok && mapped["type"] == "function_call_output" {
			flushText()
			callID, _ := mapped["call_id"].(string)
			if strings.TrimSpace(callID) == "" {
				return nil, errors.New("function_call_output requires call_id")
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				ToolCallID: callID,
				Content:    outputToString(mapped["output"]),
			})
			continue
		}
		if text := textFromValue(value); strings.TrimSpace(text) != "" {
			textParts = append(textParts, text)
		}
	}
	flushText()
	if len(messages) == 0 {
		return nil, errors.New("input must contain text or function_call_output")
	}
	return messages, nil
}

func outputToString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func textFromValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		return textFromMap(typed)
	default:
		return ""
	}
}

func textFromMap(value map[string]any) string {
	if text, ok := value["text"].(string); ok {
		return text
	}
	content, ok := value["content"]
	if !ok {
		return ""
	}
	switch typed := content.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if itemText := textFromValue(item); strings.TrimSpace(itemText) != "" {
				parts = append(parts, itemText)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func remapModel(body []byte, publicModel, upstreamModel string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	model, _ := payload["model"].(string)
	if model == "" || model == publicModel {
		payload["model"] = upstreamModel
		remapped, err := json.Marshal(payload)
		if err == nil {
			return remapped
		}
	}
	return body
}

type fallbackToolCall struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func parseFallbackToolCall(text string, tools json.RawMessage) (responseItem, bool, error) {
	trimmedText := strings.TrimSpace(text)
	if embedded, ok := embeddedFunctionCallJSON(trimmedText); ok {
		trimmedText = embedded
	} else if embedded, ok := embeddedPrefixedToolCall(trimmedText); ok {
		trimmedText = embedded
	}
	if item, ok := shellFenceToolCall(trimmedText, tools); ok {
		return item, true, nil
	}
	if call, ok, err := parsePrefixedToolCall(trimmedText); err != nil {
		return responseItem{}, false, err
	} else if ok {
		if normalized, ok, err := normalizeFallbackAlias(call, tools); err != nil || ok {
			return normalized, true, err
		}
		toolTypes := toolTypeByName(tools)
		toolType, known := toolTypes[call.Name]
		if len(toolTypes) == 0 {
			known = true
			toolType = "function"
		}
		if !known {
			return responseItem{}, true, fmt.Errorf("fallback function_call references unknown tool %q", call.Name)
		}
		callID := call.CallID
		if callID == "" {
			callID = "call_" + sanitizeToolName(call.Name)
		}
		arguments := strings.TrimSpace(string(call.Arguments))
		if arguments == "" || arguments == "null" {
			arguments = "{}"
		}
		if toolType == "custom" {
			input := arguments
			var asObject map[string]any
			if err := json.Unmarshal([]byte(arguments), &asObject); err == nil {
				if patch, _ := asObject["patch"].(string); patch != "" {
					input = patch
				} else if command, _ := asObject["cmd"].(string); command != "" {
					input = command
				}
			}
			return responseItem{
				ID:     "ctc_" + callID,
				Type:   "custom_tool_call",
				Status: "completed",
				CallID: callID,
				Name:   call.Name,
				Input:  input,
			}, true, nil
		}
		if call.Name == "exec_command" {
			if item, ok, err := normalizeExecCommandFallback(callID, arguments); err != nil || ok {
				return item, true, err
			}
		}
		return responseItem{
			ID:        "fc_" + callID,
			Type:      "function_call",
			Status:    "completed",
			CallID:    callID,
			Name:      call.Name,
			Arguments: arguments,
		}, true, nil
	}
	trimmed := stripJSONFence(trimmedText)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return responseItem{}, false, nil
	}
	var call fallbackToolCall
	if err := json.Unmarshal([]byte(trimmed), &call); err != nil {
		return responseItem{}, false, err
	}
	if call.Type != "function_call" {
		return responseItem{}, false, nil
	}
	if strings.TrimSpace(call.Name) == "" {
		return responseItem{}, true, errors.New("fallback function_call requires name")
	}
	if normalized, ok, err := normalizeFallbackAlias(call, tools); err != nil || ok {
		return normalized, true, err
	}
	toolTypes := toolTypeByName(tools)
	toolType, known := toolTypes[call.Name]
	if len(toolTypes) == 0 {
		known = true
		toolType = "function"
	}
	if !known {
		return responseItem{}, true, fmt.Errorf("fallback function_call references unknown tool %q", call.Name)
	}
	callID := call.CallID
	if callID == "" {
		callID = "call_" + sanitizeToolName(call.Name)
	}
	arguments := strings.TrimSpace(string(call.Arguments))
	if arguments == "" || arguments == "null" {
		arguments = "{}"
	} else if strings.HasPrefix(arguments, "\"") {
		var decoded string
		if err := json.Unmarshal(call.Arguments, &decoded); err != nil {
			return responseItem{}, true, err
		}
		arguments = decoded
	}
	if toolType == "custom" {
		input := arguments
		var asObject map[string]any
		if err := json.Unmarshal([]byte(arguments), &asObject); err == nil {
			if patch, _ := asObject["patch"].(string); patch != "" {
				input = patch
			} else if command, _ := asObject["cmd"].(string); command != "" {
				input = command
			} else if path, _ := asObject["path"].(string); path != "" {
				if content, ok := asObject["content"].(string); ok && toolNameAllowed("exec_command", tools) {
					return execCommandWriteFileItem(callID, path, content), true, nil
				}
			}
		}
		if call.Name == "apply_patch" && !strings.HasPrefix(strings.TrimSpace(input), "*** Begin Patch") && toolNameAllowed("exec_command", tools) {
			return responseItem{
				ID:        "fc_" + callID,
				Type:      "function_call",
				Status:    "completed",
				CallID:    callID,
				Name:      "exec_command",
				Arguments: mustJSON(map[string]string{"cmd": input}),
			}, true, nil
		}
		return responseItem{
			ID:     "ctc_" + callID,
			Type:   "custom_tool_call",
			Status: "completed",
			CallID: callID,
			Name:   call.Name,
			Input:  input,
		}, true, nil
	}
	if call.Name == "exec_command" {
		if item, ok, err := normalizeExecCommandFallback(callID, arguments); err != nil || ok {
			return item, true, err
		}
	}
	return responseItem{
		ID:        "fc_" + callID,
		Type:      "function_call",
		Status:    "completed",
		CallID:    callID,
		Name:      call.Name,
		Arguments: arguments,
	}, true, nil
}

func embeddedFunctionCallJSON(text string) (string, bool) {
	if text == "" || !strings.Contains(text, "function_call") {
		return "", false
	}
	for _, candidate := range fencedJSONCandidates(text) {
		if candidate == "" || !json.Valid([]byte(candidate)) {
			continue
		}
		var call fallbackToolCall
		if err := json.Unmarshal([]byte(candidate), &call); err != nil {
			continue
		}
		if call.Type == "function_call" && strings.TrimSpace(call.Name) != "" {
			return candidate, true
		}
	}
	return "", false
}

func fencedJSONCandidates(text string) []string {
	parts := strings.Split(text, "```")
	if len(parts) < 3 {
		return nil
	}
	candidates := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		block := strings.TrimSpace(parts[i])
		if block == "" {
			continue
		}
		if newline := strings.IndexByte(block, '\n'); newline >= 0 {
			header := strings.ToLower(strings.TrimSpace(block[:newline]))
			if header == "json" || header == "" {
				block = strings.TrimSpace(block[newline+1:])
			}
		}
		if strings.HasPrefix(block, "{") && strings.HasSuffix(block, "}") {
			candidates = append(candidates, block)
		}
	}
	return candidates
}

func embeddedPrefixedToolCall(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	pendingTool := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if prefixedToolPattern.MatchString(line) {
			return line, true
		}
		lower := strings.ToLower(strings.Trim(line, "*: "))
		if pendingTool != "" && strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
			return pendingTool + " " + line, true
		}
		if lower == "action: exec_command" || lower == "exec_command" {
			pendingTool = "exec_command"
			continue
		}
	}
	return "", false
}

func parsePrefixedToolCall(text string) (fallbackToolCall, bool, error) {
	trimmed := strings.TrimSpace(text)
	match := prefixedToolPattern.FindStringSubmatch(trimmed)
	if len(match) != 3 {
		return fallbackToolCall{}, false, nil
	}
	name := strings.TrimSpace(match[1])
	rawArgs := strings.TrimSpace(match[2])
	if name == "" || rawArgs == "" {
		return fallbackToolCall{}, false, nil
	}
	var decoded json.RawMessage
	if !json.Valid([]byte(rawArgs)) {
		return fallbackToolCall{}, false, fmt.Errorf("prefixed tool call for %q does not contain valid JSON arguments", name)
	}
	decoded = json.RawMessage(rawArgs)
	return fallbackToolCall{
		Type:      "function_call",
		Name:      name,
		Arguments: decoded,
	}, true, nil
}

func shellFenceToolCall(text string, tools json.RawMessage) (responseItem, bool) {
	if !toolNameAllowed("exec_command", tools) {
		return responseItem{}, false
	}
	command, ok := shellFenceCommand(text)
	if !ok {
		return responseItem{}, false
	}
	return execCommandItem("call_exec_command", command), true
}

func shellFenceCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	start := strings.Index(trimmed, "```")
	if start < 0 {
		return "", false
	}
	if strings.Count(trimmed, "```") != 2 {
		return "", false
	}
	rest := trimmed[start+3:]
	firstLineEnd := strings.IndexByte(rest, '\n')
	if firstLineEnd < 0 {
		return "", false
	}
	lang := strings.ToLower(strings.TrimSpace(rest[:firstLineEnd]))
	if lang != "sh" && lang != "bash" && lang != "shell" && lang != "zsh" {
		return "", false
	}
	body := strings.TrimSpace(rest[firstLineEnd+1:])
	end := strings.Index(body, "```")
	if end < 0 {
		return "", false
	}
	command := strings.TrimSpace(body[:end])
	if command == "" {
		return "", false
	}
	return command, true
}

func normalizeExecCommandFallback(callID, arguments string) (responseItem, bool, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return responseItem{}, false, nil
	}
	if path, content, ok := writeFields(args); ok {
		return execCommandWriteFileItem(callID, path, content), true, nil
	}
	cmd, ok := args["cmd"]
	if !ok {
		return responseItem{}, false, nil
	}
	switch typed := cmd.(type) {
	case string:
		if normalized, ok := unwrapNestedExecCommandString(typed); ok {
			return execCommandItem(callID, normalized), true, nil
		}
		var nested map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(typed)), &nested); err != nil {
			escapedNewlines := strings.ReplaceAll(strings.TrimSpace(typed), "\n", "\\n")
			if err := json.Unmarshal([]byte(escapedNewlines), &nested); err != nil {
				return responseItem{}, false, nil
			}
		}
		if path, content, ok := writeFields(nested); ok {
			return execCommandWriteFileItem(callID, path, content), true, nil
		}
	case map[string]any:
		if path, content, ok := writeFields(typed); ok {
			return execCommandWriteFileItem(callID, path, content), true, nil
		}
	}
	return responseItem{}, false, nil
}

func unwrapNestedExecCommandString(value string) (string, bool) {
	call, ok, err := parsePrefixedToolCall(value)
	if err == nil && ok && call.Name == "exec_command" {
		if command, ok := execCommandFromArguments(strings.TrimSpace(string(call.Arguments))); ok {
			return command, true
		}
	}

	trimmed := stripJSONFence(strings.TrimSpace(value))
	if !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	var nestedCall fallbackToolCall
	if err := json.Unmarshal([]byte(trimmed), &nestedCall); err != nil {
		return "", false
	}
	if nestedCall.Type != "function_call" || nestedCall.Name != "exec_command" {
		return "", false
	}
	return execCommandFromArguments(strings.TrimSpace(string(nestedCall.Arguments)))
}

func normalizeResponseToolItem(item responseItem) responseItem {
	if item.Type != "function_call" || item.Name != "exec_command" {
		return item
	}
	normalized, ok, err := normalizeExecCommandFallback(item.CallID, item.Arguments)
	if err != nil || !ok {
		return item
	}
	return normalized
}

func repeatedSuccessfulExecCommand(messages []chatMessage, item responseItem) bool {
	target, ok := execCommandFromResponseItem(item)
	if !ok {
		return false
	}
	targetWrite, targetWritesFile := fileWriteEffect(target)
	seen := make(map[string]string)
	for _, msg := range messages {
		if msg.Role == "assistant" {
			for _, call := range msg.ToolCalls {
				if call.Function.Name != "exec_command" {
					continue
				}
				if command, ok := execCommandFromArguments(call.Function.Arguments); ok {
					seen[call.ID] = command
				}
			}
			continue
		}
		if msg.Role == "tool" && toolOutputSucceeded(msg.Content) {
			previous := seen[msg.ToolCallID]
			if previous == target {
				return true
			}
			if targetWritesFile {
				if previousWrite, ok := fileWriteEffect(previous); ok && previousWrite.equivalent(targetWrite) {
					return true
				}
			}
		}
	}
	return false
}

func duplicateExecCommandItem(existing []responseItem, candidate responseItem) bool {
	target, ok := execCommandFromResponseItem(candidate)
	if !ok {
		return false
	}
	targetWrite, targetWritesFile := fileWriteEffect(target)
	for _, item := range existing {
		previous, ok := execCommandFromResponseItem(item)
		if !ok {
			continue
		}
		if previous == target {
			return true
		}
		if targetWritesFile {
			if previousWrite, ok := fileWriteEffect(previous); ok && previousWrite.equivalent(targetWrite) {
				return true
			}
		}
	}
	return false
}

type fileWrite struct {
	Path    string
	Content string
}

func (w fileWrite) equivalent(other fileWrite) bool {
	if w.Content != other.Content {
		return false
	}
	left := filepath.Clean(strings.TrimSpace(w.Path))
	right := filepath.Clean(strings.TrimSpace(other.Path))
	if left == right {
		return true
	}
	return filepath.Base(left) == filepath.Base(right)
}

func fileWriteEffect(command string) (fileWrite, bool) {
	trimmed := strings.TrimSpace(command)
	if match := pythonWritePattern.FindStringSubmatch(trimmed); len(match) == 3 {
		return fileWrite{Path: unescapeCommandString(match[1]), Content: unescapeCommandString(match[2])}, true
	}
	matches := echoWritePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return fileWrite{}, false
	}
	last := matches[len(matches)-1]
	path := strings.Trim(last[2], `"'`)
	content := strings.TrimSpace(last[1])
	content = strings.Trim(content, `"'`)
	content = strings.TrimSpace(unescapeCommandString(content))
	content = strings.Trim(content, `"'`)
	content = strings.TrimSuffix(content, `\`)
	return fileWrite{Path: path, Content: content + "\n"}, true
}

func unescapeCommandString(value string) string {
	replacer := strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\'`, `'`, `\\`, `\`)
	return replacer.Replace(value)
}

func execCommandFromResponseItem(item responseItem) (string, bool) {
	if item.Type != "function_call" || item.Name != "exec_command" {
		return "", false
	}
	return execCommandFromArguments(item.Arguments)
}

func execCommandFromArguments(arguments string) (string, bool) {
	var args struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		escapedNewlines := strings.ReplaceAll(arguments, "\n", "\\n")
		if err := json.Unmarshal([]byte(escapedNewlines), &args); err != nil {
			return "", false
		}
	}
	command := strings.TrimSpace(args.Cmd)
	return command, command != ""
}

func toolOutputSucceeded(content string) bool {
	var value any
	if err := json.Unmarshal([]byte(content), &value); err != nil {
		return strings.Contains(content, "exit_code=0") || strings.Contains(content, "exit_code 0")
	}
	return containsExitCodeZero(value)
}

func containsExitCodeZero(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "exit_code" {
				if number, ok := item.(float64); ok && number == 0 {
					return true
				}
			}
			if containsExitCodeZero(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsExitCodeZero(item) {
				return true
			}
		}
	}
	return false
}

func writeFields(value map[string]any) (string, string, bool) {
	path, _ := value["file_path"].(string)
	if strings.TrimSpace(path) == "" {
		path, _ = value["path"].(string)
	}
	if strings.TrimSpace(path) == "" {
		return "", "", false
	}
	content, ok := value["patch_content"].(string)
	if !ok {
		content, ok = value["content"].(string)
	}
	if !ok {
		return "", "", false
	}
	return path, content, true
}

func stripJSONFence(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed), "json"))
	if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return strings.TrimSpace(trimmed)
}

func toolNameAllowed(name string, tools json.RawMessage) bool {
	names := toolNames(tools)
	if len(names) == 0 {
		return true
	}
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func normalizeFallbackAlias(call fallbackToolCall, tools json.RawMessage) (responseItem, bool, error) {
	if call.Name != "write_file" || !toolNameAllowed("exec_command", tools) {
		return responseItem{}, false, nil
	}
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return responseItem{}, true, err
	}
	if strings.TrimSpace(args.Path) == "" {
		return responseItem{}, true, errors.New("write_file fallback requires path")
	}
	callID := call.CallID
	if callID == "" {
		callID = "call_write_file"
	}
	command := fmt.Sprintf("python3 - <<'PY'\nfrom pathlib import Path\nPath(%q).write_text(%q)\nPY", args.Path, args.Content)
	return execCommandItem(callID, command), true, nil
}

func execCommandWriteFileItem(callID, path, content string) responseItem {
	command := fmt.Sprintf("python3 - <<'PY'\nfrom pathlib import Path\nPath(%q).write_text(%q)\nPY", path, content)
	return execCommandItem(callID, command)
}

func execCommandItem(callID, command string) responseItem {
	return responseItem{
		ID:        "fc_" + callID,
		Type:      "function_call",
		Status:    "completed",
		CallID:    callID,
		Name:      "exec_command",
		Arguments: mustJSON(map[string]string{"cmd": command}),
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func toolNames(tools json.RawMessage) []string {
	values := decodeToolMaps(tools)
	names := make([]string, 0, len(values))
	for _, tool := range values {
		if name, _ := tool["name"].(string); name != "" {
			names = append(names, name)
			continue
		}
		if fn, ok := tool["function"].(map[string]any); ok {
			if fnName, _ := fn["name"].(string); fnName != "" {
				names = append(names, fnName)
			}
		}
	}
	return names
}

func preferredFallbackToolNames(tools json.RawMessage) []string {
	available := make(map[string]bool)
	for _, name := range toolNames(tools) {
		available[name] = true
	}
	preferred := make([]string, 0, 2)
	for _, name := range []string{"apply_patch", "exec_command"} {
		if available[name] {
			preferred = append(preferred, name)
		}
	}
	if len(preferred) > 0 {
		return preferred
	}
	return toolNames(tools)
}

func toolTypeByName(tools json.RawMessage) map[string]string {
	values := decodeToolMaps(tools)
	if len(values) == 0 {
		return nil
	}
	types := make(map[string]string, len(values))
	for _, tool := range values {
		toolType, _ := tool["type"].(string)
		if name, _ := tool["name"].(string); name != "" {
			types[name] = toolType
			continue
		}
		if fn, ok := tool["function"].(map[string]any); ok {
			if fnName, _ := fn["name"].(string); fnName != "" {
				types[fnName] = "function"
			}
		}
	}
	return types
}

func decodeToolMaps(tools json.RawMessage) []map[string]any {
	if len(tools) == 0 || string(tools) == "null" {
		return nil
	}
	var values []map[string]any
	if err := json.Unmarshal(tools, &values); err != nil {
		return nil
	}
	return values
}

func requiredToolName(choice any) string {
	asMap, ok := choice.(map[string]any)
	if !ok {
		return ""
	}
	if name, _ := asMap["name"].(string); name != "" {
		return name
	}
	if fn, ok := asMap["function"].(map[string]any); ok {
		name, _ := fn["name"].(string)
		return name
	}
	return ""
}

func sanitizeToolName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func requiresToolCall(choice any) bool {
	if choice == nil {
		return false
	}
	if asString, ok := choice.(string); ok {
		return asString != "" && asString != "auto" && asString != "none"
	}
	if asMap, ok := choice.(map[string]any); ok {
		typ, _ := asMap["type"].(string)
		return typ == "function"
	}
	return false
}
