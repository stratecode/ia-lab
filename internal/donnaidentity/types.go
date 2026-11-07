package donnaidentity

import (
	"errors"
	"time"
)

type EntityKind string

const (
	EntityPerson  EntityKind = "person"
	EntityEmail   EntityKind = "email"
	EntityProject EntityKind = "project"
	EntityAlias   EntityKind = "alias"
)

type MappingStatus string

const (
	MappingPending  MappingStatus = "pending"
	MappingApproved MappingStatus = "approved"
	MappingRejected MappingStatus = "rejected"
)

var (
	ErrMappingNotFound = errors.New("mapping not found")
	ErrMappingConflict = errors.New("mapping confirmation conflicts with current state")
	ErrInvalidMapping  = errors.New("invalid mapping")
)

type MappingCandidate struct {
	ID             string        `json:"id"`
	OperatorID     int64         `json:"operator_id"`
	Channel        string        `json:"channel"`
	ConversationID string        `json:"conversation_id"`
	Kind           EntityKind    `json:"kind"`
	CanonicalID    string        `json:"canonical_id"`
	CanonicalLabel string        `json:"canonical_label"`
	Provider       string        `json:"provider"`
	ExternalID     string        `json:"external_id"`
	Aliases        []string      `json:"aliases,omitempty"`
	Evidence       []string      `json:"evidence,omitempty"`
	PayloadDigest  string        `json:"payload_digest"`
	Status         MappingStatus `json:"status"`
	CreatedAt      time.Time     `json:"created_at"`
	ConfirmedAt    time.Time     `json:"confirmed_at,omitempty"`
	ConfirmedBy    int64         `json:"confirmed_by,omitempty"`
}

type Principal struct {
	OperatorID     int64
	Channel        string
	ConversationID string
}
