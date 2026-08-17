package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/invitations"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type InvitationRepository struct{ pool *pgxpool.Pool }

func NewInvitationRepository(pool *pgxpool.Pool) *InvitationRepository {
	return &InvitationRepository{pool: pool}
}

func (r *InvitationRepository) Create(ctx context.Context, value accounts.Invitation) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.account_id=$1 AND u.primary_email=$2 AND m.state='active')`, value.AccountID, value.Email).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return invitations.ErrMembershipExists
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET state='revoked',revoked_at=$3 WHERE account_id=$1 AND email=$2 AND state='pending'`, value.AccountID, value.Email, value.CreatedAt); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO invitations(id,account_id,email,role,state,invited_by_user_id,token_hash,expires_at,created_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, value.ID, value.AccountID, value.Email, value.Role, value.State, value.InvitedByUserID, value.TokenHash[:], value.ExpiresAt, value.CreatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *InvitationRepository) Delete(ctx context.Context, id ids.InvitationID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM invitations WHERE id=$1 AND state='pending'`, id)
	return err
}
func (r *InvitationRepository) Accept(ctx context.Context, userID ids.UserID, hash [32]byte, now time.Time, membershipID ids.MembershipID) (accounts.Membership, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accounts.Membership{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invitation accounts.Invitation
	var token []byte
	var userEmail string
	err = tx.QueryRow(ctx, `SELECT i.id,i.account_id,i.email,i.role,i.state,i.invited_by_user_id,i.token_hash,i.expires_at,i.created_at,i.accepted_at,i.revoked_at,u.primary_email FROM invitations i JOIN users u ON u.id=$2 WHERE i.token_hash=$1 FOR UPDATE`, hash[:], userID).Scan(&invitation.ID, &invitation.AccountID, &invitation.Email, &invitation.Role, &invitation.State, &invitation.InvitedByUserID, &token, &invitation.ExpiresAt, &invitation.CreatedAt, &invitation.AcceptedAt, &invitation.RevokedAt, &userEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Membership{}, invitations.ErrInvitationNotFound
	}
	if err != nil {
		return accounts.Membership{}, err
	}
	if invitation.State != accounts.InvitationPending {
		return accounts.Membership{}, invitations.ErrInvitationConsumed
	}
	if !invitation.ExpiresAt.After(now) {
		return accounts.Membership{}, invitations.ErrInvitationExpired
	}
	if invitation.Email != userEmail {
		return accounts.Membership{}, invitations.ErrInvitationEmailMismatch
	}
	membership := accounts.Membership{ID: membershipID, AccountID: invitation.AccountID, UserID: userID, Role: invitation.Role, State: accounts.MembershipActive, Version: 1, CreatedAt: now.UTC()}
	_, err = tx.Exec(ctx, `INSERT INTO memberships(id,account_id,user_id,role,state,version,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, membership.ID, membership.AccountID, membership.UserID, membership.Role, membership.State, membership.Version, membership.CreatedAt)
	if isUniqueConstraint(err, "memberships_account_id_user_id_key") {
		return accounts.Membership{}, invitations.ErrMembershipExists
	}
	if err != nil {
		return accounts.Membership{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE invitations SET state='accepted',accepted_at=$2 WHERE id=$1`, invitation.ID, now.UTC()); err != nil {
		return accounts.Membership{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return accounts.Membership{}, err
	}
	return membership, nil
}

var _ invitations.Repository = (*InvitationRepository)(nil)
