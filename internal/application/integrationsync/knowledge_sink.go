package integrationsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type KnowledgeCaptureDocuments interface {
	GetDetail(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error)
	DeleteSource(context.Context, knowledgeapp.DeleteSourceDocumentCommand) (knowledgeapp.DocumentDetail, error)
}

type KnowledgeCaptureAdmission interface {
	Upload(context.Context, knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error)
	UploadRevision(context.Context, knowledgeapp.UploadDocumentRevisionCommand) (knowledgedomain.DocumentRevision, error)
}

type KnowledgeCaptureSink struct {
	documents KnowledgeCaptureDocuments
	admission KnowledgeCaptureAdmission
}

func NewKnowledgeCaptureSink(documents KnowledgeCaptureDocuments, admission KnowledgeCaptureAdmission) (*KnowledgeCaptureSink, error) {
	if documents == nil || admission == nil {
		return nil, ErrInvalid
	}
	return &KnowledgeCaptureSink{documents: documents, admission: admission}, nil
}

func (sink *KnowledgeCaptureSink) Capture(ctx context.Context, input CaptureInput) (CaptureReceipt, error) {
	if !validKnowledgeCaptureInput(input) {
		return CaptureReceipt{}, ErrInvalid
	}
	documentID, revisionID, captureID, err := deriveKnowledgeCaptureIdentities(input)
	if err != nil {
		return CaptureReceipt{}, ErrInvalid
	}
	actor := access.Actor{WorkloadID: knowledgeapp.IntegrationSourceSyncWorkloadID}
	detail, detailErr := sink.documents.GetDetail(ctx, actor, input.Claim.AccountID, documentID)
	if input.Deleted {
		if detailErr != nil {
			return CaptureReceipt{}, errors.Join(ErrUnavailable, detailErr)
		}
		detail, err = sink.documents.DeleteSource(ctx, knowledgeapp.DeleteSourceDocumentCommand{Actor: actor,
			AccountID: input.Claim.AccountID, DocumentID: documentID, RevisionID: detail.LatestRevision.ID,
			ContentSHA256: detail.LatestRevision.ContentSHA256, CorrelationID: string(captureID)})
		if err != nil {
			return CaptureReceipt{}, errors.Join(ErrUnavailable, err)
		}
		return CaptureReceipt{ID: captureID, FolderID: input.FolderID, ProviderObjectSHA256: input.ProviderObjectSHA256,
			ProviderRevisionSHA256: input.ProviderRevisionSHA256, Operation: CaptureDeleted, DocumentID: documentID,
			DocumentRevisionID: detail.LatestRevision.ID, ContentSHA256: detail.LatestRevision.ContentSHA256}, nil
	}
	if detailErr != nil && !errors.Is(detailErr, knowledgeapp.ErrNotFound) {
		return CaptureReceipt{}, errors.Join(ErrUnavailable, detailErr)
	}
	contentDigest := sha256.Sum256(input.Content)
	if errors.Is(detailErr, knowledgeapp.ErrNotFound) {
		initialSummary, _ := sourceChangeSummaries(input.Claim.SourceKind)
		document, revision, uploadErr := sink.admission.Upload(ctx, knowledgeapp.UploadDocumentCommand{Actor: actor,
			AccountID: input.Claim.AccountID, DocumentID: documentID, RevisionID: revisionID, Title: input.Title,
			Sensitivity: knowledgedomain.SensitivityInternal, Filename: input.Filename, DeclaredType: input.MediaType,
			Body: bytes.NewReader(input.Content), ChangeSummary: initialSummary, CorrelationID: string(captureID)})
		if uploadErr != nil {
			return CaptureReceipt{}, errors.Join(ErrUnavailable, uploadErr)
		}
		if document.ID != documentID || revision.ID != revisionID || revision.ContentSHA256 != contentDigest {
			return CaptureReceipt{}, ErrInvalid
		}
		return admittedCaptureReceipt(input, captureID, documentID, revisionID, contentDigest), nil
	}
	if detail.Document.State == knowledgedomain.DocumentDeletionPending {
		return CaptureReceipt{}, ErrUnavailable
	}
	if detail.LatestRevision.ID == revisionID && detail.LatestRevision.Number == 1 && detail.Document.CurrentRevision == 0 {
		initialSummary, _ := sourceChangeSummaries(input.Claim.SourceKind)
		document, revision, uploadErr := sink.admission.Upload(ctx, knowledgeapp.UploadDocumentCommand{Actor: actor,
			AccountID: input.Claim.AccountID, DocumentID: documentID, RevisionID: revisionID, Title: input.Title,
			Sensitivity: knowledgedomain.SensitivityInternal, Filename: input.Filename, DeclaredType: input.MediaType,
			Body: bytes.NewReader(input.Content), ChangeSummary: initialSummary, CorrelationID: string(captureID)})
		if uploadErr != nil {
			return CaptureReceipt{}, errors.Join(ErrUnavailable, uploadErr)
		}
		if document.ID != documentID || revision.ID != revisionID || revision.ContentSHA256 != contentDigest {
			return CaptureReceipt{}, ErrInvalid
		}
		return admittedCaptureReceipt(input, captureID, documentID, revisionID, contentDigest), nil
	}
	_, revisionSummary := sourceChangeSummaries(input.Claim.SourceKind)
	revision, err := sink.admission.UploadRevision(ctx, knowledgeapp.UploadDocumentRevisionCommand{Actor: actor,
		AccountID: input.Claim.AccountID, DocumentID: documentID, RevisionID: revisionID, Filename: input.Filename,
		DeclaredType: input.MediaType, Body: bytes.NewReader(input.Content), ChangeSummary: revisionSummary,
		CorrelationID: string(captureID)})
	if err != nil {
		return CaptureReceipt{}, errors.Join(ErrUnavailable, err)
	}
	if revision.ID != revisionID || revision.DocumentID != documentID || revision.ContentSHA256 != contentDigest {
		return CaptureReceipt{}, ErrInvalid
	}
	return admittedCaptureReceipt(input, captureID, documentID, revisionID, contentDigest), nil
}

func validKnowledgeCaptureInput(input CaptureInput) bool {
	if input.CapturedAt.IsZero() || !input.Claim.Valid(input.CapturedAt.UTC()) || !contains(input.Claim.FolderIDs, input.FolderID) ||
		input.ProviderObjectSHA256 == ([sha256.Size]byte{}) || input.ProviderRevisionSHA256 == ([sha256.Size]byte{}) {
		return false
	}
	if input.Deleted {
		return input.Title == "" && input.Filename == "" && input.MediaType == "" && len(input.Content) == 0
	}
	return boundedText(input.Title, MaximumTitleBytes, true) && boundedText(input.Filename, MaximumFilenameBytes, true) &&
		!strings.ContainsAny(input.Filename, `/\`) && len(input.MediaType) <= MaximumMediaTypeBytes &&
		validMediaType.MatchString(input.MediaType) && len(input.Content) > 0 && len(input.Content) <= MaximumChangeBytes
}

func deriveKnowledgeCaptureIdentities(input CaptureInput) (ids.KnowledgeDocumentID, ids.KnowledgeDocumentRevisionID, ids.IntegrationSourceCaptureID, error) {
	objectDigest, revisionDigest := hex.EncodeToString(input.ProviderObjectSHA256[:]), hex.EncodeToString(input.ProviderRevisionSHA256[:])
	objectNamespace, revisionNamespace, captureNamespace := sourceIdentityNamespaces(input.Claim.SourceKind)
	documentRaw, err := ids.Derive(string(input.Claim.ConnectionID), objectNamespace+objectDigest)
	if err != nil {
		return "", "", "", err
	}
	revisionRaw, err := ids.Derive(documentRaw, revisionNamespace+revisionDigest)
	if err != nil {
		return "", "", "", err
	}
	operation := string(CaptureAdmitted)
	if input.Deleted {
		operation = string(CaptureDeleted)
	}
	captureRaw, err := ids.Derive(string(input.Claim.GrantID), captureNamespace+operation+":"+objectDigest+":"+revisionDigest)
	return ids.KnowledgeDocumentID(documentRaw), ids.KnowledgeDocumentRevisionID(revisionRaw), ids.IntegrationSourceCaptureID(captureRaw), err
}

func sourceIdentityNamespaces(kind integrationsdomain.ConnectorKind) (string, string, string) {
	if kind == integrationsdomain.ConnectorEmail {
		return "email-object:", "email-revision:", "email-capture:"
	}
	// Preserve the committed Drive namespace so existing captures converge on
	// their original immutable documents after this provider-neutral expansion.
	return "google-drive-object:", "google-drive-revision:", "google-drive-capture:"
}

func sourceChangeSummaries(kind integrationsdomain.ConnectorKind) (string, string) {
	if kind == integrationsdomain.ConnectorEmail {
		return "Initial email source capture", "Email source changed"
	}
	return "Initial Google Drive capture", "Google Drive source changed"
}

func admittedCaptureReceipt(input CaptureInput, captureID ids.IntegrationSourceCaptureID, documentID ids.KnowledgeDocumentID,
	revisionID ids.KnowledgeDocumentRevisionID, contentDigest [sha256.Size]byte) CaptureReceipt {
	return CaptureReceipt{ID: captureID, FolderID: input.FolderID, ProviderObjectSHA256: input.ProviderObjectSHA256,
		ProviderRevisionSHA256: input.ProviderRevisionSHA256, Operation: CaptureAdmitted, DocumentID: documentID,
		DocumentRevisionID: revisionID, ContentSHA256: contentDigest}
}

var _ CaptureSink = (*KnowledgeCaptureSink)(nil)
