package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/schedulequeueadmin"
)

// ScheduleQueueAdminRepository has no table grants; audited security-definer
// functions are its complete database authority.
type ScheduleQueueAdminRepository struct{ pool *pgxpool.Pool }

func NewScheduleQueueAdminRepository(pool *pgxpool.Pool) *ScheduleQueueAdminRepository {
	return &ScheduleQueueAdminRepository{pool: pool}
}

func (r *ScheduleQueueAdminRepository) Inspect(ctx context.Context, queue string, limit int, change schedulequeueadmin.Change) ([]schedulequeueadmin.DeadLetter, error) {
	rows, err := r.pool.Query(ctx, `SELECT queue_name,account_id,schedule_id,trigger_id,attempt_count,last_error_code,
		occurrence_at,created_at,updated_at
		FROM public.spyglass_inspect_schedule_queue_dead_letters($1,$2,$3,$4,$5,$6)`,
		change.BatchID, queue, change.Actor, change.Reason, change.Environment, limit)
	if err != nil {
		return nil, classifyScheduleQueueAdminError(err)
	}
	defer rows.Close()
	records := make([]schedulequeueadmin.DeadLetter, 0, limit)
	for rows.Next() {
		record, err := scanScheduleQueueDeadLetter(rows, false)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyScheduleQueueAdminError(err)
	}
	return records, nil
}

func (r *ScheduleQueueAdminRepository) Requeue(ctx context.Context, target schedulequeueadmin.Target, change schedulequeueadmin.Change) (schedulequeueadmin.DeadLetter, error) {
	var triggerID any
	if target.TriggerID != "" {
		triggerID = target.TriggerID
	}
	row := r.pool.QueryRow(ctx, `SELECT queue_name,account_id,schedule_id,trigger_id,previous_attempt_count,previous_error_code,
		occurrence_at,created_at,updated_at,next_attempt_at
		FROM public.spyglass_requeue_schedule_queue_dead_letter($1,$2,$3,$4,$5,$6,$7,$8)`,
		change.BatchID, target.Queue, target.AccountID, target.ScheduleID, triggerID, change.Actor, change.Reason, change.Environment)
	record, err := scanScheduleQueueDeadLetter(row, true)
	if err != nil {
		return schedulequeueadmin.DeadLetter{}, classifyScheduleQueueAdminError(err)
	}
	return record, nil
}

func scanScheduleQueueDeadLetter(row pgx.Row, next bool) (schedulequeueadmin.DeadLetter, error) {
	var record schedulequeueadmin.DeadLetter
	var triggerID, lastError *string
	values := []any{&record.Queue, &record.AccountID, &record.ScheduleID, &triggerID, &record.AttemptCount, &lastError, &record.OccurrenceAt, &record.CreatedAt, &record.UpdatedAt}
	if next {
		values = append(values, &record.NextAttemptAt)
	}
	if err := row.Scan(values...); err != nil {
		return schedulequeueadmin.DeadLetter{}, fmt.Errorf("scan Schedule queue dead letter: %w", err)
	}
	if triggerID != nil {
		record.TriggerID = *triggerID
	}
	if lastError != nil {
		record.LastErrorCode = *lastError
	}
	return record, nil
}

func classifyScheduleQueueAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return schedulequeueadmin.ErrInvalidChange
		case "P0002":
			return schedulequeueadmin.ErrNotFound
		case "P0001":
			return schedulequeueadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("Schedule queue administration unavailable: %w", err)
}

var _ schedulequeueadmin.Store = (*ScheduleQueueAdminRepository)(nil)
