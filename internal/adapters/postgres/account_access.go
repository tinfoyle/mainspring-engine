package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountaccess"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountAccessRepository struct{ pool *pgxpool.Pool }

func NewAccountAccessRepository(pool *pgxpool.Pool) *AccountAccessRepository {
	return &AccountAccessRepository{pool: pool}
}

func (r *AccountAccessRepository) Choices(ctx context.Context, userID ids.UserID) ([]accountaccess.Choice, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id,a.slug,a.display_name,a.account_type,a.state,a.version,m.role,a.cell_id,a.placement_generation,
		       s.account_id,s.version,s.catalog_version,s.evaluated_at,s.effective_packages
		FROM memberships m JOIN accounts a ON a.id=m.account_id
		JOIN LATERAL (
		  SELECT account_id,version,catalog_version,evaluated_at,effective_packages
		  FROM entitlement_snapshots WHERE account_id=a.id ORDER BY version DESC LIMIT 1
		) s ON true
		WHERE m.user_id=$1 AND m.state='active' AND a.state IN ('active','restricted')
		ORDER BY lower(a.display_name),a.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]accountaccess.Choice, 0)
	for rows.Next() {
		var value accountaccess.Choice
		var packages []byte
		if err := rows.Scan(&value.AccountID, &value.Slug, &value.DisplayName, &value.AccountType, &value.AccountState, &value.AccountVersion, &value.Role, &value.CellID, &value.PlacementGeneration, &value.Entitlements.AccountID, &value.Entitlements.Version, &value.Entitlements.CatalogVersion, &value.Entitlements.EvaluatedAt, &packages); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(packages, &value.Entitlements.Packages); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

var _ accountaccess.Repository = (*AccountAccessRepository)(nil)
