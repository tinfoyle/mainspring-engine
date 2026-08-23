package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationSourceSyncRepository struct{ pool *pgxpool.Pool }

func NewIntegrationSourceSyncRepository(pool *pgxpool.Pool) (*IntegrationSourceSyncRepository, error) {
	if pool == nil {
		return nil, errors.New("Integration source sync pool is required")
	}
	return &IntegrationSourceSyncRepository{pool: pool}, nil
}

func (repository *IntegrationSourceSyncRepository) Claim(ctx context.Context, syncID ids.IntegrationSourceSyncID, now, leaseExpiresAt time.Time) (integrationsync.Claim, bool, error) {
	var claim integrationsync.Claim
	var credentialDigest, cursorDigest []byte
	err := repository.pool.QueryRow(ctx, `SELECT account_id,sync_id,grant_id,connection_id,connection_revision_id,connection_revision,
		credential_id,credential_generation,credential_provider,credential_reference_sha256,folder_ids,cursor_ciphertext,
		cursor_sha256,lease_expires_at FROM public.spyglass_claim_integration_source_sync($1,$2,$3)`,
		syncID, now.UTC(), leaseExpiresAt.UTC()).Scan(&claim.AccountID, &claim.SyncID, &claim.GrantID, &claim.ConnectionID,
		&claim.ConnectionRevisionID, &claim.ConnectionRevision, &claim.CredentialID, &claim.CredentialGeneration,
		&claim.CredentialProvider, &credentialDigest, &claim.FolderIDs, &claim.CursorCiphertext, &cursorDigest, &claim.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationsync.Claim{}, false, nil
	}
	if err != nil {
		return integrationsync.Claim{}, false, errors.Join(integrationsync.ErrUnavailable, err)
	}
	if len(credentialDigest) != len(claim.CredentialReferenceSHA256) || (len(cursorDigest) != 0 && len(cursorDigest) != len(claim.CursorSHA256)) {
		return claim, true, integrationsync.ErrInvalid
	}
	copy(claim.CredentialReferenceSHA256[:], credentialDigest)
	copy(claim.CursorSHA256[:], cursorDigest)
	return claim, true, nil
}

func (repository *IntegrationSourceSyncRepository) ResolvePriorFolder(ctx context.Context, claim integrationsync.Claim,
	providerObjectSHA256 [sha256.Size]byte) (string, bool, error) {
	if !claim.Valid(claim.LeaseExpiresAt.Add(-time.Nanosecond)) || providerObjectSHA256 == ([sha256.Size]byte{}) {
		return "", false, integrationsync.ErrInvalid
	}
	var folderID string
	err := repository.pool.QueryRow(ctx, `SELECT folder_id FROM public.spyglass_resolve_integration_source_folder($1,$2,$3,$4)`,
		claim.AccountID, claim.GrantID, claim.SyncID, providerObjectSHA256[:]).Scan(&folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.Join(integrationsync.ErrUnavailable, err)
	}
	return folderID, true, nil
}

func (repository *IntegrationSourceSyncRepository) Complete(ctx context.Context, completion integrationsync.Completion) error {
	if !completion.Valid() {
		return integrationsync.ErrInvalid
	}
	captureIDs := make([]ids.IntegrationSourceCaptureID, len(completion.Captures))
	folderIDs := make([]string, len(completion.Captures))
	providerObjectDigests := make([][]byte, len(completion.Captures))
	providerRevisionDigests := make([][]byte, len(completion.Captures))
	operations := make([]integrationsync.CaptureOperation, len(completion.Captures))
	documentIDs := make([]ids.KnowledgeDocumentID, len(completion.Captures))
	documentRevisionIDs := make([]ids.KnowledgeDocumentRevisionID, len(completion.Captures))
	contentDigests := make([][]byte, len(completion.Captures))
	for index, capture := range completion.Captures {
		captureIDs[index], folderIDs[index], operations[index] = capture.ID, capture.FolderID, capture.Operation
		providerObjectDigests[index] = append([]byte(nil), capture.ProviderObjectSHA256[:]...)
		providerRevisionDigests[index] = append([]byte(nil), capture.ProviderRevisionSHA256[:]...)
		documentIDs[index], documentRevisionIDs[index] = capture.DocumentID, capture.DocumentRevisionID
		contentDigests[index] = append([]byte(nil), capture.ContentSHA256[:]...)
	}
	_, err := repository.pool.Exec(ctx, `SELECT public.spyglass_complete_integration_source_sync(
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		completion.Claim.AccountID, completion.Claim.GrantID, completion.Claim.SyncID, completion.Claim.ConnectionRevisionID,
		completion.Claim.ConnectionRevision, completion.Claim.CredentialID, completion.Claim.CredentialGeneration,
		completion.CursorCiphertext, completion.CursorSHA256[:], completion.HasMore, captureIDs, folderIDs, providerObjectDigests,
		providerRevisionDigests, operations, documentIDs, documentRevisionIDs, contentDigests, completion.CompletedAt.UTC())
	if err != nil {
		return errors.Join(integrationsync.ErrUnavailable, err)
	}
	return nil
}

var _ integrationsync.Repository = (*IntegrationSourceSyncRepository)(nil)
