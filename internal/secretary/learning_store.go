package secretary

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxLearningRecordBytes = 1 << 20

// LearningStore persists metadata and reviewed knowledge without storing provider payloads.
type LearningStore interface {
	AppendTrace(InteractionTrace) error
	LastTrace(operatorID int64, channel, conversationRef string) (InteractionTrace, bool, error)
	AppendFeedback(FeedbackEvent) error
	SaveKnowledge(KnowledgeItem) error
	ListKnowledge(operatorID int64, status KnowledgeStatus) ([]KnowledgeItem, error)
	Prune(now time.Time, traceDays, candidateDays int) error
	Forget(operatorID int64) error
}

type JSONLLearningStore struct {
	root string
	mu   sync.Mutex
}

func NewJSONLLearningStore(stateDir string) *JSONLLearningStore {
	return &JSONLLearningStore{root: filepath.Join(stateDir, "learning")}
}

func learningID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("cannot create learning record id")
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *JSONLLearningStore) classDir(class string, operatorID int64) string {
	return filepath.Join(s.root, class, strconv.FormatInt(operatorID, 10))
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return errors.New("cannot create private learning directory")
	}
	if err := os.Chmod(path, 0700); err != nil {
		return errors.New("cannot secure learning directory")
	}
	return nil
}

func (s *JSONLLearningStore) append(class string, operatorID int64, at time.Time, value any) error {
	if operatorID <= 0 || at.IsZero() {
		return errors.New("invalid learning record identity")
	}
	dir := s.classDir(class, operatorID)
	for _, privatePath := range []string{s.root, filepath.Join(s.root, class), dir} {
		if err := ensurePrivateDir(privatePath); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, at.UTC().Format("2006-01-02")+".jsonl")
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxLearningRecordBytes {
		return errors.New("invalid or oversized learning record")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot open private learning record")
	}
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist learning record")
	}
	return nil
}

func (s *JSONLLearningStore) AppendTrace(trace InteractionTrace) error {
	if trace.SchemaVersion != 0 && trace.SchemaVersion != learningSchemaVersion {
		return errors.New("unsupported learning schema version")
	}
	if trace.OperatorID <= 0 || trace.At.IsZero() || strings.TrimSpace(trace.Channel) == "" || strings.TrimSpace(trace.ConversationRef) == "" {
		return errors.New("invalid interaction trace")
	}
	if trace.ID == "" {
		id, err := learningID()
		if err != nil {
			return err
		}
		trace.ID = id
	}
	trace.SchemaVersion = learningSchemaVersion
	if trace.RouterVersion == "" {
		trace.RouterVersion = secretaryRouterVersion
	}
	return s.append("traces", trace.OperatorID, trace.At, trace)
}

func decodeLearningFile[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot read learning records")
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return nil, errors.New("cannot secure learning record")
	}
	reader := bufio.NewReader(f)
	var records []T
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > maxLearningRecordBytes {
			return nil, errors.New("oversized learning record")
		}
		if len(bytesTrimSpace(line)) > 0 {
			var value T
			if json.Unmarshal(line, &value) != nil {
				return nil, errors.New("malformed learning record")
			}
			records = append(records, value)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, errors.New("cannot read learning records")
		}
	}
	return records, nil
}

// bytesTrimSpace avoids importing bytes solely for a single whitespace check.
func bytesTrimSpace(b []byte) []byte { return []byte(strings.TrimSpace(string(b))) }

func jsonlFiles(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, errors.New("cannot list learning records")
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *JSONLLearningStore) LastTrace(operatorID int64, channel, conversationRef string) (InteractionTrace, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, err := jsonlFiles(s.classDir("traces", operatorID))
	if err != nil {
		return InteractionTrace{}, false, err
	}
	var latest InteractionTrace
	found := false
	for _, path := range paths {
		records, err := decodeLearningFile[InteractionTrace](path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return InteractionTrace{}, false, err
		}
		for _, trace := range records {
			if trace.SchemaVersion != learningSchemaVersion {
				return InteractionTrace{}, false, errors.New("unsupported learning schema version")
			}
			if trace.OperatorID == operatorID && trace.Channel == channel && trace.ConversationRef == conversationRef && (!found || trace.At.After(latest.At)) {
				latest, found = trace, true
			}
		}
	}
	return latest, found, nil
}

func (s *JSONLLearningStore) AppendFeedback(event FeedbackEvent) error {
	if event.SchemaVersion != 0 && event.SchemaVersion != learningSchemaVersion {
		return errors.New("unsupported learning schema version")
	}
	if event.OperatorID <= 0 || event.At.IsZero() || strings.TrimSpace(event.InteractionID) == "" || !validFeedbackLabel(event.Label) {
		return errors.New("invalid feedback event")
	}
	if event.ID == "" {
		id, err := learningID()
		if err != nil {
			return err
		}
		event.ID = id
	}
	event.SchemaVersion = learningSchemaVersion
	return s.append("feedback", event.OperatorID, event.At, event)
}

func validFeedbackLabel(label FeedbackLabel) bool {
	switch label {
	case FeedbackCorrect, FeedbackIncorrect, FeedbackWrongSource, FeedbackMissingSource, FeedbackWrongEntity, FeedbackMissingFact, FeedbackStyle:
		return true
	default:
		return false
	}
}

func (s *JSONLLearningStore) SaveKnowledge(item KnowledgeItem) error {
	if item.SchemaVersion != 0 && item.SchemaVersion != learningSchemaVersion {
		return errors.New("unsupported learning schema version")
	}
	if item.OperatorID <= 0 || strings.TrimSpace(item.Value) == "" || !validKnowledgeStatus(item.Status) {
		return errors.New("invalid knowledge item")
	}
	if item.ID == "" {
		id, err := learningID()
		if err != nil {
			return err
		}
		item.ID = id
	}
	item.SchemaVersion = learningSchemaVersion
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	return s.append("knowledge", item.OperatorID, item.CreatedAt, item)
}

func validKnowledgeStatus(status KnowledgeStatus) bool {
	return status == KnowledgeCandidate || status == KnowledgeApproved || status == KnowledgeRejected
}

func (s *JSONLLearningStore) ListKnowledge(operatorID int64, status KnowledgeStatus) ([]KnowledgeItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paths, err := jsonlFiles(s.classDir("knowledge", operatorID))
	if err != nil {
		return nil, err
	}
	latestByID := make(map[string]KnowledgeItem)
	for _, path := range paths {
		records, err := decodeLearningFile[KnowledgeItem](path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, item := range records {
			if item.SchemaVersion != learningSchemaVersion {
				return nil, errors.New("unsupported learning schema version")
			}
			if item.OperatorID == operatorID {
				latestByID[item.ID] = item
			}
		}
	}
	items := make([]KnowledgeItem, 0, len(latestByID))
	for _, item := range latestByID {
		if (status == "" || item.Status == status) && (item.ValidUntil.IsZero() || item.ValidUntil.After(time.Now())) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func rewriteJSONL[T any](path string, records []T) error {
	if len(records) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return errors.New("cannot prune learning records")
		}
		return nil
	}
	data := make([]byte, 0, len(records)*256)
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil || len(line) > maxLearningRecordBytes {
			return errors.New("cannot rewrite learning records")
		}
		data = append(data, line...)
		data = append(data, '\n')
	}
	if err := ensurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".learning-*.tmp")
	if err != nil {
		return errors.New("cannot rewrite learning records")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return errors.New("cannot secure learning records")
	}
	if _, err := tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot rewrite learning records")
	}
	if err := os.Rename(name, path); err != nil {
		return errors.New("cannot rewrite learning records")
	}
	return nil
}

func (s *JSONLLearningStore) Prune(now time.Time, traceDays, candidateDays int) error {
	if traceDays <= 0 || candidateDays <= 0 {
		return errors.New("invalid learning retention")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	traceCutoff := now.AddDate(0, 0, -traceDays)
	candidateCutoff := now.AddDate(0, 0, -candidateDays)
	activeTraceIDs := make(map[string]bool)
	_, err := pruneLearningFiles(s.root, "traces", func(trace InteractionTrace) bool {
		keep := !trace.At.Before(traceCutoff)
		if keep {
			activeTraceIDs[trace.ID] = true
		}
		return keep
	})
	if err != nil {
		return err
	}
	_, err = pruneLearningFiles(s.root, "feedback", func(event FeedbackEvent) bool {
		return !event.At.Before(traceCutoff) && activeTraceIDs[event.InteractionID]
	})
	if err != nil {
		return err
	}
	_, err = pruneLearningFiles(s.root, "knowledge", func(item KnowledgeItem) bool {
		if item.Status == KnowledgeCandidate {
			return !item.CreatedAt.Before(candidateCutoff)
		}
		if item.Status == KnowledgeApproved && !item.ValidUntil.IsZero() {
			return item.ValidUntil.After(now)
		}
		return true
	})
	return err
}

func pruneLearningFiles[T any](root, class string, keep func(T) bool) (map[string]bool, error) {
	active := make(map[string]bool)
	dirs, err := filepath.Glob(filepath.Join(root, class, "*"))
	if err != nil {
		return nil, errors.New("cannot list learning records")
	}
	for _, dir := range dirs {
		if !fileIsDir(dir) {
			continue
		}
		paths, err := jsonlFiles(dir)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			records, err := decodeLearningFile[T](path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			kept := make([]T, 0, len(records))
			for _, record := range records {
				if keep(record) {
					kept = append(kept, record)
				}
			}
			if err := rewriteJSONL(path, kept); err != nil {
				return nil, err
			}
		}
	}
	return active, nil
}

func fileIsDir(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }

func (s *JSONLLearningStore) Forget(operatorID int64) error {
	if operatorID <= 0 {
		return errors.New("invalid operator id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := strconv.FormatInt(operatorID, 10)
	for _, class := range []string{"traces", "feedback", "knowledge"} {
		if err := os.RemoveAll(filepath.Join(s.root, class, id)); err != nil {
			return errors.New("cannot forget learning records")
		}
	}
	return nil
}

var _ LearningStore = (*JSONLLearningStore)(nil)
