package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/authentication"
)

type AuthenticationRepository struct{ pool *pgxpool.Pool }

func NewAuthenticationRepository(pool *pgxpool.Pool) *AuthenticationRepository {
	return &AuthenticationRepository{pool: pool}
}

func (r *AuthenticationRepository) LocalIdentity(ctx context.Context, email string) (authentication.LocalIdentity, error) {
	var result authentication.LocalIdentity
	err := r.pool.QueryRow(ctx, `
		SELECT u.id,u.primary_email,u.display_name,u.state,u.email_verified_at,
		       u.security_version,u.created_at,i.secret_hash
		FROM authentication_identities i
		JOIN users u ON u.id=i.user_id
		WHERE i.provider='local' AND i.identifier=$1`, email).Scan(
		&result.User.ID, &result.User.PrimaryEmail, &result.User.DisplayName,
		&result.User.State, &result.User.EmailVerifiedAt, &result.User.SecurityVersion,
		&result.User.CreatedAt, &result.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return authentication.LocalIdentity{}, authentication.ErrIdentityNotFound
	}
	return result, err
}

func (r *AuthenticationRepository) Blocked(ctx context.Context, key [32]byte, now time.Time) (bool, error) {
	var blocked bool
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(locked_until>$2,false) FROM authentication_rate_limits WHERE identifier_hash=$1`, key[:], now.UTC()).Scan(&blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return blocked, err
}

func (r *AuthenticationRepository) Failure(ctx context.Context, key [32]byte, now time.Time, threshold int, lock time.Duration) error {
	seconds := int64(lock / time.Second)
	_, err := r.pool.Exec(ctx, `
		INSERT INTO authentication_rate_limits (identifier_hash,window_started_at,attempt_count,locked_until,updated_at)
		VALUES ($1,$2,1,NULL,$2)
		ON CONFLICT (identifier_hash) DO UPDATE SET
		  attempt_count=CASE WHEN authentication_rate_limits.window_started_at <= $2-($4*interval '1 second') THEN 1 ELSE authentication_rate_limits.attempt_count+1 END,
		  window_started_at=CASE WHEN authentication_rate_limits.window_started_at <= $2-($4*interval '1 second') THEN $2 ELSE authentication_rate_limits.window_started_at END,
		  locked_until=CASE WHEN (CASE WHEN authentication_rate_limits.window_started_at <= $2-($4*interval '1 second') THEN 1 ELSE authentication_rate_limits.attempt_count+1 END) >= $3 THEN $2+($4*interval '1 second') ELSE authentication_rate_limits.locked_until END,
		  updated_at=$2`, key[:], now.UTC(), threshold, seconds)
	return err
}

func (r *AuthenticationRepository) Success(ctx context.Context, key [32]byte) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM authentication_rate_limits WHERE identifier_hash=$1`, key[:])
	return err
}

var _ authentication.IdentitySource = (*AuthenticationRepository)(nil)
var _ authentication.AttemptLimiter = (*AuthenticationRepository)(nil)
