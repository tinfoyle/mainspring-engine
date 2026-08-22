package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/application/prototypemigration"
	"github.com/tinfoyle/spyglass-engine/internal/modules/access"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestPostgresPrototypeMigrationLedgerIsReplaySafeAndImmutable(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("91000000-0000-4000-8000-000000000001")
	otherAccountID := ids.AccountID("92000000-0000-4000-8000-000000000002")
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',statement_timestamp()),($2,1,'active',statement_timestamp())`, accountID, otherAccountID); err != nil {
		t.Fatal(err)
	}
	cell, _ := database.NewCellPool(pool)
	ledger, err := postgresadapter.NewPrototypeMigrationLedger(cell, ids.RandomGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	run := prototypemigration.Run{
		ID: "93000000-0000-4000-8000-000000000003", AccountID: accountID,
		ManifestSHA256: sha256.Sum256([]byte("manifest")), SourceTenantID: "94000000-0000-4000-8000-000000000004",
		SourceCheckpoint: "00000016/B374D848", SourceInventorySHA256: sha256.Sum256([]byte("inventory")),
		ExpectedDocuments: 1, ExpectedEvidence: 1, ExpectedClaims: 1, UnresolvedRecords: 2,
		State: prototypemigration.RunImporting, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	started, err := ledger.Begin(ctx, run, "95000000-0000-4000-8000-000000000005")
	if err != nil || started.State != prototypemigration.RunImporting {
		t.Fatalf("begin=%+v err=%v", started, err)
	}
	if replay, err := ledger.Begin(ctx, run, "95000000-0000-4000-8000-000000000005"); err != nil || replay.ID != run.ID {
		t.Fatalf("begin replay=%+v err=%v", replay, err)
	}
	documentDigest := sha256.Sum256([]byte("document"))
	evidenceDigest := sha256.Sum256([]byte("evidence"))
	claimDigest := sha256.Sum256([]byte("claim"))
	receipts := []prototypemigration.Receipt{
		{AccountID: accountID, RunID: run.ID, SourceKind: "document_revision", SourceID: "legacy-revision", TargetKind: "document", TargetID: "96000000-0000-4000-8000-000000000006", ContentSHA256: documentDigest, CreatedAt: now.Add(time.Second)},
		{AccountID: accountID, RunID: run.ID, SourceKind: "document_revision", SourceID: "legacy-revision", TargetKind: "document_revision", TargetID: "97000000-0000-4000-8000-000000000007", ContentSHA256: documentDigest, CreatedAt: now.Add(2 * time.Second)},
		{AccountID: accountID, RunID: run.ID, SourceKind: "fact", SourceID: "legacy-fact", TargetKind: "evidence", TargetID: "98000000-0000-4000-8000-000000000008", ContentSHA256: evidenceDigest, CreatedAt: now.Add(3 * time.Second)},
		{AccountID: accountID, RunID: run.ID, SourceKind: "fact", SourceID: "legacy-fact", TargetKind: "claim", TargetID: "99000000-0000-4000-8000-000000000009", ContentSHA256: claimDigest, CreatedAt: now.Add(4 * time.Second)},
	}
	for index, receipt := range receipts {
		correlation, _ := ids.Derive(run.ID, receipt.TargetKind)
		if err := ledger.Record(ctx, receipt, correlation); err != nil {
			t.Fatalf("record %d: %v", index, err)
		}
		if err := ledger.Record(ctx, receipt, correlation); err != nil {
			t.Fatalf("record replay %d: %v", index, err)
		}
	}
	conflict := receipts[2]
	conflict.ContentSHA256 = sha256.Sum256([]byte("different"))
	if err := ledger.Record(ctx, conflict, "9a000000-0000-4000-8000-00000000000a"); !errors.Is(err, prototypemigration.ErrImportTarget) {
		t.Fatalf("conflicting receipt error=%v", err)
	}
	imported, err := ledger.MarkImported(ctx, accountID, run.ID, 1, now.Add(5*time.Second), "9b000000-0000-4000-8000-00000000000b")
	if err != nil || imported.State != prototypemigration.RunImported || imported.Version != 2 {
		t.Fatalf("imported=%+v err=%v", imported, err)
	}
	reconciliationDigest := sha256.Sum256([]byte("reconciliation"))
	reconciled, err := ledger.MarkReconciled(ctx, accountID, run.ID, 2, reconciliationDigest, now.Add(6*time.Second), "9c000000-0000-4000-8000-00000000000c")
	if err != nil || reconciled.State != prototypemigration.RunReconciled || reconciled.Version != 3 || reconciled.ReconciliationSHA256 == nil || *reconciled.ReconciliationSHA256 != reconciliationDigest {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
	var receiptCount, eventCount int
	if err := cell.WithAccountTx(ctx, accountID, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM spyglass.prototype_migration_receipts WHERE account_id=$1 AND run_id=$2),(SELECT count(*) FROM spyglass.prototype_migration_events WHERE account_id=$1 AND run_id=$2)`, accountID, run.ID).Scan(&receiptCount, &eventCount)
	}); err != nil || receiptCount != 4 || eventCount != 7 {
		t.Fatalf("receipts=%d events=%d err=%v", receiptCount, eventCount, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.prototype_migration_receipts SET source_id='rewritten' WHERE account_id=$1 AND run_id=$2`, accountID, run.ID); err == nil {
		t.Fatal("immutable receipt was updated")
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.prototype_migration_runs SET state='imported',version=4,updated_at=$3 WHERE account_id=$1 AND id=$2`, accountID, run.ID, now.Add(7*time.Second)); err == nil {
		t.Fatal("reconciled run was reopened")
	}
}

func TestPostgresPrototypeMigrationDestinationWaitsForExactDocumentProcessing(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	pool := openPool(t, ctx, databaseURL, nil)
	defer pool.Close()
	if _, err := migrations.Apply(ctx, pool, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	accountID := ids.AccountID("a1000000-0000-4000-8000-000000000101")
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',statement_timestamp())`, accountID); err != nil {
		t.Fatal(err)
	}
	cell, _ := database.NewCellPool(pool)
	repository, _ := postgresadapter.NewKnowledgeRepository(cell)
	clock := fixedClock{now: time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)}
	authorizer := prototypeMigrationAuthorizer{accountID: accountID}
	knowledgeService, _ := knowledgeapp.New(authorizer, repository, clock)
	documentService, _ := knowledgeapp.NewDocumentService(authorizer, repository, clock)
	objects := &prototypeMigrationObjects{values: map[string]prototypeMigrationObject{}}
	admission, _ := knowledgeapp.NewDocumentAdmissionService(documentService, objects)
	destination, _ := postgresadapter.NewPrototypeMigrationDestination(cell, admission, knowledgeService, objects)
	ledger, _ := postgresadapter.NewPrototypeMigrationLedger(cell, ids.RandomGenerator{})
	importer, _ := prototypemigration.NewImporter(ledger, destination, clock)

	body := []byte("Formation record\nVerified in retained prototype text.\n")
	bodyDigest := sha256.Sum256(body)
	snapshot := prototypemigration.Snapshot{
		TenantID: "a2000000-0000-4000-8000-000000000102", Checkpoint: "00000016/B374D848",
		Inventory:    prototypemigration.Inventory{FactVersions: 1, DocumentRevisions: 1},
		Documents:    []prototypemigration.LegacyDocumentRevision{{DocumentID: "a3000000-0000-4000-8000-000000000103", RevisionID: "a4000000-0000-4000-8000-000000000104", Revision: 1, Name: "Formation.txt", MediaType: "text/plain", Content: body, StoredSHA256: bodyDigest[:], Status: "ready", CreatedAt: clock.now.Add(-2 * time.Hour)}},
		FactVersions: []prototypemigration.LegacyFactVersion{{ID: "a5000000-0000-4000-8000-000000000105", Key: "legal.formation", Value: "verified", Scope: "account", SourceType: "document", SourceRef: "a3000000-0000-4000-8000-000000000103", Confidence: "0.900", Sensitivity: "confidential", Status: "active", RecordedAt: clock.now.Add(-time.Hour)}},
	}
	bundle, err := prototypemigration.NewTransformer().Transform(accountID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	first, err := importer.Import(ctx, bundle)
	if !errors.Is(err, prototypemigration.ErrReconciliation) || first.Run.State != prototypemigration.RunImported {
		t.Fatalf("pre-processing import=%+v err=%v", first, err)
	}
	plan := bundle.Manifest.Documents[0]
	revision, err := documentService.RecordScan(ctx, knowledgeapp.RecordScanCommand{Actor: access.Actor{WorkloadID: "knowledge-document-processor"}, AccountID: accountID, RevisionID: ids.KnowledgeDocumentRevisionID(plan.TargetRevisionID), State: knowledgedomain.ScanClean, Engine: "clamav/test", Signature: "fixture", CorrelationID: "a6000000-0000-4000-8000-000000000106"})
	if err != nil {
		t.Fatal(err)
	}
	extracted, err := objects.PutExtractedImmutable(ctx, knowledgeapp.ExtractedObjectWrite{AccountID: accountID, DocumentID: revision.DocumentID, RevisionID: revision.ID, Size: int64(len(body)), ContentSHA256: bodyDigest, Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	revision, err = documentService.RecordExtraction(ctx, knowledgeapp.RecordExtractionCommand{Actor: access.Actor{WorkloadID: "knowledge-document-processor"}, AccountID: accountID, RevisionID: revision.ID, TextSHA256: bodyDigest, TextBytes: int64(len(body)), Extractor: "spyglass/text-v1", ObjectKey: extracted.Identity.Key, ObjectVersion: extracted.Identity.Version, CorrelationID: "a7000000-0000-4000-8000-000000000107"})
	if err != nil {
		t.Fatal(err)
	}
	chunks, _ := knowledgeapp.ChunkExtractedText(body)
	if _, err := documentService.Index(ctx, knowledgeapp.IndexDocumentCommand{Actor: access.Actor{WorkloadID: "knowledge-document-processor"}, AccountID: accountID, RevisionID: revision.ID, Generation: knowledgeapp.DocumentChunkGeneration, Chunks: chunks, CorrelationID: "a8000000-0000-4000-8000-000000000108"}); err != nil {
		t.Fatal(err)
	}
	result, err := importer.Import(ctx, bundle)
	if err != nil || result.Run.State != prototypemigration.RunReconciled || result.Reconciliation.Documents != 1 || result.Reconciliation.DocumentObjects != 2 || result.Reconciliation.Chunks != 1 || result.Reconciliation.Evidence != 1 || result.Reconciliation.Claims != 1 || result.Reconciliation.Citations != 1 || result.Reconciliation.ContentSHA256 == ([32]byte{}) {
		t.Fatalf("reconciled result=%+v err=%v", result, err)
	}
}

type prototypeMigrationAuthorizer struct{ accountID ids.AccountID }

func (authorizer prototypeMigrationAuthorizer) Authorize(_ context.Context, actor access.Actor, accountID ids.AccountID, requirement access.Requirement) (access.AccountContext, error) {
	if actor.WorkloadID == "" || actor.UserID != "" || accountID != authorizer.accountID || requirement.Package != "knowledge" || !requirement.Mutation {
		return access.AccountContext{}, &access.DeniedError{Code: access.DenialUnauthenticated}
	}
	return access.AccountContext{AccountID: accountID}, nil
}

type prototypeMigrationObject struct {
	identity knowledgeapp.DocumentObjectIdentity
	body     []byte
}

type prototypeMigrationObjects struct {
	values map[string]prototypeMigrationObject
}

func (store *prototypeMigrationObjects) Verify(context.Context) error { return nil }

func (store *prototypeMigrationObjects) PutImmutable(_ context.Context, value knowledgeapp.SourceObjectWrite) (knowledgeapp.SourceObjectWriteResult, error) {
	key, err := value.Key()
	if err != nil {
		return knowledgeapp.SourceObjectWriteResult{}, err
	}
	identity, created, err := store.put(key, "source-v1", value.Size, value.ContentSHA256, value.Body)
	return knowledgeapp.SourceObjectWriteResult{Identity: identity, Created: created}, err
}

func (store *prototypeMigrationObjects) PutExtractedImmutable(_ context.Context, value knowledgeapp.ExtractedObjectWrite) (knowledgeapp.ExtractedObjectWriteResult, error) {
	key, err := value.Key()
	if err != nil {
		return knowledgeapp.ExtractedObjectWriteResult{}, err
	}
	identity, created, err := store.put(key, "extracted-v1", value.Size, value.ContentSHA256, value.Body)
	return knowledgeapp.ExtractedObjectWriteResult{Identity: identity, Created: created}, err
}

func (store *prototypeMigrationObjects) put(key, version string, size int64, digest [32]byte, body io.Reader) (knowledgeapp.DocumentObjectIdentity, bool, error) {
	raw, err := io.ReadAll(body)
	if err != nil || int64(len(raw)) != size || sha256.Sum256(raw) != digest {
		return knowledgeapp.DocumentObjectIdentity{}, false, errors.New("invalid object")
	}
	identity := knowledgeapp.DocumentObjectIdentity{Key: key, Version: version, Size: size, ContentSHA256: digest}
	if current, exists := store.values[key]; exists {
		if current.identity != identity || !bytes.Equal(current.body, raw) {
			return knowledgeapp.DocumentObjectIdentity{}, false, errors.New("object conflict")
		}
		return current.identity, false, nil
	}
	store.values[key] = prototypeMigrationObject{identity: identity, body: append([]byte(nil), raw...)}
	return identity, true, nil
}

func (store *prototypeMigrationObjects) Open(_ context.Context, identity knowledgeapp.DocumentObjectIdentity) (io.ReadCloser, error) {
	value, exists := store.values[identity.Key]
	if !exists || value.identity != identity {
		return nil, errors.New("object missing")
	}
	return io.NopCloser(bytes.NewReader(value.body)), nil
}

func (store *prototypeMigrationObjects) Delete(_ context.Context, identity knowledgeapp.DocumentObjectIdentity) error {
	delete(store.values, identity.Key)
	return nil
}
