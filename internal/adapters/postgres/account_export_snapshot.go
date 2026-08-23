package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

type AccountExportSourceFactory interface {
	Sources(context.Context, pgx.Tx, pgx.Tx, accountexport.Work) ([]accountexport.SectionSource, []accountexport.ObjectSource, error)
}

type AccountExportSnapshotCoordinator struct {
	global  *pgxpool.Pool
	cell    *pgxpool.Pool
	cellID  ids.CellID
	factory AccountExportSourceFactory
}

func NewAccountExportSnapshotCoordinator(global, cell *pgxpool.Pool, cellID ids.CellID, factory AccountExportSourceFactory) (*AccountExportSnapshotCoordinator, error) {
	if global == nil || cell == nil || !routecontext.ValidCellID(cellID) || factory == nil {
		return nil, accountexport.ErrInvalid
	}
	return &AccountExportSnapshotCoordinator{global: global, cell: cell, cellID: cellID, factory: factory}, nil
}

func (coordinator *AccountExportSnapshotCoordinator) WithSnapshot(ctx context.Context, work accountexport.Work, callback func(accountexport.Snapshot, []accountexport.SectionSource, []accountexport.ObjectSource) error) error {
	if callback == nil || ids.Validate(work.ID) != nil || ids.Validate(string(work.AccountID)) != nil || work.CellID != coordinator.cellID || work.PlacementGeneration == 0 || work.AccountVersion == 0 {
		return snapshotFailure("snapshot_identity_invalid", true, accountexport.ErrInvalid)
	}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	globalTx, err := coordinator.global.BeginTx(ctx, options)
	if err != nil {
		return snapshotFailure("snapshot_unavailable", false, err)
	}
	defer globalTx.Rollback(context.Background())

	var accountVersion int64
	var accountState, directoryCell, directoryState string
	var directoryGeneration int64
	var globalAt time.Time
	err = globalTx.QueryRow(ctx, `
		SELECT a.version,a.state,d.cell_id,d.placement_generation,d.state,transaction_timestamp()
		FROM accounts a
		JOIN account_directory d ON d.account_id=a.id
		WHERE a.id=$1`, work.AccountID).Scan(&accountVersion, &accountState, &directoryCell, &directoryGeneration, &directoryState, &globalAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return snapshotFailure("snapshot_identity_invalid", true, accountexport.ErrInvalid)
		}
		return snapshotFailure("snapshot_unavailable", false, err)
	}
	if accountState != "active" || directoryState != "active" || directoryCell != string(work.CellID) || accountVersion < 1 || directoryGeneration < 1 || uint64(accountVersion) != work.AccountVersion || uint64(directoryGeneration) != work.PlacementGeneration {
		return snapshotFailure("snapshot_identity_invalid", true, accountexport.ErrInvalid)
	}

	cellTx, err := coordinator.cell.BeginTx(ctx, options)
	if err != nil {
		return snapshotFailure("snapshot_unavailable", false, err)
	}
	defer cellTx.Rollback(context.Background())
	var configuredAccount string
	if err := cellTx.QueryRow(ctx, `SELECT set_config('spyglass.account_id',$1,true)`, work.AccountID).Scan(&configuredAccount); err != nil || configuredAccount != string(work.AccountID) {
		return snapshotFailure("snapshot_unavailable", false, err)
	}
	var namespaceGeneration int64
	var namespaceState string
	var cellAt time.Time
	err = cellTx.QueryRow(ctx, `
		SELECT placement_generation,state,transaction_timestamp()
		FROM spyglass.account_namespaces
		WHERE account_id=$1`, work.AccountID).Scan(&namespaceGeneration, &namespaceState, &cellAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return snapshotFailure("snapshot_identity_invalid", true, accountexport.ErrInvalid)
		}
		return snapshotFailure("snapshot_unavailable", false, err)
	}
	if namespaceState != "active" || namespaceGeneration < 1 || uint64(namespaceGeneration) != work.PlacementGeneration {
		return snapshotFailure("snapshot_identity_invalid", true, accountexport.ErrInvalid)
	}
	sections, objects, err := coordinator.factory.Sources(ctx, globalTx, cellTx, work)
	if err != nil {
		return err
	}
	snapshot := accountexport.Snapshot{CellID: coordinator.cellID, PlacementGeneration: work.PlacementGeneration,
		AccountVersion: work.AccountVersion, GlobalAt: globalAt.UTC(), CellAt: cellAt.UTC()}
	if err := callback(snapshot, sections, objects); err != nil {
		return err
	}
	if err := cellTx.Commit(ctx); err != nil {
		return snapshotFailure("snapshot_commit_unknown", false, err)
	}
	if err := globalTx.Commit(ctx); err != nil {
		return snapshotFailure("snapshot_commit_unknown", false, err)
	}
	return nil
}

type accountExportSnapshotFailure struct {
	code      string
	permanent bool
	cause     error
}

func snapshotFailure(code string, permanent bool, cause error) error {
	if cause == nil {
		cause = accountexport.ErrUnavailable
	}
	return accountExportSnapshotFailure{code: code, permanent: permanent, cause: cause}
}

func (failure accountExportSnapshotFailure) Error() string {
	return fmt.Sprintf("Account export %s: %v", failure.code, failure.cause)
}

func (failure accountExportSnapshotFailure) Unwrap() error   { return failure.cause }
func (failure accountExportSnapshotFailure) Code() string    { return failure.code }
func (failure accountExportSnapshotFailure) Permanent() bool { return failure.permanent }

var _ accountexport.SnapshotCoordinator = (*AccountExportSnapshotCoordinator)(nil)
