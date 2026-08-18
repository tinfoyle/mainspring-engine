package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/routeretention"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// RouteReceiptCleanupRepository coordinates through an identifier-only queue.
// Receipt reads/deletes always enter an Account-scoped RLS transaction.
type RouteReceiptCleanupRepository struct {
	pool *pgxpool.Pool
	cell *database.CellPool
	ids  ids.Generator
}

func NewRouteReceiptCleanupRepository(pool *pgxpool.Pool, cell *database.CellPool, generator ids.Generator) (*RouteReceiptCleanupRepository, error) {
	if pool == nil || cell == nil || generator == nil {
		return nil, errors.New("route receipt cleanup dependencies are required")
	}
	return &RouteReceiptCleanupRepository{pool: pool, cell: cell, ids: generator}, nil
}

func (r *RouteReceiptCleanupRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (routeretention.Job, bool, error) {
	var job routeretention.Job
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT account_id
			FROM spyglass.route_context_receipt_cleanup_queue
			WHERE (lease_id IS NULL AND next_cleanup_at<=$1)
			   OR (lease_id IS NOT NULL AND lease_expires_at<=$1)
			ORDER BY COALESCE(lease_expires_at,next_cleanup_at),account_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE spyglass.route_context_receipt_cleanup_queue q SET
			lease_id=$2,lease_expires_at=$1+($3*interval '1 second'),
			attempt_count=q.attempt_count+1,last_attempt_at=$1,last_error_code=NULL,updated_at=$1
		FROM candidate c WHERE q.account_id=c.account_id
		RETURNING q.account_id,q.lease_id,q.attempt_count,q.schedule_version,q.next_cleanup_at`,
		now.UTC(), r.ids.New(), int64(lease/time.Second)).Scan(
		&job.AccountID, &job.LeaseID, &job.Attempt, &job.ScheduleVersion, &job.DueAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return routeretention.Job{}, false, nil
	}
	if err != nil {
		return routeretention.Job{}, false, fmt.Errorf("claim route receipt cleanup: %w", err)
	}
	return job, true, nil
}

func (r *RouteReceiptCleanupRepository) Prune(ctx context.Context, job routeretention.Job, now time.Time, retention time.Duration, limit int) (int64, error) {
	var pruned int64
	var earliest *time.Time
	cutoff := now.UTC().Add(-retention)
	err := r.cell.WithAccountTx(ctx, job.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `
			WITH candidates AS (
				SELECT request_id
				FROM spyglass.route_context_receipts
				WHERE account_id=$1 AND expires_at<=$2
				ORDER BY expires_at,request_id
				FOR UPDATE SKIP LOCKED LIMIT $3
			)
			DELETE FROM spyglass.route_context_receipts r
			USING candidates c
			WHERE r.account_id=$1 AND r.request_id=c.request_id`, job.AccountID, cutoff, limit)
		if err != nil {
			return err
		}
		pruned = result.RowsAffected()
		return tx.QueryRow(ctx, `SELECT min(expires_at) FROM spyglass.route_context_receipts WHERE account_id=$1`, job.AccountID).Scan(&earliest)
	})
	if err != nil {
		return 0, fmt.Errorf("delete Account route receipts: %w", err)
	}
	if err := r.acknowledge(ctx, job, now.UTC(), retention, limit, pruned, earliest); err != nil {
		return pruned, err
	}
	return pruned, nil
}

func (r *RouteReceiptCleanupRepository) acknowledge(ctx context.Context, job routeretention.Job, now time.Time, retention time.Duration, limit int, pruned int64, earliest *time.Time) error {
	if earliest == nil && pruned < int64(limit) {
		result, err := r.pool.Exec(ctx, `DELETE FROM spyglass.route_context_receipt_cleanup_queue
			WHERE account_id=$1 AND lease_id=$2 AND schedule_version=$3`, job.AccountID, job.LeaseID, job.ScheduleVersion)
		if err != nil {
			return fmt.Errorf("complete empty route receipt cleanup: %w", err)
		}
		if result.RowsAffected() == 1 {
			return nil
		}
		// A concurrent receipt advanced the schedule version. Preserve that new
		// work and release this lease for an immediate fresh inspection.
		result, err = r.pool.Exec(ctx, `UPDATE spyglass.route_context_receipt_cleanup_queue SET
			next_cleanup_at=LEAST(next_cleanup_at,$3),lease_id=NULL,lease_expires_at=NULL,
			attempt_count=0,last_error_code=NULL,updated_at=$3
			WHERE account_id=$1 AND lease_id=$2`, job.AccountID, job.LeaseID, now)
		if err != nil {
			return fmt.Errorf("reschedule concurrent route receipt cleanup: %w", err)
		}
		if result.RowsAffected() == 1 {
			return nil
		}
		return routeretention.ErrLeaseLost
	}
	next := now
	if pruned < int64(limit) && earliest != nil {
		next = earliest.UTC().Add(retention)
	}
	result, err := r.pool.Exec(ctx, `UPDATE spyglass.route_context_receipt_cleanup_queue SET
		next_cleanup_at=CASE WHEN schedule_version=$3 THEN $4 ELSE LEAST(next_cleanup_at,$5) END,
		lease_id=NULL,lease_expires_at=NULL,attempt_count=0,last_error_code=NULL,updated_at=$5
		WHERE account_id=$1 AND lease_id=$2`, job.AccountID, job.LeaseID, job.ScheduleVersion, next, now)
	if err != nil {
		return fmt.Errorf("complete route receipt cleanup: %w", err)
	}
	if result.RowsAffected() != 1 {
		return routeretention.ErrLeaseLost
	}
	return nil
}

func (r *RouteReceiptCleanupRepository) Fail(ctx context.Context, job routeretention.Job, next time.Time, code string) error {
	result, err := r.pool.Exec(ctx, `UPDATE spyglass.route_context_receipt_cleanup_queue SET
		next_cleanup_at=CASE WHEN schedule_version=$3 THEN $4 ELSE LEAST(next_cleanup_at,$4) END,
		lease_id=NULL,lease_expires_at=NULL,last_error_code=$5,updated_at=statement_timestamp()
		WHERE account_id=$1 AND lease_id=$2`, job.AccountID, job.LeaseID, job.ScheduleVersion, next.UTC(), code)
	if err != nil {
		return fmt.Errorf("fail route receipt cleanup: %w", err)
	}
	if result.RowsAffected() != 1 {
		return routeretention.ErrLeaseLost
	}
	return nil
}

func (r *RouteReceiptCleanupRepository) Stats(ctx context.Context, now time.Time) (routeretention.Stats, error) {
	var stats routeretention.Stats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT
		count(*),
		count(*) FILTER (WHERE (lease_id IS NULL AND next_cleanup_at<=$1) OR (lease_id IS NOT NULL AND lease_expires_at<=$1)),
		count(*) FILTER (WHERE lease_id IS NOT NULL AND lease_expires_at>$1),
		count(*) FILTER (WHERE last_error_code IS NOT NULL),
		min(next_cleanup_at) FILTER (WHERE next_cleanup_at<=$1)
		FROM spyglass.route_context_receipt_cleanup_queue`, now.UTC()).Scan(
		&stats.Scheduled, &stats.Ready, &stats.Leased, &stats.Retrying, &oldest)
	if err != nil {
		return routeretention.Stats{}, fmt.Errorf("read route receipt cleanup stats: %w", err)
	}
	if oldest != nil && now.After(*oldest) {
		stats.OldestDueAge = now.Sub(*oldest).Round(time.Second)
	}
	return stats, nil
}

var _ routeretention.Queue = (*RouteReceiptCleanupRepository)(nil)
