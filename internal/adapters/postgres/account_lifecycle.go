package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountlifecycle"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountLifecycleRepository struct{ pool *pgxpool.Pool }

func NewAccountLifecycleRepository(pool *pgxpool.Pool) *AccountLifecycleRepository {
	return &AccountLifecycleRepository{pool: pool}
}

func (r *AccountLifecycleRepository) Request(ctx context.Context, mutation accountlifecycle.RequestMutation) (accountlifecycle.Status, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountlifecycle.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var accountName string
	var state accounts.AccountState
	var version uint64
	err = tx.QueryRow(ctx, `SELECT display_name,state,version FROM accounts WHERE id=$1 FOR UPDATE`, mutation.AccountID).Scan(&accountName, &state, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	if err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if state != accounts.AccountActive {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	if version != mutation.ExpectedAccountVersion {
		return accountlifecycle.Status{}, accountlifecycle.ErrVersionConflict
	}
	if err := requireActiveOwner(ctx, tx, mutation.AccountID, mutation.ActorUserID); err != nil {
		return accountlifecycle.Status{}, err
	}
	blocker, err := billingBlocker(ctx, tx, mutation.AccountID, mutation.At)
	if err != nil {
		return accountlifecycle.Status{}, err
	}
	if blocker != "" {
		return accountlifecycle.Status{}, accountlifecycle.ErrBillingActive
	}

	version++
	if _, err := tx.Exec(ctx, `UPDATE accounts SET state='closing',version=$2 WHERE id=$1`, mutation.AccountID, version); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO account_closure_requests
		(id,account_id,state,requested_by_user_id,reason,account_version,requested_at,execute_after,next_attempt_at)
		VALUES ($1,$2,'cooling_off',$3,$4,$5,$6,$7,$7)`, mutation.RequestID, mutation.AccountID, mutation.ActorUserID, mutation.Reason, version, mutation.At, mutation.ExecuteAfter); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := insertAccountLifecycleEvent(ctx, tx, mutation.EventID, mutation.AccountID, mutation.RequestID, "closure_requested", "active", "closing", "user", string(mutation.ActorUserID), mutation.Reason, "", mutation.At); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	return accountlifecycle.Status{RequestID: mutation.RequestID, AccountID: mutation.AccountID, AccountName: accountName, AccountState: accounts.AccountClosing, AccountVersion: version, State: accountlifecycle.StateCoolingOff, Reason: mutation.Reason, RequestedAt: mutation.At, ExecuteAfter: mutation.ExecuteAfter}, nil
}

func (r *AccountLifecycleRepository) Cancel(ctx context.Context, mutation accountlifecycle.CancelMutation) (accountlifecycle.Status, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountlifecycle.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var accountName string
	var state accounts.AccountState
	var version uint64
	if err := tx.QueryRow(ctx, `SELECT display_name,state,version FROM accounts WHERE id=$1 FOR UPDATE`, mutation.AccountID).Scan(&accountName, &state, &version); errors.Is(err, pgx.ErrNoRows) {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	} else if err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if state != accounts.AccountClosing {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	if version != mutation.ExpectedAccountVersion {
		return accountlifecycle.Status{}, accountlifecycle.ErrVersionConflict
	}
	if err := requireActiveOwner(ctx, tx, mutation.AccountID, mutation.ActorUserID); err != nil {
		return accountlifecycle.Status{}, err
	}
	var status accountlifecycle.Status
	var requestState accountlifecycle.State
	err = tx.QueryRow(ctx, `
		SELECT id,state,reason,requested_at,execute_after
		FROM account_closure_requests
		WHERE account_id=$1 AND state IN ('cooling_off','processing','blocked') FOR UPDATE`, mutation.AccountID).
		Scan(&status.RequestID, &requestState, &status.Reason, &status.RequestedAt, &status.ExecuteAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	if err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	version++
	if _, err := tx.Exec(ctx, `UPDATE accounts SET state='active',version=$2 WHERE id=$1`, mutation.AccountID, version); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE account_closure_requests SET state='canceled',blocker_code=NULL,lease_expires_at=NULL,
		canceled_by_user_id=$2,cancel_reason=$3,canceled_at=$4
		WHERE id=$1`, status.RequestID, mutation.ActorUserID, mutation.Reason, mutation.At); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := insertAccountLifecycleEvent(ctx, tx, mutation.EventID, mutation.AccountID, status.RequestID, "closure_canceled", "closing", "active", "user", string(mutation.ActorUserID), mutation.Reason, "", mutation.At); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	status.AccountID, status.AccountName, status.AccountState, status.AccountVersion = mutation.AccountID, accountName, accounts.AccountActive, version
	status.State, status.CanceledAt = accountlifecycle.StateCanceled, timePointer(mutation.At)
	return status, nil
}

func (r *AccountLifecycleRepository) ListOwned(ctx context.Context, userID ids.UserID) ([]accountlifecycle.Status, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT cr.id,a.id,a.display_name,a.state,a.version,cr.state,cr.reason,COALESCE(cr.blocker_code,''),
		       cr.requested_at,cr.execute_after,cr.canceled_at,cr.closed_at,cr.delete_after
		FROM account_closure_requests cr
		JOIN accounts a ON a.id=cr.account_id
		JOIN memberships m ON m.account_id=a.id
		WHERE m.user_id=$1 AND m.role='owner' AND m.state='active'
		ORDER BY cr.requested_at DESC,cr.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]accountlifecycle.Status, 0)
	for rows.Next() {
		var status accountlifecycle.Status
		if err := scanLifecycleStatus(rows, &status); err != nil {
			return nil, err
		}
		result = append(result, status)
	}
	return result, rows.Err()
}

func (r *AccountLifecycleRepository) Claim(ctx context.Context, now time.Time, lease time.Duration) (accountlifecycle.Work, bool, error) {
	var work accountlifecycle.Work
	err := r.pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM account_closure_requests
			WHERE (state IN ('cooling_off','blocked') AND next_attempt_at<=$1)
			   OR (state='processing' AND lease_expires_at<=$1)
			ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE account_closure_requests cr
		SET state='processing',blocker_code=NULL,attempt_count=cr.attempt_count+1,lease_expires_at=$2
		FROM candidate c WHERE cr.id=c.id
		RETURNING cr.id,cr.account_id,cr.attempt_count,cr.requested_by_user_id`, now, now.Add(lease)).
		Scan(&work.RequestID, &work.AccountID, &work.Attempt, &work.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountlifecycle.Work{}, false, nil
	}
	return work, err == nil, err
}

func (r *AccountLifecycleRepository) Evaluate(ctx context.Context, work accountlifecycle.Work, eventID string, now time.Time, retention, blockedRetry time.Duration) (accountlifecycle.Status, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountlifecycle.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status accountlifecycle.Status
	var attempt int
	err = tx.QueryRow(ctx, `
		SELECT cr.id,a.id,a.display_name,a.state,a.version,cr.state,cr.reason,COALESCE(cr.blocker_code,''),
		       cr.requested_at,cr.execute_after,cr.canceled_at,cr.closed_at,cr.delete_after,cr.attempt_count
		FROM account_closure_requests cr JOIN accounts a ON a.id=cr.account_id
		WHERE cr.id=$1 FOR UPDATE OF cr,a`, work.RequestID).
		Scan(&status.RequestID, &status.AccountID, &status.AccountName, &status.AccountState, &status.AccountVersion,
			&status.State, &status.Reason, &status.BlockerCode, &status.RequestedAt, &status.ExecuteAfter,
			&status.CanceledAt, &status.ClosedAt, &status.DeleteAfter, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountlifecycle.Status{}, accountlifecycle.ErrNotFound
	}
	if err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if status.AccountState != accounts.AccountClosing || status.State != accountlifecycle.StateProcessing || attempt != work.Attempt {
		return accountlifecycle.Status{}, accountlifecycle.ErrStateConflict
	}
	blocker, err := billingBlocker(ctx, tx, work.AccountID, now)
	if err != nil {
		return accountlifecycle.Status{}, err
	}
	if blocker != "" {
		if _, err := tx.Exec(ctx, `UPDATE account_closure_requests SET state='blocked',blocker_code=$2,next_attempt_at=$3,lease_expires_at=NULL WHERE id=$1`, work.RequestID, blocker, now.Add(blockedRetry)); err != nil {
			return accountlifecycle.Status{}, classifyAccountLifecycle(err)
		}
		if err := insertAccountLifecycleEvent(ctx, tx, eventID, work.AccountID, work.RequestID, "closure_blocked", "closing", "closing", "workload", "account-lifecycle-worker", "Billing state must settle before logical closure", blocker, now); err != nil {
			return accountlifecycle.Status{}, classifyAccountLifecycle(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return accountlifecycle.Status{}, classifyAccountLifecycle(err)
		}
		status.State, status.BlockerCode = accountlifecycle.StateBlocked, blocker
		return status, nil
	}

	closedAt, deleteAfter := now.UTC(), now.UTC().Add(retention)
	status.AccountVersion++
	if _, err := tx.Exec(ctx, `UPDATE accounts SET state='closed',version=$2 WHERE id=$1`, work.AccountID, status.AccountVersion); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE account_closure_requests SET state='closed',blocker_code=NULL,lease_expires_at=NULL,closed_at=$2,delete_after=$3 WHERE id=$1`, work.RequestID, closedAt, deleteAfter); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := insertAccountLifecycleEvent(ctx, tx, eventID, work.AccountID, work.RequestID, "account_closed", "closing", "closed", "workload", "account-lifecycle-worker", "Cooling-off completed and closure preflight passed", "", now); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountlifecycle.Status{}, classifyAccountLifecycle(err)
	}
	status.AccountState, status.State = accounts.AccountClosed, accountlifecycle.StateClosed
	status.BlockerCode, status.ClosedAt, status.DeleteAfter = "", &closedAt, &deleteAfter
	return status, nil
}

type lifecycleScanner interface{ Scan(...any) error }

func scanLifecycleStatus(scanner lifecycleScanner, status *accountlifecycle.Status) error {
	return scanner.Scan(&status.RequestID, &status.AccountID, &status.AccountName, &status.AccountState, &status.AccountVersion,
		&status.State, &status.Reason, &status.BlockerCode, &status.RequestedAt, &status.ExecuteAfter,
		&status.CanceledAt, &status.ClosedAt, &status.DeleteAfter)
}

func requireActiveOwner(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, userID ids.UserID) error {
	var role accounts.MembershipRole
	var state accounts.MembershipState
	err := tx.QueryRow(ctx, `SELECT role,state FROM memberships WHERE account_id=$1 AND user_id=$2 FOR UPDATE`, accountID, userID).Scan(&role, &state)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (role != accounts.RoleOwner || state != accounts.MembershipActive)) {
		return accountlifecycle.ErrOwnershipRequired
	}
	return err
}

func billingBlocker(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, now time.Time) (string, error) {
	var blocker string
	err := tx.QueryRow(ctx, `
		SELECT CASE
			WHEN EXISTS (SELECT 1 FROM subscriptions WHERE account_id=$1 AND state NOT IN ('canceled','incomplete_expired')) THEN 'billing_active'
			WHEN EXISTS (SELECT 1 FROM billing_checkout_attempts WHERE account_id=$1 AND state='active' AND expires_at>$2) THEN 'checkout_active'
			ELSE '' END`, accountID, now).Scan(&blocker)
	return blocker, err
}

func insertAccountLifecycleEvent(ctx context.Context, tx pgx.Tx, eventID string, accountID ids.AccountID, requestID, action, fromState, toState, actorKind, actorID, reason, blocker string, at time.Time) error {
	var blockerValue any
	if blocker != "" {
		blockerValue = blocker
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO account_lifecycle_events
		(id,account_id,closure_request_id,action,from_state,to_state,actor_kind,actor_id,reason,blocker_code,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, eventID, accountID, requestID, action, fromState, toState, actorKind, actorID, reason, blockerValue, at.UTC())
	return err
}

func classifyAccountLifecycle(err error) error {
	if err == nil {
		return nil
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && (pgError.Code == "40001" || pgError.Code == "23505") {
		return accountlifecycle.ErrVersionConflict
	}
	return err
}

func timePointer(value time.Time) *time.Time { return &value }

var _ accountlifecycle.Repository = (*AccountLifecycleRepository)(nil)
