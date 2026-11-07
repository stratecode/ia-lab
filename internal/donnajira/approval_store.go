package donnajira

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ApprovalStore interface {
	Create(context.Context, TaskDraft) (TaskDraft, error)
	Get(context.Context, string, ActionPrincipal) (TaskDraft, error)
	FindByDedupeKey(context.Context, string) (TaskDraft, error)
	Consume(context.Context, string, ActionPrincipal, string, time.Time) (TaskDraft, error)
	Complete(context.Context, string, ActionPrincipal, ActionReceipt) error
	Cancel(context.Context, string, ActionPrincipal, string, time.Time) error
}

type approvalState struct {
	Version int                  `json:"version"`
	Drafts  map[string]TaskDraft `json:"drafts"`
}

type FileApprovalStore struct {
	mu   sync.Mutex
	path string
}

func OpenApprovalStore(path string) (*FileApprovalStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrActionInvalid
	}
	store := &FileApprovalStore{path: path}
	if _, err := store.readState(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileApprovalStore) Create(ctx context.Context, draft TaskDraft) (TaskDraft, error) {
	if err := ctx.Err(); err != nil {
		return TaskDraft{}, err
	}
	if err := validateDraft(draft); err != nil {
		return TaskDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return TaskDraft{}, err
	}
	for _, existing := range state.Drafts {
		if existing.DedupeKey != draft.DedupeKey {
			continue
		}
		if existing.Status == ActionCanceled {
			continue
		}
		if existing.PayloadDigest == payloadDigest(draft) && existing.Principal == draft.Principal {
			return existing, nil
		}
		return TaskDraft{}, ErrActionDuplicateSource
	}
	if draft.ID == "" {
		draft.ID, err = newActionID()
		if err != nil {
			return TaskDraft{}, err
		}
	}
	draft.PayloadDigest = payloadDigest(draft)
	draft.Status = ActionPending
	state.Drafts[draft.ID] = draft
	if err := s.writeState(state); err != nil {
		return TaskDraft{}, err
	}
	return draft, nil
}

func (s *FileApprovalStore) Cancel(ctx context.Context, id string, principal ActionPrincipal, digest string, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	draft, ok := state.Drafts[id]
	if !ok {
		return ErrActionNotFound
	}
	if draft.Principal != principal {
		return ErrActionPrincipalMismatch
	}
	if !constantTimeEqual(draft.PayloadDigest, digest) {
		return ErrActionDigestMismatch
	}
	if !now.Before(draft.ExpiresAt) {
		return ErrActionExpired
	}
	if draft.Status != ActionPending {
		return ErrActionAlreadyConsumed
	}
	draft.Status = ActionCanceled
	state.Drafts[id] = draft
	return s.writeState(state)
}

func (s *FileApprovalStore) Get(ctx context.Context, id string, principal ActionPrincipal) (TaskDraft, error) {
	if err := ctx.Err(); err != nil {
		return TaskDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return TaskDraft{}, err
	}
	draft, ok := state.Drafts[id]
	if !ok {
		return TaskDraft{}, ErrActionNotFound
	}
	if draft.Principal != principal {
		return TaskDraft{}, ErrActionPrincipalMismatch
	}
	return draft, nil
}

func (s *FileApprovalStore) FindByDedupeKey(ctx context.Context, key string) (TaskDraft, error) {
	if err := ctx.Err(); err != nil {
		return TaskDraft{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return TaskDraft{}, ErrActionInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return TaskDraft{}, err
	}
	for _, draft := range state.Drafts {
		if draft.DedupeKey == key {
			return draft, nil
		}
	}
	return TaskDraft{}, ErrActionNotFound
}

func (s *FileApprovalStore) Consume(ctx context.Context, id string, principal ActionPrincipal, digest string, now time.Time) (TaskDraft, error) {
	if err := ctx.Err(); err != nil {
		return TaskDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return TaskDraft{}, err
	}
	draft, ok := state.Drafts[id]
	if !ok {
		return TaskDraft{}, ErrActionNotFound
	}
	if draft.Principal != principal {
		return TaskDraft{}, ErrActionPrincipalMismatch
	}
	if !constantTimeEqual(draft.PayloadDigest, digest) {
		return TaskDraft{}, ErrActionDigestMismatch
	}
	if !now.Before(draft.ExpiresAt) {
		return TaskDraft{}, ErrActionExpired
	}
	if draft.Status != ActionPending {
		return TaskDraft{}, ErrActionAlreadyConsumed
	}
	draft.Status = ActionConsumed
	state.Drafts[id] = draft
	if err := s.writeState(state); err != nil {
		return TaskDraft{}, err
	}
	return draft, nil
}

func (s *FileApprovalStore) Complete(ctx context.Context, id string, principal ActionPrincipal, receipt ActionReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readState()
	if err != nil {
		return err
	}
	draft, ok := state.Drafts[id]
	if !ok {
		return ErrActionNotFound
	}
	if draft.Principal != principal {
		return ErrActionPrincipalMismatch
	}
	if draft.Status != ActionConsumed {
		return ErrActionAlreadyConsumed
	}
	if receipt.ActionID != id || receipt.Kind != draft.Kind || (receipt.Verified && strings.TrimSpace(receipt.IssueKey) == "") {
		return ErrActionInvalid
	}
	if receipt.Verified {
		draft.Status = ActionCommitted
	} else {
		draft.Status = ActionUncertain
	}
	draft.Receipt = &receipt
	state.Drafts[id] = draft
	return s.writeState(state)
}

func validateDraft(draft TaskDraft) error {
	if draft.Principal.OperatorID <= 0 || strings.TrimSpace(draft.Principal.Channel) == "" || strings.TrimSpace(draft.Principal.ConversationID) == "" ||
		strings.TrimSpace(draft.DedupeKey) == "" || strings.TrimSpace(draft.NoteID) == "" || strings.TrimSpace(draft.NoteURL) == "" ||
		strings.TrimSpace(draft.SourceSpan) == "" || draft.CreatedAt.IsZero() || !draft.ExpiresAt.After(draft.CreatedAt) {
		return ErrActionInvalid
	}
	switch draft.Kind {
	case ActionCreate:
		if strings.TrimSpace(draft.ProjectKey) == "" || strings.TrimSpace(draft.IssueType) == "" || strings.TrimSpace(draft.Summary) == "" || strings.TrimSpace(draft.IssueKey) != "" || strings.TrimSpace(draft.Comment) != "" {
			return ErrActionInvalid
		}
	case ActionComment:
		if strings.TrimSpace(draft.IssueKey) == "" || strings.TrimSpace(draft.ExpectedIssueSummary) == "" || strings.TrimSpace(draft.Comment) == "" || strings.TrimSpace(draft.ProjectKey) != "" || strings.TrimSpace(draft.Summary) != "" {
			return ErrActionInvalid
		}
	default:
		return ErrActionInvalid
	}
	if draft.PayloadDigest != "" && !constantTimeEqual(draft.PayloadDigest, payloadDigest(draft)) {
		return ErrActionDigestMismatch
	}
	return nil
}

func payloadDigest(draft TaskDraft) string {
	payload := struct {
		Principal            ActionPrincipal `json:"principal"`
		Kind                 ActionKind      `json:"kind"`
		ProjectKey           string          `json:"project_key,omitempty"`
		IssueType            string          `json:"issue_type,omitempty"`
		IssueKey             string          `json:"issue_key,omitempty"`
		ExpectedIssueSummary string          `json:"expected_issue_summary,omitempty"`
		Summary              string          `json:"summary,omitempty"`
		Description          string          `json:"description,omitempty"`
		Comment              string          `json:"comment,omitempty"`
		AssigneeAccountID    string          `json:"assignee_account_id,omitempty"`
		DueDate              string          `json:"due_date,omitempty"`
		NoteID               string          `json:"note_id"`
		NoteURL              string          `json:"note_url"`
		SourceSpan           string          `json:"source_span"`
		DedupeKey            string          `json:"dedupe_key"`
	}{draft.Principal, draft.Kind, draft.ProjectKey, draft.IssueType, draft.IssueKey, draft.ExpectedIssueSummary, draft.Summary, draft.Description, draft.Comment, draft.AssigneeAccountID, draft.DueDate, draft.NoteID, draft.NoteURL, draft.SourceSpan, draft.DedupeKey}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func newActionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate action ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func (s *FileApprovalStore) readState() (approvalState, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return approvalState{Version: 1, Drafts: map[string]TaskDraft{}}, nil
	}
	if err != nil {
		return approvalState{}, ErrActionStoreUnavailable
	}
	var state approvalState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != 1 || state.Drafts == nil {
		return approvalState{}, ErrActionStoreUnavailable
	}
	return state, nil
}

func (s *FileApprovalStore) writeState(state approvalState) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return ErrActionStoreUnavailable
	}
	data, err := json.Marshal(state)
	if err != nil {
		return ErrActionStoreUnavailable
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".actions-*.tmp")
	if err != nil {
		return ErrActionStoreUnavailable
	}
	tempName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			os.Remove(tempName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return ErrActionStoreUnavailable
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return ErrActionStoreUnavailable
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return ErrActionStoreUnavailable
	}
	if err := tmp.Close(); err != nil {
		return ErrActionStoreUnavailable
	}
	if err := os.Rename(tempName, s.path); err != nil {
		return ErrActionStoreUnavailable
	}
	removeTemp = false
	return nil
}
