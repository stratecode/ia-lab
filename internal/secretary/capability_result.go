package secretary

import (
	"context"
	"time"
)

// CapabilityResult contains a provider's bounded, synthesis-ready text and
// references that were actually present in the provider response.
type CapabilityResult struct {
	Text         string
	ResultCount  int
	Evidence     []EvidenceRef
	DirectAnswer bool
	Partial      bool
	// Compact is a short, code-built rendering of Text for list-like answers.
	// It is used instead of model synthesis unless the operator asks for detail.
	Compact string
	// Due maps an evidence ID (a Jira key) to its due date, YYYY-MM-DD, when the
	// provider reported one. Scheduled skills read deadlines from here instead of
	// parsing prose.
	Due map[string]string
}

type EvidenceRef struct {
	Provider    string    `json:"provider,omitempty"`
	ID          string    `json:"id,omitempty"`
	Title       string    `json:"title,omitempty"`
	URL         string    `json:"url,omitempty"`
	RetrievedAt time.Time `json:"retrieved_at,omitempty"`
}

type CapabilityOutcome struct {
	Capability  string        `json:"capability"`
	Outcome     string        `json:"outcome"`
	Duration    time.Duration `json:"duration"`
	ResultCount int           `json:"result_count"`
	Evidence    []EvidenceRef `json:"evidence,omitempty"`
	Partial     bool          `json:"partial,omitempty"`
}

// StructuredSourceReader supplements the legacy direct-command interface with
// source metadata for the common natural-language router.
type StructuredSourceReader interface {
	GranolaResult(context.Context) (CapabilityResult, error)
}

// JiraQueryReader performs a bounded, read-only query against Jira REST using
// JQL compiled by the secretary.
type JiraQueryReader interface {
	JiraQueryResult(context.Context, string) (CapabilityResult, error)
}

// GranolaQueryReader exposes query-aware retrieval when the provider supports it.
type GranolaQueryReader interface {
	GranolaQueryResult(context.Context, string) (CapabilityResult, error)
}
