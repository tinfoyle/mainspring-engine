package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/recovery"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type RecoveryRepository struct{ pool *pgxpool.Pool }

func NewRecoveryRepository(pool *pgxpool.Pool) *RecoveryRepository {
	return &RecoveryRepository{pool: pool}
}

func (r *RecoveryRepository) Create(ctx context.Context, pending recovery.Pending) (recovery.Recipient, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return recovery.Recipient{}, false, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "credential-recovery-email:"+pending.Email); err != nil {
		return recovery.Recipient{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var recipient recovery.Recipient
	err = tx.QueryRow(ctx, `
		SELECT u.id,u.primary_email,u.display_name
		FROM users u JOIN authentication_identities i ON i.user_id=u.id AND i.provider='local'
		WHERE u.primary_email=$1 AND u.state='active'`, pending.Email).Scan(&recipient.UserID, &recipient.Email, &recipient.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return recovery.Recipient{}, false, nil
	}
	if err != nil {
		return recovery.Recipient{}, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_recovery_challenges SET consumed_at=$2 WHERE user_id=$1 AND consumed_at IS NULL`, recipient.UserID, pending.CreatedAt.UTC()); err != nil {
		return recovery.Recipient{}, false, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO credential_recovery_challenges (id,user_id,token_hash,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5)`, pending.ID, recipient.UserID, pending.TokenHash[:], pending.ExpiresAt.UTC(), pending.CreatedAt.UTC()); err != nil {
		return recovery.Recipient{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return recovery.Recipient{}, false, err
	}
	return recipient, true, nil
}

func (r *RecoveryRepository) Delete(ctx context.Context, id ids.RecoveryID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM credential_recovery_challenges WHERE id=$1 AND consumed_at IS NULL`, id)
	return err
}

func (r *RecoveryRepository) Complete(ctx context.Context, tokenHash [32]byte, passwordHash string, now time.Time) (ids.UserID, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID ids.UserID
	var email string
	var state string
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT c.user_id,u.primary_email,u.state,c.expires_at,c.consumed_at
		FROM credential_recovery_challenges c JOIN users u ON u.id=c.user_id
		WHERE c.token_hash=$1 FOR UPDATE OF c,u`, tokenHash[:]).Scan(&userID, &email, &state, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (state != "active" || consumedAt != nil || !expiresAt.After(now)) {
		return "", recovery.ErrInvalidChallenge
	}
	if err != nil {
		return "", err
	}
	command, err := tx.Exec(ctx, `UPDATE authentication_identities SET secret_hash=$2,failed_attempts=0,locked_until=NULL,updated_at=$3 WHERE user_id=$1 AND provider='local'`, userID, passwordHash, now.UTC())
	if err != nil {
		return "", err
	}
	if command.RowsAffected() != 1 {
		return "", recovery.ErrInvalidChallenge
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET security_version=security_version+1 WHERE id=$1`, userID); err != nil {
		return "", err
	}
	loginKey := sha256.Sum256([]byte(email))
	reauthenticationKey := sha256.Sum256([]byte("reauth:" + string(userID)))
	if _, err := tx.Exec(ctx, `DELETE FROM authentication_rate_limits WHERE identifier_hash=$1 OR identifier_hash=$2`, loginKey[:], reauthenticationKey[:]); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, userID, now.UTC()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE credential_recovery_challenges SET consumed_at=$2 WHERE user_id=$1 AND consumed_at IS NULL`, userID, now.UTC()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,'credential_recovered',$2)`, userID, now.UTC()); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

var _ recovery.Repository = (*RecoveryRepository)(nil)
