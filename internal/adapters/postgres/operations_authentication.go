package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tinfoyle/spyglass-engine/internal/application/operationsauth"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type OperationsAuthenticationRepository struct{ pool *pgxpool.Pool }

func NewOperationsAuthenticationRepository(pool *pgxpool.Pool) *OperationsAuthenticationRepository {
	return &OperationsAuthenticationRepository{pool}
}
func (r *OperationsAuthenticationRepository) Begin(ctx context.Context, h [32]byte, now time.Time) error {
	// Short-lived login state contains no tokens in plaintext and is swept on use.
	_, err := r.pool.Exec(ctx, `DELETE FROM operations_login_challenges WHERE expires_at<$1`, now)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO operations_login_challenges(token_hash,created_at,expires_at) VALUES($1,$2,$2::timestamptz+interval '10 minutes')`, h[:], now)
	return err
}
func authEvent(ctx context.Context, tx pgx.Tx, user ids.UserID, action string, now time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO operations_authentication_events(id,user_id,action,occurred_at) VALUES($1,$2,$3,$4)`, ids.RandomGenerator{}.New(), user, action, now)
	return err
}
func (r *OperationsAuthenticationRepository) Bind(ctx context.Context, h [32]byte, identifier string, now time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var user ids.UserID
	err = tx.QueryRow(ctx, `UPDATE operations_login_challenges c SET user_id=u.id,security_version=u.security_version,staff_version=s.version,google_at=$3
 FROM users u JOIN authentication_identities i ON i.user_id=u.id JOIN operations_staff s ON s.user_id=u.id
 WHERE c.token_hash=$1 AND c.google_at IS NULL AND NOT c.consumed AND c.expires_at>$3
 AND i.provider='oidc' AND i.identifier=$2 AND u.state='active' AND s.state='active'
 AND EXISTS(SELECT 1 FROM operations_staff_role_assignments a WHERE a.staff_user_id=u.id AND a.revoked_at IS NULL)
 RETURNING u.id`, h[:], identifier, now).Scan(&user)
	if errors.Is(err, pgx.ErrNoRows) {
		return operationsauth.ErrDenied
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO operations_authenticators(user_id,window_start) VALUES($1,$2) ON CONFLICT DO NOTHING`, user, now)
	if err != nil {
		return err
	}
	if err = authEvent(ctx, tx, user, "google_verified", now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type authQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadChallenge(ctx context.Context, q authQuerier, h [32]byte, now time.Time, lock bool) (operationsauth.Challenge, error) {
	var c operationsauth.Challenge
	var pending []byte
	query := `SELECT c.user_id,c.security_version,c.staff_version,c.expires_at,c.google_at,c.pending,c.recovery_only,c.consumed,u.display_name
 FROM operations_login_challenges c JOIN users u ON u.id=c.user_id JOIN operations_staff s ON s.user_id=c.user_id
 WHERE c.token_hash=$1 AND c.google_at IS NOT NULL AND NOT c.consumed AND c.expires_at>$2
 AND u.state='active' AND u.security_version=c.security_version AND s.state='active' AND s.version=c.staff_version
 AND EXISTS(SELECT 1 FROM operations_staff_role_assignments a WHERE a.staff_user_id=u.id AND a.revoked_at IS NULL)`
	if lock {
		query += " FOR UPDATE OF c"
	}
	err := q.QueryRow(ctx, query, h[:], now).Scan(&c.UserID, &c.SecurityVersion, &c.StaffVersion, &c.ExpiresAt, &c.GoogleAt, &pending, &c.RecoveryOnly, &c.Consumed, &c.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, operationsauth.ErrDenied
	}
	if err != nil {
		return c, err
	}
	c.Hash = h
	err = json.Unmarshal(pending, &c.Pending)
	return c, err
}
func loadCredential(ctx context.Context, q authQuerier, user ids.UserID, lock bool) (operationsauth.Credential, error) {
	var k operationsauth.Credential
	var secret []byte
	query := `SELECT user_id,secret,last_step,recovery_hashes,failed,window_start,recovery_locked FROM operations_authenticators WHERE user_id=$1`
	if lock {
		query += " FOR UPDATE"
	}
	err := q.QueryRow(ctx, query, user).Scan(&k.UserID, &secret, &k.LastStep, &k.RecoveryHashes, &k.Failed, &k.WindowStart, &k.RecoveryLocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, operationsauth.ErrDenied
	}
	if err != nil {
		return k, err
	}
	err = json.Unmarshal(secret, &k.Secret)
	return k, err
}
func saveCredential(ctx context.Context, tx pgx.Tx, k operationsauth.Credential) error {
	secret, err := json.Marshal(k.Secret)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE operations_authenticators SET secret=$2,last_step=$3,recovery_hashes=$4,failed=$5,window_start=$6,recovery_locked=$7 WHERE user_id=$1`, k.UserID, secret, k.LastStep, k.RecoveryHashes, k.Failed, k.WindowStart, k.RecoveryLocked)
	return err
}
func (r *OperationsAuthenticationRepository) Load(ctx context.Context, h [32]byte, now time.Time) (operationsauth.Challenge, operationsauth.Credential, error) {
	c, err := loadChallenge(ctx, r.pool, h, now, false)
	if err != nil {
		return c, operationsauth.Credential{}, err
	}
	k, err := loadCredential(ctx, r.pool, c.UserID, false)
	return c, k, err
}
func (r *OperationsAuthenticationRepository) Update(ctx context.Context, h [32]byte, now time.Time, fn func(*operationsauth.Challenge, *operationsauth.Credential) (string, error)) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := loadChallenge(ctx, tx, h, now, false)
	if err != nil {
		return err
	}
	k, err := loadCredential(ctx, tx, c.UserID, true)
	if err != nil {
		return err
	}
	c, err = loadChallenge(ctx, tx, h, now, true)
	if err != nil {
		return err
	}
	action, denial := fn(&c, &k)
	if denial != nil && !errors.Is(denial, operationsauth.ErrDenied) && !errors.Is(denial, operationsauth.ErrLimited) {
		return denial
	}
	if err = saveCredential(ctx, tx, k); err != nil {
		return err
	}
	if denial != nil {
		action = "code_rejected"
	} else {
		pending, e := json.Marshal(c.Pending)
		if e != nil {
			return e
		}
		_, err = tx.Exec(ctx, `UPDATE operations_login_challenges SET pending=$2,recovery_only=$3,consumed=$4 WHERE token_hash=$1`, h[:], pending, c.RecoveryOnly, c.Consumed)
		if err != nil {
			return err
		}
		if action == "authenticator_enrolled" || action == "recovery_used" {
			_, err = tx.Exec(ctx, `UPDATE operations_sessions SET revoked_at=$2 WHERE user_id=$1 AND revoked_at IS NULL`, c.UserID, now)
			if err != nil {
				return err
			}
			// Other pending challenges cannot survive recovery or credential replacement.
			_, err = tx.Exec(ctx, `UPDATE operations_login_challenges SET consumed=true WHERE user_id=$1 AND token_hash<>$2`, c.UserID, h[:])
			if err != nil {
				return err
			}
		}
	}
	if denial == nil && c.NewSession != nil {
		if err = insertOperationsSession(ctx, tx, *c.NewSession); err != nil {
			return err
		}
	}
	if err = authEvent(ctx, tx, c.UserID, action, now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return denial
}
func (r *OperationsAuthenticationRepository) Reauthenticate(ctx context.Context, user ids.UserID, session ids.SessionID, now time.Time, fn func(*operationsauth.Credential) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	k, err := loadCredential(ctx, tx, user, true)
	if err != nil {
		return err
	}
	denial := fn(&k)
	if denial != nil && !errors.Is(denial, operationsauth.ErrDenied) && !errors.Is(denial, operationsauth.ErrLimited) {
		return denial
	}
	if err = saveCredential(ctx, tx, k); err != nil {
		return err
	}
	action := "code_rejected"
	if denial == nil {
		tag, e := tx.Exec(ctx, `UPDATE operations_sessions s SET reauthenticated_at=$3,reauthentication_method='google_totp'
 FROM users u,operations_staff staff WHERE s.id=$2 AND s.user_id=$1 AND u.id=s.user_id AND staff.user_id=s.user_id
 AND u.state='active' AND staff.state='active' AND u.security_version=s.security_version
 AND s.revoked_at IS NULL AND s.expires_at>$3 AND s.last_seen_at>$3::timestamptz-interval '30 minutes' AND s.authentication_method='google_totp'`, user, session, now)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return operationsauth.ErrDenied
		}
		action = "reauthenticated"
	}
	if err = authEvent(ctx, tx, user, action, now); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return denial
}

var _ operationsauth.Repository = (*OperationsAuthenticationRepository)(nil)
