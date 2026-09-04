package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	workagent "github.com/tinfoyle/spyglass-engine/internal/application/workagentexecution"
	agentdomain "github.com/tinfoyle/spyglass-engine/internal/modules/agents"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// WorkAgentExecutionRepository has identifier-only cross-Account queue access.
// Snapshot reads are RLS scoped, and every mutation is an execute-only function.
type WorkAgentExecutionRepository struct {
	pool *pgxpool.Pool
	cell *database.CellPool
}

func NewWorkAgentExecutionRepository(pool *pgxpool.Pool, cell *database.CellPool) (*WorkAgentExecutionRepository, error) {
	if pool == nil || cell == nil {
		return nil, errors.New("Work Agent execution repository dependencies are required")
	}
	return &WorkAgentExecutionRepository{pool: pool, cell: cell}, nil
}

func (r *WorkAgentExecutionRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (workagent.Claim, bool, error) {
	var claim workagent.Claim
	err := r.pool.QueryRow(ctx, `SELECT execution_id,account_id,work_item_id,lease_id,attempt_count
		FROM public.spyglass_claim_work_agent_execution($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.ExecutionID, &claim.AccountID, &claim.WorkItemID, &claim.LeaseID, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return workagent.Claim{}, false, nil
	}
	if err != nil {
		return workagent.Claim{}, false, mapWorkAgentExecutionError("claim Work Agent execution", err)
	}
	return claim, true, nil
}

func (r *WorkAgentExecutionRepository) Load(ctx context.Context, claim workagent.Claim) (workagent.Snapshot, error) {
	var snapshot workagent.Snapshot
	snapshot.ExecutionID, snapshot.AccountID, snapshot.WorkItemID = claim.ExecutionID, claim.AccountID, claim.WorkItemID
	var personaVersionID ids.PersonaVersionID
	err := r.pool.QueryRow(ctx, `SELECT work_version,initiating_user_id,persona_id,persona_version_id,boardroom_id,
		boardroom_version,planned_run_id,planned_conversation_id,title,description,queued_at
		FROM public.spyglass_load_work_agent_execution($1,$2,$3,$4)`, claim.ExecutionID, claim.AccountID, claim.WorkItemID, claim.LeaseID).Scan(
		&snapshot.WorkVersion, &snapshot.UserID, &snapshot.PersonaID, &personaVersionID, &snapshot.BoardroomID,
		&snapshot.BoardroomVersion, &snapshot.RunID, &snapshot.ConversationID, &snapshot.Title, &snapshot.Description, &snapshot.QueuedAt)
	if err != nil {
		return workagent.Snapshot{}, mapWorkAgentExecutionError("load Work Agent execution intent", err)
	}
	err = r.cell.WithAccountTx(ctx, claim.AccountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		persona, found, err := loadPersonaVersion(ctx, tx, claim.AccountID, personaVersionID)
		if err != nil {
			return err
		}
		if !found || persona.PersonaID != snapshot.PersonaID {
			return workagent.ErrInvalidSnapshot
		}
		snapshot.Persona = persona
		return nil
	})
	if err != nil {
		if errors.Is(err, workagent.ErrLeaseLost) || errors.Is(err, workagent.ErrInvalidSnapshot) {
			return workagent.Snapshot{}, err
		}
		return workagent.Snapshot{}, fmt.Errorf("load Work Agent execution snapshot: %w", err)
	}
	return snapshot, nil
}

func (r *WorkAgentExecutionRepository) Heartbeat(ctx context.Context, claim workagent.Claim, now time.Time, lease time.Duration) error {
	var accepted bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_heartbeat_work_agent_execution($1,$2,$3,$4,$5)`,
		claim.ExecutionID, claim.AccountID, claim.LeaseID, now.UTC(), int(lease/time.Second)).Scan(&accepted)
	if err != nil {
		return mapWorkAgentExecutionError("heartbeat Work Agent execution", err)
	}
	if !accepted {
		return workagent.ErrLeaseLost
	}
	return nil
}

func (r *WorkAgentExecutionRepository) StartLink(ctx context.Context, command workagent.StartLinkCommand) (workagent.StartLinkResult, error) {
	snapshot := command.Snapshot
	turn := agentdomain.PlannedTurn{Turn: 1, PersonaID: snapshot.PersonaID, PersonaVersionID: snapshot.Persona.ID, PersonaDigest: snapshot.Persona.ContentDigest}
	plan, err := agentdomain.NewRunPlan(agentdomain.RunPlan{
		RunID: snapshot.RunID, AccountID: snapshot.AccountID, BoardroomID: snapshot.BoardroomID,
		ConversationID: snapshot.ConversationID, EntitlementVersion: command.Authorization.EntitlementVersion,
		PolicyVersion: snapshot.BoardroomVersion, Turns: []agentdomain.PlannedTurn{turn}, CreatedBy: snapshot.UserID, CreatedAt: command.At,
	})
	if err != nil {
		return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
	}
	eventID, err := ids.Derive(snapshot.ExecutionID, "work-event/linked")
	if err != nil {
		return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
	}
	messageID, err := ids.Derive(string(snapshot.RunID), "user-message")
	if err != nil {
		return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
	}
	invocationRaw, err := ids.Derive(string(snapshot.RunID), "turn/1/invocation")
	if err != nil {
		return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
	}
	modelTargets := []string{"pending-a", "pending-b", "pending-c"}
	modelIDs := make([]string, (snapshot.Persona.Policy.MaximumToolSteps+1)*(agentdomain.MaximumFallbackModels+1))
	toolIDs := make([]string, snapshot.Persona.Policy.MaximumToolSteps)
	for index := range modelIDs {
		modelIDs[index], err = ids.Derive(invocationRaw, fmt.Sprintf("model/%d", index+1))
		if err != nil {
			return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
		}
	}
	for index := range toolIDs {
		toolIDs[index], err = ids.Derive(invocationRaw, fmt.Sprintf("tool/%d", index+1))
		if err != nil {
			return workagent.StartLinkResult{}, workagent.ErrInvalidSnapshot
		}
	}
	var result workagent.StartLinkResult
	err = r.pool.QueryRow(ctx, `SELECT created_run,linked_run,reconciled
		FROM public.spyglass_start_link_work_agent_execution($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		snapshot.ExecutionID, snapshot.AccountID, command.Claim.LeaseID, command.Authorization.EntitlementVersion,
		command.Authorization.MaximumConcurrentRun, plan.Digest[:], eventID, messageID, invocationRaw,
		profileFor(snapshot.Persona.Policy), modelTargets, modelIDs, toolIDs, command.At.Add(24*time.Hour), command.At.UTC()).Scan(
		&result.CreatedRun, &result.LinkedRun, &result.Reconciled)
	if err != nil {
		return workagent.StartLinkResult{}, mapWorkAgentExecutionError("start/link Work Agent execution", err)
	}
	return result, nil
}

func (r *WorkAgentExecutionRepository) Fail(ctx context.Context, claim workagent.Claim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_fail_work_agent_execution($1,$2,$3,$4,$5,$6,$7,$8)`,
		claim.ExecutionID, claim.AccountID, claim.LeaseID, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", mapWorkAgentExecutionError("fail Work Agent execution", err)
	}
	return state, nil
}

func (r *WorkAgentExecutionRepository) Stats(ctx context.Context, now time.Time) (workagent.Stats, error) {
	var result workagent.Stats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,linked,dead_letter,oldest_ready_at
		FROM public.spyglass_work_agent_execution_stats($1)`, now.UTC()).Scan(
		&result.Pending, &result.Ready, &result.Leased, &result.Retrying, &result.Linked, &result.DeadLetter, &oldest)
	if err != nil {
		return workagent.Stats{}, mapWorkAgentExecutionError("read Work Agent execution stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func mapWorkAgentExecutionError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "P0001":
			return workagent.ErrLeaseLost
		case "P0002", "22023", "23505", "23514", "23503":
			return workagent.ErrInvalidSnapshot
		case "P0003":
			return workagent.ErrWorkChanged
		case "P0004":
			return workagent.ErrPersonaUnavailable
		case "P0005":
			return workagent.ErrRunCapacity
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
