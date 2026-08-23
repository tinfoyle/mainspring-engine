package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type DocumentRepository interface {
	AdmitDocument(context.Context, knowledgedomain.Document, knowledgedomain.DocumentRevision, Mutation) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error)
	AdmitDocumentRevision(context.Context, knowledgedomain.Document, knowledgedomain.DocumentRevision, Mutation) (knowledgedomain.DocumentRevision, error)
	GetDocument(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.Document, error)
	GetLatestDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.DocumentRevision, error)
	GetDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentRevisionID) (knowledgedomain.DocumentRevision, error)
	ListDocuments(context.Context, ids.AccountID, DocumentListQuery) (DocumentPage, error)
	SaveDocumentRevision(context.Context, knowledgedomain.DocumentRevision, time.Time, string, Mutation) (knowledgedomain.DocumentRevision, error)
	IndexDocumentRevision(context.Context, knowledgedomain.DocumentRevision, time.Time, []knowledgedomain.DocumentChunk, Mutation) (knowledgedomain.DocumentRevision, error)
	PublishDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, uint64, Mutation) (knowledgedomain.Document, error)
	RequestDocumentDeletion(context.Context, ids.AccountID, ids.KnowledgeDocumentID, uint64, Mutation) (knowledgedomain.Document, error)
}

type DocumentService struct {
	authorizer Authorizer
	repository DocumentRepository
	retrieval  DocumentRetrievalRepository
	clock      Clock
}

type DocumentSummary struct {
	ID                ids.KnowledgeDocumentID
	Title             string
	Sensitivity       knowledgedomain.Sensitivity
	CurrentRevisionID ids.KnowledgeDocumentRevisionID
	CurrentRevision   uint64
	State             knowledgedomain.DocumentState
	RetainUntil       *time.Time
	LegalHold         bool
	Version           uint64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type DocumentCursor struct {
	UpdatedAt time.Time
	ID        ids.KnowledgeDocumentID
}

type DocumentListQuery struct {
	State             knowledgedomain.DocumentState
	TitlePrefix       string
	AfterUpdatedAt    *time.Time
	AfterID           ids.KnowledgeDocumentID
	Limit             int
	IncludeRestricted bool
}

type DocumentPage struct {
	Items      []DocumentSummary
	NextCursor *DocumentCursor
}

type DocumentDetail struct {
	Document       knowledgedomain.Document
	LatestRevision knowledgedomain.DocumentRevision
}

func NewDocumentService(authorizer Authorizer, repository DocumentRepository, clock Clock) (*DocumentService, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("Knowledge document dependencies are required")
	}
	retrieval, ok := repository.(DocumentRetrievalRepository)
	if !ok {
		return nil, errors.New("Knowledge document retrieval repository is required")
	}
	return &DocumentService{authorizer: authorizer, repository: repository, retrieval: retrieval, clock: clock}, nil
}

type AdmitDocumentCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	DocumentID    ids.KnowledgeDocumentID
	RevisionID    ids.KnowledgeDocumentRevisionID
	Title         string
	Sensitivity   knowledgedomain.Sensitivity
	RetainUntil   *time.Time
	Filename      string
	DeclaredType  string
	VerifiedType  string
	ByteSize      int64
	ContentSHA256 [sha256.Size]byte
	ObjectKey     string
	ObjectVersion string
	ChangeSummary string
	CorrelationID string
}

func (s *DocumentService) Admit(ctx context.Context, command AdmitDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	document, revision, actor, err := s.buildAdmission(ctx, command)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	return s.repository.AdmitDocument(ctx, document, revision, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "document_admitted", At: document.CreatedAt})
}

type AdmitDocumentRevisionCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	DocumentID    ids.KnowledgeDocumentID
	RevisionID    ids.KnowledgeDocumentRevisionID
	Filename      string
	DeclaredType  string
	VerifiedType  string
	ByteSize      int64
	ContentSHA256 [sha256.Size]byte
	ObjectKey     string
	ObjectVersion string
	ChangeSummary string
	CorrelationID string
}

func (s *DocumentService) AdmitRevision(ctx context.Context, command AdmitDocumentRevisionCommand) (knowledgedomain.DocumentRevision, error) {
	document, revision, actor, err := s.buildRevisionAdmission(ctx, command)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	return s.repository.AdmitDocumentRevision(ctx, document, revision, Mutation{Actor: actor, CorrelationID: command.CorrelationID,
		ReasonCode: "document_revision_admitted", At: revision.CreatedAt})
}

func (s *DocumentService) buildRevisionAdmission(ctx context.Context, command AdmitDocumentRevisionCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, knowledgedomain.Actor, error) {
	actor, accountContext, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, err
	}
	if ids.Validate(string(command.DocumentID)) != nil || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrInvalid
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	document, err := s.repository.GetDocument(ctx, command.AccountID, command.DocumentID)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, err
	}
	number := uint64(0)
	existing, existingErr := s.repository.GetDocumentRevision(ctx, command.AccountID, command.RevisionID)
	if existingErr == nil {
		number = existing.Number
	} else if !errors.Is(existingErr, ErrNotFound) {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, existingErr
	} else {
		latest, latestErr := s.repository.GetLatestDocumentRevision(ctx, command.AccountID, command.DocumentID)
		if latestErr != nil || !canAdmitDocumentRevision(document, latest) {
			if latestErr != nil {
				return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, latestErr
			}
			return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrConstraint
		}
		number = latest.Number + 1
	}
	now := s.clock.Now().UTC()
	revision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{ID: command.RevisionID,
		DocumentID: command.DocumentID, AccountID: command.AccountID, Number: number, Filename: command.Filename,
		DeclaredType: command.DeclaredType, VerifiedType: command.VerifiedType, ByteSize: command.ByteSize,
		ContentSHA256: command.ContentSHA256, ObjectKey: command.ObjectKey, ObjectVersion: command.ObjectVersion,
		ChangeSummary: command.ChangeSummary, CreatedBy: actor}, now)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrInvalid
	}
	return document, revision, actor, nil
}

func canAdmitDocumentRevision(document knowledgedomain.Document, latest knowledgedomain.DocumentRevision) bool {
	if latest.AccountID != document.AccountID || latest.DocumentID != document.ID || latest.Number < document.CurrentRevision {
		return false
	}
	if document.State == knowledgedomain.DocumentReady && latest.Number == document.CurrentRevision {
		return latest.ID == document.CurrentRevisionID && latest.State == knowledgedomain.RevisionReady
	}
	return (document.State == knowledgedomain.DocumentReady || document.State == knowledgedomain.DocumentFailed) &&
		latest.State == knowledgedomain.RevisionFailed
}

func (s *DocumentService) buildAdmission(ctx context.Context, command AdmitDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, knowledgedomain.Actor, error) {
	actor, accountContext, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, err
	}
	if ids.Validate(string(command.DocumentID)) != nil || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrInvalid
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	now := s.clock.Now().UTC()
	document, err := knowledgedomain.NewDocument(command.DocumentID, command.AccountID, command.Title, command.Sensitivity, command.RetainUntil, actor, now)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrInvalid
	}
	revision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID, Number: 1, Filename: command.Filename, DeclaredType: command.DeclaredType, VerifiedType: command.VerifiedType, ByteSize: command.ByteSize, ContentSHA256: command.ContentSHA256, ObjectKey: command.ObjectKey, ObjectVersion: command.ObjectVersion, ChangeSummary: command.ChangeSummary, CreatedBy: actor}, now)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, knowledgedomain.Actor{}, ErrInvalid
	}
	return document, revision, actor, nil
}

type RecordScanCommand struct {
	Actor                            access.Actor
	AccountID                        ids.AccountID
	RevisionID                       ids.KnowledgeDocumentRevisionID
	State                            knowledgedomain.ScanState
	Engine, Signature, CorrelationID string
}

func (s *DocumentService) RecordScan(ctx context.Context, command RecordScanCommand) (knowledgedomain.DocumentRevision, error) {
	actor, _, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	if actor.Kind != knowledgedomain.ActorWorkload || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	return s.recordScan(ctx, actor, command.AccountID, command.RevisionID, command.State, command.Engine, command.Signature, command.CorrelationID, s.clock.Now().UTC())
}

func (s *DocumentService) recordScan(ctx context.Context, actor knowledgedomain.Actor, accountID ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID, state knowledgedomain.ScanState, engine, signature, correlationID string, now time.Time) (knowledgedomain.DocumentRevision, error) {
	current, err := s.repository.GetDocumentRevision(ctx, accountID, revisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	result, err := current.RecordScan(state, engine, signature, now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	event := "scan_completed"
	if result.State == knowledgedomain.RevisionFailed {
		event = "processing_failed"
	}
	return s.repository.SaveDocumentRevision(ctx, result, current.UpdatedAt, event, Mutation{Actor: actor, CorrelationID: correlationID, ReasonCode: event, At: now})
}

type RecordExtractionCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	RevisionID    ids.KnowledgeDocumentRevisionID
	TextSHA256    [sha256.Size]byte
	TextBytes     int64
	Extractor     string
	ObjectKey     string
	ObjectVersion string
	CorrelationID string
}

func (s *DocumentService) RecordExtraction(ctx context.Context, command RecordExtractionCommand) (knowledgedomain.DocumentRevision, error) {
	actor, _, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	if actor.Kind != knowledgedomain.ActorWorkload || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	return s.recordExtraction(ctx, actor, command.AccountID, command.RevisionID, command.TextSHA256, command.TextBytes, command.Extractor, command.ObjectKey, command.ObjectVersion, command.CorrelationID, s.clock.Now().UTC())
}

func (s *DocumentService) recordExtraction(ctx context.Context, actor knowledgedomain.Actor, accountID ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID, textSHA256 [sha256.Size]byte, textBytes int64, extractor, objectKey, objectVersion, correlationID string, now time.Time) (knowledgedomain.DocumentRevision, error) {
	current, err := s.repository.GetDocumentRevision(ctx, accountID, revisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	result, err := current.RecordExtraction(textSHA256, textBytes, extractor, objectKey, objectVersion, now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	return s.repository.SaveDocumentRevision(ctx, result, current.UpdatedAt, "extraction_completed", Mutation{Actor: actor, CorrelationID: correlationID, ReasonCode: "extraction_completed", At: now})
}

type IndexDocumentCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	RevisionID    ids.KnowledgeDocumentRevisionID
	Generation    string
	Chunks        []DocumentChunkDraft
	CorrelationID string
}

type DocumentChunkDraft struct {
	StartByte  int64
	EndByte    int64
	Content    string
	TokenCount uint32
}

func (s *DocumentService) Index(ctx context.Context, command IndexDocumentCommand) (knowledgedomain.DocumentRevision, error) {
	actor, _, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	if actor.Kind != knowledgedomain.ActorWorkload || ids.Validate(string(command.RevisionID)) != nil || len(command.Chunks) == 0 || len(command.Chunks) > int(knowledgedomain.MaximumDocumentChunks) {
		return knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	return s.index(ctx, actor, command.AccountID, command.RevisionID, command.Generation, command.Chunks, command.CorrelationID, s.clock.Now().UTC())
}

func (s *DocumentService) index(ctx context.Context, actor knowledgedomain.Actor, accountID ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID, generation string, drafts []DocumentChunkDraft, correlationID string, now time.Time) (knowledgedomain.DocumentRevision, error) {
	if len(drafts) == 0 || len(drafts) > int(knowledgedomain.MaximumDocumentChunks) {
		return knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	current, err := s.repository.GetDocumentRevision(ctx, accountID, revisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	result, err := current.RecordIndex(generation, uint32(len(drafts)), now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	chunks := make([]knowledgedomain.DocumentChunk, len(drafts))
	for index, draft := range drafts {
		chunkID, deriveErr := ids.Derive(correlationID, fmt.Sprintf("document-chunk-%d", index))
		if deriveErr != nil {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		chunk, chunkErr := knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: ids.KnowledgeDocumentChunkID(chunkID), AccountID: accountID, RevisionID: revisionID, Index: uint32(index), StartByte: draft.StartByte, EndByte: draft.EndByte, Content: draft.Content, ContentSHA256: sha256.Sum256([]byte(draft.Content)), TokenCount: draft.TokenCount, IndexGeneration: result.IndexGeneration, CreatedAt: now})
		if chunkErr != nil {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		if chunk.EndByte > current.TextBytes {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		chunks[index] = chunk
	}
	return s.repository.IndexDocumentRevision(ctx, result, current.UpdatedAt, chunks, Mutation{Actor: actor, CorrelationID: correlationID, ReasonCode: "index_completed", At: now})
}

type PublishDocumentCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	DocumentID      ids.KnowledgeDocumentID
	RevisionID      ids.KnowledgeDocumentRevisionID
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *DocumentService) Publish(ctx context.Context, command PublishDocumentCommand) (knowledgedomain.Document, error) {
	actor, accountContext, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	if actor.Kind != knowledgedomain.ActorUser || ids.Validate(string(command.DocumentID)) != nil || ids.Validate(string(command.RevisionID)) != nil || command.ExpectedVersion == 0 {
		return knowledgedomain.Document{}, ErrInvalid
	}
	if !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	now := s.clock.Now().UTC()
	return s.repository.PublishDocumentRevision(ctx, command.AccountID, command.DocumentID, command.RevisionID, command.ExpectedVersion, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "revision_published", At: now})
}

func (s *DocumentService) publishSourceRevision(ctx context.Context, actor knowledgedomain.Actor, accountID ids.AccountID,
	documentID ids.KnowledgeDocumentID, revisionID ids.KnowledgeDocumentRevisionID) (knowledgedomain.Document, error) {
	if actor.Kind != knowledgedomain.ActorWorkload || actor.ID != documentProcessingWorkloadID {
		return knowledgedomain.Document{}, ErrInvalid
	}
	correlationID := string(revisionID)
	if _, _, err := s.authorizeMutation(ctx, access.Actor{WorkloadID: actor.ID}, accountID, correlationID); err != nil {
		return knowledgedomain.Document{}, err
	}
	document, err := s.repository.GetDocument(ctx, accountID, documentID)
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	if document.State == knowledgedomain.DocumentReady && document.CurrentRevisionID == revisionID {
		return document, nil
	}
	revision, err := s.repository.GetDocumentRevision(ctx, accountID, revisionID)
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	if revision.DocumentID != documentID || revision.CreatedBy.Kind != knowledgedomain.ActorWorkload ||
		revision.CreatedBy.ID != IntegrationSourceSyncWorkloadID || revision.State != knowledgedomain.RevisionReady {
		return knowledgedomain.Document{}, ErrConstraint
	}
	now := s.clock.Now().UTC()
	return s.repository.PublishDocumentRevision(ctx, accountID, documentID, revisionID, document.Version,
		Mutation{Actor: actor, CorrelationID: correlationID, ReasonCode: "source_revision_published", At: now})
}

type DeleteDocumentCommand struct {
	Actor           access.Actor
	AccountID       ids.AccountID
	DocumentID      ids.KnowledgeDocumentID
	ExpectedVersion uint64
	CorrelationID   string
}

func (s *DocumentService) Delete(ctx context.Context, command DeleteDocumentCommand) (knowledgedomain.Document, error) {
	actor, accountContext, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	if actor.Kind != knowledgedomain.ActorUser || ids.Validate(string(command.DocumentID)) != nil || command.ExpectedVersion == 0 {
		return knowledgedomain.Document{}, ErrInvalid
	}
	if !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	now := s.clock.Now().UTC()
	return s.repository.RequestDocumentDeletion(ctx, command.AccountID, command.DocumentID, command.ExpectedVersion, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "deletion_requested", At: now})
}

type DeleteSourceDocumentCommand struct {
	Actor         access.Actor
	AccountID     ids.AccountID
	DocumentID    ids.KnowledgeDocumentID
	RevisionID    ids.KnowledgeDocumentRevisionID
	ContentSHA256 [sha256.Size]byte
	CorrelationID string
}

func (s *DocumentService) DeleteSource(ctx context.Context, command DeleteSourceDocumentCommand) (DocumentDetail, error) {
	actor, _, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return DocumentDetail{}, err
	}
	if actor.Kind != knowledgedomain.ActorWorkload || ids.Validate(string(command.DocumentID)) != nil ||
		ids.Validate(string(command.RevisionID)) != nil || command.ContentSHA256 == ([sha256.Size]byte{}) {
		return DocumentDetail{}, ErrInvalid
	}
	document, err := s.repository.GetDocument(ctx, command.AccountID, command.DocumentID)
	if err != nil {
		return DocumentDetail{}, err
	}
	revision, err := s.repository.GetLatestDocumentRevision(ctx, command.AccountID, command.DocumentID)
	if err != nil {
		return DocumentDetail{}, err
	}
	if revision.ID != command.RevisionID || revision.ContentSHA256 != command.ContentSHA256 ||
		(revision.State != knowledgedomain.RevisionReady && revision.State != knowledgedomain.RevisionFailed && revision.State != knowledgedomain.RevisionDeleted) {
		return DocumentDetail{}, ErrConstraint
	}
	if document.State == knowledgedomain.DocumentDeletionPending || document.State == knowledgedomain.DocumentDeleted {
		return DocumentDetail{Document: document, LatestRevision: revision}, nil
	}
	now := s.clock.Now().UTC()
	document, err = s.repository.RequestDocumentDeletion(ctx, command.AccountID, command.DocumentID, document.Version,
		Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "source_deletion_requested", At: now})
	if err != nil {
		return DocumentDetail{}, err
	}
	return DocumentDetail{Document: document, LatestRevision: revision}, nil
}

func (s *DocumentService) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (knowledgedomain.Document, error) {
	resolvedActor, ok := domainActor(actor)
	if !ok || ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil {
		return knowledgedomain.Document{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge})
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	value, err := s.repository.GetDocument(ctx, accountID, documentID)
	if err != nil {
		return knowledgedomain.Document{}, err
	}
	if resolvedActor.Kind == knowledgedomain.ActorUser && !canReadSensitivity(accountContext.Role, value.Sensitivity) {
		return knowledgedomain.Document{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	return value, nil
}

func (s *DocumentService) GetDetail(ctx context.Context, actor access.Actor, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (DocumentDetail, error) {
	resolvedActor, ok := domainActor(actor)
	if !ok || ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil {
		return DocumentDetail{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge})
	if err != nil {
		return DocumentDetail{}, err
	}
	document, err := s.repository.GetDocument(ctx, accountID, documentID)
	if err != nil {
		return DocumentDetail{}, err
	}
	if resolvedActor.Kind == knowledgedomain.ActorUser && !canReadSensitivity(accountContext.Role, document.Sensitivity) {
		return DocumentDetail{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	revision, err := s.repository.GetLatestDocumentRevision(ctx, accountID, documentID)
	if err != nil {
		return DocumentDetail{}, err
	}
	return DocumentDetail{Document: document, LatestRevision: revision}, nil
}

func (s *DocumentService) List(ctx context.Context, actor access.Actor, accountID ids.AccountID, query DocumentListQuery) (DocumentPage, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || !validDocumentListQuery(query) {
		return DocumentPage{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, actor, accountID, access.Requirement{Package: catalog.PackageKnowledge})
	if err != nil {
		return DocumentPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = DefaultLimit
	}
	if query.Limit < 1 || query.Limit > MaximumLimit {
		return DocumentPage{}, ErrInvalid
	}
	query.IncludeRestricted = accountContext.Role == accounts.RoleOwner || accountContext.Role == accounts.RoleAdministrator
	page, err := s.repository.ListDocuments(ctx, accountID, query)
	if err != nil {
		return DocumentPage{}, err
	}
	visible := page.Items[:0]
	for _, item := range page.Items {
		if canReadSensitivity(accountContext.Role, item.Sensitivity) {
			visible = append(visible, item)
		}
	}
	page.Items = visible
	return page, nil
}

func validDocumentListQuery(query DocumentListQuery) bool {
	if len(query.TitlePrefix) > knowledgedomain.MaximumDocumentTitle || strings.ContainsRune(query.TitlePrefix, '\x00') || query.Limit < 0 || query.Limit > MaximumLimit {
		return false
	}
	if (query.AfterUpdatedAt == nil) != (query.AfterID == "") || (query.AfterUpdatedAt != nil && (query.AfterUpdatedAt.IsZero() || ids.Validate(string(query.AfterID)) != nil)) {
		return false
	}
	switch query.State {
	case "", knowledgedomain.DocumentProcessing, knowledgedomain.DocumentReady, knowledgedomain.DocumentFailed, knowledgedomain.DocumentDeletionPending:
		return true
	default:
		return false
	}
}

func (s *DocumentService) authorizeMutation(ctx context.Context, accessActor access.Actor, accountID ids.AccountID, correlationID string) (knowledgedomain.Actor, access.AccountContext, error) {
	actor, ok := domainActor(accessActor)
	if !ok || ids.Validate(string(accountID)) != nil || ids.Validate(correlationID) != nil {
		return knowledgedomain.Actor{}, access.AccountContext{}, ErrInvalid
	}
	accountContext, err := s.authorizer.Authorize(ctx, accessActor, accountID, access.Requirement{Package: catalog.PackageKnowledge, Mutation: true})
	return actor, accountContext, err
}

func classifyDocumentDomain(err error) error {
	if errors.Is(err, knowledgedomain.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, knowledgedomain.ErrState) || errors.Is(err, knowledgedomain.ErrDocumentHold) || errors.Is(err, knowledgedomain.ErrDocumentRetention) {
		return ErrConstraint
	}
	return ErrInvalid
}
