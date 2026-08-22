// Package prototypemigration transforms the retained Mainspring prototype's
// per-tenant records into a deterministic, review-first Spyglass migration
// bundle. It never promotes ambiguous legacy provenance to authoritative
// Knowledge.
package prototypemigration

import (
	"encoding/json"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const ManifestVersion = "spyglass-prototype-knowledge-v1"

type Inventory struct {
	FactVersions      uint64 `json:"fact_versions"`
	CurrentFacts      uint64 `json:"current_facts"`
	DocumentRevisions uint64 `json:"document_revisions"`
	Assessments       uint64 `json:"assessments"`
	AssessmentFacts   uint64 `json:"assessment_facts"`
	Requirements      uint64 `json:"requirements"`
	EvidenceLinks     uint64 `json:"evidence_links"`
	SourceItems       uint64 `json:"source_items"`
	DocumentChunks    uint64 `json:"document_chunks"`
	InterviewMessages uint64 `json:"interview_messages"`
	BaselineWorkLinks uint64 `json:"baseline_work_links"`
	ResearchResults   uint64 `json:"research_results"`
}

type Snapshot struct {
	TenantID       string
	Checkpoint     string
	SchemaVersions []string
	Inventory      Inventory
	FactVersions   []LegacyFactVersion
	CurrentFacts   []LegacyCurrentFact
	Documents      []LegacyDocumentRevision
	Assessments    []LegacyAssessment
	Deferred       []LegacyDeferredRecord
}

type LegacyFactVersion struct {
	ID          string
	Key         string
	Label       string
	Value       string
	Scope       string
	SourceType  string
	SourceRef   string
	Confidence  string
	Sensitivity string
	Status      string
	RecordedAt  time.Time
}

type LegacyCurrentFact struct {
	Key         string
	Label       string
	Value       string
	Scope       string
	SourceType  string
	SourceRef   string
	Confidence  string
	Sensitivity string
	Status      string
	ConfirmedAt *time.Time
	UpdatedAt   time.Time
}

type LegacyDocumentRevision struct {
	DocumentID    string
	RevisionID    string
	Revision      uint64
	Name          string
	MediaType     string
	Content       []byte
	StoredSHA256  []byte
	ChangeSummary string
	Status        string
	DeletedAt     *time.Time
	CreatedAt     time.Time
}

type LegacyAssessment struct {
	ID                 string
	Status             string
	Phase              string
	BaselineVersion    uint64
	NextReassessmentAt *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Facts              []LegacyAssessmentFact
	Requirements       []LegacyRequirement
}

type LegacyAssessmentFact struct {
	ID          string
	Key         string
	Value       string
	SourceType  string
	SourceRef   string
	Confidence  string
	ConfirmedAt *time.Time
	UpdatedAt   time.Time
}

type LegacyRequirement struct {
	ID             string
	Key            string
	Status         string
	Disposition    string
	Responsibility string
	RenewalDueAt   *time.Time
	Evidence       []LegacyEvidenceLink
}

type LegacyEvidenceLink struct {
	ID           string
	DocumentID   string
	SourceItemID string
	PublicURL    string
	VerifiedAt   *time.Time
}

type LegacyDeferredRecord struct {
	Kind     string
	SourceID string
	Detail   string
}

type Bundle struct {
	Manifest Manifest
	Objects  map[string][]byte
}

type Manifest struct {
	Version       string             `json:"version"`
	AccountID     ids.AccountID      `json:"account_id"`
	Source        SourceCheckpoint   `json:"source"`
	Facts         []FactPlan         `json:"facts"`
	Documents     []DocumentPlan     `json:"documents"`
	Baselines     []BaselinePlan     `json:"baselines"`
	Unresolved    []UnresolvedRecord `json:"unresolved"`
	Totals        ManifestTotals     `json:"totals"`
	ContentSHA256 string             `json:"content_sha256"`
}

type SourceCheckpoint struct {
	TenantID        string    `json:"tenant_id"`
	Checkpoint      string    `json:"checkpoint"`
	SchemaVersions  []string  `json:"schema_versions"`
	Inventory       Inventory `json:"inventory"`
	InventorySHA256 string    `json:"inventory_sha256"`
}

type FactPlan struct {
	SourceID         string          `json:"source_id"`
	SourceCurrent    bool            `json:"source_current"`
	Key              string          `json:"key"`
	Scope            string          `json:"scope"`
	CanonicalValue   json.RawMessage `json:"canonical_value"`
	Confidence       uint16          `json:"confidence"`
	Sensitivity      string          `json:"sensitivity"`
	SourceType       string          `json:"source_type"`
	SourceReference  string          `json:"source_reference,omitempty"`
	SourceRevision   string          `json:"source_revision,omitempty"`
	ContentSHA256    string          `json:"content_sha256"`
	EvidenceSHA256   string          `json:"evidence_sha256,omitempty"`
	EvidenceKind     string          `json:"evidence_kind,omitempty"`
	TargetEvidenceID string          `json:"target_evidence_id,omitempty"`
	TargetClaimID    string          `json:"target_claim_id,omitempty"`
	Action           string          `json:"action"`
	RecordedAt       time.Time       `json:"recorded_at"`
}

type DocumentPlan struct {
	SourceDocumentID  string    `json:"source_document_id"`
	SourceRevisionID  string    `json:"source_revision_id"`
	SourceRevision    uint64    `json:"source_revision"`
	TargetDocumentID  string    `json:"target_document_id,omitempty"`
	TargetRevisionID  string    `json:"target_revision_id,omitempty"`
	Title             string    `json:"title"`
	Filename          string    `json:"filename"`
	OriginalMediaType string    `json:"original_media_type"`
	ImportMediaType   string    `json:"import_media_type"`
	ObjectPath        string    `json:"object_path,omitempty"`
	ContentSHA256     string    `json:"content_sha256"`
	ByteSize          int64     `json:"byte_size"`
	ExpectedChunks    uint32    `json:"expected_chunks"`
	Action            string    `json:"action"`
	CreatedAt         time.Time `json:"created_at"`
}

type BaselinePlan struct {
	SourceAssessmentID string                  `json:"source_assessment_id"`
	SourceVersion      uint64                  `json:"source_version"`
	SourceStatus       string                  `json:"source_status"`
	SourcePhase        string                  `json:"source_phase"`
	Action             string                  `json:"action"`
	Answers            []BaselineAnswerPlan    `json:"answers"`
	Requirements       []RequirementReviewPlan `json:"requirements"`
	NextReassessmentAt *time.Time              `json:"next_reassessment_at,omitempty"`
}

type BaselineAnswerPlan struct {
	SourceFactID string          `json:"source_fact_id"`
	QuestionKey  string          `json:"question_key"`
	Value        json.RawMessage `json:"value"`
	Action       string          `json:"action"`
}

type RequirementReviewPlan struct {
	SourceRequirementID string     `json:"source_requirement_id"`
	Code                string     `json:"code"`
	SourceStatus        string     `json:"source_status"`
	SourceDisposition   string     `json:"source_disposition"`
	Action              string     `json:"action"`
	RenewalDueAt        *time.Time `json:"renewal_due_at,omitempty"`
	EvidenceLinkCount   uint32     `json:"evidence_link_count"`
}

type UnresolvedRecord struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id"`
	Code     string `json:"code"`
	Detail   string `json:"detail"`
}

type ManifestTotals struct {
	FactRecords         uint64 `json:"fact_records"`
	ImportableClaims    uint64 `json:"importable_claims"`
	DocumentRevisions   uint64 `json:"document_revisions"`
	ImportableDocuments uint64 `json:"importable_documents"`
	ExpectedChunks      uint64 `json:"expected_chunks"`
	BaselineAssessments uint64 `json:"baseline_assessments"`
	BaselineAnswers     uint64 `json:"baseline_answers"`
	RequirementReviews  uint64 `json:"requirement_reviews"`
	UnresolvedRecords   uint64 `json:"unresolved_records"`
}
