package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IntentHTTPClient exchanges interpretation only. It has no connector credentials.
type IntentHTTPClient struct {
	URL, Token string
	Client     *http.Client
}
type intentWireRequest struct {
	Profile               string   `json:"profile"`
	Locale                string   `json:"locale"`
	Utterance             string   `json:"utterance"`
	AvailableCapabilities []string `json:"available_capabilities"`
}
type intentWireResult struct {
	SchemaVersion         string   `json:"schema_version"`
	ProfileVersion        string   `json:"profile_version"`
	Status                string   `json:"status"`
	Goal                  *string  `json:"goal"`
	RequestedOperation    string   `json:"requested_operation"`
	TargetMentions        []string `json:"target_mentions"`
	SuggestedCapabilities []string `json:"suggested_capabilities"`
	Basis                 string   `json:"basis"`
	Ambiguities           []string `json:"ambiguities"`
	ReasonCode            string   `json:"reason_code"`
}

var errIntentNegatedAction = errors.New("intent action explicitly negated")

// intentFallbackCategory returns a bounded diagnostic label. It deliberately
// never persists the error text, which can contain transport details.
func intentFallbackCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errIntentNegatedAction):
		return "action_negated"
	case strings.Contains(err.Error(), "unavailable"):
		return "service_unavailable"
	case strings.Contains(err.Error(), "status"):
		return "service_http_error"
	case strings.Contains(err.Error(), "source missing"):
		return "source_missing"
	case strings.Contains(err.Error(), "ambiguous"):
		return "ambiguous_route"
	case strings.Contains(err.Error(), "unresolved"):
		return "unresolved_intent"
	default:
		return "invalid_intent_response"
	}
}

func (c *IntentHTTPClient) interpret(ctx context.Context, utterance string, available []string) (intentWireResult, error) {
	var result intentWireResult
	body, err := json.Marshal(intentWireRequest{Profile: "donna-v1", Locale: "es-ES", Utterance: utterance, AvailableCapabilities: available})
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.URL, "/")+"/v1/interpret", bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("intent service unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("intent service status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("intent response invalid: %w", err)
	}
	if result.SchemaVersion != "1" || result.ProfileVersion != "donna-v1" {
		return result, errors.New("intent response version mismatch")
	}
	return result, nil
}

func (s *Service) interpretRoute(ctx context.Context, utterance string, history []Turn) ([]ReadCapability, bool, string, InteractionMeaning, error) {
	if s.Intent == nil {
		return nil, false, utterance, InteractionNeutral, errors.New("intent client unavailable")
	}
	registered := s.capabilities()
	byID := map[string]ReadCapability{}
	for _, cap := range registered {
		if cap.Read == nil {
			continue
		}
		switch strings.ToLower(cap.Name) {
		case "jira":
			byID["jira.read"] = cap
		case "granola":
			byID["granola.read"] = cap
		case strings.ToLower(sharedWebCapabilityName):
			byID["web.read"] = cap
		}
	}
	available := []string{"memory.read"}
	for _, id := range []string{"jira.read", "granola.read", "web.read"} {
		if _, ok := byID[id]; ok {
			available = append(available, id)
		}
	}
	result, err := s.Intent.interpret(ctx, utterance, available)
	if err != nil {
		return nil, false, utterance, InteractionNeutral, err
	}
	if result.Status != "resolved" || result.Goal == nil {
		return nil, false, utterance, InteractionNeutral, errors.New("intent unresolved")
	}
	if result.ReasonCode == "action_negated" {
		return nil, false, utterance, InteractionNeutral, errIntentNegatedAction
	}
	goal := *result.Goal
	if goal != "lookup" && goal != "recall" && goal != "change_request" && goal != "general" {
		return nil, false, utterance, InteractionNeutral, errors.New("intent goal invalid")
	}
	meaning := InteractionNeutral
	switch goal {
	case "lookup", "recall":
		meaning = InteractionLookup
	case "change_request":
		meaning = InteractionAction
	}
	if goal == "change_request" && result.RequestedOperation == "" {
		return nil, false, utterance, meaning, errors.New("intent operation missing")
	}
	if jiraTaskIntent(utterance) && (len(jiraPeople(utterance)) > 0 || explicitJiraVerificationFollowUp(utterance)) {
		localCapabilities, matched, localQuery := s.capabilitiesFor(utterance, history)
		if matched && len(localCapabilities) == 1 && strings.EqualFold(localCapabilities[0].Name, "Jira") {
			return localCapabilities, true, localQuery, InteractionLookup, nil
		}
	}
	if len(result.SuggestedCapabilities) > 1 {
		return nil, false, utterance, meaning, errors.New("intent capabilities ambiguous")
	}
	if len(result.SuggestedCapabilities) == 0 {
		if goal == "lookup" || goal == "change_request" {
			return nil, false, utterance, meaning, errors.New("intent source missing")
		}
		return nil, false, utterance, meaning, nil
	}
	id := result.SuggestedCapabilities[0]
	if id == "memory.read" && goal == "recall" {
		return nil, false, utterance, meaning, nil
	}
	cap, ok := byID[id]
	if !ok || explicitlyExcludedCapability(utterance, cap.Name) {
		return nil, false, utterance, meaning, errors.New("intent capability unregistered")
	}
	if goal == "general" {
		return nil, false, utterance, meaning, errors.New("intent capability conflicts with general goal")
	}
	// The intent service can return one best capability even when the user
	// explicitly asks for a comparison across sources. Supplement that result
	// only when deterministic local routing recognizes multiple registered,
	// read-only capabilities in the same utterance. Explicit exclusions and
	// source-only requests are already enforced by capabilitiesFor.
	if goal == "lookup" || goal == "change_request" {
		localCapabilities, matched, localQuery := s.capabilitiesFor(utterance, history)
		localNames := make([]string, 0, len(localCapabilities))
		for _, localCapability := range localCapabilities {
			localNames = append(localNames, localCapability.Name)
		}
		if matched && len(localCapabilities) > 1 && (goal == "lookup" || wantsGranolaJiraComparison(utterance, localNames)) {
			return localCapabilities, true, localQuery, meaning, nil
		}
		if matched && len(localCapabilities) == 1 && strings.EqualFold(localCapabilities[0].Name, cap.Name) {
			return localCapabilities, true, localQuery, meaning, nil
		}
	}
	readQuery := utterance
	if id == "granola.read" && refersToPriorMeeting(utterance) {
		for i := len(history) - 1; i >= 0; i-- {
			if history[i].Role == "user" && explicitMeetingAnchor(history[i].Content) {
				if anchor := meetingAnchorFromQuery(history[i].Content); anchor != "" {
					readQuery = "Reunión de referencia: " + anchor + "\nSeguimiento: " + utterance
				}
				break
			}
		}
	}
	return []ReadCapability{cap}, true, readQuery, meaning, nil
}
