// Package integrationsync defines the content-free persistence boundary for
// incremental provider capture. Provider payloads and plaintext cursors stay
// outside PostgreSQL and outside this contract.
package integrationsync

import (
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	domain "github.com/tinfoyle/spyglass-engine/internal/modules/integrations"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const (
	MaximumCursorCiphertextBytes = 8192
	MaximumCaptureBatch          = 100
)

var (
	ErrInvalid     = errors.New("integration source sync is invalid")
	ErrUnavailable = errors.New("integration source sync is unavailable")
	validProvider  = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,63}$`)
)

type Claim struct {
	AccountID                 ids.AccountID
	SyncID                    ids.IntegrationSourceSyncID
	GrantID                   ids.BaselineSourceGrantID
	ConnectionID              ids.IntegrationConnectionID
	ConnectionRevisionID      ids.IntegrationConnectionRevisionID
	ConnectionRevision        uint64
	CredentialID              ids.IntegrationCredentialID
	CredentialGeneration      uint64
	CredentialProvider        string
	CredentialReferenceSHA256 [sha256.Size]byte
	SourceKind                domain.ConnectorKind
	FolderIDs                 []string
	SinceAt                   *time.Time
	UntilAt                   *time.Time
	CursorCiphertext          []byte
	CursorSHA256              [sha256.Size]byte
	LeaseExpiresAt            time.Time
}

func (claim Claim) Valid(now time.Time) bool {
	if ids.Validate(string(claim.AccountID)) != nil || ids.Validate(string(claim.SyncID)) != nil ||
		ids.Validate(string(claim.GrantID)) != nil || ids.Validate(string(claim.ConnectionID)) != nil ||
		ids.Validate(string(claim.ConnectionRevisionID)) != nil || ids.Validate(string(claim.CredentialID)) != nil ||
		claim.ConnectionRevision == 0 || claim.CredentialGeneration == 0 || !validProvider.MatchString(claim.CredentialProvider) ||
		claim.CredentialReferenceSHA256 == [sha256.Size]byte{} || len(claim.FolderIDs) == 0 ||
		!claim.LeaseExpiresAt.After(now.UTC()) || len(claim.CursorCiphertext) > MaximumCursorCiphertextBytes {
		return false
	}
	maximumFolders, maximumFolderBytes := 0, 0
	switch claim.SourceKind {
	case domain.ConnectorGoogleDrive:
		maximumFolders, maximumFolderBytes = 50, 200
		if claim.SinceAt != nil || claim.UntilAt != nil {
			return false
		}
	case domain.ConnectorEmail:
		maximumFolders, maximumFolderBytes = 20, 512
		if claim.SinceAt != nil && claim.UntilAt != nil && claim.SinceAt.After(*claim.UntilAt) {
			return false
		}
	default:
		return false
	}
	if len(claim.FolderIDs) > maximumFolders {
		return false
	}
	if (len(claim.CursorCiphertext) == 0) != (claim.CursorSHA256 == [sha256.Size]byte{}) {
		return false
	}
	for index, folder := range claim.FolderIDs {
		if folder == "" || folder != strings.TrimSpace(folder) || len(folder) > maximumFolderBytes || !utf8.ValidString(folder) ||
			strings.ContainsRune(folder, '\x00') || (index > 0 && claim.FolderIDs[index-1] >= folder) {
			return false
		}
		if claim.SourceKind == domain.ConnectorGoogleDrive {
			for _, character := range []byte(folder) {
				if character < 0x21 || character > 0x7e {
					return false
				}
			}
		}
	}
	return true
}

func (claim Claim) Capability() domain.Capability {
	switch claim.SourceKind {
	case domain.ConnectorEmail:
		return domain.CapabilityEmailRead
	case domain.ConnectorGoogleDrive:
		return domain.CapabilityDriveRead
	default:
		return ""
	}
}

type CaptureOperation string

const (
	CaptureAdmitted CaptureOperation = "admitted"
	CaptureDeleted  CaptureOperation = "deleted"
)

type CaptureReceipt struct {
	ID                     ids.IntegrationSourceCaptureID
	FolderID               string
	ProviderObjectSHA256   [sha256.Size]byte
	ProviderRevisionSHA256 [sha256.Size]byte
	Operation              CaptureOperation
	DocumentID             ids.KnowledgeDocumentID
	DocumentRevisionID     ids.KnowledgeDocumentRevisionID
	ContentSHA256          [sha256.Size]byte
}

func (receipt CaptureReceipt) Valid(claim Claim) bool {
	return ids.Validate(string(receipt.ID)) == nil && slices.Contains(claim.FolderIDs, receipt.FolderID) &&
		receipt.ProviderObjectSHA256 != [sha256.Size]byte{} && receipt.ProviderRevisionSHA256 != [sha256.Size]byte{} &&
		(receipt.Operation == CaptureAdmitted || receipt.Operation == CaptureDeleted) &&
		ids.Validate(string(receipt.DocumentID)) == nil && ids.Validate(string(receipt.DocumentRevisionID)) == nil &&
		receipt.ContentSHA256 != [sha256.Size]byte{}
}

type Completion struct {
	Claim            Claim
	CursorCiphertext []byte
	CursorSHA256     [sha256.Size]byte
	Captures         []CaptureReceipt
	HasMore          bool
	CompletedAt      time.Time
}

func (completion Completion) Valid() bool {
	if !completion.Claim.Valid(completion.CompletedAt.Add(-time.Nanosecond)) || completion.CompletedAt.IsZero() ||
		completion.CompletedAt.After(completion.Claim.LeaseExpiresAt) || len(completion.CursorCiphertext) == 0 ||
		len(completion.CursorCiphertext) > MaximumCursorCiphertextBytes || completion.CursorSHA256 == [sha256.Size]byte{} ||
		len(completion.Captures) > MaximumCaptureBatch {
		return false
	}
	identities := make(map[ids.IntegrationSourceCaptureID]struct{}, len(completion.Captures))
	providerRevisions := make(map[[sha256.Size * 2]byte]struct{}, len(completion.Captures))
	for _, capture := range completion.Captures {
		if !capture.Valid(completion.Claim) {
			return false
		}
		if _, exists := identities[capture.ID]; exists {
			return false
		}
		identities[capture.ID] = struct{}{}
		var providerRevision [sha256.Size * 2]byte
		copy(providerRevision[:sha256.Size], capture.ProviderObjectSHA256[:])
		copy(providerRevision[sha256.Size:], capture.ProviderRevisionSHA256[:])
		if _, exists := providerRevisions[providerRevision]; exists {
			return false
		}
		providerRevisions[providerRevision] = struct{}{}
	}
	return true
}

type Repository interface {
	Claim(context.Context, ids.IntegrationSourceSyncID, time.Time, time.Time) (Claim, bool, error)
	ResolvePriorFolder(context.Context, Claim, [sha256.Size]byte) (string, bool, error)
	Complete(context.Context, Completion) error
}
