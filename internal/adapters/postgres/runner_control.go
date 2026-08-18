package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercontrol"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// RunnerControlQueue persists only execution routing identifiers. Its pool
// must be a dedicated technical role with no access to Account business data.
type RunnerControlQueue struct {
	pool *pgxpool.Pool
	ids  ids.Generator
}

func NewRunnerControlQueue(pool *pgxpool.Pool, generator ids.Generator) (*RunnerControlQueue, error) {
	if pool == nil || generator == nil {
		return nil, errors.New("runner control queue dependencies are required")
	}
	return &RunnerControlQueue{pool: pool, ids: generator}, nil
}

func (q *RunnerControlQueue) Configure(ctx context.Context, policy runnercontrol.AccountPolicy, now time.Time) error {
	_, err := q.pool.Exec(ctx, `SELECT public.spyglass_configure_runner_account($1,$2,$3,$4)`,
		policy.AccountID, policy.Weight, policy.ConcurrencyLimit, now.UTC())
	if err != nil {
		return fmt.Errorf("configure runner Account scheduling: %w", err)
	}
	return nil
}

func (q *RunnerControlQueue) Enqueue(ctx context.Context, invocation runnercontrol.Invocation) (bool, error) {
	var created bool
	err := q.pool.QueryRow(ctx, `SELECT public.spyglass_enqueue_runner_invocation($1,$2,$3,$4)`,
		invocation.ID, invocation.AccountID, invocation.Profile, invocation.QueuedAt.UTC()).Scan(&created)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.Message == "runner invocation identity conflicts with an existing record" {
			return false, runnercontrol.ErrInvocationConflict
		}
		return false, fmt.Errorf("enqueue runner invocation: %w", err)
	}
	return created, nil
}

func (q *RunnerControlQueue) RequestCancellation(ctx context.Context, accountID ids.AccountID, invocationID string, now time.Time) (string, error) {
	var state string
	err := q.pool.QueryRow(ctx, `SELECT public.spyglass_cancel_runner_invocation($1,$2,$3)`,
		invocationID, accountID, now.UTC()).Scan(&state)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "P0002" {
			return "", runnercontrol.ErrInvocationNotFound
		}
		return "", fmt.Errorf("request runner cancellation: %w", err)
	}
	return state, nil
}

func (q *RunnerControlQueue) ClaimFair(ctx context.Context, now time.Time, lease time.Duration) (runnercontrol.Invocation, bool, error) {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("begin runner claim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	leaseID := q.ids.New()
	invocation, found, err := claimExpiredRunner(ctx, tx, leaseID, now, lease)
	if err != nil {
		return runnercontrol.Invocation{}, false, err
	}
	if !found {
		invocation, found, err = claimFreshRunner(ctx, tx, leaseID, now, lease)
		if err != nil {
			return runnercontrol.Invocation{}, false, err
		}
	}
	if !found {
		return runnercontrol.Invocation{}, false, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("commit runner claim: %w", err)
	}
	return invocation, true, nil
}

func claimExpiredRunner(ctx context.Context, tx pgx.Tx, leaseID string, now time.Time, lease time.Duration) (runnercontrol.Invocation, bool, error) {
	var invocation runnercontrol.Invocation
	var leaseExpires time.Time
	err := tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT invocation_id
			FROM spyglass.runner_invocation_queue
			WHERE processing_state='launching' AND lease_expires_at<=$1
			ORDER BY lease_expires_at,invocation_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE spyglass.runner_invocation_queue q SET
			attempt_count=q.attempt_count+1,
			lease_id=$2,
			lease_expires_at=$1+($3::bigint*interval '1 millisecond'),
			last_attempt_at=$1,
			last_error_code=NULL
		FROM candidate c
		WHERE q.invocation_id=c.invocation_id
		RETURNING q.invocation_id,q.account_id,q.profile,q.processing_state,q.attempt_count,
			q.lease_id,q.lease_expires_at,q.queued_at`,
		now.UTC(), leaseID, lease.Milliseconds()).Scan(
		&invocation.ID, &invocation.AccountID, &invocation.Profile, &invocation.State,
		&invocation.AttemptCount, &invocation.LeaseID, &leaseExpires, &invocation.QueuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return runnercontrol.Invocation{}, false, nil
	}
	if err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("reclaim expired runner launch: %w", err)
	}
	invocation.LeaseExpiresAt = &leaseExpires
	return invocation, true, nil
}

func claimFreshRunner(ctx context.Context, tx pgx.Tx, leaseID string, now time.Time, lease time.Duration) (runnercontrol.Invocation, bool, error) {
	var accountID ids.AccountID
	err := tx.QueryRow(ctx, `
		SELECT scheduling.account_id
		FROM spyglass.runner_account_scheduling scheduling
		WHERE scheduling.active_count<scheduling.concurrency_limit
		  AND EXISTS (
			SELECT 1 FROM spyglass.runner_invocation_queue invocation
			WHERE invocation.account_id=scheduling.account_id
			  AND invocation.processing_state IN ('queued','failed')
			  AND invocation.next_attempt_at<=$1
		  )
		ORDER BY scheduling.virtual_finish,
			(SELECT min(invocation.queued_at) FROM spyglass.runner_invocation_queue invocation
			 WHERE invocation.account_id=scheduling.account_id
			   AND invocation.processing_state IN ('queued','failed')
			   AND invocation.next_attempt_at<=$1),
			scheduling.account_id
		FOR UPDATE SKIP LOCKED LIMIT 1`, now.UTC()).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return runnercontrol.Invocation{}, false, nil
	}
	if err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("select fair runner Account: %w", err)
	}

	var invocation runnercontrol.Invocation
	var leaseExpires time.Time
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT invocation_id
			FROM spyglass.runner_invocation_queue
			WHERE account_id=$1 AND processing_state IN ('queued','failed') AND next_attempt_at<=$2
			ORDER BY next_attempt_at,queued_at,invocation_id
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE spyglass.runner_invocation_queue q SET
			processing_state='launching',
			attempt_count=q.attempt_count+1,
			next_attempt_at=NULL,
			lease_id=$3,
			lease_expires_at=$2+($4::bigint*interval '1 millisecond'),
			last_attempt_at=$2,
			last_error_code=NULL
		FROM candidate c
		WHERE q.invocation_id=c.invocation_id
		RETURNING q.invocation_id,q.account_id,q.profile,q.processing_state,q.attempt_count,
			q.lease_id,q.lease_expires_at,q.queued_at`,
		accountID, now.UTC(), leaseID, lease.Milliseconds()).Scan(
		&invocation.ID, &invocation.AccountID, &invocation.Profile, &invocation.State,
		&invocation.AttemptCount, &invocation.LeaseID, &leaseExpires, &invocation.QueuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return runnercontrol.Invocation{}, false, nil
	}
	if err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("claim fair runner invocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE spyglass.runner_account_scheduling SET
			active_count=active_count+1,
			virtual_finish=virtual_finish+(1.0/weight),
			last_dispatched_at=$2,
			updated_at=$2
		WHERE account_id=$1`, accountID, now.UTC()); err != nil {
		return runnercontrol.Invocation{}, false, fmt.Errorf("advance runner Account schedule: %w", err)
	}
	invocation.LeaseExpiresAt = &leaseExpires
	return invocation, true, nil
}

func (q *RunnerControlQueue) MarkLaunched(ctx context.Context, invocation runnercontrol.Invocation, jobName string, now time.Time) error {
	result, err := q.pool.Exec(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state=CASE WHEN cancel_requested_at IS NULL THEN 'launched' ELSE 'canceling' END,
			lease_id=NULL,lease_expires_at=NULL,
			job_name=$3,launched_at=COALESCE(launched_at,$4),next_inspection_at=$4,last_error_code=NULL
		WHERE invocation_id=$1 AND processing_state='launching' AND lease_id=$2`,
		invocation.ID, invocation.LeaseID, jobName, now.UTC())
	if err != nil {
		return fmt.Errorf("mark runner launched: %w", err)
	}
	if result.RowsAffected() != 1 {
		return runnercontrol.ErrLeaseLost
	}
	return nil
}

func (q *RunnerControlQueue) MarkLaunchUncertain(ctx context.Context, invocation runnercontrol.Invocation, jobName string, now time.Time, code string) error {
	result, err := q.pool.Exec(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state=CASE WHEN cancel_requested_at IS NULL THEN 'launch_uncertain' ELSE 'canceling' END,
			lease_id=NULL,lease_expires_at=NULL,job_name=$3,next_inspection_at=$4,last_error_code=$5
		WHERE invocation_id=$1 AND processing_state='launching' AND lease_id=$2`,
		invocation.ID, invocation.LeaseID, jobName, now.UTC(), code)
	if err != nil {
		return fmt.Errorf("mark runner launch uncertain: %w", err)
	}
	if result.RowsAffected() != 1 {
		return runnercontrol.ErrLeaseLost
	}
	return nil
}

func (q *RunnerControlQueue) FailLaunch(ctx context.Context, invocation runnercontrol.Invocation, failedAt, next time.Time, code string, dead bool) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin runner launch failure: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state := "failed"
	var nextAttempt any = next.UTC()
	if dead {
		state = "dead_letter"
		nextAttempt = nil
	}
	var accountID ids.AccountID
	err = tx.QueryRow(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state=CASE WHEN cancel_requested_at IS NULL THEN $3::text ELSE 'canceled' END,
			next_attempt_at=CASE WHEN cancel_requested_at IS NULL THEN $4::timestamptz ELSE NULL END,
			completed_at=CASE WHEN cancel_requested_at IS NULL THEN NULL ELSE $5::timestamptz END,
			lease_id=NULL,lease_expires_at=NULL,
			last_error_code=CASE WHEN cancel_requested_at IS NULL THEN $6::text ELSE NULL END
		WHERE invocation_id=$1 AND processing_state='launching' AND lease_id=$2
		RETURNING account_id`, invocation.ID, invocation.LeaseID, state, nextAttempt, failedAt.UTC(), code).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var current string
		inspectErr := tx.QueryRow(ctx, `SELECT processing_state FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, invocation.ID).Scan(&current)
		if inspectErr == nil && (current == "failed" || current == "dead_letter" || current == "canceled") {
			return nil
		}
		if inspectErr != nil && !errors.Is(inspectErr, pgx.ErrNoRows) {
			return fmt.Errorf("inspect runner launch lease: %w", inspectErr)
		}
		return runnercontrol.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("fail runner launch: %w", err)
	}
	if err := decrementRunnerActive(ctx, tx, accountID, failedAt.UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit runner launch failure: %w", err)
	}
	return nil
}

func (q *RunnerControlQueue) ConfirmLaunch(ctx context.Context, invocation runnercontrol.Invocation, now time.Time) error {
	result, err := q.pool.Exec(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state='launched',launched_at=COALESCE(launched_at,$3),last_error_code=NULL
		WHERE invocation_id=$1 AND processing_state='launch_uncertain' AND job_name=$2`,
		invocation.ID, invocation.JobName, now.UTC())
	if err != nil {
		return fmt.Errorf("confirm uncertain runner launch: %w", err)
	}
	if result.RowsAffected() == 1 {
		return nil
	}
	var current, currentJob string
	if err := q.pool.QueryRow(ctx, `SELECT processing_state,job_name FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, invocation.ID).Scan(&current, &currentJob); err == nil && current == "launched" && currentJob == invocation.JobName {
		return nil
	}
	return runnercontrol.ErrLeaseLost
}

func (q *RunnerControlQueue) ResolveLaunchAbsent(ctx context.Context, invocation runnercontrol.Invocation, failedAt, next time.Time, code string, dead bool) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin absent runner launch resolution: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state := "failed"
	var nextAttempt any = next.UTC()
	if dead {
		state = "dead_letter"
		nextAttempt = nil
	}
	var accountID ids.AccountID
	err = tx.QueryRow(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state=$3,next_attempt_at=$4::timestamptz,job_name=NULL,
			next_inspection_at=NULL,last_error_code=$5
		WHERE invocation_id=$1 AND processing_state='launch_uncertain' AND job_name=$2
		RETURNING account_id`, invocation.ID, invocation.JobName, state, nextAttempt, code).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var current string
		inspectErr := tx.QueryRow(ctx, `SELECT processing_state FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, invocation.ID).Scan(&current)
		if inspectErr == nil && (current == "failed" || current == "dead_letter") {
			return nil
		}
		if inspectErr != nil && !errors.Is(inspectErr, pgx.ErrNoRows) {
			return fmt.Errorf("inspect absent runner launch resolution: %w", inspectErr)
		}
		return runnercontrol.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("resolve absent runner launch: %w", err)
	}
	if err := decrementRunnerActive(ctx, tx, accountID, failedAt.UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit absent runner launch resolution: %w", err)
	}
	return nil
}

func (q *RunnerControlQueue) Complete(ctx context.Context, invocationID, jobName, outcome string, now time.Time) error {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin runner completion: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var accountID ids.AccountID
	err = tx.QueryRow(ctx, `
		UPDATE spyglass.runner_invocation_queue SET
			processing_state=$3,completed_at=$4,next_inspection_at=NULL,last_error_code=NULL
		WHERE invocation_id=$1 AND job_name=$2 AND (
			(processing_state IN ('launch_uncertain','launched') AND $3 IN ('completed','execution_failed')) OR
			(processing_state='canceling' AND $3='canceled')
		)
		RETURNING account_id`, invocationID, jobName, outcome, now.UTC()).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		var current, currentJob string
		inspectErr := tx.QueryRow(ctx, `SELECT processing_state,job_name FROM spyglass.runner_invocation_queue WHERE invocation_id=$1`, invocationID).Scan(&current, &currentJob)
		if inspectErr == nil && current == outcome && currentJob == jobName {
			return nil
		}
		if inspectErr != nil && !errors.Is(inspectErr, pgx.ErrNoRows) {
			return fmt.Errorf("inspect runner completion: %w", inspectErr)
		}
		return runnercontrol.ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("complete runner invocation: %w", err)
	}
	if err := decrementRunnerActive(ctx, tx, accountID, now.UTC()); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit runner completion: %w", err)
	}
	return nil
}

func (q *RunnerControlQueue) ClaimReconciliationCandidates(ctx context.Context, now time.Time, interval time.Duration, limit int) ([]runnercontrol.Invocation, error) {
	rows, err := q.pool.Query(ctx, `
		WITH candidates AS (
			SELECT invocation_id
			FROM spyglass.runner_invocation_queue
			WHERE processing_state IN ('launch_uncertain','launched','canceling') AND next_inspection_at<=$1
			ORDER BY next_inspection_at,COALESCE(launched_at,last_attempt_at),invocation_id
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE spyglass.runner_invocation_queue q SET
			next_inspection_at=$1+($3::bigint*interval '1 millisecond')
		FROM candidates c WHERE q.invocation_id=c.invocation_id
		RETURNING q.invocation_id,q.account_id,q.profile,q.processing_state,q.attempt_count,q.job_name,q.queued_at,q.launched_at,q.cancel_requested_at`,
		now.UTC(), limit, interval.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claim runner reconciliation candidates: %w", err)
	}
	defer rows.Close()
	result := make([]runnercontrol.Invocation, 0)
	for rows.Next() {
		var invocation runnercontrol.Invocation
		if err := rows.Scan(&invocation.ID, &invocation.AccountID, &invocation.Profile, &invocation.State, &invocation.AttemptCount, &invocation.JobName, &invocation.QueuedAt, &invocation.LaunchedAt, &invocation.CancelRequestedAt); err != nil {
			return nil, fmt.Errorf("scan runner reconciliation candidate: %w", err)
		}
		result = append(result, invocation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runner reconciliation candidates: %w", err)
	}
	return result, nil
}

func decrementRunnerActive(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, now time.Time) error {
	result, err := tx.Exec(ctx, `
		UPDATE spyglass.runner_account_scheduling SET active_count=active_count-1,updated_at=$2
		WHERE account_id=$1 AND active_count>0`, accountID, now.UTC())
	if err != nil {
		return fmt.Errorf("release runner Account capacity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errors.New("runner Account active count is inconsistent")
	}
	return nil
}

func (q *RunnerControlQueue) Stats(ctx context.Context, now time.Time) (runnercontrol.Stats, error) {
	var stats runnercontrol.Stats
	var oldest *time.Time
	err := q.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE processing_state IN ('queued','failed') AND next_attempt_at<=$1),
		count(*) FILTER (WHERE processing_state='launching'),
		count(*) FILTER (WHERE processing_state='launch_uncertain'),
		count(*) FILTER (WHERE processing_state='launched'),
		count(*) FILTER (WHERE processing_state='canceling'),
		count(*) FILTER (WHERE processing_state='failed'),
		count(*) FILTER (WHERE processing_state='dead_letter'),
		min(queued_at) FILTER (WHERE processing_state IN ('queued','failed') AND next_attempt_at<=$1)
		FROM spyglass.runner_invocation_queue`, now.UTC()).Scan(
		&stats.Ready, &stats.Launching, &stats.LaunchUncertain, &stats.Launched, &stats.Canceling, &stats.Failed, &stats.DeadLetter, &oldest)
	if err != nil {
		return runnercontrol.Stats{}, fmt.Errorf("read runner control stats: %w", err)
	}
	if oldest != nil && now.After(*oldest) {
		stats.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return stats, nil
}

var _ runnercontrol.Queue = (*RunnerControlQueue)(nil)
