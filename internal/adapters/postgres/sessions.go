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
	_, err := r.pool.Exec(ctx, `
		WITH created AS (
			INSERT INTO sessions (id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,expires_at,client_label)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id,user_id,authenticated_at
		)
		INSERT INTO user_security_events (user_id,session_id,event_type,occurred_at)
		SELECT user_id,id,'session_created',authenticated_at FROM created`, value.ID, value.UserID, value.TokenHash[:], value.SecurityVersion, value.AuthenticatedAt, value.ReauthenticatedAt, value.LastSeenAt, value.RotatedAt, value.ExpiresAt, value.ClientLabel)
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
		RETURNING s.id,s.user_id,s.token_hash,s.security_version,s.authenticated_at,s.reauthenticated_at,
		          s.last_seen_at,s.rotated_at,s.expires_at,s.revoked_at,s.client_label`, hash[:], now.UTC(), int64(idleTTL/time.Second)).Scan(
		&value.ID, &value.UserID, &tokenHash, &value.SecurityVersion, &value.AuthenticatedAt,
		&value.ReauthenticatedAt, &value.LastSeenAt, &value.RotatedAt, &value.ExpiresAt, &value.RevokedAt, &value.ClientLabel)
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
	_, err := r.pool.Exec(ctx, `
		WITH revoked AS (
			UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL RETURNING user_id
		)
		INSERT INTO user_security_events (user_id,event_type,occurred_at)
		SELECT $1,'sessions_revoked',$2 WHERE EXISTS (SELECT 1 FROM revoked)`, userID, now.UTC())
	return err
}

func (r *SessionRepository) Revoke(ctx context.Context, sessionID ids.SessionID, now time.Time) error {
	_, err := r.pool.Exec(ctx, `
		WITH revoked AS (
			UPDATE sessions SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL RETURNING id,user_id
		)
		INSERT INTO user_security_events (user_id,session_id,event_type,occurred_at)
		SELECT user_id,id,'session_revoked',$2 FROM revoked`, sessionID, now.UTC())
	return err
}

func (r *SessionRepository) RevokeOwned(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	command, err := r.pool.Exec(ctx, `
		WITH revoked AS (
			UPDATE sessions SET revoked_at=$3 WHERE id=$2 AND user_id=$1 AND revoked_at IS NULL RETURNING id,user_id
		)
		INSERT INTO user_security_events (user_id,session_id,event_type,occurred_at)
		SELECT user_id,id,'session_revoked',$3 FROM revoked`, userID, sessionID, now.UTC())
	return command.RowsAffected() == 1, err
}

func (r *SessionRepository) Active(ctx context.Context, userID ids.UserID, now time.Time, idleTTL time.Duration) ([]sessions.Session, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.id,s.user_id,s.security_version,s.authenticated_at,s.reauthenticated_at,s.last_seen_at,s.rotated_at,s.expires_at,s.client_label
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.user_id=$1 AND u.state='active' AND u.security_version=s.security_version
		  AND s.revoked_at IS NULL AND s.expires_at>$2
		  AND s.last_seen_at>($2-($3*interval '1 second'))
		ORDER BY s.last_seen_at DESC,s.id`, userID, now.UTC(), int64(idleTTL/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sessions.Session, 0)
	for rows.Next() {
		var value sessions.Session
		if err := rows.Scan(&value.ID, &value.UserID, &value.SecurityVersion, &value.AuthenticatedAt, &value.ReauthenticatedAt, &value.LastSeenAt, &value.RotatedAt, &value.ExpiresAt, &value.ClientLabel); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *SessionRepository) MarkReauthenticated(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	command, err := r.pool.Exec(ctx, `
		WITH refreshed AS (
			UPDATE sessions SET reauthenticated_at=$3,last_seen_at=$3
			WHERE id=$2 AND user_id=$1 AND revoked_at IS NULL AND expires_at>$3 RETURNING id,user_id
		)
		INSERT INTO user_security_events (user_id,session_id,event_type,occurred_at)
		SELECT user_id,id,'session_reauthenticated',$3 FROM refreshed`, userID, sessionID, now.UTC())
	return command.RowsAffected() == 1, err
}

func (r *SessionRepository) SecurityEvents(ctx context.Context, userID ids.UserID, limit int) ([]sessions.SecurityEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT event_type,COALESCE(session_id::text,''),occurred_at
		FROM user_security_events WHERE user_id=$1
		ORDER BY occurred_at DESC,id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sessions.SecurityEvent, 0)
	for rows.Next() {
		var value sessions.SecurityEvent
		if err := rows.Scan(&value.Type, &value.SessionID, &value.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

var _ sessions.Repository = (*SessionRepository)(nil)
