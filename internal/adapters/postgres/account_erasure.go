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

	"github.com/tinfoyle/spyglass-engine/internal/application/accounterasure"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const erasureRequestColumns = `
	id::text,closure_request_id::text,account_id::text,state,cell_id,placement_generation,account_version,policy_version,version,
	export_disposition,COALESCE(export_reference,''),export_sha256,export_expires_at,COALESCE(export_reason,''),backup_expires_at,
	cell_namespace_state,cell_attested_at,environment,requested_by,request_reason,requested_at,
	COALESCE(approved_by,''),COALESCE(approve_reason,''),approved_at,COALESCE(canceled_by,''),COALESCE(cancel_reason,''),canceled_at,
	account_fingerprint,operator_evidence_sha256,COALESCE(cell_request_version,0),COALESCE(execution_lease_id::text,''),lease_expires_at,
	cell_erased_at,COALESCE(cell_row_counts,'{}'::jsonb),cell_tombstone_sha256`

type AccountErasureRepository struct{ pool *pgxpool.Pool }

func NewAccountErasureRepository(pool *pgxpool.Pool) *AccountErasureRepository {
	return &AccountErasureRepository{pool: pool}
}

func (r *AccountErasureRepository) Target(ctx context.Context, accountID ids.AccountID) (accounterasure.Target, error) {
	var target accounterasure.Target
	target.AccountID = accountID
	err := r.pool.QueryRow(ctx, `
		SELECT cell_id,placement_generation,account_version
		FROM public.spyglass_assert_account_erasure_eligible($1)`, accountID).
		Scan(&target.CellID, &target.PlacementGeneration, &target.AccountVersion)
	if err != nil {
		return accounterasure.Target{}, classifyErasureEligibilityError(err)
	}
	return target, nil
}

func (r *AccountErasureRepository) Prepare(ctx context.Context, requestID string, command accounterasure.PrepareCommand, attestation accounterasure.CellAttestation, change accounterasure.Change) (accounterasure.Request, error) {
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_prepare_account_erasure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query,
		requestID, change.EventID, command.AccountID, command.PolicyVersion, command.Export.Disposition,
		nullableText(command.Export.Reference), nullableBytes(command.Export.SHA256), command.Export.ExpiresAt, nullableText(command.Export.Reason), command.BackupExpiresAt.UTC(),
		attestation.PlacementGeneration, attestation.NamespaceState, attestation.UnfinishedReleaseJobs, attestation.ObservedAt.UTC(),
		change.Actor, change.Reason, change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasurePreparationError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) Inspect(ctx context.Context, requestID string, change accounterasure.Change) (accounterasure.Request, error) {
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_inspect_account_erasure($1,$2,$3,$4,$5)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query, requestID, change.EventID, change.Actor, change.Reason, change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasureRequestError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) Approve(ctx context.Context, requestID string, expectedVersion uint64, attestation accounterasure.CellAttestation, change accounterasure.Change) (accounterasure.Request, error) {
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_approve_account_erasure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query, requestID, change.EventID, expectedVersion,
		attestation.PlacementGeneration, attestation.NamespaceState, attestation.UnfinishedReleaseJobs, attestation.ObservedAt.UTC(),
		change.Actor, change.Reason, change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasureRequestError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) Cancel(ctx context.Context, requestID string, expectedVersion uint64, change accounterasure.Change) (accounterasure.Request, error) {
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_cancel_account_erasure($1,$2,$3,$4,$5,$6)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query, requestID, change.EventID, expectedVersion, change.Actor, change.Reason, change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasureRequestError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) ClaimExecution(ctx context.Context, command accounterasure.ExecutionClaim) (accounterasure.Request, error) {
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_claim_account_erasure_execution($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query,
		command.RequestID, command.EventID, command.AccountID, command.ExpectedVersion, command.LeaseID,
		int64(command.LeaseDuration/time.Second), command.AccountFingerprint, command.OperatorEvidenceSHA256,
		command.Change.Actor, command.Change.Reason, command.Change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasureExecutionError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) RecordCellErasure(ctx context.Context, command accounterasure.CellRecord) (accounterasure.Request, error) {
	rowCounts, err := json.Marshal(command.CellRowCounts)
	if err != nil {
		return accounterasure.Request{}, err
	}
	query := `SELECT ` + erasureRequestColumns + ` FROM public.spyglass_record_account_cell_erasure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	result, err := scanErasureRequest(r.pool.QueryRow(ctx, query,
		command.RequestID, command.EventID, command.ExpectedVersion, command.LeaseID, command.AccountFingerprint,
		command.CellErasedAt.UTC(), rowCounts, command.CellTombstoneSHA256,
		command.Change.Actor, command.Change.Reason, command.Change.Environment))
	if err != nil {
		return accounterasure.Request{}, classifyErasureExecutionError(err)
	}
	return result, nil
}

const globalTombstoneColumns = `
	request_id::text,account_fingerprint,policy_version,final_request_version,environment,prepared_at,approved_at,
	cell_erased_at,completed_at,cell_row_counts,global_row_counts,export_sha256,cell_tombstone_sha256,
	operator_evidence_sha256,backup_expires_at`

func (r *AccountErasureRepository) FinalizeGlobalErasure(ctx context.Context, command accounterasure.GlobalFinalize) (accounterasure.GlobalTombstone, error) {
	query := `SELECT ` + globalTombstoneColumns + ` FROM public.spyglass_finalize_account_erasure($1,$2,$3,$4,$5,$6,$7)`
	result, err := scanGlobalTombstone(r.pool.QueryRow(ctx, query,
		command.RequestID, command.AccountID, command.ExpectedVersion, command.LeaseID,
		command.AccountFingerprint, command.CellTombstoneSHA256, command.Environment))
	if err != nil {
		return accounterasure.GlobalTombstone{}, classifyErasureExecutionError(err)
	}
	return result, nil
}

func (r *AccountErasureRepository) AttestGlobalErasure(ctx context.Context, requestID string, fingerprint []byte) (accounterasure.GlobalTombstone, error) {
	query := `SELECT ` + globalTombstoneColumns + ` FROM public.spyglass_attest_global_account_erasure($1,$2)`
	result, err := scanGlobalTombstone(r.pool.QueryRow(ctx, query, requestID, fingerprint))
	if err != nil {
		return accounterasure.GlobalTombstone{}, classifyErasureExecutionError(err)
	}
	return result, nil
}

type AccountErasureCellRepository struct {
	pool   *pgxpool.Pool
	cellID ids.CellID
}

func NewAccountErasureCellRepository(pool *pgxpool.Pool, cellID ids.CellID) *AccountErasureCellRepository {
	return &AccountErasureCellRepository{pool: pool, cellID: cellID}
}

func (r *AccountErasureCellRepository) Attest(ctx context.Context, target accounterasure.Target) (accounterasure.CellAttestation, error) {
	if target.CellID != r.cellID {
		return accounterasure.CellAttestation{}, accounterasure.ErrCellMismatch
	}
	result := accounterasure.CellAttestation{Target: target}
	err := r.pool.QueryRow(ctx, `
		SELECT placement_generation,namespace_state,unfinished_release_count,observed_at
		FROM public.spyglass_attest_account_erasure_readiness($1,$2)`, target.AccountID, target.PlacementGeneration).
		Scan(&result.PlacementGeneration, &result.NamespaceState, &result.UnfinishedReleaseJobs, &result.ObservedAt)
	if err != nil {
		return accounterasure.CellAttestation{}, classifyErasureEligibilityError(err)
	}
	return result, nil
}

const cellTombstoneColumns = `
	request_id::text,account_fingerprint,placement_generation,policy_version,request_version,environment,erased_at,
	row_counts,export_sha256,operator_evidence_sha256,backup_expires_at`

func (r *AccountErasureCellRepository) Erase(ctx context.Context, command accounterasure.CellEraseCommand) (accounterasure.CellTombstone, error) {
	if command.CellID != r.cellID {
		return accounterasure.CellTombstone{}, accounterasure.ErrCellMismatch
	}
	query := `SELECT ` + cellTombstoneColumns + ` FROM public.spyglass_erase_account_cell($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	result, err := scanCellTombstone(r.pool.QueryRow(ctx, query,
		command.RequestID, command.AccountID, command.PlacementGeneration, command.AccountFingerprint,
		command.PolicyVersion, command.RequestVersion, command.Environment, nullableBytes(command.ExportSHA256),
		command.OperatorEvidenceSHA256, command.BackupExpiresAt.UTC()))
	if err != nil {
		return accounterasure.CellTombstone{}, classifyCellErasureError(err)
	}
	return result, nil
}

func (r *AccountErasureCellRepository) AttestErasure(ctx context.Context, cellID ids.CellID, requestID string, fingerprint []byte) (accounterasure.CellTombstone, error) {
	if cellID != r.cellID {
		return accounterasure.CellTombstone{}, accounterasure.ErrCellMismatch
	}
	query := `SELECT ` + cellTombstoneColumns + ` FROM public.spyglass_attest_account_cell_erasure($1,$2)`
	result, err := scanCellTombstone(r.pool.QueryRow(ctx, query, requestID, fingerprint))
	if err != nil {
		return accounterasure.CellTombstone{}, classifyCellErasureError(err)
	}
	return result, nil
}

type erasureRow interface{ Scan(...any) error }

func scanErasureRequest(row erasureRow) (accounterasure.Request, error) {
	var result accounterasure.Request
	var rawCellCounts []byte
	err := row.Scan(
		&result.ID, &result.ClosureRequestID, &result.AccountID, &result.State, &result.CellID,
		&result.PlacementGeneration, &result.AccountVersion, &result.PolicyVersion, &result.Version,
		&result.ExportDisposition, &result.ExportReference, &result.ExportSHA256, &result.ExportExpiresAt, &result.ExportReason, &result.BackupExpiresAt,
		&result.CellNamespaceState, &result.CellAttestedAt, &result.Environment, &result.RequestedBy, &result.RequestReason, &result.RequestedAt,
		&result.ApprovedBy, &result.ApproveReason, &result.ApprovedAt, &result.CanceledBy, &result.CancelReason, &result.CanceledAt,
		&result.AccountFingerprint, &result.OperatorEvidenceSHA256, &result.CellRequestVersion, &result.ExecutionLeaseID, &result.LeaseExpiresAt,
		&result.CellErasedAt, &rawCellCounts, &result.CellTombstoneSHA256)
	if err != nil {
		return accounterasure.Request{}, fmt.Errorf("scan Account erasure request: %w", err)
	}
	if err := json.Unmarshal(rawCellCounts, &result.CellRowCounts); err != nil {
		return accounterasure.Request{}, fmt.Errorf("decode cell Account erasure row counts: %w", err)
	}
	return result, nil
}

func scanCellTombstone(row erasureRow) (accounterasure.CellTombstone, error) {
	var result accounterasure.CellTombstone
	var rawCounts []byte
	err := row.Scan(&result.RequestID, &result.AccountFingerprint, &result.PlacementGeneration, &result.PolicyVersion,
		&result.RequestVersion, &result.Environment, &result.ErasedAt, &rawCounts, &result.ExportSHA256,
		&result.OperatorEvidenceSHA256, &result.BackupExpiresAt)
	if err != nil {
		return accounterasure.CellTombstone{}, fmt.Errorf("scan cell Account erasure tombstone: %w", err)
	}
	if err := json.Unmarshal(rawCounts, &result.RowCounts); err != nil {
		return accounterasure.CellTombstone{}, fmt.Errorf("decode cell Account erasure row counts: %w", err)
	}
	return result, nil
}

func scanGlobalTombstone(row erasureRow) (accounterasure.GlobalTombstone, error) {
	var result accounterasure.GlobalTombstone
	var rawCellCounts, rawGlobalCounts []byte
	err := row.Scan(&result.RequestID, &result.AccountFingerprint, &result.PolicyVersion, &result.FinalRequestVersion,
		&result.Environment, &result.PreparedAt, &result.ApprovedAt, &result.CellErasedAt, &result.CompletedAt,
		&rawCellCounts, &rawGlobalCounts, &result.ExportSHA256, &result.CellTombstoneSHA256,
		&result.OperatorEvidenceSHA256, &result.BackupExpiresAt)
	if err != nil {
		return accounterasure.GlobalTombstone{}, fmt.Errorf("scan global Account erasure tombstone: %w", err)
	}
	if err := json.Unmarshal(rawCellCounts, &result.CellRowCounts); err != nil {
		return accounterasure.GlobalTombstone{}, fmt.Errorf("decode global Account erasure cell counts: %w", err)
	}
	if err := json.Unmarshal(rawGlobalCounts, &result.GlobalRowCounts); err != nil {
		return accounterasure.GlobalTombstone{}, fmt.Errorf("decode global Account erasure row counts: %w", err)
	}
	return result, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func classifyErasurePreparationError(err error) error {
	if postgresCode(err) == "23505" {
		return accounterasure.ErrStateConflict
	}
	return classifyErasureEligibilityError(err)
}

func classifyErasureEligibilityError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return accounterasure.ErrNotFound
	}
	switch postgresCode(err) {
	case "22023":
		return accounterasure.ErrInvalidChange
	case "P0002":
		return accounterasure.ErrNotFound
	case "P0001":
		return accounterasure.ErrNotEligible
	}
	return fmt.Errorf("Account erasure eligibility unavailable: %w", err)
}

func classifyErasureRequestError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return accounterasure.ErrNotFound
	}
	switch postgresCode(err) {
	case "22023":
		return accounterasure.ErrInvalidChange
	case "P0002":
		return accounterasure.ErrNotFound
	case "P0001", "23505":
		return accounterasure.ErrStateConflict
	}
	return fmt.Errorf("Account erasure request unavailable: %w", err)
}

func classifyCellErasureError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return accounterasure.ErrNotFound
	}
	switch postgresCode(err) {
	case "22023":
		return accounterasure.ErrInvalidChange
	case "P0002":
		return accounterasure.ErrNotFound
	case "P0001":
		return accounterasure.ErrNotEligible
	case "P0003", "23505":
		return accounterasure.ErrStateConflict
	}
	return fmt.Errorf("cell Account erasure unavailable: %w", err)
}

func classifyErasureExecutionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return accounterasure.ErrNotFound
	}
	switch postgresCode(err) {
	case "22023":
		return accounterasure.ErrInvalidChange
	case "P0002":
		return accounterasure.ErrNotFound
	case "P0001", "P0003", "23505":
		return accounterasure.ErrStateConflict
	}
	return fmt.Errorf("Account erasure execution unavailable: %w", err)
}

func postgresCode(err error) string {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		return databaseError.Code
	}
	return ""
}

var _ accounterasure.Store = (*AccountErasureRepository)(nil)
var _ accounterasure.CellStore = (*AccountErasureCellRepository)(nil)
var _ accounterasure.CellExecutor = (*AccountErasureCellRepository)(nil)
var _ accounterasure.ExecutionStore = (*AccountErasureRepository)(nil)
