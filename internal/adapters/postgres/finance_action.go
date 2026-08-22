package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	financeapp "github.com/tinfoyle/spyglass-engine/internal/application/finance"
	"github.com/tinfoyle/spyglass-engine/internal/application/financeaction"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	financedomain "github.com/tinfoyle/spyglass-engine/internal/modules/finance"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type FinanceActionStore struct {
	cell       *database.CellPool
	repository *FinanceRepository
	clock      interface{ Now() time.Time }
}

func NewFinanceActionStore(cell *database.CellPool, repository *FinanceRepository, clock interface{ Now() time.Time }) (*FinanceActionStore, error) {
	if cell == nil || repository == nil || clock == nil {
		return nil, errors.New("finance action store dependencies are required")
	}
	return &FinanceActionStore{cell: cell, repository: repository, clock: clock}, nil
}

func (store *FinanceActionStore) PostEntry(ctx context.Context, accountID ids.AccountID, entryID ids.FinanceEntryID, expected uint64, operationID string, approvedBy ids.UserID) error {
	actor := financedomain.Actor{Kind: financedomain.ActorUser, ID: string(approvedBy)}
	_, err := store.repository.PostEntry(ctx, accountID, entryID, expected, actor, accounts.RoleAdministrator, financeapp.Mutation{
		EventID: operationID, Kind: "posted", Actor: actor, CorrelationID: operationID, At: store.clock.Now().UTC(),
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, financeapp.ErrInvalid) || errors.Is(err, financeapp.ErrNotFound) {
		return errors.Join(financeaction.ErrInvalid, err)
	}
	if errors.Is(err, financeapp.ErrConflict) {
		return errors.Join(financeaction.ErrConflict, err)
	}
	return errors.Join(financeaction.ErrRepository, err)
}

func (store *FinanceActionStore) EntryPostedByEvent(ctx context.Context, accountID ids.AccountID, entryID ids.FinanceEntryID, expected uint64, operationID string) (bool, error) {
	var matched bool
	err := store.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		matched, err = financeEventMatches(ctx, tx, accountID, operationID, "entry", string(entryID), "posted", expected, expected+1)
		return err
	})
	if err != nil {
		return false, errors.Join(financeaction.ErrRepository, err)
	}
	return matched, nil
}

var _ financeaction.Store = (*FinanceActionStore)(nil)
