package knowledge

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const appKnowledgeDocument ids.KnowledgeDocumentID = "60000000-0000-4000-8000-000000000006"
const appKnowledgeRevision ids.KnowledgeDocumentRevisionID = "70000000-0000-4000-8000-000000000007"

type documentRepository struct {
	document knowledgedomain.Document
	revision knowledgedomain.DocumentRevision
	chunks   []knowledgedomain.DocumentChunk
	mutation Mutation
}

func (repository *documentRepository) AdmitDocument(_ context.Context, document knowledgedomain.Document, revision knowledgedomain.DocumentRevision, mutation Mutation) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	repository.document, repository.revision, repository.mutation = document, revision, mutation
	return document, revision, nil
}
func (repository *documentRepository) GetDocument(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.Document, error) {
	return repository.document, nil
}
func (repository *documentRepository) GetDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentRevisionID) (knowledgedomain.DocumentRevision, error) {
	return repository.revision, nil
}
func (repository *documentRepository) SaveDocumentRevision(_ context.Context, value knowledgedomain.DocumentRevision, _ time.Time, _ string, mutation Mutation) (knowledgedomain.DocumentRevision, error) {
	repository.revision, repository.mutation = value, mutation
	return value, nil
}
func (repository *documentRepository) IndexDocumentRevision(_ context.Context, value knowledgedomain.DocumentRevision, _ time.Time, chunks []knowledgedomain.DocumentChunk, mutation Mutation) (knowledgedomain.DocumentRevision, error) {
	repository.revision, repository.chunks, repository.mutation = value, chunks, mutation
	return value, nil
}
func (repository *documentRepository) PublishDocumentRevision(_ context.Context, _ ids.AccountID, _ ids.KnowledgeDocumentID, _ ids.KnowledgeDocumentRevisionID, expected uint64, mutation Mutation) (knowledgedomain.Document, error) {
	result, err := repository.document.Publish(repository.revision, expected, mutation.At)
	if err == nil {
		repository.document, repository.mutation = result, mutation
	}
	return result, err
}
func (repository *documentRepository) RequestDocumentDeletion(_ context.Context, _ ids.AccountID, _ ids.KnowledgeDocumentID, expected uint64, mutation Mutation) (knowledgedomain.Document, error) {
	result, err := repository.document.RequestDeletion(expected, mutation.At)
	if err == nil {
		repository.document, repository.mutation = result, mutation
	}
	return result, err
}

func documentServiceFixture(t *testing.T) (*DocumentService, *knowledgeAuthorizer, *documentRepository, *knowledgeClock) {
	t.Helper()
	authorizer := &knowledgeAuthorizer{account: access.AccountContext{Role: accounts.RoleOwner}}
	repository := &documentRepository{}
	clock := &knowledgeClock{now: time.Date(2026, 8, 21, 23, 0, 0, 0, time.UTC)}
	service, err := NewDocumentService(authorizer, repository, clock)
	if err != nil {
		t.Fatal(err)
	}
	return service, authorizer, repository, clock
}

func admitDocumentFixture(t *testing.T, service *DocumentService) (knowledgedomain.Document, knowledgedomain.DocumentRevision) {
	t.Helper()
	value, revision, err := service.Admit(context.Background(), AdmitDocumentCommand{
		Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument, RevisionID: appKnowledgeRevision,
		Title: "Operating plan", Sensitivity: knowledgedomain.SensitivityConfidential, Filename: "operating-plan.md", DeclaredType: "text/markdown", VerifiedType: "text/markdown",
		ByteSize: 128, ContentSHA256: sha256.Sum256([]byte("source")), ObjectKey: "accounts/" + string(appKnowledgeAccount) + "/documents/" + string(appKnowledgeDocument) + "/revisions/" + string(appKnowledgeRevision) + "/source", ObjectVersion: "version-1", ChangeSummary: "Initial upload", CorrelationID: appKnowledgeOperation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return value, revision
}

func TestDocumentAdmissionBindsAccountObjectIdentityAndPackageMutation(t *testing.T) {
	service, authorizer, repository, _ := documentServiceFixture(t)
	document, revision := admitDocumentFixture(t, service)
	if document.State != knowledgedomain.DocumentProcessing || revision.State != knowledgedomain.RevisionQuarantined || revision.ObjectKey != "accounts/"+string(appKnowledgeAccount)+"/documents/"+string(appKnowledgeDocument)+"/revisions/"+string(appKnowledgeRevision)+"/source" || !authorizer.requirement.Mutation || authorizer.requirement.Package != "knowledge" || repository.mutation.ReasonCode != "document_admitted" {
		t.Fatalf("document=%+v revision=%+v requirement=%+v mutation=%+v", document, revision, authorizer.requirement, repository.mutation)
	}
}

func TestDocumentProcessingRequiresWorkloadAndIndexesStableChunks(t *testing.T) {
	service, _, repository, clock := documentServiceFixture(t)
	_, revision := admitDocumentFixture(t, service)
	if _, err := service.RecordScan(context.Background(), RecordScanCommand{Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, RevisionID: revision.ID, State: knowledgedomain.ScanClean, Engine: "clamav", CorrelationID: "81000000-0000-4000-8000-000000000008"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("user scanner err=%v", err)
	}
	clock.now = clock.now.Add(time.Second)
	revision, err := service.RecordScan(context.Background(), RecordScanCommand{Actor: access.Actor{WorkloadID: "worker:document-admission"}, AccountID: appKnowledgeAccount, RevisionID: revision.ID, State: knowledgedomain.ScanClean, Engine: "clamav/1.4.3", Signature: "daily.cvd", CorrelationID: "81000000-0000-4000-8000-000000000008"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	revision, err = service.RecordExtraction(context.Background(), RecordExtractionCommand{Actor: access.Actor{WorkloadID: "worker:document-admission"}, AccountID: appKnowledgeAccount, RevisionID: revision.ID, TextSHA256: sha256.Sum256([]byte("first second")), TextBytes: 12, Extractor: "spyglass/text-v1", CorrelationID: "82000000-0000-4000-8000-000000000008"})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	revision, err = service.Index(context.Background(), IndexDocumentCommand{Actor: access.Actor{WorkloadID: "worker:document-admission"}, AccountID: appKnowledgeAccount, RevisionID: revision.ID, Generation: "knowledge-v1", Chunks: []DocumentChunkDraft{{StartByte: 0, EndByte: 5, Content: "first", TokenCount: 1}, {StartByte: 6, EndByte: 12, Content: "second", TokenCount: 1}}, CorrelationID: "83000000-0000-4000-8000-000000000008"})
	if err != nil || revision.State != knowledgedomain.RevisionReady || len(repository.chunks) != 2 || repository.chunks[0].Index != 0 || repository.chunks[1].Index != 1 || repository.chunks[0].ID == repository.chunks[1].ID {
		t.Fatalf("revision=%+v chunks=%+v err=%v", revision, repository.chunks, err)
	}
}

func TestRestrictedDocumentReadRequiresManagingRole(t *testing.T) {
	service, authorizer, repository, clock := documentServiceFixture(t)
	repository.document, _ = knowledgedomain.NewDocument(appKnowledgeDocument, appKnowledgeAccount, "Recovery procedure", knowledgedomain.SensitivityRestricted, nil, knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(appKnowledgeUser)}, clock.now)
	authorizer.account.Role = accounts.RoleMember
	if _, err := service.Get(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, appKnowledgeDocument); !access.IsDenied(err, access.DenialRole) {
		t.Fatalf("restricted document read err=%v", err)
	}
}
