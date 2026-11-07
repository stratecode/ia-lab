package donnagranola

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
)

type SecretaryNotificationSink struct {
	baseURL string
	token   string
	client  *http.Client
}

var ErrNotificationRejected = errors.New("secretary rejected Granola notification")

type secretaryNotificationRequest struct {
	OperatorID     int64 `json:"operator_id"`
	TelegramChatID int64 `json:"telegram_chat_id,omitempty"`
	Notification   struct {
		ID             string          `json:"id"`
		Message        string          `json:"message"`
		TaskCandidates []GranolaAction `json:"task_candidates,omitempty"`
	} `json:"notification"`
}

func NewSecretaryNotificationSink(baseURL, token string) (*SecretaryNotificationSink, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !isLoopbackHostname(parsed.Hostname()) || parsed.Port() == "" || len(strings.TrimSpace(token)) < 32 {
		return nil, errors.New("secretary notification endpoint must be loopback HTTP with a private token")
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &SecretaryNotificationSink{baseURL: strings.TrimRight(baseURL, "/"), token: strings.TrimSpace(token), client: client}, nil
}

func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *SecretaryNotificationSink) Deliver(ctx context.Context, operator OperatorContext, notification Notification) error {
	if s == nil || s.client == nil || operator.OperatorID <= 0 || !eventIDPattern.MatchString(notification.ID) || strings.TrimSpace(notification.Message) == "" {
		return errors.New("invalid secretary notification")
	}
	var requestBody secretaryNotificationRequest
	requestBody.OperatorID = operator.OperatorID
	requestBody.TelegramChatID = operator.TelegramChatID
	requestBody.Notification.ID = notification.ID
	requestBody.Notification.Message = notification.Message
	requestBody.Notification.TaskCandidates = notification.TaskCandidates
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return errors.New("cannot encode secretary notification")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/internal/granola-notifications", bytes.NewReader(encoded))
	if err != nil {
		return errors.New("cannot create secretary notification request")
	}
	request.Header.Set("Authorization", "Bearer "+s.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: secretary notification service unavailable", ErrTemporary)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError {
			return fmt.Errorf("%w: secretary notification service returned HTTP %d", ErrTemporary, response.StatusCode)
		}
		return fmt.Errorf("%w: HTTP %d", ErrNotificationRejected, response.StatusCode)
	}
	return nil
}
