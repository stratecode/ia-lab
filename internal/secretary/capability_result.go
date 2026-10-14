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
	Capability  string
	Outcome     string
	Duration    time.Duration
	ResultCount int
	Evidence    []EvidenceRef
}

// StructuredSourceReader supplements the legacy direct-command interface with
// source metadata for the common natural-language router.
type StructuredSourceReader interface {
	GranolaResult(context.Context) (CapabilityResult, error)
	RovoResult(context.Context, string) (CapabilityResult, error)
}
