package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
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
	document          knowledgedomain.Document
	revision          knowledgedomain.DocumentRevision
	chunks            []knowledgedomain.DocumentChunk
	mutation          Mutation
	admitErr          error
	retrievalQuery    DocumentRetrievalQuery
	retrievalItems    []DocumentCitation
	citation          DocumentCitation
	includeRestricted bool
	publishErr        error
}

func (repository *documentRepository) RetrieveDocumentChunks(_ context.Context, _ ids.AccountID, query DocumentRetrievalQuery) ([]DocumentCitation, error) {
	repository.retrievalQuery = query
	return repository.retrievalItems, nil
}

func (repository *documentRepository) GetDocumentCitation(_ context.Context, _ ids.AccountID, _ ids.KnowledgeDocumentID, _ ids.KnowledgeDocumentRevisionID, _ ids.KnowledgeDocumentChunkID, includeRestricted bool) (DocumentCitation, error) {
	repository.includeRestricted = includeRestricted
	return repository.citation, nil
}

func (repository *documentRepository) AdmitDocument(_ context.Context, document knowledgedomain.Document, revision knowledgedomain.DocumentRevision, mutation Mutation) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	repository.document, repository.revision, repository.mutation = document, revision, mutation
	return document, revision, repository.admitErr
}
func (repository *documentRepository) AdmitDocumentRevision(_ context.Context, _ knowledgedomain.Document, revision knowledgedomain.DocumentRevision, mutation Mutation) (knowledgedomain.DocumentRevision, error) {
	repository.revision, repository.mutation = revision, mutation
	return revision, repository.admitErr
}

type sourceObjectStore struct {
	puts    int
	deleted *SourceObjectIdentity
	created bool
}

func (store *sourceObjectStore) Verify(context.Context) error { return nil }
func (store *sourceObjectStore) PutImmutable(_ context.Context, value SourceObjectWrite) (SourceObjectWriteResult, error) {
	store.puts++
	key, err := value.Key()
	if err != nil {
		return SourceObjectWriteResult{}, err
	}
	if _, err := io.ReadAll(value.Body); err != nil {
		return SourceObjectWriteResult{}, err
	}
	return SourceObjectWriteResult{Identity: SourceObjectIdentity{Key: key, Version: "object-version-1", Size: value.Size, ContentSHA256: value.ContentSHA256}, Created: store.created}, nil
}
func (store *sourceObjectStore) Open(context.Context, SourceObjectIdentity) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}
func (store *sourceObjectStore) Delete(_ context.Context, value SourceObjectIdentity) error {
	store.deleted = &value
	return nil
}
func (repository *documentRepository) GetDocument(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.Document, error) {
	return repository.document, nil
}
func (repository *documentRepository) GetLatestDocumentRevision(context.Context, ids.AccountID, ids.KnowledgeDocumentID) (knowledgedomain.DocumentRevision, error) {
	return repository.revision, nil
}
func (repository *documentRepository) GetDocumentRevision(_ context.Context, _ ids.AccountID, revisionID ids.KnowledgeDocumentRevisionID) (knowledgedomain.DocumentRevision, error) {
	if repository.revision.ID != revisionID {
		return knowledgedomain.DocumentRevision{}, ErrNotFound
	}
	return repository.revision, nil
}
func (repository *documentRepository) ListDocuments(context.Context, ids.AccountID, DocumentListQuery) (DocumentPage, error) {
	return DocumentPage{Items: []DocumentSummary{{ID: repository.document.ID, Title: repository.document.Title, Sensitivity: repository.document.Sensitivity, State: repository.document.State, Version: repository.document.Version, CreatedAt: repository.document.CreatedAt, UpdatedAt: repository.document.UpdatedAt}}}, nil
}
func (repository *documentRepository) SaveDocumentRevision(_ context.Context, value knowledgedomain.DocumentRevision, _ time.Time, _ string, mutation Mutation) (knowledgedomain.DocumentRevision, error) {
	repository.revision, repository.mutation = value, mutation
	if value.State == knowledgedomain.RevisionFailed && value.Number == 1 && repository.document.State == knowledgedomain.DocumentProcessing {
		repository.document, _ = repository.document.FailInitial(repository.document.Version, mutation.At)
	}
	return value, nil
}
func (repository *documentRepository) IndexDocumentRevision(_ context.Context, value knowledgedomain.DocumentRevision, _ time.Time, chunks []knowledgedomain.DocumentChunk, mutation Mutation) (knowledgedomain.DocumentRevision, error) {
	repository.revision, repository.chunks, repository.mutation = value, chunks, mutation
	return value, nil
}
func (repository *documentRepository) PublishDocumentRevision(_ context.Context, _ ids.AccountID, _ ids.KnowledgeDocumentID, _ ids.KnowledgeDocumentRevisionID, expected uint64, mutation Mutation) (knowledgedomain.Document, error) {
	if repository.publishErr != nil {
		return knowledgedomain.Document{}, repository.publishErr
	}
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

func readyDocumentFixture(t *testing.T, service *DocumentService, repository *documentRepository, clock *knowledgeClock) (knowledgedomain.Document, knowledgedomain.DocumentRevision) {
	t.Helper()
	document, revision := admitDocumentFixture(t, service)
	clock.now = clock.now.Add(time.Second)
	revision, err := revision.RecordScan(knowledgedomain.ScanClean, "clamav/1.4.3", "daily.cvd", clock.now)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	revision, err = revision.RecordExtraction(sha256.Sum256([]byte("source")), 6, "spyglass/text-v1",
		"accounts/"+string(appKnowledgeAccount)+"/documents/"+string(appKnowledgeDocument)+"/revisions/"+string(appKnowledgeRevision)+"/extracted/text",
		"extracted-version-1", clock.now)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	revision, err = revision.RecordIndex("knowledge-v1", 1, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	document, err = document.Publish(revision, document.Version, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	repository.document, repository.revision = document, revision
	return document, revision
}

func TestDocumentAdmissionBindsAccountObjectIdentityAndPackageMutation(t *testing.T) {
	service, authorizer, repository, _ := documentServiceFixture(t)
	document, revision := admitDocumentFixture(t, service)
	if document.State != knowledgedomain.DocumentProcessing || revision.State != knowledgedomain.RevisionQuarantined || revision.ObjectKey != "accounts/"+string(appKnowledgeAccount)+"/documents/"+string(appKnowledgeDocument)+"/revisions/"+string(appKnowledgeRevision)+"/source" || !authorizer.requirement.Mutation || authorizer.requirement.Package != "knowledge" || repository.mutation.ReasonCode != "document_admitted" {
		t.Fatalf("document=%+v revision=%+v requirement=%+v mutation=%+v", document, revision, authorizer.requirement, repository.mutation)
	}
}

func TestDocumentRetrievalIsBoundedAuthorizedAndSensitivityScoped(t *testing.T) {
	service, authorizer, repository, _ := documentServiceFixture(t)
	repository.retrievalItems = []DocumentCitation{{AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument, RevisionID: appKnowledgeRevision, ChunkID: "71000000-0000-4000-8000-000000000007", Content: "operating plan"}}
	items, err := service.Retrieve(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, DocumentRetrievalQuery{Text: " operating plan "})
	if err != nil || len(items) != 1 || repository.retrievalQuery.Limit != MaximumDocumentRetrievalLimit || !repository.retrievalQuery.IncludeRestricted || authorizer.requirement.Mutation || authorizer.requirement.Package != "knowledge" {
		t.Fatalf("items=%+v query=%+v requirement=%+v err=%v", items, repository.retrievalQuery, authorizer.requirement, err)
	}
	authorizer.account.Role = accounts.RoleMember
	repository.citation = items[0]
	if _, err := service.GetCitation(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, appKnowledgeDocument, appKnowledgeRevision, items[0].ChunkID); err != nil || repository.includeRestricted {
		t.Fatalf("member citation includeRestricted=%v err=%v", repository.includeRestricted, err)
	}
	if _, err := service.Retrieve(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, DocumentRetrievalQuery{Text: ""}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty retrieval query err=%v", err)
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
	revision, err = service.RecordExtraction(context.Background(), RecordExtractionCommand{Actor: access.Actor{WorkloadID: "worker:document-admission"}, AccountID: appKnowledgeAccount, RevisionID: revision.ID, TextSHA256: sha256.Sum256([]byte("first second")), TextBytes: 12, Extractor: "spyglass/text-v1", ObjectKey: "accounts/" + string(appKnowledgeAccount) + "/documents/" + string(appKnowledgeDocument) + "/revisions/" + string(appKnowledgeRevision) + "/extracted/text", ObjectVersion: "extracted-version-1", CorrelationID: "82000000-0000-4000-8000-000000000008"})
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

func TestDocumentDetailAndListApplySensitivityAuthorization(t *testing.T) {
	service, authorizer, repository, _ := documentServiceFixture(t)
	document, revision := admitDocumentFixture(t, service)
	detail, err := service.GetDetail(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, document.ID)
	if err != nil || detail.Document.ID != document.ID || detail.LatestRevision.ID != revision.ID {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	page, err := service.List(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, DocumentListQuery{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("owner page=%+v err=%v", page, err)
	}
	authorizer.account.Role = accounts.RoleViewer
	repository.document.Sensitivity = knowledgedomain.SensitivityRestricted
	page, err = service.List(context.Background(), access.Actor{UserID: appKnowledgeUser}, appKnowledgeAccount, DocumentListQuery{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("viewer page=%+v err=%v", page, err)
	}
}

func TestUploadAuthorizesBeforeObjectWriteAndCleansNewOrphan(t *testing.T) {
	documents, authorizer, repository, _ := documentServiceFixture(t)
	objects := &sourceObjectStore{created: true}
	service, err := NewDocumentAdmissionService(documents, objects)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("source")
	command := UploadDocumentCommand{Actor: access.Actor{UserID: appKnowledgeUser}, AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument, RevisionID: appKnowledgeRevision, Title: "Operating plan", Sensitivity: knowledgedomain.SensitivityInternal, Filename: "plan.txt", DeclaredType: "text/plain", Body: bytes.NewReader(body), ChangeSummary: "Initial", CorrelationID: appKnowledgeOperation}
	authorizer.account.Role = accounts.RoleViewer
	if _, _, err := service.Upload(context.Background(), command); !access.IsDenied(err, access.DenialRole) || objects.puts != 0 {
		t.Fatalf("unauthorized upload puts=%d err=%v", objects.puts, err)
	}
	authorizer.account.Role = accounts.RoleOwner
	command.Body = bytes.NewReader(body)
	document, revision, err := service.Upload(context.Background(), command)
	if err != nil || objects.puts != 1 || document.ID != appKnowledgeDocument || revision.ObjectVersion != "object-version-1" {
		t.Fatalf("upload document=%+v revision=%+v puts=%d err=%v", document, revision, objects.puts, err)
	}
	repository.admitErr = ErrConflict
	command.DocumentID = "91000000-0000-4000-8000-000000000009"
	command.RevisionID = "92000000-0000-4000-8000-000000000009"
	command.CorrelationID = "93000000-0000-4000-8000-000000000009"
	command.Body = bytes.NewReader(body)
	if _, _, err := service.Upload(context.Background(), command); !errors.Is(err, ErrConflict) || objects.deleted == nil || objects.deleted.Version != "object-version-1" {
		t.Fatalf("orphan cleanup=%+v err=%v", objects.deleted, err)
	}
}

func TestRevisionAdmissionIsReplaySafeAndAllowsOnlyOnePendingRevision(t *testing.T) {
	service, _, repository, clock := documentServiceFixture(t)
	_, _ = readyDocumentFixture(t, service, repository, clock)
	command := AdmitDocumentRevisionCommand{
		Actor: access.Actor{WorkloadID: "integration-source-sync"}, AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument,
		RevisionID: "94000000-0000-4000-8000-000000000009", Filename: "plan.txt", DeclaredType: "text/plain", VerifiedType: "text/plain",
		ByteSize: 7, ContentSHA256: sha256.Sum256([]byte("updated")),
		ObjectKey:     "accounts/" + string(appKnowledgeAccount) + "/documents/" + string(appKnowledgeDocument) + "/revisions/94000000-0000-4000-8000-000000000009/source",
		ObjectVersion: "version-2", ChangeSummary: "Drive source changed", CorrelationID: "95000000-0000-4000-8000-000000000009",
	}
	revision, err := service.AdmitRevision(context.Background(), command)
	if err != nil || revision.Number != 2 || revision.State != knowledgedomain.RevisionQuarantined || repository.mutation.ReasonCode != "document_revision_admitted" {
		t.Fatalf("revision=%+v mutation=%+v err=%v", revision, repository.mutation, err)
	}
	replayed, err := service.AdmitRevision(context.Background(), command)
	if err != nil || replayed.ID != revision.ID || replayed.Number != revision.Number {
		t.Fatalf("replayed revision=%+v err=%v", replayed, err)
	}
	command.RevisionID = "96000000-0000-4000-8000-000000000009"
	command.ObjectKey = "accounts/" + string(appKnowledgeAccount) + "/documents/" + string(appKnowledgeDocument) + "/revisions/96000000-0000-4000-8000-000000000009/source"
	if _, err := service.AdmitRevision(context.Background(), command); !errors.Is(err, ErrConstraint) {
		t.Fatalf("second pending revision err=%v", err)
	}
	failed, err := revision.RecordScan(knowledgedomain.ScanInfected, "clamav", "Eicar-Signature", clock.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	repository.revision = failed
	clock.now = clock.now.Add(2 * time.Second)
	recovered, err := service.AdmitRevision(context.Background(), command)
	if err != nil || recovered.Number != 3 || recovered.ID != command.RevisionID {
		t.Fatalf("revision after failed pending version=%+v err=%v", recovered, err)
	}
}

func TestUploadRevisionCleansNewObjectWhenAdmissionLosesRace(t *testing.T) {
	documents, _, repository, clock := documentServiceFixture(t)
	_, _ = readyDocumentFixture(t, documents, repository, clock)
	objects := &sourceObjectStore{created: true}
	service, err := NewDocumentAdmissionService(documents, objects)
	if err != nil {
		t.Fatal(err)
	}
	repository.admitErr = ErrConflict
	command := UploadDocumentRevisionCommand{
		Actor: access.Actor{WorkloadID: "integration-source-sync"}, AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument,
		RevisionID: "97000000-0000-4000-8000-000000000009", Filename: "plan.txt", DeclaredType: "text/plain",
		Body: bytes.NewReader([]byte("updated")), ChangeSummary: "Drive source changed", CorrelationID: "98000000-0000-4000-8000-000000000009",
	}
	if _, err := service.UploadRevision(context.Background(), command); !errors.Is(err, ErrConflict) || objects.puts != 1 || objects.deleted == nil || objects.deleted.Version != "object-version-1" {
		t.Fatalf("puts=%d orphan cleanup=%+v err=%v", objects.puts, objects.deleted, err)
	}
}

func TestSourceDeletionRequiresWorkloadAndExactTerminalRevision(t *testing.T) {
	service, authorizer, repository, clock := documentServiceFixture(t)
	_, revision := readyDocumentFixture(t, service, repository, clock)
	repository.document.Sensitivity = knowledgedomain.SensitivityRestricted
	authorizer.account.Role = ""
	actor := access.Actor{WorkloadID: "integration-source-sync"}
	if detail, err := service.GetDetail(context.Background(), actor, appKnowledgeAccount, appKnowledgeDocument); err != nil || detail.LatestRevision.ID != revision.ID {
		t.Fatalf("workload detail=%+v err=%v", detail, err)
	}
	command := DeleteSourceDocumentCommand{Actor: actor, AccountID: appKnowledgeAccount, DocumentID: appKnowledgeDocument,
		RevisionID: revision.ID, ContentSHA256: sha256.Sum256([]byte("wrong")), CorrelationID: "99000000-0000-4000-8000-000000000009"}
	if _, err := service.DeleteSource(context.Background(), command); !errors.Is(err, ErrConstraint) {
		t.Fatalf("wrong source digest deletion err=%v", err)
	}
	command.ContentSHA256 = revision.ContentSHA256
	detail, err := service.DeleteSource(context.Background(), command)
	if err != nil || detail.Document.State != knowledgedomain.DocumentDeletionPending || detail.LatestRevision.ID != revision.ID || repository.mutation.ReasonCode != "source_deletion_requested" {
		t.Fatalf("deletion detail=%+v mutation=%+v err=%v", detail, repository.mutation, err)
	}
	if replayed, err := service.DeleteSource(context.Background(), command); err != nil || replayed.Document.State != knowledgedomain.DocumentDeletionPending {
		t.Fatalf("replayed source deletion=%+v err=%v", replayed, err)
	}
	command.Actor = access.Actor{UserID: appKnowledgeUser}
	if _, err := service.DeleteSource(context.Background(), command); !errors.Is(err, ErrInvalid) {
		t.Fatalf("human source deletion err=%v", err)
	}
}

func TestUploadAllowsAuthorizedWorkloadWithoutHumanRole(t *testing.T) {
	documents, authorizer, _, _ := documentServiceFixture(t)
	authorizer.account.Role = ""
	objects := &sourceObjectStore{created: true}
	service, _ := NewDocumentAdmissionService(documents, objects)
	document, revision, err := service.Upload(context.Background(), UploadDocumentCommand{
		Actor: access.Actor{WorkloadID: "prototype-migration"}, AccountID: appKnowledgeAccount,
		DocumentID: appKnowledgeDocument, RevisionID: appKnowledgeRevision, Title: "Migrated plan",
		Sensitivity: knowledgedomain.SensitivityInternal, Filename: "migrated-plan.txt", DeclaredType: "text/plain",
		Body: bytes.NewReader([]byte("source")), ChangeSummary: "Review-first import", CorrelationID: appKnowledgeOperation,
	})
	if err != nil || objects.puts != 1 || document.CreatedBy.Kind != knowledgedomain.ActorWorkload || document.CreatedBy.ID != "prototype-migration" || revision.CreatedBy != document.CreatedBy {
		t.Fatalf("document=%+v revision=%+v puts=%d err=%v", document, revision, objects.puts, err)
	}
}
