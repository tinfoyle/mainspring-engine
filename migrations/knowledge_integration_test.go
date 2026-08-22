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
		WHERE namespace_row.nspname='spyglass' AND table_row.relname LIKE 'knowledge_%' AND trigger_row.tgname='account_namespace_write_fence' AND NOT trigger_row.tgisinternal`).Scan(&fencedTables); err != nil || fencedTables != 10 {
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
	if _, err := repository.GetDocument(ctx, otherAccountID, documentID); !errors.Is(err, knowledgeapp.ErrNotFound) {
		t.Fatalf("cross-Account document read err=%v", err)
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
	published, err := repository.PublishDocumentRevision(ctx, accountID, documentID, revisionID, 1, knowledgeapp.Mutation{Actor: actor, CorrelationID: "d9000000-0000-4000-8000-000000000009", ReasonCode: "revision_published", At: now.Add(4 * time.Second)})
	if err != nil || published.State != knowledgedomain.DocumentReady || published.CurrentRevisionID != revisionID {
		t.Fatalf("publish document=%+v err=%v", published, err)
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
	deleting, err := repository.RequestDocumentDeletion(ctx, accountID, documentID, 2, knowledgeapp.Mutation{Actor: actor, CorrelationID: "da000000-0000-4000-8000-00000000000a", ReasonCode: "deletion_requested", At: now.Add(5 * time.Second)})
	if err != nil || deleting.State != knowledgedomain.DocumentDeletionPending {
		t.Fatalf("request document deletion=%+v err=%v", deleting, err)
	}
}
