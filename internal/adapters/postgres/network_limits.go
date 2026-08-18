package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/abuse"
)

type NetworkRateLimiter struct{ pool *pgxpool.Pool }

func NewNetworkRateLimiter(pool *pgxpool.Pool) *NetworkRateLimiter {
	return &NetworkRateLimiter{pool: pool}
}

func (l *NetworkRateLimiter) Consume(ctx context.Context, scope abuse.Scope, actor [32]byte, now time.Time, policy abuse.Policy) (bool, error) {
	var allowed bool
	seconds := int64(policy.Window / time.Second)
	err := l.pool.QueryRow(ctx, `
		INSERT INTO network_actor_rate_limits
		(scope,actor_hash,window_started_at,attempt_count,blocked_until,updated_at)
		VALUES ($1,$2,$3,1,NULL,$3)
		ON CONFLICT (scope,actor_hash) DO UPDATE SET
		  attempt_count=CASE
		    WHEN network_actor_rate_limits.blocked_until>$3 THEN network_actor_rate_limits.attempt_count
		    WHEN network_actor_rate_limits.window_started_at<=$3-($5*interval '1 second') THEN 1
		    ELSE network_actor_rate_limits.attempt_count+1 END,
		  window_started_at=CASE
		    WHEN network_actor_rate_limits.blocked_until>$3 THEN network_actor_rate_limits.window_started_at
		    WHEN network_actor_rate_limits.window_started_at<=$3-($5*interval '1 second') THEN $3
		    ELSE network_actor_rate_limits.window_started_at END,
		  blocked_until=CASE
		    WHEN network_actor_rate_limits.blocked_until>$3 THEN network_actor_rate_limits.blocked_until
		    WHEN network_actor_rate_limits.window_started_at<=$3-($5*interval '1 second') THEN NULL
		    WHEN network_actor_rate_limits.attempt_count+1>$4 THEN $3+($5*interval '1 second')
		    ELSE NULL END,
		  updated_at=$3
		RETURNING (blocked_until IS NULL OR blocked_until<=$3) AND attempt_count<=$4`, scope, actor[:], now.UTC(), policy.Limit, seconds).Scan(&allowed)
	return allowed, err
}

var _ abuse.Limiter = (*NetworkRateLimiter)(nil)
