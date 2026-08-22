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
	pool *pgxpool.Pool
	cell *database.CellPool
}

func NewScheduleExecutionRepository(pool *pgxpool.Pool, cell *database.CellPool) (*ScheduleExecutionRepository, error) {
	if pool == nil || cell == nil {
		return nil, errors.New("Schedule execution repository dependencies are required")
	}
	return &ScheduleExecutionRepository{pool: pool, cell: cell}, nil
}

func (r *ScheduleExecutionRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (scheduleapp.ExecutionClaim, bool, error) {
	var claim scheduleapp.ExecutionClaim
	err := r.pool.QueryRow(ctx, `SELECT account_id,schedule_id,schedule_version,scheduled_for,lease_id,attempt_count
		FROM public.spyglass_claim_schedule_dispatch($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.AccountID, &claim.ScheduleID, &claim.ScheduleVersion, &claim.ScheduledFor, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return scheduleapp.ExecutionClaim{}, false, nil
	}
	if err != nil {
		return scheduleapp.ExecutionClaim{}, false, classifyScheduleExecution(err)
	}
	return claim, true, nil
}

func (r *ScheduleExecutionRepository) Load(ctx context.Context, claim scheduleapp.ExecutionClaim) (scheduleapp.ExecutionSnapshot, error) {
	if !claim.Valid() {
		return scheduleapp.ExecutionSnapshot{}, scheduleapp.ErrExecutionClaimInvalid
	}
	var result scheduledomain.Schedule
	err := r.cell.WithAccountTx(ctx, claim.AccountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var leased bool
		if err := tx.QueryRow(ctx, `SELECT true FROM spyglass.schedule_dispatch_queue
			WHERE account_id=$1 AND schedule_id=$2 AND schedule_version=$3 AND state='leased' AND lease_id=$4
			  AND scheduled_for=$5 AND lease_expires_at>=statement_timestamp()`, claim.AccountID, claim.ScheduleID, claim.ScheduleVersion,
			claim.LeaseID, claim.ScheduledFor).Scan(&leased); errors.Is(err, pgx.ErrNoRows) {
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
	var accepted bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_heartbeat_schedule_dispatch($1,$2,$3,$4,$5,$6)`,
		claim.AccountID, claim.ScheduleID, claim.LeaseID, claim.ScheduledFor, now.UTC(), int(lease/time.Second)).Scan(&accepted)
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
	var state string
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_fail_schedule_dispatch($1,$2,$3,$4,$5,$6,$7,$8,$9)`, claim.AccountID,
		claim.ScheduleID, claim.LeaseID, claim.ScheduledFor, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	return state, classifyScheduleExecution(err)
}

func lockScheduleExecution(ctx context.Context, tx pgx.Tx, command scheduleapp.OccurrenceCommand) (scheduledomain.Schedule, error) {
	current, err := loadSchedule(ctx, tx, command.Claim.AccountID, command.Claim.ScheduleID, true)
	if err != nil {
		return scheduledomain.Schedule{}, err
	}
	if !reflect.DeepEqual(current, command.Schedule) || current.State != scheduledomain.StateActive || current.Version != command.Claim.ScheduleVersion ||
		current.NextRunAt == nil || !current.NextRunAt.Equal(command.Claim.ScheduledFor) {
		return scheduledomain.Schedule{}, scheduleapp.ErrExecutionConflict
	}
	var leased bool
	if err := tx.QueryRow(ctx, `SELECT true FROM spyglass.schedule_dispatch_queue
		WHERE account_id=$1 AND schedule_id=$2 AND schedule_version=$3 AND state='leased' AND lease_id=$4
		  AND scheduled_for=$5 AND lease_expires_at>=statement_timestamp() FOR UPDATE`, command.Claim.AccountID, command.Claim.ScheduleID,
		command.Claim.ScheduleVersion, command.Claim.LeaseID, command.Claim.ScheduledFor).Scan(&leased); errors.Is(err, pgx.ErrNoRows) {
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
	advanced, err := current.AdvanceOccurrence(current.Version, command.NextRunAt, command.At)
	if err != nil {
		return scheduleapp.ErrExecutionSnapshotInvalid
	}
	var runID, conversationID any
	if outcome == "dispatched" {
		runID, conversationID = command.RunID, command.ConversationID
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
	var storedOutcome string
	var scheduleID ids.ScheduleID
	var version uint64
	var scheduledFor time.Time
	var runID, conversationID *string
	err := tx.QueryRow(ctx, `SELECT schedule_id,schedule_version,scheduled_for,outcome,run_id::text,conversation_id::text
		FROM spyglass.schedule_occurrences WHERE account_id=$1 AND id=$2`, command.Claim.AccountID, command.OccurrenceID).Scan(
		&scheduleID, &version, &scheduledFor, &storedOutcome, &runID, &conversationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	matched := scheduleID == command.Claim.ScheduleID && version == command.Claim.ScheduleVersion && scheduledFor.Equal(command.Claim.ScheduledFor) && storedOutcome == outcome
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
