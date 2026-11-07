package secretary

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stratecode/lab/internal/donnaidentity"
)

type IdentityClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func (c *IdentityClient) CreateCandidate(ctx context.Context, principal OperatorContext, candidate donnaidentity.MappingCandidate) (donnaidentity.MappingCandidate, error) {
	var result donnaidentity.MappingCandidate
	body := map[string]any{"kind": candidate.Kind, "canonical_id": candidate.CanonicalID, "canonical_label": candidate.CanonicalLabel, "provider": candidate.Provider, "external_id": candidate.ExternalID, "aliases": candidate.Aliases, "evidence": candidate.Evidence}
	err := c.request(ctx, principal, http.MethodPost, "/v1/mappings", body, &result)
	return result, err
}

func (c *IdentityClient) ListMappings(ctx context.Context, principal OperatorContext, kind donnaidentity.EntityKind, status donnaidentity.MappingStatus) ([]donnaidentity.MappingCandidate, error) {
	path := "/v1/mappings"
	query := url.Values{}
	if kind != "" {
		query.Set("kind", string(kind))
	}
	if status != "" {
		query.Set("status", string(status))
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result []donnaidentity.MappingCandidate
	if err := c.request(ctx, principal, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *IdentityClient) ConfirmMapping(ctx context.Context, principal OperatorContext, id, digest string, approve bool) (donnaidentity.MappingCandidate, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(digest) == "" {
		return donnaidentity.MappingCandidate{}, errors.New("mapping ID and digest required")
	}
	action := "confirm"
	if !approve {
		action = "reject"
	}
	var result donnaidentity.MappingCandidate
	err := c.request(ctx, principal, http.MethodPost, "/v1/mappings/"+url.PathEscape(id)+"/"+action, map[string]string{"payload_digest": digest}, &result)
	return result, err
}

func (c *IdentityClient) ApprovedKnowledge(ctx context.Context, operatorID int64) ([]KnowledgeItem, error) {
	// The API scopes this read by operator; the internal service identity is
	// still required. Use a fixed service conversation for this read-only list.
	principal, err := NewOperatorContext(operatorID, "telegram", "approved-knowledge")
	if err != nil {
		return nil, err
	}
	mappings, err := c.ListMappings(ctx, principal, "", donnaidentity.MappingApproved)
	if err != nil {
		return nil, err
	}
	items := make([]KnowledgeItem, 0, len(mappings))
	rootByKey := make(map[string][]int, len(mappings))
	for _, mapping := range mappings {
		if mapping.Kind != donnaidentity.EntityPerson && mapping.Kind != donnaidentity.EntityProject {
			continue
		}
		aliases := append([]string{mapping.CanonicalLabel}, mapping.Aliases...)
		refs := map[string]string{}
		if mapping.ExternalID != "" {
			refs[mapping.Provider] = mapping.ExternalID
		}
		if mapping.CanonicalID != "" && mapping.ExternalID == "" {
			refs[mapping.Provider] = mapping.CanonicalID
		}
		entityType := EntityPerson
		if mapping.Kind == donnaidentity.EntityProject {
			entityType = EntityProject
		}
		key := identityMappingKey(mapping.CanonicalID, mapping.Provider, mapping.ExternalID)
		merged := false
		for _, index := range rootByKey[key] {
			if items[index].EntityType != entityType {
				continue
			}
			items[index].Aliases = uniqueStrings(append(items[index].Aliases, aliases...))
			if mapping.ConfirmedAt.After(items[index].UpdatedAt) {
				items[index].UpdatedAt = mapping.ConfirmedAt
			}
			merged = true
			break
		}
		if merged {
			continue
		}
		rootByKey[key] = append(rootByKey[key], len(items))
		items = append(items, KnowledgeItem{SchemaVersion: learningSchemaVersion, ID: "identity-" + mapping.ID, OperatorID: operatorID, Type: KnowledgeEntityAlias, EntityType: entityType, Subject: mapping.CanonicalLabel, Predicate: "canonical", Value: mapping.CanonicalID, Aliases: uniqueStrings(aliases), ExternalRefs: refs, Sources: []EvidenceRef{{Provider: mapping.Provider, ID: mapping.ExternalID, Title: mapping.CanonicalLabel}}, Confidence: 1, Status: KnowledgeApproved, CreatedAt: mapping.CreatedAt, UpdatedAt: mapping.ConfirmedAt})
	}
	for _, mapping := range mappings {
		if mapping.Kind != donnaidentity.EntityAlias && mapping.Kind != donnaidentity.EntityEmail {
			continue
		}
		indexes := rootByKey[identityMappingKey(mapping.CanonicalID, mapping.Provider, mapping.ExternalID)]
		if len(indexes) != 1 {
			continue
		}
		index := indexes[0]
		items[index].Aliases = uniqueStrings(append(items[index].Aliases, mapping.Aliases...))
		if mapping.ConfirmedAt.After(items[index].UpdatedAt) {
			items[index].UpdatedAt = mapping.ConfirmedAt
		}
	}
	return items, nil
}

func identityMappingKey(canonicalID, provider, externalID string) string {
	return strings.ToLower(strings.TrimSpace(canonicalID)) + "\x00" + strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(externalID))
}

func (c *IdentityClient) request(ctx context.Context, principal OperatorContext, method, path string, body any, target any) error {
	if _, err := NewOperatorContext(principal.OperatorID, principal.Channel, principal.ConversationID); err != nil {
		return err
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" || c.Token == "" {
		return errors.New("identity service is not configured")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid identity service URL")
	}
	host := u.Hostname()
	if host != "localhost" && net.ParseIP(host) == nil {
		return errors.New("identity service must use a loopback host")
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return errors.New("identity service must use a loopback host")
	}
	var reader io.Reader
	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Operator-ID", fmt.Sprint(principal.OperatorID))
	req.Header.Set("X-Channel", principal.Channel)
	req.Header.Set("X-Conversation-ID", principal.ConversationID)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("identity service request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("identity service returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("invalid identity service response")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("identity service response has trailing data")
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[strings.ToLower(value)] {
			seen[strings.ToLower(value)] = true
			out = append(out, value)
		}
	}
	return out
}
