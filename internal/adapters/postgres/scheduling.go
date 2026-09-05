package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	domain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ScheduleRepository struct{ cell *database.CellPool }

func NewScheduleRepository(cell *database.CellPool) (*ScheduleRepository, error) {
	if cell == nil {
		return nil, errors.New("Schedule cell pool is required")
	}
	return &ScheduleRepository{cell: cell}, nil
}

func (r *ScheduleRepository) Create(ctx context.Context, value domain.Schedule, mutation scheduleapp.Mutation) (domain.Schedule, bool, error) {
	value, err := domain.Restore(value)
	if err != nil || !mutation.Valid() || mutation.Kind != "created" || value.Version != 1 || value.State != domain.StateActive || !value.CreatedAt.Equal(mutation.At.UTC()) {
		return domain.Schedule{}, false, scheduleapp.ErrInvalid
	}
	recurrence, template, err := encodeScheduleDefinition(value)
	if err != nil {
		return domain.Schedule{}, false, scheduleapp.ErrInvalid
	}
	created := false
	result := value
	err = r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadSchedule(ctx, tx, value.AccountID, value.ID, true)
		if loadErr == nil {
			matched, err := scheduleEventMatches(ctx, tx, value.AccountID, value.ID, mutation, 0, 1)
			if err != nil {
				return err
			}
			if !matched || !sameScheduleDefinition(existing, value) || existing.Version != 1 || existing.State != domain.StateActive {
				return scheduleapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, scheduleapp.ErrNotFound) {
			return loadErr
		}
		if err := validateScheduleTargets(ctx, tx, value); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedules
			(account_id,id,name,timezone,recurrence,missed_run_policy,execution_template,state,version,next_run_at,created_by_user_id,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, value.AccountID, value.ID, value.Name, value.Timezone,
			recurrence, value.MissedRunPolicy, template, value.State, value.Version, value.NextRunAt, value.CreatedBy, value.CreatedAt, value.UpdatedAt); err != nil {
			return err
		}
		if err := insertScheduleEvent(ctx, tx, value, 0, mutation); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifySchedule(err)
}

func (r *ScheduleRepository) Get(ctx context.Context, accountID ids.AccountID, scheduleID ids.ScheduleID) (domain.Schedule, error) {
	var result domain.Schedule
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		value, err := loadSchedule(ctx, tx, accountID, scheduleID, false)
		result = value
		return err
	})
	return result, classifySchedule(err)
}

func (r *ScheduleRepository) List(ctx context.Context, accountID ids.AccountID, query scheduleapp.ListQuery) (scheduleapp.Page, error) {
	if query.Limit < 1 || query.Limit > scheduleapp.MaximumPageSize || (query.AfterUpdatedAt == nil) != (query.AfterID == "") ||
		(query.AfterID != "" && ids.Validate(string(query.AfterID)) != nil) {
		return scheduleapp.Page{}, scheduleapp.ErrInvalid
	}
	result := scheduleapp.Page{Items: []domain.Schedule{}}
	err := r.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,account_id,name,timezone,recurrence,missed_run_policy,execution_template,state,version,next_run_at,
			created_by_user_id,created_at,updated_at FROM spyglass.schedules
			WHERE account_id=$1 AND state<>'deleted' AND ($2::timestamptz IS NULL OR (updated_at,id)<($2,$3))
			ORDER BY updated_at DESC,id DESC LIMIT $4`, accountID, query.AfterUpdatedAt, nullableScheduleID(query.AfterID), query.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			value, err := scanSchedule(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, value)
		}
		return rows.Err()
	})
	if err != nil {
		return scheduleapp.Page{}, classifySchedule(err)
	}
	if len(result.Items) > query.Limit {
		result.Items = result.Items[:query.Limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = &scheduleapp.Cursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
	return result, nil
}

func (r *ScheduleRepository) Update(ctx context.Context, value domain.Schedule, expected uint64, mutation scheduleapp.Mutation) (domain.Schedule, error) {
	value, err := domain.Restore(value)
	if err != nil || !mutation.Valid() || value.Version != expected+1 || !value.UpdatedAt.Equal(mutation.At.UTC()) || !validScheduleMutationState(value, mutation.Kind) {
		return domain.Schedule{}, scheduleapp.ErrInvalid
	}
	recurrence, template, err := encodeScheduleDefinition(value)
	if err != nil {
		return domain.Schedule{}, scheduleapp.ErrInvalid
	}
	result := value
	err = r.cell.WithAccountTx(ctx, value.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadSchedule(ctx, tx, value.AccountID, value.ID, true)
		if err != nil {
			return err
		}
		if current.Version != expected {
			matched, matchErr := scheduleEventMatches(ctx, tx, value.AccountID, value.ID, mutation, expected, expected+1)
			if matchErr != nil {
				return matchErr
			}
			if current.Version == expected+1 && matched && sameScheduleDefinition(current, value) && current.State == value.State {
				result = current
				return nil
			}
			return scheduleapp.ErrConflict
		}
		if (mutation.Kind == "updated" && value.Template.EmailSelf || mutation.Kind == "resumed" && current.Template.EmailSelf) && current.CreatedBy != mutation.ActorUserID {
			return scheduleapp.ErrInvalid
		}
		var intended domain.Schedule
		switch mutation.Kind {
		case "updated":
			intended, err = current.Revise(expected, domain.Revision{Name: value.Name, Timezone: value.Timezone, Recurrence: value.Recurrence,
				MissedRunPolicy: value.MissedRunPolicy, Template: value.Template}, mutation.At)
			if err == nil {
				err = validateScheduleTargets(ctx, tx, intended)
			}
		case "paused":
			intended, err = current.Pause(expected, mutation.At)
		case "resumed":
			intended, err = current.Resume(expected, mutation.At)
		case "deleted":
			intended, err = current.Delete(expected, mutation.At)
		default:
			return scheduleapp.ErrInvalid
		}
		if err != nil || !reflect.DeepEqual(intended, value) {
			return scheduleapp.ErrInvalid
		}
		command, err := tx.Exec(ctx, `UPDATE spyglass.schedules SET name=$3,timezone=$4,recurrence=$5,missed_run_policy=$6,
			execution_template=$7,state=$8,version=$9,next_run_at=$10,updated_at=$11 WHERE account_id=$1 AND id=$2 AND version=$12`,
			value.AccountID, value.ID, value.Name, value.Timezone, recurrence, value.MissedRunPolicy, template, value.State,
			value.Version, value.NextRunAt, value.UpdatedAt, expected)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return scheduleapp.ErrConflict
		}
		return insertScheduleEvent(ctx, tx, value, expected, mutation)
	})
	return result, classifySchedule(err)
}

func (r *ScheduleRepository) EnqueueTrigger(ctx context.Context, request scheduleapp.TriggerRequest, mutation scheduleapp.Mutation) (scheduleapp.Trigger, bool, error) {
	if !request.Valid() || !mutation.Valid() || mutation.Kind != "trigger_requested" || mutation.EventID != request.ID ||
		mutation.ActorUserID != request.RequestedBy || !mutation.At.UTC().Equal(request.RequestedAt.UTC()) {
		return scheduleapp.Trigger{}, false, scheduleapp.ErrInvalid
	}
	result := scheduleapp.Trigger{ID: request.ID, AccountID: request.AccountID, ScheduleID: request.ScheduleID,
		ScheduleVersion: request.ScheduleVersion, RequestedBy: request.RequestedBy, RequestedAt: request.RequestedAt.UTC(), State: "accepted"}
	created := false
	err := r.cell.WithAccountTx(ctx, request.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		existing, loadErr := loadScheduleTrigger(ctx, tx, request.AccountID, request.ID)
		if loadErr == nil {
			matched, err := scheduleEventMatches(ctx, tx, request.AccountID, request.ScheduleID, mutation, request.ScheduleVersion, request.ScheduleVersion)
			if err != nil {
				return err
			}
			if !matched || existing.ScheduleID != request.ScheduleID || existing.ScheduleVersion != request.ScheduleVersion || existing.RequestedBy != request.RequestedBy {
				return scheduleapp.ErrConflict
			}
			result = existing
			return nil
		}
		if !errors.Is(loadErr, scheduleapp.ErrNotFound) {
			return loadErr
		}
		current, err := loadSchedule(ctx, tx, request.AccountID, request.ScheduleID, true)
		if err != nil {
			return err
		}
		if current.Template.EmailSelf && current.CreatedBy != request.RequestedBy {
			return scheduleapp.ErrInvalid
		}
		if current.State != domain.StateActive || current.Version != request.ScheduleVersion {
			return scheduleapp.ErrConflict
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_triggers
			(account_id,id,schedule_id,schedule_version,requested_by_user_id,requested_at)
			VALUES ($1,$2,$3,$4,$5,$6)`, request.AccountID, request.ID, request.ScheduleID, request.ScheduleVersion,
			request.RequestedBy, request.RequestedAt.UTC()); err != nil {
			return err
		}
		if err := insertScheduleEvent(ctx, tx, current, current.Version, mutation); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_trigger_queue
			(account_id,trigger_id,schedule_id,schedule_version,requested_for,state,attempt_count,next_attempt_at,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,'pending',0,$5,$5,$5)`, request.AccountID, request.ID, request.ScheduleID,
			request.ScheduleVersion, request.RequestedAt.UTC()); err != nil {
			return err
		}
		created = true
		return nil
	})
	return result, created, classifySchedule(err)
}

func loadScheduleTrigger(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, triggerID string) (scheduleapp.Trigger, error) {
	result := scheduleapp.Trigger{State: "accepted"}
	err := tx.QueryRow(ctx, `SELECT id,account_id,schedule_id,schedule_version,requested_by_user_id,requested_at
		FROM spyglass.schedule_triggers WHERE account_id=$1 AND id=$2`, accountID, triggerID).Scan(&result.ID, &result.AccountID,
		&result.ScheduleID, &result.ScheduleVersion, &result.RequestedBy, &result.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return scheduleapp.Trigger{}, scheduleapp.ErrNotFound
	}
	if err != nil || !result.Valid() {
		if err != nil {
			return scheduleapp.Trigger{}, err
		}
		return scheduleapp.Trigger{}, scheduleapp.ErrRepository
	}
	return result, nil
}

func validScheduleMutationState(value domain.Schedule, kind string) bool {
	switch kind {
	case "updated":
		return value.State == domain.StateActive || value.State == domain.StatePaused
	case "paused":
		return value.State == domain.StatePaused
	case "resumed":
		return value.State == domain.StateActive
	case "deleted":
		return value.State == domain.StateDeleted
	default:
		return false
	}
}

func validateScheduleTargets(ctx context.Context, tx pgx.Tx, value domain.Schedule) error {
	var boardroomState string
	var managerID *string
	if err := tx.QueryRow(ctx, `SELECT state,manager_persona_id::text FROM spyglass.agent_boardrooms
		WHERE account_id=$1 AND id=$2 FOR SHARE`, value.AccountID, value.Template.BoardroomID).Scan(&boardroomState, &managerID); errors.Is(err, pgx.ErrNoRows) {
		return scheduleapp.ErrNotFound
	} else if err != nil {
		return err
	}
	if boardroomState != "active" || (value.Template.Mode == "manager_led" && managerID == nil) {
		return scheduleapp.ErrInvalid
	}
	if value.Template.Mode == "manager_led" {
		var managerState string
		var managerVersion int64
		if err := tx.QueryRow(ctx, `SELECT state,latest_version FROM spyglass.agent_personas
			WHERE account_id=$1 AND id=$2::uuid AND boardroom_id=$3 FOR SHARE`, value.AccountID, *managerID, value.Template.BoardroomID).Scan(&managerState, &managerVersion); errors.Is(err, pgx.ErrNoRows) {
			return scheduleapp.ErrNotFound
		} else if err != nil {
			return err
		} else if managerState != "active" || managerVersion < 1 {
			return scheduleapp.ErrInvalid
		}
	}
	for _, personaID := range value.Template.PersonaIDs {
		var state string
		var latestVersion int64
		if err := tx.QueryRow(ctx, `SELECT state,latest_version FROM spyglass.agent_personas
			WHERE account_id=$1 AND id=$2 AND boardroom_id=$3 FOR SHARE`, value.AccountID, personaID, value.Template.BoardroomID).Scan(&state, &latestVersion); errors.Is(err, pgx.ErrNoRows) {
			return scheduleapp.ErrNotFound
		} else if err != nil {
			return err
		} else if state != "active" || latestVersion < 1 || (managerID != nil && value.Template.Mode == "manager_led" && string(personaID) == *managerID) {
			return scheduleapp.ErrInvalid
		}
	}
	return nil
}

func encodeScheduleDefinition(value domain.Schedule) ([]byte, []byte, error) {
	recurrence, err := json.Marshal(value.Recurrence)
	if err != nil {
		return nil, nil, err
	}
	template, err := json.Marshal(value.Template)
	return recurrence, template, err
}

func loadSchedule(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, scheduleID ids.ScheduleID, lock bool) (domain.Schedule, error) {
	query := `SELECT id,account_id,name,timezone,recurrence,missed_run_policy,execution_template,state,version,next_run_at,
		created_by_user_id,created_at,updated_at FROM spyglass.schedules WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	row := tx.QueryRow(ctx, query, accountID, scheduleID)
	value, err := scanSchedule(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Schedule{}, scheduleapp.ErrNotFound
	}
	return value, err
}

type scheduleScanner interface{ Scan(...any) error }

func scanSchedule(row scheduleScanner) (domain.Schedule, error) {
	var value domain.Schedule
	var recurrence, template []byte
	err := row.Scan(&value.ID, &value.AccountID, &value.Name, &value.Timezone, &recurrence, &value.MissedRunPolicy, &template,
		&value.State, &value.Version, &value.NextRunAt, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt)
	if err != nil {
		return domain.Schedule{}, err
	}
	if json.Unmarshal(recurrence, &value.Recurrence) != nil || json.Unmarshal(template, &value.Template) != nil {
		return domain.Schedule{}, scheduleapp.ErrRepository
	}
	value, err = domain.Restore(value)
	if err != nil {
		return domain.Schedule{}, scheduleapp.ErrRepository
	}
	return value, nil
}

func nullableScheduleID(value ids.ScheduleID) any {
	if value == "" {
		return nil
	}
	return value
}

func insertScheduleEvent(ctx context.Context, tx pgx.Tx, value domain.Schedule, from uint64, mutation scheduleapp.Mutation) error {
	actorKind := "user"
	actorID := string(mutation.ActorUserID)
	payload, err := json.Marshal(map[string]any{"state": value.State, "missed_run_policy": value.MissedRunPolicy})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO spyglass.schedule_events
		(account_id,id,schedule_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, value.AccountID, mutation.EventID, value.ID, mutation.Kind,
		from, value.Version, actorKind, actorID, mutation.Reason, mutation.CorrelationID,
		payload, mutation.At.UTC())
	return err
}

func scheduleEventMatches(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, scheduleID ids.ScheduleID, mutation scheduleapp.Mutation, from, to uint64) (bool, error) {
	var kind string
	var storedFrom, storedTo uint64
	err := tx.QueryRow(ctx, `SELECT event_type,from_version,to_version FROM spyglass.schedule_events
		WHERE account_id=$1 AND id=$2 AND schedule_id=$3`, accountID, mutation.EventID, scheduleID).Scan(&kind, &storedFrom, &storedTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil && kind == mutation.Kind && storedFrom == from && storedTo == to, err
}

func sameScheduleDefinition(left, right domain.Schedule) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.Name == right.Name && left.Timezone == right.Timezone &&
		reflect.DeepEqual(left.Recurrence, right.Recurrence) && left.MissedRunPolicy == right.MissedRunPolicy &&
		reflect.DeepEqual(left.Template, right.Template) && left.Version == right.Version && left.CreatedBy == right.CreatedBy
}

func classifySchedule(err error) error {
	if err == nil || errors.Is(err, scheduleapp.ErrInvalid) || errors.Is(err, scheduleapp.ErrNotFound) || errors.Is(err, scheduleapp.ErrConflict) || errors.Is(err, scheduleapp.ErrRepository) {
		return err
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23505", "40001":
			return scheduleapp.ErrConflict
		case "23503", "23514", "22023":
			return scheduleapp.ErrInvalid
		}
	}
	return fmt.Errorf("%w: %v", scheduleapp.ErrRepository, err)
}

var _ scheduleapp.Store = (*ScheduleRepository)(nil)
