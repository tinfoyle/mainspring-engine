package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/contactchange"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type ContactChangeRepository struct{ pool *pgxpool.Pool }

func NewContactChangeRepository(pool *pgxpool.Pool) *ContactChangeRepository {
	return &ContactChangeRepository{pool: pool}
}

func (r *ContactChangeRepository) User(ctx context.Context, userID ids.UserID) (identity.User, error) {
	var user identity.User
	err := r.pool.QueryRow(ctx, `
		SELECT id,primary_email,display_name,state,email_verified_at,security_version,created_at
		FROM users WHERE id=$1`, userID).Scan(&user.ID, &user.PrimaryEmail, &user.DisplayName, &user.State, &user.EmailVerifiedAt, &user.SecurityVersion, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.User{}, contactchange.ErrInvalidUser
	}
	return user, err
}

func (r *ContactChangeRepository) CreatePending(ctx context.Context, pending contactchange.Pending, notifications []contactchange.PreparedNotification) error {
	if len(notifications) != 2 {
		return errors.New("contact change requires verification and request notifications")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		UPDATE primary_email_change_challenges SET consumed_at=$2
		WHERE consumed_at IS NULL AND (expires_at<=$2 OR user_id=$1)`, pending.UserID, pending.CreatedAt.UTC()); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO primary_email_change_challenges
		(id,user_id,old_email,new_email,display_name,security_version,token_hash,expires_at,created_at)
		SELECT $1,u.id,u.primary_email,$3,u.display_name,u.security_version,$4,$5,$6
		FROM users u
		WHERE u.id=$2 AND u.state='active' AND u.email_verified_at IS NOT NULL
		  AND u.primary_email=$7 AND u.security_version=$8
		  AND NOT EXISTS (SELECT 1 FROM users other WHERE other.primary_email=$3)`,
		pending.ID, pending.UserID, pending.NewEmail, pending.TokenHash[:], pending.ExpiresAt.UTC(), pending.CreatedAt.UTC(), pending.OldEmail, pending.SecurityVersion)
	if isUniqueConstraint(err, "primary_email_change_one_pending_email", "users_primary_email_unique") {
		return contactchange.ErrEmailExists
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		var exists bool
		if scanErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE primary_email=$1)`, pending.NewEmail).Scan(&exists); scanErr != nil {
			return scanErr
		}
		if exists {
			return contactchange.ErrEmailExists
		}
		return contactchange.ErrStaleIdentity
	}
	for _, notification := range notifications {
		if err := insertContactChangeNotification(ctx, tx, notification); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,'primary_email_change_requested',$2)`, pending.UserID, pending.CreatedAt.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ContactChangeRepository) Complete(ctx context.Context, tokenHash [32]byte, now time.Time, prepare func(contactchange.Pending) ([]contactchange.PreparedNotification, error)) (contactchange.Completed, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return contactchange.Completed{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pending, err := loadContactChangeForUpdate(ctx, tx, tokenHash)
	if err != nil {
		return contactchange.Completed{}, err
	}
	if pending.ConsumedAt != nil {
		return contactchange.Completed{}, contactchange.ErrConsumed
	}
	if !pending.ExpiresAt.After(now) {
		return contactchange.Completed{}, contactchange.ErrExpired
	}
	var currentEmail string
	var currentVersion uint64
	var state identity.UserState
	var verifiedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT primary_email,security_version,state,email_verified_at FROM users WHERE id=$1 FOR UPDATE`, pending.UserID).Scan(&currentEmail, &currentVersion, &state, &verifiedAt); errors.Is(err, pgx.ErrNoRows) {
		return contactchange.Completed{}, contactchange.ErrStaleIdentity
	} else if err != nil {
		return contactchange.Completed{}, err
	}
	if state != identity.UserActive || verifiedAt == nil || currentEmail != pending.OldEmail || currentVersion != pending.SecurityVersion {
		return contactchange.Completed{}, contactchange.ErrStaleIdentity
	}
	var emailExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE primary_email=$1 AND id<>$2)`, pending.NewEmail, pending.UserID).Scan(&emailExists); err != nil {
		return contactchange.Completed{}, err
	}
	if emailExists {
		return contactchange.Completed{}, contactchange.ErrEmailExists
	}
	notifications, err := prepare(pending)
	if err != nil {
		return contactchange.Completed{}, err
	}
	if len(notifications) != 2 {
		return contactchange.Completed{}, errors.New("contact change completion requires two notifications")
	}
	command, err := tx.Exec(ctx, `
		UPDATE users SET primary_email=$2,email_verified_at=$3,security_version=security_version+1
		WHERE id=$1 AND primary_email=$4 AND security_version=$5`, pending.UserID, pending.NewEmail, now.UTC(), pending.OldEmail, pending.SecurityVersion)
	if isUniqueConstraint(err, "users_primary_email_unique") {
		return contactchange.Completed{}, contactchange.ErrEmailExists
	}
	if err != nil {
		return contactchange.Completed{}, err
	}
	if command.RowsAffected() != 1 {
		return contactchange.Completed{}, contactchange.ErrStaleIdentity
	}
	identityUpdate, err := tx.Exec(ctx, `UPDATE authentication_identities SET identifier=$2,updated_at=$3 WHERE user_id=$1 AND provider='local' AND identifier=$4`, pending.UserID, pending.NewEmail, now.UTC(), pending.OldEmail)
	if err != nil {
		if isUniqueConstraint(err, "authentication_identities_pkey") {
			return contactchange.Completed{}, contactchange.ErrEmailExists
		}
		return contactchange.Completed{}, err
	}
	if identityUpdate.RowsAffected() != 1 {
		return contactchange.Completed{}, contactchange.ErrStaleIdentity
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, pending.UserID, now.UTC()); err != nil {
		return contactchange.Completed{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE primary_email_change_challenges SET consumed_at=$2 WHERE id=$1`, pending.ID, now.UTC()); err != nil {
		return contactchange.Completed{}, err
	}
	for _, notification := range notifications {
		if err := insertContactChangeNotification(ctx, tx, notification); err != nil {
			return contactchange.Completed{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,'primary_email_changed',$2)`, pending.UserID, now.UTC()); err != nil {
		return contactchange.Completed{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contactchange.Completed{}, err
	}
	return contactchange.Completed{UserID: pending.UserID, OldEmail: pending.OldEmail, NewEmail: pending.NewEmail, SecurityVersion: pending.SecurityVersion + 1, ChangedAt: now.UTC()}, nil
}

func loadContactChangeForUpdate(ctx context.Context, tx pgx.Tx, tokenHash [32]byte) (contactchange.Pending, error) {
	var pending contactchange.Pending
	var storedHash []byte
	err := tx.QueryRow(ctx, `
		SELECT id,user_id,old_email,new_email,display_name,security_version,token_hash,expires_at,created_at,consumed_at
		FROM primary_email_change_challenges WHERE token_hash=$1 FOR UPDATE`, tokenHash[:]).Scan(
		&pending.ID, &pending.UserID, &pending.OldEmail, &pending.NewEmail, &pending.DisplayName,
		&pending.SecurityVersion, &storedHash, &pending.ExpiresAt, &pending.CreatedAt, &pending.ConsumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return contactchange.Pending{}, contactchange.ErrNotFound
	}
	if err != nil {
		return contactchange.Pending{}, err
	}
	if len(storedHash) != 32 {
		return contactchange.Pending{}, errors.New("contact change token hash is corrupt")
	}
	copy(pending.TokenHash[:], storedHash)
	return pending, nil
}

func insertContactChangeNotification(ctx context.Context, tx pgx.Tx, notification contactchange.PreparedNotification) error {
	if notification.ID == "" || len(notification.Ciphertext) == 0 || len(notification.Nonce) == 0 || notification.KeyVersion <= 0 || notification.CreatedAt.IsZero() {
		return errors.New("prepared contact change notification is invalid")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO identity_notification_outbox
		(id,account_id,kind,ciphertext,nonce,key_version,processing_state,attempt_count,created_at)
		VALUES ($1,NULL,'contact_change',$2,$3,$4,'queued',0,$5)`, notification.ID, notification.Ciphertext, notification.Nonce, notification.KeyVersion, notification.CreatedAt.UTC())
	return err
}

var _ contactchange.Repository = (*ContactChangeRepository)(nil)
