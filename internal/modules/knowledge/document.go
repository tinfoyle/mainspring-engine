package knowledge

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumDocumentBytes      = int64(50 << 20)
	MaximumExtractedTextBytes = int64(8 << 20)
	MaximumDocumentTitle      = 200
	MaximumDocumentFilename   = 255
	MaximumChangeSummary      = 1000
	MaximumObjectKey          = 1024
	MaximumProcessorIdentity  = 200
	MaximumDocumentChunks     = uint32(16384)
)

var (
	ErrDocumentRetention = errors.New("document retention prevents deletion")
	ErrDocumentHold      = errors.New("document legal hold prevents deletion")
)

type DocumentState string

const (
	DocumentProcessing      DocumentState = "processing"
	DocumentReady           DocumentState = "ready"
	DocumentFailed          DocumentState = "failed"
	DocumentDeletionPending DocumentState = "deletion_pending"
	DocumentDeleted         DocumentState = "deleted"
)

type Document struct {
	ID                ids.KnowledgeDocumentID
	AccountID         ids.AccountID
	Title             string
	Sensitivity       Sensitivity
	CurrentRevisionID ids.KnowledgeDocumentRevisionID
	CurrentRevision   uint64
	State             DocumentState
	RetainUntil       *time.Time
	LegalHold         bool
	Version           uint64
	CreatedBy         Actor
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeletionRequested *time.Time
	DeletedAt         *time.Time
}

func NewDocument(id ids.KnowledgeDocumentID, accountID ids.AccountID, title string, sensitivity Sensitivity, retainUntil *time.Time, actor Actor, now time.Time) (Document, error) {
	value := Document{ID: id, AccountID: accountID, Title: title, Sensitivity: sensitivity, State: DocumentProcessing, RetainUntil: retainUntil, Version: 1, CreatedBy: actor, CreatedAt: now, UpdatedAt: now}
	return RestoreDocument(value)
}

func RestoreDocument(value Document) (Document, error) {
	value.Title = strings.TrimSpace(value.Title)
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	value.RetainUntil = utcTimePointer(value.RetainUntil)
	value.DeletionRequested = utcTimePointer(value.DeletionRequested)
	value.DeletedAt = utcTimePointer(value.DeletedAt)
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || value.Title == "" || len(value.Title) > MaximumDocumentTitle || strings.ContainsRune(value.Title, '\x00') || !value.Sensitivity.Valid() || !value.CreatedBy.Valid() || value.Version == 0 || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) || (value.RetainUntil != nil && value.RetainUntil.Before(value.CreatedAt)) {
		return Document{}, ErrInvalid
	}
	hasCurrent := value.CurrentRevisionID != "" || value.CurrentRevision != 0
	if hasCurrent && (ids.Validate(string(value.CurrentRevisionID)) != nil || value.CurrentRevision == 0) {
		return Document{}, ErrInvalid
	}
	switch value.State {
	case DocumentProcessing:
		if hasCurrent || value.DeletionRequested != nil || value.DeletedAt != nil {
			return Document{}, ErrInvalid
		}
	case DocumentReady:
		if !hasCurrent || value.DeletionRequested != nil || value.DeletedAt != nil {
			return Document{}, ErrInvalid
		}
	case DocumentFailed:
		if hasCurrent || value.DeletionRequested != nil || value.DeletedAt != nil {
			return Document{}, ErrInvalid
		}
	case DocumentDeletionPending:
		if value.DeletionRequested == nil || value.DeletedAt != nil || value.DeletionRequested.Before(value.CreatedAt) || value.UpdatedAt.Before(*value.DeletionRequested) {
			return Document{}, ErrInvalid
		}
	case DocumentDeleted:
		if value.DeletionRequested == nil || value.DeletedAt == nil || value.DeletedAt.Before(*value.DeletionRequested) || value.UpdatedAt.Before(*value.DeletedAt) || value.LegalHold {
			return Document{}, ErrInvalid
		}
	default:
		return Document{}, ErrInvalid
	}
	return value, nil
}

func (value Document) Publish(revision DocumentRevision, expectedVersion uint64, now time.Time) (Document, error) {
	if expectedVersion != value.Version {
		return Document{}, ErrConflict
	}
	if (value.State != DocumentProcessing && value.State != DocumentReady) || revision.AccountID != value.AccountID || revision.DocumentID != value.ID || revision.State != RevisionReady || revision.Number != value.CurrentRevision+1 || now.IsZero() || now.Before(value.UpdatedAt) {
		return Document{}, ErrState
	}
	result := value
	result.CurrentRevisionID, result.CurrentRevision = revision.ID, revision.Number
	result.State, result.Version, result.UpdatedAt = DocumentReady, result.Version+1, now.UTC()
	return RestoreDocument(result)
}

func (value Document) FailInitial(expectedVersion uint64, now time.Time) (Document, error) {
	if expectedVersion != value.Version {
		return Document{}, ErrConflict
	}
	if value.State != DocumentProcessing || now.IsZero() || now.Before(value.UpdatedAt) {
		return Document{}, ErrState
	}
	value.State, value.Version, value.UpdatedAt = DocumentFailed, value.Version+1, now.UTC()
	return RestoreDocument(value)
}

func (value Document) RequestDeletion(expectedVersion uint64, now time.Time) (Document, error) {
	if expectedVersion != value.Version {
		return Document{}, ErrConflict
	}
	if value.LegalHold {
		return Document{}, ErrDocumentHold
	}
	if value.RetainUntil != nil && now.Before(*value.RetainUntil) {
		return Document{}, ErrDocumentRetention
	}
	if (value.State != DocumentReady && value.State != DocumentFailed) || now.IsZero() || now.Before(value.UpdatedAt) {
		return Document{}, ErrState
	}
	now = now.UTC()
	value.State, value.Version, value.UpdatedAt, value.DeletionRequested = DocumentDeletionPending, value.Version+1, now, &now
	return RestoreDocument(value)
}

func (value Document) CompleteDeletion(expectedVersion uint64, now time.Time) (Document, error) {
	if expectedVersion != value.Version {
		return Document{}, ErrConflict
	}
	if value.LegalHold {
		return Document{}, ErrDocumentHold
	}
	if value.State != DocumentDeletionPending || now.IsZero() || now.Before(value.UpdatedAt) {
		return Document{}, ErrState
	}
	now = now.UTC()
	value.State, value.Version, value.UpdatedAt, value.DeletedAt = DocumentDeleted, value.Version+1, now, &now
	return RestoreDocument(value)
}

type RevisionState string

const (
	RevisionQuarantined RevisionState = "quarantined"
	RevisionExtracting  RevisionState = "extracting"
	RevisionReady       RevisionState = "ready"
	RevisionFailed      RevisionState = "failed"
	RevisionDeleted     RevisionState = "deleted"
)

type ScanState string

const (
	ScanPending  ScanState = "pending"
	ScanClean    ScanState = "clean"
	ScanInfected ScanState = "infected"
	ScanError    ScanState = "error"
)

type ExtractionState string

const (
	ExtractionPending ExtractionState = "pending"
	ExtractionReady   ExtractionState = "ready"
	ExtractionFailed  ExtractionState = "failed"
)

type IndexState string

const (
	IndexPending IndexState = "pending"
	IndexReady   IndexState = "ready"
	IndexFailed  IndexState = "failed"
)

type DocumentRevisionDraft struct {
	ID            ids.KnowledgeDocumentRevisionID
	DocumentID    ids.KnowledgeDocumentID
	AccountID     ids.AccountID
	Number        uint64
	Filename      string
	DeclaredType  string
	VerifiedType  string
	ByteSize      int64
	ContentSHA256 [sha256.Size]byte
	ObjectKey     string
	ObjectVersion string
	ChangeSummary string
	CreatedBy     Actor
}

type DocumentRevision struct {
	ID                     ids.KnowledgeDocumentRevisionID
	DocumentID             ids.KnowledgeDocumentID
	AccountID              ids.AccountID
	Number                 uint64
	Filename               string
	DeclaredType           string
	VerifiedType           string
	ByteSize               int64
	ContentSHA256          [sha256.Size]byte
	ObjectKey              string
	ObjectVersion          string
	ChangeSummary          string
	State                  RevisionState
	ScanState              ScanState
	ScanEngine             string
	ScanSignature          string
	ScannedAt              *time.Time
	Extraction             ExtractionState
	Extractor              string
	TextSHA256             [sha256.Size]byte
	TextBytes              int64
	ExtractedObjectKey     string
	ExtractedObjectVersion string
	ExtractedAt            *time.Time
	Index                  IndexState
	IndexGeneration        string
	ChunkCount             uint32
	IndexedAt              *time.Time
	FailureCode            string
	CreatedBy              Actor
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type DocumentChunk struct {
	ID              ids.KnowledgeDocumentChunkID
	AccountID       ids.AccountID
	RevisionID      ids.KnowledgeDocumentRevisionID
	Index           uint32
	StartByte       int64
	EndByte         int64
	Content         string
	ContentSHA256   [sha256.Size]byte
	TokenCount      uint32
	IndexGeneration string
	CreatedAt       time.Time
}

func NewDocumentChunk(value DocumentChunk) (DocumentChunk, error) {
	value.IndexGeneration = strings.TrimSpace(value.IndexGeneration)
	value.CreatedAt = value.CreatedAt.UTC()
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.AccountID)) != nil || ids.Validate(string(value.RevisionID)) != nil || value.Index >= MaximumDocumentChunks || value.StartByte < 0 || value.EndByte <= value.StartByte || value.Content == "" || len(value.Content) > 65536 || !utf8.ValidString(value.Content) || strings.ContainsRune(value.Content, '\x00') || value.ContentSHA256 != sha256.Sum256([]byte(value.Content)) || value.TokenCount == 0 || value.IndexGeneration == "" || !validProcessor(value.IndexGeneration) || value.CreatedAt.IsZero() {
		return DocumentChunk{}, ErrInvalid
	}
	return value, nil
}

func NewDocumentRevision(draft DocumentRevisionDraft, now time.Time) (DocumentRevision, error) {
	value := DocumentRevision{ID: draft.ID, DocumentID: draft.DocumentID, AccountID: draft.AccountID, Number: draft.Number, Filename: draft.Filename, DeclaredType: draft.DeclaredType, VerifiedType: draft.VerifiedType, ByteSize: draft.ByteSize, ContentSHA256: draft.ContentSHA256, ObjectKey: draft.ObjectKey, ObjectVersion: draft.ObjectVersion, ChangeSummary: draft.ChangeSummary, State: RevisionQuarantined, ScanState: ScanPending, Extraction: ExtractionPending, Index: IndexPending, CreatedBy: draft.CreatedBy, CreatedAt: now, UpdatedAt: now}
	return RestoreDocumentRevision(value)
}

func RestoreDocumentRevision(value DocumentRevision) (DocumentRevision, error) {
	value.Filename, value.DeclaredType, value.VerifiedType = strings.TrimSpace(value.Filename), normalizeMediaType(value.DeclaredType), normalizeMediaType(value.VerifiedType)
	value.ObjectKey, value.ObjectVersion, value.ChangeSummary = strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.ObjectVersion), strings.TrimSpace(value.ChangeSummary)
	value.ExtractedObjectKey, value.ExtractedObjectVersion = strings.TrimSpace(value.ExtractedObjectKey), strings.TrimSpace(value.ExtractedObjectVersion)
	value.ScanEngine, value.ScanSignature, value.Extractor, value.IndexGeneration, value.FailureCode = strings.TrimSpace(value.ScanEngine), strings.TrimSpace(value.ScanSignature), strings.TrimSpace(value.Extractor), strings.TrimSpace(value.IndexGeneration), strings.TrimSpace(value.FailureCode)
	value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
	value.ScannedAt, value.ExtractedAt, value.IndexedAt = utcTimePointer(value.ScannedAt), utcTimePointer(value.ExtractedAt), utcTimePointer(value.IndexedAt)
	if ids.Validate(string(value.ID)) != nil || ids.Validate(string(value.DocumentID)) != nil || ids.Validate(string(value.AccountID)) != nil || value.Number == 0 || !validDocumentName(value.Filename) || !validMediaPair(value.Filename, value.DeclaredType, value.VerifiedType) || value.ByteSize <= 0 || value.ByteSize > MaximumDocumentBytes || value.ContentSHA256 == ([sha256.Size]byte{}) || !validObjectIdentity(value) || !validExtractedObjectIdentity(value) || len(value.ChangeSummary) > MaximumChangeSummary || !value.CreatedBy.Valid() || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return DocumentRevision{}, ErrInvalid
	}
	if !validProcessor(value.ScanEngine) || !validProcessor(value.ScanSignature) || !validProcessor(value.Extractor) || !validProcessor(value.IndexGeneration) || !validFailureCode(value.FailureCode) {
		return DocumentRevision{}, ErrInvalid
	}
	if err := validateRevisionProcessing(value); err != nil {
		return DocumentRevision{}, err
	}
	return value, nil
}

func (value DocumentRevision) RecordScan(state ScanState, engine, signature string, now time.Time) (DocumentRevision, error) {
	if value.State != RevisionQuarantined || value.ScanState != ScanPending || now.IsZero() || now.Before(value.UpdatedAt) || (state != ScanClean && state != ScanInfected && state != ScanError) {
		return DocumentRevision{}, ErrState
	}
	now = now.UTC()
	value.ScanState, value.ScanEngine, value.ScanSignature, value.ScannedAt, value.UpdatedAt = state, strings.TrimSpace(engine), strings.TrimSpace(signature), &now, now
	if state == ScanClean {
		value.State = RevisionExtracting
	} else {
		value.State, value.Extraction, value.Index, value.FailureCode = RevisionFailed, ExtractionFailed, IndexFailed, "malware_scan_"+string(state)
	}
	return RestoreDocumentRevision(value)
}

func (value DocumentRevision) RecordExtraction(textSHA256 [sha256.Size]byte, textBytes int64, extractor, objectKey, objectVersion string, now time.Time) (DocumentRevision, error) {
	if value.State != RevisionExtracting || value.ScanState != ScanClean || value.Extraction != ExtractionPending || textSHA256 == ([sha256.Size]byte{}) || textBytes <= 0 || textBytes > MaximumExtractedTextBytes || now.IsZero() || now.Before(value.UpdatedAt) {
		return DocumentRevision{}, ErrState
	}
	now = now.UTC()
	value.Extraction, value.TextSHA256, value.TextBytes, value.Extractor, value.ExtractedObjectKey, value.ExtractedObjectVersion, value.ExtractedAt, value.UpdatedAt = ExtractionReady, textSHA256, textBytes, strings.TrimSpace(extractor), strings.TrimSpace(objectKey), strings.TrimSpace(objectVersion), &now, now
	return RestoreDocumentRevision(value)
}

func (value DocumentRevision) RecordIndex(generation string, chunkCount uint32, now time.Time) (DocumentRevision, error) {
	if value.State != RevisionExtracting || value.Extraction != ExtractionReady || value.Index != IndexPending || chunkCount == 0 || chunkCount > MaximumDocumentChunks || now.IsZero() || now.Before(value.UpdatedAt) {
		return DocumentRevision{}, ErrState
	}
	now = now.UTC()
	value.State, value.Index, value.IndexGeneration, value.ChunkCount, value.IndexedAt, value.UpdatedAt = RevisionReady, IndexReady, strings.TrimSpace(generation), chunkCount, &now, now
	return RestoreDocumentRevision(value)
}

func (value DocumentRevision) FailProcessing(code string, now time.Time) (DocumentRevision, error) {
	if value.State != RevisionExtracting || now.IsZero() || now.Before(value.UpdatedAt) {
		return DocumentRevision{}, ErrState
	}
	now = now.UTC()
	value.State, value.FailureCode, value.UpdatedAt = RevisionFailed, strings.TrimSpace(code), now
	if value.Extraction == ExtractionPending {
		value.Extraction = ExtractionFailed
	}
	value.Index = IndexFailed
	return RestoreDocumentRevision(value)
}

func (value DocumentRevision) MarkDeleted(now time.Time) (DocumentRevision, error) {
	if (value.State != RevisionReady && value.State != RevisionFailed) || now.IsZero() || now.Before(value.UpdatedAt) {
		return DocumentRevision{}, ErrState
	}
	value.State, value.UpdatedAt = RevisionDeleted, now.UTC()
	return RestoreDocumentRevision(value)
}

func validateRevisionProcessing(value DocumentRevision) error {
	scanCompleted := value.ScannedAt != nil && value.ScanEngine != ""
	extractionCompleted := value.ExtractedAt != nil && value.Extractor != "" && value.TextSHA256 != ([sha256.Size]byte{}) && value.TextBytes > 0 && value.TextBytes <= MaximumExtractedTextBytes
	indexCompleted := value.IndexedAt != nil && value.IndexGeneration != "" && value.ChunkCount > 0
	switch value.State {
	case RevisionQuarantined:
		if value.ScanState != ScanPending || scanCompleted || value.Extraction != ExtractionPending || extractionCompleted || value.Index != IndexPending || indexCompleted || value.FailureCode != "" {
			return ErrInvalid
		}
	case RevisionExtracting:
		if value.ScanState != ScanClean || !scanCompleted || value.Extraction == ExtractionFailed || (value.Extraction == ExtractionReady) != extractionCompleted || value.Index != IndexPending || indexCompleted || value.FailureCode != "" {
			return ErrInvalid
		}
	case RevisionReady:
		if value.ScanState != ScanClean || !scanCompleted || value.Extraction != ExtractionReady || !extractionCompleted || value.Index != IndexReady || !indexCompleted || value.FailureCode != "" {
			return ErrInvalid
		}
	case RevisionFailed:
		if value.ScanState == ScanPending || !scanCompleted || (value.Extraction != ExtractionFailed && value.Extraction != ExtractionReady) || (value.Extraction == ExtractionReady) != extractionCompleted || value.Index != IndexFailed || value.FailureCode == "" || indexCompleted {
			return ErrInvalid
		}
	case RevisionDeleted:
		if value.ScanState == ScanPending || !scanCompleted || value.Extraction == ExtractionPending || value.Index == IndexPending {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	for _, instant := range []*time.Time{value.ScannedAt, value.ExtractedAt, value.IndexedAt} {
		if instant != nil && (instant.Before(value.CreatedAt) || instant.After(value.UpdatedAt)) {
			return ErrInvalid
		}
	}
	return nil
}

func validDocumentName(value string) bool {
	return value != "" && len(value) <= MaximumDocumentFilename && filepath.Base(value) == value && value != "." && !strings.ContainsAny(value, "\x00/\\")
}

func validMediaPair(filename, declared, verified string) bool {
	expected, ok := ExpectedDocumentMediaType(filename)
	return ok && expected == verified && (declared == "" || declared == "application/octet-stream" || declared == verified)
}

func ExpectedDocumentMediaType(filename string) (string, bool) {
	if !validDocumentName(filename) {
		return "", false
	}
	value, ok := documentMediaTypes[strings.ToLower(filepath.Ext(filename))]
	return value, ok
}

var documentMediaTypes = map[string]string{
	".txt": "text/plain", ".log": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown",
	".csv": "text/csv", ".tsv": "text/tab-separated-values", ".json": "application/json", ".xml": "application/xml",
	".html": "text/html", ".htm": "text/html", ".yaml": "application/yaml", ".yml": "application/yaml",
	".pdf": "application/pdf", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
}

func normalizeMediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
}

func validObjectIdentity(value DocumentRevision) bool {
	expected, err := SourceObjectKey(value.AccountID, value.DocumentID, value.ID)
	if err != nil {
		return false
	}
	return value.ObjectKey == expected && len(value.ObjectKey) <= MaximumObjectKey && value.ObjectVersion != "" && len(value.ObjectVersion) <= 256
}

func SourceObjectKey(accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID) (string, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil || ids.Validate(string(revisionID)) != nil {
		return "", ErrInvalid
	}
	return fmt.Sprintf("accounts/%s/documents/%s/revisions/%s/source", accountID, documentID, revisionID), nil
}

func ExtractedObjectKey(accountID ids.AccountID, documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID) (string, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil || ids.Validate(string(revisionID)) != nil {
		return "", ErrInvalid
	}
	return fmt.Sprintf("accounts/%s/documents/%s/revisions/%s/extracted/text", accountID, documentID, revisionID), nil
}

func validExtractedObjectIdentity(value DocumentRevision) bool {
	if value.Extraction != ExtractionReady {
		return value.ExtractedObjectKey == "" && value.ExtractedObjectVersion == ""
	}
	expected, err := ExtractedObjectKey(value.AccountID, value.DocumentID, value.ID)
	return err == nil && value.ExtractedObjectKey == expected && len(value.ExtractedObjectKey) <= MaximumObjectKey && value.ExtractedObjectVersion != "" && len(value.ExtractedObjectVersion) <= 256
}

func validProcessor(value string) bool {
	return len(value) <= MaximumProcessorIdentity && !strings.ContainsRune(value, '\x00')
}

func validFailureCode(value string) bool { return value == "" || codePattern.MatchString(value) }

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()
	return &result
}
