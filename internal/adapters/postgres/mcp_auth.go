package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/mcpauth"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type MCPAuthRepository struct{ pool *pgxpool.Pool }

func NewMCPAuthRepository(pool *pgxpool.Pool) *MCPAuthRepository {
	return &MCPAuthRepository{pool: pool}
}

func (r *MCPAuthRepository) CreateAuthorization(ctx context.Context, value mcpauth.PendingAuthorization) error {
	command, err := r.pool.Exec(ctx, `
		INSERT INTO mcp_oauth_authorizations
		(id,user_id,session_id,client_id,client_name,redirect_uri,resource,scope,client_state,code_challenge,status,expires_at,created_at)
		SELECT $1,s.user_id,s.id,$4,$5,$6,$7,$8,$9,$10,'pending',$11,$12
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.id=$3 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>$12
		  AND u.state='active' AND u.security_version=s.security_version`,
		value.ID, value.UserID, value.SessionID, value.ClientID, value.ClientName, value.RedirectURI,
		value.Resource, value.Scope, value.State, value.CodeChallenge, value.ExpiresAt.UTC(), value.CreatedAt.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return mcpauth.ErrAccessDenied
	}
	return nil
}

func (r *MCPAuthRepository) DecideAuthorization(ctx context.Context, decision mcpauth.AuthorizationDecision, codeHash [32]byte, grantID string, now, codeExpiresAt time.Time) (mcpauth.PendingAuthorization, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pending mcpauth.PendingAuthorization
	var status string
	var securityVersion int64
	err = tx.QueryRow(ctx, `
		SELECT request.id,request.user_id,request.session_id,request.client_id,request.client_name,
		       request.redirect_uri,request.resource,request.scope,request.client_state,request.code_challenge,
		       request.expires_at,request.created_at,request.status,identity.security_version
		FROM mcp_oauth_authorizations request
		JOIN sessions session_record ON session_record.id=request.session_id AND session_record.user_id=request.user_id
		JOIN users identity ON identity.id=request.user_id
		WHERE request.id=$1 AND request.user_id=$2 AND request.session_id=$3
		  AND session_record.revoked_at IS NULL AND session_record.expires_at>$4
		  AND identity.state='active' AND identity.security_version=session_record.security_version
		FOR UPDATE OF request,session_record,identity`, decision.PendingID, decision.UserID, decision.SessionID, now.UTC()).Scan(
		&pending.ID, &pending.UserID, &pending.SessionID, &pending.ClientID, &pending.ClientName,
		&pending.RedirectURI, &pending.Resource, &pending.Scope, &pending.State, &pending.CodeChallenge,
		&pending.ExpiresAt, &pending.CreatedAt, &status, &securityVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return mcpauth.PendingAuthorization{}, mcpauth.ErrNotFound
	}
	if err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	if status != "pending" {
		return mcpauth.PendingAuthorization{}, mcpauth.ErrConsumed
	}
	if !pending.ExpiresAt.After(now) {
		return mcpauth.PendingAuthorization{}, mcpauth.ErrExpired
	}

	if !decision.Approve {
		if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_authorizations SET status='denied',decided_at=$2 WHERE id=$1`, pending.ID, now.UTC()); err != nil {
			return mcpauth.PendingAuthorization{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,event_type,occurred_at) VALUES ($1,'authorization_denied',$2)`, pending.UserID, now.UTC()); err != nil {
			return mcpauth.PendingAuthorization{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return mcpauth.PendingAuthorization{}, err
		}
		return pending, nil
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_grants
		(id,user_id,client_id,client_name,resource,scope,security_version,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, grantID, pending.UserID, pending.ClientID,
		pending.ClientName, pending.Resource, pending.Scope, securityVersion, now.UTC()); err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_codes(code_hash,grant_id,redirect_uri,code_challenge,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, codeHash[:], grantID, pending.RedirectURI, pending.CodeChallenge, codeExpiresAt.UTC(), now.UTC()); err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_authorizations SET status='approved',decided_at=$2 WHERE id=$1`, pending.ID, now.UTC()); err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'authorization_approved',$3)`, pending.UserID, grantID, now.UTC()); err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return mcpauth.PendingAuthorization{}, err
	}
	return pending, nil
}

func (r *MCPAuthRepository) ExchangeCode(ctx context.Context, exchange mcpauth.CodeExchange) (mcpauth.IssuedAuthority, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var issued mcpauth.IssuedAuthority
	var storedClientID, storedRedirect, storedChallenge, state string
	var codeExpires time.Time
	var consumedAt, grantRevokedAt *time.Time
	var securityVersion, currentSecurityVersion int64
	var grantID string
	err = tx.QueryRow(ctx, `
		SELECT grant_record.id,grant_record.user_id,grant_record.client_id,code_record.redirect_uri,
		       grant_record.resource,grant_record.scope,code_record.code_challenge,code_record.expires_at,
		       code_record.consumed_at,grant_record.revoked_at,grant_record.security_version,
		       identity.security_version,identity.state
		FROM mcp_oauth_codes code_record
		JOIN mcp_oauth_grants grant_record ON grant_record.id=code_record.grant_id
		JOIN users identity ON identity.id=grant_record.user_id
		WHERE code_record.code_hash=$1
		FOR UPDATE OF code_record,grant_record,identity`, exchange.CodeHash[:]).Scan(
		&grantID, &issued.UserID, &storedClientID, &storedRedirect, &issued.Resource, &issued.Scope,
		&storedChallenge, &codeExpires, &consumedAt, &grantRevokedAt, &securityVersion, &currentSecurityVersion, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrNotFound
	}
	if err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if consumedAt != nil {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrConsumed
	}
	if !codeExpires.After(exchange.Now) {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrExpired
	}
	if grantRevokedAt != nil || state != "active" || securityVersion != currentSecurityVersion {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrAccessDenied
	}
	if storedClientID != exchange.ClientID || storedRedirect != exchange.RedirectURI || issued.Resource != exchange.Resource || storedChallenge != exchange.VerifierChallenge {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrInvalid
	}

	var familyID string
	if err = tx.QueryRow(ctx, `SELECT gen_random_uuid()`).Scan(&familyID); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_codes SET consumed_at=$2 WHERE code_hash=$1`, exchange.CodeHash[:], exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_access_tokens(token_hash,grant_id,family_id,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5)`, exchange.Access.Hash[:], grantID, familyID, exchange.Access.ExpiresAt.UTC(), exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_refresh_tokens(token_hash,grant_id,family_id,sequence,expires_at,created_at)
		VALUES ($1,$2,$3,1,$4,$5)`, exchange.Refresh.Hash[:], grantID, familyID, exchange.Refresh.ExpiresAt.UTC(), exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_grants SET last_used_at=$2 WHERE id=$1`, grantID, exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'code_exchanged',$3)`, issued.UserID, grantID, exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	return issued, nil
}

func (r *MCPAuthRepository) RotateRefresh(ctx context.Context, exchange mcpauth.RefreshExchange) (mcpauth.IssuedAuthority, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var issued mcpauth.IssuedAuthority
	var grantID, familyID, storedClientID, state string
	var sequence, securityVersion, currentSecurityVersion int64
	var expiresAt time.Time
	var consumedAt, tokenRevokedAt, grantRevokedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT grant_record.id,refresh.family_id,refresh.sequence,grant_record.user_id,
		       grant_record.client_id,grant_record.resource,grant_record.scope,refresh.expires_at,
		       refresh.consumed_at,refresh.revoked_at,grant_record.revoked_at,
		       grant_record.security_version,identity.security_version,identity.state
		FROM mcp_oauth_refresh_tokens refresh
		JOIN mcp_oauth_grants grant_record ON grant_record.id=refresh.grant_id
		JOIN users identity ON identity.id=grant_record.user_id
		WHERE refresh.token_hash=$1
		FOR UPDATE OF refresh,grant_record,identity`, exchange.RefreshHash[:]).Scan(
		&grantID, &familyID, &sequence, &issued.UserID, &storedClientID, &issued.Resource, &issued.Scope,
		&expiresAt, &consumedAt, &tokenRevokedAt, &grantRevokedAt, &securityVersion, &currentSecurityVersion, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrNotFound
	}
	if err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if consumedAt != nil {
		if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, exchange.Now.UTC()); err != nil {
			return mcpauth.IssuedAuthority{}, err
		}
		if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, exchange.Now.UTC()); err != nil {
			return mcpauth.IssuedAuthority{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'refresh_reuse_detected',$3)`, issued.UserID, grantID, exchange.Now.UTC()); err != nil {
			return mcpauth.IssuedAuthority{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return mcpauth.IssuedAuthority{}, err
		}
		return mcpauth.IssuedAuthority{}, mcpauth.ErrRefreshReuse
	}
	if tokenRevokedAt != nil || grantRevokedAt != nil || state != "active" || securityVersion != currentSecurityVersion {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrAccessDenied
	}
	if !expiresAt.After(exchange.Now) {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrExpired
	}
	if storedClientID != exchange.ClientID || issued.Resource != exchange.Resource {
		return mcpauth.IssuedAuthority{}, mcpauth.ErrInvalid
	}

	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_refresh_tokens SET consumed_at=$2 WHERE token_hash=$1`, exchange.RefreshHash[:], exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_access_tokens(token_hash,grant_id,family_id,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5)`, exchange.Access.Hash[:], grantID, familyID, exchange.Access.ExpiresAt.UTC(), exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO mcp_oauth_refresh_tokens(token_hash,grant_id,family_id,sequence,expires_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6)`, exchange.Refresh.Hash[:], grantID, familyID, sequence+1, exchange.Refresh.ExpiresAt.UTC(), exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_grants SET last_used_at=$2 WHERE id=$1`, grantID, exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'token_refreshed',$3)`, issued.UserID, grantID, exchange.Now.UTC()); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return mcpauth.IssuedAuthority{}, err
	}
	return issued, nil
}

func (r *MCPAuthRepository) AuthenticateAccess(ctx context.Context, hash [32]byte, requirement mcpauth.TokenRequirement, now time.Time) (access.Actor, error) {
	var userID ids.UserID
	err := r.pool.QueryRow(ctx, `SELECT user_id FROM spyglass_authenticate_mcp_access_token($1,$2,$3,$4)`, hash[:], requirement.Audience, requirement.Scope, now.UTC()).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Actor{}, mcpauth.ErrAccessDenied
	}
	if err != nil {
		return access.Actor{}, err
	}
	return access.Actor{UserID: userID}, nil
}

func (r *MCPAuthRepository) Revoke(ctx context.Context, hash [32]byte, clientID string, now time.Time) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var userID ids.UserID
	var grantID, familyID string
	err = tx.QueryRow(ctx, `
		SELECT grant_record.user_id,grant_record.id,token.family_id
		FROM mcp_oauth_access_tokens token
		JOIN mcp_oauth_grants grant_record ON grant_record.id=token.grant_id
		WHERE token.token_hash=$1 AND grant_record.client_id=$2
		FOR UPDATE OF token,grant_record`, hash[:], clientID).Scan(&userID, &grantID, &familyID)
	if err == nil {
		if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, now.UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, now.UTC()); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'token_revoked',$3)`, userID, grantID, now.UTC()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	err = tx.QueryRow(ctx, `
		SELECT grant_record.user_id,grant_record.id,refresh.family_id
		FROM mcp_oauth_refresh_tokens refresh
		JOIN mcp_oauth_grants grant_record ON grant_record.id=refresh.grant_id
		WHERE refresh.token_hash=$1 AND grant_record.client_id=$2
		FOR UPDATE OF refresh,grant_record`, hash[:], clientID).Scan(&userID, &grantID, &familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, now.UTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE family_id=$1`, familyID, now.UTC()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'token_revoked',$3)`, userID, grantID, now.UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *MCPAuthRepository) ListGrants(ctx context.Context, userID ids.UserID, now time.Time) ([]mcpauth.GrantSummary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT grant_record.id,grant_record.client_id,grant_record.client_name,
		       grant_record.created_at,grant_record.last_used_at
		FROM mcp_oauth_grants grant_record
		JOIN users identity ON identity.id=grant_record.user_id
		WHERE grant_record.user_id=$1 AND grant_record.revoked_at IS NULL
		  AND identity.state='active'
		  AND identity.security_version=grant_record.security_version
		  AND (
		    EXISTS (SELECT 1 FROM mcp_oauth_access_tokens access_token
		            WHERE access_token.grant_id=grant_record.id AND access_token.revoked_at IS NULL AND access_token.expires_at>$2)
		    OR EXISTS (SELECT 1 FROM mcp_oauth_refresh_tokens refresh_token
		               WHERE refresh_token.grant_id=grant_record.id AND refresh_token.revoked_at IS NULL AND refresh_token.consumed_at IS NULL AND refresh_token.expires_at>$2)
		  )
		ORDER BY COALESCE(grant_record.last_used_at,grant_record.created_at) DESC,grant_record.id DESC`, userID, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := make([]mcpauth.GrantSummary, 0)
	for rows.Next() {
		var grant mcpauth.GrantSummary
		if err := rows.Scan(&grant.ID, &grant.ClientID, &grant.ClientName, &grant.CreatedAt, &grant.LastUsedAt); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (r *MCPAuthRepository) RevokeGrant(ctx context.Context, userID ids.UserID, grantID string, now time.Time) (bool, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	command, err := tx.Exec(ctx, `
		UPDATE mcp_oauth_grants
		SET revoked_at=$3
		WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, grantID, userID, now.UTC())
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE grant_id=$1`, grantID, now.UTC()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_oauth_access_tokens SET revoked_at=COALESCE(revoked_at,$2) WHERE grant_id=$1`, grantID, now.UTC()); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_oauth_events(user_id,grant_id,event_type,occurred_at) VALUES ($1,$2,'grant_revoked',$3)`, userID, grantID, now.UTC()); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

var _ mcpauth.Repository = (*MCPAuthRepository)(nil)
