package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/integrationexecution"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type IntegrationExecutionRepository struct{ pool *pgxpool.Pool }

func NewIntegrationExecutionRepository(pool *pgxpool.Pool) (*IntegrationExecutionRepository, error) {
	if pool == nil {
		return nil, errors.New("Integration execution pool is required")
	}
	return &IntegrationExecutionRepository{pool: pool}, nil
}

func (repository *IntegrationExecutionRepository) Claim(ctx context.Context, attemptID ids.IntegrationAttemptID, now, leaseExpiresAt time.Time) (integrationexecution.Claim, bool, error) {
	var claim integrationexecution.Claim
	var digest []byte
	err := repository.pool.QueryRow(ctx, `SELECT account_id,execution_id,attempt_id,mode,capability,release_id,release_version,approval_id,
		connection_id,connection_revision_id,connection_revision,credential_id,credential_generation,payload_sha256,idempotency_key,lease_expires_at
		FROM public.spyglass_claim_integration_execution($1,$2,$3)`, attemptID, now.UTC(), leaseExpiresAt.UTC()).Scan(&claim.AccountID,
		&claim.ExecutionID, &claim.AttemptID, &claim.Mode, &claim.Capability, &claim.ReleaseID, &claim.ReleaseVersion, &claim.ApprovalID,
		&claim.ConnectionID, &claim.ConnectionRevisionID, &claim.ConnectionRevision, &claim.CredentialID, &claim.CredentialGeneration,
		&digest, &claim.IdempotencyKey, &claim.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return integrationexecution.Claim{}, false, nil
	}
	if err != nil {
		return integrationexecution.Claim{}, false, errors.Join(integrationexecution.ErrUnavailable, err)
	}
	if len(digest) != len(claim.ManifestSHA256) {
		return claim, true, integrationexecution.ErrInvalid
	}
	copy(claim.ManifestSHA256[:], digest)
	return claim, true, nil
}

func (repository *IntegrationExecutionRepository) Complete(ctx context.Context, completion integrationexecution.Completion) error {
	var errorCode any
	if completion.ErrorCode != "" {
		errorCode = completion.ErrorCode
	}
	_, err := repository.pool.Exec(ctx, `SELECT public.spyglass_complete_integration_execution($1,$2,$3,$4,$5,$6,$7)`, completion.Claim.AccountID,
		completion.Claim.ExecutionID, completion.Claim.AttemptID, completion.Outcome, errorCode, completion.RetryAt, completion.CompletedAt.UTC())
	if err != nil {
		return errors.Join(integrationexecution.ErrUnavailable, err)
	}
	return nil
}

var _ integrationexecution.Repository = (*IntegrationExecutionRepository)(nil)
