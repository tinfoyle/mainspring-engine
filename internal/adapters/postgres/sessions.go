package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type SessionRepository struct{ pool *pgxpool.Pool }

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

func (r *SessionRepository) Create(ctx context.Context, value sessions.Session) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO sessions (id,user_id,token_hash,security_version,authenticated_at,last_seen_at,rotated_at,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, value.ID, value.UserID, value.TokenHash[:], value.SecurityVersion, value.AuthenticatedAt, value.LastSeenAt, value.RotatedAt, value.ExpiresAt)
	return err
}

func (r *SessionRepository) Use(ctx context.Context, hash [32]byte, now time.Time, idleTTL time.Duration) (sessions.Session, error) {
	var value sessions.Session
	var tokenHash []byte
	err := r.pool.QueryRow(ctx, `
		UPDATE sessions s SET last_seen_at=$2
		FROM users u
		WHERE s.token_hash=$1 AND u.id=s.user_id AND u.state='active'
		  AND u.security_version=s.security_version AND s.revoked_at IS NULL
		  AND s.expires_at>$2 AND s.last_seen_at>($2-($3 * interval '1 second'))
		RETURNING s.id,s.user_id,s.token_hash,s.security_version,s.authenticated_at,
		          s.last_seen_at,s.rotated_at,s.expires_at,s.revoked_at`, hash[:], now.UTC(), int64(idleTTL/time.Second)).Scan(
		&value.ID, &value.UserID, &tokenHash, &value.SecurityVersion, &value.AuthenticatedAt,
		&value.LastSeenAt, &value.RotatedAt, &value.ExpiresAt, &value.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrInvalidSession
	}
	if err != nil {
		return sessions.Session{}, err
	}
	if len(tokenHash) != 32 {
		return sessions.Session{}, errors.New("session token hash is corrupt")
	}
	copy(value.TokenHash[:], tokenHash)
	return value, nil
}

func (r *SessionRepository) Rotate(ctx context.Context, id ids.SessionID, oldHash, newHash [32]byte, now time.Time) (bool, error) {
	command, err := r.pool.Exec(ctx, `UPDATE sessions SET token_hash=$3,rotated_at=$4 WHERE id=$1 AND token_hash=$2 AND revoked_at IS NULL`, id, oldHash[:], newHash[:], now.UTC())
	return command.RowsAffected() == 1, err
}

func (r *SessionRepository) RevokeAll(ctx context.Context, userID ids.UserID, now time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, now.UTC())
	return err
}

var _ sessions.Repository = (*SessionRepository)(nil)
