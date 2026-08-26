package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/identitymaintenance"
)

type IdentityMaintenanceRepository struct{ pool *pgxpool.Pool }

func NewIdentityMaintenanceRepository(pool *pgxpool.Pool) *IdentityMaintenanceRepository {
	return &IdentityMaintenanceRepository{pool: pool}
}

func (r *IdentityMaintenanceRepository) Prune(ctx context.Context, now time.Time, retention time.Duration, batch int) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT spyglass_prune_passkey_ceremonies($1,$2,$3)`, now.UTC(), int64(retention/time.Second), batch).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("prune passkey ceremonies: %w", err)
	}
	return count, nil
}

func (r *IdentityMaintenanceRepository) Stats(ctx context.Context, now time.Time, retention time.Duration) (identitymaintenance.Stats, error) {
	var result identitymaintenance.Stats
	var ageSeconds int64
	err := r.pool.QueryRow(ctx, `SELECT total,eligible,oldest_eligible_age_seconds FROM spyglass_passkey_ceremony_retention_stats($1,$2)`, now.UTC(), int64(retention/time.Second)).Scan(&result.Total, &result.Eligible, &ageSeconds)
	if err != nil {
		return identitymaintenance.Stats{}, fmt.Errorf("read passkey ceremony retention stats: %w", err)
	}
	result.OldestEligibleAge = time.Duration(ageSeconds) * time.Second
	return result, nil
}

func (r *IdentityMaintenanceRepository) PruneNetworkLimits(ctx context.Context, now time.Time, retention time.Duration, batch int) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT spyglass_prune_network_actor_limits($1,$2,$3)`, now.UTC(), int64(retention/time.Second), batch).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("prune network actor limits: %w", err)
	}
	return count, nil
}

func (r *IdentityMaintenanceRepository) NetworkLimitStats(ctx context.Context, now time.Time, retention time.Duration) (identitymaintenance.Stats, error) {
	var result identitymaintenance.Stats
	var ageSeconds int64
	err := r.pool.QueryRow(ctx, `SELECT total,eligible,oldest_eligible_age_seconds FROM spyglass_network_actor_limit_retention_stats($1,$2)`, now.UTC(), int64(retention/time.Second)).Scan(&result.Total, &result.Eligible, &ageSeconds)
	if err != nil {
		return identitymaintenance.Stats{}, fmt.Errorf("read network actor limit retention stats: %w", err)
	}
	result.OldestEligibleAge = time.Duration(ageSeconds) * time.Second
	return result, nil
}

var _ identitymaintenance.Repository = (*IdentityMaintenanceRepository)(nil)
