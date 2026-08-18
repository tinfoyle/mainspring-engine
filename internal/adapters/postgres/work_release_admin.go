package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/workreleaseadmin"
)

// WorkReleaseAdminRepository can reach release metadata only through audited
// security-definer functions. Its production role needs no table privileges.
type WorkReleaseAdminRepository struct{ pool *pgxpool.Pool }

func NewWorkReleaseAdminRepository(pool *pgxpool.Pool) *WorkReleaseAdminRepository {
	return &WorkReleaseAdminRepository{pool: pool}
}

func (r *WorkReleaseAdminRepository) Inspect(ctx context.Context, limit int, change workreleaseadmin.Change) ([]workreleaseadmin.DeadLetter, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT account_id,work_item_id,reservation_id,attempt_count,last_error_code,queued_at,last_attempt_at
		FROM public.spyglass_inspect_work_capacity_release_dead_letters($1,$2,$3,$4,$5)`,
		change.BatchID, change.Actor, change.Reason, change.Environment, limit)
	if err != nil {
		return nil, classifyWorkReleaseAdminError(err)
	}
	defer rows.Close()
	records := make([]workreleaseadmin.DeadLetter, 0, limit)
	for rows.Next() {
		record, err := scanInspectedDeadLetter(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyWorkReleaseAdminError(err)
	}
	return records, nil
}

func (r *WorkReleaseAdminRepository) Requeue(ctx context.Context, target workreleaseadmin.Target, change workreleaseadmin.Change) (workreleaseadmin.DeadLetter, error) {
	var record workreleaseadmin.DeadLetter
	record.Target = target
	var lastError *string
	err := r.pool.QueryRow(ctx, `
		SELECT account_id,work_item_id,reservation_id,previous_attempt_count,previous_error_code,queued_at,last_attempt_at,next_attempt_at
		FROM public.spyglass_requeue_work_capacity_release_dead_letter($1,$2,$3,$4,$5,$6,$7)`,
		change.BatchID, target.AccountID, target.WorkItemID, target.ReservationID, change.Actor, change.Reason, change.Environment).
		Scan(&record.AccountID, &record.WorkItemID, &record.ReservationID, &record.AttemptCount, &lastError, &record.QueuedAt, &record.LastAttemptAt, &record.NextAttemptAt)
	if err != nil {
		return workreleaseadmin.DeadLetter{}, classifyWorkReleaseAdminError(err)
	}
	if lastError != nil {
		record.LastErrorCode = *lastError
	}
	return record, nil
}

func scanInspectedDeadLetter(row pgx.Row) (workreleaseadmin.DeadLetter, error) {
	var record workreleaseadmin.DeadLetter
	var lastError *string
	if err := row.Scan(&record.AccountID, &record.WorkItemID, &record.ReservationID, &record.AttemptCount, &lastError, &record.QueuedAt, &record.LastAttemptAt); err != nil {
		return workreleaseadmin.DeadLetter{}, fmt.Errorf("scan Work release dead letter: %w", err)
	}
	if lastError != nil {
		record.LastErrorCode = *lastError
	}
	return record, nil
}

func classifyWorkReleaseAdminError(err error) error {
	if err == nil || errors.Is(err, workreleaseadmin.ErrInvalidChange) || errors.Is(err, workreleaseadmin.ErrNotFound) || errors.Is(err, workreleaseadmin.ErrStateConflict) {
		return err
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return workreleaseadmin.ErrInvalidChange
		case "P0002":
			return workreleaseadmin.ErrNotFound
		case "P0001":
			return workreleaseadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("Work release administration unavailable: %w", err)
}

var _ workreleaseadmin.Store = (*WorkReleaseAdminRepository)(nil)
