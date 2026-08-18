package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentqueueadmin"
)

// AgentQueueAdminRepository has no table grants; audited security-definer
// functions are its complete database authority.
type AgentQueueAdminRepository struct{ pool *pgxpool.Pool }

func NewAgentQueueAdminRepository(pool *pgxpool.Pool) *AgentQueueAdminRepository {
	return &AgentQueueAdminRepository{pool: pool}
}

func (r *AgentQueueAdminRepository) Inspect(ctx context.Context, queue string, limit int, change agentqueueadmin.Change) ([]agentqueueadmin.DeadLetter, error) {
	rows, err := r.pool.Query(ctx, `SELECT queue_name,account_id,invocation_id,attempt_count,last_error_code,created_at,updated_at
		FROM public.spyglass_inspect_agent_queue_dead_letters($1,$2,$3,$4,$5,$6)`,
		change.BatchID, queue, change.Actor, change.Reason, change.Environment, limit)
	if err != nil {
		return nil, classifyAgentQueueAdminError(err)
	}
	defer rows.Close()
	records := make([]agentqueueadmin.DeadLetter, 0, limit)
	for rows.Next() {
		record, err := scanAgentQueueDeadLetter(rows, false)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyAgentQueueAdminError(err)
	}
	return records, nil
}

func (r *AgentQueueAdminRepository) Requeue(ctx context.Context, target agentqueueadmin.Target, change agentqueueadmin.Change) (agentqueueadmin.DeadLetter, error) {
	row := r.pool.QueryRow(ctx, `SELECT queue_name,account_id,invocation_id,previous_attempt_count,previous_error_code,created_at,updated_at,next_attempt_at
		FROM public.spyglass_requeue_agent_queue_dead_letter($1,$2,$3,$4,$5,$6,$7)`,
		change.BatchID, target.Queue, target.AccountID, target.InvocationID, change.Actor, change.Reason, change.Environment)
	record, err := scanAgentQueueDeadLetter(row, true)
	if err != nil {
		return agentqueueadmin.DeadLetter{}, classifyAgentQueueAdminError(err)
	}
	return record, nil
}

func scanAgentQueueDeadLetter(row pgx.Row, next bool) (agentqueueadmin.DeadLetter, error) {
	var record agentqueueadmin.DeadLetter
	var lastError *string
	values := []any{&record.Queue, &record.AccountID, &record.InvocationID, &record.AttemptCount, &lastError, &record.CreatedAt, &record.UpdatedAt}
	if next {
		values = append(values, &record.NextAttemptAt)
	}
	if err := row.Scan(values...); err != nil {
		return agentqueueadmin.DeadLetter{}, fmt.Errorf("scan Agent queue dead letter: %w", err)
	}
	if lastError != nil {
		record.LastErrorCode = *lastError
	}
	return record, nil
}

func classifyAgentQueueAdminError(err error) error {
	if err == nil {
		return nil
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "22023":
			return agentqueueadmin.ErrInvalidChange
		case "P0002":
			return agentqueueadmin.ErrNotFound
		case "P0001":
			return agentqueueadmin.ErrStateConflict
		}
	}
	return fmt.Errorf("Agent queue administration unavailable: %w", err)
}

var _ agentqueueadmin.Store = (*AgentQueueAdminRepository)(nil)
