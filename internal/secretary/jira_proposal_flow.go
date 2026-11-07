package secretary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/stratecode/lab/internal/donnajira"
)

var jiraProposalProjectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,9}$`)

func ClarifyTaskCandidate(candidate TaskCandidate, dimension TaskDimension, answer string) (TaskCandidate, CandidateDecision) {
	answer = strings.TrimSpace(answer)
	if candidate.UserConfirmed == nil {
		candidate.UserConfirmed = make(map[TaskDimension]bool)
	}
	switch dimension {
	case TaskDimensionAction:
		if isTaskAnswerNo(answer) {
			return candidate, CandidateDecision{Status: CandidateReject, Dimension: dimension, Question: "Entendido. No propondré esta acción como tarea."}
		}
		if !isTaskAnswerYes(answer) {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Responde «sí» para proponer esta acción como tarea o «no» para descartarla."}
		}
		candidate.UserConfirmed[dimension] = true
	case TaskDimensionProject:
		value := strings.ToUpper(answer)
		if !jiraProposalProjectKey.MatchString(value) {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Indica la clave del proyecto Jira, por ejemplo PMI."}
		}
		candidate.ProjectKey = value
		candidate.UserConfirmed[dimension] = true
	case TaskDimensionIssueType:
		if answer == "" || len([]rune(answer)) > 50 || strings.ContainsAny(answer, "\r\n\x00") {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Indica un tipo de incidencia de Jira, por ejemplo Task."}
		}
		candidate.IssueType = answer
		candidate.UserConfirmed[dimension] = true
	case TaskDimensionSummary:
		if answer == "" || len([]rune(answer)) > 255 || strings.ContainsAny(answer, "\r\n\x00") {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Indica un resumen breve para la tarea."}
		}
		candidate.Summary = answer
		candidate.UserConfirmed[dimension] = true
	case TaskDimensionEvidence:
		parsed, err := url.Parse(answer)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || strings.ContainsAny(answer, "\r\n\x00") {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Pega un enlace HTTPS a la nota de Granola."}
		}
		candidate.NoteURL = answer
	case TaskDimensionMatch:
		if isTaskAnswerNo(answer) {
			candidate.ConfirmedNoMatch = true
			candidate.ConfirmedMatch = nil
			candidate.PossibleMatch = nil
			return candidate, CandidateDecision{Status: CandidateNoMatch, Dimension: dimension}
		}
		if !isTaskAnswerYes(answer) {
			return candidate, CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: "Responde «sí» si es la misma tarea o «no» para preparar una tarea nueva."}
		}
		if candidate.PossibleMatch == nil {
			return candidate, CandidateDecision{Status: CandidateReject, Dimension: dimension, Question: "No puedo recuperar la tarea candidata; no modificaré Jira."}
		}
		candidate.ConfirmedMatch = candidate.PossibleMatch
		candidate.ConfirmedNoMatch = false
	default:
		return candidate, CandidateDecision{Status: CandidateReject, Dimension: dimension, Question: "No puedo procesar esa aclaración de forma segura."}
	}
	return candidate, CandidateDecision{Status: CandidateReady, Dimension: dimension}
}

func isTaskAnswerYes(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sí", "si", "yes", "confirmo", "adelante", "correcto":
		return true
	default:
		return false
	}
}

func isTaskAnswerNo(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "no", "descartar", "descártala", "descartarla", "no es la misma":
		return true
	default:
		return false
	}
}

type JiraProposalResult struct {
	Decision  CandidateDecision
	Candidate TaskCandidate
	Preview   *JiraActionPreview
	Message   string
}

// PrepareJiraTaskProposal evaluates every independent confidence gate, checks
// for a duplicate, then persists a no-write preview for the appropriate action.
func PrepareJiraTaskProposal(ctx context.Context, client *JiraActionsClient, principal JiraActionPrincipal, candidate TaskCandidate, policy ConfidencePolicy) (JiraProposalResult, error) {
	decision := EvaluateTaskCandidate(candidate, policy)
	if decision.Status != CandidateReady {
		return JiraProposalResult{Decision: decision, Candidate: candidate, Message: decision.Question}, nil
	}
	if client == nil {
		return JiraProposalResult{}, ErrJiraActionsUnavailable
	}
	principal.Channel = strings.ToLower(strings.TrimSpace(principal.Channel))
	var calibrated []donnajira.MatchCandidate
	matchDecision := CandidateDecision{Status: CandidateNoMatch, Dimension: TaskDimensionMatch}
	switch {
	case candidate.ConfirmedMatch != nil:
		calibrated = append(calibrated, *candidate.ConfirmedMatch)
		matchDecision = CandidateDecision{Status: CandidateReady, Dimension: TaskDimensionMatch, IssueKey: candidate.ConfirmedMatch.IssueKey}
	case candidate.ConfirmedNoMatch:
		matchDecision = CandidateDecision{Status: CandidateNoMatch, Dimension: TaskDimensionMatch}
	default:
		matches, err := client.Match(ctx, principal, candidate.ProjectKey, candidate.Summary, 5)
		if err != nil {
			return JiraProposalResult{}, err
		}
		calibrated = make([]donnajira.MatchCandidate, 0, len(matches))
		for _, match := range matches {
			calibrated = append(calibrated, donnajira.MatchCandidate{
				IssueKey: match.IssueKey, Summary: match.Summary, Status: match.Status,
				Similarity: match.Similarity, Confidence: match.Confidence,
			})
		}
		matchDecision = EvaluateTaskMatch(candidate.Summary, calibrated, policy)
	}
	var draft JiraTaskDraft
	if matchDecision.Status == CandidateClarify {
		candidate.PossibleMatch = &donnajira.MatchCandidate{IssueKey: matchDecision.IssueKey, Summary: matchDecision.IssueSummary, Similarity: matchDecision.Similarity}
		return JiraProposalResult{Decision: matchDecision, Candidate: candidate, Message: matchDecision.Question}, nil
	}
	if matchDecision.Status == CandidateReady {
		var selected *donnajira.MatchCandidate
		for i := range calibrated {
			if calibrated[i].IssueKey == matchDecision.IssueKey {
				selected = &calibrated[i]
				break
			}
		}
		if selected == nil {
			return JiraProposalResult{Decision: CandidateDecision{Status: CandidateClarify, Dimension: TaskDimensionMatch, Question: "No puedo verificar la coincidencia en Jira. ¿Quieres revisar manualmente las tareas existentes?"}, Message: "No puedo verificar la coincidencia en Jira. ¿Quieres revisar manualmente las tareas existentes?"}, nil
		}
		comment := fmt.Sprintf("Acción de reunión: %s\nFuente: %s", strings.TrimSpace(candidate.SourceSpan), strings.TrimSpace(candidate.NoteURL))
		draft = JiraTaskDraft{
			Kind: "comment", IssueKey: selected.IssueKey, ExpectedIssueSummary: selected.Summary,
			Comment: comment, NoteID: candidate.GranolaNoteID, NoteURL: candidate.NoteURL,
			SourceSpan: candidate.SourceSpan, DedupeKey: taskActionDedupeKey(candidate),
		}
	} else if matchDecision.Status == CandidateNoMatch {
		description := strings.TrimSpace(candidate.Description)
		if description == "" {
			description = strings.TrimSpace(candidate.SourceSpan)
		}
		description += "\n\nFuente de Granola: " + strings.TrimSpace(candidate.NoteURL)
		draft = JiraTaskDraft{
			Kind: "create", ProjectKey: candidate.ProjectKey, IssueType: candidate.IssueType,
			Summary: candidate.Summary, Description: description, AssigneeAccountID: candidate.Assignee,
			DueDate: candidate.DueDate, NoteID: candidate.GranolaNoteID, NoteURL: candidate.NoteURL,
			SourceSpan: candidate.SourceSpan, DedupeKey: taskActionDedupeKey(candidate),
		}
	} else {
		return JiraProposalResult{Decision: matchDecision, Message: matchDecision.Question}, nil
	}
	preview, err := client.Draft(ctx, principal, draft)
	if err != nil {
		return JiraProposalResult{}, err
	}
	return JiraProposalResult{Decision: matchDecision, Candidate: candidate, Preview: &preview, Message: FormatJiraActionPreview(preview)}, nil
}

func taskActionDedupeKey(candidate TaskCandidate) string {
	value := strings.ToLower(strings.Join(strings.Fields(candidate.SourceSpan), " "))
	digest := sha256.Sum256([]byte(strings.TrimSpace(candidate.GranolaNoteID) + "\x00" + value))
	return hex.EncodeToString(digest[:])
}

func FormatJiraActionPreview(preview JiraActionPreview) string {
	var lines []string
	if preview.Kind == "create" {
		lines = append(lines, "Propuesta de tarea para Jira", "Proyecto: "+preview.ProjectKey, "Tipo: "+preview.IssueType, "Resumen: "+preview.Summary)
		if strings.TrimSpace(preview.Description) != "" {
			lines = append(lines, "Descripción:\n"+preview.Description)
		}
		lines = append(lines, "Acción: **Crear en Jira**")
	} else if preview.Kind == "comment" {
		lines = append(lines, "Tarea posiblemente existente: "+preview.IssueKey+" · "+preview.IssueSummary,
			"Comentario exacto que se añadirá:\n"+preview.Comment,
			"Acción: **Actualizar tarea**")
	}
	if strings.TrimSpace(preview.SourceSpan) != "" {
		lines = append(lines, "Evidencia: "+preview.SourceSpan)
	}
	if strings.TrimSpace(preview.NoteURL) != "" {
		lines = append(lines, "Fuente: "+preview.NoteURL)
	}
	lines = append(lines, fmt.Sprintf("<!-- donna-jira-action:%s:%s -->", preview.ActionID, preview.PayloadDigest))
	return strings.Join(lines, "\n\n")
}
