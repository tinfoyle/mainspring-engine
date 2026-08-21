package migrations_test

import (
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tinfoyle/spyglass-engine/internal/platform/ids"
	"github.com/tinfoyle/spyglass-engine/migrations"
)

func TestAttentionPersistenceIsAccountIsolatedBoundedAndImmutable(t *testing.T) {
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
	now = now.UTC()
	accountA, accountB := "11000000-0000-4000-8000-000000000001", "12000000-0000-4000-8000-000000000002"
	seedAttentionPersistenceFixture(t, ctx, owner, accountA, "31000000-0000-4000-8000-000000000001", "41000000-0000-4000-8000-000000000001", "51000000-0000-4000-8000-000000000001", "61000000-0000-4000-8000-000000000001", "21000000-0000-4000-8000-000000000001", now)
	seedAttentionPersistenceFixture(t, ctx, owner, accountB, "32000000-0000-4000-8000-000000000002", "42000000-0000-4000-8000-000000000002", "52000000-0000-4000-8000-000000000002", "62000000-0000-4000-8000-000000000002", "22000000-0000-4000-8000-000000000002", now)

	role := "spyglass_attention_reader_" + randomSuffix(t)
	if _, err := owner.Exec(ctx, `CREATE ROLE `+role+` NOLOGIN NOBYPASSRLS;
		GRANT USAGE ON SCHEMA spyglass TO `+role+`;
		GRANT SELECT,INSERT,UPDATE,DELETE ON spyglass.attention_information_requests,spyglass.attention_work_reviews,
			spyglass.attention_consequential_approvals,spyglass.attention_events TO `+role); err != nil {
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
		var information, reviews, approvals, events int
		if err := reader.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM spyglass.attention_information_requests),
			(SELECT count(*) FROM spyglass.attention_work_reviews),
			(SELECT count(*) FROM spyglass.attention_consequential_approvals),
			(SELECT count(*) FROM spyglass.attention_events)`).Scan(&information, &reviews, &approvals, &events); err != nil || information != 1 || reviews != 1 || approvals != 1 || events != 3 {
			t.Fatalf("Account %s attention counts information=%d reviews=%d approvals=%d events=%d err=%v", accountID, information, reviews, approvals, events, err)
		}
	}
	if _, err := reader.Exec(ctx, `INSERT INTO spyglass.attention_information_requests
		(account_id,id,parent_work_item_id,fact_key,scope_kind,question,requested_by_kind,requested_by_id,state,version,created_at,updated_at)
		VALUES ($1,'71000000-0000-4000-8000-000000000007','21000000-0000-4000-8000-000000000001','company.name','account','Cross Account','workload','test','open',1,$2,$2)`, accountA, now); err == nil {
		t.Fatal("cross-Account Attention insert bypassed RLS")
	}
	if _, err := reader.Exec(ctx, `SELECT set_config('app.account_id',$1,false)`, accountA); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, `UPDATE spyglass.attention_events SET reason='changed'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("Attention event update = %v", err)
	}
	if _, err := reader.Exec(ctx, `DELETE FROM spyglass.attention_events`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("Attention event delete = %v", err)
	}

	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.attention_information_requests
		(account_id,id,parent_work_item_id,fact_key,scope_kind,scope_work_item_id,question,requested_by_kind,requested_by_id,state,version,created_at,updated_at)
		VALUES ($1,'72000000-0000-4000-8000-000000000007',$2,'company.name','work_item',$3,'Wrong scope','workload','test','open',1,$4,$4)`, accountA, "21000000-0000-4000-8000-000000000001", "22000000-0000-4000-8000-000000000002", now); err == nil {
		t.Fatal("cross-Account fact scope bypassed composite foreign key")
	}
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,
		 policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
		VALUES ($1,'73000000-0000-4000-8000-000000000007','74000000-0000-4000-8000-000000000007',$2,'email_send',convert_to('{}','UTF8'),
		 decode(repeat('11',32),'hex'),1,decode(repeat('22',32),'hex'),'workload','test',1,false,$3::timestamptz+interval '10 minutes','open',1,$3,$3)`, accountA, "61000000-0000-4000-8000-000000000001", now); err == nil {
		t.Fatal("runner-incompatible capability bypassed persistence constraint")
	}
	var fencedTables int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM pg_trigger trigger_row JOIN pg_class table_row ON table_row.oid=trigger_row.tgrelid
		JOIN pg_namespace namespace_row ON namespace_row.oid=table_row.relnamespace
		WHERE namespace_row.nspname='spyglass' AND table_row.relname LIKE 'attention_%' AND trigger_row.tgname='account_namespace_write_fence' AND NOT trigger_row.tgisinternal`).Scan(&fencedTables); err != nil || fencedTables != 4 {
		t.Fatalf("Attention movement write fences=%d err=%v", fencedTables, err)
	}
}

func seedAttentionPersistenceFixture(t *testing.T, ctx context.Context, owner *pgxpool.Pool, accountID, boardroomID, conversationID, runID, invocationID, workItemID string, now time.Time) {
	t.Helper()
	seedAgentProjectionFixture(t, ctx, owner, accountID, boardroomID, conversationID, runID, invocationID, []byte(strings.Repeat("3", 32)), "completed", now)
	capacityID, _ := ids.Derive(workItemID, "attention-capacity")
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.work_item_number_counters(account_id,next_number) VALUES ($1,2);
		INSERT INTO spyglass.work_items
		(account_id,id,number,depth,kind,title,description,state,priority,responsibility,source,created_by_actor_kind,created_by_actor_id,capacity_reservation_id,version,created_at,updated_at)
		VALUES ($1,$2,1,0,'todo','Attention fixture','Bound persistence fixture','open','normal','shared','manual','workload','attention-test',$3,1,$4,$4)`,
		pgx.QueryExecModeSimpleProtocol, accountID, workItemID, capacityID, now); err != nil {
		t.Fatal(err)
	}
	informationID, _ := ids.Derive(workItemID, "attention-information")
	reviewID, _ := ids.Derive(workItemID, "attention-review")
	approvalID, _ := ids.Derive(workItemID, "attention-approval")
	operationID, _ := ids.Derive(workItemID, "attention-operation")
	reviewerID, _ := ids.Derive(workItemID, "attention-reviewer")
	informationEventID, _ := ids.Derive(workItemID, "attention-information-created")
	reviewEventID, _ := ids.Derive(workItemID, "attention-review-created")
	approvalEventID, _ := ids.Derive(workItemID, "attention-approval-created")
	payloadDigest := sha256.Sum256([]byte(`{"message":"hello"}`))
	if _, err := owner.Exec(ctx, `INSERT INTO spyglass.attention_information_requests
		(account_id,id,parent_work_item_id,fact_key,scope_kind,question,requested_by_kind,requested_by_id,state,version,created_at,updated_at)
		VALUES ($1,$3,$2,'company.legal_name','account','What is the company name?','workload','attention-test','open',1,$9,$9);
		INSERT INTO spyglass.attention_work_reviews
		(account_id,id,work_item_id,work_version,proposal_sha256,question,requested_by_kind,requested_by_id,reviewer_user_id,state,version,created_at,updated_at)
		VALUES ($1,$4,$2,1,decode(repeat('44',32),'hex'),'Is the result ready?','workload','attention-test',$8,'open',1,$9,$9);
		INSERT INTO spyglass.attention_consequential_approvals
		(account_id,id,operation_id,invocation_id,work_item_id,capability,canonical_payload,input_sha256,hash_version,evidence_sha256,proposer_kind,proposer_id,
		 policy_version,require_independent_review,expires_at,state,version,created_at,updated_at)
		VALUES ($1,$5,$6,$7,$2,'email.send',convert_to('{"message":"hello"}','UTF8'),$13,1,decode(repeat('66',32),'hex'),
		 'workload','attention-test',1,true,$9::timestamptz+interval '30 minutes','open',1,$9,$9);
		INSERT INTO spyglass.attention_events(account_id,id,aggregate_kind,information_request_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$10,'information_request',$3,'information_requested',0,1,'workload','attention-test','','fixture','{"state":"open"}',$9);
		INSERT INTO spyglass.attention_events(account_id,id,aggregate_kind,work_review_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$11,'work_review',$4,'review_requested',0,1,'workload','attention-test','','fixture','{"state":"open"}',$9);
		INSERT INTO spyglass.attention_events(account_id,id,aggregate_kind,consequential_approval_id,event_type,from_version,to_version,actor_kind,actor_id,reason,correlation_id,redacted_payload,occurred_at)
		VALUES ($1,$12,'consequential_approval',$5,'approval_requested',0,1,'workload','attention-test','','fixture','{"state":"open"}',$9)`,
		pgx.QueryExecModeSimpleProtocol, accountID, workItemID, informationID, reviewID, approvalID, operationID, invocationID, reviewerID, now, informationEventID, reviewEventID, approvalEventID, payloadDigest[:]); err != nil {
		t.Fatal(err)
	}
}
