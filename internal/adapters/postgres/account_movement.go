package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountmovement"
)

const accountMoveColumns = `
	id::text,account_id::text,state,COALESCE(resume_state,''),source_cell_id,destination_cell_id,
	source_generation,destination_generation,COALESCE(rollback_generation,0),account_version,version,
	COALESCE(lease_id::text,''),lease_expires_at,COALESCE(source_high_watermark,''),
	COALESCE(source_manifest,'{}'::jsonb),COALESCE(destination_manifest,'{}'::jsonb),
	COALESCE(source_digest,''::bytea),COALESCE(destination_digest,''::bytea),rollback_window_seconds,
	rollback_expires_at,prepared_at,environment`

type AccountMovementRepository struct{ pool *pgxpool.Pool }

func NewAccountMovementRepository(pool *pgxpool.Pool) *AccountMovementRepository {
	return &AccountMovementRepository{pool: pool}
}

func (r *AccountMovementRepository) Prepare(ctx context.Context, moveID string, command accountmovement.PrepareCommand, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_prepare_account_move($1,$2,$3,$4,$5,$6,$7,$8)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, moveID, change.EventID, command.AccountID, command.DestinationCellID,
		int(command.RollbackWindow/time.Second), change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Inspect(ctx context.Context, moveID string, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_inspect_account_move($1,$2,$3,$4,$5)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, moveID, change.EventID, change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Claim(ctx context.Context, moveID string, expectedVersion uint64, leaseID string, duration time.Duration, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_claim_account_move($1,$2,$3,$4,$5,$6,$7,$8)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, moveID, change.EventID, expectedVersion, leaseID,
		int(duration/time.Second), change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Advance(ctx context.Context, move accountmovement.Move, action string, evidence accountmovement.Evidence, change accountmovement.Change) (accountmovement.Move, error) {
	var sourceHighWatermark any
	var sourceManifest, destinationManifest any
	var sourceDigest, destinationDigest []byte
	switch action {
	case "copy":
		sourceHighWatermark, sourceManifest, sourceDigest = evidence.HighWatermark, jsonValue(evidence.Manifest), evidence.Digest
	case "reconcile":
		destinationManifest, destinationDigest = jsonValue(evidence.Manifest), evidence.Digest
	}
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_advance_account_move($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, move.ID, change.EventID, move.Version, move.LeaseID, action,
		sourceHighWatermark, sourceManifest, sourceDigest, destinationManifest, destinationDigest, change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Switch(ctx context.Context, move accountmovement.Move, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_switch_account_move($1,$2,$3,$4,$5,$6,$7)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, move.ID, change.EventID, move.Version, move.LeaseID, change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Pause(ctx context.Context, moveID string, expectedVersion uint64, pause bool, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_pause_account_move($1,$2,$3,$4,$5,$6,$7)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, moveID, change.EventID, expectedVersion, pause, change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) Rollback(ctx context.Context, move accountmovement.Move, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_rollback_account_move($1,$2,$3,$4,$5,$6,$7)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, move.ID, change.EventID, move.Version, move.LeaseID, change.Actor, change.Reason, change.Environment)))
}

func (r *AccountMovementRepository) BeginRetirement(ctx context.Context, move accountmovement.Move, change accountmovement.Change) (accountmovement.Move, error) {
	return r.finish(ctx, move, false, change)
}

func (r *AccountMovementRepository) Complete(ctx context.Context, move accountmovement.Move, change accountmovement.Change) (accountmovement.Move, error) {
	return r.finish(ctx, move, true, change)
}

func (r *AccountMovementRepository) finish(ctx context.Context, move accountmovement.Move, complete bool, change accountmovement.Change) (accountmovement.Move, error) {
	query := `SELECT ` + accountMoveColumns + ` FROM public.spyglass_finish_account_move($1,$2,$3,$4,$5,$6,$7,$8)`
	return movementResult(scanAccountMove(r.pool.QueryRow(ctx, query, move.ID, change.EventID, move.Version, move.LeaseID, complete, change.Actor, change.Reason, change.Environment)))
}

type movementRowScanner interface{ Scan(...any) error }

func scanAccountMove(row movementRowScanner) (accountmovement.Move, error) {
	var result accountmovement.Move
	var sourceManifest, destinationManifest []byte
	var rollbackSeconds int64
	err := row.Scan(&result.ID, &result.AccountID, &result.State, &result.ResumeState, &result.SourceCellID, &result.DestinationCellID,
		&result.SourceGeneration, &result.DestinationGeneration, &result.RollbackGeneration, &result.AccountVersion, &result.Version,
		&result.LeaseID, &result.LeaseExpiresAt, &result.SourceHighWatermark, &sourceManifest, &destinationManifest,
		&result.SourceDigest, &result.DestinationDigest, &rollbackSeconds, &result.RollbackExpiresAt, &result.PreparedAt, &result.Environment)
	if err != nil {
		return accountmovement.Move{}, err
	}
	if err := json.Unmarshal(sourceManifest, &result.SourceManifest); err != nil {
		return accountmovement.Move{}, err
	}
	if err := json.Unmarshal(destinationManifest, &result.DestinationManifest); err != nil {
		return accountmovement.Move{}, err
	}
	result.RollbackWindow = time.Duration(rollbackSeconds) * time.Second
	return result, nil
}

func movementResult(result accountmovement.Move, err error) (accountmovement.Move, error) {
	if err == nil {
		return result, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return accountmovement.Move{}, accountmovement.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0002":
			return accountmovement.Move{}, accountmovement.ErrNotFound
		case "40001", "P0001", "23505", "23514", "23503":
			return accountmovement.Move{}, fmt.Errorf("%w: %s", accountmovement.ErrStateConflict, pgErr.Message)
		case "22023":
			return accountmovement.Move{}, accountmovement.ErrInvalid
		}
	}
	return accountmovement.Move{}, err
}

func jsonValue(value map[string]int64) any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

var _ accountmovement.Store = (*AccountMovementRepository)(nil)
