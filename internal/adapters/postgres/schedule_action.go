package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/tinfoyle/spyglass-engine/internal/application/scheduleaction"
	app "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

func (r *ScheduleRepository) CreateApprovedSchedule(ctx context.Context, value domain.Schedule, runNow bool, operationID string) error {
	value, err := domain.Restore(value)
	if err != nil || string(value.ID) != operationID || value.State != domain.StateActive || value.Version != 1 {
		return app.ErrInvalid
	}
	recurrence, template, err := encodeScheduleDefinition(value)
	if err != nil {
		return err
	}
	return r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		// Serialize retries without granting this role UPDATE on customer tables.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(value.AccountID)+":"+operationID); err != nil {
			return err
		}
		existing, err := loadSchedule(ctx, tx, value.AccountID, value.ID, false)
		if err == nil {
			if existing.CreatedBy != value.CreatedBy || !sameScheduleDefinition(existing, value) {
				return app.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, app.ErrNotFound) {
			return err
		}
		if err = validateScheduleTargetsWithLock(ctx, tx, value, false); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.schedules
   (account_id,id,name,timezone,recurrence,missed_run_policy,execution_template,state,version,next_run_at,created_by_user_id,created_at,updated_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			value.AccountID, value.ID, value.Name, value.Timezone, recurrence, value.MissedRunPolicy, template, value.State, value.Version, value.NextRunAt, value.CreatedBy, value.CreatedAt, value.UpdatedAt)
		if err != nil {
			return err
		}
		mutation := app.Mutation{EventID: operationID, Kind: "created", ActorUserID: value.CreatedBy, Reason: "Approved agent schedule proposal", CorrelationID: operationID, At: value.CreatedAt}
		if err = insertScheduleEvent(ctx, tx, value, 0, mutation); err != nil {
			return err
		}
		if !runNow {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.schedule_triggers(account_id,id,schedule_id,schedule_version,requested_by_user_id,requested_at)
   VALUES($1,$2,$2,1,$3,$4)`, value.AccountID, value.ID, value.CreatedBy, value.CreatedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.schedule_trigger_queue
   (account_id,trigger_id,schedule_id,schedule_version,requested_for,state,attempt_count,next_attempt_at,created_at,updated_at)
   VALUES($1,$2,$2,1,$3,'pending',0,$3,$3,$3)`, value.AccountID, value.ID, value.CreatedAt)
		return err
	})
}
func (r *ScheduleRepository) ApprovedScheduleExists(ctx context.Context, accountID ids.AccountID, scheduleID ids.ScheduleID, input scheduleaction.Input, userID ids.UserID) (bool, error) {
	value, err := r.Get(ctx, accountID, scheduleID)
	if errors.Is(err, app.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	expected, err := input.Schedule(accountID, string(scheduleID), userID, time.Now().UTC())
	if err != nil {
		return false, err
	}
	if value.CreatedBy != userID || !sameScheduleDefinition(value, expected) {
		return false, nil
	}
	var found bool
	err = r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM spyglass.schedule_events WHERE account_id=$1 AND id=$2 AND schedule_id=$2 AND event_type='created')
   AND ($3=false OR EXISTS(SELECT 1 FROM spyglass.schedule_triggers WHERE account_id=$1 AND id=$2 AND schedule_id=$2))`, accountID, scheduleID, input.RunNow).Scan(&found)
	})
	return found, err
}
