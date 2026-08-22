package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type DocumentRepository interface {
	AdmitDocument(context.Context, knowledgedomain.Document, knowledgedomain.DocumentRevision, Mutation) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error)
	GetDocument(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.Document, error)
	GetDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentRevisionID) (knowledgedomain.DocumentRevision, error)
	SaveDocumentRevision(context.Context, knowledgedomain.DocumentRevision, time.Time, string, Mutation) (knowledgedomain.DocumentRevision, error)
	IndexDocumentRevision(context.Context, knowledgedomain.DocumentRevision, time.Time, []knowledgedomain.DocumentChunk, Mutation) (knowledgedomain.DocumentRevision, error)
	PublishDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, uint64, Mutation) (knowledgedomain.Document, error)
	RequestDocumentDeletion(context.Context, ids.AccountID, ids.KnowledgeDocumentID, uint64, Mutation) (knowledgedomain.Document, error)
}

type DocumentService struct {
	authorizer Authorizer
	repository DocumentRepository
	clock      Clock
}

func NewDocumentService(authorizer Authorizer, repository DocumentRepository, clock Clock) (*DocumentService, error) {
	if authorizer == nil || repository == nil || clock == nil {
		return nil, errors.New("Knowledge document dependencies are required")
	}
	return &DocumentService{authorizer: authorizer, repository: repository, clock: clock}, nil
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
	actor, accountContext, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	if ids.Validate(string(command.DocumentID)) != nil || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	now := s.clock.Now().UTC()
	document, err := knowledgedomain.NewDocument(command.DocumentID, command.AccountID, command.Title, command.Sensitivity, command.RetainUntil, actor, now)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	revision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID, Number: 1, Filename: command.Filename, DeclaredType: command.DeclaredType, VerifiedType: command.VerifiedType, ByteSize: command.ByteSize, ContentSHA256: command.ContentSHA256, ObjectKey: command.ObjectKey, ObjectVersion: command.ObjectVersion, ChangeSummary: command.ChangeSummary, CreatedBy: actor}, now)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	return s.repository.AdmitDocument(ctx, document, revision, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "document_admitted", At: now})
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
	current, err := s.repository.GetDocumentRevision(ctx, command.AccountID, command.RevisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	now := s.clock.Now().UTC()
	result, err := current.RecordScan(command.State, command.Engine, command.Signature, now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	event := "scan_completed"
	if result.State == knowledgedomain.RevisionFailed {
		event = "processing_failed"
	}
	return s.repository.SaveDocumentRevision(ctx, result, current.UpdatedAt, event, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: event, At: now})
}

type RecordExtractionCommand struct {
	Actor                    access.Actor
	AccountID                ids.AccountID
	RevisionID               ids.KnowledgeDocumentRevisionID
	TextSHA256               [sha256.Size]byte
	TextBytes                int64
	Extractor, CorrelationID string
}

func (s *DocumentService) RecordExtraction(ctx context.Context, command RecordExtractionCommand) (knowledgedomain.DocumentRevision, error) {
	actor, _, err := s.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	if actor.Kind != knowledgedomain.ActorWorkload || ids.Validate(string(command.RevisionID)) != nil {
		return knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	current, err := s.repository.GetDocumentRevision(ctx, command.AccountID, command.RevisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	now := s.clock.Now().UTC()
	result, err := current.RecordExtraction(command.TextSHA256, command.TextBytes, command.Extractor, now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	return s.repository.SaveDocumentRevision(ctx, result, current.UpdatedAt, "extraction_completed", Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "extraction_completed", At: now})
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
	current, err := s.repository.GetDocumentRevision(ctx, command.AccountID, command.RevisionID)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	now := s.clock.Now().UTC()
	result, err := current.RecordIndex(command.Generation, uint32(len(command.Chunks)), now)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, classifyDocumentDomain(err)
	}
	chunks := make([]knowledgedomain.DocumentChunk, len(command.Chunks))
	for index, draft := range command.Chunks {
		chunkID, deriveErr := ids.Derive(command.CorrelationID, fmt.Sprintf("document-chunk-%d", index))
		if deriveErr != nil {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		chunk, chunkErr := knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: ids.KnowledgeDocumentChunkID(chunkID), AccountID: command.AccountID, RevisionID: command.RevisionID, Index: uint32(index), StartByte: draft.StartByte, EndByte: draft.EndByte, Content: draft.Content, ContentSHA256: sha256.Sum256([]byte(draft.Content)), TokenCount: draft.TokenCount, IndexGeneration: result.IndexGeneration, CreatedAt: now})
		if chunkErr != nil {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		if chunk.EndByte > current.TextBytes {
			return knowledgedomain.DocumentRevision{}, ErrInvalid
		}
		chunks[index] = chunk
	}
	return s.repository.IndexDocumentRevision(ctx, result, current.UpdatedAt, chunks, Mutation{Actor: actor, CorrelationID: command.CorrelationID, ReasonCode: "index_completed", At: now})
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

func (s *DocumentService) Get(ctx context.Context, actor access.Actor, accountID ids.AccountID, documentID ids.KnowledgeDocumentID) (knowledgedomain.Document, error) {
	if _, ok := domainActor(actor); !ok || ids.Validate(string(accountID)) != nil || ids.Validate(string(documentID)) != nil {
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
	if !canReadSensitivity(accountContext.Role, value.Sensitivity) {
		return knowledgedomain.Document{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	return value, nil
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
