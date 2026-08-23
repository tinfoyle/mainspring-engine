package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/application/accountexport"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/internal/platform/routecontext"
)

const accountExportStatusColumns = `id::text,account_id::text,requested_by_user_id::text,state,cell_id,placement_generation,account_version,
attempt_count,COALESCE(error_code,''),COALESCE(artifact_bytes,0),version,requested_at,expires_at,available_at,deleted_at`

type AccountExportRepository struct{ pool *pgxpool.Pool }

func NewAccountExportRepository(pool *pgxpool.Pool) *AccountExportRepository {
	return &AccountExportRepository{pool: pool}
}

func (repository *AccountExportRepository) Create(ctx context.Context, mutation accountexport.CreateMutation) (accountexport.Status, error) {
	if !validCreateExport(mutation) {
		return accountexport.Status{}, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountexport.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state accounts.AccountState
	var cellID ids.CellID
	var generation, accountVersion uint64
	err = tx.QueryRow(ctx, `SELECT state,cell_id,placement_generation,version FROM accounts WHERE id=$1 FOR UPDATE`, mutation.AccountID).
		Scan(&state, &cellID, &generation, &accountVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrNotFound
	}
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if state != accounts.AccountActive || cellID != mutation.CellID || generation != mutation.PlacementGeneration {
		return accountexport.Status{}, accountexport.ErrStateConflict
	}
	if err := requireActiveOwner(ctx, tx, mutation.AccountID, mutation.RequestedBy); err != nil {
		return accountexport.Status{}, err
	}
	var directoryCell ids.CellID
	var directoryGeneration uint64
	var directoryState string
	if err := tx.QueryRow(ctx, `SELECT cell_id,placement_generation,state FROM account_directory WHERE account_id=$1 FOR SHARE`, mutation.AccountID).
		Scan(&directoryCell, &directoryGeneration, &directoryState); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if directoryCell != cellID || directoryGeneration != generation || directoryState != "active" {
		return accountexport.Status{}, accountexport.ErrStateConflict
	}
	row := tx.QueryRow(ctx, `INSERT INTO account_export_requests
		(id,account_id,requested_by_user_id,state,cell_id,placement_generation,account_version,next_attempt_at,requested_at,expires_at)
		VALUES ($1,$2,$3,'queued',$4,$5,$6,$7,$7,$8) RETURNING `+accountExportStatusColumns,
		mutation.ID, mutation.AccountID, mutation.RequestedBy, mutation.CellID, mutation.PlacementGeneration, accountVersion, mutation.RequestedAt, mutation.ExpiresAt)
	status, err := scanAccountExport(row)
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, mutation.EventID, status, "requested", "user", string(mutation.RequestedBy), ""); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	return status, nil
}

func (repository *AccountExportRepository) Get(ctx context.Context, accountID ids.AccountID, id string) (accountexport.Status, error) {
	status, err := scanAccountExport(repository.pool.QueryRow(ctx, `SELECT `+accountExportStatusColumns+` FROM account_export_requests WHERE account_id=$1 AND id=$2`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrNotFound
	}
	return status, classifyAccountExport(err)
}

func (repository *AccountExportRepository) List(ctx context.Context, accountID ids.AccountID, limit uint64) ([]accountexport.Status, error) {
	if ids.Validate(string(accountID)) != nil || limit == 0 || limit > 100 {
		return nil, accountexport.ErrInvalid
	}
	rows, err := repository.pool.Query(ctx, `SELECT `+accountExportStatusColumns+` FROM account_export_requests WHERE account_id=$1 ORDER BY requested_at DESC,id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, classifyAccountExport(err)
	}
	defer rows.Close()
	result := make([]accountexport.Status, 0)
	for rows.Next() {
		status, err := scanAccountExport(rows)
		if err != nil {
			return nil, classifyAccountExport(err)
		}
		result = append(result, status)
	}
	return result, classifyAccountExport(rows.Err())
}

func (repository *AccountExportRepository) Cancel(ctx context.Context, mutation accountexport.CancelMutation) (accountexport.Status, error) {
	if ids.Validate(mutation.ID) != nil || ids.Validate(mutation.EventID) != nil || ids.Validate(string(mutation.AccountID)) != nil || ids.Validate(string(mutation.RequestedBy)) != nil || mutation.ExpectedVersion == 0 || mutation.At.IsZero() {
		return accountexport.Status{}, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return accountexport.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := requireActiveOwner(ctx, tx, mutation.AccountID, mutation.RequestedBy); err != nil {
		return accountexport.Status{}, err
	}
	status, err := scanAccountExport(tx.QueryRow(ctx, `UPDATE account_export_requests SET state='canceled',next_attempt_at=NULL,version=version+1
		WHERE id=$1 AND account_id=$2 AND state='queued' AND version=$3 RETURNING `+accountExportStatusColumns, mutation.ID, mutation.AccountID, mutation.ExpectedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrStateConflict
	}
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, mutation.EventID, status, "canceled", "user", string(mutation.RequestedBy), ""); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	return status, nil
}

func (repository *AccountExportRepository) ClaimBuild(ctx context.Context, cellID ids.CellID, now time.Time, lease time.Duration, leaseID, eventID string) (accountexport.Work, bool, error) {
	if !routecontext.ValidCellID(cellID) || now.IsZero() || lease <= 0 || lease > 30*time.Minute || ids.Validate(leaseID) != nil || ids.Validate(eventID) != nil {
		return accountexport.Work{}, false, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accountexport.Work{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	expired, expireErr := scanAccountExport(tx.QueryRow(ctx, `WITH candidate AS (
		SELECT id AS request_id FROM account_export_requests WHERE state IN ('queued','building') AND expires_at<=$1 AND cell_id=$2
		ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE account_export_requests request SET state='failed',next_attempt_at=NULL,lease_id=NULL,lease_expires_at=NULL,
		error_code='request_expired',version=request.version+1 FROM candidate WHERE request.id=candidate.request_id RETURNING `+accountExportStatusColumns, now, cellID))
	if expireErr == nil {
		if err := insertAccountExportEvent(ctx, tx, eventID, expired, "build_failed", "workload", "account-export-worker", "request_expired"); err != nil {
			return accountexport.Work{}, false, classifyAccountExport(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return accountexport.Work{}, false, classifyAccountExport(err)
		}
		return accountexport.Work{}, false, nil
	}
	if !errors.Is(expireErr, pgx.ErrNoRows) {
		return accountexport.Work{}, false, classifyAccountExport(expireErr)
	}
	drifted, driftErr := scanAccountExport(tx.QueryRow(ctx, `WITH candidate AS (
		SELECT request.id AS request_id FROM account_export_requests request
		WHERE request.state IN ('queued','building') AND request.cell_id=$1 AND NOT EXISTS (
			SELECT 1 FROM accounts account JOIN account_directory directory ON directory.account_id=account.id
			WHERE account.id=request.account_id AND account.state='active' AND account.cell_id=request.cell_id
			  AND account.placement_generation=request.placement_generation AND account.version=request.account_version
			  AND directory.cell_id=request.cell_id AND directory.placement_generation=request.placement_generation AND directory.state='active'
		) ORDER BY request.requested_at,request.id FOR UPDATE OF request SKIP LOCKED LIMIT 1
	) UPDATE account_export_requests request SET state='failed',next_attempt_at=NULL,lease_id=NULL,lease_expires_at=NULL,
		error_code='placement_changed',version=request.version+1 FROM candidate WHERE request.id=candidate.request_id RETURNING `+accountExportStatusColumns, cellID))
	if driftErr == nil {
		if err := insertAccountExportEvent(ctx, tx, eventID, drifted, "build_failed", "workload", "account-export-worker", "placement_changed"); err != nil {
			return accountexport.Work{}, false, classifyAccountExport(err)
		}
		if err := tx.Commit(ctx); err != nil {
			return accountexport.Work{}, false, classifyAccountExport(err)
		}
		return accountexport.Work{}, false, nil
	}
	if !errors.Is(driftErr, pgx.ErrNoRows) {
		return accountexport.Work{}, false, classifyAccountExport(driftErr)
	}
	var work accountexport.Work
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT request.id FROM account_export_requests request
		WHERE request.expires_at>$1 AND request.cell_id=$2 AND ((request.state='queued' AND request.next_attempt_at<=$1) OR (request.state='building' AND request.lease_expires_at<=$1))
		AND EXISTS (SELECT 1 FROM accounts account JOIN account_directory directory ON directory.account_id=account.id
			WHERE account.id=request.account_id AND account.state='active' AND account.cell_id=request.cell_id
			  AND account.placement_generation=request.placement_generation AND account.version=request.account_version
			  AND directory.cell_id=request.cell_id AND directory.placement_generation=request.placement_generation AND directory.state='active')
		ORDER BY CASE WHEN state='building' THEN 0 ELSE 1 END,COALESCE(lease_expires_at,next_attempt_at),id
		FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE account_export_requests request SET state='building',attempt_count=request.attempt_count+1,next_attempt_at=NULL,
		lease_id=$3,lease_expires_at=$4,error_code=NULL,version=request.version+1 FROM candidate
	WHERE request.id=candidate.id RETURNING request.id::text,request.account_id::text,request.requested_by_user_id::text,
		request.cell_id,request.placement_generation,request.account_version,request.attempt_count,request.version,request.requested_at,request.expires_at`,
		now, cellID, leaseID, now.Add(lease)).Scan(&work.ID, &work.AccountID, &work.RequestedBy, &work.CellID, &work.PlacementGeneration,
		&work.AccountVersion, &work.AttemptCount, &work.Version, &work.RequestedAt, &work.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Work{}, false, nil
	}
	if err != nil {
		return accountexport.Work{}, false, classifyAccountExport(err)
	}
	work.LeaseID = leaseID
	status := statusFromWork(work, accountexport.StateBuilding)
	if err := insertAccountExportEvent(ctx, tx, eventID, status, "build_claimed", "workload", "account-export-worker", ""); err != nil {
		return accountexport.Work{}, false, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Work{}, false, classifyAccountExport(err)
	}
	return work, true, nil
}

func (repository *AccountExportRepository) Complete(ctx context.Context, mutation accountexport.CompleteMutation) (accountexport.Status, error) {
	if !validCompleteExport(mutation) {
		return accountexport.Status{}, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accountexport.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status, err := scanAccountExport(tx.QueryRow(ctx, `UPDATE account_export_requests SET state='available',lease_id=NULL,lease_expires_at=NULL,
		snapshot_global_at=$5,snapshot_cell_at=$6,artifact_reference=$7,artifact_sha256=$8,artifact_bytes=$9,error_code=NULL,available_at=$10,version=version+1
		WHERE id=$1 AND account_id=$2 AND state='building' AND version=$3 AND lease_id=$4 AND lease_expires_at>=$10
		AND cell_id=$11 AND placement_generation=$12 AND account_version=$13
		AND EXISTS (SELECT 1 FROM accounts account JOIN account_directory directory ON directory.account_id=account.id
			WHERE account.id=account_export_requests.account_id AND account.state='active' AND account.cell_id=account_export_requests.cell_id
			  AND account.placement_generation=account_export_requests.placement_generation AND account.version=account_export_requests.account_version
			  AND directory.cell_id=account_export_requests.cell_id AND directory.placement_generation=account_export_requests.placement_generation AND directory.state='active')
		RETURNING `+accountExportStatusColumns,
		mutation.Work.ID, mutation.Work.AccountID, mutation.Work.Version, mutation.Work.LeaseID, mutation.Snapshot.GlobalAt, mutation.Snapshot.CellAt,
		mutation.Artifact.Reference, mutation.Artifact.SHA256[:], mutation.Artifact.Bytes, mutation.AvailableAt, mutation.Work.CellID,
		mutation.Work.PlacementGeneration, mutation.Work.AccountVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrLeaseConflict
	}
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, mutation.EventID, status, "available", "workload", "account-export-worker", ""); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	return status, nil
}

func (repository *AccountExportRepository) RecordFailure(ctx context.Context, mutation accountexport.FailureMutation) (accountexport.Status, error) {
	if !validFailureExport(mutation) {
		return accountexport.Status{}, accountexport.ErrInvalid
	}
	state, eventType := accountexport.StateQueued, "build_requeued"
	var nextAttempt any = mutation.NextAttemptAt
	if mutation.Permanent || mutation.Work.AttemptCount >= 3 || !mutation.NextAttemptAt.Before(mutation.Work.ExpiresAt) {
		state, eventType, nextAttempt = accountexport.StateFailed, "build_failed", nil
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accountexport.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status, err := scanAccountExport(tx.QueryRow(ctx, `UPDATE account_export_requests SET state=$5,next_attempt_at=$6,lease_id=NULL,lease_expires_at=NULL,
		error_code=$7,version=version+1 WHERE id=$1 AND account_id=$2 AND state='building' AND version=$3 AND lease_id=$4
		RETURNING `+accountExportStatusColumns, mutation.Work.ID, mutation.Work.AccountID, mutation.Work.Version, mutation.Work.LeaseID, state, nextAttempt, mutation.ErrorCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrLeaseConflict
	}
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, mutation.EventID, status, eventType, "workload", "account-export-worker", mutation.ErrorCode); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	return status, nil
}

func (repository *AccountExportRepository) ClaimDeletion(ctx context.Context, now time.Time, lease time.Duration, leaseID, eventID string) (accountexport.DeletionWork, bool, error) {
	if now.IsZero() || lease <= 0 || lease > 30*time.Minute || ids.Validate(leaseID) != nil || ids.Validate(eventID) != nil {
		return accountexport.DeletionWork{}, false, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accountexport.DeletionWork{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var work accountexport.DeletionWork
	var digest []byte
	err = tx.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM account_export_requests WHERE (state='available' AND expires_at<=$1) OR (state='deleting' AND lease_expires_at<=$1)
		ORDER BY COALESCE(lease_expires_at,expires_at),id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE account_export_requests request SET state='deleting',lease_id=$2,lease_expires_at=$3,version=request.version+1
	FROM candidate WHERE request.id=candidate.id RETURNING request.id::text,request.account_id::text,request.version,
		request.artifact_reference,request.artifact_sha256,request.artifact_bytes`, now, leaseID, now.Add(lease)).
		Scan(&work.ID, &work.AccountID, &work.Version, &work.Artifact.Reference, &digest, &work.Artifact.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.DeletionWork{}, false, nil
	}
	if err != nil {
		return accountexport.DeletionWork{}, false, classifyAccountExport(err)
	}
	copy(work.Artifact.SHA256[:], digest)
	work.LeaseID = leaseID
	status, err := scanAccountExport(tx.QueryRow(ctx, `SELECT `+accountExportStatusColumns+` FROM account_export_requests WHERE id=$1`, work.ID))
	if err != nil {
		return accountexport.DeletionWork{}, false, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, eventID, status, "deletion_claimed", "workload", "account-export-expiry-worker", ""); err != nil {
		return accountexport.DeletionWork{}, false, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.DeletionWork{}, false, classifyAccountExport(err)
	}
	return work, true, nil
}

func (repository *AccountExportRepository) CompleteDeletion(ctx context.Context, work accountexport.DeletionWork, at time.Time, eventID string) (accountexport.Status, error) {
	if ids.Validate(work.ID) != nil || ids.Validate(work.LeaseID) != nil || ids.Validate(eventID) != nil || ids.Validate(string(work.AccountID)) != nil || work.Version == 0 || at.IsZero() || !accountexportArtifactValid(work.Artifact) {
		return accountexport.Status{}, accountexport.ErrInvalid
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return accountexport.Status{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	status, err := scanAccountExport(tx.QueryRow(ctx, `UPDATE account_export_requests SET state='deleted',lease_id=NULL,lease_expires_at=NULL,
		artifact_reference=NULL,deleted_at=$5,version=version+1 WHERE id=$1 AND account_id=$2 AND state='deleting' AND version=$3 AND lease_id=$4
		AND artifact_reference=$6 AND artifact_sha256=$7 AND artifact_bytes=$8 RETURNING `+accountExportStatusColumns,
		work.ID, work.AccountID, work.Version, work.LeaseID, at, work.Artifact.Reference, work.Artifact.SHA256[:], work.Artifact.Bytes))
	if errors.Is(err, pgx.ErrNoRows) {
		return accountexport.Status{}, accountexport.ErrLeaseConflict
	}
	if err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := insertAccountExportEvent(ctx, tx, eventID, status, "deleted", "workload", "account-export-expiry-worker", ""); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return accountexport.Status{}, classifyAccountExport(err)
	}
	return status, nil
}

type accountExportScanner interface{ Scan(...any) error }

func scanAccountExport(scanner accountExportScanner) (accountexport.Status, error) {
	var status accountexport.Status
	err := scanner.Scan(&status.ID, &status.AccountID, &status.RequestedBy, &status.State, &status.CellID, &status.PlacementGeneration,
		&status.AccountVersion, &status.AttemptCount, &status.ErrorCode, &status.ArtifactBytes, &status.Version,
		&status.RequestedAt, &status.ExpiresAt, &status.AvailableAt, &status.DeletedAt)
	return status, err
}

func insertAccountExportEvent(ctx context.Context, tx pgx.Tx, eventID string, status accountexport.Status, eventType, actorKind, actorID, errorCode string) error {
	_, err := tx.Exec(ctx, `INSERT INTO account_export_events(id,account_id,request_id,event_type,state,request_version,actor_kind,actor_id,error_code,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),statement_timestamp())`, eventID, status.AccountID, status.ID, eventType, status.State, status.Version, actorKind, actorID, errorCode)
	return err
}

func validCreateExport(mutation accountexport.CreateMutation) bool {
	return ids.Validate(mutation.ID) == nil && ids.Validate(mutation.EventID) == nil && ids.Validate(string(mutation.AccountID)) == nil &&
		ids.Validate(string(mutation.RequestedBy)) == nil && routecontext.ValidCellID(mutation.CellID) && mutation.PlacementGeneration > 0 &&
		!mutation.RequestedAt.IsZero() && mutation.ExpiresAt.After(mutation.RequestedAt) && mutation.ExpiresAt.Sub(mutation.RequestedAt) >= time.Hour &&
		mutation.ExpiresAt.Sub(mutation.RequestedAt) <= 30*24*time.Hour
}

func validCompleteExport(mutation accountexport.CompleteMutation) bool {
	work, snapshot := mutation.Work, mutation.Snapshot
	return ids.Validate(work.ID) == nil && ids.Validate(work.LeaseID) == nil && ids.Validate(mutation.EventID) == nil &&
		ids.Validate(string(work.AccountID)) == nil && work.Version > 0 && work.AttemptCount > 0 && routecontext.ValidCellID(work.CellID) &&
		work.PlacementGeneration > 0 && work.AccountVersion > 0 && snapshot.CellID == work.CellID &&
		snapshot.PlacementGeneration == work.PlacementGeneration && snapshot.AccountVersion == work.AccountVersion &&
		!mutation.AvailableAt.IsZero() && !mutation.AvailableAt.Before(work.RequestedAt) && mutation.AvailableAt.Before(work.ExpiresAt) &&
		!snapshot.GlobalAt.Before(work.RequestedAt.Add(-time.Minute)) && !snapshot.CellAt.Before(work.RequestedAt.Add(-time.Minute)) &&
		!snapshot.GlobalAt.After(work.RequestedAt.Add(accountexport.MaximumSnapshotDelay)) && !snapshot.CellAt.After(work.RequestedAt.Add(accountexport.MaximumSnapshotDelay)) &&
		accountexportArtifactValid(mutation.Artifact)
}

func validFailureExport(mutation accountexport.FailureMutation) bool {
	work := mutation.Work
	return ids.Validate(work.ID) == nil && ids.Validate(work.LeaseID) == nil && ids.Validate(mutation.EventID) == nil && ids.Validate(string(work.AccountID)) == nil &&
		work.Version > 0 && work.AttemptCount > 0 && accountexportErrorCodeValid(mutation.ErrorCode) && !mutation.At.IsZero() &&
		(mutation.Permanent || mutation.NextAttemptAt.After(mutation.At))
}

func accountexportArtifactValid(value accountexport.Artifact) bool {
	return value.Reference != "" && len(value.Reference) <= 1000 && value.SHA256 != [32]byte{} && value.Bytes > 0 && value.Bytes <= accountexport.MaximumArtifactBytes
}

func accountexportErrorCodeValid(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (index == 0 && (character < 'a' || character > 'z')) || (index > 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_')) {
			return false
		}
	}
	return true
}

func statusFromWork(work accountexport.Work, state accountexport.State) accountexport.Status {
	return accountexport.Status{ID: work.ID, AccountID: work.AccountID, RequestedBy: work.RequestedBy, State: state, CellID: work.CellID,
		PlacementGeneration: work.PlacementGeneration, AccountVersion: work.AccountVersion, AttemptCount: work.AttemptCount,
		Version: work.Version, RequestedAt: work.RequestedAt, ExpiresAt: work.ExpiresAt}
}

func classifyAccountExport(err error) error {
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return accountexport.ErrStateConflict
		case "23503", "23514", "22P02", "22001":
			return fmt.Errorf("%w: %v", accountexport.ErrInvalid, err)
		}
	}
	return err
}

var _ accountexport.RequestStore = (*AccountExportRepository)(nil)
var _ accountexport.BuildStore = (*AccountExportRepository)(nil)
var _ accountexport.ExpiryStore = (*AccountExportRepository)(nil)
