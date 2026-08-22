package knowledge

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
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
	Body          DocumentSource
	ChangeSummary string
	CorrelationID string
}

func (service *DocumentAdmissionService) Upload(ctx context.Context, command UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	actor, accountContext, err := service.documents.authorizeMutation(ctx, command.Actor, command.AccountID, command.CorrelationID)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	if actor.Kind == knowledgedomain.ActorUser && !canContribute(accountContext.Role) {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, &access.DeniedError{Code: access.DenialRole, Package: catalog.PackageKnowledge}
	}
	verified, err := VerifyDocumentSource(command.Filename, command.DeclaredType, command.Body)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	key, keyErr := knowledgedomain.SourceObjectKey(command.AccountID, command.DocumentID, command.RevisionID)
	if keyErr != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	preflight := AdmitDocumentCommand{Actor: command.Actor, AccountID: command.AccountID, DocumentID: command.DocumentID, RevisionID: command.RevisionID, Title: command.Title, Sensitivity: command.Sensitivity, RetainUntil: command.RetainUntil, Filename: command.Filename, DeclaredType: command.DeclaredType, VerifiedType: verified.MediaType, ByteSize: verified.Size, ContentSHA256: verified.ContentSHA256, ObjectKey: key, ObjectVersion: "preflight", ChangeSummary: command.ChangeSummary, CorrelationID: command.CorrelationID}
	if _, _, _, err := service.documents.buildAdmission(ctx, preflight); err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	if _, err := command.Body.Seek(0, io.SeekStart); err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, ErrInvalid
	}
	write, err := service.objects.PutImmutable(ctx, SourceObjectWrite{AccountID: command.AccountID, DocumentID: command.DocumentID, RevisionID: command.RevisionID, MediaType: verified.MediaType, Size: verified.Size, ContentSHA256: verified.ContentSHA256, Body: command.Body})
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
