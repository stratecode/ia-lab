package secretary

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Service) StartJiraTaskProposal(ctx context.Context, principal JiraActionPrincipal, candidate TaskCandidate) (JiraProposalResult, error) {
	if s == nil || s.JiraActions == nil || s.JiraProposals == nil || principal.OperatorID <= 0 || (principal.Channel != "telegram" && principal.Channel != "openwebui") || strings.TrimSpace(principal.ConversationID) == "" {
		return JiraProposalResult{}, ErrJiraActionsUnavailable
	}
	options, err := s.JiraActions.Options(ctx, principal)
	if err != nil {
		return JiraProposalResult{}, err
	}
	if candidate.UserConfirmed == nil {
		candidate.UserConfirmed = map[TaskDimension]bool{}
	}
	// Selecting a Granola action's “Proponer tarea” control confirms only that
	// the operator wants this exact source span considered as a task.
	candidate.UserConfirmed[TaskDimensionAction] = true
	if strings.TrimSpace(candidate.Summary) == "" {
		candidate.Summary = strings.TrimSpace(candidate.SourceSpan)
	}
	if strings.TrimSpace(candidate.Summary) == strings.TrimSpace(candidate.SourceSpan) && candidate.Summary != "" {
		candidate.UserConfirmed[TaskDimensionSummary] = true
	}
	if candidate.ProjectKey == "" && len(options.ProjectKeys) == 1 {
		candidate.ProjectKey = options.ProjectKeys[0]
		candidate.UserConfirmed[TaskDimensionProject] = true
	}
	if candidate.IssueType == "" && len(options.IssueTypes) == 1 {
		candidate.IssueType = options.IssueTypes[0]
		candidate.UserConfirmed[TaskDimensionIssueType] = true
	}
	result, err := s.evaluateAndStoreJiraProposal(ctx, principal, candidate, options)
	if err != nil || result.Preview != nil || result.Decision.Status == CandidateReject {
		return result, err
	}
	result, _, err = s.persistJiraProposalQuestion(principal, result.Candidate, result)
	return result, err
}

func (s *Service) ClarifyJiraTaskProposal(ctx context.Context, principal JiraActionPrincipal, answer string) (JiraProposalResult, bool, error) {
	if s == nil || s.JiraActions == nil || s.JiraProposals == nil {
		return JiraProposalResult{}, false, nil
	}
	pending, found, err := s.JiraProposals.Get(principal, time.Now().UTC())
	if err != nil || !found {
		return JiraProposalResult{}, found, err
	}
	options, err := s.JiraActions.Options(ctx, principal)
	if err != nil {
		return JiraProposalResult{}, true, err
	}
	candidate, decision := ClarifyTaskCandidate(pending.Candidate, pending.QuestionDimension, answer)
	if decision.Status == CandidateReject {
		_ = s.JiraProposals.Delete(principal)
		return JiraProposalResult{Candidate: candidate, Decision: decision, Message: decision.Question}, true, nil
	}
	if decision.Status == CandidateClarify {
		pending.Candidate = candidate
		pending.Question = decision.Question
		return JiraProposalResult{Candidate: candidate, Decision: decision, Message: decision.Question}, true, s.JiraProposals.Save(pending)
	}
	if pending.QuestionDimension == TaskDimensionProject && !containsExactString(options.ProjectKeys, candidate.ProjectKey) {
		question := optionQuestion("No puedo usar ese proyecto. Elige una clave permitida", options.ProjectKeys)
		return s.keepJiraProposalQuestion(pending, candidate, TaskDimensionProject, question)
	}
	if pending.QuestionDimension == TaskDimensionIssueType && !containsExactString(options.IssueTypes, candidate.IssueType) {
		question := optionQuestion("No puedo usar ese tipo de incidencia. Elige un tipo permitido", options.IssueTypes)
		return s.keepJiraProposalQuestion(pending, candidate, TaskDimensionIssueType, question)
	}
	result, err := s.evaluateAndStoreJiraProposal(ctx, principal, candidate, options)
	if err != nil {
		return JiraProposalResult{}, true, err
	}
	if result.Preview != nil || result.Decision.Status == CandidateReject {
		_ = s.JiraProposals.Delete(principal)
		return result, true, nil
	}
	return s.persistJiraProposalQuestion(principal, candidate, result)
}

func (s *Service) evaluateAndStoreJiraProposal(ctx context.Context, principal JiraActionPrincipal, candidate TaskCandidate, options JiraTaskOptions) (JiraProposalResult, error) {
	result, err := PrepareJiraTaskProposal(ctx, s.JiraActions, principal, candidate, s.JiraTaskPolicy)
	if err != nil {
		return JiraProposalResult{}, err
	}
	if result.Preview != nil || result.Decision.Status == CandidateReject {
		return result, nil
	}
	if result.Decision.Dimension == TaskDimensionProject {
		result.Message = optionQuestion(result.Message, options.ProjectKeys)
	}
	if result.Decision.Dimension == TaskDimensionIssueType {
		result.Message = optionQuestion(result.Message, options.IssueTypes)
	}
	return result, nil
}

func (s *Service) persistJiraProposalQuestion(principal JiraActionPrincipal, candidate TaskCandidate, result JiraProposalResult) (JiraProposalResult, bool, error) {
	id, err := newJiraProposalID()
	if err != nil {
		return JiraProposalResult{}, true, err
	}
	now := time.Now().UTC()
	pending := PendingJiraProposal{
		ID: id, Principal: principal, Candidate: result.Candidate,
		QuestionDimension: result.Decision.Dimension, Question: result.Message,
		CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute),
	}
	if err := s.JiraProposals.Save(pending); err != nil {
		return JiraProposalResult{}, true, err
	}
	return result, true, nil
}

func (s *Service) keepJiraProposalQuestion(pending PendingJiraProposal, candidate TaskCandidate, dimension TaskDimension, question string) (JiraProposalResult, bool, error) {
	pending.Candidate = candidate
	pending.QuestionDimension = dimension
	pending.Question = question
	if err := s.JiraProposals.Save(pending); err != nil {
		return JiraProposalResult{}, true, err
	}
	return JiraProposalResult{Candidate: candidate, Decision: CandidateDecision{Status: CandidateClarify, Dimension: dimension, Question: question}, Message: question}, true, nil
}

func (s *Service) sendJiraProposalResult(ctx context.Context, principal JiraActionPrincipal, result JiraProposalResult) error {
	if principal.Channel == "telegram" && result.Preview != nil {
		if s.GranolaBot == nil {
			return ErrJiraActionsUnavailable
		}
		return s.GranolaBot.sendJiraActionPreview(ctx, principal.OperatorID, *result.Preview)
	}
	return nil
}

func optionQuestion(prompt string, values []string) string {
	if len(values) == 0 {
		return prompt + ". No hay opciones configuradas."
	}
	return prompt + ": " + strings.Join(values, ", ")
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func formatJiraProposalStatus(result JiraProposalResult) string {
	if result.Preview != nil {
		return result.Message
	}
	if strings.TrimSpace(result.Message) == "" {
		return fmt.Sprintf("No puedo preparar la tarea: estado %s.", result.Decision.Status)
	}
	return result.Message
}
