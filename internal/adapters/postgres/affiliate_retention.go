package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/affiliateretention"
)

// AffiliateRetentionRepository has execute-only access to the retention
// functions. Its database role needs no direct access to Affiliate records,
// minimization tombstones, or retired-code fingerprints.
type AffiliateRetentionRepository struct{ pool *pgxpool.Pool }

func NewAffiliateRetentionRepository(pool *pgxpool.Pool) *AffiliateRetentionRepository {
	return &AffiliateRetentionRepository{pool: pool}
}

func (r *AffiliateRetentionRepository) Minimize(ctx context.Context, now time.Time, batch int) (int64, error) {
	var count int64
	if err := r.pool.QueryRow(ctx, `SELECT public.spyglass_minimize_due_affiliates($1,$2)`, now, batch).Scan(&count); err != nil {
		return 0, fmt.Errorf("minimize due Affiliates: %w", err)
	}
	return count, nil
}

func (r *AffiliateRetentionRepository) Stats(ctx context.Context, now time.Time) (affiliateretention.Stats, error) {
	var value affiliateretention.Stats
	var oldestSeconds int64
	if err := r.pool.QueryRow(ctx, `SELECT total,eligible,oldest_eligible_age_seconds
		FROM public.spyglass_affiliate_minimization_stats($1)`, now).Scan(
		&value.Total, &value.Eligible, &oldestSeconds); err != nil {
		return affiliateretention.Stats{}, fmt.Errorf("read Affiliate minimization stats: %w", err)
	}
	value.OldestEligibleAge = time.Duration(oldestSeconds) * time.Second
	if err := value.Validate(); err != nil {
		return affiliateretention.Stats{}, fmt.Errorf("Affiliate minimization stats are invalid: %w", err)
	}
	return value, nil
}

var _ affiliateretention.Store = (*AffiliateRetentionRepository)(nil)
