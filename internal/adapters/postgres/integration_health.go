package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationhealth"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationHealthRepository struct{ pool *pgxpool.Pool }

func NewIntegrationHealthRepository(pool *pgxpool.Pool) (*IntegrationHealthRepository, error) {
	if pool == nil {
		return nil, errors.New("Integration health pool is required")
	}
	return &IntegrationHealthRepository{pool: pool}, nil
}

func (repository *IntegrationHealthRepository) Claim(ctx context.Context, probeID ids.IntegrationHealthObservationID, now, leaseExpiresAt time.Time) (integrationhealth.Claim, bool, error) {
	var claim integrationhealth.Claim
	var referenceDigest []byte
	err := repository.pool.QueryRow(ctx, `SELECT account_id,probe_id,connection_id,connection_revision_id,connection_revision,
		connector_kind,capabilities,email_address,audience_reference,https_origin,path_prefix,drive_folder_ids,credential_id,credential_generation,
		credential_provider,credential_reference_sha256,lease_expires_at
		FROM public.spyglass_claim_integration_health_probe($1,$2,$3)`, probeID, now.UTC(), leaseExpiresAt.UTC()).Scan(
		&claim.AccountID, &claim.ProbeID, &claim.ConnectionID, &claim.ConnectionRevisionID, &claim.ConnectionRevision,
		&claim.ConnectorKind, &claim.Capabilities, &claim.Scope.EmailAddress, &claim.Scope.AudienceReference, &claim.Scope.HTTPSOrigin,
		&claim.Scope.PathPrefix, &claim.Scope.DriveFolderIDs, &claim.CredentialID, &claim.CredentialGeneration, &claim.CredentialProvider, &referenceDigest,
		&claim.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationhealth.Claim{}, false, nil
	}
	if err != nil {
		return integrationhealth.Claim{}, false, errors.Join(integrationhealth.ErrUnavailable, err)
	}
	if len(referenceDigest) != len(claim.CredentialReferenceSHA256) {
		return claim, true, integrationhealth.ErrInvalid
	}
	copy(claim.CredentialReferenceSHA256[:], referenceDigest)
	return claim, true, nil
}

func (repository *IntegrationHealthRepository) Complete(ctx context.Context, completion integrationhealth.Completion) error {
	var errorCode any
	if completion.ErrorCode != "" {
		errorCode = completion.ErrorCode
	}
	_, err := repository.pool.Exec(ctx, `SELECT public.spyglass_complete_integration_health_probe($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		completion.Claim.AccountID, completion.Claim.ConnectionID, completion.Claim.ProbeID, completion.Claim.ConnectionRevisionID,
		completion.Claim.ConnectionRevision, completion.Claim.CredentialID, completion.Claim.CredentialGeneration, completion.State,
		errorCode, completion.LatencyMilliseconds, completion.CheckedAt.UTC())
	if err != nil {
		return errors.Join(integrationhealth.ErrUnavailable, err)
	}
	return nil
}

var _ integrationhealth.Repository = (*IntegrationHealthRepository)(nil)
