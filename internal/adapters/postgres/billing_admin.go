package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/billingadmin"
)

// BillingAdminRepository has execute-only access to audited security-definer
// functions; the operator role needs no direct billing-table privileges.
type BillingAdminRepository struct{ pool *pgxpool.Pool }

func NewBillingAdminRepository(pool *pgxpool.Pool) *BillingAdminRepository {
	return &BillingAdminRepository{pool: pool}
}

func (r *BillingAdminRepository) Inspect(ctx context.Context, limit int, change billingadmin.Change) ([]billingadmin.Record, error) {
	rows, err := r.pool.Query(ctx, `SELECT target_kind,target_id,account_id,provider_mode,processing_state,attempt_count,last_error_code,next_attempt_at,created_at FROM public.spyglass_inspect_billing_failures($1,$2,$3,$4,$5,$6)`, change.BatchID, change.Actor, change.Reason, change.Environment, change.Mode, limit)
	if err != nil {
		return nil, classifyBillingAdminError(err)
	}
	defer rows.Close()
	records := make([]billingadmin.Record, 0, limit)
	for rows.Next() {
		record, err := scanBillingAdminRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, classifyBillingAdminError(rows.Err())
}

func (r *BillingAdminRepository) ReplayEvent(ctx context.Context, target string, change billingadmin.Change) (billingadmin.Record, error) {
	return r.one(ctx, `SELECT target_kind,target_id,account_id,provider_mode,processing_state,attempt_count,last_error_code,next_attempt_at,created_at FROM public.spyglass_replay_billing_event($1,$2,$3,$4,$5,$6)`, change.BatchID, target, change.Actor, change.Reason, change.Environment, change.Mode)
}

func (r *BillingAdminRepository) QueueRefresh(ctx context.Context, target string, change billingadmin.Change) (billingadmin.Record, error) {
	return r.one(ctx, `SELECT target_kind,target_id,account_id,provider_mode,processing_state,attempt_count,last_error_code,next_attempt_at,created_at FROM public.spyglass_queue_billing_subscription_refresh($1,$2,$3,$4,$5,$6)`, change.BatchID, target, change.Actor, change.Reason, change.Environment, change.Mode)
}

func (r *BillingAdminRepository) one(ctx context.Context, query string, args ...any) (billingadmin.Record, error) {
	record, err := scanBillingAdminRecord(r.pool.QueryRow(ctx, query, args...))
	return record, classifyBillingAdminError(err)
}

func scanBillingAdminRecord(row pgx.Row) (billingadmin.Record, error) {
	var record billingadmin.Record
	var lastError *string
	if err := row.Scan(&record.Kind, &record.TargetID, &record.AccountID, &record.Mode, &record.State, &record.AttemptCount, &lastError, &record.NextAttemptAt, &record.CreatedAt); err != nil {
		return billingadmin.Record{}, err
	}
	if lastError != nil {
		record.LastErrorCode = *lastError
	}
	return record, nil
}

func classifyBillingAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return billingadmin.ErrInvalidChange
		case "P0002":
			return billingadmin.ErrNotFound
		case "P0001":
			return billingadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("billing administration unavailable: %w", err)
}

var _ billingadmin.Store = (*BillingAdminRepository)(nil)
