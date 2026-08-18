package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/recoverycodes"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RecoveryCodeRepository struct{ pool *pgxpool.Pool }

func NewRecoveryCodeRepository(pool *pgxpool.Pool) *RecoveryCodeRepository {
	return &RecoveryCodeRepository{pool: pool}
}

func (r *RecoveryCodeRepository) Rotate(ctx context.Context, set recoverycodes.Set) (recoverycodes.Status, error) {
	if len(set.Hashes) != recoverycodes.CodeCount {
		return recoverycodes.Status{}, recoverycodes.ErrInvalidOperation
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return recoverycodes.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID ids.UserID
	if err := tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND state='active' FOR UPDATE`, set.UserID).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return recoverycodes.Status{}, recoverycodes.ErrInvalidOperation
		}
		return recoverycodes.Status{}, err
	}
	var previousVersion uint64
	err = tx.QueryRow(ctx, `SELECT version FROM user_recovery_code_sets WHERE user_id=$1 FOR UPDATE`, set.UserID).Scan(&previousVersion)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return recoverycodes.Status{}, err
	}
	set.Version = previousVersion + 1
	if _, err := tx.Exec(ctx, `DELETE FROM passkey_recovery_grants WHERE user_id=$1`, set.UserID); err != nil {
		return recoverycodes.Status{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_recovery_code_sets WHERE user_id=$1`, set.UserID); err != nil {
		return recoverycodes.Status{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_recovery_code_sets(id,user_id,version,created_at) VALUES ($1,$2,$3,$4)`, set.ID, set.UserID, set.Version, set.CreatedAt.UTC()); err != nil {
		return recoverycodes.Status{}, err
	}
	for index, hash := range set.Hashes {
		if _, err := tx.Exec(ctx, `INSERT INTO user_recovery_codes(set_id,user_id,position,code_hash,created_at) VALUES ($1,$2,$3,$4,$5)`, set.ID, set.UserID, index+1, hash[:], set.CreatedAt.UTC()); err != nil {
			return recoverycodes.Status{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events(user_id,event_type,occurred_at) VALUES ($1,'recovery_codes_rotated',$2)`, set.UserID, set.CreatedAt.UTC()); err != nil {
		return recoverycodes.Status{}, err
	}
	status := recoverycodes.Status{Configured: true, Version: set.Version, Remaining: len(set.Hashes), CreatedAt: set.CreatedAt.UTC()}
	return status, tx.Commit(ctx)
}

func (r *RecoveryCodeRepository) Status(ctx context.Context, userID ids.UserID) (recoverycodes.Status, error) {
	var result recoverycodes.Status
	err := r.pool.QueryRow(ctx, `
		SELECT s.version,s.created_at,count(c.position) FILTER (WHERE c.used_at IS NULL)
		FROM user_recovery_code_sets s LEFT JOIN user_recovery_codes c ON c.set_id=s.id
		WHERE s.user_id=$1 GROUP BY s.id,s.version,s.created_at`, userID).Scan(&result.Version, &result.CreatedAt, &result.Remaining)
	if errors.Is(err, pgx.ErrNoRows) {
		return recoverycodes.Status{}, nil
	}
	if err != nil {
		return recoverycodes.Status{}, err
	}
	result.Configured = true
	return result, nil
}

func (r *RecoveryCodeRepository) Consume(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, hash recoverycodes.CodeHash, now, expiresAt time.Time) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var setID string
	err = tx.QueryRow(ctx, `
		UPDATE user_recovery_codes c SET used_at=$3
		FROM user_recovery_code_sets s
		WHERE c.set_id=s.id AND s.user_id=$1 AND c.user_id=$1 AND c.code_hash=$2 AND c.used_at IS NULL
		RETURNING c.set_id::text`, userID, hash[:], now.UTC()).Scan(&setID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO passkey_recovery_grants(session_id,user_id,created_at,expires_at)
		SELECT id,user_id,$3,$4 FROM sessions
		WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>$3
		ON CONFLICT (session_id) DO UPDATE SET created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at
		WHERE passkey_recovery_grants.user_id=EXCLUDED.user_id`, sessionID, userID, now.UTC(), expiresAt.UTC())
	if err != nil || command.RowsAffected() != 1 {
		if err == nil {
			err = recoverycodes.ErrInvalidOperation
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events(user_id,session_id,event_type,occurred_at) VALUES ($1,$2,'recovery_code_consumed',$3)`, userID, sessionID, now.UTC()); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *RecoveryCodeRepository) Granted(ctx context.Context, userID ids.UserID, sessionID ids.SessionID, now time.Time) (bool, error) {
	var result bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM passkey_recovery_grants g JOIN sessions s ON s.id=g.session_id AND s.user_id=g.user_id
			WHERE g.user_id=$1 AND g.session_id=$2 AND g.expires_at>$3 AND s.revoked_at IS NULL AND s.expires_at>$3
		)`, userID, sessionID, now.UTC()).Scan(&result)
	return result, err
}

var _ recoverycodes.Store = (*RecoveryCodeRepository)(nil)
