package semanticir

import (
	"strings"
	"testing"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

func TestCompileSeparatesTrustedAndInvalidContext(t *testing.T) {
	chunks := []domain.ContextChunk{
		{
			SourceType:   "task",
			SourceID:     "task-ok",
			SourceRef:    "task:task-ok#0",
			ContentText:  "Completed scaffold task",
			Metadata:     map[string]any{"outcome": "trusted", "confidence": 0.9, "agent_type": "coder", "constraints": []any{"keep tests deterministic"}},
			ChunkIndex:   0,
			Score:        nil,
			InitiativeID: nil,
		},
		{
			SourceType:  "task",
			SourceID:    "task-failed",
			SourceRef:   "task:task-failed#0",
			ContentText: "Deployment failed",
			Metadata:    map[string]any{"outcome": "failed", "confidence": 1.0, "failure_reason": "missing env var", "validation_rules": []any{"env must be present"}},
		},
	}
	ir := Compile(CompileRequest{
		Mode:       "PLAN_MODE",
		Chunks:     chunks,
		Policies:   []string{"policy a"},
		SourceRefs: []string{"task:task-ok#0", "task:task-failed#0"},
		MaxChars:   6000,
	})
	if ir.Version != Version {
		t.Fatalf("unexpected version %q", ir.Version)
	}
	if len(ir.Trusted) != 1 || ir.Trusted[0].SourceID != "task-ok" {
		t.Fatalf("trusted context not separated: %+v", ir.Trusted)
	}
	if len(ir.Invalid) != 1 || ir.Invalid[0].FailureReason != "missing env var" {
		t.Fatalf("invalid context not separated: %+v", ir.Invalid)
	}
	if !contains(ir.Constraints, "keep tests deterministic") {
		t.Fatalf("constraint not extracted: %+v", ir.Constraints)
	}
	if !contains(ir.ValidationRules, "env must be present") {
		t.Fatalf("validation rule not extracted: %+v", ir.ValidationRules)
	}
	if !strings.Contains(ir.JSONata.Conditions["has_invalid_prior"], "$.invalid") {
		t.Fatalf("jsonata condition missing invalid selector: %+v", ir.JSONata)
	}
}

func TestCompileSanitizesSecretMetadata(t *testing.T) {
	ir := Compile(CompileRequest{
		Mode: "REVIEW_MODE",
		Chunks: []domain.ContextChunk{{
			SourceType:  "review",
			SourceID:    "review-1",
			SourceRef:   "review:review-1#0",
			ContentText: "API_KEY=secret",
			Metadata:    map[string]any{"outcome": "rejected", "api_key": "secret", "failure_reason": "token leaked"},
		}},
	})
	raw := ir.Invalid[0].Metadata["api_key"]
	if raw != nil {
		t.Fatalf("secret metadata should not be retained, got %+v", ir.Invalid[0].Metadata)
	}
	if strings.Contains(ir.Invalid[0].Summary, "secret") {
		t.Fatalf("secret content leaked into summary: %q", ir.Invalid[0].Summary)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
