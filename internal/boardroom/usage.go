package boardroom

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/mainspring-engine/internal/agent"
	"github.com/tinfoyle/mainspring-engine/internal/domain"
)

var (
	ErrInvocationCapacity  = errors.New("tenant invocation capacity is currently full")
	ErrMonthlyTokenQuota   = errors.New("tenant monthly token allowance has been reached")
	ErrMonthlyCostQuota    = errors.New("tenant monthly cost allowance has been reached")
	ErrProviderCircuitOpen = errors.New("provider circuit is temporarily open")
)

type UsageService struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewUsageService(pool *pgxpool.Pool, logger *slog.Logger) *UsageService {
	return &UsageService{pool: pool, logger: logger}
}

func (s *UsageService) Reserve(ctx context.Context, invocationID domain.InvocationID, provider string, estimatedTokens, estimatedCostMicros int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('mainspring-tenant-admission'))`); err != nil {
		return err
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT status FROM usage_reservations WHERE invocation_id=$1`, invocationID.String()).Scan(&existing)
	if err == nil && (existing == "active" || existing == "reconciled") {
		return tx.Commit(ctx)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var maximum int
	var tokenLimit, costLimit int64
	if err := tx.QueryRow(ctx, `SELECT max_concurrent_invocations,monthly_token_limit,monthly_cost_limit_micros FROM tenant_execution_policy WHERE singleton`).Scan(&maximum, &tokenLimit, &costLimit); err != nil {
		return err
	}
	var active int
	var usedTokens, usedCost int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM usage_reservations WHERE status='active'`).Scan(&active); err != nil {
		return err
	}
	if active >= maximum {
		s.logger.Warn("tenant invocation capacity reached", "active", active, "limit", maximum, "provider", provider)
		return ErrInvocationCapacity
	}
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(input_tokens+output_tokens),0), COALESCE(sum(estimated_cost_micros),0)
		FROM agent_invocations WHERE completed_at >= date_trunc('month',now()) AND status='succeeded'
	`).Scan(&usedTokens, &usedCost); err != nil {
		return err
	}
	var reservedTokens, reservedCost int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(reserved_tokens),0), COALESCE(sum(reserved_cost_micros),0)
		FROM usage_reservations WHERE status='active'
	`).Scan(&reservedTokens, &reservedCost); err != nil {
		return err
	}
	if usedTokens+reservedTokens+estimatedTokens > tokenLimit {
		s.logger.Warn("tenant monthly token quota reached", "used", usedTokens, "reserved", reservedTokens, "requested", estimatedTokens, "limit", tokenLimit)
		return ErrMonthlyTokenQuota
	}
	if usedCost+reservedCost+estimatedCostMicros > costLimit {
		s.logger.Warn("tenant monthly cost quota reached", "used_micros", usedCost, "reserved_micros", reservedCost, "requested_micros", estimatedCostMicros, "limit_micros", costLimit)
		return ErrMonthlyCostQuota
	}
	var openUntil *time.Time
	if err := tx.QueryRow(ctx, `SELECT open_until FROM provider_circuits WHERE provider=$1`, provider).Scan(&openUntil); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if openUntil != nil && openUntil.After(time.Now()) {
		s.logger.Warn("provider circuit open", "provider", provider, "open_until", openUntil)
		return ErrProviderCircuitOpen
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO usage_reservations (invocation_id,provider,status,reserved_tokens,reserved_cost_micros)
		VALUES ($1,$2,'active',$3,$4)
		ON CONFLICT (invocation_id) DO UPDATE SET status='active',provider=EXCLUDED.provider,
			reserved_tokens=EXCLUDED.reserved_tokens,reserved_cost_micros=EXCLUDED.reserved_cost_micros,reconciled_at=NULL
	`, invocationID.String(), provider, estimatedTokens, estimatedCostMicros); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *UsageService) Reconcile(ctx context.Context, invocationID domain.InvocationID, usage agent.Usage, actualCostMicros int64) error {
	actualTokens := usage.InputTokens + usage.OutputTokens
	_, err := s.pool.Exec(ctx, `
		UPDATE usage_reservations SET status='reconciled',actual_tokens=$2,actual_cost_micros=$3,reconciled_at=now()
		WHERE invocation_id=$1
	`, invocationID.String(), actualTokens, actualCostMicros)
	return err
}

func (s *UsageService) Release(ctx context.Context, invocationID domain.InvocationID) {
	_, _ = s.pool.Exec(ctx, `UPDATE usage_reservations SET status='released',reconciled_at=now() WHERE invocation_id=$1 AND status='active'`, invocationID.String())
}

func (s *UsageService) RecordProviderResult(ctx context.Context, provider string, invocationErr error) {
	if invocationErr == nil {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO provider_circuits (provider) VALUES ($1)
			ON CONFLICT (provider) DO UPDATE SET consecutive_failures=0,open_until=NULL,last_error_category=NULL,last_error=NULL,updated_at=now()
		`, provider)
		return
	}
	category, _ := agent.Failure(invocationErr)
	s.logger.Warn("provider invocation failed", "provider", provider, "category", category, "error", invocationErr)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO provider_circuits (provider,consecutive_failures,last_error_category,last_error)
		VALUES ($1,1,$2,$3)
		ON CONFLICT (provider) DO UPDATE SET
			consecutive_failures=provider_circuits.consecutive_failures+1,
			open_until=CASE WHEN provider_circuits.consecutive_failures+1 >= 5 THEN now()+interval '2 minutes' ELSE provider_circuits.open_until END,
			last_error_category=$2,last_error=$3,updated_at=now()
	`, provider, category, invocationErr.Error())
	if err != nil {
		s.logger.Error("record provider circuit", "provider", provider, "error", err)
	}
}

type OperationsSnapshot struct {
	QueuedRuns, RunningRuns, AwaitingApproval, FailedRuns      int
	ActiveInvocations, CompletedInvocations, FailedInvocations int
	MonthlyTokens, MonthlyCostMicros                           int64
	AverageLatency                                             time.Duration
	ToolDenials                                                int
	ProviderCircuits                                           []ProviderCircuit
}

type ProviderCircuit struct {
	Provider            string
	ConsecutiveFailures int
	OpenUntil           *time.Time
	LastErrorCategory   string
}

func (s *UsageService) Snapshot(ctx context.Context) (OperationsSnapshot, error) {
	var snapshot OperationsSnapshot
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status IN ('pending','queued')), count(*) FILTER (WHERE status IN ('preparing','running')),
		       count(*) FILTER (WHERE status='awaiting_approval'), count(*) FILTER (WHERE status='failed')
		FROM boardroom_runs
	`).Scan(&snapshot.QueuedRuns, &snapshot.RunningRuns, &snapshot.AwaitingApproval, &snapshot.FailedRuns); err != nil {
		return snapshot, err
	}
	var averageMilliseconds float64
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status='running'), count(*) FILTER (WHERE status='succeeded'), count(*) FILTER (WHERE status='failed'),
		       COALESCE(sum(input_tokens+output_tokens) FILTER (WHERE completed_at>=date_trunc('month',now())),0),
		       COALESCE(sum(estimated_cost_micros) FILTER (WHERE completed_at>=date_trunc('month',now())),0),
		       COALESCE(avg(extract(epoch from (completed_at-started_at))*1000) FILTER (WHERE completed_at IS NOT NULL),0)
		FROM agent_invocations
	`).Scan(&snapshot.ActiveInvocations, &snapshot.CompletedInvocations, &snapshot.FailedInvocations, &snapshot.MonthlyTokens, &snapshot.MonthlyCostMicros, &averageMilliseconds); err != nil {
		return snapshot, err
	}
	snapshot.AverageLatency = time.Duration(averageMilliseconds) * time.Millisecond
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tenant_audit_events WHERE event_type='tool.denied' AND created_at>now()-interval '24 hours'`).Scan(&snapshot.ToolDenials); err != nil {
		return snapshot, err
	}
	rows, err := s.pool.Query(ctx, `SELECT provider,consecutive_failures,open_until,COALESCE(last_error_category,'') FROM provider_circuits ORDER BY provider`)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var circuit ProviderCircuit
		if err := rows.Scan(&circuit.Provider, &circuit.ConsecutiveFailures, &circuit.OpenUntil, &circuit.LastErrorCategory); err != nil {
			return snapshot, err
		}
		snapshot.ProviderCircuits = append(snapshot.ProviderCircuits, circuit)
	}
	return snapshot, rows.Err()
}

func CapacityInvocationError(err error) error {
	switch {
	case errors.Is(err, ErrInvocationCapacity), errors.Is(err, ErrProviderCircuitOpen):
		return &agent.InvocationError{Category: agent.FailureUnavailable, Retryable: true, Err: err}
	case errors.Is(err, ErrMonthlyTokenQuota), errors.Is(err, ErrMonthlyCostQuota):
		return &agent.InvocationError{Category: agent.FailureRateLimited, Retryable: false, Err: err}
	default:
		return fmt.Errorf("reserve invocation capacity: %w", err)
	}
}
