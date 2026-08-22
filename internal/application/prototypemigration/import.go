package prototypemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

const MigrationWorkloadID = "prototype-migration"

var (
	ErrImportDependency = errors.New("prototype migration import dependencies are invalid")
	ErrImportTarget     = errors.New("prototype migration target is invalid")
	ErrReconciliation   = errors.New("prototype migration reconciliation is incomplete")
)

type RunState string

const (
	RunImporting  RunState = "importing"
	RunImported   RunState = "imported"
	RunReconciled RunState = "reconciled"
)

type Run struct {
	ID                    string
	AccountID             ids.AccountID
	ManifestSHA256        [sha256.Size]byte
	SourceTenantID        string
	SourceCheckpoint      string
	SourceInventorySHA256 [sha256.Size]byte
	ExpectedDocuments     uint64
	ExpectedEvidence      uint64
	ExpectedClaims        uint64
	UnresolvedRecords     uint64
	State                 RunState
	ReconciliationSHA256  *[sha256.Size]byte
	Version               uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ImportedAt            *time.Time
	ReconciledAt          *time.Time
}

type Receipt struct {
	AccountID     ids.AccountID
	RunID         string
	SourceKind    string
	SourceID      string
	TargetKind    string
	TargetID      string
	ContentSHA256 [sha256.Size]byte
	CreatedAt     time.Time
}

type ImportLedger interface {
	Begin(context.Context, Run, string) (Run, error)
	Record(context.Context, Receipt, string) error
	MarkImported(context.Context, ids.AccountID, string, uint64, time.Time, string) (Run, error)
	MarkReconciled(context.Context, ids.AccountID, string, uint64, [sha256.Size]byte, time.Time, string) (Run, error)
}

type ImportDestination interface {
	ImportDocument(context.Context, ids.AccountID, DocumentPlan, []byte) error
	ImportFact(context.Context, ids.AccountID, FactPlan) error
	Reconcile(context.Context, Bundle) (Reconciliation, error)
}

type Reconciliation struct {
	Documents       uint64
	DocumentObjects uint64
	Chunks          uint64
	Evidence        uint64
	Claims          uint64
	Citations       uint64
	Unresolved      uint64
	ContentSHA256   [sha256.Size]byte
}

type ImportResult struct {
	Run            Run
	Reconciliation Reconciliation
}

type Importer struct {
	ledger      ImportLedger
	destination ImportDestination
	clock       interface{ Now() time.Time }
}

func NewImporter(ledger ImportLedger, destination ImportDestination, clock interface{ Now() time.Time }) (*Importer, error) {
	if ledger == nil || destination == nil || clock == nil {
		return nil, ErrImportDependency
	}
	return &Importer{ledger: ledger, destination: destination, clock: clock}, nil
}

func (importer *Importer) Import(ctx context.Context, bundle Bundle) (ImportResult, error) {
	if err := Verify(bundle); err != nil {
		return ImportResult{}, err
	}
	run, err := runFromManifest(bundle.Manifest, importer.clock.Now().UTC())
	if err != nil {
		return ImportResult{}, err
	}
	run, err = importer.ledger.Begin(ctx, run, run.ID)
	if err != nil {
		return ImportResult{}, err
	}
	if run.State == RunReconciled {
		reconciliation, reconcileErr := importer.destination.Reconcile(ctx, bundle)
		if reconcileErr == nil && !reconciliation.matches(bundle.Manifest) {
			reconcileErr = ErrReconciliation
		}
		return ImportResult{Run: run, Reconciliation: reconciliation}, reconcileErr
	}

	documents := append([]DocumentPlan(nil), bundle.Manifest.Documents...)
	sort.Slice(documents, func(left, right int) bool {
		return documents[left].SourceRevisionID < documents[right].SourceRevisionID
	})
	for _, document := range documents {
		if document.Action != "import_reconstructed_text" {
			continue
		}
		body, exists := bundle.Objects[document.ObjectPath]
		if !exists {
			return ImportResult{}, ErrManifest
		}
		if err := importer.destination.ImportDocument(ctx, bundle.Manifest.AccountID, document, body); err != nil {
			return ImportResult{Run: run}, err
		}
		contentDigest, err := decodeDigest(document.ContentSHA256)
		if err != nil {
			return ImportResult{}, ErrManifest
		}
		for _, target := range []struct{ kind, id string }{{"document", document.TargetDocumentID}, {"document_revision", document.TargetRevisionID}} {
			receipt := Receipt{AccountID: bundle.Manifest.AccountID, RunID: run.ID, SourceKind: "document_revision", SourceID: document.SourceRevisionID, TargetKind: target.kind, TargetID: target.id, ContentSHA256: contentDigest, CreatedAt: importer.clock.Now().UTC()}
			if err := importer.ledger.Record(ctx, receipt, receiptCorrelation(run.ID, receipt)); err != nil {
				return ImportResult{Run: run}, err
			}
		}
	}

	facts := append([]FactPlan(nil), bundle.Manifest.Facts...)
	sort.Slice(facts, func(left, right int) bool { return facts[left].SourceID < facts[right].SourceID })
	for _, fact := range facts {
		if fact.Action != "import_proposed_claim" && fact.Action != "import_non_authoritative_claim" {
			continue
		}
		if err := importer.destination.ImportFact(ctx, bundle.Manifest.AccountID, fact); err != nil {
			return ImportResult{Run: run}, err
		}
		evidenceDigest, err := decodeDigest(fact.EvidenceSHA256)
		if err != nil {
			return ImportResult{}, ErrManifest
		}
		claimDigest, err := decodeDigest(fact.ContentSHA256)
		if err != nil {
			return ImportResult{}, ErrManifest
		}
		for _, target := range []struct {
			kind   string
			id     string
			digest [sha256.Size]byte
		}{{"evidence", fact.TargetEvidenceID, evidenceDigest}, {"claim", fact.TargetClaimID, claimDigest}} {
			receipt := Receipt{AccountID: bundle.Manifest.AccountID, RunID: run.ID, SourceKind: "fact", SourceID: fact.SourceID, TargetKind: target.kind, TargetID: target.id, ContentSHA256: target.digest, CreatedAt: importer.clock.Now().UTC()}
			if err := importer.ledger.Record(ctx, receipt, receiptCorrelation(run.ID, receipt)); err != nil {
				return ImportResult{Run: run}, err
			}
		}
	}
	if run.State == RunImporting {
		run, err = importer.ledger.MarkImported(ctx, run.AccountID, run.ID, run.Version, importer.clock.Now().UTC(), run.ID)
		if err != nil {
			return ImportResult{Run: run}, err
		}
	}
	reconciliation, err := importer.destination.Reconcile(ctx, bundle)
	if err != nil {
		return ImportResult{Run: run, Reconciliation: reconciliation}, err
	}
	if !reconciliation.matches(bundle.Manifest) {
		return ImportResult{Run: run, Reconciliation: reconciliation}, ErrReconciliation
	}
	if run.State == RunImported {
		run, err = importer.ledger.MarkReconciled(ctx, run.AccountID, run.ID, run.Version, reconciliation.ContentSHA256, importer.clock.Now().UTC(), run.ID)
		if err != nil {
			return ImportResult{Run: run, Reconciliation: reconciliation}, err
		}
	}
	return ImportResult{Run: run, Reconciliation: reconciliation}, nil
}

func runFromManifest(manifest Manifest, now time.Time) (Run, error) {
	manifestDigest, err := decodeDigest(manifest.ContentSHA256)
	if err != nil {
		return Run{}, ErrManifest
	}
	inventoryDigest, err := decodeDigest(manifest.Source.InventorySHA256)
	if err != nil {
		return Run{}, ErrManifest
	}
	runID, err := ids.Derive(string(manifest.AccountID), "prototype-migration:"+manifest.ContentSHA256)
	if err != nil || now.IsZero() {
		return Run{}, ErrManifest
	}
	return Run{ID: runID, AccountID: manifest.AccountID, ManifestSHA256: manifestDigest, SourceTenantID: manifest.Source.TenantID, SourceCheckpoint: manifest.Source.Checkpoint, SourceInventorySHA256: inventoryDigest, ExpectedDocuments: manifest.Totals.ImportableDocuments, ExpectedEvidence: manifest.Totals.ImportableClaims, ExpectedClaims: manifest.Totals.ImportableClaims, UnresolvedRecords: manifest.Totals.UnresolvedRecords, State: RunImporting, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func (value Reconciliation) matches(manifest Manifest) bool {
	return value.Documents == manifest.Totals.ImportableDocuments && value.DocumentObjects == manifest.Totals.ImportableDocuments*2 && value.Chunks == manifest.Totals.ExpectedChunks && value.Evidence == manifest.Totals.ImportableClaims && value.Claims == manifest.Totals.ImportableClaims && value.Citations == manifest.Totals.ImportableClaims && value.Unresolved == manifest.Totals.UnresolvedRecords && value.ContentSHA256 != ([sha256.Size]byte{})
}

func receiptCorrelation(runID string, receipt Receipt) string {
	value, err := ids.Derive(runID, fmt.Sprintf("receipt:%s:%s:%s", receipt.SourceKind, receipt.SourceID, receipt.TargetKind))
	if err != nil {
		return runID
	}
	return value
}

func decodeDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return result, ErrManifest
	}
	copy(result[:], decoded)
	return result, nil
}
