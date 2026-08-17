package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

// CellPool owns a connection pool for one cell database. It never owns a pool
// per Account. Every transaction sets a transaction-local Account identity so
// PostgreSQL RLS remains a defense-in-depth boundary.
type CellPool struct{ pool *pgxpool.Pool }

func NewCellPool(pool *pgxpool.Pool) (*CellPool, error) {
	if pool == nil {
		return nil, errors.New("cell pool is required")
	}
	return &CellPool{pool: pool}, nil
}

type AccountTx func(context.Context, pgx.Tx) error

func (p *CellPool) WithAccountTx(ctx context.Context, accountID ids.AccountID, options pgx.TxOptions, fn AccountTx) error {
	if accountID == "" {
		return errors.New("account ID is required")
	}
	if fn == nil {
		return errors.New("transaction function is required")
	}
	tx, err := p.pool.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.account_id', $1, true)`, string(accountID)); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
