package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentdispatch"
	"github.com/tinfoyle/spyglass-engine/internal/application/modelgateway"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// AgentDispatchRepository combines an identifier-only cross-Account claim
// pool with Account-scoped snapshot reads. Its role may read immutable Agent
// planning tables but receives no mutation grants on customer data.
type AgentDispatchRepository struct {
	pool *pgxpool.Pool
	cell *database.CellPool
}

func NewAgentDispatchRepository(pool *pgxpool.Pool, cell *database.CellPool) (*AgentDispatchRepository, error) {
	if pool == nil || cell == nil {
		return nil, errors.New("agent dispatch repository dependencies are required")
	}
	return &AgentDispatchRepository{pool: pool, cell: cell}, nil
}

func (r *AgentDispatchRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (agentdispatch.Claim, bool, error) {
	var claim agentdispatch.Claim
	err := r.pool.QueryRow(ctx, `SELECT account_id,invocation_id,lease_id,attempt_count
		FROM public.spyglass_claim_agent_dispatch($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.AccountID, &claim.InvocationID, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentdispatch.Claim{}, false, nil
	}
	if err != nil {
		return agentdispatch.Claim{}, false, mapAgentDispatchError("claim agent dispatch", err)
	}
	return claim, true, nil
}

func (r *AgentDispatchRepository) Load(ctx context.Context, claim agentdispatch.Claim) (agentdispatch.Snapshot, error) {
	var result agentdispatch.Snapshot
	result.AccountID, result.InvocationID = claim.AccountID, claim.InvocationID
	err := r.cell.WithAccountTx(ctx, claim.AccountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var personaVersionID ids.PersonaVersionID
		var conversationID ids.ConversationID
		var contextSequence int64
		var contextDigest []byte
		err := tx.QueryRow(ctx, `SELECT e.conversation_id,e.context_sequence,e.profile,e.model_operation_ids,e.tool_operation_ids,
			e.request_expires_at,i.queued_at,i.persona_version_id,r.context_payload,r.context_digest,r.context_item_count
			FROM spyglass.agent_invocation_execution_plans e JOIN spyglass.agent_invocations i
			ON i.account_id=e.account_id AND i.id=e.invocation_id
			JOIN spyglass.agent_runs r ON r.account_id=i.account_id AND r.id=i.run_id
			WHERE e.account_id=$1 AND e.invocation_id=$2 AND i.status='queued'`, claim.AccountID, claim.InvocationID).Scan(
			&conversationID, &contextSequence, &result.Profile, &result.ModelOperationIDs, &result.ToolOperationIDs,
			&result.RequestExpiresAt, &result.QueuedAt, &personaVersionID, &result.ContextPayload, &contextDigest, &result.ContextItemCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return agentdispatch.ErrInvalidSnapshot
		}
		if err != nil {
			return err
		}
		if _, digest, err := decodeAgentContext(result.ContextPayload, contextDigest, result.ContextItemCount); err != nil {
			return agentdispatch.ErrInvalidSnapshot
		} else {
			result.ContextDigest = digest
		}
		persona, found, err := loadPersonaVersion(ctx, tx, claim.AccountID, personaVersionID)
		if err != nil {
			return err
		}
		if !found {
			return agentdispatch.ErrInvalidSnapshot
		}
		result.Persona = persona
		rows, err := tx.Query(ctx, `SELECT role,body FROM (
			SELECT role,body,sequence FROM (
				SELECT 'user'::text AS role,u.body,u.sequence FROM spyglass.agent_user_messages u
				WHERE u.account_id=$1 AND u.conversation_id=$2 AND u.sequence<=$3
				UNION ALL
				SELECT 'assistant'::text AS role,m.body,m.sequence FROM spyglass.agent_messages m
				WHERE m.account_id=$1 AND m.conversation_id=$2 AND m.sequence<=$3
			) history ORDER BY sequence DESC LIMIT $4
		) bounded ORDER BY sequence`, claim.AccountID, conversationID, contextSequence, modelgateway.MaximumMessages)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var message modelgateway.Message
			if err := rows.Scan(&message.Role, &message.Content); err != nil {
				return err
			}
			result.Messages = append(result.Messages, message)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(result.Messages) == 0 || !slices.ContainsFunc(result.Messages, func(message modelgateway.Message) bool { return message.Role == "user" }) {
			return agentdispatch.ErrInvalidSnapshot
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, agentdispatch.ErrInvalidSnapshot) || errors.Is(err, agentdispatch.ErrLeaseLost) {
			return agentdispatch.Snapshot{}, err
		}
		return agentdispatch.Snapshot{}, fmt.Errorf("load agent dispatch snapshot: %w", err)
	}
	return result, nil
}

func (r *AgentDispatchRepository) Complete(ctx context.Context, claim agentdispatch.Claim, digest [32]byte, now time.Time) error {
	var created bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_complete_agent_dispatch($1,$2,$3,$4,$5)`,
		claim.AccountID, claim.InvocationID, claim.LeaseID, digest[:], now.UTC()).Scan(&created)
	if err != nil {
		return mapAgentDispatchError("complete agent dispatch", err)
	}
	_ = created
	return nil
}

func (r *AgentDispatchRepository) Fail(ctx context.Context, claim agentdispatch.Claim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_fail_agent_dispatch($1,$2,$3,$4,$5,$6,$7,$8)`,
		claim.AccountID, claim.InvocationID, claim.LeaseID, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", mapAgentDispatchError("fail agent dispatch", err)
	}
	return state, nil
}

func (r *AgentDispatchRepository) Stats(ctx context.Context, now time.Time) (agentdispatch.Stats, error) {
	var result agentdispatch.Stats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,provisioned,dead_letter,oldest_ready_at
		FROM public.spyglass_agent_dispatch_stats($1)`, now.UTC()).Scan(&result.Pending, &result.Ready, &result.Leased,
		&result.Retrying, &result.Provisioned, &result.DeadLetter, &oldest)
	if err != nil {
		return agentdispatch.Stats{}, mapAgentDispatchError("read agent dispatch stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func mapAgentDispatchError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch {
		case postgresError.Code == "P0001" && postgresError.Message == "agent dispatch lease lost":
			return agentdispatch.ErrLeaseLost
		case postgresError.Code == "22023" || postgresError.Code == "P0002" || postgresError.Code == "23505":
			return agentdispatch.ErrInvalidSnapshot
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ agentdispatch.Queue = (*AgentDispatchRepository)(nil)
