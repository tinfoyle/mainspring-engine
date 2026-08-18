package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/workreconciliation"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// WorkReleaseQueueRepository leases only the identifier-only technical
// outbox through the raw pool. Customer Work updates still use CellPool and
// transaction-local Account RLS.
type WorkReleaseQueueRepository struct {
	pool *pgxpool.Pool
	cell *database.CellPool
	ids  ids.Generator
}

func NewWorkReleaseQueueRepository(pool *pgxpool.Pool, cell *database.CellPool, generator ids.Generator) (*WorkReleaseQueueRepository, error) {
	if pool == nil || cell == nil || generator == nil {
		return nil, errors.New("Work release queue dependencies are required")
	}
	return &WorkReleaseQueueRepository{pool: pool, cell: cell, ids: generator}, nil
}

func (r *WorkReleaseQueueRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (workreconciliation.Job, bool, error) {
	leaseID := r.ids.New()
	var job workreconciliation.Job
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT account_id,work_item_id,reservation_id
			FROM spyglass.work_capacity_release_queue
			WHERE (processing_state IN ('pending','failed') AND next_attempt_at<=$1)
			   OR (processing_state='processing' AND lease_expires_at<=$1)
			ORDER BY COALESCE(next_attempt_at,lease_expires_at),queued_at,account_id,work_item_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE spyglass.work_capacity_release_queue q SET
			processing_state='processing',attempt_count=q.attempt_count+1,next_attempt_at=NULL,
			lease_id=$2,lease_expires_at=$1+($3*interval '1 second'),last_attempt_at=$1,last_error_code=NULL
		FROM candidate c
		WHERE q.account_id=c.account_id AND q.work_item_id=c.work_item_id AND q.reservation_id=c.reservation_id
		RETURNING q.account_id,q.work_item_id,q.reservation_id,q.lease_id,q.attempt_count,q.queued_at`,
		now.UTC(), leaseID, int64(lease/time.Second)).Scan(&job.AccountID, &job.WorkItemID, &job.ReservationID, &job.LeaseID, &job.Attempt, &job.QueuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return workreconciliation.Job{}, false, nil
	}
	if err != nil {
		return workreconciliation.Job{}, false, fmt.Errorf("claim Work capacity release: %w", err)
	}
	return job, true, nil
}

func (r *WorkReleaseQueueRepository) Complete(ctx context.Context, job workreconciliation.Job, now time.Time) error {
	err := r.cell.WithAccountTx(ctx, job.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `
			UPDATE spyglass.work_capacity_release_queue SET
			processing_state='completed',completed_at=$5,next_attempt_at=NULL,lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL
			WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3 AND processing_state='processing' AND lease_id=$4`,
			job.AccountID, job.WorkItemID, job.ReservationID, job.LeaseID, now.UTC())
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			var state string
			err := tx.QueryRow(ctx, `SELECT processing_state FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, job.AccountID, job.WorkItemID, job.ReservationID).Scan(&state)
			if err == nil && state == "completed" {
				return nil
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			return workreconciliation.ErrLeaseLost
		}
		_, err = tx.Exec(ctx, `
			UPDATE spyglass.work_items SET capacity_released_at=COALESCE(capacity_released_at,$4)
			WHERE account_id=$1 AND id=$2 AND capacity_reservation_id=$3 AND state IN ('done','canceled')`,
			job.AccountID, job.WorkItemID, job.ReservationID, now.UTC())
		return err
	})
	if err != nil {
		return fmt.Errorf("complete Work capacity release: %w", err)
	}
	return nil
}

func (r *WorkReleaseQueueRepository) Fail(ctx context.Context, job workreconciliation.Job, next time.Time, code string, dead bool) error {
	state := "failed"
	var nextAttempt any = next.UTC()
	if dead {
		state, nextAttempt = "dead_letter", nil
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE spyglass.work_capacity_release_queue SET
			processing_state=$5,next_attempt_at=$6,lease_id=NULL,lease_expires_at=NULL,last_error_code=$7
		WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3 AND processing_state='processing' AND lease_id=$4`,
		job.AccountID, job.WorkItemID, job.ReservationID, job.LeaseID, state, nextAttempt, code)
	if err != nil {
		return fmt.Errorf("fail Work capacity release: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var current string
	err = r.pool.QueryRow(ctx, `SELECT processing_state FROM spyglass.work_capacity_release_queue WHERE account_id=$1 AND work_item_id=$2 AND reservation_id=$3`, job.AccountID, job.WorkItemID, job.ReservationID).Scan(&current)
	if err == nil && (current == "completed" || current == "dead_letter") {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("inspect Work capacity release lease: %w", err)
	}
	return workreconciliation.ErrLeaseLost
}

func (r *WorkReleaseQueueRepository) Stats(ctx context.Context, now time.Time) (workreconciliation.Stats, error) {
	var stats workreconciliation.Stats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE processing_state IN ('pending','failed')),
		count(*) FILTER (WHERE processing_state='processing'),
		count(*) FILTER (WHERE processing_state='dead_letter'),
		min(queued_at) FILTER (WHERE processing_state IN ('pending','failed','processing'))
		FROM spyglass.work_capacity_release_queue`).Scan(&stats.Pending, &stats.Processing, &stats.DeadLetter, &oldest)
	if err != nil {
		return workreconciliation.Stats{}, fmt.Errorf("read Work capacity release stats: %w", err)
	}
	if oldest != nil && now.After(*oldest) {
		stats.OldestPendingAge = now.Sub(*oldest).Round(time.Second)
	}
	return stats, nil
}

func (r *WorkReleaseQueueRepository) PruneCompleted(ctx context.Context, before time.Time, limit int) (int64, error) {
	result, err := r.pool.Exec(ctx, `
		WITH candidates AS (
			SELECT account_id,work_item_id,reservation_id
			FROM spyglass.work_capacity_release_queue
			WHERE processing_state='completed' AND completed_at<=$1
			ORDER BY completed_at,account_id,work_item_id,reservation_id
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		DELETE FROM spyglass.work_capacity_release_queue q
		USING candidates c
		WHERE q.account_id=c.account_id AND q.work_item_id=c.work_item_id AND q.reservation_id=c.reservation_id`, before.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("prune completed Work capacity releases: %w", err)
	}
	return result.RowsAffected(), nil
}

var _ workreconciliation.Queue = (*WorkReleaseQueueRepository)(nil)
