package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/runneraction"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
)

type RunnerActionRepository struct{ pool *pgxpool.Pool }

func NewRunnerActionRepository(pool *pgxpool.Pool) (*RunnerActionRepository, error) {
	if pool == nil {
		return nil, errors.New("runner action pool is required")
	}
	return &RunnerActionRepository{pool: pool}, nil
}

func (r *RunnerActionRepository) Begin(ctx context.Context, command runneraction.BeginCommand) (runnercapability.ActionLease, error) {
	request := command.Request
	var lease runnercapability.ActionLease
	var mode string
	var digest []byte
	err := r.pool.QueryRow(ctx, `SELECT account_id,invocation_id,operation_id,attempt_id,capability,input_sha256,mode,idempotency_key,lease_expires_at
		FROM public.spyglass_begin_runner_action($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		request.AccountID, request.InvocationID, request.PodUID, request.OperationID, request.Capability, request.InputDigest[:],
		command.AttemptID, command.Now.UTC(), command.LeaseExpiresAt.UTC()).Scan(
		&lease.AccountID, &lease.InvocationID, &lease.OperationID, &lease.AttemptID, &lease.Capability, &digest,
		&mode, &lease.IdempotencyKey, &lease.LeaseExpiresAt)
	if err != nil {
		return runnercapability.ActionLease{}, runnerActionError(err)
	}
	if len(digest) != len(lease.InputDigest) {
		return runnercapability.ActionLease{}, runneraction.ErrStateConflict
	}
	copy(lease.InputDigest[:], digest)
	lease.Mode = runnercapability.ActionMode(mode)
	return lease, nil
}

func (r *RunnerActionRepository) Complete(ctx context.Context, completion runnercapability.ActionCompletion) error {
	lease := completion.Lease
	var errorCode *string
	if completion.ErrorCode != "" {
		errorCode = &completion.ErrorCode
	}
	_, err := r.pool.Exec(ctx, `SELECT public.spyglass_complete_runner_action($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		lease.AccountID, lease.InvocationID, lease.OperationID, lease.AttemptID, lease.Capability, lease.InputDigest[:],
		lease.Mode, lease.IdempotencyKey, lease.LeaseExpiresAt.UTC(), completion.Outcome, errorCode, completion.At.UTC())
	if err != nil {
		return runnerActionError(err)
	}
	return nil
}

func runnerActionError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return fmt.Errorf("%w: %v", runneraction.ErrRepository, err)
	}
	switch postgresError.Code {
	case "22023":
		return runneraction.ErrInvalidAction
	case "P2001":
		return runneraction.ErrApprovalRequired
	case "P2002":
		return runneraction.ErrApprovalExpired
	case "P2003":
		return runneraction.ErrActionBusy
	case "P2004":
		return runneraction.ErrActionDenied
	case "P2005", "23503", "23505", "23514":
		return runneraction.ErrStateConflict
	default:
		return fmt.Errorf("%w: database code %s", runneraction.ErrRepository, postgresError.Code)
	}
}

var _ runneraction.Repository = (*RunnerActionRepository)(nil)
