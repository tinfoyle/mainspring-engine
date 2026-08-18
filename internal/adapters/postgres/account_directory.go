package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountdirectory"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type AccountDirectoryRepository struct{ pool *pgxpool.Pool }

func NewAccountDirectoryRepository(pool *pgxpool.Pool) *AccountDirectoryRepository {
	return &AccountDirectoryRepository{pool: pool}
}

func (r *AccountDirectoryRepository) Lookup(ctx context.Context, accountID ids.AccountID) (accountdirectory.Entry, error) {
	var entry accountdirectory.Entry
	err := r.pool.QueryRow(ctx, `
		SELECT d.account_id,d.cell_id,d.placement_generation,d.state,d.data_region,
		       c.state,COALESCE(c.route_origin,''),d.updated_at
		FROM account_directory d
		JOIN cells c ON c.id=d.cell_id
		WHERE d.account_id=$1`, accountID).Scan(
		&entry.AccountID, &entry.CellID, &entry.PlacementGeneration, &entry.State,
		&entry.DataRegion, &entry.CellState, &entry.RouteOrigin, &entry.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountdirectory.Entry{}, accountdirectory.ErrNotFound
	}
	if err != nil {
		return accountdirectory.Entry{}, err
	}
	return entry, nil
}

var _ accountdirectory.Source = (*AccountDirectoryRepository)(nil)
