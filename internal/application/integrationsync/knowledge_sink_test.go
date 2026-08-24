package integrationsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"testing"
	"time"

	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestKnowledgeCaptureSinkAdmitsDeterministicInitialDocument(t *testing.T) {
	now := time.Date(2026, 8, 23, 21, 0, 0, 0, time.UTC)
	input := knowledgeCaptureInput(now)
	documents := &fakeKnowledgeCaptureDocuments{err: knowledgeapp.ErrNotFound}
	admission := &fakeKnowledgeCaptureAdmission{}
	sink, err := NewKnowledgeCaptureSink(documents, admission)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := sink.Capture(context.Background(), input)
	if err != nil || !receipt.Valid(input.Claim) || receipt.Operation != CaptureAdmitted || receipt.ContentSHA256 != sha256.Sum256(input.Content) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if admission.upload == nil || admission.upload.Actor.WorkloadID != knowledgeapp.IntegrationSourceSyncWorkloadID ||
		admission.upload.Sensitivity != knowledgedomain.SensitivityInternal || admission.upload.DocumentID != receipt.DocumentID ||
		admission.upload.RevisionID != receipt.DocumentRevisionID || string(admission.body) != string(input.Content) {
		t.Fatalf("upload=%+v body=%q", admission.upload, admission.body)
	}
	first := receipt
	otherGrant := input
	otherGrant.Claim.GrantID = "da000000-0000-4000-8000-00000000000a"
	otherDocumentID, otherRevisionID, otherCaptureID, deriveErr := deriveKnowledgeCaptureIdentities(otherGrant)
	if deriveErr != nil || otherDocumentID != first.DocumentID || otherRevisionID != first.DocumentRevisionID || otherCaptureID == first.ID {
		t.Fatalf("cross-grant identities document=%s revision=%s capture=%s err=%v", otherDocumentID, otherRevisionID, otherCaptureID, deriveErr)
	}
	admission.upload, admission.body = nil, nil
	replayed, err := sink.Capture(context.Background(), input)
	if err != nil || replayed != first || admission.upload == nil {
		t.Fatalf("replayed=%+v first=%+v upload=%+v err=%v", replayed, first, admission.upload, err)
	}
}

func TestKnowledgeCaptureSinkAdmitsRevisionAndFencesDeletingDocument(t *testing.T) {
	now := time.Date(2026, 8, 23, 21, 0, 0, 0, time.UTC)
	input := knowledgeCaptureInput(now)
	documentID, _, _, err := deriveKnowledgeCaptureIdentities(input)
	if err != nil {
		t.Fatal(err)
	}
	documents := &fakeKnowledgeCaptureDocuments{detail: knowledgeapp.DocumentDetail{Document: knowledgedomain.Document{
		ID: documentID, AccountID: input.Claim.AccountID, State: knowledgedomain.DocumentReady, CurrentRevision: 1,
		CurrentRevisionID: "a1000000-0000-4000-8000-000000000001", Version: 2}, LatestRevision: knowledgedomain.DocumentRevision{
		ID: "a1000000-0000-4000-8000-000000000001", DocumentID: documentID, AccountID: input.Claim.AccountID, Number: 1,
		State: knowledgedomain.RevisionReady, ContentSHA256: sha256.Sum256([]byte("prior"))}}}
	admission := &fakeKnowledgeCaptureAdmission{}
	sink, _ := NewKnowledgeCaptureSink(documents, admission)
	receipt, err := sink.Capture(context.Background(), input)
	if err != nil || admission.revision == nil || admission.revision.RevisionID != receipt.DocumentRevisionID ||
		admission.revision.DocumentID != documentID || receipt.ContentSHA256 != sha256.Sum256(input.Content) {
		t.Fatalf("receipt=%+v revision=%+v err=%v", receipt, admission.revision, err)
	}
	documents.detail.Document.State = knowledgedomain.DocumentDeletionPending
	if _, err := sink.Capture(context.Background(), input); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("capture into deleting document err=%v", err)
	}
	documents.detail.Document.State = knowledgedomain.DocumentDeleted
	documents.detail.LatestRevision.State = knowledgedomain.RevisionDeleted
	admission.revision = nil
	if receipt, err := sink.Capture(context.Background(), input); err != nil || admission.revision == nil ||
		admission.revision.RevisionID != receipt.DocumentRevisionID {
		t.Fatalf("capture into deleted document receipt=%+v revision=%+v err=%v", receipt, admission.revision, err)
	}
}

func TestKnowledgeCaptureSinkRequestsExactSourceDeletion(t *testing.T) {
	now := time.Date(2026, 8, 23, 21, 0, 0, 0, time.UTC)
	input := knowledgeCaptureInput(now)
	input.Deleted, input.Title, input.Filename, input.MediaType, input.Content = true, "", "", "", nil
	documentID, _, _, err := deriveKnowledgeCaptureIdentities(input)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("prior content"))
	detail := knowledgeapp.DocumentDetail{Document: knowledgedomain.Document{ID: documentID, AccountID: input.Claim.AccountID,
		State: knowledgedomain.DocumentReady, CurrentRevisionID: "b1000000-0000-4000-8000-000000000001", CurrentRevision: 2, Version: 3},
		LatestRevision: knowledgedomain.DocumentRevision{ID: "b1000000-0000-4000-8000-000000000001", DocumentID: documentID,
			AccountID: input.Claim.AccountID, Number: 2, State: knowledgedomain.RevisionReady, ContentSHA256: digest}}
	documents := &fakeKnowledgeCaptureDocuments{detail: detail, deleteResult: detail}
	sink, _ := NewKnowledgeCaptureSink(documents, &fakeKnowledgeCaptureAdmission{})
	receipt, err := sink.Capture(context.Background(), input)
	if err != nil || receipt.Operation != CaptureDeleted || receipt.DocumentID != documentID ||
		receipt.DocumentRevisionID != detail.LatestRevision.ID || receipt.ContentSHA256 != digest || documents.deleted == nil ||
		documents.deleted.Actor.WorkloadID != knowledgeapp.IntegrationSourceSyncWorkloadID || documents.deleted.ContentSHA256 != digest {
		t.Fatalf("receipt=%+v delete=%+v err=%v", receipt, documents.deleted, err)
	}
	documents.err = knowledgeapp.ErrNotFound
	if _, err := sink.Capture(context.Background(), input); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown provider deletion err=%v", err)
	}
}

func knowledgeCaptureInput(now time.Time) CaptureInput {
	return CaptureInput{Claim: syncClaim(now), FolderID: "folder-a", ProviderObjectSHA256: sha256.Sum256([]byte("provider-object")),
		ProviderRevisionSHA256: sha256.Sum256([]byte("provider-revision")), Title: "Formation record", Filename: "formation.txt",
		MediaType: "text/plain", Content: []byte("governed content"), CapturedAt: now.Add(10 * time.Second)}
}

type fakeKnowledgeCaptureDocuments struct {
	detail       knowledgeapp.DocumentDetail
	err          error
	deleted      *knowledgeapp.DeleteSourceDocumentCommand
	deleteResult knowledgeapp.DocumentDetail
}

func (value *fakeKnowledgeCaptureDocuments) GetDetail(context.Context, access.Actor, ids.AccountID, ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error) {
	return value.detail, value.err
}
func (value *fakeKnowledgeCaptureDocuments) DeleteSource(_ context.Context, command knowledgeapp.DeleteSourceDocumentCommand) (knowledgeapp.DocumentDetail, error) {
	value.deleted = &command
	return value.deleteResult, nil
}

type fakeKnowledgeCaptureAdmission struct {
	upload   *knowledgeapp.UploadDocumentCommand
	revision *knowledgeapp.UploadDocumentRevisionCommand
	body     []byte
}

func (value *fakeKnowledgeCaptureAdmission) Upload(_ context.Context, command knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	value.upload = &command
	value.body, _ = io.ReadAll(command.Body)
	digest := sha256.Sum256(value.body)
	return knowledgedomain.Document{ID: command.DocumentID, AccountID: command.AccountID}, knowledgedomain.DocumentRevision{
		ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID, ContentSHA256: digest}, nil
}
func (value *fakeKnowledgeCaptureAdmission) UploadRevision(_ context.Context, command knowledgeapp.UploadDocumentRevisionCommand) (knowledgedomain.DocumentRevision, error) {
	value.revision = &command
	value.body, _ = io.ReadAll(command.Body)
	return knowledgedomain.DocumentRevision{ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID,
		ContentSHA256: sha256.Sum256(value.body)}, nil
}
