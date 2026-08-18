package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnerbroker"
	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
)

// RunnerBrokerRepository crosses the database boundary only through audited
// SECURITY DEFINER functions. Its pool must not have direct table privileges.
type RunnerBrokerRepository struct{ pool *pgxpool.Pool }

func NewRunnerBrokerRepository(pool *pgxpool.Pool) (*RunnerBrokerRepository, error) {
	if pool == nil {
		return nil, errors.New("runner broker database pool is required")
	}
	return &RunnerBrokerRepository{pool: pool}, nil
}

func (r *RunnerBrokerRepository) Provision(ctx context.Context, invocation runnercontrol.Invocation, request runnerbroker.StoredRequest) (bool, error) {
	var created bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_provision_runner_invocation($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		invocation.ID, invocation.AccountID, invocation.Profile, invocation.QueuedAt.UTC(), request.Ciphertext,
		request.Nonce, request.KeyVersion, request.Digest[:], request.ExpiresAt.UTC()).Scan(&created)
	if err != nil {
		return false, mapRunnerBrokerError("provision runner exchange", err)
	}
	return created, nil
}

func (r *RunnerBrokerRepository) Claim(ctx context.Context, identity runnerbroker.Identity, now time.Time) (runnerbroker.StoredRequest, error) {
	var request runnerbroker.StoredRequest
	var digest []byte
	err := r.pool.QueryRow(ctx, `SELECT invocation_id,account_id,profile,request_ciphertext,request_nonce,
		request_key_version,request_digest,created_at,request_expires_at
		FROM public.spyglass_claim_runner_exchange($1,$2,$3,$4,$5)`,
		identity.InvocationID, identity.PodUID, identity.Profile, identity.JobName, now.UTC()).Scan(
		&request.InvocationID, &request.AccountID, &request.Profile, &request.Ciphertext, &request.Nonce,
		&request.KeyVersion, &digest, &request.CreatedAt, &request.ExpiresAt)
	if err != nil {
		return runnerbroker.StoredRequest{}, mapRunnerBrokerError("claim runner exchange", err)
	}
	if len(digest) != len(request.Digest) {
		return runnerbroker.StoredRequest{}, runnerbroker.ErrExchangeConflict
	}
	copy(request.Digest[:], digest)
	return request, nil
}

func (r *RunnerBrokerRepository) Submit(ctx context.Context, identity runnerbroker.Identity, result runnerbroker.StoredResult, now time.Time) (bool, error) {
	var created bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_submit_runner_result($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		identity.InvocationID, identity.PodUID, identity.Profile, identity.JobName, result.Outcome,
		result.Ciphertext, result.Nonce, result.KeyVersion, result.Digest[:], now.UTC()).Scan(&created)
	if err != nil {
		return false, mapRunnerBrokerError("submit runner result", err)
	}
	return created, nil
}

func mapRunnerBrokerError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	switch {
	case postgresError.Code == "23505":
		return runnerbroker.ErrExchangeConflict
	case postgresError.Code == "22023":
		return runnerbroker.ErrInvalidExchange
	case postgresError.Code == "P0002":
		return runnerbroker.ErrIdentityDenied
	case postgresError.Code == "P0001" && postgresError.Message == "runner exchange canceled":
		return runnerbroker.ErrExchangeCanceled
	case postgresError.Code == "P0001" && postgresError.Message == "runner exchange expired":
		return runnerbroker.ErrExchangeExpired
	case postgresError.Code == "P0001" && postgresError.Message == "runner exchange not ready":
		return runnerbroker.ErrExchangeNotReady
	case postgresError.Code == "P0001" && (postgresError.Message == "runner exchange Pod conflict" || postgresError.Message == "runner exchange fetch limit reached"):
		return runnerbroker.ErrIdentityDenied
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}

var _ runnerbroker.Repository = (*RunnerBrokerRepository)(nil)
