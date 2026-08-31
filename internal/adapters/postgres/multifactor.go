package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/multifactor"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type MultifactorRepository struct{ pool *pgxpool.Pool }

func NewMultifactorRepository(pool *pgxpool.Pool) *MultifactorRepository {
	return &MultifactorRepository{pool: pool}
}

func (r *MultifactorRepository) Recipient(ctx context.Context, userID ids.UserID) (multifactor.Recipient, error) {
	var value multifactor.Recipient
	err := r.pool.QueryRow(ctx, `SELECT primary_email,display_name FROM users WHERE id=$1 AND state='active' AND email_verified_at IS NOT NULL`, userID).Scan(&value.Email, &value.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return multifactor.Recipient{}, multifactor.ErrInvalidRequest
	}
	return value, err
}

func (r *MultifactorRepository) CreateChallenge(ctx context.Context, value multifactor.Challenge) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN sessions s ON s.user_id=u.id WHERE u.id=$1 AND u.state='active' AND s.id=$2 AND s.revoked_at IS NULL AND s.expires_at>$3)`, value.UserID, value.SessionID, value.CreatedAt.UTC()).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return multifactor.ErrInvalidRequest
	}
	var recent int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM user_mfa_challenges WHERE user_id=$1 AND created_at>$2::timestamptz-interval '1 hour'`, value.UserID, value.CreatedAt.UTC()).Scan(&recent); err != nil {
		return err
	}
	if recent >= 5 {
		return multifactor.ErrRateLimited
	}
	if value.Purpose == multifactor.PurposeReauthentication {
		var methodKind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM user_mfa_methods WHERE id=$1 AND user_id=$2`, value.MethodID, value.UserID).Scan(&methodKind); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return multifactor.ErrInvalidRequest
			}
			return err
		}
		if multifactor.Kind(methodKind) != value.Kind {
			return multifactor.ErrInvalidRequest
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE user_mfa_challenges SET consumed_at=$3 WHERE user_id=$1 AND session_id=$2 AND purpose=$4 AND kind=$5 AND consumed_at IS NULL`, value.UserID, value.SessionID, value.CreatedAt.UTC(), value.Purpose, value.Kind); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO user_mfa_challenges
		(id,result_method_id,method_id,user_id,session_id,purpose,kind,destination_ciphertext,destination_nonce,destination_key_version,destination_fingerprint,destination_hint,code_hash,expires_at,created_at)
		VALUES ($1,NULLIF($2,'')::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,NULLIF($10,0),$11,$12,$13,$14,$15)`,
		value.ID, value.ResultMethodID, value.MethodID, value.UserID, value.SessionID, value.Purpose, value.Kind,
		nullBytes(value.Destination.Ciphertext), nullBytes(value.Destination.Nonce), value.Destination.KeyVersion,
		value.DestinationHash[:], value.DestinationHint, value.CodeHash[:], value.ExpiresAt.UTC(), value.CreatedAt.UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *MultifactorRepository) CancelChallenge(ctx context.Context, challengeID string, userID ids.UserID, sessionID ids.SessionID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM user_mfa_challenges WHERE id=$1 AND user_id=$2 AND session_id=$3 AND consumed_at IS NULL`, challengeID, userID, sessionID)
	return err
}

func (r *MultifactorRepository) CompleteChallenge(ctx context.Context, challengeID string, userID ids.UserID, sessionID ids.SessionID, supplied [32]byte, now time.Time) (multifactor.Method, multifactor.Purpose, multifactor.Kind, bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return multifactor.Method{}, "", "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var purpose multifactor.Purpose
	var kind multifactor.Kind
	var resultMethodID, methodID *string
	var ciphertext, nonce []byte
	var keyVersion *int
	var fingerprint, expected []byte
	var hint string
	var failed int
	var expiresAt time.Time
	var consumedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT purpose,kind,result_method_id::text,method_id::text,destination_ciphertext,destination_nonce,destination_key_version,destination_fingerprint,destination_hint,code_hash,failed_attempts,expires_at,consumed_at
		FROM user_mfa_challenges WHERE id=$1 AND user_id=$2 AND session_id=$3 FOR UPDATE`, challengeID, userID, sessionID).
		Scan(&purpose, &kind, &resultMethodID, &methodID, &ciphertext, &nonce, &keyVersion, &fingerprint, &hint, &expected, &failed, &expiresAt, &consumedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return multifactor.Method{}, "", "", false, nil
	}
	if err != nil {
		return multifactor.Method{}, "", "", false, err
	}
	var expectedHash [32]byte
	copy(expectedHash[:], expected)
	if consumedAt != nil || !expiresAt.After(now) || failed >= 5 || !multifactor.EqualHash(expectedHash, supplied) {
		if consumedAt == nil {
			failed++
			_, err = tx.Exec(ctx, `UPDATE user_mfa_challenges SET failed_attempts=LEAST(5,$2),consumed_at=CASE WHEN $2>=5 OR expires_at<=$3 THEN $3 ELSE NULL END WHERE id=$1`, challengeID, failed, now.UTC())
			if err != nil {
				return multifactor.Method{}, "", "", false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return multifactor.Method{}, "", "", false, err
			}
		}
		return multifactor.Method{}, purpose, kind, false, nil
	}
	method := multifactor.Method{Kind: kind, DestinationHint: hint}
	if purpose == multifactor.PurposeEnrollment {
		if resultMethodID == nil {
			return multifactor.Method{}, "", "", false, multifactor.ErrInvalidChallenge
		}
		method.ID = *resultMethodID
		err = tx.QueryRow(ctx, `INSERT INTO user_mfa_methods(id,user_id,kind,destination_ciphertext,destination_nonce,destination_key_version,destination_fingerprint,destination_hint,created_at,last_used_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
			ON CONFLICT (user_id,kind,destination_fingerprint) DO UPDATE SET destination_ciphertext=EXCLUDED.destination_ciphertext,destination_nonce=EXCLUDED.destination_nonce,destination_key_version=EXCLUDED.destination_key_version,destination_hint=EXCLUDED.destination_hint,last_used_at=EXCLUDED.last_used_at
			RETURNING id::text,created_at,last_used_at`, method.ID, userID, kind, ciphertext, nonce, keyVersion, fingerprint, hint, now.UTC()).Scan(&method.ID, &method.CreatedAt, &method.LastUsedAt)
		if err != nil {
			return multifactor.Method{}, "", "", false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_security_events(user_id,session_id,event_type,occurred_at) VALUES ($1,$2,'mfa_method_added',$3)`, userID, sessionID, now.UTC()); err != nil {
			return multifactor.Method{}, "", "", false, err
		}
	} else {
		if methodID == nil {
			return multifactor.Method{}, "", "", false, multifactor.ErrInvalidChallenge
		}
		method.ID = *methodID
		err = tx.QueryRow(ctx, `UPDATE user_mfa_methods SET last_used_at=$3 WHERE id=$1 AND user_id=$2 RETURNING created_at,last_used_at,destination_hint`, method.ID, userID, now.UTC()).Scan(&method.CreatedAt, &method.LastUsedAt, &method.DestinationHint)
		if err != nil {
			return multifactor.Method{}, "", "", false, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_security_events(user_id,session_id,event_type,occurred_at) VALUES ($1,$2,'mfa_reauthenticated',$3)`, userID, sessionID, now.UTC()); err != nil {
			return multifactor.Method{}, "", "", false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE user_mfa_challenges SET consumed_at=$2 WHERE id=$1`, challengeID, now.UTC()); err != nil {
		return multifactor.Method{}, "", "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return multifactor.Method{}, "", "", false, err
	}
	return method, purpose, kind, true, nil
}

func (r *MultifactorRepository) Methods(ctx context.Context, userID ids.UserID) ([]multifactor.Method, error) {
	rows, err := r.pool.Query(ctx, `SELECT id::text,kind,destination_hint,created_at,last_used_at FROM user_mfa_methods WHERE user_id=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]multifactor.Method, 0)
	for rows.Next() {
		var value multifactor.Method
		if err := rows.Scan(&value.ID, &value.Kind, &value.DestinationHint, &value.CreatedAt, &value.LastUsedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *MultifactorRepository) MethodDestination(ctx context.Context, userID ids.UserID, methodID string) (multifactor.Method, multifactor.Envelope, error) {
	var value multifactor.Method
	var envelope multifactor.Envelope
	var ciphertext, nonce []byte
	var keyVersion *int
	err := r.pool.QueryRow(ctx, `SELECT id::text,kind,destination_hint,created_at,last_used_at,destination_ciphertext,destination_nonce,destination_key_version FROM user_mfa_methods WHERE id=$1 AND user_id=$2`, methodID, userID).
		Scan(&value.ID, &value.Kind, &value.DestinationHint, &value.CreatedAt, &value.LastUsedAt, &ciphertext, &nonce, &keyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return multifactor.Method{}, multifactor.Envelope{}, multifactor.ErrInvalidRequest
	}
	if err != nil {
		return multifactor.Method{}, multifactor.Envelope{}, err
	}
	if keyVersion != nil {
		envelope = multifactor.Envelope{Ciphertext: ciphertext, Nonce: nonce, KeyVersion: *keyVersion}
	}
	return value, envelope, nil
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

var _ multifactor.Store = (*MultifactorRepository)(nil)
