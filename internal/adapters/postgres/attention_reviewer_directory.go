package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	attentionapp "github.com/tinfoyle/spyglass-engine/internal/application/attention"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// AttentionReviewerDirectory resolves reviewer eligibility from the global
// Membership source of truth. It is composed only into the private admission
// broker; a serving cell never receives this database credential.
type AttentionReviewerDirectory struct{ pool *pgxpool.Pool }

func NewAttentionReviewerDirectory(pool *pgxpool.Pool) *AttentionReviewerDirectory {
	return &AttentionReviewerDirectory{pool: pool}
}

func (r *AttentionReviewerDirectory) ActiveRole(ctx context.Context, accountID ids.AccountID, userID ids.UserID) (accounts.MembershipRole, bool, error) {
	if r == nil || r.pool == nil || ids.Validate(string(accountID)) != nil || ids.Validate(string(userID)) != nil {
		return "", false, attentionapp.ErrInvalidCommand
	}
	var role accounts.MembershipRole
	err := r.pool.QueryRow(ctx, `SELECT role FROM memberships WHERE account_id=$1 AND user_id=$2 AND state='active'`, accountID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch role {
	case accounts.RoleOwner, accounts.RoleAdministrator, accounts.RoleBillingAdmin, accounts.RoleMember, accounts.RoleViewer:
		return role, true, nil
	default:
		return "", false, attentionapp.ErrCorrupt
	}
}

var _ attentionapp.ReviewerDirectory = (*AttentionReviewerDirectory)(nil)
