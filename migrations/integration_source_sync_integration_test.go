package migrations_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	"github.com/tinfoyle/spyglass-engine/internal/application/integrationsync"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestIntegrationSourceSyncPersistsBoundedCursorAndCaptureReceipts(t *testing.T) {
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
	var now time.Time
	if err := pool.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	accountID := ids.AccountID("b1000000-0000-4000-8000-000000000001")
	userID := "b2000000-0000-4000-8000-000000000002"
	assessmentID := "b3000000-0000-4000-8000-000000000003"
	connectionID := "b4000000-0000-4000-8000-000000000004"
	connectionRevisionID := "b5000000-0000-4000-8000-000000000005"
	credentialID := "b6000000-0000-4000-8000-000000000006"
	grantID := ids.BaselineSourceGrantID("b7000000-0000-4000-8000-000000000007")
	documentID := ids.KnowledgeDocumentID("b8000000-0000-4000-8000-000000000008")
	documentRevisionID := ids.KnowledgeDocumentRevisionID("b9000000-0000-4000-8000-000000000009")
	contentDigest := sha256.Sum256([]byte("governed Drive fixture"))
	if _, err := pool.Exec(ctx, `
		INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2);
		INSERT INTO spyglass.baseline_assessments(account_id,id,catalog_version,scope_policy_version,state,created_by_user_id,version,created_at,updated_at)
		VALUES ($1,$3,'catalog-v1','scope-v1','active',$4,1,$2,$2);
		INSERT INTO spyglass.integration_connections(account_id,id,name,connector_kind,state,current_revision,credential_generation,version,created_by_user_id,created_at,updated_at)
		VALUES ($1,$5,'Governed Drive','google_drive','pending',1,0,1,$4,$2,$2);
		INSERT INTO spyglass.integration_connection_revisions(account_id,id,connection_id,revision,capabilities,drive_folder_ids,created_by_user_id,created_at)
		VALUES ($1,$6,$5,1,ARRAY['google_drive.read'],ARRAY['folder-a'],$4,$2);
		INSERT INTO spyglass.integration_credentials(account_id,id,connection_id,generation,provider,reference_sha256,state,created_by_user_id,created_at,updated_at)
		VALUES ($1,$7,$5,1,'mock_google_drive',decode(repeat('61',32),'hex'),'active',$4,$2,$2);
		UPDATE spyglass.integration_connections SET state='active',credential_id=$7,credential_generation=1,version=2,updated_at=$2 WHERE account_id=$1 AND id=$5;
		INSERT INTO spyglass.baseline_source_grants(account_id,id,assessment_id,connection_id,source_kind,folders,state,granted_by_user_id,version,created_at,updated_at)
		VALUES ($1,$8,$3,$5,'google_drive',ARRAY['folder-a'],'active',$4,1,$2,$2);
		INSERT INTO spyglass.knowledge_documents(account_id,id,title,sensitivity,state,version,created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$9,'Drive fixture','internal','processing',1,'workload','integration-source-sync',$2,$2);
		INSERT INTO spyglass.knowledge_document_revisions(account_id,id,document_id,revision,filename,declared_media_type,verified_media_type,
		  byte_size,content_sha256,object_key,object_version,change_summary,state,scan_state,scan_engine,scan_signature,
		  extraction_state,extractor,extracted_object_key,extracted_object_version,index_state,index_generation,failure_code,
		  created_by_kind,created_by_id,created_at,updated_at)
		VALUES ($1,$10,$9,1,'fixture.txt','text/plain','text/plain',22,$11,
		  'accounts/'||$1::text||'/documents/'||$9::text||'/revisions/'||$10::text||'/source','version-1','Drive capture',
		  'quarantined','pending','','','pending','','','','pending','','','workload','integration-source-sync',$2,$2)`,
		pgx.QueryExecModeSimpleProtocol, accountID, now, assessmentID, userID, connectionID, connectionRevisionID, credentialID, grantID, documentID,
		documentRevisionID, contentDigest[:]); err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewIntegrationSourceSyncRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	claimAt := now.Add(time.Second)
	syncID := ids.IntegrationSourceSyncID("ba000000-0000-4000-8000-000000000010")
	claim, found, err := repository.Claim(ctx, syncID, claimAt, claimAt.Add(time.Minute))
	if err != nil || !found || !claim.Valid(claimAt) || claim.GrantID != grantID || len(claim.FolderIDs) != 1 ||
		claim.FolderIDs[0] != "folder-a" || len(claim.CursorCiphertext) != 0 {
		t.Fatalf("claim=%+v found=%v err=%v", claim, found, err)
	}
	capture := integrationsync.CaptureReceipt{ID: "bb000000-0000-4000-8000-000000000011", FolderID: "folder-a",
		ProviderObjectSHA256: sha256.Sum256([]byte("provider-object")), ProviderRevisionSHA256: sha256.Sum256([]byte("provider-revision")),
		Operation: integrationsync.CaptureAdmitted, DocumentID: documentID, DocumentRevisionID: documentRevisionID, ContentSHA256: contentDigest}
	completedAt := claimAt.Add(time.Second)
	completion := integrationsync.Completion{Claim: claim, CursorCiphertext: []byte("sealed-page-token"),
		CursorSHA256: sha256.Sum256([]byte("page-token")), Captures: []integrationsync.CaptureReceipt{capture}, HasMore: true, CompletedAt: completedAt}
	if err := repository.Complete(ctx, completion); err != nil {
		t.Fatal(err)
	}
	var captureCount int
	var storedCursor []byte
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM spyglass.integration_source_captures WHERE account_id=$1 AND grant_id=$2`, accountID, grantID).Scan(&captureCount); err != nil || captureCount != 1 {
		t.Fatalf("capture count=%d err=%v", captureCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT cursor_ciphertext FROM spyglass.integration_source_sync_queue WHERE account_id=$1 AND grant_id=$2`, accountID, grantID).Scan(&storedCursor); err != nil || string(storedCursor) != "sealed-page-token" {
		t.Fatalf("stored cursor=%q err=%v", storedCursor, err)
	}
	nextSyncID := ids.IntegrationSourceSyncID("bc000000-0000-4000-8000-000000000012")
	next, found, err := repository.Claim(ctx, nextSyncID, completedAt.Add(time.Second), completedAt.Add(time.Minute))
	if err != nil || !found || string(next.CursorCiphertext) != "sealed-page-token" || next.CursorSHA256 != completion.CursorSHA256 {
		t.Fatalf("next claim=%+v found=%v err=%v", next, found, err)
	}
	emptyPage := integrationsync.Completion{Claim: next, CursorCiphertext: []byte("sealed-next-page-token"),
		CursorSHA256: sha256.Sum256([]byte("next-page-token")), HasMore: true, CompletedAt: completedAt.Add(2 * time.Second)}
	if err := repository.Complete(ctx, emptyPage); err != nil {
		t.Fatalf("complete empty provider page: %v", err)
	}
	finalSyncID := ids.IntegrationSourceSyncID("bd000000-0000-4000-8000-000000000013")
	finalClaim, found, err := repository.Claim(ctx, finalSyncID, completedAt.Add(3*time.Second), completedAt.Add(time.Minute))
	if err != nil || !found || string(finalClaim.CursorCiphertext) != "sealed-next-page-token" {
		t.Fatalf("final claim=%+v found=%v err=%v", finalClaim, found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.baseline_source_grants SET state='revoked',revoked_by_user_id=$3,
		revoke_reason='Drive capture authorization removed',version=2,revoked_at=$4,updated_at=$4 WHERE account_id=$1 AND id=$2`,
		accountID, grantID, userID, completedAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	stale := completion
	stale.Claim = finalClaim
	stale.CompletedAt = completedAt.Add(5 * time.Second)
	if err := repository.Complete(ctx, stale); err == nil || !errors.Is(err, integrationsync.ErrUnavailable) {
		t.Fatalf("revoked-grant completion error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE spyglass.integration_source_captures SET operation='deleted' WHERE account_id=$1 AND id=$2`, accountID, capture.ID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("capture mutation result=%v", err)
	}
}
