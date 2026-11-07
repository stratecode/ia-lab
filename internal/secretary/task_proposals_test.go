package secretary

import (
	"testing"

	"github.com/stratecode/lab/internal/donnajira"
)

func calibratedTaskProposal() (TaskCandidate, ConfidencePolicy) {
	precision := map[TaskDimension]CalibrationResult{}
	for _, dimension := range []TaskDimension{TaskDimensionAction, TaskDimensionProject, TaskDimensionIssueType, TaskDimensionSummary, TaskDimensionMatch} {
		precision[dimension] = CalibrationResult{Precision: 0.95, SampleCount: 40}
	}
	candidate := TaskCandidate{
		GranolaNoteID: "note-123",
		NoteURL:       "https://notes.example.test/note-123",
		MeetingTitle:  "Planificación PMI",
		SourceSummary: "Próximos pasos:\n- Preparar el informe de conciliación para el viernes.",
		SourceSpan:    "Preparar el informe de conciliación para el viernes.",
		ProjectKey:    "PMI",
		IssueType:     "Task",
		Summary:       "Preparar el informe de conciliación",
		Confidence: map[TaskDimension]float64{
			TaskDimensionAction:    0.95,
			TaskDimensionProject:   0.98,
			TaskDimensionIssueType: 0.97,
			TaskDimensionSummary:   0.96,
		},
	}
	return candidate, ConfidencePolicy{Threshold: 0.90, MinSamples: 30, Calibration: precision}
}

func TestTaskCandidateRequiresSourceEvidence(t *testing.T) {
	candidate, policy := calibratedTaskProposal()
	candidate.SourceSpan = ""

	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateClarify || decision.Dimension != TaskDimensionEvidence || decision.Question == "" {
		t.Fatalf("decision=%+v, want clarification requiring cited source evidence", decision)
	}
}

func TestTaskCandidateClarifiesAmbiguousAction(t *testing.T) {
	candidate, policy := calibratedTaskProposal()
	candidate.Confidence[TaskDimensionAction] = 0.82

	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateClarify || decision.Dimension != TaskDimensionAction || decision.Question == "" {
		t.Fatalf("decision=%+v, want a focused action clarification", decision)
	}
}

func TestTaskCandidateClarifiesMissingRequiredField(t *testing.T) {
	candidate, policy := calibratedTaskProposal()
	candidate.ProjectKey = ""

	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateClarify || decision.Dimension != TaskDimensionProject || decision.Question == "" {
		t.Fatalf("decision=%+v, want a focused project clarification", decision)
	}
}

func TestTaskCandidateLeavesOptionalFieldsBlank(t *testing.T) {
	candidate, policy := calibratedTaskProposal()
	candidate.Assignee = ""
	candidate.DueDate = ""

	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateReady {
		t.Fatalf("decision=%+v, missing optional owner/date must not block a task", decision)
	}
}

func TestTaskCandidateConfidenceGatesAreIndependent(t *testing.T) {
	candidate, policy := calibratedTaskProposal()
	policy.Calibration[TaskDimensionProject] = CalibrationResult{Precision: 0.89, SampleCount: 40}

	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateClarify || decision.Dimension != TaskDimensionProject {
		t.Fatalf("decision=%+v, weak project calibration must not be hidden by other fields", decision)
	}

	policy.Calibration[TaskDimensionProject] = CalibrationResult{Precision: 0.95, SampleCount: 29}
	decision = EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateClarify || decision.Dimension != TaskDimensionProject {
		t.Fatalf("decision=%+v, insufficient per-field calibration must fail closed", decision)
	}
}

func TestEvaluateTaskCalibrationMeasuresEachDimensionSeparately(t *testing.T) {
	results := EvaluateTaskCalibration([]CalibrationExample{
		{Dimension: TaskDimensionAction, Predicted: true, Correct: true},
		{Dimension: TaskDimensionAction, Predicted: true, Correct: false},
		{Dimension: TaskDimensionProject, Predicted: true, Correct: true},
		{Dimension: TaskDimensionProject, Predicted: false, Correct: true},
	})

	if got := results[TaskDimensionAction]; got.SampleCount != 2 || got.Precision != 0.5 {
		t.Fatalf("action calibration=%+v, want precision 0.5 from two positive decisions", got)
	}
	if got := results[TaskDimensionProject]; got.SampleCount != 1 || got.Precision != 1 {
		t.Fatalf("project calibration=%+v, want precision 1 from one positive decision", got)
	}
	if _, ok := results[TaskDimensionSummary]; ok {
		t.Fatal("unobserved dimensions must not receive fabricated calibration")
	}
}

func TestTaskMatchRequiresCalibratedUniqueHighConfidenceCandidate(t *testing.T) {
	_, policy := calibratedTaskProposal()
	proposal := "Preparar el informe de conciliación"
	candidates := []donnajira.MatchCandidate{{IssueKey: "PMI-42", Summary: proposal, Similarity: 1}}

	decision := EvaluateTaskMatch(proposal, candidates, policy)
	if decision.Status != CandidateReady || decision.IssueKey != "PMI-42" {
		t.Fatalf("decision=%+v, want unique calibrated match", decision)
	}

	policy.Calibration[TaskDimensionMatch] = CalibrationResult{Precision: 0.85, SampleCount: 40}
	decision = EvaluateTaskMatch(proposal, candidates, policy)
	if decision.Status != CandidateClarify || decision.Question == "" {
		t.Fatalf("decision=%+v, weak match calibration must ask the operator", decision)
	}
}

func TestTaskMatchAsksWhenCandidatesAreAmbiguousOrCalibrationIsInsufficient(t *testing.T) {
	_, policy := calibratedTaskProposal()
	proposal := "Preparar el informe de conciliación"
	policy.Calibration[TaskDimensionMatch] = CalibrationResult{Precision: 0.95, SampleCount: 40}
	candidates := []donnajira.MatchCandidate{
		{IssueKey: "PMI-42", Summary: proposal, Similarity: 1},
		{IssueKey: "PMI-43", Summary: proposal, Similarity: 1},
	}
	decision := EvaluateTaskMatch(proposal, candidates, policy)
	if decision.Status != CandidateClarify {
		t.Fatalf("decision=%+v, tied tasks must remain ambiguous", decision)
	}

	candidates = candidates[:1]
	policy.Calibration[TaskDimensionMatch] = CalibrationResult{Precision: 1, SampleCount: 29}
	decision = EvaluateTaskMatch(proposal, candidates, policy)
	if decision.Status != CandidateClarify {
		t.Fatalf("decision=%+v, insufficient match evaluation must ask", decision)
	}
}

func TestTaskMatchWithNoJiraCandidatesReturnsNoMatch(t *testing.T) {
	_, policy := calibratedTaskProposal()
	decision := EvaluateTaskMatch("Preparar informe", nil, policy)
	if decision.Status != CandidateNoMatch {
		t.Fatalf("decision=%+v, no Jira candidates should return no match", decision)
	}
}
