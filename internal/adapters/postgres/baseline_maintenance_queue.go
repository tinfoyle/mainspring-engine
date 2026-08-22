package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/baselinemaintenance"
)

type BaselineMaintenanceQueue struct{ pool *pgxpool.Pool }

func NewBaselineMaintenanceQueue(pool *pgxpool.Pool) (*BaselineMaintenanceQueue, error) {
	if pool == nil {
		return nil, baselinemaintenance.ErrDependencies
	}
	return &BaselineMaintenanceQueue{pool: pool}, nil
}

func (queue *BaselineMaintenanceQueue) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (baselinemaintenance.Claim, bool, error) {
	var claim baselinemaintenance.Claim
	err := queue.pool.QueryRow(ctx, `SELECT account_id,assessment_id,assessment_version,scheduled_for,lease_id,attempt_count
		FROM public.spyglass_claim_baseline_maintenance($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.AccountID, &claim.AssessmentID, &claim.AssessmentVersion, &claim.ScheduledFor, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return baselinemaintenance.Claim{}, false, nil
	}
	if err != nil {
		return baselinemaintenance.Claim{}, false, classifyBaselineMaintenanceQueue("claim", err)
	}
	return claim, true, nil
}

func (queue *BaselineMaintenanceQueue) Complete(ctx context.Context, claim baselinemaintenance.Claim, now time.Time) error {
	var completed bool
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_complete_baseline_maintenance($1,$2,$3,$4,$5)`, claim.AccountID, claim.AssessmentID, claim.LeaseID, claim.ScheduledFor, now.UTC()).Scan(&completed)
	if err != nil {
		return classifyBaselineMaintenanceQueue("complete", err)
	}
	_ = completed
	return nil
}

func (queue *BaselineMaintenanceQueue) Fail(ctx context.Context, claim baselinemaintenance.Claim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := queue.pool.QueryRow(ctx, `SELECT public.spyglass_fail_baseline_maintenance($1,$2,$3,$4,$5,$6,$7,$8,$9)`, claim.AccountID, claim.AssessmentID, claim.LeaseID, claim.ScheduledFor, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", classifyBaselineMaintenanceQueue("fail", err)
	}
	return state, nil
}

func (queue *BaselineMaintenanceQueue) Stats(ctx context.Context, now time.Time) (baselinemaintenance.Stats, error) {
	var result baselinemaintenance.Stats
	var oldest *time.Time
	err := queue.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,dead_letter,oldest_ready_at FROM public.spyglass_baseline_maintenance_stats($1)`, now.UTC()).Scan(&result.Pending, &result.Ready, &result.Leased, &result.Retrying, &result.DeadLetter, &oldest)
	if err != nil {
		return baselinemaintenance.Stats{}, classifyBaselineMaintenanceQueue("stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func classifyBaselineMaintenanceQueue(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch {
		case postgresError.Code == "P0001":
			return baselinemaintenance.ErrLease
		case postgresError.Code == "22023":
			return baselinemaintenance.ErrClaim
		}
	}
	return fmt.Errorf("%s Baseline maintenance: %w", operation, err)
}

var _ baselinemaintenance.Queue = (*BaselineMaintenanceQueue)(nil)
