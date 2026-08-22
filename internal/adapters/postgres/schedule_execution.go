package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	agentapp "github.com/tinfoyle/spyglass-engine/internal/application/agents"
	scheduleapp "github.com/tinfoyle/spyglass-engine/internal/application/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	scheduledomain "github.com/tinfoyle/spyglass-engine/internal/modules/scheduling"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// ScheduleExecutionRepository is the trusted cell-side occurrence boundary.
// A production worker reaches Load/Dispatch/Skip through the private workload
// transport; only claim/heartbeat/failure use its queue credential directly.
type ScheduleExecutionRepository struct {
	queue *ScheduleExecutionQueueRepository
	cell  *database.CellPool
}

func NewScheduleExecutionRepository(pool *pgxpool.Pool, cell *database.CellPool) (*ScheduleExecutionRepository, error) {
	if pool == nil || cell == nil {
		return nil, errors.New("Schedule execution repository dependencies are required")
	}
	queue, err := NewScheduleExecutionQueueRepository(pool)
	if err != nil {
		return nil, err
	}
	return &ScheduleExecutionRepository{queue: queue, cell: cell}, nil
}

type ScheduleExecutionQueueRepository struct {
	pool *pgxpool.Pool
}

func NewScheduleExecutionQueueRepository(pool *pgxpool.Pool) (*ScheduleExecutionQueueRepository, error) {
	if pool == nil {
		return nil, errors.New("Schedule execution queue repository database pool is required")
	}
	return &ScheduleExecutionQueueRepository{pool: pool}, nil
}

func (r *ScheduleExecutionRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (scheduleapp.ExecutionClaim, bool, error) {
	return r.queue.Claim(ctx, leaseID, now, lease)
}

func (r *ScheduleExecutionQueueRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (scheduleapp.ExecutionClaim, bool, error) {
	var trigger scheduleapp.ExecutionClaim
	err := r.pool.QueryRow(ctx, `SELECT account_id,trigger_id,schedule_id,schedule_version,requested_for,lease_id,attempt_count
		FROM public.spyglass_claim_schedule_trigger($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&trigger.AccountID, &trigger.TriggerID, &trigger.ScheduleID, &trigger.ScheduleVersion, &trigger.ScheduledFor, &trigger.LeaseID, &trigger.Attempt)
	if err == nil {
		trigger.Kind = "triggered"
		return trigger, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return scheduleapp.ExecutionClaim{}, false, classifyScheduleExecution(err)
	}
	var claim scheduleapp.ExecutionClaim
	err = r.pool.QueryRow(ctx, `SELECT account_id,schedule_id,schedule_version,scheduled_for,lease_id,attempt_count
		FROM public.spyglass_claim_schedule_dispatch($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.AccountID, &claim.ScheduleID, &claim.ScheduleVersion, &claim.ScheduledFor, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return scheduleapp.ExecutionClaim{}, false, nil
	}
	if err != nil {
		return scheduleapp.ExecutionClaim{}, false, classifyScheduleExecution(err)
	}
	claim.Kind = "scheduled"
	return claim, true, nil
}

func (r *ScheduleExecutionRepository) Load(ctx context.Context, claim scheduleapp.ExecutionClaim) (scheduleapp.ExecutionSnapshot, error) {
	if !claim.Valid() {
		return scheduleapp.ExecutionSnapshot{}, scheduleapp.ErrExecutionClaimInvalid
	}
	var result scheduledomain.Schedule
	err := r.cell.WithAccountTx(ctx, claim.AccountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var leased bool
		query := `SELECT true FROM spyglass.schedule_dispatch_queue
			WHERE account_id=$1 AND schedule_id=$2 AND schedule_version=$3 AND state='leased' AND lease_id=$4
			  AND scheduled_for=$5 AND lease_expires_at>=statement_timestamp()`
		arguments := []any{claim.AccountID, claim.ScheduleID, claim.ScheduleVersion, claim.LeaseID, claim.ScheduledFor}
		if claim.ExecutionKind() == "triggered" {
			query = `SELECT true FROM spyglass.schedule_trigger_queue
				WHERE account_id=$1 AND trigger_id=$2 AND schedule_id=$3 AND schedule_version=$4 AND state='leased' AND lease_id=$5
				  AND requested_for=$6 AND lease_expires_at>=statement_timestamp()`
			arguments = []any{claim.AccountID, claim.TriggerID, claim.ScheduleID, claim.ScheduleVersion, claim.LeaseID, claim.ScheduledFor}
		}
		if err := tx.QueryRow(ctx, query, arguments...).Scan(&leased); errors.Is(err, pgx.ErrNoRows) {
			return scheduleapp.ErrExecutionLeaseLost
		} else if err != nil {
			return err
		}
		if !leased {
			return scheduleapp.ErrExecutionLeaseLost
		}
		value, err := loadSchedule(ctx, tx, claim.AccountID, claim.ScheduleID, false)
		result = value
		return err
	})
	if err != nil {
		return scheduleapp.ExecutionSnapshot{}, classifyScheduleExecution(err)
	}
	snapshot := scheduleapp.ExecutionSnapshot{Schedule: result}
	if !snapshot.ValidFor(claim) {
		return scheduleapp.ExecutionSnapshot{}, scheduleapp.ErrExecutionSnapshotInvalid
	}
	return snapshot, nil
}

func (r *ScheduleExecutionRepository) Heartbeat(ctx context.Context, claim scheduleapp.ExecutionClaim, now time.Time, lease time.Duration) error {
	return r.queue.Heartbeat(ctx, claim, now, lease)
}

func (r *ScheduleExecutionQueueRepository) Heartbeat(ctx context.Context, claim scheduleapp.ExecutionClaim, now time.Time, lease time.Duration) error {
	var accepted bool
	query := `SELECT public.spyglass_heartbeat_schedule_dispatch($1,$2,$3,$4,$5,$6)`
	arguments := []any{claim.AccountID, claim.ScheduleID, claim.LeaseID, claim.ScheduledFor, now.UTC(), int(lease / time.Second)}
	if claim.ExecutionKind() == "triggered" {
		query = `SELECT public.spyglass_heartbeat_schedule_trigger($1,$2,$3,$4,$5,$6)`
		arguments = []any{claim.AccountID, claim.TriggerID, claim.LeaseID, claim.ScheduledFor, now.UTC(), int(lease / time.Second)}
	}
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(&accepted)
	if err != nil {
		return classifyScheduleExecution(err)
	}
	if !accepted {
		return scheduleapp.ErrExecutionLeaseLost
	}
	return nil
}

func (r *ScheduleExecutionRepository) Dispatch(ctx context.Context, command scheduleapp.OccurrenceCommand) (bool, error) {
	if !command.Valid() {
		return false, scheduleapp.ErrExecutionSnapshotInvalid
	}
	reconciled := false
	err := r.cell.WithAccountTx(ctx, command.Schedule.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		matched, found, err := loadScheduleOccurrenceMatch(ctx, tx, command, "dispatched")
		if err != nil {
			return err
		}
		if found {
			if !matched {
				return scheduleapp.ErrExecutionConflict
			}
			reconciled = true
			return nil
		}
		current, err := lockScheduleExecution(ctx, tx, command)
		if err != nil {
			return err
		}
		draft := agentapp.StartRunDraft{
			Actor: access.Actor{UserID: current.CreatedBy}, AccountID: current.AccountID, BoardroomID: current.Template.BoardroomID,
			RunID: command.RunID, ConversationID: command.ConversationID, CreateConversation: true, UserMessageID: command.UserMessageID,
			Subject: command.Subject, Prompt: current.Template.Prompt, Mode: agentapp.RunMode(current.Template.Mode), PersonaIDs: append([]ids.PersonaID(nil), current.Template.PersonaIDs...),
			Context:            agentapp.ContextSelection{WorkItemIDs: append([]ids.WorkItemID(nil), current.Template.WorkItemIDs...), KnowledgeFactIDs: append([]ids.KnowledgeFactID(nil), current.Template.KnowledgeFactIDs...), KnowledgeDocumentIDs: append([]ids.KnowledgeDocumentID(nil), current.Template.KnowledgeDocumentIDs...), BaselineAssessmentIDs: append([]ids.BaselineAssessmentID(nil), current.Template.BaselineAssessmentIDs...)},
			EntitlementVersion: command.Authorization.EntitlementVersion, MaximumConcurrentRun: command.Authorization.MaximumConcurrentRun,
			CanReadRestricted: command.Authorization.CanReadRestricted, CreatedAt: command.At, RequestExpiresAt: command.RequestExpiresAt,
		}
		_, created, err := startAgentRunInTx(ctx, tx, draft)
		if err != nil {
			var limit *accessLimitError
			if errors.As(err, &limit) {
				return scheduleapp.ErrExecutionCapacity
			}
			return mapAgentStartToScheduleExecution(err)
		}
		if !created {
			return scheduleapp.ErrExecutionConflict
		}
		return completeScheduleOccurrence(ctx, tx, current, command, "dispatched")
	})
	return reconciled, classifyScheduleExecution(err)
}

func (r *ScheduleExecutionRepository) Skip(ctx context.Context, command scheduleapp.OccurrenceCommand) (bool, error) {
	if !command.Valid() {
		return false, scheduleapp.ErrExecutionSnapshotInvalid
	}
	reconciled := false
	err := r.cell.WithAccountTx(ctx, command.Schedule.AccountID, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		matched, found, err := loadScheduleOccurrenceMatch(ctx, tx, command, "skipped")
		if err != nil {
			return err
		}
		if found {
			if !matched {
				return scheduleapp.ErrExecutionConflict
			}
			reconciled = true
			return nil
		}
		current, err := lockScheduleExecution(ctx, tx, command)
		if err != nil {
			return err
		}
		return completeScheduleOccurrence(ctx, tx, current, command, "skipped")
	})
	return reconciled, classifyScheduleExecution(err)
}

func (r *ScheduleExecutionRepository) Fail(ctx context.Context, claim scheduleapp.ExecutionClaim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	return r.queue.Fail(ctx, claim, retry, next, code, now, maxAttempts)
}

func (r *ScheduleExecutionQueueRepository) Fail(ctx context.Context, claim scheduleapp.ExecutionClaim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	query := `SELECT public.spyglass_fail_schedule_dispatch($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	arguments := []any{claim.AccountID, claim.ScheduleID, claim.LeaseID, claim.ScheduledFor, retry, next.UTC(), code, now.UTC(), maxAttempts}
	if claim.ExecutionKind() == "triggered" {
		query = `SELECT public.spyglass_fail_schedule_trigger($1,$2,$3,$4,$5,$6,$7,$8,$9)`
		arguments = []any{claim.AccountID, claim.TriggerID, claim.LeaseID, claim.ScheduledFor, retry, next.UTC(), code, now.UTC(), maxAttempts}
	}
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(&state)
	return state, classifyScheduleExecution(err)
}

func (r *ScheduleExecutionQueueRepository) Stats(ctx context.Context, now time.Time) (scheduleapp.ExecutionStats, error) {
	result, oldest, err := r.stats(ctx, `SELECT pending,ready,leased,retrying,dead_letter,oldest_ready_at FROM public.spyglass_schedule_dispatch_stats($1)`, now)
	if err != nil {
		return scheduleapp.ExecutionStats{}, err
	}
	trigger, triggerOldest, err := r.stats(ctx, `SELECT pending,ready,leased,retrying,dead_letter,oldest_ready_at FROM public.spyglass_schedule_trigger_stats($1)`, now)
	if err != nil {
		return scheduleapp.ExecutionStats{}, err
	}
	result.Pending += trigger.Pending
	result.Ready += trigger.Ready
	result.Leased += trigger.Leased
	result.Retrying += trigger.Retrying
	result.DeadLetter += trigger.DeadLetter
	if oldest == nil || triggerOldest != nil && triggerOldest.Before(*oldest) {
		oldest = triggerOldest
	}
	if oldest != nil && oldest.Before(now) {
		result.OldestReadyAge = now.Sub(*oldest)
	}
	return result, nil
}

func (r *ScheduleExecutionQueueRepository) stats(ctx context.Context, query string, now time.Time) (scheduleapp.ExecutionStats, *time.Time, error) {
	var result scheduleapp.ExecutionStats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, query, now.UTC()).Scan(&result.Pending, &result.Ready, &result.Leased,
		&result.Retrying, &result.DeadLetter, &oldest)
	if err != nil {
		return scheduleapp.ExecutionStats{}, nil, classifyScheduleExecution(err)
	}
	return result, oldest, nil
}

func lockScheduleExecution(ctx context.Context, tx pgx.Tx, command scheduleapp.OccurrenceCommand) (scheduledomain.Schedule, error) {
	current, err := loadSchedule(ctx, tx, command.Claim.AccountID, command.Claim.ScheduleID, true)
	if err != nil {
		return scheduledomain.Schedule{}, err
	}
	if !reflect.DeepEqual(current, command.Schedule) || current.State != scheduledomain.StateActive || current.Version != command.Claim.ScheduleVersion ||
		(command.Claim.ExecutionKind() == "scheduled" && (current.NextRunAt == nil || !current.NextRunAt.Equal(command.Claim.ScheduledFor))) {
		return scheduledomain.Schedule{}, scheduleapp.ErrExecutionConflict
	}
	var leased bool
	query := `SELECT true FROM spyglass.schedule_dispatch_queue
		WHERE account_id=$1 AND schedule_id=$2 AND schedule_version=$3 AND state='leased' AND lease_id=$4
		  AND scheduled_for=$5 AND lease_expires_at>=statement_timestamp() FOR UPDATE`
	arguments := []any{command.Claim.AccountID, command.Claim.ScheduleID, command.Claim.ScheduleVersion, command.Claim.LeaseID, command.Claim.ScheduledFor}
	if command.Claim.ExecutionKind() == "triggered" {
		query = `SELECT true FROM spyglass.schedule_trigger_queue
			WHERE account_id=$1 AND trigger_id=$2 AND schedule_id=$3 AND schedule_version=$4 AND state='leased' AND lease_id=$5
			  AND requested_for=$6 AND lease_expires_at>=statement_timestamp() FOR UPDATE`
		arguments = []any{command.Claim.AccountID, command.Claim.TriggerID, command.Claim.ScheduleID, command.Claim.ScheduleVersion, command.Claim.LeaseID, command.Claim.ScheduledFor}
	}
	if err := tx.QueryRow(ctx, query, arguments...).Scan(&leased); errors.Is(err, pgx.ErrNoRows) {
		return scheduledomain.Schedule{}, scheduleapp.ErrExecutionLeaseLost
	} else if err != nil {
		return scheduledomain.Schedule{}, err
	}
	if !leased {
		return scheduledomain.Schedule{}, scheduleapp.ErrExecutionLeaseLost
	}
	return current, nil
}

func completeScheduleOccurrence(ctx context.Context, tx pgx.Tx, current scheduledomain.Schedule, command scheduleapp.OccurrenceCommand, outcome string) error {
	var runID, conversationID any
	if outcome == "dispatched" {
		runID, conversationID = command.RunID, command.ConversationID
	}
	if command.Claim.ExecutionKind() == "triggered" {
		if outcome != "dispatched" {
			return scheduleapp.ErrExecutionSnapshotInvalid
		}
		var requestedBy ids.UserID
		if err := tx.QueryRow(ctx, `SELECT requested_by_user_id FROM spyglass.schedule_triggers
			WHERE account_id=$1 AND id=$2 AND schedule_id=$3 AND schedule_version=$4`, current.AccountID, command.Claim.TriggerID,
			current.ID, current.Version).Scan(&requestedBy); errors.Is(err, pgx.ErrNoRows) {
			return scheduleapp.ErrExecutionConflict
		} else if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_occurrences
			(account_id,id,schedule_id,schedule_version,scheduled_for,outcome,run_id,conversation_id,created_by_user_id,initiated_by_kind,initiated_by_id,occurred_at,occurrence_kind,trigger_id,requested_by_user_id)
			VALUES ($1,$2,$3,$4,$5,'dispatched',$6,$7,$8,'workload','schedule-execution-worker',$9,'triggered',$10,$11)`, current.AccountID,
			command.OccurrenceID, current.ID, current.Version, command.Claim.ScheduledFor, runID, conversationID, current.CreatedBy,
			command.At, command.Claim.TriggerID, requestedBy); err != nil {
			return err
		}
		eventID, err := ids.Derive(command.OccurrenceID, "schedule-event")
		if err != nil {
			return scheduleapp.ErrExecutionSnapshotInvalid
		}
		payload, err := json.Marshal(map[string]any{"outcome": outcome, "kind": "triggered"})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_events
			(account_id,id,schedule_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
			VALUES ($1,$2,$3,'trigger_dispatched',$4,$4,'workload','schedule-execution-worker',$5,$6,$7,$8)`, current.AccountID,
			eventID, current.ID, current.Version, "Triggered occurrence dispatched", command.Claim.TriggerID, payload, command.At); err != nil {
			return err
		}
		deleted, err := tx.Exec(ctx, `DELETE FROM spyglass.schedule_trigger_queue WHERE account_id=$1 AND trigger_id=$2 AND state='leased'
			AND lease_id=$3 AND requested_for=$4`, current.AccountID, command.Claim.TriggerID, command.Claim.LeaseID, command.Claim.ScheduledFor)
		if err != nil {
			return err
		}
		if deleted.RowsAffected() != 1 {
			return scheduleapp.ErrExecutionLeaseLost
		}
		return nil
	}
	advanced, err := current.AdvanceOccurrence(current.Version, command.NextRunAt, command.At)
	if err != nil {
		return scheduleapp.ErrExecutionSnapshotInvalid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_occurrences
		(account_id,id,schedule_id,schedule_version,scheduled_for,outcome,run_id,conversation_id,created_by_user_id,initiated_by_kind,initiated_by_id,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'workload','schedule-execution-worker',$10)`, current.AccountID, command.OccurrenceID,
		current.ID, current.Version, command.Claim.ScheduledFor, outcome, runID, conversationID, current.CreatedBy, command.At); err != nil {
		return err
	}
	eventID, err := ids.Derive(command.OccurrenceID, "schedule-event")
	if err != nil {
		return scheduleapp.ErrExecutionSnapshotInvalid
	}
	payload, err := json.Marshal(map[string]any{"outcome": outcome})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO spyglass.schedule_events
		(account_id,id,schedule_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,'workload','schedule-execution-worker',$7,$8,$9,$10)`, current.AccountID, eventID, current.ID,
		"occurrence_"+outcome, current.Version, advanced.Version, "Scheduled occurrence "+outcome, command.OccurrenceID, payload, command.At); err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `UPDATE spyglass.schedules SET version=$3,next_run_at=$4,updated_at=$5
		WHERE account_id=$1 AND id=$2 AND version=$6`, current.AccountID, current.ID, advanced.Version, advanced.NextRunAt, advanced.UpdatedAt, current.Version)
	if err != nil {
		return err
	}
	if commandTag.RowsAffected() != 1 {
		return scheduleapp.ErrExecutionConflict
	}
	return nil
}

func loadScheduleOccurrenceMatch(ctx context.Context, tx pgx.Tx, command scheduleapp.OccurrenceCommand, outcome string) (bool, bool, error) {
	var storedOutcome, occurrenceKind string
	var scheduleID ids.ScheduleID
	var version uint64
	var scheduledFor time.Time
	var runID, conversationID, triggerID *string
	err := tx.QueryRow(ctx, `SELECT schedule_id,schedule_version,scheduled_for,outcome,run_id::text,conversation_id::text,occurrence_kind,trigger_id::text
		FROM spyglass.schedule_occurrences WHERE account_id=$1 AND id=$2`, command.Claim.AccountID, command.OccurrenceID).Scan(
		&scheduleID, &version, &scheduledFor, &storedOutcome, &runID, &conversationID, &occurrenceKind, &triggerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	matched := scheduleID == command.Claim.ScheduleID && version == command.Claim.ScheduleVersion && scheduledFor.Equal(command.Claim.ScheduledFor) &&
		storedOutcome == outcome && occurrenceKind == command.Claim.ExecutionKind()
	if command.Claim.ExecutionKind() == "triggered" {
		matched = matched && triggerID != nil && *triggerID == command.Claim.TriggerID
	} else {
		matched = matched && triggerID == nil
	}
	if outcome == "dispatched" {
		matched = matched && runID != nil && conversationID != nil && *runID == string(command.RunID) && *conversationID == string(command.ConversationID)
	} else {
		matched = matched && runID == nil && conversationID == nil
	}
	return matched, true, nil
}

func mapAgentStartToScheduleExecution(err error) error {
	switch {
	case errors.Is(err, agentapp.ErrNotFound), errors.Is(err, agentapp.ErrConstraint), errors.Is(err, agentapp.ErrConflict):
		return scheduleapp.ErrExecutionAuthorizationStale
	case errors.Is(err, agentapp.ErrCorrupt):
		return scheduleapp.ErrExecutionSnapshotInvalid
	default:
		return err
	}
}

func classifyScheduleExecution(err error) error {
	if err == nil || errors.Is(err, scheduleapp.ErrExecutionClaimInvalid) || errors.Is(err, scheduleapp.ErrExecutionSnapshotInvalid) ||
		errors.Is(err, scheduleapp.ErrExecutionAuthorization) || errors.Is(err, scheduleapp.ErrExecutionAuthorizationStale) ||
		errors.Is(err, scheduleapp.ErrExecutionLeaseLost) || errors.Is(err, scheduleapp.ErrExecutionConflict) || errors.Is(err, scheduleapp.ErrExecutionCapacity) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "P0001":
			return scheduleapp.ErrExecutionLeaseLost
		case "22023", "23503", "23514":
			return scheduleapp.ErrExecutionSnapshotInvalid
		case "23505", "40001":
			return scheduleapp.ErrExecutionConflict
		}
	}
	classified := classifySchedule(err)
	if errors.Is(classified, scheduleapp.ErrConflict) {
		return scheduleapp.ErrExecutionConflict
	}
	if errors.Is(classified, scheduleapp.ErrInvalid) || errors.Is(classified, scheduleapp.ErrNotFound) {
		return scheduleapp.ErrExecutionSnapshotInvalid
	}
	return classified
}

var _ scheduleapp.ExecutionStore = (*ScheduleExecutionRepository)(nil)
