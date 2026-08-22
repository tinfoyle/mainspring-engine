package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/approvedaction"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

type ApprovedActionRepository struct{ pool *pgxpool.Pool }

func NewApprovedActionRepository(pool *pgxpool.Pool) (*ApprovedActionRepository, error) {
	if pool == nil {
		return nil, errors.New("approved action pool is required")
	}
	return &ApprovedActionRepository{pool: pool}, nil
}

func (repository *ApprovedActionRepository) Claim(ctx context.Context, attemptID string, now, leaseExpiresAt time.Time) (approvedaction.Claim, bool, error) {
	var claim approvedaction.Claim
	var digest []byte
	var mode string
	err := repository.pool.QueryRow(ctx, `SELECT account_id,invocation_id,operation_id,attempt_id,capability,canonical_payload,
		input_sha256,approved_by_user_id,mode,idempotency_key,lease_expires_at
		FROM public.spyglass_claim_approved_runner_action($1,$2,$3)`, attemptID, now.UTC(), leaseExpiresAt.UTC()).Scan(
		&claim.Lease.AccountID, &claim.Lease.InvocationID, &claim.Lease.OperationID, &claim.Lease.AttemptID,
		&claim.Lease.Capability, &claim.CanonicalPayload, &digest, &claim.ApprovedByUserID, &mode,
		&claim.Lease.IdempotencyKey, &claim.Lease.LeaseExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return approvedaction.Claim{}, false, nil
	}
	if err != nil {
		return approvedaction.Claim{}, false, runnerActionError(err)
	}
	if len(digest) != sha256.Size {
		return claim, true, approvedaction.ErrInvalidClaim
	}
	copy(claim.Lease.InputDigest[:], digest)
	claim.Lease.Mode = runnercapability.ActionMode(mode)
	return claim, true, nil
}

func (repository *ApprovedActionRepository) Complete(ctx context.Context, completion runnercapability.ActionCompletion) error {
	lease := completion.Lease
	var errorCode *string
	if completion.ErrorCode != "" {
		errorCode = &completion.ErrorCode
	}
	_, err := repository.pool.Exec(ctx, `SELECT public.spyglass_complete_runner_action_v2($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		lease.AccountID, lease.InvocationID, lease.OperationID, lease.AttemptID, lease.Capability, lease.InputDigest[:],
		lease.Mode, lease.IdempotencyKey, lease.LeaseExpiresAt.UTC(), completion.Outcome, errorCode, completion.At.UTC())
	if err != nil {
		return runnerActionError(err)
	}
	return nil
}

var _ approvedaction.Repository = (*ApprovedActionRepository)(nil)
