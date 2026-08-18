package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/runnercapability"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RunnerCapabilityAuditor struct {
	pool *pgxpool.Pool
	ids  ids.Generator
}

func NewRunnerCapabilityAuditor(pool *pgxpool.Pool, generator ids.Generator) (*RunnerCapabilityAuditor, error) {
	if pool == nil || generator == nil {
		return nil, errors.New("runner capability auditor dependencies are required")
	}
	return &RunnerCapabilityAuditor{pool: pool, ids: generator}, nil
}

func (a *RunnerCapabilityAuditor) RecordCapability(ctx context.Context, record runnercapability.AuditRecord) error {
	var errorCode *string
	if record.ErrorCode != "" {
		errorCode = &record.ErrorCode
	}
	_, err := a.pool.Exec(ctx, `SELECT public.spyglass_record_runner_capability_event($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		record.AccountID, a.ids.New(), record.InvocationID, record.PodUID, record.OperationID,
		record.Capability, record.Effect, record.Decision, errorCode, record.OccurredAt.UTC())
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && (postgresError.Code == "22023" || postgresError.Code == "P0002") {
			return runnercapability.ErrAuditUnavailable
		}
		return fmt.Errorf("record runner capability audit: %w", err)
	}
	return nil
}

var _ runnercapability.Auditor = (*RunnerCapabilityAuditor)(nil)
