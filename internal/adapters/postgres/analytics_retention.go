package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/analyticsretention"
)

type AnalyticsRetentionRepository struct{ pool *pgxpool.Pool }

func NewAnalyticsRetentionRepository(pool *pgxpool.Pool) *AnalyticsRetentionRepository {
	return &AnalyticsRetentionRepository{pool: pool}
}

func (r *AnalyticsRetentionRepository) Prune(ctx context.Context, now time.Time, retention time.Duration, batch int) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT spyglass_prune_analytics_events($1,$2,$3)`, now.UTC(), int64(retention/time.Second), batch).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("prune analytics events: %w", err)
	}
	return count, nil
}

func (r *AnalyticsRetentionRepository) Stats(ctx context.Context, now time.Time, retention time.Duration) (analyticsretention.Stats, error) {
	var result analyticsretention.Stats
	var ageSeconds int64
	err := r.pool.QueryRow(ctx, `SELECT total,eligible,oldest_eligible_age_seconds FROM spyglass_analytics_retention_stats($1,$2)`, now.UTC(), int64(retention/time.Second)).Scan(&result.Total, &result.Eligible, &ageSeconds)
	if err != nil {
		return analyticsretention.Stats{}, fmt.Errorf("read analytics event retention stats: %w", err)
	}
	result.OldestEligibleAge = time.Duration(ageSeconds) * time.Second
	return result, nil
}

var _ analyticsretention.Repository = (*AnalyticsRetentionRepository)(nil)
