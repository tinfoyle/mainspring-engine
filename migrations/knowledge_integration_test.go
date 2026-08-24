package migrations_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/tinfoyle/spyglass-engine/internal/adapters/postgres"
	knowledgeapp "github.com/tinfoyle/spyglass-engine/internal/application/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/modules/accounts"
	knowledgedomain "github.com/tinfoyle/spyglass-engine/internal/modules/knowledge"
	"github.com/tinfoyle/spyglass-engine/internal/platform/database"
	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestKnowledgeFoundationIsAccountIsolatedImmutableAndEvidenceBound(t *testing.T) {
	adminURL := os.Getenv("SPYGLASS_POSTGRES_TEST_URL")
	if adminURL == "" {
		t.Skip("SPYGLASS_POSTGRES_TEST_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := createDatabase(t, ctx, adminURL)
	defer cleanup()
	owner := openPool(t, ctx, databaseURL, nil)
	defer owner.Close()
	if _, err := migrations.Apply(ctx, owner, migrations.Cell); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := owner.QueryRow(ctx, `SELECT statement_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	accountA, accountB := "a1000000-0000-4000-8000-000000000001", "b1000000-0000-4000-8000-000000000001"
	for _, accountID := range []string{accountA, accountB} {
		if _, err := owner.Exec(ctx, `INSERT INTO spyglass.account_namespaces(account_id,placement_generation,state,created_at) VALUES ($1,1,'active',$2)`, accountID, now); err != nil {
			t.Fatal(err)
		}
	}
	seedKnowledgeFoundation(t, ctx, owner, accountA, "a2000000-0000-4000-8000-000000000002", "a3000000-0000-4000-8000-000000000003", "a4000000-0000-4000-8000-000000000004", "a5000000-0000-4000-8000-000000000005", "a6000000-0000-4000-8000-000000000006", now)
	seedKnowledgeFoundation(t, ctx, owner, accountB, "b2000000-0000-4000-8000-000000000002", "b3000000-0000-4000-8000-000000000003", "b4000000-0000-4000-8000-000000000004", "b5000000-0000-4000-8000-000000000005", "b6000000-0000-4000-8000-000000000006", now)

	role := "spyglass_knowledge_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON spyglass.knowledge_evidence,spyglass.knowledge_claims,spyglass.knowledge_claim_citations,
			spyglass.knowledge_facts,spyglass.knowledge_fact_revisions,spyglass.knowledge_events TO `+role); err != nil {
		t.Fatal(err)
	}
	reader := openPool(t, ctx, databaseURL, func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE `+role)
		return err
	})
	defer func() {
		reader.Close()
		_, _ = owner.Exec(context.Background(), `DROP OWNED BY `+role+`; DROP ROLE IF EXISTS `+role)
	}()

	for _, accountID := range []string{accountA, accountB} {
		if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountID); err != nil {
			t.Fatal(err)
		}
		var evidence, claims, citations, facts, revisions, events int
		if err := reader.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM spyglass.knowledge_evidence),(SELECT count(*) FROM spyglass.knowledge_claims),
			(SELECT count(*) FROM spyglass.knowledge_claim_citations),(SELECT count(*) FROM spyglass.knowledge_facts),
			(SELECT count(*) FROM spyglass.knowledge_fact_revisions),(SELECT count(*) FROM spyglass.knowledge_events)`).
			Scan(&evidence, &claims, &citations, &facts, &revisions, &events); err != nil || evidence != 1 || claims != 1 || citations != 1 || facts != 1 || revisions != 1 || events != 3 {
			t.Fatalf("Account %s Knowledge counts=%d/%d/%d/%d/%d/%d err=%v", accountID, evidence, claims, citations, facts, revisions, events, err)
		}
	}
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,'a7000000-0000-4000-8000-000000000007','owner_statement','cross-account','1',decode(repeat('11',32),'hex'),$2,'user','a2000000-0000-4000-8000-000000000002',$2)`, accountB, now); err == nil {
		t.Fatal("cross-Account Knowledge insert bypassed RLS")
	}
	if _, err := reader.Exec(ctx, `UPDATE spyglass.knowledge_fact_revisions SET revision=2`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("fact revision update=%v", err)
	}
	if _, err := reader.Exec(ctx, `DELETE FROM spyglass.knowledge_events`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("event delete=%v", err)
	}
	if _, err := reader.Exec(ctx, `UPDATE spyglass.knowledge_evidence SET source_revision='2'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("evidence update=%v", err)
	}
	if _, err := reader.Exec(ctx, `UPDATE spyglass.knowledge_claim_citations SET locator='changed'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("citation update=%v", err)
	}
	if _, err := reader.Exec(ctx, `UPDATE spyglass.knowledge_claims SET confidence=1`); err == nil || !strings.Contains(err.Error(), "proposition is immutable") {
		t.Fatalf("claim content update=%v", err)
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_claim_citations(account_id,claim_id,evidence_id,evidence_kind,relation,locator,created_at)
		VALUES ($1,'a4000000-0000-4000-8000-000000000004','b3000000-0000-4000-8000-000000000003','owner_statement','supports','cross',$2)`, accountA, now); err == nil {
		t.Fatal("cross-Account evidence citation bypassed composite foreign key")
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_claims(account_id,id,scope_kind,fact_key,canonical_value,value_sha256,hash_version,confidence,sensitivity,proposed_by_kind,proposed_by_id,state,version,created_at,updated_at)
		VALUES ($1,'a8000000-0000-4000-8000-000000000008','account','organization.unsupported',convert_to('true','UTF8'),decode(repeat('33',32),'hex'),1,500,'internal','workload','agent:test','proposed',1,$2,$2)`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,'a9000000-0000-4000-8000-000000000009','agent_derivation','run:test','1',decode(repeat('22',32),'hex'),$2,'workload','agent:test',$2)`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.knowledge_claim_citations(account_id,claim_id,evidence_id,evidence_kind,relation,locator,created_at)
		VALUES ($1,'a8000000-0000-4000-8000-000000000008','a9000000-0000-4000-8000-000000000009','agent_derivation','supports','model output',$2)`, accountA, now); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_claims SET state='accepted',decision_reason='reviewed output',decided_by_user_id='a2000000-0000-4000-8000-000000000002',decided_at=$2,version=2,updated_at=$2 WHERE account_id=$1 AND id='a8000000-0000-4000-8000-000000000008'`, accountA, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "independent supporting evidence") {
		t.Fatalf("agent-only authoritative claim update=%v", err)
	}
	var fencedTables int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_trigger trigger_row JOIN pg_class table_row ON table_row.oid=trigger_row.tgrelid
		JOIN pg_namespace namespace_row ON namespace_row.oid=table_row.relnamespace
		WHERE namespace_row.nspname='spyglass' AND table_row.relname LIKE 'knowledge_%' AND trigger_row.tgname='account_namespace_write_fence' AND NOT trigger_row.tgisinternal`).Scan(&fencedTables); err != nil || fencedTables != 13 {
		t.Fatalf("Knowledge movement write fences=%d err=%v", fencedTables, err)
	}
	exerciseKnowledgeRepository(t, ctx, owner, ids.AccountID(accountA), ids.AccountID(accountB), now.Add(5*time.Minute))
	exerciseKnowledgeDocumentRepository(t, ctx, owner, ids.AccountID(accountA), ids.AccountID(accountB), now.Add(10*time.Minute))
}

type knowledgeExec interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func seedKnowledgeFoundation(t *testing.T, ctx context.Context, pool knowledgeExec, accountID, userID, evidenceID, claimID, factID, eventBase string, now time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_evidence(account_id,id,source_kind,source_reference,source_revision,content_sha256,captured_at,created_by_kind,created_by_id,created_at)
		VALUES ($1,$2,'owner_statement',$3,'1',decode(repeat('11',32),'hex'),$5,'user',$4,$5)`, accountID, evidenceID, "membership:"+userID, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_claims(account_id,id,scope_kind,fact_key,canonical_value,value_sha256,hash_version,confidence,sensitivity,proposed_by_kind,proposed_by_id,state,version,created_at,updated_at)
		VALUES ($1,$2,'account','organization.legal_name',convert_to('"Northstar LLC"','UTF8'),decode(repeat('22',32),'hex'),1,1000,'internal','user',$3,'proposed',1,$4,$4)`, accountID, claimID, userID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_claim_citations(account_id,claim_id,evidence_id,evidence_kind,relation,locator,created_at)
		VALUES ($1,$2,$3,'owner_statement','supports','owner statement',$4)`, accountID, claimID, evidenceID, now); err != nil {
		t.Fatal(err)
	}
	decidedAt := now.Add(time.Second)
	if _, err := pool.Exec(ctx, `UPDATE spyglass.knowledge_claims SET state='accepted',decision_reason='Owner confirmed record',decided_by_user_id=$3,decided_at=$4,version=2,updated_at=$4 WHERE account_id=$1 AND id=$2`, accountID, claimID, userID, decidedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_facts(account_id,id,scope_kind,fact_key,current_claim_id,state,revision,accepted_by_user_id,accepted_at,created_at,updated_at)
		VALUES ($1,$2,'account','organization.legal_name',$3,'active',1,$4,$5,$5,$5)`, accountID, factID, claimID, userID, decidedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_fact_revisions(account_id,fact_id,revision,claim_id,accepted_by_user_id,accepted_at) VALUES ($1,$2,1,$3,$4,$5)`, accountID, factID, claimID, userID, decidedAt); err != nil {
		t.Fatal(err)
	}
	for index, event := range []struct{ kind, eventType string }{{"evidence", "evidence_registered"}, {"claim", "claim_accepted"}, {"fact", "fact_created"}} {
		eventID := eventBase[:len(eventBase)-1] + string(rune('7'+index))
		if _, err := pool.Exec(ctx, `INSERT INTO spyglass.knowledge_events(account_id,id,aggregate_kind,evidence_id,claim_id,fact_id,event_type,from_version,to_version,actor_kind,actor_id,reason_code,correlation_id,redacted_payload,occurred_at)
			VALUES ($1,$2,$3,CASE WHEN $3='evidence' THEN $4::uuid END,CASE WHEN $3='claim' THEN $5::uuid END,CASE WHEN $3='fact' THEN $6::uuid END,$7,CASE WHEN $3='claim' THEN 1 ELSE 0 END,CASE WHEN $3='claim' THEN 2 ELSE 1 END,'user',$8,'owner_confirmed',$9,'{}',$10)`, accountID, eventID, event.kind, evidenceID, claimID, factID, event.eventType, userID, eventBase, decidedAt); err != nil {
			t.Fatal(err)
		}
	}
}

func exerciseKnowledgeRepository(t *testing.T, ctx context.Context, owner *pgxpool.Pool, accountID, otherAccountID ids.AccountID, now time.Time) {
	t.Helper()
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewKnowledgeRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	userID := ids.UserID("c1000000-0000-4000-8000-000000000001")
	actor := knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: string(userID)}
	evidenceID := ids.KnowledgeEvidenceID("c2000000-0000-4000-8000-000000000002")
	claimID := ids.KnowledgeClaimID("c3000000-0000-4000-8000-000000000003")
	correlation := "c4000000-0000-4000-8000-000000000004"
	evidence, err := knowledgedomain.NewEvidence(knowledgedomain.Evidence{ID: evidenceID, AccountID: accountID, Kind: knowledgedomain.SourceOwnerStatement, SourceReference: "membership:" + string(userID), SourceRevision: "1", ContentSHA256: sha256.Sum256([]byte("Northstar Studio LLC")), CapturedAt: now, CreatedBy: actor, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	mutation := knowledgeapp.Mutation{Actor: actor, CorrelationID: correlation, ReasonCode: "source_registered", At: now}
	createdEvidence, err := repository.RegisterEvidence(ctx, evidence, mutation)
	if err != nil || createdEvidence.ID != evidenceID {
		t.Fatalf("register evidence=%+v err=%v", createdEvidence, err)
	}
	replayedEvidence := evidence
	replayedEvidence.CreatedAt = replayedEvidence.CreatedAt.Add(time.Second)
	if _, err := repository.RegisterEvidence(ctx, replayedEvidence, mutation); err != nil {
		t.Fatalf("replay evidence err=%v", err)
	}
	claim, err := knowledgedomain.NewClaim(knowledgedomain.ClaimDraft{ID: claimID, AccountID: accountID, Scope: knowledgedomain.Scope{Kind: knowledgedomain.ScopeAccount}, Key: "organization.display_name", CanonicalValue: json.RawMessage(`"Northstar Studio"`), Confidence: 950, Sensitivity: knowledgedomain.SensitivityInternal, Citations: []knowledgedomain.Citation{{EvidenceID: evidenceID, EvidenceKind: knowledgedomain.SourceOwnerStatement, Relation: knowledgedomain.EvidenceSupports, Locator: "owner statement"}}, ProposedBy: actor}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	mutation = knowledgeapp.Mutation{Actor: actor, CorrelationID: "c5000000-0000-4000-8000-000000000005", ReasonCode: "claim_proposed", At: claim.CreatedAt}
	createdClaim, err := repository.ProposeClaim(ctx, claim, mutation)
	if err != nil || createdClaim.ID != claimID {
		t.Fatalf("propose claim=%+v err=%v", createdClaim, err)
	}
	replayedClaim := claim
	replayedClaim.CreatedAt = replayedClaim.CreatedAt.Add(time.Second)
	replayedClaim.UpdatedAt = replayedClaim.UpdatedAt.Add(time.Second)
	if _, err := repository.ProposeClaim(ctx, replayedClaim, mutation); err != nil {
		t.Fatalf("replay claim err=%v", err)
	}
	claimPage, err := repository.ListClaims(ctx, accountID, knowledgeapp.ClaimListQuery{State: knowledgedomain.ClaimProposed, KeyPrefix: "organization.", Limit: 1})
	if err != nil || len(claimPage.Items) != 1 || claimPage.Items[0].ID != claimID || claimPage.Items[0].Key != "organization.display_name" {
		t.Fatalf("claim page=%+v err=%v", claimPage, err)
	}
	if _, err := repository.GetClaim(ctx, otherAccountID, claimID); !errors.Is(err, knowledgeapp.ErrNotFound) {
		t.Fatalf("cross-Account claim read err=%v", err)
	}
	decisionAt := now.Add(2 * time.Second)
	factID := ids.KnowledgeFactID("c6000000-0000-4000-8000-000000000006")
	decided, fact, err := repository.DecideClaim(ctx, accountID, claimID, factID, knowledgedomain.DecideClaimCommand{Accept: true, Reason: "Owner statement reviewed", Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 1, At: decisionAt}, knowledgeapp.Mutation{Actor: actor, CorrelationID: "c7000000-0000-4000-8000-000000000007", ReasonCode: "claim_accepted", At: decisionAt})
	if err != nil || decided.State != knowledgedomain.ClaimAccepted || fact == nil || fact.Revision != 1 {
		t.Fatalf("decided=%+v fact=%+v err=%v", decided, fact, err)
	}
	replayedDecision, replayedFact, err := repository.DecideClaim(ctx, accountID, claimID, factID, knowledgedomain.DecideClaimCommand{Accept: true, Reason: "Owner statement reviewed", Actor: actor, Role: accounts.RoleOwner, ExpectedVersion: 1, At: decisionAt.Add(time.Second)}, knowledgeapp.Mutation{Actor: actor, CorrelationID: "c7000000-0000-4000-8000-000000000007", ReasonCode: "claim_accepted", At: decisionAt.Add(time.Second)})
	if err != nil || replayedDecision.State != knowledgedomain.ClaimAccepted || replayedFact == nil || replayedFact.ID != factID {
		t.Fatalf("replayed decision=%+v fact=%+v err=%v", replayedDecision, replayedFact, err)
	}
	page, err := repository.ListFacts(ctx, accountID, knowledgeapp.FactListQuery{KeyPrefix: "organization.", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != factID || page.Items[0].CurrentClaimID != claimID {
		t.Fatalf("fact page=%+v err=%v", page, err)
	}
	var unsafeEvents int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.knowledge_events WHERE account_id=$1 AND (redacted_payload::text LIKE '%Northstar%' OR redacted_payload::text LIKE '%Owner statement reviewed%')`, accountID).Scan(&unsafeEvents); err != nil || unsafeEvents != 0 {
		t.Fatalf("unsafe Knowledge events=%d err=%v", unsafeEvents, err)
	}
}

func exerciseKnowledgeDocumentRepository(t *testing.T, ctx context.Context, owner *pgxpool.Pool, accountID, otherAccountID ids.AccountID, now time.Time) {
	t.Helper()
	cell, err := database.NewCellPool(owner)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := postgresadapter.NewKnowledgeRepository(cell)
	if err != nil {
		t.Fatal(err)
	}
	documentID := ids.KnowledgeDocumentID("d1000000-0000-4000-8000-000000000001")
	revisionID := ids.KnowledgeDocumentRevisionID("d2000000-0000-4000-8000-000000000002")
	actor := knowledgedomain.Actor{Kind: knowledgedomain.ActorUser, ID: "d3000000-0000-4000-8000-000000000003"}
	worker := knowledgedomain.Actor{Kind: knowledgedomain.ActorWorkload, ID: "worker:document-admission"}
	document, err := knowledgedomain.NewDocument(documentID, accountID, "Operating plan", knowledgedomain.SensitivityConfidential, nil, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: revisionID, DocumentID: documentID, AccountID: accountID, Number: 1, Filename: "operating-plan.md", DeclaredType: "text/markdown", VerifiedType: "text/markdown",
		ByteSize: 128, ContentSHA256: sha256.Sum256([]byte("source bytes")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(documentID) + "/revisions/" + string(revisionID) + "/source", ObjectVersion: "version-1", ChangeSummary: "Initial upload", CreatedBy: actor,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	mutation := knowledgeapp.Mutation{Actor: actor, CorrelationID: "d4000000-0000-4000-8000-000000000004", ReasonCode: "document_admitted", At: now}
	admittedDocument, admittedRevision, err := repository.AdmitDocument(ctx, document, revision, mutation)
	if err != nil || admittedDocument.ID != documentID || admittedRevision.ID != revisionID {
		t.Fatalf("admit document=%+v revision=%+v err=%v", admittedDocument, admittedRevision, err)
	}
	if _, _, err := repository.AdmitDocument(ctx, document, revision, mutation); err != nil {
		t.Fatalf("replay document admission err=%v", err)
	}
	latest, err := repository.GetLatestDocumentRevision(ctx, accountID, documentID)
	if err != nil || latest.ID != revisionID {
		t.Fatalf("latest document revision=%+v err=%v", latest, err)
	}
	page, err := repository.ListDocuments(ctx, accountID, knowledgeapp.DocumentListQuery{State: knowledgedomain.DocumentProcessing, TitlePrefix: "Operating", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != documentID || page.Items[0].Sensitivity != knowledgedomain.SensitivityConfidential {
		t.Fatalf("document page=%+v err=%v", page, err)
	}
	queue, err := postgresadapter.NewKnowledgeDocumentProcessingQueue(owner)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := queue.Claim(ctx, "db000000-0000-4000-8000-00000000000b", now, 10*time.Minute)
	if err != nil || !found || claim.AccountID != accountID || claim.RevisionID != revisionID || claim.Attempt != 1 {
		t.Fatalf("processing claim=%+v found=%v err=%v", claim, found, err)
	}
	if second, found, err := queue.Claim(ctx, "dc000000-0000-4000-8000-00000000000c", now, 10*time.Minute); err != nil || found {
		t.Fatalf("duplicate processing claim=%+v found=%v err=%v", second, found, err)
	}
	if _, err := repository.GetDocument(ctx, otherAccountID, documentID); !errors.Is(err, knowledgeapp.ErrNotFound) {
		t.Fatalf("cross-Account document read err=%v", err)
	}
	if page, err := repository.ListDocuments(ctx, otherAccountID, knowledgeapp.DocumentListQuery{Limit: 10}); err != nil || len(page.Items) != 0 {
		t.Fatalf("cross-Account document page=%+v err=%v", page, err)
	}
	scanned, err := revision.RecordScan(knowledgedomain.ScanClean, "clamav/1.4.3", "daily.cvd:27810", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	scanned, err = repository.SaveDocumentRevision(ctx, scanned, revision.UpdatedAt, "scan_completed", knowledgeapp.Mutation{Actor: worker, CorrelationID: "d5000000-0000-4000-8000-000000000005", ReasonCode: "scan_completed", At: scanned.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	extracted, err := scanned.RecordExtraction(sha256.Sum256([]byte("Extracted operating plan")), 24, "spyglass/text-v1", "accounts/"+string(accountID)+"/documents/"+string(documentID)+"/revisions/"+string(revisionID)+"/extracted/text", "extracted-version-1", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	extracted, err = repository.SaveDocumentRevision(ctx, extracted, scanned.UpdatedAt, "extraction_completed", knowledgeapp.Mutation{Actor: worker, CorrelationID: "d6000000-0000-4000-8000-000000000006", ReasonCode: "extraction_completed", At: extracted.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := extracted.RecordIndex("knowledge-v1", 1, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	content := "Extracted operating plan"
	chunk, err := knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: "d7000000-0000-4000-8000-000000000007", AccountID: accountID, RevisionID: revisionID, Index: 0, StartByte: 0, EndByte: int64(len(content)), Content: content, ContentSHA256: sha256.Sum256([]byte(content)), TokenCount: 3, IndexGeneration: "knowledge-v1", CreatedAt: indexed.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	indexed, err = repository.IndexDocumentRevision(ctx, indexed, extracted.UpdatedAt, []knowledgedomain.DocumentChunk{chunk}, knowledgeapp.Mutation{Actor: worker, CorrelationID: "d8000000-0000-4000-8000-000000000008", ReasonCode: "index_completed", At: indexed.UpdatedAt})
	if err != nil || indexed.State != knowledgedomain.RevisionReady {
		t.Fatalf("index document revision=%+v err=%v", indexed, err)
	}
	if err := queue.Complete(ctx, claim, now.Add(4*time.Second)); err != nil {
		t.Fatalf("complete document processing queue: %v", err)
	}
	stats, err := queue.Stats(ctx, now.Add(4*time.Second))
	if err != nil || stats.Completed != 1 || stats.Ready != 0 || stats.DeadLetter != 0 {
		t.Fatalf("processing stats=%+v err=%v", stats, err)
	}
	published, err := repository.PublishDocumentRevision(ctx, accountID, documentID, revisionID, 1, knowledgeapp.Mutation{Actor: actor, CorrelationID: "d9000000-0000-4000-8000-000000000009", ReasonCode: "revision_published", At: now.Add(4 * time.Second)})
	if err != nil || published.State != knowledgedomain.DocumentReady || published.CurrentRevisionID != revisionID {
		t.Fatalf("publish document=%+v err=%v", published, err)
	}
	retrieved, err := repository.RetrieveDocumentChunks(ctx, accountID, knowledgeapp.DocumentRetrievalQuery{Text: "operating plan", Limit: 10, IncludeRestricted: true})
	if err != nil || len(retrieved) != 1 || retrieved[0].DocumentID != documentID || retrieved[0].RevisionID != revisionID || retrieved[0].ChunkID != chunk.ID || retrieved[0].Content != content || retrieved[0].Rank <= 0 {
		t.Fatalf("document retrieval=%+v err=%v", retrieved, err)
	}
	citation, err := repository.GetDocumentCitation(ctx, accountID, documentID, revisionID, chunk.ID, true)
	if err != nil || citation.ContentSHA256 != chunk.ContentSHA256 || citation.StartByte != chunk.StartByte || citation.EndByte != chunk.EndByte {
		t.Fatalf("document citation=%+v err=%v", citation, err)
	}
	if results, err := repository.RetrieveDocumentChunks(ctx, otherAccountID, knowledgeapp.DocumentRetrievalQuery{Text: "operating plan", Limit: 10, IncludeRestricted: true}); err != nil || len(results) != 0 {
		t.Fatalf("cross-Account document retrieval=%+v err=%v", results, err)
	}
	revision2ID := ids.KnowledgeDocumentRevisionID("e1000000-0000-4000-8000-000000000001")
	revision2, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: revision2ID, DocumentID: documentID, AccountID: accountID, Number: 2, Filename: "operating-plan-v2.md", DeclaredType: "text/markdown", VerifiedType: "text/markdown",
		ByteSize: 136, ContentSHA256: sha256.Sum256([]byte("updated source bytes")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(documentID) + "/revisions/" + string(revision2ID) + "/source", ObjectVersion: "version-2", ChangeSummary: "Drive source changed", CreatedBy: worker,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision2Mutation := knowledgeapp.Mutation{Actor: worker, CorrelationID: "e2000000-0000-4000-8000-000000000002", ReasonCode: "document_revision_admitted", At: revision2.CreatedAt}
	admittedRevision2, err := repository.AdmitDocumentRevision(ctx, published, revision2, revision2Mutation)
	if err != nil || admittedRevision2.ID != revision2ID || admittedRevision2.Number != 2 {
		t.Fatalf("admit second revision=%+v err=%v", admittedRevision2, err)
	}
	if replayed, err := repository.AdmitDocumentRevision(ctx, published, revision2, revision2Mutation); err != nil || replayed.ID != revision2ID {
		t.Fatalf("replay second revision=%+v err=%v", replayed, err)
	}
	conflictingRevision2 := revision2
	conflictingRevision2.ObjectVersion = "different-version"
	if _, err := repository.AdmitDocumentRevision(ctx, published, conflictingRevision2, revision2Mutation); !errors.Is(err, knowledgeapp.ErrConflict) {
		t.Fatalf("conflicting second revision replay err=%v", err)
	}
	revision3, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: "e3000000-0000-4000-8000-000000000003", DocumentID: documentID, AccountID: accountID, Number: 3, Filename: "operating-plan-v3.md", DeclaredType: "text/markdown", VerifiedType: "text/markdown",
		ByteSize: 144, ContentSHA256: sha256.Sum256([]byte("another source version")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(documentID) + "/revisions/e3000000-0000-4000-8000-000000000003/source", ObjectVersion: "version-3", ChangeSummary: "Another Drive source change", CreatedBy: worker,
	}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AdmitDocumentRevision(ctx, published, revision3, knowledgeapp.Mutation{Actor: worker, CorrelationID: "e4000000-0000-4000-8000-000000000004", ReasonCode: "document_revision_admitted", At: revision3.CreatedAt}); !errors.Is(err, knowledgeapp.ErrConstraint) {
		t.Fatalf("second pending revision err=%v", err)
	}
	revision2Claim, found, err := queue.Claim(ctx, "e5000000-0000-4000-8000-000000000005", now.Add(5*time.Second), 10*time.Minute)
	if err != nil || !found || revision2Claim.RevisionID != revision2ID {
		t.Fatalf("second revision processing claim=%+v found=%v err=%v", revision2Claim, found, err)
	}
	revision2, err = revision2.RecordScan(knowledgedomain.ScanClean, "clamav/1.4.3", "daily.cvd:27810", now.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision2, err = repository.SaveDocumentRevision(ctx, revision2, admittedRevision2.UpdatedAt, "scan_completed", knowledgeapp.Mutation{Actor: worker, CorrelationID: "e6000000-0000-4000-8000-000000000006", ReasonCode: "scan_completed", At: revision2.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	revision2Text := "Updated extracted operating plan"
	revision2, err = revision2.RecordExtraction(sha256.Sum256([]byte(revision2Text)), int64(len(revision2Text)), "spyglass/text-v1", "accounts/"+string(accountID)+"/documents/"+string(documentID)+"/revisions/"+string(revision2ID)+"/extracted/text", "extracted-version-2", now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	revision2, err = repository.SaveDocumentRevision(ctx, revision2, revision2.ScannedAt.UTC(), "extraction_completed", knowledgeapp.Mutation{Actor: worker, CorrelationID: "e7000000-0000-4000-8000-000000000007", ReasonCode: "extraction_completed", At: revision2.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	revision2BeforeIndex := revision2
	revision2, err = revision2.RecordIndex("knowledge-v1", 1, now.Add(8*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	chunk2, err := knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: "e8000000-0000-4000-8000-000000000008", AccountID: accountID, RevisionID: revision2ID, Index: 0, StartByte: 0, EndByte: int64(len(revision2Text)), Content: revision2Text, ContentSHA256: sha256.Sum256([]byte(revision2Text)), TokenCount: 4, IndexGeneration: "knowledge-v1", CreatedAt: revision2.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	revision2, err = repository.IndexDocumentRevision(ctx, revision2, revision2BeforeIndex.UpdatedAt, []knowledgedomain.DocumentChunk{chunk2}, knowledgeapp.Mutation{Actor: worker, CorrelationID: "e9000000-0000-4000-8000-000000000009", ReasonCode: "index_completed", At: revision2.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Complete(ctx, revision2Claim, now.Add(9*time.Second)); err != nil {
		t.Fatalf("complete second revision processing queue: %v", err)
	}
	published, err = repository.PublishDocumentRevision(ctx, accountID, documentID, revision2ID, published.Version, knowledgeapp.Mutation{Actor: worker, CorrelationID: "ea000000-0000-4000-8000-00000000000a", ReasonCode: "revision_published", At: now.Add(9 * time.Second)})
	if err != nil || published.CurrentRevisionID != revision2ID || published.CurrentRevision != 2 || published.Version != 3 {
		t.Fatalf("publish second revision=%+v err=%v", published, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_document_revisions SET object_key='changed' WHERE account_id=$1 AND id=$2`, accountID, revisionID); err == nil || !strings.Contains(err.Error(), "revision identity is immutable") {
		t.Fatalf("revision identity update=%v", err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_document_revisions SET extracted_object_version='changed' WHERE account_id=$1 AND id=$2`, accountID, revisionID); err == nil || !strings.Contains(err.Error(), "extracted object identity is immutable") {
		t.Fatalf("extracted object identity update=%v", err)
	}
	var unsafeEvents int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.knowledge_document_events WHERE account_id=$1 AND redacted_payload::text LIKE '%Operating plan%'`, accountID).Scan(&unsafeEvents); err != nil || unsafeEvents != 0 {
		t.Fatalf("unsafe document events=%d err=%v", unsafeEvents, err)
	}
	deleting, err := repository.RequestDocumentDeletion(ctx, accountID, documentID, 3, knowledgeapp.Mutation{Actor: actor, CorrelationID: "da000000-0000-4000-8000-00000000000a", ReasonCode: "deletion_requested", At: now.Add(10 * time.Second)})
	if err != nil || deleting.State != knowledgedomain.DocumentDeletionPending {
		t.Fatalf("request document deletion=%+v err=%v", deleting, err)
	}
	deletionQueue, err := postgresadapter.NewKnowledgeDocumentDeletionQueue(owner)
	if err != nil {
		t.Fatal(err)
	}
	deletionClaim, found, err := deletionQueue.Claim(ctx, "df000000-0000-4000-8000-00000000000f", now.Add(10*time.Second), 10*time.Minute)
	if err != nil || !found || deletionClaim.AccountID != accountID || deletionClaim.DocumentID != documentID || deletionClaim.Attempt != 1 {
		t.Fatalf("deletion claim=%+v found=%v err=%v", deletionClaim, found, err)
	}
	manifest, err := repository.LoadDeletionManifest(ctx, accountID, documentID)
	if err != nil || len(manifest.Objects) != 4 || manifest.Objects[0].Kind != "source" || manifest.Objects[1].Kind != "extracted" || manifest.Objects[2].RevisionID != revision2ID || manifest.Objects[3].RevisionID != revision2ID {
		t.Fatalf("deletion manifest=%+v err=%v", manifest, err)
	}
	if _, err := repository.LoadDeletionManifest(ctx, otherAccountID, documentID); !errors.Is(err, knowledgeapp.ErrNotFound) {
		t.Fatalf("cross-Account deletion manifest err=%v", err)
	}
	if err := deletionQueue.Complete(ctx, deletionClaim, now.Add(11*time.Second)); !errors.Is(err, knowledgeapp.ErrDocumentDeletionClaim) {
		t.Fatalf("deletion without receipts err=%v", err)
	}
	for _, object := range manifest.Objects {
		receipt := knowledgeapp.DocumentDeletionReceipt{AccountID: accountID, DocumentID: documentID, Object: object, DeletedAt: now.Add(11 * time.Second)}
		if err := repository.RecordDeletionReceipt(ctx, receipt); err != nil {
			t.Fatalf("record deletion receipt kind=%s: %v", object.Kind, err)
		}
		if err := repository.RecordDeletionReceipt(ctx, receipt); err != nil {
			t.Fatalf("replay deletion receipt kind=%s: %v", object.Kind, err)
		}
	}
	if err := deletionQueue.Complete(ctx, deletionClaim, now.Add(11*time.Second)); err != nil {
		t.Fatalf("complete document deletion: %v", err)
	}
	deletedDocument, err := repository.GetDocument(ctx, accountID, documentID)
	if err != nil || deletedDocument.State != knowledgedomain.DocumentDeleted || deletedDocument.Version != 5 || deletedDocument.DeletedAt == nil {
		t.Fatalf("deleted document=%+v err=%v", deletedDocument, err)
	}
	deletedRevision, err := repository.GetDocumentRevision(ctx, accountID, revisionID)
	if err != nil || deletedRevision.State != knowledgedomain.RevisionDeleted {
		t.Fatalf("deleted revision=%+v err=%v", deletedRevision, err)
	}
	if _, err := repository.GetDocumentCitation(ctx, accountID, documentID, revisionID, chunk.ID, true); !errors.Is(err, knowledgeapp.ErrNotFound) {
		t.Fatalf("deleted document citation err=%v", err)
	}
	var remainingChunks, deletionEvents int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.knowledge_document_chunks WHERE account_id=$1 AND revision_id=$2`, accountID, revisionID).Scan(&remainingChunks); err != nil || remainingChunks != 0 {
		t.Fatalf("remaining document chunks=%d err=%v", remainingChunks, err)
	}
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM spyglass.knowledge_document_events WHERE account_id=$1 AND document_id=$2 AND event_type='deletion_completed'`, accountID, documentID).Scan(&deletionEvents); err != nil || deletionEvents != 1 {
		t.Fatalf("document deletion events=%d err=%v", deletionEvents, err)
	}
	deletionStats, err := deletionQueue.Stats(ctx, now.Add(11*time.Second))
	if err != nil || deletionStats.Completed != 1 || deletionStats.Ready != 0 || deletionStats.DeadLetter != 0 {
		t.Fatalf("deletion stats=%+v err=%v", deletionStats, err)
	}
	if _, err := owner.Exec(ctx, `UPDATE spyglass.knowledge_document_deletion_receipts SET object_version='changed' WHERE account_id=$1 AND document_id=$2`, accountID, documentID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("deletion receipt update=%v", err)
	}
	deletedRevision2, err := repository.GetDocumentRevision(ctx, accountID, revision2ID)
	if err != nil || deletedRevision2.State != knowledgedomain.RevisionDeleted {
		t.Fatalf("deleted latest revision=%+v err=%v", deletedRevision2, err)
	}
	restoredRevisionID := ids.KnowledgeDocumentRevisionID("fa000000-0000-4000-8000-00000000000a")
	restoredAt := now.Add(11*time.Second + 100*time.Millisecond)
	restoredRevision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: restoredRevisionID, DocumentID: documentID, AccountID: accountID, Number: 3, Filename: "operating-plan-restored.md",
		DeclaredType: "text/markdown", VerifiedType: "text/markdown", ByteSize: 21,
		ContentSHA256: sha256.Sum256([]byte("restored source bytes")), ObjectKey: "accounts/" + string(accountID) +
			"/documents/" + string(documentID) + "/revisions/" + string(restoredRevisionID) + "/source",
		ObjectVersion: "version-restored", ChangeSummary: "Provider source restored", CreatedBy: worker,
	}, restoredAt)
	if err != nil {
		t.Fatal(err)
	}
	admittedRestored, err := repository.AdmitDocumentRevision(ctx, deletedDocument, restoredRevision,
		knowledgeapp.Mutation{Actor: worker, CorrelationID: "fb000000-0000-4000-8000-00000000000b", ReasonCode: "document_revision_admitted", At: restoredAt})
	if err != nil || admittedRestored.ID != restoredRevisionID || admittedRestored.Number != 3 {
		t.Fatalf("admit restored source revision=%+v err=%v", admittedRestored, err)
	}
	restoredClaim, found, err := queue.Claim(ctx, "fc000000-0000-4000-8000-00000000000c", now.Add(11*time.Second+200*time.Millisecond), 10*time.Minute)
	if err != nil || !found || restoredClaim.RevisionID != restoredRevisionID {
		t.Fatalf("restored revision processing claim=%+v found=%v err=%v", restoredClaim, found, err)
	}
	restoredRevision, err = restoredRevision.RecordScan(knowledgedomain.ScanClean, "clamav/1.4.3", "daily.cvd:27810", now.Add(11*time.Second+300*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	restoredRevision, err = repository.SaveDocumentRevision(ctx, restoredRevision, admittedRestored.UpdatedAt, "scan_completed",
		knowledgeapp.Mutation{Actor: worker, CorrelationID: "fd000000-0000-4000-8000-00000000000d", ReasonCode: "scan_completed", At: restoredRevision.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	restoredText := "Restored extracted operating plan"
	restoredRevision, err = restoredRevision.RecordExtraction(sha256.Sum256([]byte(restoredText)), int64(len(restoredText)), "spyglass/text-v1",
		"accounts/"+string(accountID)+"/documents/"+string(documentID)+"/revisions/"+string(restoredRevisionID)+"/extracted/text",
		"extracted-version-restored", now.Add(11*time.Second+400*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	restoredRevision, err = repository.SaveDocumentRevision(ctx, restoredRevision, restoredRevision.ScannedAt.UTC(), "extraction_completed",
		knowledgeapp.Mutation{Actor: worker, CorrelationID: "fe000000-0000-4000-8000-00000000000e", ReasonCode: "extraction_completed", At: restoredRevision.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	restoredBeforeIndex := restoredRevision
	restoredRevision, err = restoredRevision.RecordIndex("knowledge-v1", 1, now.Add(11*time.Second+500*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	restoredChunk, err := knowledgedomain.NewDocumentChunk(knowledgedomain.DocumentChunk{ID: "ff000000-0000-4000-8000-00000000000f",
		AccountID: accountID, RevisionID: restoredRevisionID, Index: 0, StartByte: 0, EndByte: int64(len(restoredText)),
		Content: restoredText, ContentSHA256: sha256.Sum256([]byte(restoredText)), TokenCount: 4,
		IndexGeneration: "knowledge-v1", CreatedAt: restoredRevision.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	restoredRevision, err = repository.IndexDocumentRevision(ctx, restoredRevision, restoredBeforeIndex.UpdatedAt,
		[]knowledgedomain.DocumentChunk{restoredChunk}, knowledgeapp.Mutation{Actor: worker,
			CorrelationID: "f1000000-0000-4000-8000-00000000000f", ReasonCode: "index_completed", At: restoredRevision.UpdatedAt})
	if err != nil || restoredRevision.State != knowledgedomain.RevisionReady {
		t.Fatalf("index restored revision=%+v err=%v", restoredRevision, err)
	}
	if err := queue.Complete(ctx, restoredClaim, now.Add(11*time.Second+600*time.Millisecond)); err != nil {
		t.Fatalf("complete restored revision processing: %v", err)
	}
	resurrected, err := repository.PublishDocumentRevision(ctx, accountID, documentID, restoredRevisionID, deletedDocument.Version,
		knowledgeapp.Mutation{Actor: worker, CorrelationID: "f2000000-0000-4000-8000-00000000000f", ReasonCode: "source_revision_published", At: now.Add(11*time.Second + 700*time.Millisecond)})
	if err != nil || resurrected.State != knowledgedomain.DocumentReady || resurrected.CurrentRevisionID != restoredRevisionID ||
		resurrected.CurrentRevision != 3 || resurrected.Version != 6 || resurrected.DeletionRequested != nil || resurrected.DeletedAt != nil {
		t.Fatalf("resurrected source document=%+v err=%v", resurrected, err)
	}
	if historical, historyErr := repository.GetDocumentRevision(ctx, accountID, revision2ID); historyErr != nil || historical.State != knowledgedomain.RevisionDeleted {
		t.Fatalf("resurrection changed deleted history=%+v err=%v", historical, historyErr)
	}
	failedDocumentID := ids.KnowledgeDocumentID("db000000-0000-4000-8000-00000000000b")
	failedRevisionID := ids.KnowledgeDocumentRevisionID("dc000000-0000-4000-8000-00000000000c")
	failedDocument, err := knowledgedomain.NewDocument(failedDocumentID, accountID, "Rejected source", knowledgedomain.SensitivityInternal, nil, actor, now.Add(12*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	failedRevision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: failedRevisionID, DocumentID: failedDocumentID, AccountID: accountID, Number: 1, Filename: "rejected.txt", DeclaredType: "text/plain", VerifiedType: "text/plain",
		ByteSize: 5, ContentSHA256: sha256.Sum256([]byte("eicar")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(failedDocumentID) + "/revisions/" + string(failedRevisionID) + "/source", ObjectVersion: "version-failed", ChangeSummary: "Rejected upload", CreatedBy: actor,
	}, now.Add(12*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.AdmitDocument(ctx, failedDocument, failedRevision, knowledgeapp.Mutation{Actor: actor, CorrelationID: "dd000000-0000-4000-8000-00000000000d", ReasonCode: "document_admitted", At: failedDocument.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	failedRevision, err = failedRevision.RecordScan(knowledgedomain.ScanInfected, "ClamAV 1.4.6", "Eicar-Signature", now.Add(13*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SaveDocumentRevision(ctx, failedRevision, failedDocument.CreatedAt, "processing_failed", knowledgeapp.Mutation{Actor: worker, CorrelationID: "de000000-0000-4000-8000-00000000000e", ReasonCode: "processing_failed", At: failedRevision.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	storedFailed, err := repository.GetDocument(ctx, accountID, failedDocumentID)
	if err != nil || storedFailed.State != knowledgedomain.DocumentFailed || storedFailed.Version != 2 {
		t.Fatalf("failed document=%+v err=%v", storedFailed, err)
	}
	recoveryRevisionID := ids.KnowledgeDocumentRevisionID("ed000000-0000-4000-8000-00000000000d")
	recoveryRevision, err := knowledgedomain.NewDocumentRevision(knowledgedomain.DocumentRevisionDraft{
		ID: recoveryRevisionID, DocumentID: failedDocumentID, AccountID: accountID, Number: 2, Filename: "recovered.txt", DeclaredType: "text/plain", VerifiedType: "text/plain",
		ByteSize: 9, ContentSHA256: sha256.Sum256([]byte("recovered")), ObjectKey: "accounts/" + string(accountID) + "/documents/" + string(failedDocumentID) + "/revisions/" + string(recoveryRevisionID) + "/source", ObjectVersion: "version-recovered", ChangeSummary: "Provider supplied a clean replacement", CreatedBy: worker,
	}, now.Add(14*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	admittedRecovery, err := repository.AdmitDocumentRevision(ctx, storedFailed, recoveryRevision, knowledgeapp.Mutation{Actor: worker, CorrelationID: "ee000000-0000-4000-8000-00000000000e", ReasonCode: "document_revision_admitted", At: recoveryRevision.CreatedAt})
	if err != nil || admittedRecovery.ID != recoveryRevisionID || admittedRecovery.Number != 2 {
		t.Fatalf("failed document recovery admission=%+v err=%v", admittedRecovery, err)
	}
}
