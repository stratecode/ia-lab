package secretary

import (
	"context"
	"time"
)

// CapabilityResult contains a provider's bounded, synthesis-ready text and
// references that were actually present in the provider response.
type CapabilityResult struct {
	Text        string
	ResultCount int
	Evidence    []EvidenceRef
}

type EvidenceRef struct {
	Provider    string
	ID          string
	Title       string
	URL         string
	RetrievedAt time.Time
}

type CapabilityOutcome struct {
	Capability  string        `json:"capability"`
	Outcome     string        `json:"outcome"`
	Duration    time.Duration `json:"duration"`
	ResultCount int           `json:"result_count"`
	Evidence    []EvidenceRef `json:"evidence,omitempty"`
}

// StructuredSourceReader supplements the legacy direct-command interface with
// source metadata for the common natural-language router.
type StructuredSourceReader interface {
	GranolaResult(context.Context) (CapabilityResult, error)
	RovoResult(context.Context, string) (CapabilityResult, error)
}
