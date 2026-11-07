package secretary

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/stratecode/lab/internal/donnajira"
)

type TaskDimension string

const (
	TaskDimensionAction    TaskDimension = "action"
	TaskDimensionProject   TaskDimension = "project"
	TaskDimensionIssueType TaskDimension = "issue_type"
	TaskDimensionSummary   TaskDimension = "summary"
	TaskDimensionMatch     TaskDimension = "match"
	TaskDimensionEvidence  TaskDimension = "evidence"
)

type CandidateStatus string

const (
	CandidateReady   CandidateStatus = "ready"
	CandidateClarify CandidateStatus = "clarify"
	CandidateReject  CandidateStatus = "reject"
	CandidateNoMatch CandidateStatus = "no_match"
)

type CalibrationResult struct {
	Precision   float64
	SampleCount int
}

type CalibrationExample struct {
	Dimension TaskDimension
	Predicted bool
	Correct   bool
}

// EvaluateTaskCalibration measures precision only among decisions where a
// dimension was predicted present. Unobserved dimensions are intentionally
// absent from the result so callers cannot mistake missing data for 100%.
func EvaluateTaskCalibration(examples []CalibrationExample) map[TaskDimension]CalibrationResult {
	counts := make(map[TaskDimension]CalibrationResult)
	correct := make(map[TaskDimension]int)
	for _, example := range examples {
		if !example.Predicted || !validCalibratedDimension(example.Dimension) {
			continue
		}
		result := counts[example.Dimension]
		result.SampleCount++
		counts[example.Dimension] = result
		if example.Correct {
			correct[example.Dimension]++
		}
	}
	for dimension, result := range counts {
		result.Precision = float64(correct[dimension]) / float64(result.SampleCount)
		counts[dimension] = result
	}
	return counts
}

func validCalibratedDimension(dimension TaskDimension) bool {
	switch dimension {
	case TaskDimensionAction, TaskDimensionProject, TaskDimensionIssueType, TaskDimensionSummary, TaskDimensionMatch:
		return true
	default:
		return false
	}
}

type TaskCandidate struct {
	GranolaNoteID string
	NoteURL       string
	MeetingTitle  string
	SourceSummary string
	SourceSpan    string
	ProjectKey    string
	IssueType     string
	Summary       string
	Description   string
	Assignee      string
	DueDate       string
	UserConfirmed map[TaskDimension]bool
	ConfirmedMatch *donnajira.MatchCandidate
	PossibleMatch *donnajira.MatchCandidate
	ConfirmedNoMatch bool

	// Confidence is populated by trusted, versioned calibration code. Never
	// copy a probability emitted by the language model into this map.
	Confidence map[TaskDimension]float64
}

type ConfidencePolicy struct {
	Threshold   float64
	MinSamples  int
	Calibration map[TaskDimension]CalibrationResult
}

type CandidateDecision struct {
	Status     CandidateStatus
	Dimension  TaskDimension
	IssueKey   string
	IssueSummary string
	Similarity float64
	Confidence float64
	Question   string
}

func EvaluateTaskMatch(proposal string, candidates []donnajira.MatchCandidate, policy ConfidencePolicy) CandidateDecision {
	if len(candidates) == 0 {
		return CandidateDecision{Status: CandidateNoMatch}
	}
	proposalTokens := taskSummaryTokens(proposal)
	if len(proposalTokens) == 0 {
		return clarifyTaskMatch(candidates[0], 0, 0)
	}
	for i := range candidates {
		if candidates[i].Similarity <= 0 {
			candidates[i].Similarity = taskSummarySimilarity(proposalTokens, taskSummaryTokens(candidates[i].Summary))
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Similarity == candidates[j].Similarity {
			return candidates[i].IssueKey < candidates[j].IssueKey
		}
		return candidates[i].Similarity > candidates[j].Similarity
	})
	top := candidates[0]
	calibration, calibrated := policy.Calibration[TaskDimensionMatch]
	if len(candidates) > 1 && top.Similarity-candidates[1].Similarity < 0.10 {
		return clarifyTaskMatch(top, top.Similarity, calibration.Precision)
	}
	if !strings.EqualFold(jiraIssueKeyMention.FindString(strings.ToUpper(top.IssueKey)), top.IssueKey) || strings.TrimSpace(top.Summary) == "" ||
		policy.Threshold <= 0 || policy.Threshold > 1 || math.IsNaN(policy.Threshold) ||
		!calibrated || calibration.SampleCount < policy.MinSamples || calibration.Precision < policy.Threshold ||
		top.Similarity < policy.Threshold {
		return clarifyTaskMatch(top, top.Similarity, calibration.Precision)
	}
	return CandidateDecision{
		Status: CandidateReady, Dimension: TaskDimensionMatch, IssueKey: top.IssueKey,
		Similarity: top.Similarity, Confidence: calibration.Precision,
	}
}

func clarifyTaskMatch(candidate donnajira.MatchCandidate, similarity, confidence float64) CandidateDecision {
	return CandidateDecision{
		Status: CandidateClarify, Dimension: TaskDimensionMatch, IssueKey: candidate.IssueKey,
		IssueSummary: candidate.Summary,
		Similarity: similarity, Confidence: confidence,
		Question: fmt.Sprintf("Jira contiene una tarea posiblemente relacionada: %s — %s. ¿Es la misma tarea?", candidate.IssueKey, candidate.Summary),
	}
}

func taskSummaryTokens(value string) map[string]struct{} {
	stopwords := map[string]struct{}{
		"a": {}, "al": {}, "de": {}, "del": {}, "el": {}, "en": {}, "la": {}, "las": {}, "los": {}, "para": {}, "por": {}, "que": {}, "se": {}, "un": {}, "una": {}, "y": {},
	}
	var normalized strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		} else {
			normalized.WriteByte(' ')
		}
	}
	tokens := strings.Fields(normalized.String())
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if len([]rune(token)) < 2 {
			continue
		}
		if _, ok := stopwords[token]; !ok {
			set[token] = struct{}{}
		}
	}
	return set
}

func taskSummarySimilarity(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if _, ok := right[token]; ok {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	return float64(intersection) / float64(union)
}

func EvaluateTaskCandidate(candidate TaskCandidate, policy ConfidencePolicy) CandidateDecision {
	if policy.Threshold <= 0 || policy.Threshold > 1 || math.IsNaN(policy.Threshold) || policy.MinSamples < 1 {
		return CandidateDecision{Status: CandidateReject, Question: "La regla de confianza no está configurada; no crearé la tarea."}
	}
	if strings.TrimSpace(candidate.GranolaNoteID) == "" || strings.TrimSpace(candidate.NoteURL) == "" ||
		strings.TrimSpace(candidate.SourceSpan) == "" || !strings.Contains(candidate.SourceSummary, candidate.SourceSpan) {
		return clarifyCandidate(TaskDimensionEvidence, candidate)
	}

	checks := []struct {
		dimension TaskDimension
		value     string
		label     string
	}{
		{TaskDimensionAction, candidate.SourceSpan, "acción"},
		{TaskDimensionProject, candidate.ProjectKey, "proyecto de Jira"},
		{TaskDimensionIssueType, candidate.IssueType, "tipo de tarea"},
		{TaskDimensionSummary, candidate.Summary, "resumen de la tarea"},
	}
	for _, check := range checks {
		if strings.TrimSpace(check.value) == "" {
			return clarifyCandidate(check.dimension, candidate)
		}
		if candidate.UserConfirmed[check.dimension] || (check.dimension == TaskDimensionSummary && strings.TrimSpace(candidate.Summary) == strings.TrimSpace(candidate.SourceSpan)) {
			continue
		}
		confidence := candidate.Confidence[check.dimension]
		calibration, calibrated := policy.Calibration[check.dimension]
		if !calibrated || !validCalibratedDimension(check.dimension) ||
			calibration.SampleCount < policy.MinSamples || calibration.Precision < policy.Threshold ||
			math.IsNaN(confidence) || confidence < policy.Threshold || confidence > 1 {
			return clarifyCandidate(check.dimension, candidate)
		}
	}
	return CandidateDecision{Status: CandidateReady}
}

func clarifyCandidate(dimension TaskDimension, candidate TaskCandidate) CandidateDecision {
	switch dimension {
	case TaskDimensionEvidence:
		if strings.TrimSpace(candidate.NoteURL) == "" {
			return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: fmt.Sprintf("Tengo la acción de «%s», pero falta el enlace HTTPS a su nota de Granola. ¿Puedes pegarlo y comprobar que abre esa nota?", candidate.MeetingTitle)}
		}
		return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "No puedo señalar una frase exacta de la nota de Granola para respaldar esta tarea. ¿Qué acción quieres registrar?"}
	case TaskDimensionAction:
		return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: fmt.Sprintf("¿Quieres convertir en tarea este punto de «%s»?\n%s", candidate.MeetingTitle, strings.TrimSpace(candidate.SourceSpan))}
	case TaskDimensionProject:
		return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: fmt.Sprintf("¿A qué proyecto de Jira asigno «%s»?", strings.TrimSpace(candidate.Summary))}
	case TaskDimensionIssueType:
		return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "¿Qué tipo de tarea de Jira debo usar para esta propuesta?"}
	case TaskDimensionSummary:
		return CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "¿Qué resumen quieres usar para esta tarea de Jira?"}
	default:
		return CandidateDecision{Status: CandidateReject, Dimension: dimension, Question: "No puedo verificar esta propuesta; no crearé la tarea."}
	}
}
