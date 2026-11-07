package secretary

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PendingJiraProposal struct {
	ID                string              `json:"id"`
	Principal         JiraActionPrincipal `json:"principal"`
	Candidate         TaskCandidate       `json:"candidate"`
	QuestionDimension TaskDimension       `json:"question_dimension"`
	Question          string              `json:"question"`
	CreatedAt         time.Time           `json:"created_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
}

type jiraProposalState struct {
	Version   int                            `json:"version"`
	Proposals map[string]PendingJiraProposal `json:"proposals"`
}

type JiraProposalStore struct {
	path string
	mu   sync.Mutex
}

func OpenJiraProposalStore(stateDir string) (*JiraProposalStore, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("Jira proposal state directory is required")
	}
	root := filepath.Join(stateDir, "jira-proposals")
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, errors.New("cannot create Jira proposal state")
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, errors.New("cannot secure Jira proposal state")
	}
	return &JiraProposalStore{path: filepath.Join(root, "pending.json")}, nil
}

func (s *JiraProposalStore) Save(proposal PendingJiraProposal) error {
	if s == nil || !safeActionID(proposal.ID) || proposal.Principal.OperatorID <= 0 || proposal.Principal.ConversationID == "" || proposal.Question == "" || proposal.CreatedAt.IsZero() || !proposal.ExpiresAt.After(proposal.CreatedAt) {
		return errors.New("invalid pending Jira proposal")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.read()
	if err != nil {
		return err
	}
	state.Proposals[proposalKey(proposal.Principal)] = proposal
	return atomicGranolaNotificationJSON(s.path, state)
}

func (s *JiraProposalStore) Get(principal JiraActionPrincipal, now time.Time) (PendingJiraProposal, bool, error) {
	if s == nil || principal.OperatorID <= 0 || principal.ConversationID == "" {
		return PendingJiraProposal{}, false, errors.New("invalid Jira proposal lookup")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.read()
	if err != nil {
		return PendingJiraProposal{}, false, err
	}
	proposal, found := state.Proposals[proposalKey(principal)]
	if !found || proposal.Principal != principal {
		return PendingJiraProposal{}, false, nil
	}
	if !now.Before(proposal.ExpiresAt) {
		delete(state.Proposals, proposalKey(principal))
		if err := atomicGranolaNotificationJSON(s.path, state); err != nil {
			return PendingJiraProposal{}, false, err
		}
		return PendingJiraProposal{}, false, nil
	}
	return proposal, true, nil
}

func (s *JiraProposalStore) Delete(principal JiraActionPrincipal) error {
	if s == nil || principal.OperatorID <= 0 || principal.ConversationID == "" {
		return errors.New("invalid Jira proposal deletion")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.read()
	if err != nil {
		return err
	}
	delete(state.Proposals, proposalKey(principal))
	return atomicGranolaNotificationJSON(s.path, state)
}

func (s *JiraProposalStore) read() (jiraProposalState, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return jiraProposalState{Version: 1, Proposals: map[string]PendingJiraProposal{}}, nil
	}
	if err != nil {
		return jiraProposalState{}, errors.New("cannot read Jira proposal state")
	}
	var state jiraProposalState
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || state.Proposals == nil {
		return jiraProposalState{}, errors.New("Jira proposal state is invalid")
	}
	return state, nil
}

func proposalKey(principal JiraActionPrincipal) string {
	value := strconv.FormatInt(principal.OperatorID, 10) + "\x00" + principal.Channel + "\x00" + principal.ConversationID
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func newJiraProposalID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", errors.New("cannot generate Jira proposal ID")
	}
	return hex.EncodeToString(raw[:]), nil
}
