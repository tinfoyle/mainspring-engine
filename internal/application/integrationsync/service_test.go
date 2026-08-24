package integrationsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func TestServiceAdvancesOnlyAfterExactCaptureReceipts(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	claim := syncClaim(now)
	repository := &fakeSyncRepository{claim: claim, found: true}
	authority := &fakeSyncAuthority{}
	broker := &fakeSyncBroker{material: []byte(`{"refresh_token":"fixture"}`)}
	cursors := &fakeCursorCipher{}
	content := []byte("governed content")
	expectedContentDigest := sha256.Sum256(content)
	provider := &fakeDriveProvider{page: ProviderPage{NextCursor: []byte("next-page"), HasMore: true,
		Changes: []ProviderChange{{FolderID: "folder-a", ObjectID: "object-a", RevisionID: "revision-a", Title: "Formation record",
			Filename: "formation.txt", MediaType: "text/plain", Content: content}}}}
	sink := &fakeCaptureSink{}
	service, err := New(repository, authority, broker, cursors, provider, sink, fixedSyncIDs{id: string(claim.SyncID)}, fixedSyncClock{now},
		time.Minute, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completed == nil || len(repository.completed.Captures) != 1 {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, repository.completed, err)
	}
	if broker.request.Purpose != integrationcredentials.PurposeSync || broker.request.OperationID != string(claim.SyncID) {
		t.Fatalf("credential request=%+v", broker.request)
	}
	if len(cursors.openedCiphertext) != 0 || string(cursors.sealedPlaintext) != "next-page" || !repository.completed.HasMore {
		t.Fatalf("cursor open=%q seal=%q completion=%+v", cursors.openedCiphertext, cursors.sealedPlaintext, repository.completed)
	}
	if sink.input.ProviderObjectSHA256 != sha256.Sum256([]byte("object-a")) || sink.contentDigest != expectedContentDigest {
		t.Fatalf("sink input=%+v", sink.input)
	}
	for _, value := range content {
		if value != 0 {
			t.Fatalf("provider content was not wiped: %v", content)
		}
	}
}

func TestServiceRejectsProviderScopeDriftWithoutAdvancingCursor(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	claim := syncClaim(now)
	repository := &fakeSyncRepository{claim: claim, found: true}
	provider := &fakeDriveProvider{page: ProviderPage{NextCursor: []byte("next-page"), Changes: []ProviderChange{{
		FolderID: "folder-outside", ObjectID: "object-a", RevisionID: "revision-a", Title: "Record", Filename: "record.txt",
		MediaType: "text/plain", Content: []byte("content")}}}}
	service, err := New(repository, &fakeSyncAuthority{}, &fakeSyncBroker{material: []byte("credential")}, &fakeCursorCipher{}, provider,
		&fakeCaptureSink{}, fixedSyncIDs{id: string(claim.SyncID)}, fixedSyncClock{now}, time.Minute, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if !worked || !errors.Is(err, ErrInvalid) || repository.completed != nil {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, repository.completed, err)
	}
}

func TestServiceResolvesRemovedFileToPriorAuthorizedFolder(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	claim := syncClaim(now)
	repository := &fakeSyncRepository{claim: claim, found: true, priorFolder: "folder-a", priorFound: true}
	provider := &fakeDriveProvider{page: ProviderPage{NextCursor: []byte("next-page"), Changes: []ProviderChange{{
		ObjectID: "removed-object", RevisionID: "removed-at-page-7", Deleted: true}}}}
	sink := &fakeCaptureSink{}
	service, err := New(repository, &fakeSyncAuthority{}, &fakeSyncBroker{material: []byte("credential")}, &fakeCursorCipher{}, provider,
		sink, fixedSyncIDs{id: string(claim.SyncID)}, fixedSyncClock{now}, time.Minute, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completed == nil || len(repository.completed.Captures) != 1 ||
		repository.resolvedObject != sha256.Sum256([]byte("removed-object")) || sink.input.FolderID != "folder-a" || !sink.input.Deleted {
		t.Fatalf("worked=%v resolved=%x sink=%+v completion=%+v err=%v", worked, repository.resolvedObject, sink.input, repository.completed, err)
	}
}

func TestServiceIgnoresUnknownRemovalAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)
	claim := syncClaim(now)
	repository := &fakeSyncRepository{claim: claim, found: true}
	provider := &fakeDriveProvider{page: ProviderPage{NextCursor: []byte("next-page"), Changes: []ProviderChange{{
		ObjectID: "unknown-object", RevisionID: "removed-at-page-8", Deleted: true}}}}
	sink := &fakeCaptureSink{}
	service, _ := New(repository, &fakeSyncAuthority{}, &fakeSyncBroker{material: []byte("credential")}, &fakeCursorCipher{}, provider,
		sink, fixedSyncIDs{id: string(claim.SyncID)}, fixedSyncClock{now}, time.Minute, 30*time.Second)
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completed == nil || len(repository.completed.Captures) != 0 || sink.input.Claim.AccountID != "" {
		t.Fatalf("worked=%v sink=%+v completion=%+v err=%v", worked, sink.input, repository.completed, err)
	}
}

func TestServiceCarriesExactEmailReadAndDateScopeToProvider(t *testing.T) {
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	since, until := now.Add(-24*time.Hour), now.Add(time.Hour)
	claim := syncClaim(now)
	claim.SourceKind, claim.FolderIDs, claim.SinceAt, claim.UntilAt = domain.ConnectorEmail, []string{"INBOX"}, &since, &until
	claim.CredentialProvider = "imap"
	repository := &fakeSyncRepository{claim: claim, found: true}
	provider := &fakeDriveProvider{page: ProviderPage{NextCursor: []byte("email-cursor"), Changes: []ProviderChange{{
		FolderID: "INBOX", ObjectID: "uidvalidity:7/uid:42/body", RevisionID: "message-sha256:fixture", Title: "Quarterly record",
		Filename: "message.eml", MediaType: "message/rfc822", Content: []byte("bounded message")}}}}
	broker := &fakeSyncBroker{material: []byte(`{"username":"reader@example.com","password":"fixture"}`)}
	service, err := New(repository, &fakeSyncAuthority{}, broker, &fakeCursorCipher{}, provider, &fakeCaptureSink{},
		fixedSyncIDs{id: string(claim.SyncID)}, fixedSyncClock{now}, time.Minute, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := service.ProcessOne(context.Background())
	if err != nil || !worked || repository.completed == nil || len(repository.completed.Captures) != 1 {
		t.Fatalf("worked=%v completion=%+v err=%v", worked, repository.completed, err)
	}
	if broker.request.Purpose != integrationcredentials.PurposeSync || broker.request.Capability != domain.CapabilityEmailRead ||
		provider.request.SourceKind != domain.ConnectorEmail || provider.request.SinceAt == nil || !provider.request.SinceAt.Equal(since) ||
		provider.request.UntilAt == nil || !provider.request.UntilAt.Equal(until) {
		t.Fatalf("broker=%+v provider=%+v", broker.request, provider.request)
	}
}

func syncClaim(now time.Time) Claim {
	return Claim{AccountID: "d1000000-0000-4000-8000-000000000001", SyncID: "d2000000-0000-4000-8000-000000000002",
		GrantID: "d3000000-0000-4000-8000-000000000003", ConnectionID: "d4000000-0000-4000-8000-000000000004",
		ConnectionRevisionID: "d5000000-0000-4000-8000-000000000005", ConnectionRevision: 1,
		CredentialID: "d6000000-0000-4000-8000-000000000006", CredentialGeneration: 1, CredentialProvider: "google_drive",
		CredentialReferenceSHA256: sha256.Sum256([]byte("reference")), SourceKind: domain.ConnectorGoogleDrive,
		FolderIDs: []string{"folder-a"}, LeaseExpiresAt: now.Add(time.Minute)}
}

type fakeSyncRepository struct {
	claim          Claim
	found          bool
	priorFolder    string
	priorFound     bool
	resolvedObject [sha256.Size]byte
	completed      *Completion
}

func (value *fakeSyncRepository) Claim(context.Context, ids.IntegrationSourceSyncID, time.Time, time.Time) (Claim, bool, error) {
	return value.claim, value.found, nil
}
func (value *fakeSyncRepository) ResolvePriorFolder(_ context.Context, _ Claim, digest [sha256.Size]byte) (string, bool, error) {
	value.resolvedObject = digest
	return value.priorFolder, value.priorFound, nil
}
func (value *fakeSyncRepository) Complete(_ context.Context, completion Completion) error {
	value.completed = &completion
	return nil
}

type fakeSyncAuthority struct{ err error }

func (value *fakeSyncAuthority) AuthorizeAccount(context.Context, ids.AccountID) error {
	return value.err
}

type fakeSyncBroker struct {
	material []byte
	request  integrationcredentials.Request
}

func (value *fakeSyncBroker) Acquire(_ context.Context, request integrationcredentials.Request) (integrationcredentials.Lease, error) {
	value.request = request
	return &fakeSyncLease{material: append([]byte(nil), value.material...)}, nil
}

type fakeSyncLease struct{ material []byte }

func (value *fakeSyncLease) Material() []byte { return value.material }
func (value *fakeSyncLease) Close() error {
	wipe(value.material)
	return nil
}

type fakeCursorCipher struct {
	openedCiphertext []byte
	sealedPlaintext  []byte
}

func (value *fakeCursorCipher) Open(_ context.Context, _ ids.AccountID, _ ids.BaselineSourceGrantID, ciphertext []byte, _ [sha256.Size]byte) ([]byte, error) {
	value.openedCiphertext = append([]byte(nil), ciphertext...)
	return nil, nil
}
func (value *fakeCursorCipher) Seal(_ context.Context, _ ids.AccountID, _ ids.BaselineSourceGrantID, plaintext []byte) ([]byte, [sha256.Size]byte, error) {
	value.sealedPlaintext = append([]byte(nil), plaintext...)
	return []byte("sealed-next-page"), sha256.Sum256(plaintext), nil
}

type fakeDriveProvider struct {
	page    ProviderPage
	request ProviderRequest
}

func (value *fakeDriveProvider) Sync(_ context.Context, request ProviderRequest) (ProviderPage, error) {
	value.request = request
	return value.page, nil
}

type fakeCaptureSink struct {
	input         CaptureInput
	contentDigest [sha256.Size]byte
}

func (value *fakeCaptureSink) Capture(_ context.Context, input CaptureInput) (CaptureReceipt, error) {
	value.input = input
	value.contentDigest = sha256.Sum256(input.Content)
	operation := CaptureAdmitted
	contentDigest := sha256.Sum256(input.Content)
	if input.Deleted {
		operation = CaptureDeleted
		contentDigest = sha256.Sum256([]byte("prior-content"))
	}
	return CaptureReceipt{ID: "d7000000-0000-4000-8000-000000000007", FolderID: input.FolderID,
		ProviderObjectSHA256: input.ProviderObjectSHA256, ProviderRevisionSHA256: input.ProviderRevisionSHA256, Operation: operation,
		DocumentID: "d8000000-0000-4000-8000-000000000008", DocumentRevisionID: "d9000000-0000-4000-8000-000000000009",
		ContentSHA256: contentDigest}, nil
}

type fixedSyncIDs struct{ id string }

func (value fixedSyncIDs) New() string { return value.id }

type fixedSyncClock struct{ now time.Time }

func (value fixedSyncClock) Now() time.Time { return value.now }
