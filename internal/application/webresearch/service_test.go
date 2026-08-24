package webresearch

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/modules/catalog"
	integrationsdomain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	testAccount    = ids.AccountID("b1100000-0000-4000-8000-000000000001")
	testConnection = ids.IntegrationConnectionID("b1200000-0000-4000-8000-000000000002")
	testRevision   = ids.IntegrationConnectionRevisionID("b1300000-0000-4000-8000-000000000003")
	testCredential = ids.IntegrationCredentialID("b1400000-0000-4000-8000-000000000004")
	testOperation  = "b1500000-0000-4000-8000-000000000005"
)

type testClock struct{ now time.Time }

func (clock testClock) Now() time.Time { return clock.now }

type testAuthorizer struct {
	calls []access.Requirement
	drift bool
	err   error
}

func (authorizer *testAuthorizer) Authorize(_ context.Context, _ access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	authorizer.calls = append(authorizer.calls, requirement)
	if authorizer.err != nil {
		return access.AccountContext{}, authorizer.err
	}
	context := access.AccountContext{AccountID: accountID, CellID: "b1600000-0000-4000-8000-000000000006", PlacementGeneration: 1, EntitlementVersion: 2}
	if authorizer.drift && requirement.Package == catalog.PackageKnowledge {
		context.PlacementGeneration++
	}
	return context, nil
}

type testRepository struct {
	authority ConnectionAuthority
	captures  []Capture
	err       error
}

func (repository *testRepository) RecordCapture(_ context.Context, capture Capture) (Capture, error) {
	if repository.err != nil {
		return Capture{}, repository.err
	}
	repository.captures = append(repository.captures, capture)
	return capture, nil
}

func (repository *testRepository) GetCapture(_ context.Context, accountID ids.AccountID, captureID ids.WebResearchCaptureID) (Capture, error) {
	for _, capture := range repository.captures {
		if capture.AccountID == accountID && capture.ID == captureID {
			return capture, nil
		}
	}
	return Capture{}, ErrNotFound
}

// Resolve is written separately to keep the exact Account/connection types in
// the interface visible to the compiler.
func (repository *testRepository) resolve(_ context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, _ time.Time) (ConnectionAuthority, error) {
	if repository.err != nil {
		return ConnectionAuthority{}, repository.err
	}
	if accountID != repository.authority.AccountID || connectionID != repository.authority.ConnectionID {
		return ConnectionAuthority{}, ErrNotFound
	}
	return repository.authority, nil
}

type repositoryAdapter struct{ *testRepository }

func (repository repositoryAdapter) Resolve(ctx context.Context, accountID ids.AccountID, connectionID ids.IntegrationConnectionID, at time.Time) (ConnectionAuthority, error) {
	return repository.resolve(ctx, accountID, connectionID, at)
}

func (repository repositoryAdapter) GetCapture(ctx context.Context, accountID ids.AccountID, captureID ids.WebResearchCaptureID) (Capture, error) {
	return repository.testRepository.GetCapture(ctx, accountID, captureID)
}

type testLease struct {
	material []byte
	closed   bool
}

func (lease *testLease) Material() []byte { return lease.material }
func (lease *testLease) Close() error {
	for index := range lease.material {
		lease.material[index] = 0
	}
	lease.closed = true
	return nil
}

type testBroker struct {
	request integrationcredentials.Request
	lease   *testLease
	err     error
}

func (broker *testBroker) Acquire(_ context.Context, request integrationcredentials.Request) (integrationcredentials.Lease, error) {
	broker.request = request
	if broker.err != nil {
		return nil, broker.err
	}
	broker.lease = &testLease{material: []byte("search-token")}
	return broker.lease, nil
}

type testProvider struct {
	searchRequest SearchRequest
	readRequest   ReadRequest
	hits          []SearchHit
	page          ReadPage
	err           error
	credentialRef []byte
}

func (provider *testProvider) Search(_ context.Context, request SearchRequest) ([]SearchHit, error) {
	provider.searchRequest = request
	provider.credentialRef = request.Credential
	return provider.hits, provider.err
}

func (provider *testProvider) Read(_ context.Context, request ReadRequest) (ReadPage, error) {
	provider.readRequest = request
	provider.credentialRef = request.Credential
	return provider.page, provider.err
}

type testDocuments struct {
	detail knowledgeapp.DocumentDetail
	err    error
}

func (documents *testDocuments) GetDetail(_ context.Context, _ access.Actor, _ ids.AccountID, _ ids.KnowledgeDocumentID) (knowledgeapp.DocumentDetail, error) {
	return documents.detail, documents.err
}

type testAdmission struct {
	uploads   []knowledgeapp.UploadDocumentCommand
	revisions []knowledgeapp.UploadDocumentRevisionCommand
	body      []byte
}

func (admission *testAdmission) Upload(_ context.Context, command knowledgeapp.UploadDocumentCommand) (knowledgedomain.Document, knowledgedomain.DocumentRevision, error) {
	content, err := io.ReadAll(command.Body)
	if err != nil {
		return knowledgedomain.Document{}, knowledgedomain.DocumentRevision{}, err
	}
	admission.uploads, admission.body = append(admission.uploads, command), content
	return knowledgedomain.Document{ID: command.DocumentID, AccountID: command.AccountID}, knowledgedomain.DocumentRevision{
		ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID, ContentSHA256: sha256.Sum256(content)}, nil
}

func (admission *testAdmission) UploadRevision(_ context.Context, command knowledgeapp.UploadDocumentRevisionCommand) (knowledgedomain.DocumentRevision, error) {
	content, err := io.ReadAll(command.Body)
	if err != nil {
		return knowledgedomain.DocumentRevision{}, err
	}
	admission.revisions, admission.body = append(admission.revisions, command), content
	return knowledgedomain.DocumentRevision{ID: command.RevisionID, DocumentID: command.DocumentID, AccountID: command.AccountID,
		ContentSHA256: sha256.Sum256(content)}, nil
}

func TestSearchUsesExactReadOnlyAuthorityCredentialAndScope(t *testing.T) {
	service, fixture := newFixture(t)
	fixture.provider.hits = []SearchHit{
		{Title: "Official rule", URL: "https://Research.Example/authoritative/rule", Description: "Current rule", RetrievedAt: fixture.clock.now},
		{Title: "Duplicate", URL: "https://research.example/authoritative/rule", Description: "Same URL", RetrievedAt: fixture.clock.now},
	}
	result, err := service.Search(context.Background(), SearchCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, Query: " current rule ", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Query != "current rule" || len(result.Items) != 1 || result.Items[0].URL != "https://research.example/authoritative/rule" ||
		result.Items[0].CitationID == "" || fixture.provider.searchRequest.Scope.HTTPSOrigin != "https://research.example" {
		t.Fatalf("result=%+v request=%+v", result, fixture.provider.searchRequest)
	}
	if fixture.broker.request.Purpose != integrationcredentials.PurposeResearch || fixture.broker.request.Capability != integrationsdomain.CapabilityWebResearch ||
		fixture.broker.request.ReferenceSHA256 != fixture.repository.authority.CredentialReferenceSHA256 || !fixture.broker.lease.closed ||
		!allZero(fixture.provider.credentialRef) {
		t.Fatalf("broker=%+v lease=%+v provider credential=%v", fixture.broker.request, fixture.broker.lease, fixture.provider.credentialRef)
	}
	if len(fixture.authorizer.calls) != 1 || fixture.authorizer.calls[0].Package != catalog.PackageIntegrations || fixture.authorizer.calls[0].Mutation {
		t.Fatalf("authority calls=%+v", fixture.authorizer.calls)
	}
}

func TestReadCapturesImmutableKnowledgeRevisionAndSourceAttribution(t *testing.T) {
	service, fixture := newFixture(t)
	content := []byte("<!doctype html><title>Rule</title><p>Evidence</p>")
	expectedDigest := sha256.Sum256(content)
	fixture.provider.page = ReadPage{CanonicalURL: "https://research.example/authoritative/rule", Title: "Official rule", MediaType: "text/html",
		Content: append([]byte(nil), content...), Excerpt: "Evidence", SHA256: expectedDigest, RetrievedAt: fixture.clock.now}
	result, err := service.Read(context.Background(), ReadCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, URL: "https://research.example/authoritative/rule"})
	if err != nil {
		t.Fatal(err)
	}
	if result.CaptureID != ids.WebResearchCaptureID(testOperation) || result.DocumentID == "" || result.DocumentRevisionID == "" ||
		result.ContentSHA256 != hexDigest(content) || result.Excerpt != "Evidence" || len(fixture.admission.uploads) != 1 ||
		!slices.Equal(fixture.admission.body, content) || len(fixture.repository.captures) != 1 {
		t.Fatalf("result=%+v uploads=%d captures=%+v", result, len(fixture.admission.uploads), fixture.repository.captures)
	}
	capture := fixture.repository.captures[0]
	if capture.CanonicalURL != result.URL || capture.ContentSHA256 != expectedDigest || capture.DocumentID != result.DocumentID ||
		capture.RequestedURLSHA256 == ([32]byte{}) || capture.CreatedBy != fixture.actor || !allZero(fixture.provider.page.Content) || !allZero(fixture.provider.credentialRef) {
		t.Fatalf("capture=%+v provider page=%v credential=%v", capture, fixture.provider.page.Content, fixture.provider.credentialRef)
	}
	if len(fixture.authorizer.calls) != 2 || fixture.authorizer.calls[0].Package != catalog.PackageIntegrations ||
		fixture.authorizer.calls[1].Package != catalog.PackageKnowledge || !fixture.authorizer.calls[0].Mutation || !fixture.authorizer.calls[1].Mutation {
		t.Fatalf("authority calls=%+v", fixture.authorizer.calls)
	}
}

func TestReadChangedContentCreatesRevisionAndReplayDoesNotDuplicate(t *testing.T) {
	service, fixture := newFixture(t)
	content := []byte("changed evidence")
	documentID, revisionID, err := captureDocumentIDs(testConnection, "https://research.example/authoritative/rule", sha256.Sum256(content))
	if err != nil {
		t.Fatal(err)
	}
	fixture.documents.err = nil
	fixture.documents.detail = knowledgeapp.DocumentDetail{Document: knowledgedomain.Document{ID: documentID, AccountID: testAccount},
		LatestRevision: knowledgedomain.DocumentRevision{ID: "b1700000-0000-4000-8000-000000000007", DocumentID: documentID, AccountID: testAccount}}
	fixture.provider.page = ReadPage{CanonicalURL: "https://research.example/authoritative/rule", MediaType: "text/plain", Content: content,
		SHA256: sha256.Sum256(content), RetrievedAt: fixture.clock.now}
	result, err := service.Read(context.Background(), ReadCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, URL: fixture.provider.page.CanonicalURL})
	if err != nil || result.DocumentRevisionID != revisionID || len(fixture.admission.revisions) != 1 || len(fixture.admission.uploads) != 0 {
		t.Fatalf("result=%+v revisions=%d err=%v", result, len(fixture.admission.revisions), err)
	}
	fixture.documents.detail.LatestRevision.ID = revisionID
	fixture.provider.page.Content = []byte("changed evidence")
	if _, err := service.Read(context.Background(), ReadCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, URL: fixture.provider.page.CanonicalURL}); err != nil || len(fixture.admission.revisions) != 1 {
		t.Fatalf("replay revisions=%d captures=%+v page=%+v err=%v", len(fixture.admission.revisions), fixture.repository.captures, fixture.provider.page, err)
	}
}

func TestResearchRejectsScopeEscapeInvalidProviderAndAuthorityDrift(t *testing.T) {
	service, fixture := newFixture(t)
	if _, err := service.Read(context.Background(), ReadCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, URL: "https://other.example/authoritative/rule"}); !errors.Is(err, ErrInvalid) || fixture.provider.readRequest.URL != "" {
		t.Fatalf("scope escape err=%v request=%+v", err, fixture.provider.readRequest)
	}
	fixture.provider.hits = []SearchHit{{Title: "Unsafe", URL: "https://other.example/", RetrievedAt: fixture.clock.now}}
	if _, err := service.Search(context.Background(), SearchCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, Query: "rule"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsafe provider result err=%v", err)
	}
	service, fixture = newFixture(t)
	fixture.authorizer.drift = true
	if _, err := service.Read(context.Background(), ReadCommand{Actor: fixture.actor, AccountID: testAccount, OperationID: testOperation,
		ConnectionID: testConnection, URL: "https://research.example/authoritative/rule"}); !errors.Is(err, ErrUnavailable) || fixture.broker.request.OperationID != "" {
		t.Fatalf("authority drift err=%v broker=%+v", err, fixture.broker.request)
	}
}

type fixture struct {
	clock      testClock
	actor      access.Actor
	authorizer *testAuthorizer
	repository *testRepository
	broker     *testBroker
	provider   *testProvider
	documents  *testDocuments
	admission  *testAdmission
}

func newFixture(t *testing.T) (*Service, *fixture) {
	t.Helper()
	now := time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC)
	value := &fixture{clock: testClock{now: now}, actor: access.Actor{WorkloadID: "runner-invocation:b1800000-0000-4000-8000-000000000008"},
		authorizer: &testAuthorizer{}, broker: &testBroker{}, provider: &testProvider{}, documents: &testDocuments{err: knowledgeapp.ErrNotFound}, admission: &testAdmission{}}
	value.repository = &testRepository{authority: ConnectionAuthority{AccountID: testAccount, ConnectionID: testConnection,
		ConnectionRevisionID: testRevision, ConnectionRevision: 1, Scope: integrationsdomain.ConnectionScope{HTTPSOrigin: "https://research.example", PathPrefix: "/authoritative"},
		CredentialID: testCredential, CredentialGeneration: 1, CredentialProvider: "firecrawl", CredentialReferenceSHA256: sha256.Sum256([]byte("reference"))}}
	service, err := New(value.authorizer, repositoryAdapter{value.repository}, value.broker, value.provider, value.documents, value.admission, value.clock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return service, value
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func hexDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return fmtHex(digest[:])
}

func fmtHex(value []byte) string {
	const alphabet = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, item := range value {
		result[index*2], result[index*2+1] = alphabet[item>>4], alphabet[item&15]
	}
	return string(result)
}

var _ Repository = repositoryAdapter{}
