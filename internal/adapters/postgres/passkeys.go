package postgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/passkeys"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type PasskeyRepository struct {
	pool   *pgxpool.Pool
	cipher *passkeys.Cipher
}

func NewPasskeyRepository(pool *pgxpool.Pool, cipher *passkeys.Cipher) (*PasskeyRepository, error) {
	if pool == nil || cipher == nil {
		return nil, errors.New("passkey repository pool and cipher are required")
	}
	return &PasskeyRepository{pool: pool, cipher: cipher}, nil
}

func (r *PasskeyRepository) EnsureUser(ctx context.Context, userID ids.UserID, handle []byte, now time.Time) (passkeys.User, error) {
	command, err := r.pool.Exec(ctx, `
		INSERT INTO passkey_users (user_id,user_handle,created_at)
		SELECT id,$2,$3 FROM users WHERE id=$1 AND state='active'
		ON CONFLICT (user_id) DO NOTHING`, userID, handle, now.UTC())
	if err != nil {
		return passkeys.User{}, err
	}
	if command.RowsAffected() == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM passkey_users WHERE user_id=$1)`, userID).Scan(&exists); err != nil {
			return passkeys.User{}, err
		}
		if !exists {
			return passkeys.User{}, passkeys.ErrCredentialNotFound
		}
	}
	return r.User(ctx, userID)
}

func (r *PasskeyRepository) User(ctx context.Context, userID ids.UserID) (passkeys.User, error) {
	var user passkeys.User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id,u.primary_email,u.display_name,u.state,u.email_verified_at,u.security_version,u.created_at,p.user_handle
		FROM passkey_users p JOIN users u ON u.id=p.user_id
		WHERE p.user_id=$1 AND u.state='active'`, userID).Scan(
		&user.Identity.ID, &user.Identity.PrimaryEmail, &user.Identity.DisplayName, &user.Identity.State,
		&user.Identity.EmailVerifiedAt, &user.Identity.SecurityVersion, &user.Identity.CreatedAt, &user.Handle)
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	if err != nil {
		return passkeys.User{}, err
	}
	user.Credentials, err = r.loadCredentials(ctx, user.Identity.ID)
	return user, err
}

func (r *PasskeyRepository) UserByHandle(ctx context.Context, handle, credentialID []byte) (passkeys.User, error) {
	var userID ids.UserID
	err := r.pool.QueryRow(ctx, `
		SELECT p.user_id FROM passkey_users p
		JOIN passkey_credentials c ON c.user_id=p.user_id
		JOIN users u ON u.id=p.user_id
		WHERE p.user_handle=$1 AND c.credential_id=$2 AND u.state='active'`, handle, credentialID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeys.User{}, passkeys.ErrCredentialNotFound
	}
	if err != nil {
		return passkeys.User{}, err
	}
	return r.User(ctx, userID)
}

func (r *PasskeyRepository) CreateCeremony(ctx context.Context, ceremony passkeys.Ceremony) error {
	if _, err := r.pool.Exec(ctx, `
		WITH stale AS (
			SELECT id FROM passkey_ceremonies WHERE expires_at<=$1 ORDER BY expires_at LIMIT 100
		) DELETE FROM passkey_ceremonies WHERE id IN (SELECT id FROM stale)`, ceremony.CreatedAt.UTC()); err != nil {
		return err
	}
	raw, err := json.Marshal(ceremony.Data)
	if err != nil {
		return err
	}
	envelope, err := r.cipher.Seal(ceremonyLabel(ceremony.ID, ceremony.Kind, ceremony.UserID, ceremony.SessionID), raw)
	if err != nil {
		return err
	}
	var userID any
	if ceremony.UserID != "" {
		userID = ceremony.UserID
	}
	var sessionID any
	if ceremony.SessionID != "" {
		sessionID = ceremony.SessionID
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO passkey_ceremonies
		(id,kind,user_id,session_id,encrypted_session_data,encryption_nonce,encryption_key_version,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, ceremony.ID, ceremony.Kind, userID, sessionID,
		envelope.Ciphertext, envelope.Nonce, envelope.KeyVersion, ceremony.ExpiresAt.UTC(), ceremony.CreatedAt.UTC())
	return err
}

func (r *PasskeyRepository) ConsumeCeremony(ctx context.Context, id string, kind passkeys.CeremonyKind, userID ids.UserID, sessionID ids.SessionID, now time.Time) (passkeys.Ceremony, error) {
	var value passkeys.Ceremony
	var storedUserID, storedSessionID *string
	var envelope passkeys.Envelope
	err := r.pool.QueryRow(ctx, `
		UPDATE passkey_ceremonies SET consumed_at=$5
		WHERE id=$1 AND kind=$2 AND user_id IS NOT DISTINCT FROM $3::uuid
		  AND session_id IS NOT DISTINCT FROM $4::uuid AND consumed_at IS NULL AND expires_at>$5
		RETURNING id,kind,user_id::text,session_id::text,encrypted_session_data,encryption_nonce,
		          encryption_key_version,expires_at,created_at`, id, kind, nullableUUID(userID), nullableUUID(sessionID), now.UTC()).Scan(
		&value.ID, &value.Kind, &storedUserID, &storedSessionID, &envelope.Ciphertext, &envelope.Nonce,
		&envelope.KeyVersion, &value.ExpiresAt, &value.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return passkeys.Ceremony{}, passkeys.ErrInvalidCeremony
	}
	if err != nil {
		return passkeys.Ceremony{}, err
	}
	if storedUserID != nil {
		value.UserID = ids.UserID(*storedUserID)
	}
	if storedSessionID != nil {
		value.SessionID = ids.SessionID(*storedSessionID)
	}
	raw, err := r.cipher.Open(ceremonyLabel(value.ID, value.Kind, value.UserID, value.SessionID), envelope)
	if err != nil || json.Unmarshal(raw, &value.Data) != nil {
		return passkeys.Ceremony{}, errors.New("passkey ceremony record is corrupt")
	}
	return value, nil
}

func (r *PasskeyRepository) CreateCredential(ctx context.Context, userID ids.UserID, name string, credential webauthn.Credential, now time.Time, maximum int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked ids.UserID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM passkey_users WHERE user_id=$1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM passkey_credentials WHERE user_id=$1`, userID).Scan(&count); err != nil {
		return err
	}
	if count >= maximum {
		return passkeys.ErrCredentialLimit
	}
	envelope, err := r.sealCredential(userID, credential)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO passkey_credentials
		(credential_id,user_id,name,encrypted_credential,encryption_nonce,encryption_key_version,sign_count,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, credential.ID, userID, name, envelope.Ciphertext,
		envelope.Nonce, envelope.KeyVersion, credential.Authenticator.SignCount, now.UTC()); err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			return passkeys.ErrInvalidCredential
		}
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,'passkey_added',$2)`, userID, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PasskeyRepository) UpdateCredential(ctx context.Context, userID ids.UserID, credentialID []byte, expected uint32, credential webauthn.Credential, event passkeys.CredentialEvent, now time.Time) (bool, error) {
	envelope, err := r.sealCredential(userID, credential)
	if err != nil {
		return false, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `
		UPDATE passkey_credentials SET encrypted_credential=$4,encryption_nonce=$5,encryption_key_version=$6,
		       sign_count=$7,last_used_at=$8
		WHERE credential_id=$2 AND user_id=$1 AND sign_count=$3`, userID, credentialID, expected,
		envelope.Ciphertext, envelope.Nonce, envelope.KeyVersion, credential.Authenticator.SignCount, now.UTC())
	if err != nil || command.RowsAffected() != 1 {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,$2,$3)`, userID, event, now.UTC()); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *PasskeyRepository) RecordCloneWarning(ctx context.Context, userID ids.UserID, credentialID []byte, now time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO user_security_events (user_id,event_type,occurred_at)
		SELECT $1,'passkey_clone_warning',$3
		WHERE EXISTS (SELECT 1 FROM passkey_credentials WHERE user_id=$1 AND credential_id=$2)`, userID, credentialID, now.UTC())
	return err
}

func (r *PasskeyRepository) ListCredentials(ctx context.Context, userID ids.UserID) ([]passkeys.CredentialRecord, error) {
	return r.loadCredentials(ctx, userID)
}

func (r *PasskeyRepository) DeleteCredential(ctx context.Context, userID ids.UserID, credentialID []byte, now time.Time) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM passkey_credentials WHERE user_id=$1 AND credential_id=$2`, userID, credentialID)
	if err != nil || command.RowsAffected() != 1 {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_security_events (user_id,event_type,occurred_at) VALUES ($1,'passkey_removed',$2)`, userID, now.UTC()); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *PasskeyRepository) loadCredentials(ctx context.Context, userID ids.UserID) ([]passkeys.CredentialRecord, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT credential_id,name,encrypted_credential,encryption_nonce,encryption_key_version,created_at,last_used_at
		FROM passkey_credentials WHERE user_id=$1 ORDER BY created_at,credential_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]passkeys.CredentialRecord, 0)
	for rows.Next() {
		var value passkeys.CredentialRecord
		var credentialID []byte
		var envelope passkeys.Envelope
		if err := rows.Scan(&credentialID, &value.Name, &envelope.Ciphertext, &envelope.Nonce, &envelope.KeyVersion, &value.CreatedAt, &value.LastUsedAt); err != nil {
			return nil, err
		}
		value.UserID = userID
		raw, err := r.cipher.Open(credentialLabel(userID, credentialID), envelope)
		if err != nil || json.Unmarshal(raw, &value.Credential) != nil || !bytes.Equal(value.Credential.ID, credentialID) {
			return nil, errors.New("passkey credential record is corrupt")
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *PasskeyRepository) sealCredential(userID ids.UserID, credential webauthn.Credential) (passkeys.Envelope, error) {
	raw, err := json.Marshal(credential)
	if err != nil {
		return passkeys.Envelope{}, err
	}
	return r.cipher.Seal(credentialLabel(userID, credential.ID), raw)
}

func credentialLabel(userID ids.UserID, credentialID []byte) string {
	return "passkey/credential/" + string(userID) + "/" + base64.RawURLEncoding.EncodeToString(credentialID)
}

func ceremonyLabel(id string, kind passkeys.CeremonyKind, userID ids.UserID, sessionID ids.SessionID) string {
	return fmt.Sprintf("passkey/ceremony/%s/%s/%s/%s", id, kind, userID, sessionID)
}

func nullableUUID[T ~string](value T) any {
	if value == "" {
		return nil
	}
	return string(value)
}

var _ passkeys.Repository = (*PasskeyRepository)(nil)
