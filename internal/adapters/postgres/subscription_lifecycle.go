package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/subscriptionlifecycle"
)

type SubscriptionLifecycleRepository struct{ pool *pgxpool.Pool }

func NewSubscriptionLifecycleRepository(pool *pgxpool.Pool) *SubscriptionLifecycleRepository {
	return &SubscriptionLifecycleRepository{pool: pool}
}

func (r *SubscriptionLifecycleRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (subscriptionlifecycle.Work, bool, error) {
	var work subscriptionlifecycle.Work
	err := r.pool.QueryRow(ctx, `SELECT lifecycle_id,account_id,state,trigger_kind,effective_at,restriction_at,delete_at,lease_id
		FROM spyglass_claim_subscription_lifecycle($1,$2)`, now.UTC(), int64(lease/time.Second)).Scan(
		&work.LifecycleID, &work.AccountID, &work.State, &work.Trigger, &work.EffectiveAt, &work.RestrictionAt, &work.DeleteAt, &work.LeaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscriptionlifecycle.Work{}, false, nil
	}
	return work, err == nil, err
}

func (r *SubscriptionLifecycleRepository) Advance(ctx context.Context, work subscriptionlifecycle.Work, now time.Time, retry time.Duration) (subscriptionlifecycle.State, error) {
	var state subscriptionlifecycle.State
	err := r.pool.QueryRow(ctx, `SELECT spyglass_advance_subscription_lifecycle($1,$2,$3,$4)`, work.LifecycleID, work.LeaseID, now.UTC(), int64(retry/time.Second)).Scan(&state)
	return state, err
}

func (r *SubscriptionLifecycleRepository) ClaimNotice(ctx context.Context, now time.Time, lease time.Duration) (subscriptionlifecycle.Notice, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return subscriptionlifecycle.Notice{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var notice subscriptionlifecycle.Notice
	err = tx.QueryRow(ctx, `
		WITH candidate AS (
			SELECT notice_id FROM account_subscription_lifecycle_notices
			WHERE (state IN ('scheduled','failed') AND next_attempt_at<=$1)
			   OR (state='processing' AND lease_expires_at<=$1)
			ORDER BY next_attempt_at,notice_id FOR UPDATE SKIP LOCKED LIMIT 1
		), claimed AS (
			UPDATE account_subscription_lifecycle_notices notice
			SET state='processing',attempt_count=notice.attempt_count+1,lease_id=gen_random_uuid(),
			    lease_expires_at=$1+($2*interval '1 second'),last_error_code=NULL
			FROM candidate WHERE notice.notice_id=candidate.notice_id RETURNING notice.*
		)
		SELECT claimed.notice_id,lifecycle.account_id,account.display_name,claimed.kind,claimed.due_at,
		       lifecycle.delete_at,claimed.lease_id,claimed.attempt_count
		FROM claimed JOIN account_subscription_lifecycles lifecycle USING (lifecycle_id)
		JOIN accounts account ON account.id=lifecycle.account_id`, now.UTC(), int64(lease/time.Second)).Scan(
		&notice.NoticeID, &notice.AccountID, &notice.AccountName, &notice.Kind, &notice.DueAt,
		&notice.DeleteAt, &notice.LeaseID, &notice.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscriptionlifecycle.Notice{}, false, nil
	}
	if err != nil {
		return subscriptionlifecycle.Notice{}, false, err
	}
	rows, err := tx.Query(ctx, `
		SELECT user_account.primary_email,user_account.display_name
		FROM memberships membership JOIN users user_account ON user_account.id=membership.user_id
		WHERE membership.account_id=$1 AND membership.role='owner' AND membership.state='active'
		  AND user_account.state='active' ORDER BY user_account.id`, notice.AccountID)
	if err != nil {
		return subscriptionlifecycle.Notice{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var recipient subscriptionlifecycle.Recipient
		if err := rows.Scan(&recipient.Email, &recipient.DisplayName); err != nil {
			return subscriptionlifecycle.Notice{}, false, err
		}
		notice.Recipients = append(notice.Recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return subscriptionlifecycle.Notice{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return subscriptionlifecycle.Notice{}, false, err
	}
	return notice, true, nil
}

func (r *SubscriptionLifecycleRepository) EmitNotice(ctx context.Context, notice subscriptionlifecycle.Notice, prepared []subscriptionlifecycle.PreparedNotification, now time.Time) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_subscription_lifecycle_notices WHERE notice_id=$1 AND state='processing' AND lease_id=$2 AND lease_expires_at>$3 FOR UPDATE)`, notice.NoticeID, notice.LeaseID, now.UTC()).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return errors.New("subscription notice lease was lost")
	}
	for _, item := range prepared {
		if item.AccountID != notice.AccountID {
			return errors.New("subscription notice Account changed")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity_notification_outbox
			(id,account_id,kind,ciphertext,nonce,key_version,processing_state,attempt_count,created_at)
			VALUES ($1,$2,'subscription_lifecycle',$3,$4,$5,'queued',0,$6)`, item.ID, item.AccountID, item.Ciphertext, item.Nonce, item.KeyVersion, item.CreatedAt.UTC()); err != nil {
			return err
		}
	}
	command, err := tx.Exec(ctx, `UPDATE account_subscription_lifecycle_notices SET state='emitted',emitted_at=$3,lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL WHERE notice_id=$1 AND state='processing' AND lease_id=$2`, notice.NoticeID, notice.LeaseID, now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("subscription notice lease was lost")
	}
	return tx.Commit(ctx)
}

func (r *SubscriptionLifecycleRepository) FailNotice(ctx context.Context, notice subscriptionlifecycle.Notice, next time.Time, code string, terminal bool) error {
	state := "failed"
	if terminal {
		state = "dead_letter"
	}
	command, err := r.pool.Exec(ctx, `UPDATE account_subscription_lifecycle_notices SET state=$3,next_attempt_at=$4,lease_id=NULL,lease_expires_at=NULL,last_error_code=$5 WHERE notice_id=$1 AND state='processing' AND lease_id=$2`, notice.NoticeID, notice.LeaseID, state, next.UTC(), code)
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("subscription notice lease was lost")
	}
	return err
}

func (r *SubscriptionLifecycleRepository) ClaimTermination(ctx context.Context, now time.Time, lease time.Duration) (subscriptionlifecycle.Termination, bool, error) {
	var work subscriptionlifecycle.Termination
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT lifecycle_id FROM account_subscription_termination_jobs
			WHERE (state IN ('pending','failed') AND next_attempt_at<=$1) OR (state='processing' AND lease_expires_at<=$1)
			ORDER BY next_attempt_at,lifecycle_id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE account_subscription_termination_jobs job
		SET state='processing',attempt_count=job.attempt_count+1,lease_id=gen_random_uuid(),
		    lease_expires_at=$1+($2*interval '1 second'),last_error_code=NULL
		FROM candidate WHERE job.lifecycle_id=candidate.lifecycle_id
		RETURNING job.lifecycle_id,job.provider_subscription_id,job.lease_id,job.attempt_count`, now.UTC(), int64(lease/time.Second)).Scan(
		&work.LifecycleID, &work.ProviderSubscriptionID, &work.LeaseID, &work.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return subscriptionlifecycle.Termination{}, false, nil
	}
	return work, err == nil, err
}

func (r *SubscriptionLifecycleRepository) CompleteTermination(ctx context.Context, work subscriptionlifecycle.Termination, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE account_subscription_termination_jobs SET state='completed',completed_at=$3,lease_id=NULL,lease_expires_at=NULL,last_error_code=NULL WHERE lifecycle_id=$1 AND state='processing' AND lease_id=$2`, work.LifecycleID, work.LeaseID, now.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return errors.New("subscription termination lease was lost")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO billing_reconciliation_queue(provider_subscription_id,reason,requested_at,next_attempt_at)
		VALUES ($1,'subscription lifecycle termination',$2,$2)
		ON CONFLICT (provider_subscription_id) DO UPDATE SET
			reason=EXCLUDED.reason,requested_at=EXCLUDED.requested_at,
			next_attempt_at=LEAST(billing_reconciliation_queue.next_attempt_at,EXCLUDED.next_attempt_at),
			processing_state='pending',completed_at=NULL`, work.ProviderSubscriptionID, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *SubscriptionLifecycleRepository) FailTermination(ctx context.Context, work subscriptionlifecycle.Termination, next time.Time, code string, terminal bool) error {
	state := "failed"
	if terminal {
		state = "dead_letter"
	}
	command, err := r.pool.Exec(ctx, `UPDATE account_subscription_termination_jobs SET state=$3,next_attempt_at=$4,lease_id=NULL,lease_expires_at=NULL,last_error_code=$5 WHERE lifecycle_id=$1 AND state='processing' AND lease_id=$2`, work.LifecycleID, work.LeaseID, state, next.UTC(), code)
	if err == nil && command.RowsAffected() != 1 {
		return errors.New("subscription termination lease was lost")
	}
	return err
}

var _ subscriptionlifecycle.Repository = (*SubscriptionLifecycleRepository)(nil)
