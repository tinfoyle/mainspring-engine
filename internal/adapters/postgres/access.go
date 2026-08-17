package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccessRepository struct{ pool *pgxpool.Pool }

func NewAccessRepository(pool *pgxpool.Pool) *AccessRepository { return &AccessRepository{pool: pool} }

func (r *AccessRepository) AccessState(ctx context.Context, userID ids.UserID, accountID ids.AccountID) (access.State, error) {
	var state access.State
	var effectivePackages []byte
	err := r.pool.QueryRow(ctx, `
		SELECT a.id,a.slug,a.display_name,a.account_type,a.state,a.cell_id,
		       a.placement_generation,a.entitlement_version,a.created_by_user_id,a.created_at,
		       m.id,m.account_id,m.user_id,m.role,m.state,m.version,m.created_at,
		       s.account_id,s.version,s.catalog_version,s.evaluated_at,s.effective_packages
		FROM accounts a
		JOIN memberships m ON m.account_id=a.id AND m.user_id=$1
		JOIN LATERAL (
			SELECT account_id,version,catalog_version,evaluated_at,effective_packages
			FROM entitlement_snapshots WHERE account_id=a.id
			ORDER BY version DESC LIMIT 1
		) s ON true
		WHERE a.id=$2`, userID, accountID).Scan(
		&state.Account.ID, &state.Account.Slug, &state.Account.DisplayName, &state.Account.Type,
		&state.Account.State, &state.Account.CellID, &state.Account.PlacementGeneration,
		&state.Account.EntitlementVersion, &state.Account.CreatedByUserID, &state.Account.CreatedAt,
		&state.Membership.ID, &state.Membership.AccountID, &state.Membership.UserID,
		&state.Membership.Role, &state.Membership.State, &state.Membership.Version,
		&state.Membership.CreatedAt, &state.Entitlements.AccountID, &state.Entitlements.Version,
		&state.Entitlements.CatalogVersion, &state.Entitlements.EvaluatedAt, &effectivePackages)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.State{}, &access.DeniedError{Code: access.DenialMembership}
	}
	if err != nil {
		return access.State{}, err
	}
	if err := json.Unmarshal(effectivePackages, &state.Entitlements.Packages); err != nil {
		return access.State{}, err
	}
	return state, nil
}

var _ access.StateSource = (*AccessRepository)(nil)
