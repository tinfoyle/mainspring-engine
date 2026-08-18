package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/securityposture"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SecurityPostureRepository struct{ pool *pgxpool.Pool }

func NewSecurityPostureRepository(pool *pgxpool.Pool) *SecurityPostureRepository {
	return &SecurityPostureRepository{pool: pool}
}

func (r *SecurityPostureRepository) Status(ctx context.Context, userID ids.UserID) (securityposture.State, error) {
	var result securityposture.State
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM passkey_credentials WHERE user_id=$1),
			EXISTS (SELECT 1 FROM user_recovery_code_sets WHERE user_id=$1),
			(SELECT count(*) FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NULL)`, userID).
		Scan(&result.PasskeyCount, &result.RecoveryCodesConfigured, &result.RecoveryCodesRemaining)
	return result, err
}

var _ securityposture.Store = (*SecurityPostureRepository)(nil)
