package integrationsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationcredentials"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumCursorBytes           = 4096
	MaximumChangeBytes           = 50 << 20
	MaximumFilenameBytes         = 255
	MaximumMediaTypeBytes        = 200
	MaximumProviderIdentityBytes = 1024
	MaximumTitleBytes            = 200
	MaximumLease                 = 5 * time.Minute
)

var validMediaType = regexp.MustCompile(`^[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+$`)

type Authority interface {
	AuthorizeAccount(context.Context, ids.AccountID) error
}

type CursorCipher interface {
	Open(context.Context, ids.AccountID, ids.BaselineSourceGrantID, []byte, [sha256.Size]byte) ([]byte, error)
	Seal(context.Context, ids.AccountID, ids.BaselineSourceGrantID, []byte) ([]byte, [sha256.Size]byte, error)
}

type ProviderRequest struct {
	AccountID          ids.AccountID
	GrantID            ids.BaselineSourceGrantID
	CredentialProvider string
	SourceKind         domain.ConnectorKind
	FolderIDs          []string
	SinceAt            *time.Time
	UntilAt            *time.Time
	Cursor             []byte
	Credential         []byte
}

type ProviderChange struct {
	FolderID, ObjectID, RevisionID string
	Title, Filename, MediaType     string
	Deleted                        bool
	Content                        []byte
}

type ProviderPage struct {
	Changes    []ProviderChange
	NextCursor []byte
	HasMore    bool
}

type Provider interface {
	Sync(context.Context, ProviderRequest) (ProviderPage, error)
}

type CaptureInput struct {
	Claim                  Claim
	FolderID               string
	ProviderObjectSHA256   [sha256.Size]byte
	ProviderRevisionSHA256 [sha256.Size]byte
	Title, Filename        string
	MediaType              string
	Deleted                bool
	Content                []byte
	CapturedAt             time.Time
}

type CaptureSink interface {
	Capture(context.Context, CaptureInput) (CaptureReceipt, error)
}

type Clock interface{ Now() time.Time }

type Service struct {
	repository Repository
	authority  Authority
	broker     integrationcredentials.Broker
	cursors    CursorCipher
	provider   Provider
	sink       CaptureSink
	ids        ids.Generator
	clock      Clock
	lease      time.Duration
	timeout    time.Duration
}

func New(repository Repository, authority Authority, broker integrationcredentials.Broker, cursors CursorCipher, provider Provider,
	sink CaptureSink, generator ids.Generator, clock Clock, lease, timeout time.Duration) (*Service, error) {
	if repository == nil || authority == nil || broker == nil || cursors == nil || provider == nil || sink == nil || generator == nil || clock == nil ||
		lease < time.Second || lease > MaximumLease || timeout < 100*time.Millisecond || timeout > MaximumLease {
		return nil, ErrInvalid
	}
	return &Service{repository: repository, authority: authority, broker: broker, cursors: cursors, provider: provider, sink: sink,
		ids: generator, clock: clock, lease: lease, timeout: timeout}, nil
}

func (service *Service) ProcessOne(ctx context.Context) (bool, error) {
	now := service.clock.Now().UTC()
	syncID := ids.IntegrationSourceSyncID(service.ids.New())
	if ids.Validate(string(syncID)) != nil {
		return false, ErrInvalid
	}
	claim, found, err := service.repository.Claim(ctx, syncID, now, now.Add(service.lease))
	if err != nil || !found {
		return false, err
	}
	if claim.SyncID != syncID || !claim.Valid(now) {
		return true, ErrInvalid
	}
	if err := service.authority.AuthorizeAccount(ctx, claim.AccountID); err != nil {
		return true, errors.Join(ErrUnavailable, err)
	}
	lease, err := service.broker.Acquire(ctx, integrationcredentials.Request{AccountID: claim.AccountID, OperationID: string(claim.SyncID),
		Purpose: integrationcredentials.PurposeSync, Capability: claim.Capability(), ConnectionID: claim.ConnectionID,
		CredentialID: claim.CredentialID, CredentialGeneration: claim.CredentialGeneration, CredentialProvider: claim.CredentialProvider,
		ReferenceSHA256: claim.CredentialReferenceSHA256, ExpiresAt: claim.LeaseExpiresAt})
	if err != nil || lease == nil {
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(ErrUnavailable, err)
	}
	material := lease.Material()
	if len(material) == 0 || len(material) > 64<<10 {
		closeErr := lease.Close()
		return true, errors.Join(ErrUnavailable, ErrInvalid, closeErr)
	}
	credential := append([]byte(nil), material...)
	defer wipe(credential)
	defer lease.Close()
	cursor, err := service.cursors.Open(ctx, claim.AccountID, claim.GrantID, claim.CursorCiphertext, claim.CursorSHA256)
	if err != nil || len(cursor) > MaximumCursorBytes {
		wipe(cursor)
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(ErrUnavailable, err)
	}
	defer wipe(cursor)
	providerContext, cancel := context.WithDeadline(ctx, minimum(now.Add(service.timeout), claim.LeaseExpiresAt))
	page, err := service.provider.Sync(providerContext, ProviderRequest{AccountID: claim.AccountID, GrantID: claim.GrantID,
		CredentialProvider: claim.CredentialProvider, SourceKind: claim.SourceKind, FolderIDs: append([]string(nil), claim.FolderIDs...),
		SinceAt: cloneTime(claim.SinceAt), UntilAt: cloneTime(claim.UntilAt), Cursor: cursor, Credential: credential})
	cancel()
	if err != nil || !validPage(page, claim) {
		wipePage(page)
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(ErrUnavailable, err)
	}
	defer wipePage(page)
	capturedAt := service.clock.Now().UTC()
	receipts := make([]CaptureReceipt, 0, len(page.Changes))
	for index := range page.Changes {
		change := &page.Changes[index]
		objectDigest, revisionDigest := sha256.Sum256([]byte(change.ObjectID)), sha256.Sum256([]byte(change.RevisionID))
		if change.Deleted && change.FolderID == "" && claim.SourceKind == domain.ConnectorGoogleDrive {
			priorFolder, found, resolveErr := service.repository.ResolvePriorFolder(ctx, claim, objectDigest)
			if resolveErr != nil {
				return true, errors.Join(ErrUnavailable, resolveErr)
			}
			if !found {
				continue
			}
			change.FolderID = priorFolder
		}
		receipt, err := service.sink.Capture(ctx, CaptureInput{Claim: claim, FolderID: change.FolderID,
			ProviderObjectSHA256: objectDigest, ProviderRevisionSHA256: revisionDigest, Title: change.Title, Filename: change.Filename,
			MediaType: change.MediaType, Deleted: change.Deleted, Content: change.Content, CapturedAt: capturedAt})
		if err != nil || !receiptMatches(receipt, claim, *change, objectDigest, revisionDigest) {
			if err == nil {
				err = ErrInvalid
			}
			return true, errors.Join(ErrUnavailable, err)
		}
		receipts = append(receipts, receipt)
	}
	ciphertext, cursorDigest, err := service.cursors.Seal(ctx, claim.AccountID, claim.GrantID, page.NextCursor)
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > MaximumCursorCiphertextBytes || cursorDigest == [sha256.Size]byte{} {
		if err == nil {
			err = ErrInvalid
		}
		return true, errors.Join(ErrUnavailable, err)
	}
	completion := Completion{Claim: claim, CursorCiphertext: ciphertext, CursorSHA256: cursorDigest, Captures: receipts,
		HasMore: page.HasMore, CompletedAt: service.clock.Now().UTC()}
	completionContext, completionCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer completionCancel()
	if err := service.repository.Complete(completionContext, completion); err != nil {
		return true, fmt.Errorf("%w: settle provider sync: %v", ErrUnavailable, err)
	}
	return true, nil
}

func validPage(page ProviderPage, claim Claim) bool {
	if len(page.Changes) > MaximumCaptureBatch || len(page.NextCursor) == 0 || len(page.NextCursor) > MaximumCursorBytes {
		return false
	}
	seen := make(map[[sha256.Size * 2]byte]struct{}, len(page.Changes))
	for _, change := range page.Changes {
		if !validChange(change, claim) {
			return false
		}
		objectDigest, revisionDigest := sha256.Sum256([]byte(change.ObjectID)), sha256.Sum256([]byte(change.RevisionID))
		var key [sha256.Size * 2]byte
		copy(key[:sha256.Size], objectDigest[:])
		copy(key[sha256.Size:], revisionDigest[:])
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validChange(change ProviderChange, claim Claim) bool {
	if !boundedText(change.ObjectID, MaximumProviderIdentityBytes, true) || !boundedText(change.RevisionID, MaximumProviderIdentityBytes, true) {
		return false
	}
	if change.Deleted {
		return ((claim.SourceKind == domain.ConnectorGoogleDrive && change.FolderID == "") || contains(claim.FolderIDs, change.FolderID)) && change.Title == "" &&
			change.Filename == "" && change.MediaType == "" && len(change.Content) == 0
	}
	return contains(claim.FolderIDs, change.FolderID) && boundedText(change.Title, MaximumTitleBytes, true) && boundedText(change.Filename, MaximumFilenameBytes, true) &&
		!strings.ContainsAny(change.Filename, `/\`) && len(change.MediaType) <= MaximumMediaTypeBytes && validMediaType.MatchString(change.MediaType) &&
		len(change.Content) > 0 && len(change.Content) <= MaximumChangeBytes
}

func receiptMatches(receipt CaptureReceipt, claim Claim, change ProviderChange, objectDigest, revisionDigest [sha256.Size]byte) bool {
	operation := CaptureAdmitted
	if change.Deleted {
		operation = CaptureDeleted
	}
	if !receipt.Valid(claim) || receipt.FolderID != change.FolderID || receipt.ProviderObjectSHA256 != objectDigest ||
		receipt.ProviderRevisionSHA256 != revisionDigest || receipt.Operation != operation {
		return false
	}
	return change.Deleted || receipt.ContentSHA256 == sha256.Sum256(change.Content)
}

func boundedText(value string, maximum int, required bool) bool {
	return value == strings.TrimSpace(value) && (!required || value != "") && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func wipePage(page ProviderPage) {
	wipe(page.NextCursor)
	for index := range page.Changes {
		wipe(page.Changes[index].Content)
	}
}

func minimum(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
