package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/oidcauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/identity"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type OIDCAuthenticationRepository struct{ pool *pgxpool.Pool }

func NewOIDCAuthenticationRepository(pool *pgxpool.Pool) *OIDCAuthenticationRepository {
	return &OIDCAuthenticationRepository{pool: pool}
}

func (r *OIDCAuthenticationRepository) UserForOIDC(ctx context.Context, identifier string) (identity.User, error) {
	var user identity.User
	err := r.pool.QueryRow(ctx, `
		SELECT u.id,u.primary_email,u.display_name,u.state,u.email_verified_at,u.security_version,u.created_at
		FROM authentication_identities i JOIN users u ON u.id=i.user_id
		WHERE i.provider='oidc' AND i.identifier=$1`, identifier).Scan(
		&user.ID, &user.PrimaryEmail, &user.DisplayName, &user.State, &user.EmailVerifiedAt,
		&user.SecurityVersion, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.User{}, oidcauth.ErrIdentityNotFound
	}
	return user, err
}

func (r *OIDCAuthenticationRepository) Connected(ctx context.Context, userID ids.UserID, issuerPrefix string) (bool, error) {
	var connected bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM authentication_identities
		WHERE user_id=$1 AND provider='oidc' AND left(identifier,length($2))=$2
	)`, userID, issuerPrefix).Scan(&connected)
	return connected, err
}

func (r *OIDCAuthenticationRepository) Connect(ctx context.Context, userID ids.UserID, identifier string, now time.Time) error {
	command, err := r.pool.Exec(ctx, `
		INSERT INTO authentication_identities (user_id,provider,identifier,created_at,updated_at)
		SELECT id,'oidc',$2,$3,$3 FROM users WHERE id=$1 AND state='active'`, userID, identifier, now.UTC())
	if isUniqueConstraint(err, "authentication_identities_pkey", "authentication_identities_user_id_provider_key") {
		return oidcauth.ErrIdentityConflict
	}
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return oidcauth.ErrIdentityNotFound
	}
	return nil
}

func (r *OIDCAuthenticationRepository) Disconnect(ctx context.Context, userID ids.UserID, issuerPrefix string, _ time.Time) error {
	command, err := r.pool.Exec(ctx, `
		DELETE FROM authentication_identities
		WHERE user_id=$1 AND provider='oidc' AND left(identifier,length($2))=$2
		  AND EXISTS (SELECT 1 FROM authentication_identities local_identity WHERE local_identity.user_id=$1 AND local_identity.provider='local')`, userID, issuerPrefix)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return oidcauth.ErrLastLoginMethod
	}
	return nil
}

var _ oidcauth.Repository = (*OIDCAuthenticationRepository)(nil)
