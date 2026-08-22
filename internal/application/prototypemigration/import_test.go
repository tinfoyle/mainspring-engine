package prototypemigration

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
)

type importClock struct{ at time.Time }

func (clock *importClock) Now() time.Time {
	value := clock.at
	clock.at = clock.at.Add(time.Second)
	return value
}

type memoryImportLedger struct {
	run      Run
	receipts map[string]Receipt
}

func (ledger *memoryImportLedger) Begin(_ context.Context, requested Run, _ string) (Run, error) {
	if ledger.receipts == nil {
		ledger.run, ledger.receipts = requested, map[string]Receipt{}
	}
	return ledger.run, nil
}

func (ledger *memoryImportLedger) Record(_ context.Context, receipt Receipt, _ string) error {
	key := receipt.SourceKind + ":" + receipt.SourceID + ":" + receipt.TargetKind
	if current, exists := ledger.receipts[key]; exists {
		if current.AccountID != receipt.AccountID || current.RunID != receipt.RunID || current.SourceKind != receipt.SourceKind || current.SourceID != receipt.SourceID || current.TargetKind != receipt.TargetKind || current.TargetID != receipt.TargetID || current.ContentSHA256 != receipt.ContentSHA256 {
			return ErrImportTarget
		}
		return nil
	}
	ledger.receipts[key] = receipt
	return nil
}

func (ledger *memoryImportLedger) MarkImported(_ context.Context, _ ids.AccountID, _ string, expected uint64, at time.Time, _ string) (Run, error) {
	if ledger.run.State != RunImporting || ledger.run.Version != expected {
		return Run{}, ErrImportTarget
	}
	ledger.run.State, ledger.run.Version, ledger.run.UpdatedAt, ledger.run.ImportedAt = RunImported, expected+1, at, &at
	return ledger.run, nil
}

func (ledger *memoryImportLedger) MarkReconciled(_ context.Context, _ ids.AccountID, _ string, expected uint64, digest [32]byte, at time.Time, _ string) (Run, error) {
	if ledger.run.State != RunImported || ledger.run.Version != expected {
		return Run{}, ErrImportTarget
	}
	ledger.run.State, ledger.run.Version, ledger.run.UpdatedAt, ledger.run.ReconciledAt, ledger.run.ReconciliationSHA256 = RunReconciled, expected+1, at, &at, &digest
	return ledger.run, nil
}

type memoryImportDestination struct {
	documents map[string][]byte
	facts     map[string]FactPlan
	failFact  bool
	result    Reconciliation
}

func (destination *memoryImportDestination) ImportDocument(_ context.Context, _ ids.AccountID, plan DocumentPlan, body []byte) error {
	if destination.documents == nil {
		destination.documents = map[string][]byte{}
	}
	if current, exists := destination.documents[plan.TargetRevisionID]; exists && string(current) != string(body) {
		return ErrImportTarget
	}
	destination.documents[plan.TargetRevisionID] = append([]byte(nil), body...)
	return nil
}

func (destination *memoryImportDestination) ImportFact(_ context.Context, _ ids.AccountID, plan FactPlan) error {
	if destination.failFact {
		return errors.New("interrupted")
	}
	if destination.facts == nil {
		destination.facts = map[string]FactPlan{}
	}
	destination.facts[plan.SourceID] = plan
	return nil
}

func (destination *memoryImportDestination) Reconcile(context.Context, Bundle) (Reconciliation, error) {
	return destination.result, nil
}

func TestImporterConvergesAfterInterruptedRetryAndRecordsDistinctEvidenceDigest(t *testing.T) {
	bundle := importTestBundle(t)
	reconciliationDigest := sha256.Sum256([]byte("reconciliation"))
	destination := &memoryImportDestination{failFact: true, result: Reconciliation{Documents: 1, DocumentObjects: 2, Chunks: bundle.Manifest.Totals.ExpectedChunks, Evidence: 2, Claims: 2, Citations: 2, Unresolved: bundle.Manifest.Totals.UnresolvedRecords, ContentSHA256: reconciliationDigest}}
	ledger := &memoryImportLedger{}
	clock := &importClock{at: time.Date(2026, 8, 22, 16, 0, 0, 0, time.UTC)}
	importer, err := NewImporter(ledger, destination, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importer.Import(context.Background(), bundle); err == nil || ledger.run.State != RunImporting || len(ledger.receipts) != 2 {
		t.Fatalf("first import state=%s receipts=%d err=%v", ledger.run.State, len(ledger.receipts), err)
	}
	destination.failFact = false
	result, err := importer.Import(context.Background(), bundle)
	if err != nil || result.Run.State != RunReconciled || len(ledger.receipts) != 6 || len(destination.documents) != 1 || len(destination.facts) != 2 {
		t.Fatalf("result=%+v receipts=%d documents=%d facts=%d err=%v", result, len(ledger.receipts), len(destination.documents), len(destination.facts), err)
	}
	var documentFact FactPlan
	for _, fact := range bundle.Manifest.Facts {
		if fact.EvidenceKind == "document_revision" {
			documentFact = fact
			break
		}
	}
	receipt := ledger.receipts["fact:"+documentFact.SourceID+":evidence"]
	want, _ := decodeDigest(documentFact.EvidenceSHA256)
	if receipt.ContentSHA256 != want {
		t.Fatalf("evidence receipt digest=%x want=%x", receipt.ContentSHA256, want)
	}
	if _, err := importer.Import(context.Background(), bundle); err != nil {
		t.Fatalf("reconciled replay: %v", err)
	}
}

func TestImporterRefusesToCertifyCountDrift(t *testing.T) {
	bundle := importTestBundle(t)
	destination := &memoryImportDestination{result: Reconciliation{Documents: 1, DocumentObjects: 2, Chunks: bundle.Manifest.Totals.ExpectedChunks, Evidence: 2, Claims: 2, Citations: 1, Unresolved: bundle.Manifest.Totals.UnresolvedRecords, ContentSHA256: sha256.Sum256([]byte("drift"))}}
	importer, _ := NewImporter(&memoryImportLedger{}, destination, &importClock{at: time.Date(2026, 8, 22, 17, 0, 0, 0, time.UTC)})
	if _, err := importer.Import(context.Background(), bundle); !errors.Is(err, ErrReconciliation) {
		t.Fatalf("error=%v", err)
	}
}

func importTestBundle(t *testing.T) Bundle {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	content := []byte("Formation record\nVerified in retained prototype text.\n")
	digest := sha256.Sum256(content)
	snapshot := Snapshot{
		TenantID: testTenant, Checkpoint: "00000016/B374D848",
		Inventory: Inventory{FactVersions: 2, DocumentRevisions: 1},
		Documents: []LegacyDocumentRevision{{DocumentID: testDoc, RevisionID: testDocRev, Revision: 1, Name: "Formation.txt", MediaType: "text/plain", Content: content, StoredSHA256: digest[:], Status: "ready", CreatedAt: now.Add(-time.Hour)}},
		FactVersions: []LegacyFactVersion{
			{ID: "52000000-0000-4000-8000-000000000005", Key: "legal.formation", Value: "verified", Scope: "account", SourceType: "document", SourceRef: testDoc, Confidence: "0.900", Sensitivity: "confidential", Status: "active", RecordedAt: now},
			{ID: "53000000-0000-4000-8000-000000000005", Key: "operations.summary", Value: "draft", Scope: "account", SourceType: "agent", Confidence: "0.500", Sensitivity: "internal", Status: "active", RecordedAt: now.Add(time.Minute)},
		},
	}
	bundle, err := NewTransformer().Transform(ids.AccountID(testAccount), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}
