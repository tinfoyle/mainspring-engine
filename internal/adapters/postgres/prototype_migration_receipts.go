package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type PrototypeMigrationLedger struct {
	cell *database.CellPool
	ids  ids.Generator
}

func NewPrototypeMigrationLedger(cell *database.CellPool, generator ids.Generator) (*PrototypeMigrationLedger, error) {
	if cell == nil || generator == nil {
		return nil, errors.New("prototype migration ledger dependencies are required")
	}
	return &PrototypeMigrationLedger{cell: cell, ids: generator}, nil
}

func (ledger *PrototypeMigrationLedger) Begin(ctx context.Context, run prototypemigration.Run, correlationID string) (prototypemigration.Run, error) {
	if !validPrototypeRun(run) || ids.Validate(correlationID) != nil {
		return prototypemigration.Run{}, prototypemigration.ErrImportTarget
	}
	var result prototypemigration.Run
	err := ledger.cell.WithAccountTx(ctx, run.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		existing, found, err := loadPrototypeRun(ctx, tx, run.AccountID, run.ID, true)
		if err != nil {
			return err
		}
		if found {
			if !samePrototypeRunIdentity(existing, run) {
				return prototypemigration.ErrImportTarget
			}
			result = existing
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.prototype_migration_runs
			(account_id,id,manifest_sha256,source_tenant_id,source_checkpoint,source_inventory_sha256,
			 expected_documents,expected_evidence,expected_claims,unresolved_records,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'importing',1,$11,$11)`,
			run.AccountID, run.ID, run.ManifestSHA256[:], run.SourceTenantID, run.SourceCheckpoint, run.SourceInventorySHA256[:],
			run.ExpectedDocuments, run.ExpectedEvidence, run.ExpectedClaims, run.UnresolvedRecords, run.CreatedAt.UTC())
		if err != nil {
			return err
		}
		if err := ledger.insertEvent(ctx, tx, run.AccountID, run.ID, "migration_started", nil, 0, 1, correlationID, run.CreatedAt); err != nil {
			return err
		}
		result = run
		return nil
	})
	if err != nil {
		return prototypemigration.Run{}, classifyPrototypeMigrationError(err)
	}
	return result, nil
}

func (ledger *PrototypeMigrationLedger) Record(ctx context.Context, receipt prototypemigration.Receipt, correlationID string) error {
	if !validPrototypeReceipt(receipt) || ids.Validate(correlationID) != nil {
		return prototypemigration.ErrImportTarget
	}
	err := ledger.cell.WithAccountTx(ctx, receipt.AccountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		run, found, err := loadPrototypeRun(ctx, tx, receipt.AccountID, receipt.RunID, true)
		if err != nil {
			return err
		}
		if !found {
			return prototypemigration.ErrImportTarget
		}
		var targetID string
		var digest []byte
		err = tx.QueryRow(ctx, `SELECT target_id::text,content_sha256 FROM spyglass.prototype_migration_receipts
			WHERE account_id=$1 AND run_id=$2 AND source_kind=$3 AND source_id=$4 AND target_kind=$5`, receipt.AccountID, receipt.RunID, receipt.SourceKind, receipt.SourceID, receipt.TargetKind).Scan(&targetID, &digest)
		if err == nil {
			if targetID != receipt.TargetID || !bytes.Equal(digest, receipt.ContentSHA256[:]) {
				return prototypemigration.ErrImportTarget
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if run.State != prototypemigration.RunImporting {
			return prototypemigration.ErrReconciliation
		}
		_, err = tx.Exec(ctx, `INSERT INTO spyglass.prototype_migration_receipts
			(account_id,run_id,source_kind,source_id,target_kind,target_id,content_sha256,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, receipt.AccountID, receipt.RunID, receipt.SourceKind, receipt.SourceID, receipt.TargetKind, receipt.TargetID, receipt.ContentSHA256[:], receipt.CreatedAt.UTC())
		if err != nil {
			return err
		}
		return ledger.insertEvent(ctx, tx, receipt.AccountID, receipt.RunID, "target_imported", &receipt, 0, 1, correlationID, receipt.CreatedAt)
	})
	return classifyPrototypeMigrationError(err)
}

func (ledger *PrototypeMigrationLedger) MarkImported(ctx context.Context, accountID ids.AccountID, runID string, expectedVersion uint64, at time.Time, correlationID string) (prototypemigration.Run, error) {
	return ledger.transition(ctx, accountID, runID, expectedVersion, at, correlationID, prototypemigration.RunImported, nil)
}

func (ledger *PrototypeMigrationLedger) MarkReconciled(ctx context.Context, accountID ids.AccountID, runID string, expectedVersion uint64, digest [32]byte, at time.Time, correlationID string) (prototypemigration.Run, error) {
	if digest == ([32]byte{}) {
		return prototypemigration.Run{}, prototypemigration.ErrImportTarget
	}
	return ledger.transition(ctx, accountID, runID, expectedVersion, at, correlationID, prototypemigration.RunReconciled, &digest)
}

func (ledger *PrototypeMigrationLedger) transition(ctx context.Context, accountID ids.AccountID, runID string, expectedVersion uint64, at time.Time, correlationID string, target prototypemigration.RunState, digest *[32]byte) (prototypemigration.Run, error) {
	if ids.Validate(string(accountID)) != nil || ids.Validate(runID) != nil || expectedVersion == 0 || at.IsZero() || ids.Validate(correlationID) != nil {
		return prototypemigration.Run{}, prototypemigration.ErrImportTarget
	}
	var result prototypemigration.Run
	err := ledger.cell.WithAccountTx(ctx, accountID, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		current, found, err := loadPrototypeRun(ctx, tx, accountID, runID, true)
		if err != nil {
			return err
		}
		if !found {
			return prototypemigration.ErrImportTarget
		}
		if current.Version != expectedVersion {
			return prototypemigration.ErrImportTarget
		}
		switch target {
		case prototypemigration.RunImported:
			if current.State != prototypemigration.RunImporting || digest != nil {
				return prototypemigration.ErrImportTarget
			}
			var documents, revisions, evidence, claims uint64
			if err := tx.QueryRow(ctx, `SELECT
				count(*) FILTER (WHERE target_kind='document'),count(*) FILTER (WHERE target_kind='document_revision'),
				count(*) FILTER (WHERE target_kind='evidence'),count(*) FILTER (WHERE target_kind='claim')
				FROM spyglass.prototype_migration_receipts WHERE account_id=$1 AND run_id=$2`, accountID, runID).Scan(&documents, &revisions, &evidence, &claims); err != nil {
				return err
			}
			if documents != current.ExpectedDocuments || revisions != current.ExpectedDocuments || evidence != current.ExpectedEvidence || claims != current.ExpectedClaims {
				return prototypemigration.ErrReconciliation
			}
			_, err = tx.Exec(ctx, `UPDATE spyglass.prototype_migration_runs SET state='imported',version=version+1,updated_at=$3,imported_at=$3
				WHERE account_id=$1 AND id=$2 AND version=$4`, accountID, runID, at.UTC(), expectedVersion)
		case prototypemigration.RunReconciled:
			if current.State != prototypemigration.RunImported || digest == nil {
				return prototypemigration.ErrImportTarget
			}
			_, err = tx.Exec(ctx, `UPDATE spyglass.prototype_migration_runs SET state='reconciled',version=version+1,updated_at=$3,reconciled_at=$3,reconciliation_sha256=$4
				WHERE account_id=$1 AND id=$2 AND version=$5`, accountID, runID, at.UTC(), digest[:], expectedVersion)
		default:
			return prototypemigration.ErrImportTarget
		}
		if err != nil {
			return err
		}
		eventType := "migration_imported"
		if target == prototypemigration.RunReconciled {
			eventType = "migration_reconciled"
		}
		if err := ledger.insertEvent(ctx, tx, accountID, runID, eventType, nil, expectedVersion, expectedVersion+1, correlationID, at); err != nil {
			return err
		}
		result, _, err = loadPrototypeRun(ctx, tx, accountID, runID, false)
		return err
	})
	if err != nil {
		return prototypemigration.Run{}, classifyPrototypeMigrationError(err)
	}
	return result, nil
}

func (ledger *PrototypeMigrationLedger) insertEvent(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, runID, eventType string, receipt *prototypemigration.Receipt, fromVersion, toVersion uint64, correlationID string, at time.Time) error {
	var sourceKind, sourceID, targetKind, targetID any
	if receipt != nil {
		sourceKind, sourceID, targetKind, targetID = receipt.SourceKind, receipt.SourceID, receipt.TargetKind, receipt.TargetID
	}
	_, err := tx.Exec(ctx, `INSERT INTO spyglass.prototype_migration_events
		(account_id,id,run_id,event_type,source_kind,source_id,target_kind,target_id,from_version,to_version,actor_id,correlation_id,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, accountID, ledger.ids.New(), runID, eventType, sourceKind, sourceID, targetKind, targetID, fromVersion, toVersion, prototypemigration.MigrationWorkloadID, correlationID, at.UTC())
	return err
}

func loadPrototypeRun(ctx context.Context, tx pgx.Tx, accountID ids.AccountID, runID string, lock bool) (prototypemigration.Run, bool, error) {
	query := `SELECT account_id,id::text,manifest_sha256,source_tenant_id::text,source_checkpoint,source_inventory_sha256,
		expected_documents,expected_evidence,expected_claims,unresolved_records,state,reconciliation_sha256,version,
		created_at,updated_at,imported_at,reconciled_at FROM spyglass.prototype_migration_runs WHERE account_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	var result prototypemigration.Run
	var manifest, inventory, reconciliation []byte
	err := tx.QueryRow(ctx, query, accountID, runID).Scan(&result.AccountID, &result.ID, &manifest, &result.SourceTenantID, &result.SourceCheckpoint, &inventory, &result.ExpectedDocuments, &result.ExpectedEvidence, &result.ExpectedClaims, &result.UnresolvedRecords, &result.State, &reconciliation, &result.Version, &result.CreatedAt, &result.UpdatedAt, &result.ImportedAt, &result.ReconciledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return prototypemigration.Run{}, false, nil
	}
	if err != nil || len(manifest) != 32 || len(inventory) != 32 || len(reconciliation) != 0 && len(reconciliation) != 32 {
		return prototypemigration.Run{}, false, errors.Join(err, prototypemigration.ErrImportTarget)
	}
	copy(result.ManifestSHA256[:], manifest)
	copy(result.SourceInventorySHA256[:], inventory)
	if len(reconciliation) == 32 {
		var digest [32]byte
		copy(digest[:], reconciliation)
		result.ReconciliationSHA256 = &digest
	}
	if !validPrototypeRun(result) {
		return prototypemigration.Run{}, false, prototypemigration.ErrImportTarget
	}
	return result, true, nil
}

func validPrototypeRun(run prototypemigration.Run) bool {
	if ids.Validate(run.ID) != nil || ids.Validate(string(run.AccountID)) != nil || ids.Validate(run.SourceTenantID) != nil || run.ManifestSHA256 == ([32]byte{}) || run.SourceInventorySHA256 == ([32]byte{}) || run.Version == 0 || run.CreatedAt.IsZero() || run.UpdatedAt.Before(run.CreatedAt) {
		return false
	}
	switch run.State {
	case prototypemigration.RunImporting:
		return run.ImportedAt == nil && run.ReconciledAt == nil && run.ReconciliationSHA256 == nil
	case prototypemigration.RunImported:
		return run.ImportedAt != nil && run.ReconciledAt == nil && run.ReconciliationSHA256 == nil
	case prototypemigration.RunReconciled:
		return run.ImportedAt != nil && run.ReconciledAt != nil && run.ReconciliationSHA256 != nil
	default:
		return false
	}
}

func validPrototypeReceipt(receipt prototypemigration.Receipt) bool {
	if ids.Validate(string(receipt.AccountID)) != nil || ids.Validate(receipt.RunID) != nil || ids.Validate(receipt.TargetID) != nil || receipt.ContentSHA256 == ([32]byte{}) || receipt.CreatedAt.IsZero() || len(receipt.SourceID) == 0 || len(receipt.SourceID) > 256 {
		return false
	}
	return (receipt.SourceKind == "document_revision" && (receipt.TargetKind == "document" || receipt.TargetKind == "document_revision")) || (receipt.SourceKind == "fact" && (receipt.TargetKind == "evidence" || receipt.TargetKind == "claim"))
}

func samePrototypeRunIdentity(left, right prototypemigration.Run) bool {
	return left.ID == right.ID && left.AccountID == right.AccountID && left.ManifestSHA256 == right.ManifestSHA256 && left.SourceTenantID == right.SourceTenantID && left.SourceCheckpoint == right.SourceCheckpoint && left.SourceInventorySHA256 == right.SourceInventorySHA256 && left.ExpectedDocuments == right.ExpectedDocuments && left.ExpectedEvidence == right.ExpectedEvidence && left.ExpectedClaims == right.ExpectedClaims && left.UnresolvedRecords == right.UnresolvedRecords
}

func classifyPrototypeMigrationError(err error) error {
	if err == nil || errors.Is(err, prototypemigration.ErrImportTarget) || errors.Is(err, prototypemigration.ErrReconciliation) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505", "23503", "23514", "22P02", "P0001":
			return prototypemigration.ErrImportTarget
		}
	}
	return fmt.Errorf("prototype migration ledger: %w", err)
}

var _ prototypemigration.ImportLedger = (*PrototypeMigrationLedger)(nil)
