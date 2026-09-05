package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/sessions"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// OperationsSessionRepository stores passkey-only staff sessions separately
// from customer sessions. Copying an operations cookie to the customer origin
// therefore cannot create a customer identity session.
type OperationsSessionRepository struct{ pool *pgxpool.Pool }

func NewOperationsSessionRepository(pool *pgxpool.Pool) *OperationsSessionRepository {
	return &OperationsSessionRepository{pool: pool}
}

func (repository *OperationsSessionRepository) Create(ctx context.Context, value sessions.Session) error {
	return insertOperationsSession(ctx, repository.pool, value)
}

type operationsSessionWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertOperationsSession(ctx context.Context, writer operationsSessionWriter, value sessions.Session) error {

	if value.AuthenticationMethod != value.ReauthenticationMethod || (value.AuthenticationMethod != sessions.AuthenticationMethodPasskey && value.AuthenticationMethod != sessions.AuthenticationMethodGoogleTOTP) {
		return sessions.ErrInvalidSession
	}
	command, err := writer.Exec(ctx, `
		INSERT INTO operations_sessions
			(id,user_id,token_hash,security_version,authenticated_at,reauthenticated_at,last_seen_at,rotated_at,
			 expires_at,client_label,authentication_method,reauthentication_method)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12
		FROM operations_staff s JOIN users u ON u.id=s.user_id
		WHERE s.user_id=$2 AND s.state='active' AND u.state='active' AND u.security_version=$4
		  AND EXISTS (SELECT 1 FROM operations_staff_role_assignments r WHERE r.staff_user_id=s.user_id AND r.revoked_at IS NULL)`,
		value.ID, value.UserID, value.TokenHash[:], value.SecurityVersion, value.AuthenticatedAt, value.ReauthenticatedAt,
		value.LastSeenAt, value.RotatedAt, value.ExpiresAt, value.ClientLabel, value.AuthenticationMethod, value.ReauthenticationMethod)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return sessions.ErrInvalidSession
	}
	return nil
}

func (repository *OperationsSessionRepository) Use(ctx context.Context, hash [32]byte, now time.Time, idleTTL time.Duration) (sessions.Session, error) {
	var value sessions.Session
	var tokenHash []byte
	err := repository.pool.QueryRow(ctx, `
		UPDATE operations_sessions s SET last_seen_at=$2
		FROM users u,operations_staff staff
		WHERE s.token_hash=$1 AND u.id=s.user_id AND staff.user_id=s.user_id
		  AND u.state='active' AND staff.state='active' AND u.security_version=s.security_version
		  AND s.revoked_at IS NULL AND s.expires_at>$2 AND s.last_seen_at>($2-($3*interval '1 second'))
		  AND EXISTS (SELECT 1 FROM operations_staff_role_assignments r WHERE r.staff_user_id=s.user_id AND r.revoked_at IS NULL)
		RETURNING s.id,s.user_id,s.token_hash,s.security_version,s.authenticated_at,s.reauthenticated_at,s.last_seen_at,
		          s.rotated_at,s.expires_at,s.revoked_at,s.client_label,s.authentication_method,s.reauthentication_method`,
		hash[:], now.UTC(), int64(idleTTL/time.Second)).Scan(
		&value.ID, &value.UserID, &tokenHash, &value.SecurityVersion, &value.AuthenticatedAt, &value.ReauthenticatedAt,
		&value.LastSeenAt, &value.RotatedAt, &value.ExpiresAt, &value.RevokedAt, &value.ClientLabel,
		&value.AuthenticationMethod, &value.ReauthenticationMethod)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessions.Session{}, sessions.ErrInvalidSession
	}
	if err != nil || len(tokenHash) != 32 {
		if err == nil {
			err = sessions.ErrInvalidSession
		}
		return sessions.Session{}, err
	}
	copy(value.TokenHash[:], tokenHash)
	return value, nil
}

func (repository *OperationsSessionRepository) Rotate(ctx context.Context, id ids.SessionID, oldHash, newHash [32]byte, now time.Time) (bool, error) {
	command, err := repository.pool.Exec(ctx, `UPDATE operations_sessions SET token_hash=$3,rotated_at=$4 WHERE id=$1 AND token_hash=$2 AND revoked_at IS NULL`, id, oldHash[:], newHash[:], now.UTC())
	return command.RowsAffected() == 1, err
}

func (repository *OperationsSessionRepository) Revoke(ctx context.Context, sessionID ids.SessionID, now time.Time) error {
	_, err := repository.pool.Exec(ctx, `UPDATE operations_sessions SET revoked_at=$2 WHERE id=$1 AND revoked_at IS NULL`, sessionID, now.UTC())
	return err
}

func (repository *OperationsSessionRepository) RevokeAll(ctx context.Context, userID ids.UserID, now time.Time) error {
	_, err := repository.pool.Exec(ctx, `UPDATE operations_sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, now.UTC())
	return err
}

func (repository *OperationsSessionRepository) RevokeOwned(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	command, err := repository.pool.Exec(ctx, `UPDATE operations_sessions SET revoked_at=$3 WHERE user_id=$1 AND id=$2 AND revoked_at IS NULL`, userID, sessionID, now.UTC())
	return command.RowsAffected() == 1, err
}

func (repository *OperationsSessionRepository) Active(ctx context.Context, userID ids.UserID, now time.Time, idleTTL time.Duration) ([]sessions.Session, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT s.id,s.user_id,s.security_version,s.authenticated_at,s.reauthenticated_at,s.last_seen_at,s.rotated_at,s.expires_at,
		       s.client_label,s.authentication_method,s.reauthentication_method
		FROM operations_sessions s JOIN users u ON u.id=s.user_id JOIN operations_staff staff ON staff.user_id=s.user_id
		WHERE s.user_id=$1 AND u.state='active' AND staff.state='active' AND u.security_version=s.security_version
		  AND s.revoked_at IS NULL AND s.expires_at>$2 AND s.last_seen_at>($2-($3*interval '1 second'))
		ORDER BY s.last_seen_at DESC,s.id`, userID, now.UTC(), int64(idleTTL/time.Second))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sessions.Session, 0)
	for rows.Next() {
		var value sessions.Session
		if err := rows.Scan(&value.ID, &value.UserID, &value.SecurityVersion, &value.AuthenticatedAt, &value.ReauthenticatedAt,
			&value.LastSeenAt, &value.RotatedAt, &value.ExpiresAt, &value.ClientLabel, &value.AuthenticationMethod, &value.ReauthenticationMethod); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (repository *OperationsSessionRepository) MarkReauthenticated(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, method sessions.AuthenticationMethod, now time.Time) (bool, error) {
	if method != sessions.AuthenticationMethodPasskey && method != sessions.AuthenticationMethodGoogleTOTP {
		return false, sessions.ErrInvalidSession
	}
	command, err := repository.pool.Exec(ctx, `
		UPDATE operations_sessions SET reauthenticated_at=$3,last_seen_at=$3,reauthentication_method=$4
		WHERE id=$2 AND user_id=$1 AND revoked_at IS NULL AND expires_at>$3`, userID, sessionID, now.UTC(), method)
	return command.RowsAffected() == 1, err
}

func (repository *OperationsSessionRepository) SecurityEvents(ctx context.Context, userID ids.UserID, limit int) ([]sessions.SecurityEvent, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT CASE action WHEN 'staff_authenticated' THEN 'session_created' ELSE 'session_revoked' END,
		       COALESCE(session_id::text,''),occurred_at
		FROM operations_access_events
		WHERE staff_user_id=$1 AND action IN ('staff_authenticated','staff_logged_out')
		ORDER BY occurred_at DESC,id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sessions.SecurityEvent, 0)
	for rows.Next() {
		var event sessions.SecurityEvent
		if err := rows.Scan(&event.Type, &event.SessionID, &event.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

var _ sessions.Repository = (*OperationsSessionRepository)(nil)
