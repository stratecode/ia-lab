package secretary

import "time"

const (
	learningSchemaVersion  = 1
	secretaryRouterVersion = "secretary-router-v1"
)

type EntityType string
type KnowledgeStatus string
type KnowledgeType string
type FeedbackLabel string
type EntityResolutionStatus string

const (
	EntityClient  EntityType = "client"
	EntityProject EntityType = "project"
	EntityPerson  EntityType = "person"

	KnowledgeCandidate KnowledgeStatus = "candidate"
	KnowledgeApproved  KnowledgeStatus = "approved"
	KnowledgeRejected  KnowledgeStatus = "rejected"

	KnowledgePreference       KnowledgeType = "preference"
	KnowledgeFact             KnowledgeType = "fact"
	KnowledgeEntityAlias      KnowledgeType = "entity_alias"
	KnowledgeRelationship     KnowledgeType = "relationship"
	KnowledgeProcedure        KnowledgeType = "procedure"
	KnowledgeTypePreference                 = KnowledgePreference
	KnowledgeTypeFact                       = KnowledgeFact
	KnowledgeTypeEntityAlias                = KnowledgeEntityAlias
	KnowledgeTypeRelationship               = KnowledgeRelationship
	KnowledgeTypeProcedure                  = KnowledgeProcedure

	FeedbackCorrect       FeedbackLabel = "correct"
	FeedbackIncorrect     FeedbackLabel = "incorrect"
	FeedbackWrongSource   FeedbackLabel = "wrong_source"
	FeedbackMissingSource FeedbackLabel = "missing_source"
	FeedbackWrongEntity   FeedbackLabel = "wrong_entity"
	FeedbackMissingFact   FeedbackLabel = "missing_fact"
	FeedbackStyle         FeedbackLabel = "style"

	EntityResolved  EntityResolutionStatus = "resolved"
	EntityAmbiguous EntityResolutionStatus = "ambiguous"
	EntityNotFound  EntityResolutionStatus = "not_found"
)

type InteractionTrace struct {
	SchemaVersion   int                 `json:"schema_version"`
	ID              string              `json:"id"`
	OperatorID      int64               `json:"operator_id"`
	At              time.Time           `json:"at"`
	Channel         string              `json:"channel"`
	ConversationRef string              `json:"conversation_ref"`
	Intent          string              `json:"intent"`
	Meaning         string              `json:"meaning"`
	Outcome         string              `json:"outcome"`
	TotalDuration   time.Duration       `json:"total_duration"`
	ModelLabel      string              `json:"model_label,omitempty"`
	RouterVersion   string              `json:"router_version"`
	Capabilities    []CapabilityOutcome `json:"capabilities,omitempty"`
}

type FeedbackInput struct {
	Label      FeedbackLabel `json:"label"`
	Correction string        `json:"correction,omitempty"`
}

type FeedbackEvent struct {
	SchemaVersion int           `json:"schema_version"`
	ID            string        `json:"id"`
	InteractionID string        `json:"interaction_id"`
	OperatorID    int64         `json:"operator_id"`
	At            time.Time     `json:"at"`
	Label         FeedbackLabel `json:"label"`
	Correction    string        `json:"correction,omitempty"`
}

type KnowledgeItem struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	OperatorID    int64             `json:"operator_id"`
	Type          KnowledgeType     `json:"type"`
	EntityType    EntityType        `json:"entity_type,omitempty"`
	Subject       string            `json:"subject,omitempty"`
	Predicate     string            `json:"predicate,omitempty"`
	Value         string            `json:"value"`
	Aliases       []string          `json:"aliases,omitempty"`
	ExternalRefs  map[string]string `json:"external_refs,omitempty"`
	Sources       []EvidenceRef     `json:"sources,omitempty"`
	ValidFrom     time.Time         `json:"valid_from,omitempty"`
	ValidUntil    time.Time         `json:"valid_until,omitempty"`
	Confidence    float64           `json:"confidence"`
	Status        KnowledgeStatus   `json:"status"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type LearningRetention struct {
	TraceDays     int `json:"trace_days"`
	CandidateDays int `json:"candidate_days"`
}

type EntityResolution struct {
	Status       EntityResolutionStatus `json:"status"`
	Item         *KnowledgeItem         `json:"item,omitempty"`
	CandidateIDs []string               `json:"candidate_ids,omitempty"`
}
