package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/agentprojection"
	"github.com/tinfoyle/spyglass-engine/internal/application/agentresultpolicy"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// AgentProjectionRepository has execute-only authority. Its pool must not be
// granted SELECT, INSERT, UPDATE, or DELETE on customer-owned Agent tables.
type AgentProjectionRepository struct{ pool *pgxpool.Pool }

func NewAgentProjectionRepository(pool *pgxpool.Pool) (*AgentProjectionRepository, error) {
	if pool == nil {
		return nil, errors.New("agent projection database pool is required")
	}
	return &AgentProjectionRepository{pool: pool}, nil
}

func (r *AgentProjectionRepository) Claim(ctx context.Context, leaseID string, now time.Time, lease time.Duration) (agentprojection.Claim, bool, error) {
	var claim agentprojection.Claim
	var digest []byte
	var resultPolicyVersion int64
	var delegateIDs, citationBindings []string
	err := r.pool.QueryRow(ctx, `SELECT account_id,invocation_id,lease_id,attempt_count,expected_provider,requested_model,permitted_models,
		result_policy_version,citation_policy,action_policy,current_persona_id,delegate_persona_ids,citation_bindings,
		pod_uid,result_outcome,result_ciphertext,result_nonce,result_key_version,result_digest,result_submitted_at
		FROM public.spyglass_claim_agent_result_projection_v2($1,$2,$3)`, leaseID, now.UTC(), int(lease/time.Second)).Scan(
		&claim.AccountID, &claim.InvocationID, &claim.LeaseID, &claim.Attempt, &claim.ExpectedProvider, &claim.RequestedModel, &claim.PermittedModels,
		&resultPolicyVersion, &claim.CitationPolicy, &claim.ActionPolicy, &claim.CurrentPersonaID, &delegateIDs, &citationBindings,
		&claim.Result.PodUID, &claim.Result.Outcome, &claim.Result.Ciphertext, &claim.Result.Nonce, &claim.Result.KeyVersion,
		&digest, &claim.Result.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return agentprojection.Claim{}, false, nil
	}
	if err != nil {
		return agentprojection.Claim{}, false, mapAgentProjectionError("claim agent result projection", err)
	}
	claim.Result.InvocationID = claim.InvocationID
	if len(digest) != len(claim.Result.Digest) || resultPolicyVersion < 1 || resultPolicyVersion > math.MaxUint32 {
		return claim, true, agentprojection.ErrInvalidClaim
	}
	claim.ResultPolicyVersion = uint32(resultPolicyVersion)
	claim.DelegatePersonaIDs = make([]ids.PersonaID, len(delegateIDs))
	for index, raw := range delegateIDs {
		claim.DelegatePersonaIDs[index] = ids.PersonaID(raw)
	}
	claim.CitationBindings = make([]agentresultpolicy.CitationBinding, len(citationBindings))
	for index, raw := range citationBindings {
		documentID, chunkID, found := strings.Cut(raw, ":")
		if !found {
			return claim, true, agentprojection.ErrInvalidClaim
		}
		claim.CitationBindings[index] = agentresultpolicy.CitationBinding{DocumentID: documentID, ChunkID: chunkID}
	}
	copy(claim.Result.Digest[:], digest)
	return claim, true, nil
}

func (r *AgentProjectionRepository) ProjectSuccess(ctx context.Context, result agentprojection.Success) error {
	var projected bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_success(
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb,$12,$13,$14,$15,$16,$17,$18)`,
		result.Claim.AccountID, result.Claim.InvocationID, result.Claim.LeaseID, result.MessageID, result.Provider,
		result.SelectedModel, result.ResponseModel, result.ProviderResponseID, result.RunnerDigest[:], result.ResultDigest[:], result.ResultPayload,
		result.Body, result.InputTokens, result.OutputTokens, result.TotalTokens, result.CostMicros, result.CompletedAt.UTC(), result.ProjectedAt.UTC()).Scan(&projected)
	if err != nil {
		return mapAgentProjectionError("project agent invocation success", err)
	}
	_ = projected // false is an exact idempotent replay of an already committed projection.
	return nil
}

func (r *AgentProjectionRepository) ProjectFailure(ctx context.Context, result agentprojection.Failure) error {
	var projected bool
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_project_agent_invocation_failure($1,$2,$3,$4,$5,$6,$7)`,
		result.Claim.AccountID, result.Claim.InvocationID, result.Claim.LeaseID, result.RunnerDigest[:], result.FailureCode,
		result.CompletedAt.UTC(), result.ProjectedAt.UTC()).Scan(&projected)
	if err != nil {
		return mapAgentProjectionError("project agent invocation failure", err)
	}
	_ = projected
	return nil
}

func (r *AgentProjectionRepository) Fail(ctx context.Context, claim agentprojection.Claim, retry bool, next time.Time, code string, now time.Time, maxAttempts int) (string, error) {
	var state string
	err := r.pool.QueryRow(ctx, `SELECT public.spyglass_fail_agent_result_projection($1,$2,$3,$4,$5,$6,$7,$8)`,
		claim.AccountID, claim.InvocationID, claim.LeaseID, retry, next.UTC(), code, now.UTC(), maxAttempts).Scan(&state)
	if err != nil {
		return "", mapAgentProjectionError("fail agent result projection", err)
	}
	return state, nil
}

func (r *AgentProjectionRepository) Stats(ctx context.Context, now time.Time) (agentprojection.Stats, error) {
	var result agentprojection.Stats
	var oldest *time.Time
	err := r.pool.QueryRow(ctx, `SELECT pending,ready,leased,retrying,dead_letter,oldest_ready_at
		FROM public.spyglass_agent_result_projection_stats($1)`, now.UTC()).Scan(
		&result.Pending, &result.Ready, &result.Leased, &result.Retrying, &result.DeadLetter, &oldest)
	if err != nil {
		return agentprojection.Stats{}, mapAgentProjectionError("read agent result projection stats", err)
	}
	if oldest != nil && now.After(*oldest) {
		result.OldestReadyAge = now.Sub(*oldest).Round(time.Second)
	}
	return result, nil
}

func mapAgentProjectionError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch {
		case postgresError.Code == "P0001" && postgresError.Message == "agent result projection lease lost":
			return agentprojection.ErrLeaseLost
		case postgresError.Code == "22023":
			return agentprojection.ErrInvalidPayload
		case postgresError.Code == "P0002" || postgresError.Code == "23505":
			return agentprojection.ErrInvalidPayload
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ agentprojection.Queue = (*AgentProjectionRepository)(nil)
