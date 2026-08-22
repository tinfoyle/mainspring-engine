package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type DocumentAdmissionService struct {
	documents *DocumentService
	objects   SourceObjectStore
}

func NewDocumentAdmissionService(documents *DocumentService, objects SourceObjectStore) (*DocumentAdmissionService, error) {
	if documents == nil || objects == nil {
		return nil, fmt.Errorf("Knowledge document admission dependencies are required")
	}
	return &DocumentAdmissionService{documents: documents, objects: objects}, nil
}

type UploadDocumentCommand struct {
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
	Body          io.Reader
	ChangeSummary string
	CorrelationID string
}

func (service *DocumentAdmissionService) Upload(ctx context.Context, command UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	key, keyErr := knowledgedomain.SourceObjectKey(command.AccountID, command.DocumentID, command.RevisionID)
	if keyErr != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	preflight := AdmitDocumentCommand{Actor: command.Actor, AccountID: command.AccountID, DocumentID: command.DocumentID, RevisionID: command.RevisionID, Title: command.Title, Sensitivity: command.Sensitivity, RetainUntil: command.RetainUntil, Filename: command.Filename, DeclaredType: command.DeclaredType, VerifiedType: command.VerifiedType, ByteSize: command.ByteSize, ContentSHA256: command.ContentSHA256, ObjectKey: key, ObjectVersion: "preflight", ChangeSummary: command.ChangeSummary, CorrelationID: command.CorrelationID}
	if _, _, _, err := service.documents.buildAdmission(ctx, preflight); err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	write, err := service.objects.PutImmutable(ctx, SourceObjectWrite{AccountID: command.AccountID, DocumentID: command.DocumentID, RevisionID: command.RevisionID, MediaType: command.VerifiedType, Size: command.ByteSize, ContentSHA256: command.ContentSHA256, Body: command.Body})
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	preflight.ByteSize, preflight.ContentSHA256, preflight.ObjectKey, preflight.ObjectVersion = write.Identity.Size, write.Identity.ContentSHA256, write.Identity.Key, write.Identity.Version
	document, revision, err := service.documents.Admit(ctx, preflight)
	if err == nil || !write.Created {
		return document, revision, err
	}
	if cleanupErr := service.objects.Delete(ctx, write.Identity); cleanupErr != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, fmt.Errorf("%w: source-object cleanup failed: %v", err, cleanupErr)
	}
	return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
}
